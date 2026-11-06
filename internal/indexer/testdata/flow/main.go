// Package main is a fixture for the follow-a-value tests: a value handed into
// a callee, received back from one, shadowed in an inner scope, and read
// through a struct field.
package main

import "fmt"

type Config struct {
	Name  string
	Count int
}

// Run is the function most tests follow values through. The outer err and
// the inner err are different objects with the same spelling.
func Run(cfg Config, name string) (string, error) {
	greeting, err := greet(name, cfg.Count)
	if err != nil {
		return "", err
	}
	if cfg.Count > 1 {
		err := validate(greeting)
		if err != nil {
			fmt.Println(err)
		}
	}
	out := decorate(greeting)
	return out, nil
}

func greet(who string, n int) (string, error) {
	if n < 0 {
		return "", fmt.Errorf("bad count %d", n)
	}
	msg := "hello " + who
	return msg, nil
}

func validate(s string) error {
	if s == "" {
		return fmt.Errorf("empty")
	}
	return nil
}

func decorate(s string) (out string) {
	out = "*" + s + "*"
	return
}

func main() {
	cfg := Config{Name: "x", Count: 2}
	Run(cfg, cfg.Name)
}
