package indexer

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"github.com/MaxInertia/unfold/internal/model"
	"golang.org/x/tools/go/packages"
)

// The inbound half of gRPC: the RPCs a service implements, read from the
// registration its own code performs.
//
// The declared tier answers the same question from a proto, and answers it
// better — a proto is the contract both ends are generated from, and it is
// readable without indexing anyone's Go. But it is reachable only through
// `microservice.yaml` protoPaths resolved against a shared proto repository,
// which is one organisation's convention rather than a fact about gRPC. Point
// unfold at two repositories that don't follow it and the surface is empty:
// the implementation sits right there in the index, nothing publishes it as an
// end of its key, and a call site in the other repo resolves to nothing. The
// far side of every gRPC edge went missing for want of a manifest.
//
// What makes it readable without any convention is that protoc-gen-go-grpc
// emits the join key itself. `<Service>_ServiceDesc` carries the fully
// qualified service name and one entry per RPC — the same strings the
// generated client passes to Invoke, which is what the outbound pass already
// reads. So both ends of a gRPC edge come from generated code, and they meet
// on a key neither side invented.
//
// Two rules earn their place, and each is pinned by a test:
//
//   - The registration that counts is the one *this service* performs, not the
//     generated helper that performs it. `RegisterFooServer(s, srv)` hands its
//     own parameter to `s.RegisterService`, so the helper's body looks exactly
//     like a registration and would double every RPC — with no implementation
//     to name, since a parameter is not an object. A registration whose
//     implementation is a parameter of the function containing it is a
//     pass-through, and the real one is at its caller.
//     (TestGeneratedRegistrationHelperIsNotItsOwnRegistration)
//   - A key the declared surface already carries is left alone. The proto
//     tier knows things this one cannot — the file the RPC is declared in,
//     and whether it is excluded from SDK generation — so a service with a
//     manifest reads exactly as it did before this pass existed.
//     (TestDeclaredSurfaceWinsOverTheRegistration)
const servedRuleID = "builtin.grpc.server"

// serviceDesc is a generated grpc.ServiceDesc, reduced to the join: the fully
// qualified service name and the RPCs registered under it.
type serviceDesc struct {
	service string
	methods []string
}

// indexServiceDescs records every generated service descriptor in the loaded
// set, keyed by the variable holding it.
//
// Dependencies are visited as well as this module: generated code is imported
// as often as it is vendored, and which of the two a repo does is not
// something its service surface should depend on.
func (i *Indexer) indexServiceDescs(pkgs []*packages.Package) {
	i.descs = map[types.Object]*serviceDesc{}
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		info := pkg.TypesInfo
		if info == nil {
			return
		}
		for _, file := range pkg.Syntax {
			for _, d := range file.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for n, name := range vs.Names {
						if n >= len(vs.Values) {
							continue
						}
						obj := info.Defs[name]
						if obj == nil {
							continue
						}
						if desc := serviceDescIn(info, vs.Values[n]); desc != nil {
							i.descs[obj] = desc
						}
					}
				}
			}
		}
	})
}

// serviceDescIn reads a descriptor out of a variable's initializer.
//
// The type is matched by name rather than by import path, for the same reason
// isGeneratedClient tests the implements relation rather than a package: the
// shape is what identifies it, and pinning the path would mean a vendored or
// re-exported grpc stops being recognized while looking identical.
func serviceDescIn(info *types.Info, val ast.Expr) *serviceDesc {
	if u, ok := val.(*ast.UnaryExpr); ok && u.Op == token.AND {
		val = u.X
	}
	cl, ok := val.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	if name, _ := namedTypeParts(info.TypeOf(cl)); name != "ServiceDesc" {
		return nil
	}
	d := &serviceDesc{}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "ServiceName":
			if s, ok := constStringOf(info, kv.Value); ok {
				d.service = s
			}
		case "Methods", "Streams":
			// A streaming RPC is registered in a second list and is no less
			// an entrypoint for it — dropping Streams would leave a service
			// that only streams looking like one that serves nothing.
			d.methods = append(d.methods, methodNamesIn(info, kv.Value)...)
		}
	}
	if d.service == "" || len(d.methods) == 0 {
		return nil
	}
	sort.Strings(d.methods)
	return d
}

// methodNamesIn reads the MethodName/StreamName of each entry in a descriptor's
// method or stream list.
func methodNamesIn(info *types.Info, val ast.Expr) []string {
	cl, ok := val.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var out []string
	for _, elt := range cl.Elts {
		entry, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, f := range entry.Elts {
			kv, ok := f.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || (key.Name != "MethodName" && key.Name != "StreamName") {
				continue
			}
			if s, ok := constStringOf(info, kv.Value); ok {
				out = appendUnique(out, s)
			}
		}
	}
	return out
}

