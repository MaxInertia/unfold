package main

import "example.com/shared"

// A second service emitting the same event, so a subscriber asking "where does
// this come from" has more than one answer.
func publishAgain(c *shared.Client) {
	_ = c.Emit(shared.FooEventDefn, nil)
}

func main() { publishAgain(&shared.Client{}) }
