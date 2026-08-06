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

// InboxServiceServer is the generated server interface for the RPC this
// service declares in its manifest.
type InboxServiceServer interface {
	ShowThread(ctx context.Context) error
}

type Server struct{ convo *conversationServiceClient }

// ShowThread implements inbox.v1.InboxService. Serving it calls conversation,
// which is what makes a caller of *this* RPC transitively reach anything
// inside conversation.
func (s *Server) ShowThread(ctx context.Context) error {
	return s.convo.GetConversation(ctx)
}

var _ InboxServiceServer = (*Server)(nil)

func main() { _ = (&Server{}).ShowThread(context.Background()) }
