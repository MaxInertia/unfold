package indexer

import (
	"go/ast"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/platform"
)

// Outbound gRPC edges, derived from the call graph rather than from scanning
// for literals.
//
// The earlier approach asked "does this call site reach a method path?" and
// then needed a filter for every way that question answers wrongly: a depth
// bound because chains reach everything, a uniqueness rule because a function
// reaching many RPCs got summarized by one, relay dedup because every caller
// above a call inherited it, and stub suppression because a generated client
// *is* a literal Invoke. Four filters for one question is a sign the question
// was wrong.
//
// The real question is simpler and the index already answers it: a generated
// client method corresponds one-to-one with an RPC, so the service calls that
// RPC exactly when the service's own code calls that method. That's
// reachability over the call graph unfold already built for the callers tree.
//
// Two consequences fall out rather than being enforced:
//
//   - A stub nothing calls yields nothing. No suppression rule needed; it has
//     no callers, so it produces no edges.
//   - The edge lands on the caller, at the boundary where owned code meets the
//     client. No relay dedup needed; the walk stops at the first owned frame,
//     so callers further out are never reached in the first place.

// grpcOutbound returns this project's outbound gRPC edges.
func (i *Indexer) grpcOutbound() []model.Binding {
	var out []model.Binding
	for id := range i.funcs {
		key, ok := i.directInvokeKey(id)
		if !ok {
			continue
		}
		// A hand-written function that issues the call itself *is* the call
		// site — business logic talking to grpc directly, not a client. Its
		// callers are ordinary callers, not the ones making the request.
		if i.ownsCode(i.funcs[id]) && !i.isGeneratedClient(id) {
			out = append(out, i.grpcBinding(id, key, id))
			continue
		}
		// Otherwise the method stands for the RPC, and the calls are whoever
		// invokes it from this project.
		for _, site := range i.ownedCallersOf(id) {
			out = append(out, i.grpcBinding(site, key, id))
		}
	}
	return out
}

func (i *Indexer) grpcBinding(site TargetID, key string, client TargetID) model.Binding {
	b := model.Binding{
		Role:       model.RoleOutbound,
		Kind:       "grpc.method",
		Key:        strings.TrimPrefix(key, "/"),
		Site:       site,
		Confidence: model.ConfExact,
	}
	if fi := i.funcs[site]; fi != nil {
		pos := i.fset.Position(fi.decl.Pos())
		b.File, b.Line = pos.Filename, pos.Line
	}
	if fi := i.funcs[client]; fi != nil && client != site {
		b.Detail = goTitle(fi.obj)
	}
	return b
}

// ownedCallersOf walks the reverse call graph out from a client method until
// it reaches this project's code, and returns that frontier.
//
// Ownership is what bounds the walk, which is why no distance limit is needed:
// intermediate hops are an SDK's own layers, and the search stops the moment
// it arrives somewhere that belongs to the service. Frames further out never
// enter the result, so a caller-of-a-caller can't inherit the edge.
func (i *Indexer) ownedCallersOf(client TargetID) []TargetID {
	var (
		frontier []TargetID
		seen     = map[TargetID]bool{client: true}
		queue    = []TargetID{client}
	)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, u := range i.usagesByTarget[cur] {
			// Value references don't execute anything, the same reason the
			// callers tree won't splice through them.
			if u.kind == model.UsageRef || seen[u.parent] {
				continue
			}
			seen[u.parent] = true
			if fi := i.funcs[u.parent]; fi != nil && i.ownsCode(fi) {
				frontier = append(frontier, u.parent)
				continue // the frontier is the answer; don't climb past it
			}
			queue = append(queue, u.parent)
		}
	}
	return frontier
}

// directInvokeKey returns the gRPC method path a function issues in its own
// body. A generated client method contains exactly one; a body with several is
// a generic transport standing for no particular RPC.
func (i *Indexer) directInvokeKey(target TargetID) (string, bool) {
	if v, ok := i.invokeCache[target]; ok {
		return v.path, v.path != ""
	}
	fi := i.funcs[target]
	if fi == nil || fi.decl == nil || fi.decl.Body == nil || fi.pkg.TypesInfo == nil {
		i.invokeCache[target] = invokeResult{}
		return "", false
	}
	info := fi.pkg.TypesInfo
	var found []string
	ast.Inspect(fi.decl.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := nameIdent(ce.Fun)
		if name == nil || (name.Name != "Invoke" && name.Name != "NewStream") {
			return true
		}
		for _, a := range ce.Args {
			if v := argFacts(info, a); v.Known && platform.IsMethodPath(v.Value) {
				found = appendUnique(found, v.Value)
			}
		}
		return true
	})
	res := invokeResult{}
	if len(found) == 1 {
		res.path = found[0]
	}
	i.invokeCache[target] = res
	return res.path, res.path != ""
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}

// isGeneratedClient reports whether a function is a method on a generated
// gRPC client.
//
// protoc-gen-go-grpc emits, per service, a `<Service>Client` interface and an
// unexported struct implementing it with one method per RPC. Testing the
// implements relation makes this structural rather than a guess about naming.
func (i *Indexer) isGeneratedClient(target TargetID) bool {
	fi := i.funcs[target]
	if fi == nil {
		return false
	}
	recv, recvPkg := receiverParts(fi)
	if recv == "" {
		return false
	}
	for key, impls := range i.interfaceImpls {
		if !strings.HasSuffix(key, "Client") {
			continue
		}
		for _, t := range impls {
			if n, p := namedTypeParts(t); n == recv && p == recvPkg {
				return true
			}
		}
	}
	// No such interface indexed (hand-rolled client, or generated code whose
	// interface didn't load) — fall back to the naming codegen guarantees.
	return strings.HasSuffix(recv, "Client")
}
