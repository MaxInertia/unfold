// Package protoapi reads the gRPC surface a service declares in its protos.
//
// This is what makes a cross-service call resolvable without any dataflow
// analysis: the proto is the artifact both the server and its generated SDK
// are built from, so `<package>.<Service>/<Method>` is a key both ends of the
// edge name. That's a declared join, not an inferred one.
//
// Proto paths in a microservice.yaml are relative to a shared proto
// repository rather than to the service, so every call here takes the root
// that they resolve against.
package protoapi

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
)

// Method is one RPC in the declared surface.
type Method struct {
	// FullName is the join key: "conversation.v1.ConversationService/Get".
	FullName string
	// Service is the fully-qualified service name, Name the bare RPC name.
	Service string
	Name    string
	// File is the proto path as written in the manifest.
	File string
	// ExcludedFromSDK is true when this method's file is excluded from SDK
	// generation — implemented here, but not callable by other services.
	ExcludedFromSDK bool
	// Streaming records the RPC's shape, which is worth showing because a
	// streaming method reads very differently from a unary one.
	ClientStreaming bool
	ServerStreaming bool
}

// Load compiles the given proto paths (relative to root) and returns every
// RPC they declare, sorted by full name.
//
// Compilation resolves imports through root, so a proto importing another
// proto in the same repository works without listing the import explicitly.
// Errors from individual files are returned rather than swallowed: a proto
// root pointed at the wrong directory should say so, not silently yield an
// empty surface.
func Load(ctx context.Context, root string, paths []string, excluded map[string]bool) ([]Method, error) {
	if root == "" || len(paths) == 0 {
		return nil, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("proto root %q: %w", root, err)
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{abs},
		}),
		// Only the descriptors are needed; skipping source info keeps the
		// compile cheap for a surface listing.
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	files, err := compiler.Compile(ctx, paths...)
	if err != nil {
		return nil, fmt.Errorf("compile protos under %s: %w", abs, err)
	}

	var out []Method
	for i, f := range files {
		path := paths[i]
		out = append(out, methodsOf(f, path, excluded[path])...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].FullName < out[b].FullName })
	return out, nil
}

func methodsOf(f linker.File, path string, excludedFile bool) []Method {
	var out []Method
	services := f.Services()
	for i := 0; i < services.Len(); i++ {
		svc := services.Get(i)
		methods := svc.Methods()
		for j := 0; j < methods.Len(); j++ {
			m := methods.Get(j)
			out = append(out, Method{
				FullName:        fmt.Sprintf("%s/%s", svc.FullName(), m.Name()),
				Service:         string(svc.FullName()),
				Name:            string(m.Name()),
				File:            path,
				ExcludedFromSDK: excludedFile,
				ClientStreaming: m.IsStreamingClient(),
				ServerStreaming: m.IsStreamingServer(),
			})
		}
	}
	return out
}
