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

// --- shapes that must NOT become outbound edges, or must not double up ---

// annotate stands in for grpc-gateway's runtime.AnnotateContext, which takes a
// method path as an ordinary argument. It issues no RPC; passing the string
// around is not calling it.
func annotate(ctx context.Context, method string) context.Context {
	_ = method
	return ctx
}

const DupService_Ping_FullMethodName = "/dup.v1.DupService/Ping"

// Two generated clients for the same RPC, as happens when generated code is
// duplicated across packages. One call site should still be one row.
type dupServiceClient struct{ cc *grpcConn }

func (c *dupServiceClient) Ping(ctx context.Context) error {
	return c.cc.Invoke(ctx, DupService_Ping_FullMethodName, nil, nil)
}

type dupServiceAltClient struct{ cc *grpcConn }

func (c *dupServiceAltClient) Ping(ctx context.Context) error {
	return c.cc.Invoke(ctx, DupService_Ping_FullMethodName, nil, nil)
}

// serveWithGateway passes a method path around without calling anything, and
// pings through both duplicate clients.
func (s *Server) serveWithGateway(ctx context.Context) error {
	ctx = annotate(ctx, "/ai_assistants.v1.Assistants/ListAssistants")
	if err := (&dupServiceClient{cc: &grpcConn{}}).Ping(ctx); err != nil {
		return err
	}
	return (&dupServiceAltClient{cc: &grpcConn{}}).Ping(ctx)
}

// deadPath calls a client, but nothing calls deadPath and it is not an
// entrypoint — so the call site exists yet execution never arrives. This is
// what the reachability filter excludes, as opposed to a stub with no callers
// at all, which never produces a site in the first place.
func (s *Server) deadPath(ctx context.Context) error {
	return (&inventoryServiceClient{cc: &grpcConn{}}).Reserve(ctx)
}
