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

type Client struct{}

func (c *Client) Emit(def EventDefinition, payload []byte) error { return nil }

func (c *Client) Subscribe(def EventDefinition, handler func([]byte) error) error { return nil }
