// Package main is a fixture for the side-effect detector: direct effects of
// every shipped kind, read and write where the distinction exists, a function
// that inherits them, a call chain long enough to cross the depth cap, a
// function with no effects at all, and an in-house client a user rule names.
package main

import (
	"database/sql"
	"net/http"
	"os"
	"os/exec"
	"time"
)

var counter int

var db *sql.DB

// readConfig reads the environment and a file: two direct read effects, each
// with a static key.
func readConfig() string {
	name := os.Getenv("APP_NAME")
	data, _ := os.ReadFile("/etc/app.conf")
	return name + string(data)
}

// writeState writes a file and a package-level variable.
func writeState() {
	_ = os.WriteFile("/tmp/state", nil, 0o644)
	counter++
}

func query() {
	rows, _ := db.Query("SELECT 1")
	_ = rows
}

func mutate() {
	_, _ = db.Exec("DELETE FROM t")
}

// launch has only statement-shaped effects plus a sleep.
func launch(ch chan int) {
	go query()
	ch <- 1
	time.Sleep(time.Second)
}

func shell() {
	_ = exec.Command("ls").Run()
	resp, _ := http.Get("http://example.com")
	_ = resp
}

// pure touches nothing outside its stack.
func pure(a, b int) int { return a + b }

// handler inherits readConfig's and writeState's effects at depth 1, and
// nothing from pure.
func handler() {
	_ = readConfig()
	writeState()
	_ = pure(1, 2)
}

// l0 … l5 is a chain whose only effect sits five calls down — one past the
// cap, from l0; exactly at it, from l1.
func l0() { l1() }
func l1() { l2() }
func l2() { l3() }
func l3() { l4() }
func l4() { l5() }
func l5() { _ = os.Remove("/tmp/x") }

// cache is the in-house client a user rule declares to be a database.
type cache struct{}

func (c *cache) Put(k, v string) {}

func useCache() { (&cache{}).Put("k", "v") }

func main() {
	handler()
	launch(make(chan int))
	shell()
	l0()
	useCache()
	mutate()
	query()
}
