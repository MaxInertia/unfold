// Package main is a service that calls inbox, which in turn calls
// conversation. It never touches conversation itself — so it reaches an
// anchor inside conversation only transitively, through inbox's handler.
package main

import "context"

type clientConn struct{}

func (c *clientConn) Invoke(ctx context.Context, method string, args, reply any) error {
	_ = method
	return nil
}

const InboxService_ShowThread_FullMethodName = "/inbox.v1.InboxService/ShowThread"

type inboxServiceClient struct{ cc *clientConn }

func (c *inboxServiceClient) ShowThread(ctx context.Context) error {
	return c.cc.Invoke(ctx, InboxService_ShowThread_FullMethodName, nil, nil)
}

type Server struct{ inbox *inboxServiceClient }

func (s *Server) render(ctx context.Context) error {
	return s.inbox.ShowThread(ctx)
}

func main() { _ = (&Server{}).render(context.Background()) }
