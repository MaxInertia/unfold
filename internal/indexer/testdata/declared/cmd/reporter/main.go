// Command reporter is the shape that motivated treating whole main packages
// as entrypoints: an RPC called only from a command, through a helper that
// main never calls directly — it's wired up by a framework at run time.
package main

import (
	"context"

	"example.com/conversation/clients"
)

// run is invoked by the command framework, not from main, so no call edge
// leads here. It runs all the same: this is a command.
func run(ctx context.Context) error {
	return clients.NewReportingClient().Export(ctx)
}

func main() {}
