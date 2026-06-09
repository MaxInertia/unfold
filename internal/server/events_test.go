package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MaxInertia/unfold/internal/model"
)

// stubEngine is a no-op model.Engine; the SSE endpoint never touches it.
type stubEngine struct{}

func (stubEngine) LookupSymbol(string) (model.TargetID, error) { return "", nil }
func (stubEngine) Frame(model.TargetID) (*model.Frame, error)  { return nil, nil }
func (stubEngine) FrameForCall(model.CallID, int) (*model.Frame, error) {
	return nil, nil
}
func (stubEngine) Search(string, int) []model.SearchResult { return nil }
func (stubEngine) Files() []string                         { return nil }
func (stubEngine) TypeInfo(model.TargetID, int) (*model.TypeInfo, error) {
	return nil, nil
}

// TestEventsReload connects to /api/events and asserts that NotifyReload
// pushes a "reload" event to the subscriber.
func TestEventsReload(t *testing.T) {
	srv := New(stubEngine{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type: got %q want text/event-stream", ct)
	}

	// Stream lines off the body in the background so reads can't block the test.
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	// Wait until the handler has registered our client, then notify.
	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.mu.Lock()
		n := len(srv.clients)
		srv.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client never registered with the server")
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.NotifyReload()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before reload event arrived")
			}
			if line == "event: reload" {
				return // success
			}
		case <-timeout:
			t.Fatal("did not receive reload event within timeout")
		}
	}
}
