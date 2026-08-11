package main

import "example.com/shared"

func publish(c *shared.Client) {
	_ = c.Emit(shared.FooEventDefn, nil)
}

func publishBar(c *shared.Client) {
	_ = c.Emit(shared.BarEventDefn, nil)
}

func main() {
	publish(&shared.Client{})
	publishBar(&shared.Client{})
}
