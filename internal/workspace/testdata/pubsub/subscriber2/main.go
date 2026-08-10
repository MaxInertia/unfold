package main

import "example.com/shared"

// A second service listening to the same event. One subscriber is the easy
// case; the interesting one is that neither subscriber is "the" subscriber.
func consumeToo(c *shared.Client) {
	_ = c.Subscribe(shared.FooEventDefn, handleFooAgain)
}

func handleFooAgain(payload []byte) error { return nil }

func main() { consumeToo(&shared.Client{}) }
