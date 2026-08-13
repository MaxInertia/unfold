package main

import "example.com/shared"

func publish(c *shared.Client) {
	_ = c.Emit(shared.FooEventDefn, nil)
}

func publishBar(c *shared.Client) {
	_ = c.Emit(shared.BarEventDefn, nil)
}

func publishBaz(c *shared.Client) {
	_ = c.Emit(shared.BazEventDefn, nil)
}

func publishQux(c *shared.Client) {
	_ = c.Emit(shared.QuxEventDefn, nil)
}

func main() {
	publish(&shared.Client{})
	publishBar(&shared.Client{})
	publishBaz(&shared.Client{})
	publishQux(&shared.Client{})
}
