// Package indexer loads Go packages and builds the call-site and
// implementer indexes that power unfold's interactive code expansion.
//
// Phase 2: direct and interface calls are resolved. Interface calls
// carry a list of concrete-type implementations; the API picks one as
// the default expansion target and lets the caller switch via choice.
// Calls through a function value or a builtin (len, make, ...) are
// recorded as kind="indirect" and remain unexpandable.
package indexer

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MaxInertia/unfold/internal/manifest"
	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/platform"
	"github.com/MaxInertia/unfold/internal/rules"
	"github.com/MaxInertia/unfold/internal/protoapi"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// utf16Offset returns the number of UTF-16 code units in b[:byteOffset].
// The frontend indexes a function's source as a JavaScript (UTF-16) string,
// so call-site span offsets must be expressed in UTF-16 units, not UTF-8
// bytes. ASCII text counts one unit per byte (this is the identity); runes
// above the BMP count as two units (a surrogate pair).
func utf16Offset(b []byte, byteOffset int) int {
	if byteOffset > len(b) {
		byteOffset = len(b)
	}
	n := 0
	for i := 0; i < byteOffset; {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			n++ // invalid byte — count it as one unit and advance
			i++
			continue
		}
		if r > 0xFFFF {
			n += 2 // encoded as a surrogate pair in UTF-16
		} else {
			n++
		}
		i += size
	}
	return n
}

// byteOffsetForUTF16 is the inverse of utf16Offset: it maps a UTF-16 code-unit
// offset (what the frontend sends) back to a byte offset in b.
func byteOffsetForUTF16(b []byte, u16 int) int {
	n := 0
	for i := 0; i < len(b); {
		if n >= u16 {
			return i
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			n++
			i++
			continue
		}
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return len(b)
}

// The wire types live in internal/model so every engine emits the same
// JSON shapes. These aliases keep the indexer's call sites terse and let
// existing callers/tests continue to reference indexer.Frame etc. For the
// Go engine, TargetID is *types.Func.FullName (e.g.
// "github.com/x/y.(*T).Method") and CallID is "<enclosing function>@<n>",
// the nth site in source order inside that function (see assignCallIDs).
type (
	TargetID     = model.TargetID
	CallID       = model.CallID
	CallKind     = model.CallKind
	Frame        = model.Frame
	CallSite     = model.CallSite
	Candidate    = model.Candidate
	SearchResult = model.SearchResult
	TypeInfo     = model.TypeInfo
	CallFacts    = model.CallFacts
)

const (
	KindDirect    = model.KindDirect
	KindInterface = model.KindInterface
	KindIndirect  = model.KindIndirect
	KindRef       = model.KindRef
)

// Indexer implements model.Engine, and the optional platform half of it.
var (
	_ model.Engine         = (*Indexer)(nil)
	_ model.PlatformEngine = (*Indexer)(nil)
)

// Indexer holds loaded packages and the per-function call-site index.
type Indexer struct {
	mu sync.RWMutex

	pkgs []*packages.Package
	fset *token.FileSet

	// funcs is keyed by TargetID (i.e. *types.Func.FullName()).
	funcs map[TargetID]*funcInfo

	// callsByID lets the server resolve a CallID to its parent function and
	// pick a candidate target for interface calls.
	callsByID map[CallID]*callInfo

	// interfaceImpls maps a named-interface key (pkgpath.TypeName) to
	// the list of concrete types in the loaded set that implement it.
	// Built once during Load.
	interfaceImpls map[string][]types.Type

	// usagesByTarget is the reverse of the call-site index: for each target,
	// the places it's referenced (direct calls, interface calls that may
	// dispatch to it, and value references). Built once during Load.
	usagesByTarget map[TargetID][]*usageInfo

	// invokeCache memoizes invokeInfo per target; a client method's body is
	// scanned once however many call sites reach it.
	invokeCache map[TargetID]invokeResult

	// bindings are the platform edges recognized in this project — routes it
	// serves, topics it names, calls it makes out. Collected during Load in
	// the same body walk that resolves call sites.
	bindings []model.Binding

	// Identity of the indexed project, for the service-level view. The
	// manifest's declared name wins; the repo directory is the fallback.
	serviceName string
	modulePath  string
	rootDir     string

	// mf is the parsed microservice.yaml, nil when the repo has none.
	mf *manifest.Manifest

	// protoRoot is the shared proto repository that a manifest's protoPaths
	// resolve against. Empty disables proto loading — the paths are relative
	// to a repo unfold has no way to locate on its own.
	protoRoot string
	// ruleSet is the configured recognizers: user-written rules, plus which
	// built-ins are switched off. Empty by default, so a project with no rules
	// file behaves exactly as before.
	ruleSet   rules.Set
	ruleStats map[string]int
	// varStrings is the string a package-level variable is initialized with;
	// varFields the same for the fields of a struct variable. Both are what a
	// key argument resolves against when it isn't a constant.
	varStrings map[types.Object]string
	varFields  map[types.Object]map[string]string
	// ruleSites is which recognizers claimed each call site, keyed file:line.
	// Built-ins and configured rules land in the same map: from where a reader
	// stands, "what already recognizes this call" is one question, and an
	// answer that silently omitted the built-ins would be worse than none —
	// they are the majority of matches in most repos.
	ruleSites map[string][]string
	// leaves is the reading-time classification rules made for call sites,
	// keyed file:line.
	leaves map[string]rules.LeafDecision

	// outboundUnreachable counts outbound call sites excluded because
	// execution can't reach them from any recognized entrypoint.
	outboundUnreachable int

	// protoErr records why the declared gRPC surface is missing, so the UI
	// can say "proto root is wrong" instead of showing an empty surface as
	// though the service had none.
	protoErr string

	// fileBytes caches the raw source of files whose functions we've
	// produced frames for, so we don't re-read on every /body request.
	fileBytesMu sync.Mutex
	fileBytes   map[string][]byte
}

// invokeResult memoizes the method path a function issues in its own body,
// empty when it issues none, or several (a generic transport standing for no
// particular RPC).
type invokeResult struct {
	path string
}

// funcInfo is one indexed body of code. Usually that's a function, but not
// always: a package-level variable's initializer holds calls too, and they
// execute — so it is indexed the same way and everything downstream (frames,
// usages, callers, reachability) works on it without knowing the difference.
//
// obj and decl are therefore nil for an initializer. The fields below them
// are the ones every entry has, and are what the rest of the indexer reads:
// asking a variable for its *ast.FuncDecl is a question with no answer.
type funcInfo struct {
	id    TargetID
	obj   *types.Func    // nil for a package-level initializer
	decl  *ast.FuncDecl  // nil ditto
	pkg   *packages.Package
	calls []*callInfo

	node  ast.Node // the whole declaration — its range is the frame
	body  ast.Node // what to walk for calls; nil when there's nothing to walk
	title string   // display name
	name  string   // bare identifier, for main/init checks
	doc   string
	// initializer marks a package-level variable rather than a function. It
	// runs at program start, which is why it seeds reachability.
	initializer bool
}

type callInfo struct {
	id          CallID
	parent      TargetID
	kind        CallKind
	target      TargetID    // direct target (empty otherwise)
	candidates  []Candidate // interface candidates (in stable order)
	displayName string
	pos, end    token.Pos
	goroutine   bool // call is launched with the `go` keyword
}

// usageInfo is one reverse-index entry: a place a target is referenced.
type usageInfo struct {
	call   *callInfo // the call site; nil for value references
	choice int       // candidate index selecting the target at that call
	parent TargetID  // enclosing function
	kind   model.UsageKind
	pos    token.Pos // the usage's name token
}

// New returns a fresh, empty indexer.
func New() *Indexer {
	return &Indexer{
		funcs:          make(map[TargetID]*funcInfo),
		callsByID:      make(map[CallID]*callInfo),
		interfaceImpls: make(map[string][]types.Type),
		fileBytes:      make(map[string][]byte),
	}
}

// Load parses and type-checks the Go packages matched by pattern (e.g.
// "./...") relative to dir, and builds the call-site index. If dir is
// empty, the current working directory is used. Packages with type
// errors are kept and indexed best-effort; their errors are written to
// stderr.
func (i *Indexer) Load(dir, pattern string) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedTypesSizes |
			packages.NeedModule,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return fmt.Errorf("packages.Load: %w", err)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages matched %q", pattern)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		fmt.Fprintf(os.Stderr, "unfold: %d package errors (continuing)\n", n)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	i.pkgs = pkgs
	i.fset = pkgs[0].Fset
	i.funcs = make(map[TargetID]*funcInfo)
	i.callsByID = make(map[CallID]*callInfo)
	i.interfaceImpls = buildInterfaceImpls(pkgs)
	i.usagesByTarget = make(map[TargetID][]*usageInfo)
	i.bindings = nil
	i.outboundUnreachable = 0
	i.invokeCache = make(map[TargetID]invokeResult)
	i.mf = nil
	i.protoErr = ""
	i.identify(dir, pkgs)

	// Pass 1: register every FuncDecl as a target.
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.TypesInfo == nil {
			return
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}
				obj, _ := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
				if obj == nil {
					return true
				}
				tid := TargetID(obj.FullName())
				if _, dup := i.funcs[tid]; dup {
					// Two FuncDecls with the same FullName shouldn't happen
					// inside one type-checked package set; if it does (e.g.
					// build constraints), keep the first.
					return true
				}
				fi := &funcInfo{id: tid, obj: obj, decl: fd, pkg: pkg, node: fd, title: goTitle(obj), name: obj.Name()}
				if fd.Body != nil {
					fi.body = fd.Body
				}
				if fd.Doc != nil {
					fi.doc = strings.TrimSpace(fd.Doc.Text())
				}
				i.funcs[tid] = fi
				return true
			})
		}
	})

	// Pass 1b: package-level variables whose initializer contains a call.
	//
	// `var cmd = &cobra.Command{RunE: func(...) { client.Do() }}` holds a call
	// that no FuncDecl contains, so pass 2 never reached it: no call site, no
	// usage, no outbound surface, and nothing for the callers tree to walk
	// through. It was the largest remaining blind spot, and it hides exactly
	// the code that wires a program together.
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.TypesInfo == nil {
			return
		}
		for _, file := range pkg.Syntax {
			for _, d := range file.Decls {
				gd, ok := d.(*ast.GenDecl)
				// Only var: a const initializer can't call anything, and a
				// type or import has nothing to run.
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Values) == 0 || len(vs.Names) == 0 {
						continue
					}
					// Registering every variable would fill the index with
					// frames for `var timeout = 5s`. Only those that actually
					// hold calls are code worth reading as code.
					if !containsCall(vs.Values) {
						continue
					}
					obj, _ := pkg.TypesInfo.Defs[vs.Names[0]].(*types.Var)
					if obj == nil || obj.Pkg() == nil {
						continue
					}
					// A package can't declare a func and a var with the same
					// name, so this shares one namespace with FullName safely.
					tid := TargetID(obj.Pkg().Path() + "." + obj.Name())
					if _, dup := i.funcs[tid]; dup {
						continue
					}
					names := make([]string, 0, len(vs.Names))
					for _, n := range vs.Names {
						names = append(names, n.Name)
					}
					// A lone `var x = …` reads as Go only if the frame starts
					// at the keyword, so the declaration is the range. Inside
					// a `var ( … )` block it can't be — the block holds other
					// variables — so the spec is, and the frame opens on the
					// line itself. The body walked is the spec either way.
					var node ast.Node = vs
					doc := vs.Doc
					if len(gd.Specs) == 1 {
						node = gd
						if gd.Doc != nil {
							doc = gd.Doc
						}
					}
					fi := &funcInfo{
						id:          tid,
						pkg:         pkg,
						node:        node,
						body:        vs,
						title:       strings.Join(names, ", "),
						name:        obj.Name(),
						initializer: true,
					}
					if doc != nil {
						fi.doc = strings.TrimSpace(doc.Text())
					}
					i.funcs[tid] = fi
				}
			}
		}
	})

	// Pass 1c: the string a variable starts out holding.
	i.indexVarStrings(pkgs)

	// Configured rules run alongside the built-ins: `disabled` switches
	// built-ins off, and the evaluator collects the facts its two phases need.
	disabled := i.ruleSet.Disabled()
	ev := rules.NewEvaluator(i.ruleSet.Rules)

	// Pass 2: walk each function body, resolve call sites. Idents that name
	// an indexed function but are not a call's name token are recorded as
	// value references (the function passed around as a value).
	for _, fi := range i.funcs {
		if fi.body == nil {
			continue
		}
		// goLaunched collects the CallExpr that are the operand of a `go`
		// statement. ast.Inspect visits a node before its children, so a
		// GoStmt is always seen before its own Call — the set is populated
		// by the time we resolve that CallExpr below. callNames works the
		// same way: a CallExpr is visited before the Ident that names it.
		goLaunched := make(map[*ast.CallExpr]bool)
		callNames := make(map[*ast.Ident]bool)
		// Sel idents already recorded as a reference by the selector case, so
		// the ident case doesn't record them a second time.
		selRefs := make(map[*ast.Ident]bool)
		ast.Inspect(fi.body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				goLaunched[node.Call] = true
			case *ast.CallExpr:
				if name := nameIdent(node.Fun); name != nil {
					callNames[name] = true
				}
				// Binding extraction is independent of call resolution: a
				// recognized call site (mux.HandleFunc) is an ordinary
				// resolvable call *and* a platform edge, and a call whose
				// target we can't resolve can still carry a usable key.
				//
				// Only this module's own call sites count. A route a
				// dependency registers, or a gRPC call it makes internally,
				// isn't part of *this* service's surface — and since
				// invokePath follows chains without a depth limit, admitting
				// dependency sites would bury the real edges under library
				// plumbing.
				ci := i.resolveCall(fi, node)
				if i.ownsCode(fi) {
					if facts, ok := i.callFacts(fi, node); ok {
						i.bindings = append(i.bindings, platform.Extract(facts, disabled)...)
						// Rules need the callee as well as the call site:
						// phase 1 asks what a function's own body does, which
						// is a question about the target, not about here.
						if ev != nil {
							var targets []TargetID
							if ci != nil {
								targets = append(targets, ci.target)
								for _, cand := range ci.candidates {
									targets = append(targets, cand.TargetID)
								}
							}
							ev.Observe(facts, targets...)
						}
					}
				}
				if ci == nil {
					return true
				}
				if goLaunched[node] {
					ci.goroutine = true
				}
				fi.calls = append(fi.calls, ci)
			case *ast.SelectorExpr:
				// A method named as a value — `foo.Bar.Handle`. The ident case
				// below already catches the ones go/types records in Uses: a
				// package function, and a concrete method. It cannot catch an
				// *interface* method, because an interface method has no body
				// and so is not an indexed function — and a handler injected
				// as an interface is exactly how a subscriber is usually
				// written, so that was the shape that silently wasn't a site.
				//
				// It resolves the way an interface *call* does: not one target
				// but a set of implementations, offered as candidates.
				if callNames[node.Sel] {
					return true
				}
				sel, ok := fi.pkg.TypesInfo.Selections[node]
				if !ok {
					return true
				}
				fnObj, _ := sel.Obj().(*types.Func)
				if fnObj == nil || !isInterface(sel.Recv()) {
					return true
				}
				cands := i.candidatesFor(sel.Recv(), fnObj.Name())
				if len(cands) == 0 {
					return true // nothing implements it; naming a hop to nowhere would be worse
				}
				ci := &callInfo{
					parent:      fi.id,
					kind:        KindRef,
					displayName: fnObj.Name(),
					candidates:  cands,
					pos:         node.Sel.Pos(),
					end:         node.Sel.End(),
				}
				fi.calls = append(fi.calls, ci)
				selRefs[node.Sel] = true
				// One usage per implementation, carrying the choice that
				// selects it — the same shape an interface call produces, so
				// the callers list can reproduce this site as an expansion.
				for j, cand := range cands {
					i.usagesByTarget[cand.TargetID] = append(i.usagesByTarget[cand.TargetID], &usageInfo{
						call:   ci,
						choice: j,
						parent: fi.id,
						kind:   model.UsageRef,
						pos:    node.Sel.Pos(),
					})
				}
			case *ast.Ident:
				if callNames[node] || selRefs[node] {
					return true
				}
				obj, ok := fi.pkg.TypesInfo.Uses[node].(*types.Func)
				if !ok {
					return true
				}
				tid := TargetID(obj.FullName())
				if _, known := i.funcs[tid]; known {
					// A reference is a site too. Nothing is invoked here, but
					// the function it names has a body, and "what does that
					// do" is the same question as at a call — so it joins
					// fi.calls and is given an id with the rest, which is what
					// makes it expandable inline and splice-able from the
					// callers list.
					ci := &callInfo{
						parent:      fi.id,
						kind:        KindRef,
						target:      tid,
						displayName: obj.Name(),
						pos:         node.Pos(),
						end:         node.End(),
					}
					fi.calls = append(fi.calls, ci)
					i.usagesByTarget[tid] = append(i.usagesByTarget[tid], &usageInfo{
						call:   ci,
						parent: fi.id,
						kind:   model.UsageRef,
						pos:    node.Pos(),
					})
				}
			}
			return true
		})
	}

	i.assignCallIDs()

	// Reverse index over call sites: a direct call references its target; an
	// interface call references every candidate it may dispatch to (Choice
	// records the candidate's index so the frontend can reproduce this usage
	// as an expansion via FrameForCall).
	for _, c := range i.callsByID {
		switch c.kind {
		case KindDirect:
			if _, known := i.funcs[c.target]; known {
				i.usagesByTarget[c.target] = append(i.usagesByTarget[c.target], &usageInfo{
					call:   c,
					parent: c.parent,
					kind:   model.UsageCall,
					pos:    c.pos,
				})
			}
		case KindInterface:
			for j, cand := range c.candidates {
				i.usagesByTarget[cand.TargetID] = append(i.usagesByTarget[cand.TargetID], &usageInfo{
					call:   c,
					choice: j,
					parent: c.parent,
					kind:   model.UsageInterface,
					pos:    c.pos,
				})
			}
		}
	}

	// Titles for binding endpoints, resolved once the whole function set is
	// known (a route registered in one package can hand off to a handler
	// defined in another, so this can't be done during the walk).
	i.titleEndpoints(i.bindings)
	// Declared surface is folded in after the code-derived bindings, so the
	// publicRoutes cross-check can see what the code actually registered.
	i.applyVisibility()
	i.bindings = append(i.bindings, i.declaredBindings()...)

	// Outbound gRPC is a call-graph question rather than a per-call-site one,
	// so it runs as its own pass. It goes *after* the declared surface
	// because it seeds reachability from the inbound entrypoints, and for a
	// gRPC-only service those are the proto-declared implementations — seed
	// before they exist and every outbound edge looks unreachable.
	grpcOut, unreachable := i.grpcOutbound()
	i.bindings = append(i.bindings, grpcOut...)
	i.outboundUnreachable = unreachable

	// Configured rules produce bindings the same way built-ins do, and are
	// filtered the same way afterwards. Reachability is a property of the
	// evaluator rather than of any rule: a rule says what shape counts, and
	// the engine decides whether execution can get there — so a configured
	// outbound edge can't claim a call the service never makes, which is the
	// filter the gRPC pass had to learn.
	ruleOut, ruleUnreachable := i.filterReachable(ev.Run())
	// Titled here rather than with the rest: the pass above runs before the
	// evaluator has produced anything, so a configured rule's binding used to
	// arrive with no title at all — the cross-repo hop into a subscriber found
	// the handler and then had nothing to call it.
	i.titleEndpoints(ruleOut)
	i.bindings = append(i.bindings, ruleOut...)
	i.outboundUnreachable += ruleUnreachable
	i.ruleStats = ev.Stats
	i.leaves = ev.Leaves()
	i.indexRuleSites(ev.Matched())

	i.sortBindings()
	i.computeCrossings()

	return nil
}

