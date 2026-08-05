// Package main is a miniature service: it registers a couple of HTTP routes
// and makes one outbound call, which is exactly the shape the platform
// recognizers extract bindings from.
package main

import "net/http"

const prefix = "/v1"

type Server struct{}

// handleOrders reaches validate, so an anchor on validate should mark the
// orders route (and only the orders route) as an entrypoint that reaches it.
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	s.validate()
}

func (s *Server) validate() {}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Pattern built from a constant — the type checker folds it, so this
	// still yields a literal key.
	mux.HandleFunc("POST "+prefix+"/orders", s.handleOrders)
	// Handler wrapped in a conversion — the recognizer looks through it.
	mux.Handle("/health", http.HandlerFunc(s.handleHealth))
	return mux
}

func fetchOrders() {
	_, _ = http.Get("https://orders.internal/v1/orders")
}

func main() {
	_ = (&Server{}).Handler()
	fetchOrders()
}
