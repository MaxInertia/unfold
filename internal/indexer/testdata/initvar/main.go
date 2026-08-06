// Package main is a command whose real work hangs off package-level
// variables. This is the cobra shape, with the dependency stubbed out: the
// calls that matter are inside a composite literal assigned to a var, not
// inside any function declaration.
package main

import "net/http"

// Command stands in for cobra.Command — a struct holding callbacks.
type Command struct {
	Use  string
	RunE func() error
}

func (c *Command) Execute() error { return c.RunE() }

// syncCmd is the whole point. The call to the orders API lives in a closure
// held by a package-level var, so nothing in any FuncDecl contains it.
var syncCmd = &Command{
	Use: "sync",
	RunE: func() error {
		return pullOrders()
	},
}

// registry is a var whose initializer calls a function directly, rather than
// wrapping it in a closure. Both shapes have to be indexed.
var registry = register(newBackend())

// plain has no call in its initializer, so it should not become a frame —
// otherwise the index fills with variables that aren't code to read.
var plain = 42

func pullOrders() error {
	_, err := http.Get("https://orders.internal/v1/orders")
	return err
}

func newBackend() *Command { return &Command{Use: "backend"} }

func register(c *Command) string { return c.Use }

func main() {
	_ = syncCmd.Execute()
	_ = registry
	_ = plain
}
