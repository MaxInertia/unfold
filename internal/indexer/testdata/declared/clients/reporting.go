// Package clients holds generated clients shared by the service and its
// commands.
package clients

import "context"

type conn struct{}

func (c *conn) Invoke(ctx context.Context, method string, args, reply any) error {
	_ = method
	return nil
}

const ReportingService_Export_FullMethodName = "/reporting.v1.ReportingService/Export"

type ReportingClient interface {
	Export(ctx context.Context) error
}

type reportingClient struct{ cc *conn }

func NewReportingClient() ReportingClient { return &reportingClient{cc: &conn{}} }

func (c *reportingClient) Export(ctx context.Context) error {
	return c.cc.Invoke(ctx, ReportingService_Export_FullMethodName, nil, nil)
}

const ArchiveService_Purge_FullMethodName = "/archive.v1.ArchiveService/Purge"

type ArchiveClient interface {
	Purge(ctx context.Context) error
}

type archiveClient struct{ cc *conn }

func (c *archiveClient) Purge(ctx context.Context) error {
	return c.cc.Invoke(ctx, ArchiveService_Purge_FullMethodName, nil, nil)
}

// DeadPath calls a client, but nothing calls DeadPath and it is neither an
// entrypoint nor in a command package — a call site execution never arrives
// at. That is distinct from a stub with no callers, which produces no call
// site to begin with.
func DeadPath(ctx context.Context) error {
	return (&archiveClient{cc: &conn{}}).Purge(ctx)
}
