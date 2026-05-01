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

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/server"
)

func main() {
	var (
		addr   = flag.String("addr", "127.0.0.1:0", "address to bind (default: random free port on localhost)")
		noOpen = flag.Bool("no-open", false, "don't open the browser")
	)
	flag.Parse()

	target := flag.Arg(0)
	if target == "" {
		target = "./..."
	}

	idx := indexer.New()
	if err := idx.Load(target); err != nil {
		log.Fatalf("indexer load failed: %v", err)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", listener.Addr().String())

	srv := server.New(idx)
	srv.SetTarget(target)
	httpServer := &http.Server{Handler: srv.Handler()}

	serverErr := make(chan error, 1)
	go func() { serverErr <- httpServer.Serve(listener) }()

	log.Printf("unfold listening on %s (target: %s)", url, target)

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
