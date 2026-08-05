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

func main() {}
