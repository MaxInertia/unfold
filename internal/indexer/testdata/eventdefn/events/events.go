// Package events declares the events this platform publishes. Each one is a
// package-level var of a struct type, and both ends name the var rather than
// the string inside it.
package events

import "example.com/eventdefn/eventdefinition"

var FooEventDefn = eventdefinition.EventDefinition{
	ID:          "foo-happened",
	Description: "a foo happened",
}

var BarEventDefn = eventdefinition.EventDefinition{
	ID:          "bar-happened",
	Description: "a bar happened",
}
