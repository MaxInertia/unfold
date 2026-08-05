# unfold

A code-reading tool that lets you follow execution paths *linearly* by expanding function calls inline into their implementations, recursively, across files.

When reading code that's heavily decomposed (DI, layered services, lots of small functions), you spend most of your time jumping between files trying to hold the call stack in your head. `unfold` lets you pick a starting symbol, then click any call site to splice the callee's body in directly below — recursively — so a multi-file execution path reads top-to-bottom in one view.

See [`PLAN.md`](./PLAN.md) for architecture, scope, and phasing.

## Usages / callers

Unfolding follows execution *downward*; the usages feature is the reverse
direction. **▲ callers** in any frame header lists where that function is
referenced; picking one re-roots the view so the caller reads as spliced
above (the frame you clicked from keeps its expansion state, nested at the
picked call site). The **callers** sidebar tab is the same data as an
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
- **Only references inside indexed function bodies are found.** Package-level
  initializers (`var handler = myFunc`) and struct literal defaults at
  package scope aren't walked.
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
- **outbound** — where the service reaches out: `http.Get`/`Post` calls with a
  statically known URL, Pub/Sub topics it names. These have no in-repo target
  (the far end lives in another service) so they open their call site.

### The anchor

The frame you zoomed out from is carried up as the **anchor**. Every inbound
entry that transitively reaches it is highlighted and badged `reaches anchor`;
the rest dim. That's what keeps a large surface readable — with eighty routes
you're not reading eighty, you're seeing the two that concern you with the
rest as context. The trail names the count (`unfold › 3 entrypoints ›
validateCoupon`) because until you pick one, the entrypoint slot genuinely has
three answers.

Reachability is computed the same way the callers tree walks: backwards over
call and interface edges. Value references aren't followed — a function passed
as a value has no call site, so a chain through one isn't an execution path.

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
- **declared** — asserted by a manifest or infra-as-code. Not yet produced.

Recognizers currently cover `net/http` route registration (including Go 1.22
`"POST /path"` patterns, host-qualified patterns, and handlers wrapped in
`http.HandlerFunc`), GCP Pub/Sub topic and subscription handles, and outbound
`net/http` client calls. A URL assembled at runtime is skipped rather than
guessed. Adding a router or broker means adding a rule in
`internal/platform` — recognizers see a neutral `Call` (package, receiver,
function, constant-folded args), never an AST.

### Limitations

- **Single repo.** The service is the module you indexed, named after its
  directory. Joining outbound keys to the services that serve them needs a
  multi-repo index, which doesn't exist yet.
- **Go only.** The TypeScript engine has no recognizers, so the zoom control
  is hidden when it's loaded.
- **Only `net/http`.** Third-party routers (chi, gin, echo) aren't recognized
  yet, so a service using one shows an empty inbound surface.
- **Handlers must be named functions.** A route registered with an inline
  closure has no target to open, so it falls back to the registration site.

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
