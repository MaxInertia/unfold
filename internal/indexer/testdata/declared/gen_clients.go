package main

import "context"

// Generated clients living in the service repo, which is what makes this the
// interesting case: every RPC on the platform has a stub here, but the service
// only actually calls one of them. The stubs are capability, not usage.

type grpcConn struct{}

func (c *grpcConn) Invoke(ctx context.Context, method string, args, reply any) error {
	_ = method
	return nil
}

const (
	BillingService_Charge_FullMethodName    = "/billing.v1.BillingService/Charge"
	BillingService_Refund_FullMethodName    = "/billing.v1.BillingService/Refund"
	SearchService_Query_FullMethodName      = "/search.v1.SearchService/Query"
	InventoryService_Reserve_FullMethodName = "/inventory.v1.InventoryService/Reserve"
)

// BillingServiceClient is the generated interface; billingServiceClient is the
// generated implementation. Both are what protoc-gen-go-grpc emits.
type BillingServiceClient interface {
	Charge(ctx context.Context) error
	Refund(ctx context.Context) error
}

type billingServiceClient struct{ cc *grpcConn }

func (c *billingServiceClient) Charge(ctx context.Context) error {
	return c.cc.Invoke(ctx, BillingService_Charge_FullMethodName, nil, nil)
}

func (c *billingServiceClient) Refund(ctx context.Context) error {
	return c.cc.Invoke(ctx, BillingService_Refund_FullMethodName, nil, nil)
}

var _ BillingServiceClient = (*billingServiceClient)(nil)

type SearchServiceClient interface {
	Query(ctx context.Context) error
}

type searchServiceClient struct{ cc *grpcConn }

func (c *searchServiceClient) Query(ctx context.Context) error {
	return c.cc.Invoke(ctx, SearchService_Query_FullMethodName, nil, nil)
}

var _ SearchServiceClient = (*searchServiceClient)(nil)

type InventoryServiceClient interface {
	Reserve(ctx context.Context) error
}

type inventoryServiceClient struct{ cc *grpcConn }

func (c *inventoryServiceClient) Reserve(ctx context.Context) error {
	return c.cc.Invoke(ctx, InventoryService_Reserve_FullMethodName, nil, nil)
}

var _ InventoryServiceClient = (*inventoryServiceClient)(nil)

// The service calls exactly one of them.
func (s *Server) chargeCustomer(ctx context.Context) error {
	return (&billingServiceClient{cc: &grpcConn{}}).Charge(ctx)
}
