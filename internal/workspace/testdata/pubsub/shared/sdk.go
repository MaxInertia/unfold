// Package shared stands in for the in-house SDK and the events it defines:
// one dependency both services import, which is where the join comes from —
// neither service names the other, and neither writes the key as a string.
package shared

type EventDefinition struct {
	ID          string
	Description string
}

var FooEventDefn = EventDefinition{
	ID:          "foo-happened",
	Description: "a foo happened",
}

// Bar has exactly one publisher and one subscriber, so the singular case stays
// covered alongside the one with several of each.
var BarEventDefn = EventDefinition{
	ID:          "bar-happened",
	Description: "a bar happened",
}

type Client struct{}

// With returns the client again, so a registration can be written as a chain —
// two calls on one line, which is the shape that made a line too coarse a key
// for a decision about a call.
func (c *Client) With(opt string) *Client { return c }

func (c *Client) Emit(def EventDefinition, payload []byte) error { return nil }

func (c *Client) Subscribe(def EventDefinition, handler func([]byte) error) error { return nil }
