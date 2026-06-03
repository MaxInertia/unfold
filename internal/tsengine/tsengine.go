// Package tsengine implements model.Engine for TypeScript projects by
// driving the unfold-tsindexer sidecar (Bun + ts-morph) over a
// newline-delimited JSON-RPC stdio protocol. The sidecar owns all
// TypeScript analysis and emits the same Frame JSON the Go engine does, so
// the server and frontend are unchanged.
package tsengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MaxInertia/unfold/internal/model"
)

// Engine is a client for the TypeScript sidecar process.
type Engine struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	nextID int
}

var _ model.Engine = (*Engine)(nil)

type rpcRequest struct {
	ID     int            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// Load spawns the sidecar and loads the TypeScript project rooted at dir
// (defaults to the working directory). target is accepted for interface
// symmetry with the Go engine but ignored — the sidecar loads the whole
// tsconfig project.
func Load(dir, _ string) (*Engine, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	cmd, err := sidecarCommand()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr // sidecar logs go straight through

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tsindexer: %w", err)
	}

	e := &Engine{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20)}

	var loadRes struct {
		Funcs int `json:"funcs"`
	}
	if err := e.call("load", map[string]any{"dir": abs}, &loadRes); err != nil {
		_ = e.Close()
		return nil, fmt.Errorf("tsindexer load: %w", err)
	}
	return e, nil
}

// sidecarCommand resolves how to launch the sidecar:
//  1. $UNFOLD_TSINDEXER — a path; a ".ts" file is run via `bun run`,
//     anything else is exec'd directly.
//  2. an "unfold-tsindexer" binary sitting next to the unfold executable.
//
// It returns an unstarted *exec.Cmd.
func sidecarCommand() (*exec.Cmd, error) {
	if v := os.Getenv("UNFOLD_TSINDEXER"); v != "" {
		if strings.HasSuffix(v, ".ts") {
			return exec.Command("bun", "run", v), nil
		}
		return exec.Command(v), nil
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "unfold-tsindexer")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return exec.Command(cand), nil
		}
	}
	return nil, fmt.Errorf("tsindexer sidecar not found: set UNFOLD_TSINDEXER to tsindexer/main.ts " +
		"or build the sidecar (make build-tsindexer)")
}

// call sends one request and decodes the matching response. The sidecar is
// strictly request/response and single-threaded, so the mutex serialises
// callers and the next line out is always our answer.
func (e *Engine) call(method string, params map[string]any, out any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.nextID++
	id := e.nextID
	req := rpcRequest{ID: id, Method: method, Params: params}
	buf, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := e.stdin.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	line, err := e.out.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func (e *Engine) LookupSymbol(name string) (model.TargetID, error) {
	var res struct {
		TargetID model.TargetID `json:"targetId"`
	}
	if err := e.call("lookupSymbol", map[string]any{"name": name}, &res); err != nil {
		return "", err
	}
	return res.TargetID, nil
}

func (e *Engine) Frame(id model.TargetID) (*model.Frame, error) {
	var f model.Frame
	if err := e.call("frame", map[string]any{"targetId": string(id)}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (e *Engine) FrameForCall(id model.CallID, choice int) (*model.Frame, error) {
	var f model.Frame
	if err := e.call("frameForCall", map[string]any{"callId": string(id), "choice": choice}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (e *Engine) Search(query string, limit int) []model.SearchResult {
	var res struct {
		Results []model.SearchResult `json:"results"`
	}
	if err := e.call("search", map[string]any{"query": query, "limit": limit}, &res); err != nil {
		return nil
	}
	return res.Results
}

// Close shuts the sidecar down.
func (e *Engine) Close() error {
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
	}
	return nil
}
