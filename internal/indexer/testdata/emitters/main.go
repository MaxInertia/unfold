// Package main calls two different Emit methods: one carrying the SDK's event
// type, one carrying a local metric. A rule for the event broker must claim
// the first and not the second.
package main

import (
	"context"

	"example.com/emitters/local"
	"example.com/emitters/sdk"
)

type Server struct {
	events  sdk.Emitter
	metrics local.Recorder
}

func (s *Server) publish(ctx context.Context) error {
	return s.events.Emit(ctx, "orders-v1", &sdk.Event{Name: "created"})
}

func (s *Server) record(ctx context.Context) error {
	return s.metrics.Emit(ctx, "latency-ms", &local.Metric{Name: "p99"})
}

func main() {
	s := &Server{}
	_ = s.publish(context.Background())
	_ = s.record(context.Background())
}
