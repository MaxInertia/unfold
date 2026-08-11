package main

import "example.com/shared"

func consume(c *shared.Client) {
	_ = c.Subscribe(shared.FooEventDefn, handleFoo)
	_ = c.Subscribe(shared.BarEventDefn, handleBar)
}

func handleBar(payload []byte) error { return nil }

// handleFoo calls on, so a reader who splices it in from the publishing side
// has somewhere further to go — which is the whole point of arriving here.
func handleFoo(payload []byte) error {
	return persist(payload)
}

func persist(payload []byte) error { return nil }

func main() {
	consume(&shared.Client{})
}
