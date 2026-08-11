package main

type Inner struct{}

func (Inner) HandleFooBar(payload []byte) error { return nil }

type Handler interface {
	HandleFooBar(payload []byte) error
}

type Embedder struct{ Inner }

type Outer struct {
	Bar   Inner
	Iface Handler
	Emb   Embedder
	Ptr   *Inner
}

var foo = Outer{Iface: Inner{}, Ptr: &Inner{}}

func HandleFoo(payload []byte) error { return nil }

func subscribe(h func([]byte) error) {}

func register() {
	subscribe(HandleFoo)              // package-level func value
	subscribe(foo.Bar.HandleFooBar)   // method value through a field
	subscribe(foo.Iface.HandleFooBar) // method value on an interface field
	subscribe(foo.Emb.HandleFooBar)   // promoted method from an embedded type
	subscribe(foo.Ptr.HandleFooBar)   // method value through a pointer field
}

func main() { register() }
