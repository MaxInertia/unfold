package main

import "context"

// A stand-in for generated gRPC client code. The point of the fixture is
// that it declares a method with the *same name* as the real implementation
// below, which is what a real dependency-laden package set looks like, and
// that it states the full method name as a constant the way protoc-gen-go-grpc
// does.

type clientConn struct{}

func (c *clientConn) Invoke(ctx context.Context, method string, args, reply any) error {
	_ = method
	return nil
}

const ConversationService_GetConversation_FullMethodName = "/conversation.v1.ConversationService/GetConversation"

type conversationServiceClient struct{ cc *clientConn }

func (c *conversationServiceClient) GetConversation(ctx context.Context) error {
	return c.cc.Invoke(ctx, ConversationService_GetConversation_FullMethodName, nil, nil)
}

// UnimplementedConversationServiceServer is the generated embed; it must not
// be mistaken for the implementation either.
type UnimplementedConversationServiceServer struct{}

func (UnimplementedConversationServiceServer) GetConversation() {}