// indexRuleSites records which recognizer claimed each call site, and folds
// the built-ins' matches into the same per-rule counts the configured rules
// report.
//
// Built-ins had no counts at all: the report exists so a rule that quietly
// stopped matching says so, and the three rules most likely to break on a
// library upgrade were the ones exempt from it. They're countable now for the
// same reason they're attributable — Extract stamps each binding with the rule
// that produced it.
func (i *Indexer) indexRuleSites(matched map[string][]string) {
	sites := make(map[string][]string, len(matched))
	for site, ids := range matched {
		sites[site] = append([]string(nil), ids...)
	}
	for _, b := range i.bindings {
		if b.Rule == "" || b.File == "" {
			continue // the declared surface: a manifest, not a rule
		}
		site := b.File + ":" + strconv.Itoa(b.Line)
		if !slices.Contains(sites[site], b.Rule) {
			sites[site] = append(sites[site], b.Rule)
		}
		// Only the built-ins: a configured rule's matches are already counted
		// by the evaluator, and counting them here as well would double every
		// one that emitted a binding.
		if isBuiltinID(b.Rule) {
			i.ruleStats[b.Rule]++
		}
	}
	for site := range sites {
		sort.Strings(sites[site])
	}
	i.ruleSites = sites
}

// RulesAt names the recognizers matching a call site, for "what already claims
// this" at the point of authoring another one.
func (i *Indexer) RulesAt(file string, line int) []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.ruleSites[file+":"+strconv.Itoa(line)]
}

