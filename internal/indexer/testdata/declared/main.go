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

// GetConversation implements conversation.v1.ConversationService, and calls
// another service while serving. Nothing but the gRPC surface reaches this
// path, so it only counts as outbound if declared entrypoints seed
// reachability.
func (s *Server) GetConversation() {
	_ = (&searchServiceClient{cc: &grpcConn{}}).Query(context.Background())
}

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

// syncAll fans out to two different RPCs. It is not a client for either one,
// and naming it after whichever was found first is exactly how outbound
// filled with calls the service never makes.
func (s *Server) syncAll(ctx context.Context) error {
	c := agsdk.New()
	if err := c.GetMulti(ctx); err != nil {
		return err
	}
	return c.Create(ctx)
}

func (s *Server) refresh(ctx context.Context) error { return s.syncAll(ctx) }

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
	_ = (&Server{}).chargeCustomer(context.Background())
	_ = (&Server{}).refresh(context.Background())
}
