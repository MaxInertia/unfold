// Package main is a service that calls conversation through its SDK. The
// join key is never written here — it lives inside the generated client.
package main

import "context"

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

type Server struct{ convo *conversationServiceClient }

func (s *Server) showThread(ctx context.Context) error {
	return s.convo.GetConversation(ctx)
}

func main() { _ = (&Server{}).showThread(context.Background()) }