// SetProtoRoot points the indexer at the shared proto repository that a
// manifest's protoPaths are relative to. Without it the declared gRPC surface
// is skipped, since those paths don't resolve against the service's own
// directory.
//
// It works before Load (the flag path) and after (the user picking a
// directory in the UI). Changing it doesn't need a re-index: the declared
// surface is derived from the manifest and the protos, and depends on the Go
// index only to link an RPC to its implementation — so only the declared
// bindings are recomputed. The returned error reports an unusable root; the
// previous declared surface is dropped either way, since keeping a surface
// built from a directory the user just replaced would be a lie.
func (i *Indexer) SetProtoRoot(dir string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.protoRoot = dir
	i.protoErr = ""
	if i.mf == nil {
		return nil // nothing loaded yet, or no manifest — Load will apply it
	}
	kept := make([]model.Binding, 0, len(i.bindings))
	for _, b := range i.bindings {
		if b.Confidence != model.ConfDeclared {
			kept = append(kept, b)
		}
	}
	i.bindings = append(kept, i.declaredBindings()...)
	i.sortBindings()
	// The declared surface changed, so both the ids and what each entrypoint
	// reaches have to be rebuilt — a proto root that adds RPCs adds
	// entrypoints, and those reach outbound calls nothing else did.
	i.computeCrossings()
	// Only a root that yielded nothing is worth rejecting: a partial failure
	// still produced a usable surface, and the warning on the view says which
	// files were skipped.
	if i.protoErr != "" && !i.hasDeclaredGRPC() {
		return errors.New(i.protoErr)
	}
	return nil
}

// ProtoRoot reports the configured shared proto repository, if any.
func (i *Indexer) ProtoRoot() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.protoRoot
}

// sortBindings orders the surface deterministically.
//
// The location tiebreak matters: pass 2 walks functions in map order, so two
// call sites sharing a kind and key would otherwise swap places between runs.
// That makes output unstable for anyone diffing it and quietly flaky for
// tests that pick "the" binding for a key.
func (i *Indexer) sortBindings() {
	sort.SliceStable(i.bindings, func(a, b int) bool {
		x, y := i.bindings[a], i.bindings[b]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		if x.Key != y.Key {
			return x.Key < y.Key
		}
		if x.File != y.File {
			return x.File < y.File
		}
		if x.Line != y.Line {
			return x.Line < y.Line
		}
		return x.SiteTitle < y.SiteTitle
	})
}

// dropRelayedBindings removes edges that were discovered by following a call
// chain but belong to a call site further in.

// identify records what to call this project at the service level. The repo
// (module) directory name is the fallback; a microservice.yaml at the project
// root overrides it, and the module path is kept alongside as the unambiguous
// identifier.
func (i *Indexer) identify(dir string, pkgs []*packages.Package) {
	root := dir
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Dir != "" {
			root = p.Module.Dir
			i.modulePath = p.Module.Path
			break
		}
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	i.rootDir = filepath.Clean(root)
	i.serviceName = filepath.Base(i.rootDir)
	if i.serviceName == "." || i.serviceName == string(filepath.Separator) {
		i.serviceName = i.modulePath
	}

	// A manifest is optional; most repos unfold opens won't have one. A
	// malformed one is worth reporting but not worth failing the index over
	// — the code-derived view is still correct without it.
	m, err := manifest.Read(i.rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unfold: %v\n", err)
		return
	}
	i.mf = m
	if m == nil {
		return
	}
	if m.Name != "" {
		i.serviceName = m.Name
	}
}

// declaredBindings turns the manifest into bindings: the proto-declared gRPC
// surface, plus any publicRoutes the code never registered.
//
// Declared facts are the strongest tier — a proto is the contract both the
// server and its generated SDK are built from — but they're also the ones
// that rot, so anything the index can't corroborate is marked stale rather
// than presented as real surface.
func (i *Indexer) declaredBindings() []model.Binding {
	if i.mf == nil {
		return nil
	}
	var out []model.Binding

	paths, excluded := i.mf.IncludedProtos()
	if len(paths) > 0 {
		if i.protoRoot == "" {
			i.protoErr = fmt.Sprintf("%d proto path(s) declared but no --proto-root was given", len(paths))
		} else {
			// Partial results are normal: one unreadable proto shouldn't hide
			// the surface the others declare, so methods and the error are
			// both used.
			methods, err := protoapi.Load(i.protoRoot, paths, excluded)
			if err != nil {
				i.protoErr = err.Error()
				fmt.Fprintf(os.Stderr, "unfold: %v\n", err)
			}
			for _, m := range methods {
				b := model.Binding{
					Role:       model.RoleInbound,
					Kind:       "grpc.method",
					Key:        m.FullName,
					Detail:     m.File,
					Confidence: model.ConfDeclared,
					Visibility: model.VisPlatform,
					File:       filepath.Join(i.protoRoot, m.File),
				}
				if m.ExcludedFromSDK {
					// Implemented here, but no other service can call it.
					b.Visibility = model.VisInternal
					b.Detail = m.File + " (excluded from SDK)"
				}
				if m.ClientStreaming || m.ServerStreaming {
					b.Detail += " · streaming"
				}
				// Link to the implementation, and distinguish "nothing
				// implements this" (stale — the declaration is out of date)
				// from "several candidates, couldn't tell which" (not stale;
				// the RPC is fine, the *link* is what's missing). Conflating
				// them would report a healthy service as rotten.
				target, candidates := i.implementationOfRPC(m.Service, m.Name)
				switch {
				case target != "":
					b.Target = target
					b.TargetTitle = i.funcs[target].title
					b.Site = target
					b.SiteTitle = b.TargetTitle
				case len(candidates) == 0:
					b.Stale = true
				default:
					// Several implementations and no way to tell which is
					// "the" one — a service behind decorators, or one with
					// generated mocks beside the real thing. Enumerate them:
					// dropping the link left the row unopenable *and*
					// unreachable, since reachability is computed from the
					// very fields that were left empty.
					for _, c := range candidates {
						b.Candidates = append(b.Candidates, model.Candidate{
							TargetID: c,
							Label:    i.funcs[c].title,
						})
					}
					b.Detail += fmt.Sprintf(" · %d implementations", len(candidates))
				}
				out = append(out, b)
			}
		}
	}

	// A declared public route nothing registers is the drift case worth
	// surfacing; one that *is* registered gets marked public in place, below.
	for _, route := range i.mf.PublicRoutes {
		if i.routeRegistered(route) {
			continue
		}
		out = append(out, model.Binding{
			Role:       model.RoleInbound,
			Kind:       "http.route",
			Key:        route,
			Detail:     "declared in " + manifest.Name,
			Confidence: model.ConfDeclared,
			Visibility: model.VisPublic,
			Stale:      true,
		})
	}
	return out
}

// applyVisibility classifies the code-derived inbound surface against the
// manifest: a route the manifest calls public is public, everything else the
// code registered is internal until something says otherwise.
func (i *Indexer) applyVisibility() {
	public := map[string]bool{}
	if i.mf != nil {
		for _, r := range i.mf.PublicRoutes {
			public[r] = true
		}
	}
	for n := range i.bindings {
		b := &i.bindings[n]
		if b.Role != model.RoleInbound || b.Visibility != "" {
			continue
		}
		if public[routePath(b.Key)] {
			b.Visibility = model.VisPublic
		} else {
			b.Visibility = model.VisInternal
		}
	}
}

// routeRegistered reports whether the code registers a route at this path.
// publicRoutes are bare paths, so the method half of a "POST /x" key is
// ignored on both sides of the comparison.
func (i *Indexer) routeRegistered(route string) bool {
	for _, b := range i.bindings {
		if b.Kind == "http.route" && routePath(b.Key) == route {
			return true
		}
	}
	return false
}

// routePath drops the optional leading method from a route key.
func routePath(key string) string {
	if _, rest, ok := strings.Cut(key, " "); ok {
		return rest
	}
	return key
}

// implementationOf finds the Go method implementing an RPC, and reports how
// many plausible candidates there were.
//
// gRPC forces the implementation's method name to equal the RPC's, so the
// name is a reliable starting point — but a loaded package set contains that
// name several times over: the generated client, the Unimplemented embed,
// mocks, and the real server. Filtering those out is what makes the match
// usable rather than perpetually ambiguous:
//
//   - must be a method (a bare function of that name is something else),
//
//   - must not itself invoke gRPC — that's the *client* for this very RPC,
//     identified by the same Invoke literal the outbound recognizer reads,
//
//   - must not be a generated Unimplemented stub,
//
//   - and main-module methods win outright, since a match inside a
//     dependency is someone else's implementation, not this service's.
//
//   - and test files and generated mocks are skipped, since a double is
//     never the implementation being asked about.
//
// Zero candidates means nothing implements the RPC — a real staleness signal.
// Several means unfold can't tell which, which is a different claim: the
// candidates are returned so the caller can offer them all rather than
// dropping the link.
func (i *Indexer) implementationOf(name string) (TargetID, []TargetID) {
	return i.implementationOfRPC("", name)
}

// implementationOfRPC narrows by the generated server interface when the
// service is known.
//
// Matching on the method name alone is far too loose in a real service: a
// decorator, a metrics wrapper, an auth layer and the server itself all
// declare the same method, and enumerating all of them is barely better than
// guessing. But gRPC generates an interface per service — `<Service>Server`,
// carrying exactly that service's methods — so the types implementing it are
// the only real answers. That's a structural test, not a naming one: a
// decorator implements the interface and belongs in the list; a helper that
// merely shares a method name does not.
func (i *Indexer) implementationOfRPC(service, name string) (TargetID, []TargetID) {
	if impls := i.serverImplementors(service); impls != nil {
		var narrowed []TargetID
		_, all := i.implementationByName(name)
		for _, id := range all {
			if fi := i.funcs[id]; fi != nil {
				if recv, _ := receiverParts(fi); impls[recv] {
					narrowed = append(narrowed, id)
				}
			}
		}
		if len(narrowed) == 1 {
			return narrowed[0], narrowed
		}
		if len(narrowed) > 1 {
			return "", narrowed
		}
		// The interface exists but nothing indexed implements it; fall
		// through rather than claiming the RPC is unimplemented.
	}
	return i.implementationByName(name)
}

