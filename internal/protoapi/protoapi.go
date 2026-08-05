// Package protoapi reads the gRPC surface a service declares in its protos.
//
// This is what makes a cross-service call resolvable without any dataflow
// analysis: the proto is the artifact both the server and its generated SDK
// are built from, so `<package>.<Service>/<Method>` is a key both ends of the
// edge name. That's a declared join, not an inferred one.
//
// Each file is *parsed*, not compiled. All that's wanted here are names, and
// a fully linked descriptor would require resolving the entire import
// closure — which in practice reaches outside the proto repository
// altogether (googleapis' google/rpc/*.proto, google/api/*.proto, and so on).
// Linking would make the surface depend on vendoring decisions that have
// nothing to do with what a service exposes. A proto's fully-qualified names
// are already determined by its own `package` and `service` declarations, so
// parsing loses nothing that matters.
//
// Proto paths in a microservice.yaml are relative to a shared proto
// repository rather than to the service, so every call here takes the root
// that they resolve against.
package protoapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
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

// Load parses the given proto paths (relative to root) and returns every RPC
// they declare, sorted by full name.
//
// Failures are per-file and partial: a proto that can't be read or parsed
// doesn't hide the surface declared by the others. The returned error names
// what failed, and is non-nil even when some methods came back — the caller
// is expected to show both. It's only fatal when nothing parsed at all.
func Load(root string, paths []string, excluded map[string]bool) ([]Method, error) {
	if root == "" || len(paths) == 0 {
		return nil, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("proto root %q: %w", root, err)
	}

	var (
		out      []Method
		problems []string
	)
	for _, p := range paths {
		methods, err := parseFile(abs, p, excluded[p])
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out = append(out, methods...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].FullName < out[b].FullName })

	if len(problems) == 0 {
		return out, nil
	}
	err = fmt.Errorf("%d of %d proto file(s) under %s could not be read: %s",
		len(problems), len(paths), abs, strings.Join(problems, "; "))
	if len(out) == 0 {
		return nil, err
	}
	return out, err
}

func parseFile(root, path string, excludedFile bool) ([]Method, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	defer f.Close()

	// A reporter that collects errors rather than aborting on the first,
	// so a file with one bad declaration still yields the services around it.
	var errs []error
	handler := reporter.NewHandler(reporter.NewReporter(
		func(e reporter.ErrorWithPos) error { errs = append(errs, e); return nil },
		nil, // warnings are not interesting for a surface listing
	))
	file, err := parser.Parse(path, f, handler)
	if file == nil {
		if err == nil {
			err = errors.Join(errs...)
		}
		return nil, fmt.Errorf("%s: %v", path, err)
	}

	pkg := packageOf(file)
	var out []Method
	for _, decl := range file.Decls {
		svc, ok := decl.(*ast.ServiceNode)
		if !ok {
			continue
		}
		svcName := svc.Name.Val
		if pkg != "" {
			svcName = pkg + "." + svcName
		}
		for _, sd := range svc.Decls {
			rpc, ok := sd.(*ast.RPCNode)
			if !ok {
				continue
			}
			out = append(out, Method{
				FullName:        svcName + "/" + rpc.Name.Val,
				Service:         svcName,
				Name:            rpc.Name.Val,
				File:            path,
				ExcludedFromSDK: excludedFile,
				ClientStreaming: rpc.Input != nil && rpc.Input.Stream != nil,
				ServerStreaming: rpc.Output != nil && rpc.Output.Stream != nil,
			})
		}
	}
	return out, nil
}

func packageOf(file *ast.FileNode) string {
	for _, decl := range file.Decls {
		if p, ok := decl.(*ast.PackageNode); ok {
			return string(p.Name.AsIdentifier())
		}
	}
	return ""
}
