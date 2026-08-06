// Package agsdk stands in for a hand-written Go SDK: the kind that wraps a
// generated gRPC client in a couple of layers. It lives in its own module so
// it is a *dependency* of the service under test, which is where real SDKs
// live — and which is what makes the service's own call site the right place
// to attribute the edge to.
package agsdk

import "context"

type conn struct{}

func (c *conn) Invoke(ctx context.Context, method string, args, reply any) error {
	_ = method
	return nil
}

const AccountGroupService_GetMulti_FullMethodName = "/accountgroup.v1.AccountGroupService/GetMulti"

// invokeGetMulti is reached by a plain function call, not a method call —
// following only selector callees never gets here.
func invokeGetMulti(ctx context.Context, cc *conn) error {
	return cc.Invoke(ctx, AccountGroupService_GetMulti_FullMethodName, nil, nil)
}

type Client struct{ cc *conn }

func New() *Client { return &Client{cc: &conn{}} }

func (c *Client) getMulti(ctx context.Context) error { return invokeGetMulti(ctx, c.cc) }

// GetMulti is what a caller actually writes. The method name is three hops
// from the literal that identifies it.
func (c *Client) GetMulti(ctx context.Context) error { return c.getMulti(ctx) }
