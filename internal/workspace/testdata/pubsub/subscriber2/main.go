package main

import "example.com/shared"

// A second service listening to the same event. One subscriber is the easy
// case; the interesting one is that neither subscriber is "the" subscriber.
func consumeToo(c *shared.Client) {
	_ = c.With("retries").Subscribe(shared.FooEventDefn, handleFooAgain)
}

func handleFooAgain(payload []byte) error { return nil }

// Two registrations of the same event in one service — one file, even. Both
// run; neither is "the" subscriber.
func consumeTwice(c *shared.Client) {
	_ = c.Subscribe(shared.QuxEventDefn, handleQuxOne)
	_ = c.Subscribe(shared.QuxEventDefn, handleQuxTwo)
}

func handleQuxOne(payload []byte) error { return nil }
func handleQuxTwo(payload []byte) error { return nil }

// A handler reached through an interface: the registration names
// deps.Handler.Handle, and what actually runs is whichever implementation was
// injected.
type QuuxHandler interface {
	Handle(payload []byte) error
}

type quuxLogger struct{}

func (quuxLogger) Handle(payload []byte) error { return nil }

type deps struct{ Handler QuuxHandler }

var wiring = deps{Handler: quuxLogger{}}

func consumeViaInterface(c *shared.Client) {
	_ = c.Subscribe(shared.QuuxEventDefn, wiring.Handler.Handle)
}

// The handler written where it is registered, rather than named elsewhere.
func consumeInline(c *shared.Client) {
	_ = c.Subscribe(shared.BazEventDefn, func(payload []byte) error {
		return nil
	})
}

func main() {
	consumeToo(&shared.Client{})
	consumeInline(&shared.Client{})
	consumeTwice(&shared.Client{})
	consumeViaInterface(&shared.Client{})
}
