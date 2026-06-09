// Package main is a fixture for indexer tests: it exercises goroutine
// launch detection (`go f()`) alongside an ordinary blocking call, a
// deferred call (which must not be flagged), and an anonymous goroutine
// (which has no named call site to badge).
package main

func worker() {}

func blocking() {}

func cleanup() {}

func launch() {
	go worker()     // launched asynchronously — flagged
	blocking()      // ordinary blocking call — not flagged
	defer cleanup() // deferred — must NOT be flagged
	go func() {     // anonymous goroutine — no named call site
		_ = 0
	}()
}

func main() {
	launch()
}
