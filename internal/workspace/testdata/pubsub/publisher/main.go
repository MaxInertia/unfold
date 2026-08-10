package main

import "example.com/shared"

func publish(c *shared.Client) {
	_ = c.Emit(shared.FooEventDefn, nil)
}

func main() {
	publish(&shared.Client{})
}
