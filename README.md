# unfold

A code-reading tool that lets you follow execution paths *linearly* by expanding function calls inline into their implementations, recursively, across files.

When reading code that's heavily decomposed (DI, layered services, lots of small functions), you spend most of your time jumping between files trying to hold the call stack in your head. `unfold` lets you pick a starting symbol, then click any call site to splice the callee's body in directly below — recursively — so a multi-file execution path reads top-to-bottom in one view.

See [`PLAN.md`](./PLAN.md) for architecture, scope, and phasing.

## Usages / callers

Unfolding follows execution *downward*; the usages feature is the reverse
direction. **▲ callers** in any frame header lists where that function is
referenced; picking one re-roots the view so the caller reads as spliced
above (the frame you clicked from keeps its expansion state, nested at the
picked call site). The **callers** tab in the left sidebar is the same data as an
inverted tree: expand to walk toward entry points, click a node to load the
whole chain as one pre-unfolded view.

Three usage kinds:

- `call` — a direct call to the function.
- `iface` — a call through an interface the function's receiver implements;
  execution *may* dispatch here. Re-rooting through one selects the right
  implementation in the impl switcher automatically.
- `ref` — a value reference: the function is passed or stored as a value
  (`apply(myFunc)`, `mux.HandleFunc("/x", s.handler)`), not called at that
  site.

### Known limitations

