// Package sdk stands in for a vendored event-broker SDK: it defines the
// interface a service calls through, and the payload type that crosses it.
package sdk

import "context"

// Event is the type that identifies this library at a call site. The
// interface below can be re-declared by anyone; the payload type cannot.
type Event struct {
	Name string
}

// Emitter is the SDK's interface. A repo is free to declare its own with the
// same method — see ../local — which is exactly why matching on the
// receiver's package identifies the declaration site rather than the library.
type Emitter interface {
	Emit(ctx context.Context, topic string, e *Event) error
}
