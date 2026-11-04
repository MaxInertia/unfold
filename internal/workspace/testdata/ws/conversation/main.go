// Package main implements the conversation service.
package main

import "context"

type ConversationServer struct{}

// GetConversation implements conversation.v1.ConversationService.
func (s *ConversationServer) GetConversation(ctx context.Context) error {
	s.reachMe()
	return nil
}

func (s *ConversationServer) StreamConversation(ctx context.Context) error {
	return nil
}

// main registers the implementation, which is where this service says what it
// serves without a manifest saying it for them.
func main() {
	RegisterConversationServiceServer(&server{}, &ConversationServer{})
}
