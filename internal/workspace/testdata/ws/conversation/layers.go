package main

import "context"

// The shape a real service has: the gRPC surface is fronted by decorators,
// and generated mocks sit beside the real implementation. Every one of these
// is a method with the RPC's name, so name matching alone finds several.

// loggingServer wraps another implementation.
type loggingServer struct{ next *ConversationServer }

func (s *loggingServer) GetConversation(ctx context.Context) error {
	return s.next.GetConversation(ctx)
}

// MockConversationServiceServer is a generated test double — never the
// implementation being asked about.
type MockConversationServiceServer struct{}

func (m *MockConversationServiceServer) GetConversation(ctx context.Context) error { return nil }

func (s *ConversationServer) reachMe() {}
