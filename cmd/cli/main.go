package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/MaxInertia/unfold/internal/diff"
	"github.com/MaxInertia/unfold/internal/engine"
	"github.com/MaxInertia/unfold/internal/gitbase"
	"github.com/MaxInertia/unfold/internal/notes"
	"github.com/MaxInertia/unfold/internal/prefs"
	"github.com/MaxInertia/unfold/internal/server"
	"github.com/MaxInertia/unfold/internal/workspace"
)

// projectDir resolves the --dir flag to the directory per-project state is
// stored under; empty means the working directory.
func projectDir(dir string) string {
	if dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func main() {
	var (
		addr         = flag.String("addr", "127.0.0.1:0", "address to bind (default: random free port on localhost)")
		noOpen       = flag.Bool("no-open", false, "don't open the browser")
		dir          = flag.String("dir", "", "project directory to load from (default: cwd)")
		lang         = flag.String("lang", "", "force engine language: go|typescript (default: autodetect)")
		watch        = flag.Bool("watch", true, "reindex automatically when source files change")
		diffBase     = flag.String("diff-base", "", "git ref to diff against (e.g. main); frames show what this branch changes vs the merge-base. Go only.")
		workspaceDir = flag.String("workspace", "", "directory of sibling repository checkouts to open together, so cross-service calls can be followed into the repo that implements them")
		indexMode    = flag.String("index", "auto", "when to index each workspace repo's Go code: eager|lazy|auto (auto indexes up front for a small workspace, on demand for a large one)")
		protoRoot    = flag.String("proto-root", "", "directory of the shared proto repository that microservice.yaml protoPaths are relative to; enables the declared gRPC surface in the service view")
	)
	flag.Parse()

	engine.Workspace = *workspaceDir
	switch engine.IndexMode = workspace.Mode(*indexMode); engine.IndexMode {
	case workspace.ModeEager, workspace.ModeLazy, workspace.ModeAuto:
	default:
		log.Fatalf("--index %q: want eager, lazy, or auto", *indexMode)
	}

	// Set before the first Load, and read again on every watch-mode rebuild.
	// Without the flag, fall back to whatever was picked in the UI last time.
	saved := prefs.Load(projectDir(*dir))
	engine.ProtoRoot = *protoRoot
	if engine.ProtoRoot == "" {
		engine.ProtoRoot = saved.ProtoRoot
	}
	// Repos linked from the UI in a previous session. Restoring them here is
	// what makes the link stick: without it, opening a workspace would be
	// something you had to redo on every start.
	engine.LinkedRepos = append([]string(nil), saved.LinkedRepos...)

	target := flag.Arg(0)
	if target == "" {
		target = "./..."
	}

	detected, err := engine.Detect(*dir, *lang)
	if err != nil {
		log.Fatalf("%v", err)
	}
	eng, err := engine.NewReloadable(detected, *dir, target)
	if err != nil {
		log.Fatalf("%s engine load failed: %v", detected, err)
	}
	defer eng.Close()

	// Optional diff mode: index the merge-base with --diff-base in a throwaway
	// worktree and annotate frames with what this branch changes.
	var differ *diff.Differ
	if *diffBase != "" {
		if detected != engine.LangGo {
			log.Printf("diff mode is Go-only for now; ignoring --diff-base for %s", detected)
		} else if commit, err := gitbase.MergeBase(*dir, *diffBase); err != nil {
			log.Fatalf("--diff-base %q: %v", *diffBase, err)
		} else if baseDir, cleanup, err := gitbase.AddWorktree(*dir, commit); err != nil {
			log.Fatalf("diff base worktree: %v", err)
		} else {
			defer cleanup()
			baseEng, err := engine.Load(detected, baseDir, target)
			if err != nil {
				log.Fatalf("diff base index failed: %v", err)
			}
			differ = diff.New(baseEng)
			log.Printf("diff mode: comparing against %.12s (merge-base with %s)", commit, *diffBase)
		}
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", listener.Addr().String())

	srv := server.New(eng)
	srv.SetTarget(target)
	srv.SetDiffer(differ)
	srv.SetProjectDir(projectDir(*dir))
	// Notes persist to <dir>/.unfold/notes.json (created on first save).
	srv.SetNotes(notes.NewStore(*dir))
	// Linking a repo changes which repositories the engine opens, and that
	// only takes effect on a rebuild. Reload is the same path watch mode
	// uses — it swaps atomically and keeps the previous engine on failure.
	srv.SetReloader(eng.Reload)
	httpServer := &http.Server{Handler: srv.Handler()}

	serverErr := make(chan error, 1)
	go func() { serverErr <- httpServer.Serve(listener) }()

	log.Printf("unfold listening on %s (target: %s)", url, target)

	// Watch mode: reindex on source changes and push a reload to the browser.
	if *watch {
		w, err := engine.NewWatcher(*dir, 250*time.Millisecond, func() {
			log.Printf("change detected, reindexing...")
			if err := eng.Reload(); err != nil {
				log.Printf("reindex failed (keeping previous index): %v", err)
				return
			}
			log.Printf("reindex complete")
			srv.NotifyReload()
		})
		if err != nil {
			log.Printf("watch disabled: %v", err)
		} else {
			defer w.Close()
		}
	}

	if !*noOpen {
		if err := openBrowser(url); err != nil {
			log.Printf("open browser: %v", err)
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatalf("server: %v", err)
	case sig := <-sigs:
		log.Printf("got %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}
	return cmd.Start()
}
