// Package main is a fixture for indexer tests: a small DI-style program
// with one interface and two concrete implementations.
package main

import "fmt"

type Greeter interface {
	Greet(name string) string
}

type English struct{}

func (English) Greet(name string) string { return "Hello, " + name }

type French struct{}

func (French) Greet(name string) string { return "Bonjour, " + name }

func RunGreeter(g Greeter, name string) {
	msg := g.Greet(name)
	fmt.Println(msg)
}

func main() {
	RunGreeter(English{}, "world")
	RunGreeter(French{}, "monde")
}