// serverImplementors returns the receiver types implementing <service>Server,
// or nil when no such interface is in the index.
func (i *Indexer) serverImplementors(service string) map[string]bool {
	if service == "" {
		return nil
	}
	bare := service[strings.LastIndex(service, ".")+1:]
	want := bare + "Server"
	out := map[string]bool{}
	for key, impls := range i.interfaceImpls {
		if key[strings.LastIndex(key, ".")+1:] != want {
			continue
		}
		for _, t := range impls {
			if n, _ := namedTypeParts(t); n != "" {
				out[n] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// receiverParts returns a method's receiver type name and package.
func receiverParts(fi *funcInfo) (name, pkgPath string) {
	if fi.obj == nil {
		return "", "" // an initializer has no receiver
	}
	sig, _ := fi.obj.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return "", ""
	}
	return namedTypeParts(sig.Recv().Type())
}

func (i *Indexer) implementationByName(name string) (TargetID, []TargetID) {
	var local, any []TargetID
	for id, fi := range i.funcs {
		if fi.obj == nil || fi.obj.Name() != name {
			continue
		}
		sig, _ := fi.obj.Type().(*types.Signature)
		if sig == nil || sig.Recv() == nil {
			continue
		}
		recv, _ := namedTypeParts(sig.Recv().Type())
		if strings.HasPrefix(recv, "Unimplemented") || isDouble(recv) {
			continue
		}
		if _, isClient := i.directInvokeKey(id); isClient {
			continue // a generated client method, not a server implementation
		}
		if pos := i.fset.Position(fi.decl.Pos()); strings.HasSuffix(pos.Filename, "_test.go") {
			continue
		}
		any = append(any, id)
		if i.ownsCode(fi) {
			local = append(local, id)
		}
	}
	if len(local) > 0 {
		any = local
	}
	sort.Slice(any, func(a, b int) bool { return any[a] < any[b] })
	if len(any) == 1 {
		return any[0], any
	}
	return "", any
}

// isDouble reports whether a receiver type name looks like a test double
// rather than an implementation. Naming is the only signal available — a mock
// satisfies the same interface as the real thing by construction — but the
// conventions are near-universal.
func isDouble(recv string) bool {
	l := strings.ToLower(recv)
	for _, p := range []string{"mock", "fake", "stub", "spy"} {
		if strings.HasPrefix(l, p) || strings.HasSuffix(l, p) {
			return true
		}
	}
	return false
}

// callFacts reduces a call site to the neutral shape recognizers consume.
// Reports false when the callee can't be identified — a builtin, an
// immediately-invoked literal, or a call in a package that failed to type
// check.
func (i *Indexer) callFacts(fi *funcInfo, ce *ast.CallExpr) (platform.Call, bool) {
	return i.callFactsIn(fi.pkg, fi.id, ce)
}

// callFactsIn is callFacts without a funcInfo, for callers that have the
// package and position but no enclosing indexed function — the hover card on a
// whole-file frame, notably. Keeping one extraction matters more than the
// argument list: what the authoring form offers has to be what the evaluator
// will actually match.
func (i *Indexer) callFactsIn(pkg *packages.Package, site TargetID, ce *ast.CallExpr) (platform.Call, bool) {
	if pkg == nil {
		return platform.Call{}, false
	}
	info := pkg.TypesInfo
	if info == nil {
		return platform.Call{}, false
	}
	var obj *types.Func
	switch fn := ce.Fun.(type) {
	case *ast.Ident:
		obj, _ = info.Uses[fn].(*types.Func)
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fn]; ok {
			obj, _ = sel.Obj().(*types.Func)
		} else {
			obj, _ = info.Uses[fn.Sel].(*types.Func)
		}
	}
	if obj == nil || obj.Pkg() == nil {
		return platform.Call{}, false
	}

	pos := i.fset.Position(ce.Pos())
	c := platform.Call{
		PkgPath: obj.Pkg().Path(),
		Func:    obj.Name(),
		Site:    site,
		File:    pos.Filename,
		Line:    pos.Line,
	}
	sig, _ := obj.Type().(*types.Signature)
	if sig != nil && sig.Recv() != nil {
		c.Recv, c.RecvPkg = namedTypeParts(sig.Recv().Type())
	}
	for n, a := range ce.Args {
		arg := i.argFacts(info, a)
		arg.ParamType = paramTypeAt(sig, n)
		c.Args = append(c.Args, arg)
	}
	return c, true
}

// varString resolves an expression that names a variable, or a field of one,
// to the string it was initialized with.
//
// Two shapes, both by lookup rather than by walking: `Topic` (an identifier
// bound to a var) and `Topics.Shipped` (a field selector on one). A selector
// through a package qualifier — `topics.Topic` — is an identifier as far as
// the type checker is concerned, so it needs no separate case; what
// distinguishes it from a field selector is only whether the base resolves to
// a package or to a variable.
func (i *Indexer) varString(info *types.Info, e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		if obj := info.Uses[v]; obj != nil {
			s, ok := i.varStrings[obj]
			return s, ok
		}
	case *ast.SelectorExpr:
		// `pkg.Name` — the selector itself resolves to the variable.
		if obj := info.Uses[v.Sel]; obj != nil {
			if s, ok := i.varStrings[obj]; ok {
				return s, true
			}
		}
		// `Registry.Field` or `pkg.Registry.Field` — the base is the variable
		// and the selector names one of its fields.
		if base := i.baseVar(info, v.X); base != nil {
			if fields, ok := i.varFields[base]; ok {
				s, ok := fields[v.Sel.Name]
				return s, ok
			}
		}
	}
	return "", false
}

// titleEndpoints fills in the display names for a batch of bindings' endpoints
// and drops a handler that isn't an indexed function, so the UI never offers a
// link that goes nowhere. Applied per batch because the batches are produced
// at different points in the load, and a binding titled from an incomplete
// function set would be titled wrongly rather than not at all.
func (i *Indexer) titleEndpoints(bs []model.Binding) {
	for n := range bs {
		b := &bs[n]
		if fi := i.funcs[b.Target]; fi != nil {
			b.TargetTitle = fi.title
		} else {
			b.Target = ""
		}
		if fi := i.funcs[b.Site]; fi != nil {
			b.SiteTitle = fi.title
		}
	}
}

// varFieldsOf resolves an expression naming a package-level struct variable to
// the strings its fields were initialized with. Nil for anything else, which
// includes a struct built at the call site: its fields are right there in the
// source, and reading them would be a different lookup for a case nobody has
// asked for.
func (i *Indexer) varFieldsOf(info *types.Info, e ast.Expr) map[string]string {
	if obj := i.baseVar(info, e); obj != nil {
		return i.varFields[obj]
	}
	return nil
}

// baseVar resolves the variable an expression denotes, looking through a
// package qualifier. Nil when the expression is anything else — a call, an
// index, a field of a field: all of them have an answer, and none of them has
// one this cheap, so they're left to say "unknown" rather than be guessed at.
func (i *Indexer) baseVar(info *types.Info, e ast.Expr) types.Object {
	switch v := e.(type) {
	case *ast.Ident:
		if obj, ok := info.Uses[v].(*types.Var); ok {
			return obj
		}
	case *ast.SelectorExpr:
		if obj, ok := info.Uses[v.Sel].(*types.Var); ok {
			return obj
		}
	}
	return nil
}

// indexVarStrings records the string a package-level variable is initialized
// with, and the strings the fields of a struct variable are initialized with.
//
// Keys are frequently not literals at the call site. A constant already
// resolves — the type checker folds it, across packages and through
// concatenation — but the two shapes it can't reach are the ones a shared
// events package usually uses:
//
//	var PaymentTaken = "acme.payments.taken"
//	var Topics = Registry{Shipped: "acme.orders.shipped"}
//
// Both are a value the program *starts* with, which is a weaker claim than a
// constant: nothing here can see an assignment made later. That's what the
// confidence badge is for, so the value is recorded and marked inferred rather
// than being declined — declining it leaves the edge missing entirely, and a
// missing edge is the failure this whole surface exists to avoid.
//
// Only strings, and only package scope: a local variable is a different
// question (it has a flow, and answering it properly means dataflow rather
// than a lookup), and every other type is irrelevant to a join key.
func (i *Indexer) indexVarStrings(pkgs []*packages.Package) {
	i.varStrings = map[types.Object]string{}
	i.varFields = map[types.Object]map[string]string{}
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
							continue // `var a, b = f()` — nothing per-name to read
						}
						obj, _ := info.Defs[name].(*types.Var)
						if obj == nil {
							continue
						}
						i.recordVarValue(info, obj, vs.Values[n])
					}
				}
			}
		}
	})
}

func (i *Indexer) recordVarValue(info *types.Info, obj types.Object, val ast.Expr) {
	if s, ok := constStringOf(info, val); ok {
		i.varStrings[obj] = s
		return
	}
	// `&T{…}` is as common as `T{…}` for a registry, and the pointer makes no
	// difference to what the fields hold.
	if u, ok := val.(*ast.UnaryExpr); ok && u.Op == token.AND {
		val = u.X
	}
	cl, ok := val.(*ast.CompositeLit)
	if !ok {
		return
	}
	fields := map[string]string{}
	for n, elt := range cl.Elts {
		switch e := elt.(type) {
		case *ast.KeyValueExpr:
			key, ok := e.Key.(*ast.Ident)
			if !ok {
				continue
			}
			if s, ok := constStringOf(info, e.Value); ok {
				fields[key.Name] = s
			}
		default:
			// Positional: the field is identified by its index in the struct
			// type, which is the only place that order is recorded.
			if name := structFieldName(info.TypeOf(cl), n); name != "" {
				if s, ok := constStringOf(info, e); ok {
					fields[name] = s
				}
			}
		}
	}
	if len(fields) > 0 {
		i.varFields[obj] = fields
	}
}

