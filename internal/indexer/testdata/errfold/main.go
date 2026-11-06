// Package main is a fixture for the error-return detector: every common
// shape of `if err != nil { … }` that only bails out, plus the shapes that
// look like one but carry real control flow and must NOT be folded. The
// test addresses blocks by the text on their `if` line, so comments here
// double as labels.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
)

type myErr struct{}

func (*myErr) Error() string { return "my" }

func open(name string) (*os.File, error) { return os.Open(name) }

func parse(b []byte) (int, error) { return len(b), nil }

func lookup() *myErr { return nil }

func shapes(name string) (int, error) {
	f, err := open(name)
	if err != nil { // plain
		return 0, err
	}
	defer f.Close()

	var buf []byte
	_, err = f.Read(buf)
	if err != nil { // wrapped
		return 0, fmt.Errorf("read %s: %w", name, err)
	}

	n, err := parse(buf)
	if err != nil { // logged
		log.Printf("parse: %v", err)
		return 0, err
	}

	if err := f.Sync(); err != nil { // init form
		return 0, fmt.Errorf("sync: %w", err)
	}

	if err != nil { // panic
		panic(err)
	}

	if err != nil { // does more — must NOT fold
		n = -1
		return n, err
	}

	if err != nil { // has else — must NOT fold
		return 0, err
	} else {
		n++
	}

	if errors.Is(err, os.ErrNotExist) { // errors.Is
		return 0, nil
	}

	if e := lookup(); e != nil { // typed error, not named err
		return 0, e
	}

	if err != nil { // bare return in a func with results isn't legal; use a wrap with no literal
		return 0, errors.Join(err, errors.New("x"))
	}

	if n > 3 { // not an error check
		return n, nil
	}

	if err != nil { // fatal
		log.Fatalf("boom: %v", err)
	}

	if err != nil { // reports (an HTTP handler's writeError-and-return shape)
		writeErr(err)
		return 0, err
	}

	return n, nil
}

func writeErr(error) {}

func main() {
	if _, err := shapes("x"); err != nil { // multi-assign init
		os.Exit(1)
	}
}
