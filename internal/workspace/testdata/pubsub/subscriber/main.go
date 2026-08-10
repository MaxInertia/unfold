package main

import "example.com/shared"

func consume(c *shared.Client) {
	_ = c.Subscribe(shared.FooEventDefn, handleFoo)
}

func handleFoo(payload []byte) error { return nil }

func main() {
	consume(&shared.Client{})
}
