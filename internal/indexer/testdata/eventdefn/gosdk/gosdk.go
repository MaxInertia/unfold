// Package gosdk stands in for the in-house SDK: both ends of the pubsub
// system take the event definition itself, so neither call site mentions the
// string that joins them.
package gosdk

import (
	"context"

	"example.com/eventdefn/eventdefinition"
)

type Client struct{}

func (c *Client) Emit(ctx context.Context, def eventdefinition.EventDefinition, payload []byte) error {
	return nil
}

func (c *Client) Subscribe(def eventdefinition.EventDefinition, handler func([]byte) error) error {
	return nil
}
