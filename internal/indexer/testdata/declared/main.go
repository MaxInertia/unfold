// Package main is a service that declares itself in microservice.yaml: a
// public route, an internal one, and gRPC methods implemented from protos in
// the shared proto repository.
package main

import (
	"context"
	"net/http"

	"example.com/agsdk"
)

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

// fetchConversation calls another service through its generated client. The
// key is stated inside the client, not here.
func (s *Server) fetchConversation() {
	c := &conversationServiceClient{cc: &clientConn{}}
	_ = c.GetConversation(context.Background())
}

// listAccounts calls another service through a hand-written SDK. Nothing here
// names the RPC — the key is three hops away, inside the dependency.
func (s *Server) listAccounts(ctx context.Context) error {
	return agsdk.New().GetMulti(ctx)
}

// Wiring several calls deep from the SDK. Everything here transitively
// reaches the same Invoke, which is exactly why unbounded chain-following
// reported RPCs a service never calls: startup() no more calls
// AccountGroupService than main() does.
func (s *Server) bootstrap() { _ = s.listAccounts(context.Background()) }
func (s *Server) startup()   { s.bootstrap() }
func (s *Server) wireUp()    { s.startup() }

func main() {
	_ = (&Server{}).Handler()
	(&Server{}).fetchConversation()
	_ = (&Server{}).listAccounts(context.Background())
	(&Server{}).wireUp()
}