// grpcServed returns the RPCs this service registers an implementation for.
//
// `declared` names the keys the proto tier already carries, which this pass
// leaves alone: it is the same surface known from a stronger source, and
// emitting it twice would show every RPC of a manifest-carrying service twice.
func (i *Indexer) grpcServed(declared map[string]bool) []model.Binding {
	if len(i.descs) == 0 {
		return nil
	}
	var out []model.Binding
	seen := map[string]bool{}
	// Fixed order: two registrations of one key (a service registered on two
	// servers, which real code does for a debug port) must not decide by map
	// iteration which one names the surface.
	for _, id := range i.sortedFuncIDs() {
		fi := i.funcs[id]
		if fi.body == nil || fi.pkg == nil || fi.pkg.TypesInfo == nil || !i.ownsCode(fi) {
			continue
		}
		info := fi.pkg.TypesInfo
		ast.Inspect(fi.body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			desc := i.descAt(info, ce)
			if desc == nil {
				return true
			}
			impl, ok := implArg(info, fi, ce, desc)
			if !ok {
				return true // a pass-through helper; the caller is the registration
			}
			pos := i.fset.Position(ce.Pos())
			for _, m := range desc.methods {
				key := desc.service + "/" + m
				if declared[key] || seen[key] {
					continue
				}
				seen[key] = true
				b := model.Binding{
					Role:       model.RoleInbound,
					Kind:       "grpc.method",
					Key:        key,
					Site:       fi.id,
					File:       pos.Filename,
					Line:       pos.Line,
					Confidence: model.ConfExact,
					Visibility: model.VisPlatform,
					Rule:       servedRuleID,
					Detail:     "registered here",
				}
				target, candidates := i.methodOn(impl, m)
				switch {
				case target != "":
					b.Target = target
				case len(candidates) > 0:
					// The implementation arrived through an interface, so
					// several bodies may be what runs. The same answer an
					// interface call site gets, and the same reason: naming
					// one of them would be a guess, dropping them all would
					// leave the RPC unopenable.
					b.Candidates = candidates
				default:
					// Registered, but the method isn't in this index. Not
					// stale — the registration is code, and code that
					// wouldn't compile if the method were missing — so this
					// is unfold failing to find it, and saying nothing about
					// the implementation is the honest report.
					b.Detail = "registered here (implementation not indexed)"
				}
				out = append(out, b)
			}
			return true
		})
	}
	return out
}

// descAt returns the service descriptor a call site registers, if any.
//
// Two shapes, because generated code and hand-written code register
// differently: the call may name the descriptor itself
// (`s.RegisterService(&Foo_ServiceDesc, impl)`), or it may be the generated
// `RegisterFooServer(s, impl)` whose body does. Following one level into the
// callee is what makes the second readable, and it is the same one-level rule
// the outbound pass uses to read a key out of a client stub.
func (i *Indexer) descAt(info *types.Info, ce *ast.CallExpr) *serviceDesc {
	for _, a := range ce.Args {
		if d := i.descNamedBy(info, a); d != nil {
			return d
		}
	}
	fn := calleeFunc(info, ce)
	if fn == nil {
		return nil
	}
	return i.descInBody(TargetID(fn.FullName()))
}

// descNamedBy returns the descriptor an expression names, following `&x`.
func (i *Indexer) descNamedBy(info *types.Info, e ast.Expr) *serviceDesc {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	var obj types.Object
	switch v := e.(type) {
	case *ast.Ident:
		obj = info.Uses[v]
	case *ast.SelectorExpr:
		obj = info.Uses[v.Sel]
	}
	if obj == nil {
		return nil
	}
	return i.descs[obj]
}

// descInBody returns the single descriptor a function registers in its own
// body, memoized.
//
// Single, for the reason directInvokeKey is single: a body naming several
// descriptors is a registry helper standing for no particular service, and
// picking one of them would attribute a whole surface to the wrong name.
func (i *Indexer) descInBody(fn TargetID) *serviceDesc {
	if d, ok := i.descCache[fn]; ok {
		return d
	}
	fi := i.funcs[fn]
	if fi == nil || fi.body == nil || fi.pkg == nil || fi.pkg.TypesInfo == nil {
		i.descCache[fn] = nil
		return nil
	}
	info := fi.pkg.TypesInfo
	var found []*serviceDesc
	ast.Inspect(fi.body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, a := range ce.Args {
			if d := i.descNamedBy(info, a); d != nil && !containsDesc(found, d) {
				found = append(found, d)
			}
		}
		return true
	})
	var res *serviceDesc
	if len(found) == 1 {
		res = found[0]
	}
	i.descCache[fn] = res
	return res
}

