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

	"github.com/MaxInertia/unfold/internal/engine"
	"github.com/MaxInertia/unfold/internal/server"
)

func main() {
	var (
		addr   = flag.String("addr", "127.0.0.1:0", "address to bind (default: random free port on localhost)")
		noOpen = flag.Bool("no-open", false, "don't open the browser")
		dir    = flag.String("dir", "", "project directory to load from (default: cwd)")
		lang   = flag.String("lang", "", "force engine language: go|typescript (default: autodetect)")
		watch  = flag.Bool("watch", true, "reindex automatically when source files change")
	)
	flag.Parse()

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

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", listener.Addr().String())

	srv := server.New(eng)
	srv.SetTarget(target)
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
