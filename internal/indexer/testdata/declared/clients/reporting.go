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

const ScheduleService_Sync_FullMethodName = "/schedule.v1.ScheduleService/Sync"

type scheduleClient struct{ cc *conn }

func (c *scheduleClient) Sync(ctx context.Context) error {
	return c.cc.Invoke(ctx, ScheduleService_Sync_FullMethodName, nil, nil)
}

type task struct{ run func(context.Context) error }

// scheduled is the cobra shape in miniature: a package-level var holding a
// closure that calls a client. No FuncDecl contains that call, and this is
// not a command package, so nothing else would make it reachable — it only
// counts because a package-level initializer runs at program start.
var scheduled = &task{run: func(ctx context.Context) error {
	return (&scheduleClient{cc: &conn{}}).Sync(ctx)
}}

// Keep it referenced so the package compiles cleanly.
func Scheduled() *task { return scheduled }