// structFieldName is the name of field n of a (possibly pointer, possibly
// named) struct type.
func structFieldName(t types.Type, n int) string {
	if t == nil {
		return ""
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || n >= st.NumFields() {
		return ""
	}
	return st.Field(n).Name()
}

// constStringOf is the type checker's answer for a constant string
// expression, which covers named constants in any package and concatenations
// of them.
func constStringOf(info *types.Info, e ast.Expr) (string, bool) {
	tv, ok := info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// paramTypeAt renders the callee's declared parameter type at position n.
//
// The declared type is a different fact from the type of the value passed, and
// a rule may need either: a parameter declared `any` says nothing about what
// arrives, and a value that is a locally-defined implementation of an SDK
// interface says nothing about what the callee accepts. Recording both is what
// lets a rule pick the one that discriminates for a given library.
func paramTypeAt(sig *types.Signature, n int) string {
	if sig == nil {
		return ""
	}
	params := sig.Params()
	if params == nil || params.Len() == 0 {
		return ""
	}
	last := params.Len() - 1
	switch {
	case n < last, n == last && !sig.Variadic():
		if n > last {
			return ""
		}
		return qualifiedType(params.At(n).Type())
	case sig.Variadic():
		// Every argument in the variadic tail is an element, not the slice —
		// matching on "[]T" would never fire for the call that was written.
		if slice, ok := params.At(last).Type().(*types.Slice); ok {
			return qualifiedType(slice.Elem())
		}
		return qualifiedType(params.At(last).Type())
	}
	return ""
}

// qualifiedType renders a type with full package paths, so a rule names
// "github.com/acme/events/pb.Event" rather than "pb.Event" — the same
// discipline as matching on a package path rather than a package name, and
// for the same reason: short names collide across modules.
func qualifiedType(t types.Type) string {
	if t == nil {
		return ""
	}
	return types.TypeString(t, func(p *types.Package) string { return p.Path() })
}

// namedTypeParts unwraps a receiver type to its bare name and package, so a
// recognizer can match on ("net/http", "ServeMux") without caring whether the
// method was declared on the value or the pointer.
func namedTypeParts(t types.Type) (name, pkgPath string) {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil {
		return "", ""
	}
	if pkg := named.Obj().Pkg(); pkg != nil {
		pkgPath = pkg.Path()
	}
	return named.Obj().Name(), pkgPath
}

// argFacts extracts the two things recognizers read off an argument: its
// constant string value, and the function it names when it's a function
// value. Using the type checker's constant folding (rather than looking for
// *ast.BasicLit) means `"POST " + routePrefix` resolves like a literal.
func (i *Indexer) argFacts(info *types.Info, e ast.Expr) platform.Arg {
	var a platform.Arg
	if tv, ok := info.Types[e]; ok {
		if tv.Value != nil && tv.Value.Kind() == constant.String {
			a.Value = constant.StringVal(tv.Value)
			a.Known = true
		}
		a.Type = qualifiedType(tv.Type)
	}
	if !a.Known {
		// Not a constant, but possibly a variable whose starting value is
		// known — a shared events package names its topics one of these two
		// ways about as often as it uses a const.
		if s, ok := i.varString(info, e); ok {
			a.Value, a.Known, a.Inferred = s, true, true
		}
		// Or the argument is the definition *itself* — a struct var passed
		// whole, with the identity inside it. There is no Value to take here:
		// which field is the identity is a fact about the library, so the
		// fields are carried and a rule says which one it keys off.
		a.Fields = i.varFieldsOf(info, e)
	}
	switch v := e.(type) {
	case *ast.Ident:
		if f, ok := info.Uses[v].(*types.Func); ok {
			a.Target = TargetID(f.FullName())
		}
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[v]; ok {
			if f, ok := sel.Obj().(*types.Func); ok {
				a.Target = TargetID(f.FullName())
			}
		} else if f, ok := info.Uses[v.Sel].(*types.Func); ok {
			a.Target = TargetID(f.FullName())
		}
	case *ast.CallExpr:
		// A conversion wrapping the real argument, e.g.
		// http.Handle("/x", http.HandlerFunc(h)) — look through it.
		if tv, ok := info.Types[v.Fun]; ok && tv.IsType() && len(v.Args) == 1 {
			inner := i.argFacts(info, v.Args[0])
			// ...but not for the type. The conversion is what the call site
			// says the value *is*, and it's the more specific of the two —
			// http.HandlerFunc, not the bare func type it wraps.
			if a.Type != "" {
				inner.Type = a.Type
			}
			return inner
		}
	}
	return a
}

// ServiceView implements model.PlatformEngine.
func (i *Indexer) ServiceView(anchor TargetID) (*model.ServiceView, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	sv := &model.ServiceView{
		Name:                i.serviceName,
		Module:              i.modulePath,
		Root:                i.rootDir,
		ProtoRoot:           i.protoRoot,
		Warning:             i.protoErr,
		OutboundUnreachable: i.outboundUnreachable,
		// The cue for the UI to offer a picker: protos are declared but the
		// surface didn't come out, so a root is missing or wrong.
		NeedsProtoRoot: i.needsProtoRoot(),
		Inbound:        []model.Binding{},
		Outbound:       []model.Binding{},
	}
	// An anchor that isn't an indexed function (a stale URL, a file frame)
	// degrades to the plain service view rather than erroring — the view is
	// still correct, it just can't mark anything.
	// Two walks, because the anchor asks two different questions. Backwards
	// answers "what runs this code" and marks the entrypoints; forwards
	// answers "what does this code run" and marks the calls it makes.
	var reaching, reached map[TargetID]bool
	if fi := i.funcs[anchor]; fi != nil {
		sv.Anchor = anchor
		sv.AnchorTitle = fi.title
		reaching = i.callersClosure(anchor)
		reached = i.forwardClosure(map[TargetID]bool{anchor: true})
	}

	for _, b := range i.bindings {
		if b.Role == model.RoleInbound {
			// Only inbound surface is marked. The closure runs backwards
			// (who reaches the anchor), which answers "which entrypoints run
			// this code". The outbound question is the mirror image — which
			// calls the anchor itself makes — and needs a forward walk, so
			// labelling outbound rows with this closure would state
			// something true but not what the badge claims.
			if reaching != nil && bindingReaches(b, reaching) {
				b.ReachesAnchor = true
			}
			sv.Inbound = append(sv.Inbound, b)
		} else {
			if reached != nil && reached[b.Site] {
				b.ReachedByAnchor = true
			}
			sv.Outbound = append(sv.Outbound, b)
		}
	}
	return sv, nil
}

// needsProtoRoot reports whether the manifest declares protos that the
// current root can't resolve at all — no root set, or one that yielded
// nothing. A root that resolved most files and tripped on one is *not* the
// wrong root, so it warns without prompting for a replacement.
func (i *Indexer) needsProtoRoot() bool {
	if i.mf == nil {
		return false
	}
	paths, _ := i.mf.IncludedProtos()
	if len(paths) == 0 {
		return false
	}
	return i.protoRoot == "" || !i.hasDeclaredGRPC()
}

// hasDeclaredGRPC reports whether any RPC came out of the declared protos.
// Only the declared inbound surface counts: an outbound grpc.method binding
// is recognized from code and says nothing about whether the proto root
// resolved.
func (i *Indexer) hasDeclaredGRPC() bool {
	for _, b := range i.bindings {
		if b.Kind == "grpc.method" && b.Role == model.RoleInbound && b.Confidence == model.ConfDeclared {
			return true
		}
	}
	return false
}

// bindingReaches reports whether any endpoint of a binding reaches the
// anchor. Candidates count: an RPC with three possible implementations is
// still an entrypoint to the anchor if any one of them leads there, and
// checking only Target/Site silently excluded every enumerated binding.
func bindingReaches(b model.Binding, reaching map[TargetID]bool) bool {
	if reaching[b.Target] || reaching[b.Site] {
		return true
	}
	for _, c := range b.Candidates {
		if reaching[c.TargetID] {
			return true
		}
	}
	return false
}

// callersClosure returns every function that transitively reaches target,
// including target itself. It walks the usage index backwards, which makes
// it the same traversal the callers tree uses — an inbound binding whose
// handler lands in this set is an entrypoint through which the anchor runs.
//
// Value references are not followed: a function passed as a value has no call
// site, so a chain through one isn't an execution path (the same limitation
// the callers tree documents).
func (i *Indexer) callersClosure(target TargetID) map[TargetID]bool {
	seen := map[TargetID]bool{target: true}
	queue := []TargetID{target}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, u := range i.usagesByTarget[cur] {
			if u.kind == model.UsageRef || seen[u.parent] {
				continue
			}
			seen[u.parent] = true
			queue = append(queue, u.parent)
		}
	}
	return seen
}

// computeCrossings fills in, for every inbound binding, which outbound
// bindings its handler can actually cause — the relation joining the two
// halves of the service view.
//
// Both columns describe the same service but neither says anything about the
// other, so "this route is hit, what does the service then call?" — and read
// backwards, "what has to be hit for this call to happen?" — had no answer
// short of unfolding the handler by hand. It's also the missing piece for
// transitive anchor marking at the platform level: propagating a mark from
// one service to its callers needs exactly this, per repo.
//
// Walking forwards from each entrypoint costs one closure per inbound
// binding. The alternative — one backwards closure per outbound binding — is
// the better shape when a service has far more routes than calls, and is
// worth switching to if this ever shows up in a profile. It isn't a different
// answer, only a different traversal order.
//
// Seeds are the handler and its candidates, never the registration site: a
// function that registers a route doesn't run it, so seeding from the site
// would attribute every call the registrar makes to every route it registers.
func (i *Indexer) computeCrossings() {
	for n := range i.bindings {
		i.bindings[n].ID = "b" + strconv.Itoa(n)
		i.bindings[n].Reaches = nil
		i.bindings[n].CrossingKnown = false
	}

	type outbound struct {
		site TargetID
		id   string
	}
	var outs []outbound
	for _, b := range i.bindings {
		if b.Role == model.RoleOutbound && b.Site != "" {
			outs = append(outs, outbound{site: b.Site, id: b.ID})
		}
	}
	for n := range i.bindings {
		b := &i.bindings[n]
		if b.Role != model.RoleInbound {
			continue
		}
		seeds := map[TargetID]bool{}
		if b.Target != "" {
			seeds[b.Target] = true
		}
		// An enumerated binding reaches what *any* of its implementations
		// reaches: unfold can't tell which one serves, so claiming only the
		// first would be a guess dressed as a fact.
		for _, c := range b.Candidates {
			if c.TargetID != "" {
				seeds[c.TargetID] = true
			}
		}
		if len(seeds) == 0 {
			continue // no handler to walk from: unknown, not empty
		}
		// Knowable is about having a handler to walk from, not about finding
		// anything. A service with no outbound surface at all still gives a
		// determined answer for each of its entrypoints — "calls nothing" —
		// and reporting that as "can't tell" would be the wrong claim.
		b.CrossingKnown = true
		if len(outs) == 0 {
			continue // nothing to reach; skip the walk, keep the answer
		}
		reached := i.forwardClosure(seeds)
		for _, o := range outs {
			if reached[o.site] {
				b.Reaches = append(b.Reaches, o.id)
			}
		}
	}
}

// containsCall reports whether any of these expressions contains a call,
// anywhere — including inside a function literal, which is the shape that
// matters most here (a handler or RunE closure held in a package-level var).
func containsCall(exprs []ast.Expr) bool {
	found := false
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if found {
				return false
			}
			if _, ok := n.(*ast.CallExpr); ok {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// nameIdent returns the identifier that names a call's function — the same
// token nameSpan spans — or nil when there is none (IIFE, conversions).
func nameIdent(fun ast.Expr) *ast.Ident {
	switch f := fun.(type) {
	case *ast.Ident:
		return f
	case *ast.SelectorExpr:
		return f.Sel
	case *ast.IndexExpr:
		return nameIdent(f.X)
	case *ast.IndexListExpr:
		return nameIdent(f.X)
	case *ast.ParenExpr:
		return nameIdent(f.X)
	default:
		return nil
	}
}

func (i *Indexer) resolveCall(parent *funcInfo, ce *ast.CallExpr) *callInfo {
	// Span only the function-name token, not the whole call expression.
	// This avoids overlapping decorations for nested calls (Shiki rejects
	// overlap). For example fmt.Sprintf("...", listener.Addr().String())
	// yields three disjoint spans on "Sprintf", "Addr", and "String".
	//
	// We skip immediately-invoked function literals — there's no name to
	// click on, and their body is inline anyway.
	spanPos, spanEnd, ok := nameSpan(ce.Fun)
	if !ok {
		return nil
	}
	// No id yet: ids are assigned per function after the walk (see
	// assignCallIDs), because a call's identity is its place in its function,
	// not its byte offset in a file.
	ci := &callInfo{
		parent: parent.id,
		pos:    spanPos,
		end:    spanEnd,
		kind:   KindIndirect, // overwritten below if resolvable
	}

	info := parent.pkg.TypesInfo
	if info == nil {
		return ci
	}

	// A conversion is not a call. `[]byte(s)`, `time.Duration(n)`, `(*T)(p)`
	// all parse as CallExpr, and nameSpan's fallback spans the whole type
	// expression — so each one became a call site with no name, no target and
	// nothing to expand: a decoration on `[]byte` that does nothing when
	// clicked. go/types answers this exactly rather than by shape, which
	// matters because a conversion to a named type is syntactically identical
	// to calling a function of that name.
	if tv, ok := info.Types[ce.Fun]; ok && tv.IsType() {
		return nil
	}

	switch fn := ce.Fun.(type) {
	case *ast.Ident:
		// foo()  — package-level function or local name
		if obj, ok := info.Uses[fn].(*types.Func); ok {
			ci.kind = KindDirect
			ci.target = TargetID(obj.FullName())
			ci.displayName = fn.Name
		} else {
			ci.displayName = fn.Name
		}

	case *ast.SelectorExpr:
		// x.Foo() — method call, package selector, or field-of-func
		ci.displayName = formatSelector(fn)
		if sel, ok := info.Selections[fn]; ok {
			// Real method/field selection.
			fnObj, _ := sel.Obj().(*types.Func)
			if fnObj == nil {
				return ci
			}
			recv := sel.Recv()
			if isInterface(recv) {
				ci.kind = KindInterface
				ci.candidates = i.candidatesFor(recv, fnObj.Name())
				return ci
			}
			ci.kind = KindDirect
			ci.target = TargetID(fnObj.FullName())
			return ci
		}
		// Package-qualified call: pkg.Func() — info.Uses[fn.Sel].
		if obj, ok := info.Uses[fn.Sel].(*types.Func); ok {
			ci.kind = KindDirect
			ci.target = TargetID(obj.FullName())
		}

	default:
		// Function literals invoked immediately, type conversions, etc.
		// Leave as indirect.
	}

	return ci
}

// ownsCode reports whether a function is part of the project being read, as
// opposed to a dependency.
//
// This is decided by file path, not by module metadata. `pkg.Module` is nil
// under vendored builds and some go.work configurations, and the previous
// `Module.Main` test then answered "no" for *every* function — which silently
// dropped every code-derived binding while the declared surface, which comes
// from protos rather than code, carried on looking fine. Path containment is
// what "this repo's own code" means anyway, and it's always available.
func (i *Indexer) ownsCode(fi *funcInfo) bool {
	if fi == nil {
		return false
	}
	if i.rootDir == "" {
		// Nothing to compare against; fall back to module metadata.
		return fi.pkg != nil && fi.pkg.Module != nil && fi.pkg.Module.Main
	}
	if fi.node == nil {
		return false
	}
	return underDir(i.fset.Position(fi.node.Pos()).Filename, i.rootDir)
}

// underDir reports whether path lies inside dir, comparing whole path
// segments so a sibling checkout like "orders-v2" isn't read as being inside
// "orders".
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isInterface(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// buildInterfaceImpls walks every named type in the loaded package set
// and, for every named interface, records the concrete types (or pointer
// types) that satisfy it. Anonymous interfaces and the empty interface
// are skipped — anonymous because they have no stable lookup key, empty
// because every type would qualify.
func buildInterfaceImpls(pkgs []*packages.Package) map[string][]types.Type {
	var (
		concretes []*types.Named
		ifaces    []*types.Named
	)
	seen := make(map[*types.Named]bool)
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.Types == nil {
			return
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			n, ok := obj.Type().(*types.Named)
			if !ok || seen[n] {
				continue
			}
			seen[n] = true
			if _, isIface := n.Underlying().(*types.Interface); isIface {
				ifaces = append(ifaces, n)
			} else {
				concretes = append(concretes, n)
			}
		}
	})

	impls := make(map[string][]types.Type)
	for _, iface := range ifaces {
		ifaceT, _ := iface.Underlying().(*types.Interface)
		if ifaceT == nil || ifaceT.NumMethods() == 0 {
			continue
		}
		key := interfaceKey(iface)
		if key == "" {
			continue
		}
		for _, n := range concretes {
			switch {
			case types.Implements(n, ifaceT):
				impls[key] = append(impls[key], n)
			case types.Implements(types.NewPointer(n), ifaceT):
				impls[key] = append(impls[key], types.NewPointer(n))
			}
		}
		// Stable order so candidate indexes are deterministic.
		sort.Slice(impls[key], func(a, b int) bool {
			return types.TypeString(impls[key][a], nil) < types.TypeString(impls[key][b], nil)
		})
	}
	return impls
}

// interfaceKey returns a stable string key for a named interface type.
func interfaceKey(n *types.Named) string {
	obj := n.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// candidatesFor enumerates concrete-method candidates for a call through
// the given interface receiver and method name.
func (i *Indexer) candidatesFor(recv types.Type, methodName string) []Candidate {
	named, ok := recv.(*types.Named)
	if !ok {
		// Anonymous interface — leave candidates empty; the frontend will
		// render a non-expandable interface call.
		return nil
	}
	key := interfaceKey(named)
	if key == "" {
		return nil
	}
	implTypes := i.interfaceImpls[key]
	if len(implTypes) == 0 {
		return nil
	}

	out := make([]Candidate, 0, len(implTypes))
	for _, t := range implTypes {
		ms := types.NewMethodSet(t)
		for j := 0; j < ms.Len(); j++ {
			fn, _ := ms.At(j).Obj().(*types.Func)
			if fn == nil || fn.Name() != methodName {
				continue
			}
			// Only surface methods we actually have FuncDecl source for —
			// i.e. they're in the loaded package set.
			id := TargetID(fn.FullName())
			if _, known := i.funcs[id]; !known {
				continue
			}
			out = append(out, Candidate{
				TargetID: id,
				Label:    types.TypeString(t, types.RelativeTo(named.Obj().Pkg())) + "." + fn.Name(),
			})
			break
		}
	}
	return out
}

func formatSelector(s *ast.SelectorExpr) string {
	switch x := s.X.(type) {
	case *ast.Ident:
		return x.Name + "." + s.Sel.Name
	default:
		return s.Sel.Name
	}
}

// nameSpan returns the byte range of the function-name token in a call's
// Fun expression. For x.Method() it's the range of "Method"; for plain
// identifiers and qualified names it's the identifier itself. Returns
// false for function literals invoked immediately, which have no name
// to click on.
func nameSpan(fun ast.Expr) (token.Pos, token.Pos, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Pos(), f.End(), true
	case *ast.SelectorExpr:
		return f.Sel.Pos(), f.Sel.End(), true
	case *ast.IndexExpr:
		// Generic instantiation: Foo[T](args). Span the indexed name.
		return nameSpan(f.X)
	case *ast.IndexListExpr:
		return nameSpan(f.X)
	case *ast.ParenExpr:
		return nameSpan(f.X)
	case *ast.FuncLit:
		// IIFE — no name token, skip.
		return 0, 0, false
	default:
		// Type conversions, less common forms — fall back to the full Fun.
		// These rarely nest inside other calls in a way that overlaps.
		return f.Pos(), f.End(), true
	}
}

// Frame returns a Frame for the given target. Returns an error if the
// target is unknown or its body source can't be read.
func (i *Indexer) Frame(id TargetID) (*Frame, error) {
	if path, ok := strings.CutPrefix(string(id), "file:"); ok {
		return i.fileFrame(path)
	}
	i.mu.RLock()
	fi, ok := i.funcs[id]
	i.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown target %q", id)
	}
	if fi.body == nil {
		return nil, fmt.Errorf("target %q has no body", id)
	}

	startPos := i.fset.Position(fi.node.Pos())
	endPos := i.fset.Position(fi.node.End())
	src, err := i.readRange(startPos.Filename, startPos.Offset, endPos.Offset)
	if err != nil {
		return nil, err
	}

	// Span offsets are reported as UTF-16 code-unit indices into Source,
	// because the frontend indexes the source as a JavaScript (UTF-16)
	// string. Emitting raw UTF-8 byte offsets would drift the highlight
	// right by one unit per extra byte of any non-ASCII rune before the
	// span (e.g. an em-dash in a comment is 3 bytes but 1 UTF-16 unit).
	calls := make([]CallSite, 0, len(fi.calls))
	base := startPos.Offset
	for _, c := range fi.calls {
		byteStart := i.fset.Position(c.pos).Offset - base
		byteEnd := i.fset.Position(c.end).Offset - base
		cs := CallSite{
			ID:          c.id,
			SpanStart:   utf16Offset(src, byteStart),
			SpanEnd:     utf16Offset(src, byteEnd),
			DisplayName: c.displayName,
			Kind:        c.kind,
			TargetID:    c.target,
			Candidates:  c.candidates,
			Goroutine:   c.goroutine,
			External:    (c.kind == KindDirect || c.kind == KindRef) && i.isExternal(c.target),
		}
		// Leaves are keyed by file:line, and a line holds more than one site
		// now that a value reference is one: `Subscribe(defn, handleFoo)` is a
		// call *and* a reference, and both matched the line. A rule matched
		// the call — the evaluator never sees references — so a reference must
		// not inherit its boundary, or the card renders once per site on the
		// line instead of once per boundary.
		if d, ok := i.leaves[i.siteLineOf(c)]; ok && c.kind != KindRef {
			cs.Leaf = &model.LeafInfo{
				Rule: d.RuleID, Label: d.Label,
				Key: d.Key, Kind: d.Kind, Role: d.Role, CrossRepo: d.CrossRepo,
			}
			if d.Expand != nil {
				// A rule overrides the stdlib/dependency heuristic in either
				// direction: rescuing an in-house SDK from being skipped, or
				// marking an expandable-but-pointless call as a boundary.
				cs.External = !*d.Expand
			}
		}
		calls = append(calls, cs)
	}
	return &Frame{
		ID:        id,
		Title:     fi.title,
		File:      startPos.Filename,
		Language:  "go",
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		Source:    string(src),
		Calls:     calls,
	}, nil
}

// goTitle returns a short, display-friendly name for a function: "Func" for
// package functions, "Recv.Method" for methods. (The TargetID is the fully
// qualified FullName, which is too long for a header or bookmark label.)
func goTitle(obj *types.Func) string {
	name := obj.Name()
	sig, _ := obj.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return name
	}
	recv := sig.Recv().Type()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	if named, ok := recv.(*types.Named); ok {
		return named.Obj().Name() + "." + name
	}
	return name
}