- **A `ref` can't be a link in a spliced chain.** Inline expansion splices a
  body at a *call site*; a value reference has no call site — nothing
  executes on that line, the function value just flows somewhere. So picking
  a `ref` opens its enclosing function as a bare new root (your current
  expansion subtree can't nest into it), and in the callers tree, loading a
  chain that passes *through* a `ref` link drops everything below the ref:
  the view shows the outer chain but not the function you started from.
  The tree still shows ref edges because they answer "where does this
  reach" — but they're data-flow edges, not control-flow edges. The UI
  marks the difference: ref entries are dashed/italic with a ⤳ glyph and an
  "opens bare" note, and tree nodes whose chain passes through a ref carry
  a "partial" badge.
- **A package-level variable is indexed only if its initializer contains a
  call.** `var cmd = &cobra.Command{RunE: func(){ client.Do() }}` is a target
  in its own right: it opens as a frame, its calls resolve, and it shows up in
  usages and the callers tree as the caller. Variables holding no call
  (`var timeout = 5 * time.Second`) are skipped — they aren't code to read,
  and indexing them would bury the ones that are. A `var handler = myFunc`
  with no call is therefore still not walked for the value reference.
- **TypeScript**: references inside Angular template HTML aren't covered
  (templates aren't TS AST nodes); `new Foo()` doesn't count as a usage of
  the class (constructors aren't frames); a usage inside an inline
  `subscribe` callback is attributed to the registered subscriber
  pseudo-target, so the caller label reads "subscriber" rather than the
  enclosing method.
- **Recursion isn't cycle-guarded.** A self-recursive function lists itself
  as a caller and the tree can be expanded indefinitely (expansion is
  user-driven, so this is the same behavior as IDE call hierarchies).
- **Interface usages enumerate possibilities, not certainties.** A concrete
  method's usage list includes every interface call site that *could*
  dispatch to it within the loaded package set, including sites that only
  ever dispatch to a different implementation at runtime.

## Zooming out — the service view

Unfolding follows one execution path downward. **Zoom-out** goes the other
way on a different axis: it shows the service as a whole — everything that
enters it, and everything it reaches out to.

Press **alt+↑** (or **▴ service** in the root frame header, or the service
name in the trail above the frame) to zoom out; **alt+↓**, or the frame name
in the trail, comes back. Nothing is unloaded, so zooming is lossless —
your expansion state is exactly as you left it.

The service view is a two-sided card, not a graph:

- **inbound** — where work enters: HTTP routes registered with `net/http`,
  Pub/Sub subscriptions. Click one to open its handler as a new root frame.
- **outbound** — where the service reaches out: gRPC calls through a generated
  client, `http.Get`/`Post` calls with a statically known URL, Pub/Sub topics it
  names. These have no in-repo target (the far end lives in another service) so
  they open their call site.

  A gRPC call resolves **exactly**, and it's answered from the call graph
  rather than by scanning for literals. A generated client method corresponds
  one-to-one with an RPC — it states the full method name as a literal in its
  own body, or as the `..._FullMethodName` constant newer codegen emits — so
  the service calls that RPC exactly when the service's own code calls that
  method. Finding those callers is a walk over the same index the callers tree
  uses, stopping at the first frame that belongs to this project.

  Two things fall out of that rather than being enforced. A generated stub
  nothing calls yields nothing: it has no callers, so it produces no edges —
  which matters because a repo generating its clients in-tree has one stub per
  RPC on the whole platform, and those are the *ability* to call, not calls.
  And the edge lands on the caller at the boundary where your code meets the
  client, so a caller-of-a-caller can't inherit it — the walk stops before
  reaching them.

  A hand-written function that issues the call itself is the call site, since
  it's business logic talking to grpc rather than a client standing in for an
  RPC.

  Finally, a call site only counts if execution can **reach** it from one of
  the service's entrypoints — its route handlers, the implementations of the
  RPCs it declares, any `init`, every package-level variable initializer, and
  every function of a valid `main` package. Commands count in their entirety,
  and initializers count because they run at program start — unconditionally,
  before `main`, the same reason `init` seeds. Both over-approximate in the
  same direction and for the same reason: this code exists to be run, and much
  of it is reached in ways a call graph can't show — a framework invoking a
  handler, a `RunE` closure held in a variable, a callback registered at
  startup. A repo can hold a client nothing ever invokes,
  and no amount of classifying the client tells you whether the service uses
  it; reachability answers that directly. When call sites are excluded this
  way the count is reported next to the column, because a service whose
  handlers are registered in a way unfold can't read would otherwise look like
  one that depends on nothing.

### The anchor

The frame you zoomed out from is carried up as the **anchor**. Every inbound
entry that transitively reaches it is highlighted and badged `reaches anchor`;
the rest dim. That's what keeps a large surface readable — with eighty routes
you're not reading eighty, you're seeing the two that concern you with the
rest as context. The trail names the count (`unfold › 3 entrypoints ›
validateCoupon`) because until you pick one, the entrypoint slot genuinely has
three answers.

The anchor answers two questions, and they need opposite walks:

- **`reaches anchor`** on an inbound row — this entrypoint runs the anchored
  code. Computed backwards, the same way the callers tree walks.
- **`anchor reaches`** on an outbound row — the anchored code makes this call.
  Computed forwards.

Marking outbound rows from the backwards walk would state something true (this
call is made by code that reaches the anchor) but not what the label claims,
so the two stay separate. Value references aren't followed in either
direction — a function passed as a value has no call site, so a chain through
one isn't an execution path.

The **saved anchors** list sits at the top of the sidebar (the ☆ in a frame
header adds one, and they persist per project). Since the anchor is whatever
frame is open, clicking an entry re-anchors every level at once, and the entry
you're currently on is marked.

### Both directions, without leaving the code

You usually want to know what reaches a function *while reading it*, not after
zooming away. So the two anchor walks also render beside the frame:

- **entrypoints** — a left sidebar tab at the frame level, listing the inbound
  bindings that reach the anchor.
- **outbounds** — a right panel tab, beside the call tree, listing the outbound
  calls the anchor reaches.

Neither fetches anything new. They're the same `/api/service` response the
service level renders in columns, filtered by the reachability flags already
on it, so the three views can't disagree. An empty panel says which kind of
empty it is — no surface recognized, no anchor, or a walk that ran and found
nothing.

### Tracing one column to the other

The two columns describe the same service, but neither says anything about the
other. The **⇄** on any row joins them:

- pick an **inbound** row → the outbound calls that entrypoint can cause.
- pick an **outbound** row → the entrypoints that can cause that call.

The connected rows light and the rest dim, the same treatment the anchor uses;
a trace is a transient focus and wins while it's held. The second direction is
the one that's genuinely hard by hand — it means walking callers until you hit
something registered as an entrypoint.

Only the first direction is computed and sent: for each inbound binding, the
outbound bindings its handler forward-reaches. The reverse is derived in the
browser, so there's one source of truth rather than two that can disagree.
Seeds are the handler and its candidates, never the registration site — a
function that registers a route doesn't run it, and seeding from the site
would blame every route for every call its registrar makes.

Three outcomes, and they are not the same:

- **reaches N calls** — the walk ran and found them.
- **reaches no outbound call** — the walk ran and found nothing. A fact.
- **unknown** — there was no indexed handler to walk from (a `publicRoutes`
  entry nothing registers, a proto method with no implementation). Reporting
  this as "calls nothing" would be the more confident claim and the wrong one,
  so the two are kept apart in the data rather than collapsed in the UI.

This is also the piece transitive anchor marking at the platform level needs:
propagating a mark from a service to its callers requires knowing, per repo,
which inbound key leads to which outbound call.

### What the service declares about itself

If the repo root has a `microservice.yaml`, unfold reads it. Declared facts are
the *strongest* resolution tier — a proto is the contract both a server and its
generated SDK are built from, so it needs no heuristics at all — but they're
also the ones that rot, so anything the manifest declares and the index can't
corroborate is shown and badged `stale` rather than presented as real surface.

```yaml
microservice:
  name: conversation
  protoPaths:
    - path: conversation/v1/api.proto
    - excludeFromSdk: conversation/v1/internal.proto
  publicRoutes:
    - /v1/conversations
```

- **`name`** replaces the repo directory as the service name.
- **`protoPaths`** contributes the declared gRPC surface. Those files are
  *parsed*, not compiled: only names are wanted, and a proto's fully-qualified
  names come from its own `package` and `service` declarations, so imports are
  never resolved. That matters in practice — proto import closures routinely
  reach outside the repository (`google/rpc/code.proto`, `google/api/*`), and
  linking would make a service's surface depend on vendoring decisions that
  have nothing to do with what it exposes. Every `rpc` becomes an inbound
  binding keyed
  `<proto package>.<Service>/<Method>` — the same key the generated SDK names
  on the calling side, which is what will join the two ends of a cross-service
  edge once more than one repo is indexed.

  Linking an RPC to its Go implementation narrows by the **generated server
  interface**: gRPC emits a `<Service>Server` interface carrying exactly that
  service's methods, so the types implementing it are the real answers. That's
  structural, not nominal — a decorator implements the interface and belongs in
  the list; a helper that merely shares a method name doesn't. Where no such
  interface is indexed it falls back to the name, minus generated clients,
  `Unimplemented*` stubs, test doubles and `_test.go` declarations.

  **Zero** candidates means nothing implements the RPC: that's stale.
  **Several** means unfold can't tell which — a service behind decorators, say
  — which is a different claim and not the manifest's fault. Those are
  *enumerated* rather than dropped: the row offers each implementation, the
  same way an interface call site offers its impls. An enumerated binding is
  still marked as reaching the anchor if any of its implementations does.
- **`excludeFromSdk`** marks surface implemented here but not callable from
  other services. Those methods are shown as **internal** rather than omitted —
  they exist, and later "nothing calls this" readings must not fire on them,
  because nothing *can*.
- **`publicRoutes`** classifies the inbound surface.

Proto paths are relative to the **shared proto repository**, not to the
service, so unfold can't find them on its own — point at it with
`--proto-root`:

```
unfold --proto-root ~/src/platform-protos ./...
```

Without it, declared proto paths are skipped and the view says so — and offers
a **directory picker** rather than sending you back to the command line. It's
server-backed browsing (the server lists directories; you can also just paste a
path) because a browser deliberately won't hand a page a real filesystem path
from a native picker. The choice is remembered in `.unfold/config.json`, so
`--proto-root` is only needed the first time — or never, if you pick it in the
UI. Changing it doesn't re-index: only the declared surface is recomputed.

A root where none of the declared protos are readable reports the error and
lets you pick again, dropping the surface built from the previous directory
rather than leaving a stale one on screen. Failures are per-file, though: one
unreadable proto doesn't hide the surface the others declare — it warns and
keeps going.

gRPC entries — inbound and outbound alike — cluster under their service, so
`FooService` is stated once and its rows are just `Foo` and `Bar`. Flat
fully-qualified names repeat the package on every line and push the part that
actually differs to the far end, where truncation eats it.

### Inbound is grouped by reach

What you want to know about an entrypoint first is who can get to it, so the
inbound column groups by visibility rather than by kind:

- **public** — reachable from outside the platform (in `publicRoutes`).
- **platform** — reachable by other services (proto-declared, in the SDK).
- **internal** — registered in code, named by neither list.

This also gives the first real drift check: a `publicRoutes` entry nothing
registers is stale, and so is a proto method with no implementation.

### Bindings and confidence

Inbound and outbound entries are **bindings**: a `kind` and a `key` extracted
by a recognizer. A publish and its subscriber share no AST edge — they share a
*string* — so the key is what will eventually join them once more than one repo
is indexed. Today only one end of a cross-service edge is visible, which is
why an outbound `GET /v1/orders` with nothing serving it is expected, not an
error.

Because platform edges often aren't literal, each binding records how it was
resolved, and anything short of `exact` is badged:

- **exact** — a literal (or constant-folded) string on both sides.
- **inferred** — the key is literal but something about it is a guess. A
  `client.Topic("orders-v1")` handle is marked this way: the topic name is
  certain, whether the code publishes to it is not.
- **declared** — asserted by `microservice.yaml`: a proto-declared RPC, or a
  route in `publicRoutes`.

Recognizers currently cover `net/http` route registration (including Go 1.22
`"POST /path"` patterns, host-qualified patterns, and handlers wrapped in
`http.HandlerFunc`), GCP Pub/Sub topic and subscription handles, and outbound
`net/http` client calls. A URL assembled at runtime is skipped rather than
guessed. Adding a router or broker means adding a rule in
`internal/platform` — recognizers see a neutral `Call` (package, receiver,
function, constant-folded args), never an AST.

### Limitations

- **Single repo.** Joining outbound keys to the services that serve them needs
  a multi-repo index, which doesn't exist yet — so an outbound key with nothing
  serving it is the expected state, not a defect.
- **Proto→Go linking is by name.** There's no declared link between an RPC and
  the method implementing it, so unfold matches on the bare name and only when
  it's unambiguous. A service whose methods collide with unrelated functions
  elsewhere in the module will show those RPCs unlinked.
- **Go only.** The TypeScript engine has no recognizers, so the zoom control
  is hidden when it's loaded.
- **Only `net/http`.** Third-party routers (chi, gin, echo) aren't recognized
  yet, so a service using one shows an empty inbound surface.
- **Handlers must be named functions.** A route registered with an inline
  closure has no target to open, so it falls back to the registration site.

## Workspaces — following a call into the other repo

Point unfold at a directory of sibling checkouts and every module under it is
opened together, so an outbound call can be followed into the service that
implements it:

```
unfold --workspace ~/src --proto-root ~/src/platform-protos ./...
```

### Linking a repo after launch

You usually find out mid-session — the call you're following lands somewhere
you didn't open. **+ link repo…** in the workspace strip (and in the platform
header) opens another repository without restarting, and it need not be a
sibling of anything: a linked repo is an arbitrary path.

This also works from a plain single-repo session, which is the common case:
link one repo and the session *becomes* a workspace, with the repo you
launched in as the primary. The link persists to `.unfold/config.json`, so it
survives a restart rather than being something you redo each morning.
Unlinking the last one drops back to a single-repo index.

Linking rebuilds the engine rather than mutating the open workspace. The repo
set, alias table and cross-repo declaration join are read without locks by
every request path on the assumption that they're fixed after startup;
mutating them live would mean auditing all of that for races, where a rebuild
is the mechanism watch mode already uses — it swaps atomically and keeps the
previous engine if the new one fails to build. The cost is re-indexing what
was eagerly loaded, which is the same reason a large workspace defers with
`--index lazy`.

A directory with no `go.mod`, one that doesn't exist, or one already open is
rejected *before* the rebuild — discovering it afterwards would mean reporting
a failure against an engine that had already been replaced, having paid the
reindex for nothing. And a rebuild that fails rolls the link back, so the next
rebuild for any other reason can't silently apply a repo you were told had
failed.

The repo you're standing in is the **primary**: the service view is about it,
and its ids stay unprefixed so existing URLs and bookmarks keep working. Other
repos are namespaced `<repo>::<id>`. Running from a subdirectory of a repo
still picks that repo; running from *outside* every repo has to fall back to
one of them, and says so on stderr rather than leaving you looking at a
service you didn't ask for.

Each repo logs a line as it's indexed — `indexed orders — 12 inbound, 4
outbound (7 declared rpc)` — because an empty column is otherwise
indistinguishable from a recognizer that found nothing.

Each outbound row then carries two actions — the key opens the **caller** in
this repo, and `→ <service>` opens the **implementation** in the other one.
Rows nothing serves stay as they are; within a single repo that's the normal
state, not an error.

### Two layers, because they cost differently

- **Declarations** — every repo's `microservice.yaml` and protos, read at
  startup. Milliseconds, no Go compilation, and already enough to answer
  *which service serves this key*. That's what makes `→ conversation` appear
  instantly even in a workspace of fifty repos.
- **Code** — a full index per repo: seconds and hundreds of megabytes each.
  Needed only to render a frame, so it's deferred until you actually jump.

`--index` selects when the code layer is built: `eager` up front, `lazy` on
demand, or `auto` (the default) which is eager for a small workspace and lazy
beyond four repos. The workspace strip at the top of the service view shows
which repos are indexed; search and cross-linking cover the ones that are.

### The platform level

With a workspace open there's a level above the service view: **alt+↑** again,
or `workspace` in the trail. It lists every service — those come from
declarations, so all of them appear however little has been indexed — and the
calls between them.

Edges can't work that way: knowing that A calls B means having read A's code.
So a service that hasn't been indexed contributes no *outgoing* edges, and the
view says so rather than showing it as a leaf that calls nothing. Each such
service carries an **index** button that reads just that one.

The overview is a **layered graph**: dependency direction runs left to right,
so the shape itself is the information — which services are entry points,
which are shared leaves, how deep the platform is. Edge thickness is the
number of RPCs along it. Layout is deterministic (cycle breaking, layer
assignment, barycenter ordering — no force simulation) so the picture is the
same every load and can be talked about.

Two things the layering has to get right, because both make it lie otherwise:

- **Edges that skip a layer are routed, not drawn straight.** With `A→B→C` and
  `A→C`, a straight `A→C` runs through B's column and arrives at C from the
  same direction B's edge does, so the picture reads as "A stops at B". Long
  edges are broken into per-layer waypoints that take their own row in the
  ordering, so `A→C` visibly bends around B and a node can never be stacked on
  top of an edge.
- **Cycles are broken before layering, preferring the lightest edge.** Left in,
  a single back edge inflates the depth of everything downstream: a
  `ledger→orders` call pushes `orders` past `billing` and `ledger`, and
  `gateway→orders` then has to snake across the whole graph to reach a service
  one hop away. The search starts at services nothing calls and walks heaviest
  edges first, so the loop is closed by the call carrying one RPC rather than
  the one carrying twelve. Back edges are drawn dashed and bowed clear of the
  layout.

Edge counts appear on demand — hovering a service, or an anchor lighting a
path — rather than on every edge at once, and they sit in the routing channels
the layout keeps clear of nodes. Printing every number always was clutter you
had to read past, and hovering then added more of it on top of the very nodes
you were trying to read.

The **anchor carries up to this level too**. Zoom out from a frame and the
services whose calls lead to it are lit while the rest dim — the same question
the service level answers with entrypoints, one granularity out. The RPCs that
lead there are marked individually, so an edge says *which* of its calls
matter.

Reach is **transitive**. With `gateway → inbox → conversation` and the anchor
inside `conversation`, gateway is marked even though it never calls
conversation at all: it calls inbox, and serving *that* RPC is what calls
conversation. Each link needs a different fact, which is why this needs the
crossing relation — the platform graph knows gateway calls inbox, but only
inbox's own index knows that serving `ShowThread` is what causes the call
onward.

Propagation is a fixpoint, because the service graph has cycles. Two things
are tracked separately, and conflating them would over-mark: whether a service
reaches the anchor, and *which of its own inbound keys* lead there. A service
can reach the anchor from a call made in its `init` or `main`, with no inbound
key responsible — it's marked, but its callers inherit nothing, because
nothing they could hit leads onward. That distinction is what keeps this
reachability rather than "everything upstream": walking the service graph
backwards without it would light the whole graph and mean nothing.

An unindexed service in the middle of a chain breaks it — everything behind it
goes unmarked, which looks identical to not reaching. The header says so when
an anchor is set.

Hovering a service dims everything it isn't connected to, and takes precedence
while held. Both answer "what is connected to the thing I care about", so they
share the dimming rather than competing for it.

Picking a service drops to the slice around it — callers on the left, it in
the middle, callees on the right, with the individual RPCs on each edge. Every
one of those lines opens the real call site, so the level changes but the
destination doesn't.

### The sidebar follows the level

Above the frame there is nothing frame-shaped to show, so the left sidebar
stops being files/callers/entrypoints/notes and becomes the **filter panel** —
text, reach (public/platform/internal) and "only entrypoints reaching the
anchor" at the service level, a service filter at the platform level. Filtering
is one mechanism across both upper levels rather than two bolted onto each
view. The right panel is frame-only for the same reason: the service columns
already show both directions up there.

### The URL carries the level, and history carries the moves

The hash encodes the whole view-state — `#symbol=…&zoom=…&svc=…&v=…` — so a
zoomed-out view is shareable and survives a reload, not just a frame.

The two axes stay separate, as the trail and history always meant to:

- **The trail is vertical.** Zooming never destroys what you came from, so
  clicking back down it is already the undo for a zoom.
- **History is lateral.** **alt+←/→** (or the browser's own back/forward) undo
  the moves that genuinely replace state — opening a symbol, re-rooting
  through a caller, picking a service — and cover zooms too, as a safety net.

Which transitions push is the whole design: expanding a call `replaceState`s,
because reading isn't navigating and back shouldn't be a per-click undo of
every fold you ever opened. Navigations `pushState`, and each is committed
once — opening a symbol sets the frame level and clears the service pick in a
*single* entry rather than three you have to press back through.

### Limitations

- **Discovery is one level deep.** A workspace is a directory of checkouts;
  walking deeper would index vendored copies and testdata modules.
- **Only gRPC edges join.** HTTP outbound keys are collected but not yet
  matched against other repos' route registrations.
- **Transitive anchor marking needs every service on the chain indexed.** A
  chain through an unindexed service can't be followed, so everything behind
  it goes unmarked.
- **A lazy workspace searches only what it has indexed.** Opening something in
  a repo indexes it and it stays in the results afterwards.
- **One anchor at a time.** The anchor is whatever frame is open, so the saved
  list re-anchors rather than accumulating. Marking several at once needs the
  backend walks to accept a set and union the results.

## Diff mode

Highlight what your branch changed, right where you're reading it. There's no
toggle in the UI — start unfold with `--diff-base <ref>`:

```
unfold --diff-base main
```

It indexes the **merge-base** of your working tree with `<ref>` in a throwaway
worktree and compares against it, so you see exactly what your branch changes
versus `main` (the PR diff). As you browse, frames are annotated in place:

- **added** — a function not present in the base: the whole frame is tinted and
  its header shows an `added` badge.
- **modified** — an existing function whose body changed: only the changed lines
  are tinted, with a `modified` badge.
- unchanged functions look normal.

So there's no separate diff screen — you navigate the call tree as usual and
changed code stands out inline.

- **Go only** for now. Diff identity is by package-qualified target id;
  TypeScript ids are positional, so `--diff-base` is ignored (with a log line)
  for TS projects until name-based identity lands.
- Diffs against the **merge-base**, so fetch/update the base ref first.
- Whole-file frames are left unannotated — their ids are path-based and don't
  match across the two worktrees.

## Notes

Anchored annotations over the code you're reading. Select line(s) and hit
**note** in the selection bar (single line → anchored after that line; a
multi-line selection → the range is tinted and the note follows it). Whole-
file frames get **note @ top** / **note @ end**. A note renders in every
frame containing its lines — anchors live in file space, so the same note
appears in a function frame and the whole-file view.

Reference code from note text with `[[SymbolName]]` (or a qualified
`[[Type.Method]]` to disambiguate) and `[[file:path/suffix.go]]`. Symbol
refs render like call sites, pop the same hover type card (signature,
doc, defined-at), and open the symbol as the root frame on click; file
refs open the whole-file view.

Notes persist to `.unfold/notes.json` under the project root — plain,
pretty-printed JSON that survives the browser and can be committed if you
want them shared (add `.unfold/` to `.gitignore` if you don't). If an
anchored line's text changes after an edit, the note shows a **⚠ drifted**
marker rather than silently pointing at the wrong place. The sidebar's
**notes** tab lists every note with jump-to.

## Repository layout

The Go module (CLI + HTTP server + indexers) is the repo root. Clients and
sidecars live alongside it as self-contained subprojects:

- `cmd/`, `internal/` — the Go core (CLI, server, Go indexer, diff, notes)
- `tsindexer/` — the TypeScript indexing sidecar (Bun + ts-morph)
- `web/` — the React/Vite frontend (embedded into the Go binary at build)
- `goland-plugin/` — a GoLand/IntelliJ plugin (Kotlin + Gradle) that does the
  same inline call expansion natively in the IDE. Independent Gradle build;
  see [`goland-plugin/PLAN.md`](./goland-plugin/PLAN.md). Run a sandbox IDE
  with `cd goland-plugin && ./gradlew runIde`.

## Stack

- **Indexer**: Go (`go/packages` + `go/types`)
- **Server**: Go (HTTP, embeds frontend assets)
- **Frontend**: Bun + Vite + React + TypeScript, Shiki for syntax highlighting
- **CLI**: single static Go binary
- **IDE plugin**: Kotlin + IntelliJ Platform Gradle plugin (GoLand)
