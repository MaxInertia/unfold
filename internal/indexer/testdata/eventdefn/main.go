package main

import (
	"context"

	"example.com/eventdefn/events"
	"example.com/eventdefn/gosdk"
)

func publish(ctx context.Context, c *gosdk.Client) {
	_ = c.Emit(ctx, events.FooEventDefn, nil)
	_ = c.Emit(ctx, events.BarEventDefn, nil)
}

func consume(c *gosdk.Client) {
	_ = c.Subscribe(events.FooEventDefn, handleFoo)
}

func handleFoo(payload []byte) error { return nil }

func main() {
	c := &gosdk.Client{}
	publish(context.Background(), c)
	consume(c)
}