// Files returns the sorted, distinct absolute paths of files that hold at
// least one indexed function. Only files in the main module are listed —
// dependency and stdlib sources (also loaded for resolution) are excluded
// so the tree shows the project the user is reading, not its dep graph.
func (i *Indexer) Files() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	set := make(map[string]struct{}, len(i.funcs))
	for _, fi := range i.funcs {
		if fi.pkg == nil || fi.pkg.Module == nil || !fi.pkg.Module.Main {
			continue
		}
		set[i.fset.Position(fi.node.Pos()).Filename] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// isExternal reports whether a direct target's package lives outside the
// main module (stdlib or a dependency). Such calls are still expandable,
// but the frontend's bulk "+1 level" skips them. (The TS engine never sets
// this: it only registers project files, so external calls already carry
// no target.)
func (i *Indexer) isExternal(id TargetID) bool {
	if id == "" {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	fi, ok := i.funcs[id]
	if !ok {
		return false
	}
	return fi.pkg == nil || fi.pkg.Module == nil || !fi.pkg.Module.Main
}

// fileFrame builds a Frame for a whole file: the full source plus every call
// site across all of the file's functions, with offsets relative to the file
// start. The call IDs match those produced during indexing, so expanding a
// call from the file view works through the normal FrameForCall path.
func (i *Indexer) fileFrame(path string) (*Frame, error) {
	buf, err := i.readFile(path)
	if err != nil {
		return nil, err
	}

	i.mu.RLock()
	var infos []*callInfo
	for _, fi := range i.funcs {
		if i.fset.Position(fi.node.Pos()).Filename != path {
			continue
		}
		infos = append(infos, fi.calls...)
	}
	i.mu.RUnlock()

	sort.Slice(infos, func(a, b int) bool { return infos[a].pos < infos[b].pos })
	calls := make([]CallSite, 0, len(infos))
	for _, c := range infos {
		calls = append(calls, CallSite{
			ID:          c.id,
			SpanStart:   utf16Offset(buf, i.fset.Position(c.pos).Offset),
			SpanEnd:     utf16Offset(buf, i.fset.Position(c.end).Offset),
			DisplayName: c.displayName,
			Kind:        c.kind,
			TargetID:    c.target,
			Candidates:  c.candidates,
			Goroutine:   c.goroutine,
			External:    (c.kind == KindDirect || c.kind == KindRef) && i.isExternal(c.target),
		})
	}

	return &Frame{
		ID:        TargetID("file:" + path),
		Title:     filepath.Base(path),
		File:      path,
		Language:  "go",
		StartLine: 1,
		EndLine:   1 + strings.Count(string(buf), "\n"),
		Source:    string(buf),
		Calls:     calls,
	}, nil
}

// readFile returns the full bytes of path, caching like readRange.
func (i *Indexer) readFile(path string) ([]byte, error) {
	i.fileBytesMu.Lock()
	defer i.fileBytesMu.Unlock()
	if buf, ok := i.fileBytes[path]; ok {
		return buf, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	i.fileBytes[path] = b
	return b, nil
}

// TypeInfo resolves the identifier at a UTF-16 offset into the frame's source
// and reports its type details. Returns nil (no error) when the offset isn't
// over a resolvable identifier. A negative offset describes the target's own
// declaration — used by note references, which know a target but no hover
// position.
func (i *Indexer) TypeInfo(id TargetID, offset int) (*TypeInfo, error) {
	if offset < 0 {
		return i.describeTarget(id)
	}
	var (
		srcBase  int // byte offset in the file where the frame source starts
		fileName string
		astFile  *ast.File
		pkg      *packages.Package
	)
	if path, ok := strings.CutPrefix(string(id), "file:"); ok {
		astFile, pkg = i.astFileFor(path)
		fileName = path
	} else {
		i.mu.RLock()
		fi, ok := i.funcs[id]
		i.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown target %q", id)
		}
		start := i.fset.Position(fi.node.Pos())
		srcBase, fileName, pkg = start.Offset, start.Filename, fi.pkg
		astFile = fileContaining(fi.node, fi.pkg)
	}
	if astFile == nil || pkg == nil || pkg.TypesInfo == nil {
		return nil, nil
	}

	buf, err := i.readFile(fileName)
	if err != nil {
		return nil, err
	}
	if srcBase > len(buf) {
		return nil, nil
	}
	abs := srcBase + byteOffsetForUTF16(buf[srcBase:], offset)

	tf := i.fset.File(astFile.Pos())
	if tf == nil || abs < 0 || abs > tf.Size() {
		return nil, nil
	}
	pos := tf.Pos(abs)

	enclosing, _ := astutil.PathEnclosingInterval(astFile, pos, pos)
	var ident *ast.Ident
	for _, n := range enclosing {
		if id2, ok := n.(*ast.Ident); ok {
			ident = id2
			break
		}
	}
	if ident == nil {
		return nil, nil
	}
	obj := pkg.TypesInfo.ObjectOf(ident)
	if obj == nil {
		return nil, nil
	}

	ti := &TypeInfo{
		Name:       ident.Name,
		Kind:       objKind(obj),
		Type:       types.TypeString(obj.Type(), types.RelativeTo(pkg.Types)),
		Definition: typeDefinition(obj.Type(), types.RelativeTo(pkg.Types)),
	}
	if obj.Pos().IsValid() {
		dp := i.fset.Position(obj.Pos())
		ti.DefinedAt = fmt.Sprintf("%s:%d", dp.Filename, dp.Line)
	}
	if fn, ok := obj.(*types.Func); ok {
		// Only expose TargetID for functions we actually indexed — otherwise
		// the hover card would offer "open" on a stdlib/dependency func that
		// LookupSymbol can't resolve, yielding a dead link. (The TS engine
		// gates this the same way.)
		fullName := TargetID(fn.FullName())
		i.mu.RLock()
		if dfi, ok := i.funcs[fullName]; ok {
			ti.TargetID = fullName
			if dfi.doc != "" {
				ti.Doc = dfi.doc
			}
		}
		i.mu.RUnlock()
		// What already claims this call site. Keyed by line, which is how the
		// evaluator records a match, so two calls on one line share an answer
		// — the same resolution the leaf classification has always used.
		p := i.fset.Position(ident.Pos())
		ti.Rules = i.RulesAt(p.Filename, p.Line)
		// And what a new rule could match on. Only when the hovered identifier
		// is the *called* function: hovering an argument describes that
		// argument, and offering to write a rule keyed off it would be
		// describing a different call site than the one on screen.
		if ce := calleeCallExpr(enclosing, ident); ce != nil {
			if c, ok := i.callFactsIn(pkg, "", ce); ok {
				ti.Call = &CallFacts{
					Package: c.PkgPath,
					Recv:    c.Recv,
					RecvPkg: c.RecvPkg,
					Func:    c.Func,
				}
				for _, a := range c.Args {
					ti.Call.Args = append(ti.Call.Args, model.ArgFacts{
						Type: a.Type, ParamType: a.ParamType, Value: a.Value,
					})
				}
			}
		}
	}
	return ti, nil
}

// calleeCallExpr returns the call whose *callee* is ident, or nil. The
// enclosing path runs innermost-out, so the first CallExpr encountered is the
// nearest one; it counts only if ident is what names the function, not if
// ident merely appears among the arguments.
func calleeCallExpr(enclosing []ast.Node, ident *ast.Ident) *ast.CallExpr {
	for _, n := range enclosing {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			continue
		}
		switch fn := ce.Fun.(type) {
		case *ast.Ident:
			if fn == ident {
				return ce
			}
		case *ast.SelectorExpr:
			if fn.Sel == ident {
				return ce
			}
		}
		return nil
	}
	return nil
}

// typeDefinition expands a type's shape when the name alone isn't telling:
// a named struct's fields, a named interface's methods, or a named alias's
// underlying type. Pointers are dereferenced first. Returns "" for types
// whose TypeString already says everything (basics, slices of basics,
// funcs, unnamed types).
func typeDefinition(t types.Type, qual types.Qualifier) string {
	for {
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
			continue
		}
		break
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	switch u := named.Underlying().(type) {
	case *types.Struct:
		if u.NumFields() == 0 {
			return "struct{}"
		}
		var b strings.Builder
		b.WriteString("struct {\n")
		for f := 0; f < u.NumFields(); f++ {
			field := u.Field(f)
			b.WriteString("    ")
			if !field.Embedded() {
				b.WriteString(field.Name())
				b.WriteString(" ")
			}
			b.WriteString(types.TypeString(field.Type(), qual))
			b.WriteString("\n")
		}
		b.WriteString("}")
		return b.String()
	case *types.Interface:
		if u.NumMethods() == 0 {
			return "interface{}"
		}
		var b strings.Builder
		b.WriteString("interface {\n")
		for m := 0; m < u.NumMethods(); m++ {
			fn := u.Method(m)
			sig := types.TypeString(fn.Type(), qual)
			b.WriteString("    ")
			b.WriteString(fn.Name())
			b.WriteString(strings.TrimPrefix(sig, "func"))
			b.WriteString("\n")
		}
		b.WriteString("}")
		return b.String()
	case *types.Signature:
		return "" // the Type field already shows the signature
	default:
		// Named alias of a basic/slice/map/chan: show what it really is.
		def := types.TypeString(u, qual)
		if def == types.TypeString(named, qual) {
			return ""
		}
		return def
	}
}

// declType renders a target's type: a signature for a function, the declared
// variable's type for an initializer. Both answer "what is this", which is
// what the card asks — they just live in different halves of go/types.
func (i *Indexer) declType(fi *funcInfo) string {
	if fi.obj != nil {
		return types.TypeString(fi.obj.Type(), types.RelativeTo(fi.pkg.Types))
	}
	vs, ok := fi.node.(*ast.ValueSpec)
	if !ok || fi.pkg == nil || fi.pkg.TypesInfo == nil || len(vs.Names) == 0 {
		return ""
	}
	obj, _ := fi.pkg.TypesInfo.Defs[vs.Names[0]].(*types.Var)
	if obj == nil {
		return ""
	}
	return types.TypeString(obj.Type(), types.RelativeTo(fi.pkg.Types))
}

// describeTarget builds the TypeInfo of a target's own declaration.
func (i *Indexer) describeTarget(id TargetID) (*TypeInfo, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	fi, ok := i.funcs[id]
	if !ok {
		return nil, nil
	}
	kind := "func"
	if fi.initializer {
		kind = "var"
	}
	ti := &TypeInfo{
		Kind:     kind,
		Name:     fi.title,
		Type:     i.declType(fi),
		TargetID: id,
	}
	dp := i.fset.Position(fi.node.Pos())
	ti.DefinedAt = fmt.Sprintf("%s:%d", dp.Filename, dp.Line)
	if fi.doc != "" {
		ti.Doc = fi.doc
	}
	return ti, nil
}

// astFileFor finds the parsed file and its package for a given path.
func (i *Indexer) astFileFor(path string) (*ast.File, *packages.Package) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, pkg := range i.pkgs {
		for _, f := range pkg.Syntax {
			if i.fset.Position(f.Pos()).Filename == path {
				return f, pkg
			}
		}
	}
	return nil, nil
}

