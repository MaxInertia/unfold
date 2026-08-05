// Package main is a service that declares itself in microservice.yaml: a
// public route, an internal one, and gRPC methods implemented from protos in
// the shared proto repository.
package main

import "net/http"

type Server struct{}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {}
func (s *Server) debugDump(w http.ResponseWriter, r *http.Request)         {}

// GetConversation implements conversation.v1.ConversationService.
func (s *Server) GetConversation() {}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/conversations", s.listConversations)
	mux.HandleFunc("/internal/debug", s.debugDump)
	return mux
}

func main() { _ = (&Server{}).Handler() }
