package main

import (
	"context"
	"net/http"

	"example.com/keys/topics"
)

// A route pattern held in a package-level var, which is how a service that
// shares its paths with a client usually spells them.
var healthPath = "/from-var"

type Emitter interface {
	Emit(ctx context.Context, topic string) error
}

type Server struct{ b Emitter }

func (s *Server) all(ctx context.Context) {
	_ = s.b.Emit(ctx, "literal.key")               // 0 literal
	_ = s.b.Emit(ctx, topics.OrderCreated)         // 1 exported const
	_ = s.b.Emit(ctx, topics.UserCreated)          // 2 const built from consts
	_ = s.b.Emit(ctx, topics.PaymentTaken)         // 3 package-level var
	_ = s.b.Emit(ctx, topics.Topics.Shipped)       // 4 field on a struct var
	_ = s.b.Emit(ctx, topics.Anon.Cancelled)       // 5 field on an anonymous struct var
	_ = s.b.Emit(ctx, "acme."+topics.OrderCreated) // 6 concat with a const
	_ = s.b.Emit(ctx, topics.Topics.Refund)        // 7 another field of the same var
}

func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, r *http.Request) {})
	return mux
}

func main() {
	(&Server{}).all(context.Background())
	_ = routes()
}