func fileContaining(node ast.Node, pkg *packages.Package) *ast.File {
	for _, f := range pkg.Syntax {
		if f.Pos() <= node.Pos() && node.Pos() < f.End() {
			return f
		}
	}
	return nil
}

func objKind(obj types.Object) string {
	switch o := obj.(type) {
	case *types.Var:
		if o.IsField() {
			return "field"
		}
		return "var"
	case *types.Func:
		return "func"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.PkgName:
		return "package"
	case *types.Label:
		return "label"
	case *types.Builtin:
		return "builtin"
	default:
		return "symbol"
	}
}

// FrameForCall returns a Frame for the chosen target of the given call.
//
// For direct calls, choice is ignored.
// For interface calls, choice indexes into the call's Candidates list
// (clamped to a valid range; choice<0 or out-of-range becomes 0).
// Indirect calls (function values, builtins like make/len) are not
// expandable — FrameForCall returns an error in that case.
//
// Returns ErrNoCandidates if an interface call has zero known candidates.
func (i *Indexer) FrameForCall(id CallID, choice int) (*Frame, error) {
	i.mu.RLock()
	c, ok := i.callsByID[id]
	i.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown call %q", id)
	}
	switch c.kind {
	case KindDirect, KindRef:
		// A reference to a named function resolves like a direct call: one
		// target, no dispatch to choose between. A reference to an *interface*
		// method has the same shape as an interface call — several bodies it
		// may name — so it falls through to the same choice.
		if c.target != "" {
			return i.Frame(c.target)
		}
		if len(c.candidates) == 0 {
			return nil, ErrNoCandidates
		}
		if choice < 0 || choice >= len(c.candidates) {
			choice = 0
		}
		return i.Frame(c.candidates[choice].TargetID)
	case KindInterface:
		if len(c.candidates) == 0 {
			return nil, ErrNoCandidates
		}
		if choice < 0 || choice >= len(c.candidates) {
			choice = 0
		}
		return i.Frame(c.candidates[choice].TargetID)
	default:
		return nil, fmt.Errorf("call %q is %s; not expandable", id, c.kind)
	}
}

