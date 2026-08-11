// Package eventdefinition holds the shape an event is described with. The
// identity of an event is a field of that struct, not a bare string, which is
// what makes this shape different from a topic name.
package eventdefinition

type EventDefinition struct {
	ID          string
	Description string
}
