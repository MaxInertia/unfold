package main

import "context"

// The shape a real service has: a generated server interface, the
// implementation, a decorator in front of it, generated mocks, and unrelated
// code that happens to share a method name.

// ConversationServiceServer is the generated interface. Which types implement
// it is the structural answer to "what implements this RPC" — far better than
// matching on the method name, which everything below also satisfies.
type ConversationServiceServer interface {
	GetConversation(ctx context.Context) error
	StreamConversation(ctx context.Context) error
}

// loggingServer decorates the real implementation, and implements the full
// interface — so it is a legitimate answer.
type loggingServer struct{ next *ConversationServer }

func (s *loggingServer) GetConversation(ctx context.Context) error {
	return s.next.GetConversation(ctx)
}

func (s *loggingServer) StreamConversation(ctx context.Context) error {
	return s.next.StreamConversation(ctx)
}

// analyticsReporter is unrelated code that happens to declare a method with
// the same name. It does not implement the service interface, so it must not
// be offered as an implementation.
type analyticsReporter struct{}

func (a *analyticsReporter) GetConversation(ctx context.Context) error { return nil }

// MockConversationServiceServer is a generated test double.
type MockConversationServiceServer struct{}

func (m *MockConversationServiceServer) GetConversation(ctx context.Context) error    { return nil }
func (m *MockConversationServiceServer) StreamConversation(ctx context.Context) error { return nil }

func (s *ConversationServer) reachMe() {}

var _ ConversationServiceServer = (*ConversationServer)(nil)