// ErrNoCandidates is returned by FrameForCall when an interface call has
// no known concrete implementations in the loaded package set.
var ErrNoCandidates = fmt.Errorf("no candidate implementations found for interface call")

// Usages returns the places the target is referenced inside indexed
// function bodies, sorted by file then line. Excerpts are a few lines of
// context around the usage, clamped to the enclosing function's body.
func (i *Indexer) Usages(id TargetID) ([]model.Usage, error) {
	i.mu.RLock()
	if _, ok := i.funcs[id]; !ok {
		i.mu.RUnlock()
		return nil, fmt.Errorf("unknown target %q", id)
	}
	infos := i.usagesByTarget[id]
	out := make([]model.Usage, 0, len(infos))
	for _, u := range infos {
		parent, ok := i.funcs[u.parent]
		if !ok {
			continue
		}
		pos := i.fset.Position(u.pos)
		usage := model.Usage{
			Choice:      u.choice,
			Caller:      u.parent,
			CallerTitle: parent.title,
			File:        pos.Filename,
			Line:        pos.Line,
			Kind:        u.kind,
		}
		if u.call != nil {
			usage.CallID = u.call.id
		}
		usage.Excerpt, usage.ExcerptLine = i.excerpt(
			pos.Filename,
			pos.Line,
			i.fset.Position(parent.node.Pos()).Line,
			i.fset.Position(parent.node.End()).Line,
		)
		out = append(out, usage)
	}
	i.mu.RUnlock()

	sort.Slice(out, func(a, b int) bool {
		if out[a].File != out[b].File {
			return out[a].File < out[b].File
		}
		if out[a].Line != out[b].Line {
			return out[a].Line < out[b].Line
		}
		return out[a].Kind < out[b].Kind
	})
	return out, nil
}

// excerpt returns up to two lines of context either side of line, clamped
// to [bodyStart, bodyEnd] (the enclosing function), plus the 1-based file
// line the excerpt starts at.
func (i *Indexer) excerpt(file string, line, bodyStart, bodyEnd int) (string, int) {
	buf, err := i.readFile(file)
	if err != nil {
		return "", 0
	}
	lines := strings.Split(string(buf), "\n")
	start := max(line-2, max(bodyStart, 1))
	end := min(line+2, min(bodyEnd, len(lines)))
	if start > end {
		return "", 0
	}
	return strings.Join(lines[start-1:end], "\n"), start
}

// assignCallIDs gives every site an id built from the function it is in and
// its position among that function's sites, in source order.
//
// The id used to be the file and the byte offset of the name token. That is
// unique and cheap, and it changes when anything above it in the file changes
// — so a watch-mode reindex after any edit invalidated every expansion in the
// view at once, and the reader's whole trace collapsed to its root frame on
// save. It also made a shared URL good for exactly one revision of the file.
//
// A function's identity is its name, which survives edits elsewhere, and a
// call's identity within it is its ordinal. Adding a call *inside* a function
// still renumbers that function's later sites — but that is an edit to the
// very code being read, where losing your place is expected, rather than one
// three hundred lines above it.
//
// Ordinals are assigned after the walk rather than during it because the
// walk finds a chained call's inner and outer sites out of source order —
// `sdk.New().Get(ctx)` yields Get before New — and an ordinal that depended on
// traversal order would be stable in exactly the way that doesn't matter.
func (i *Indexer) assignCallIDs() {
	for _, fi := range i.funcs {
		sort.Slice(fi.calls, func(a, b int) bool { return fi.calls[a].pos < fi.calls[b].pos })
		for n, c := range fi.calls {
			c.id = CallID(fmt.Sprintf("%s@%d", fi.id, n))
			i.callsByID[c.id] = c
		}
	}
}

// Position reports where a target is defined, without building its frame.
// Frame reads the source range off disk and highlights it; a caller that only
// wants to *name* a location — a boundary saying where it leads — shouldn't
// pay for the body it isn't showing.
func (i *Indexer) Position(id TargetID) (string, int, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	fi, ok := i.funcs[id]
	if !ok || fi.node == nil {
		return "", 0, false
	}
	pos := i.fset.Position(fi.node.Pos())
	return pos.Filename, pos.Line, true
}

// LookupSymbol resolves a symbol name (qualified or unqualified) to a
// target. If multiple match, the first lexicographic FullName wins.
func (i *Indexer) LookupSymbol(name string) (TargetID, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if name == "" {
		return "", fmt.Errorf("empty symbol")
	}

	// A file pseudo-target ("file:<path>") resolves to itself; Frame builds
	// the whole-file view.
	if strings.HasPrefix(name, "file:") {
		return TargetID(name), nil
	}

	// Exact full-name match wins.
	if _, ok := i.funcs[TargetID(name)]; ok {
		return TargetID(name), nil
	}

	// Otherwise: case-sensitive suffix match on the basename.
	var candidates []TargetID
	for id := range i.funcs {
		if matchesSymbol(string(id), name) {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no symbol matches %q", name)
	}
	sort.Slice(candidates, func(a, b int) bool {
		return string(candidates[a]) < string(candidates[b])
	})
	return candidates[0], nil
}

func matchesSymbol(full, query string) bool {
	// Match query against the basename: everything after the last '.' for
	// package functions, after the last ".(*T)." or ".(T)." for methods.
	dot := strings.LastIndex(full, ".")
	if dot < 0 {
		return full == query
	}
	return full[dot+1:] == query || full == query
}

// Search returns up to `limit` symbols whose FullName contains query
// (case-insensitive).
//
// Two things order the hits, in this order:
//
//   - This repo's own code before dependency and stdlib code. Deps are indexed
//     for resolution, not because anyone searches for them, and a name common
//     enough to appear in a library ("Load", "Get", "New") would otherwise bury
//     the project's own definition under code the reader can't change.
//   - Within a tier, matches on the leaf name — the method or function name,
//     the part after the last "." — above matches that only hit the receiver
//     type or package path, since a search is almost always for the
//     method/function itself.
//
// Ranking happens before the limit is applied, so a strong match is never
// dropped in favor of an alphabetically-earlier weak one.
func (i *Indexer) Search(query string, limit int) []SearchResult {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(query)

	ids := make([]TargetID, 0, len(i.funcs))
	for id := range i.funcs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return string(ids[a]) < string(ids[b]) })

	type hit struct {
		res  SearchResult
		leaf bool // query matched the method/function name, not just the receiver/package
	}
	hits := make([]hit, 0, len(ids))
	for _, id := range ids {
		s := strings.ToLower(string(id))
		if q != "" && !strings.Contains(s, q) {
			continue
		}
		fi := i.funcs[id]
		pos := i.fset.Position(fi.node.Pos())
		hits = append(hits, hit{
			res: SearchResult{
				TargetID: id,
				Label:    string(id),
				File:     pos.Filename,
				Line:     pos.Line,
				External: !i.ownsCode(fi),
			},
			leaf: q == "" || strings.Contains(leafName(s), q),
		})
	}

	// Stable so the alphabetical order within each tier is preserved. Rank is
	// computed rather than compared field-by-field so the two criteria stay in
	// a stated priority: owning the code outranks matching better.
	rank := func(h hit) int {
		r := 0
		if h.res.External {
			r += 2
		}
		if !h.leaf {
			r++
		}
		return r
	}
	sort.SliceStable(hits, func(a, b int) bool { return rank(hits[a]) < rank(hits[b]) })

	out := make([]SearchResult, 0, limit)
	for _, h := range hits {
		out = append(out, h.res)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// leafName returns the final dot-separated segment of a Go FullName — the bare
// method or function name, without the receiver type or package path. For
// "(github.com/x/pkg.Indexer).Load" it returns "load" (given a lowercased
// input); for "github.com/x/pkg.Validate" it returns "validate".
func leafName(fullName string) string {
	if dot := strings.LastIndex(fullName, "."); dot >= 0 {
		return fullName[dot+1:]
	}
	return fullName
}

// readRange reads bytes [start, end) from filename, caching file contents.
func (i *Indexer) readRange(filename string, start, end int) ([]byte, error) {
	i.fileBytesMu.Lock()
	defer i.fileBytesMu.Unlock()

	buf, ok := i.fileBytes[filename]
	if !ok {
		b, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filename, err)
		}
		i.fileBytes[filename] = b
		buf = b
	}
	if start < 0 || end > len(buf) || start > end {
		return nil, fmt.Errorf("range [%d,%d) out of bounds for %s (len %d)", start, end, filename, len(buf))
	}
	return buf[start:end], nil
}

// SetRules installs the configured recognizers. Like the proto root it's a
// process-wide choice applied before Load, and it must survive the engine
// rebuilds watch mode performs.
func (i *Indexer) SetRules(s rules.Set) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ruleSet = s
}

// RuleReport describes what the configured rules did, so a rule that has
// quietly stopped matching says so instead of contributing nothing in silence.
func (i *Indexer) RuleReport() model.RuleReport {
	i.mu.RLock()
	defer i.mu.RUnlock()
	rep := model.RuleReport{Problems: i.ruleSet.Problems}
	for _, b := range platform.Builtins {
		rep.Rules = append(rep.Rules, model.RuleInfo{
			ID: b.ID, Doc: b.Doc, Builtin: true,
			Enabled: !i.ruleSet.Disabled()[b.ID],
			Matches: i.ruleStats[b.ID],
		})
	}
	for _, r := range i.ruleSet.Rules {
		if isBuiltinID(r.ID) {
			continue // a settings-only entry toggling a built-in, already listed
		}
		spec, err := json.Marshal(r)
		if err != nil {
			spec = nil // unshowable, not unusable: the rest of the row stands
		}
		rep.Rules = append(rep.Rules, model.RuleInfo{
			ID: r.ID, Doc: r.Comment, Enabled: r.On(),
			Source:  i.ruleSet.Sources[r.ID],
			Matches: i.ruleStats[r.ID],
			Spec:    spec,
		})
	}
	return rep
}

func isBuiltinID(id string) bool {
	for _, b := range platform.Builtins {
		if b.ID == id {
			return true
		}
	}
	return false
}

// siteLineOf identifies a call the way the rule evaluator recorded it.
func (i *Indexer) siteLineOf(c *callInfo) string {
	p := i.fset.Position(c.pos)
	return p.Filename + ":" + strconv.Itoa(p.Line)
}