func containsDesc(xs []*serviceDesc, x *serviceDesc) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}

// implArg finds the argument holding the implementation being registered: the
// one whose type has the RPCs.
//
// The second return is false when the registration is a pass-through — the
// implementation is a parameter of the very function containing the call, which
// is what the generated `RegisterFooServer` helper looks like from the inside.
// Emitting there would describe every service twice and name no implementation
// either time, since a parameter is a placeholder rather than a body.
func implArg(info *types.Info, fi *funcInfo, ce *ast.CallExpr, desc *serviceDesc) (types.Type, bool) {
	for _, a := range ce.Args {
		t := info.TypeOf(a)
		if t == nil {
			continue
		}
		if !hasMethod(t, desc.methods[0]) {
			continue
		}
		if namesParamOf(info, fi, a) {
			return nil, false
		}
		return t, true
	}
	return nil, false
}

// hasMethod reports whether a type has a method of this name, on the value or
// the pointer — a server implementation is registered as `&Server{}` about as
// often as it is defined on the value.
func hasMethod(t types.Type, name string) bool {
	if obj, _, _ := types.LookupFieldOrMethod(t, true, nil, name); obj != nil {
		if _, ok := obj.(*types.Func); ok {
			return true
		}
	}
	return false
}

// namesParamOf reports whether an expression is a parameter of fi.
func namesParamOf(info *types.Info, fi *funcInfo, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	obj, _ := info.Uses[id].(*types.Var)
	if obj == nil || fi.obj == nil {
		return false
	}
	sig, _ := fi.obj.Type().(*types.Signature)
	if sig == nil {
		return false
	}
	for n := 0; n < sig.Params().Len(); n++ {
		if sig.Params().At(n) == obj {
			return true
		}
	}
	return false
}

// methodOn resolves one RPC to the code that serves it: the method of that name
// on the registered type, or — when what was registered is an interface — every
// implementation of it in the index.
func (i *Indexer) methodOn(t types.Type, name string) (TargetID, []model.Candidate) {
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, name)
	fn, _ := obj.(*types.Func)
	if fn == nil {
		return "", nil
	}
	if id := TargetID(fn.FullName()); i.funcs[id] != nil {
		return id, nil
	}
	// An interface method has no body. Its implementations are what may run,
	// which is the answer the declared surface gives for the same situation.
	iface, _ := t.Underlying().(*types.Interface)
	if iface == nil {
		return "", nil
	}
	typeName, pkgPath := namedTypeParts(t)
	if typeName == "" {
		return "", nil
	}
	var out []model.Candidate
	for _, impl := range i.interfaceImpls[pkgPath+"."+typeName] {
		m, _, _ := types.LookupFieldOrMethod(impl, true, nil, name)
		mf, _ := m.(*types.Func)
		if mf == nil {
			continue
		}
		id := TargetID(mf.FullName())
		if fi := i.funcs[id]; fi != nil {
			out = append(out, model.Candidate{TargetID: id, Label: fi.title})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].TargetID < out[b].TargetID })
	return "", out
}

// calleeFunc resolves the function a call site names.
func calleeFunc(info *types.Info, ce *ast.CallExpr) *types.Func {
	switch fn := ce.Fun.(type) {
	case *ast.Ident:
		f, _ := info.Uses[fn].(*types.Func)
		return f
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fn]; ok {
			f, _ := sel.Obj().(*types.Func)
			return f
		}
		f, _ := info.Uses[fn.Sel].(*types.Func)
		return f
	}
	return nil
}

// sortedFuncIDs is the indexed function set in a fixed order, for passes whose
// output must not depend on map iteration.
func (i *Indexer) sortedFuncIDs() []TargetID {
	out := make([]TargetID, 0, len(i.funcs))
	for id := range i.funcs {
		out = append(out, id)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// declaredKeys is the inbound gRPC surface the manifest and its protos already
// carry, which the registration pass stands aside for.
func (i *Indexer) declaredKeys() map[string]bool {
	out := map[string]bool{}
	for _, b := range i.bindings {
		if b.Kind == "grpc.method" && b.Role == model.RoleInbound {
			out[b.Key] = true
		}
	}
	return out
}
