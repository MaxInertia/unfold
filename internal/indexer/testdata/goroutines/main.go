// Package main is a fixture for indexer tests: it exercises goroutine
// launch detection (`go f()`) alongside an ordinary blocking call.
package main

func worker() {}

func blocking() {}

func launch() {
	go worker() // launched asynchronously
	blocking()  // ordinary blocking call
}

func main() {
	launch()
}
