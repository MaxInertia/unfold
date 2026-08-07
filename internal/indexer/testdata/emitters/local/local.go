// Package local declares its own Emit, unrelated to the SDK. It exists so a
// rule that matches on the method name alone can be shown matching it.
package local

import "context"

type Metric struct{ Name string }

type Recorder interface {
	Emit(ctx context.Context, topic string, m *Metric) error
}
