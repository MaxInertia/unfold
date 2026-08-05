package agsdk

import "context"

// A second RPC on the same client, so a caller that uses both fans out.

const AccountGroupService_Create_FullMethodName = "/accountgroup.v1.AccountGroupService/Create"

func (c *Client) Create(ctx context.Context) error {
	return c.cc.Invoke(ctx, AccountGroupService_Create_FullMethodName, nil, nil)
}
