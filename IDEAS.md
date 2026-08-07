# Ideas

Unscoped feature ideas. Each entry captures the idea + a sketch of the approach, but is not a commitment.

---

## Type info on identifiers, in floating cards (2026-05-04)

When reading an unfolded code path you often want the type of a variable or field without leaving the view. A click on an identifier could pop up a floating card showing its type signature, defined-at location, and (when relevant) its struct fields or interface methods. Cards should be draggable and individually closeable, so multiple type lookups can be pinned at once while comparing.

### Sketch

**Backend.** The indexer already has `go/types` loaded for call resolution, so a new endpoint is mostly wiring:

- `GET /api/typeinfo?targetId=<frame>&offset=<byteInFrame>` — resolve the AST node at that byte offset to its `*types.Object`, return `{kind, name, type: "<go signature>", definedAt: "<file:line>", doc?, fields?}`.
- For struct types, optionally include the field list so the card can render the shape inline.

**Frontend.** Two implementation choices:

1. **Trigger.** Plain click on a non-call identifier (call-site click is already taken). Alt+click is the safer fallback if plain click ever needs to be reserved for something else later.
2. **Decoration vs. position lookup.** Either the backend includes a `tokens: [{spanStart, spanEnd}]` list on `Frame` and the renderer wraps each ident in a clickable span (matches the existing call-site pattern, costs bytes), or the renderer attaches a line-level click handler and computes the offset from the clicked text node (lighter, more brittle). Pre-decoration is the cleaner default.

**Floating card.** Stand-alone React component, `position: fixed`, mounted at App level. Drag via `pointerdown → pointermove` on the header. Per-card state lives in `viewState`. Close button per card. The signature inside the card is Shiki-highlighted so it matches the rest of the UI.

### Open questions

- Click vs. alt-click vs. hover-with-delay. (Hover interferes with selection; alt-click is conservative; plain click is most discoverable.)
- Should clicking the "defined at" location in the card jump the main view to that frame, or open another card?
- Cap on simultaneous open cards, or unlimited with stacking?

---

## Bookmarking methods (2026-06-03)

A persistent, personal list of saved functions/methods you can jump back to. When you're tracing a path you often want to park a few key functions ("the auth entrypoint", "the place the bug lives") and return to them without re-searching. A bookmark loads its symbol as a fresh root frame.

### Sketch

**What you bookmark.** A *symbol* (a `TargetID`), not a whole view. Sharing an expansion state is already covered by the URL hash; bookmarks are the lightweight "take me back to this function" affordance. Opening a bookmark = `store.setSymbol(targetId)`.

**Label — needs a backend nudge.** The Frame header currently shows `prettyName(frame.id)`. That's fine for Go (the id is the qualified name) but ugly for TS (the id is `<file>#<pos>`). Add a `Title string` to `model.Frame` that each engine fills with a human name (Go: trimmed `FullName`; TS: the registered `name`, e.g. `English.greet`). Small change, and it also cleans up the header for TS. `SearchResult.Label` already carries a good name for the picker path.

**Resilience to edits (the real design point).** A raw `TargetID` is fragile: the TS engine keys targets by `<file>#<bytepos>`, so editing the file and re-indexing shifts the position and orphans the bookmark. Store a *re-resolvable* identifier — `{ name, file, targetId }` — and on open try `targetId` first, then fall back to `LookupSymbol(name)`. Go's `FullName` is stable across edits (unless renamed), so it round-trips directly. Mark a bookmark "unresolved" in the UI when neither path hits.

**Storage.** `localStorage` (personal, like the call-tree collapsed flag), namespaced by project so bookmarks from one repo don't bleed into another — key off the `/api/health` `target`/dir (or a hash of it). Shape: `unfold.bookmarks.<projectKey> = [{ name, file, line, targetId, addedAt }]`.

**State.** A small `web/src/bookmarks.tsx` store mirroring `viewState`'s pattern (localStorage read/write + a `subscribe` so consumers re-render): `add/remove/has/list`.

**UI.**
- A star toggle in the Frame header (`Frame.tsx`), filled when the frame's symbol is bookmarked.
- A **Bookmarks** section in the existing left sidebar (`App.tsx`), above the call tree — it's already a collapsible panel. Each entry: title + `file:line`, click to load as root, `×` to remove. Empty-state hint.

**Files.** Backend: `internal/model/model.go` (+`Title`), `internal/indexer/indexer.go` + `tsindexer/main.ts` (populate it; the Go tsengine passes it through as JSON). Frontend: new `bookmarks.tsx`, plus `types.ts` (+`title`), `Frame.tsx` (star), `App.tsx` (list), `index.css`. Mostly frontend; small and self-contained.

### Open questions

- Bookmark a symbol only, or also a *saved view* (symbol + expansion tree)? The latter overlaps with URL sharing; probably keep v1 to symbols and revisit.
- Project key: derive from the health `target` string, or have the server expose a stable project id (module path / cwd hash)? The latter is sturdier if the same project is opened from different CWDs.
- Re-resolve eagerly on load (validate every bookmark against the index, dimming dead ones) vs lazily on click? Eager gives honest UI but costs N lookups on startup.
- Export/import or shareable bookmark sets — defer.

---

## File explorer in the sidebar (2026-06-03)

Today the only way into the code is searching for a symbol. A file tree on the left would let you see the project's path structure and select files to navigate from — orientation you don't get from search alone.

### Sketch

**What selecting a file does.** unfold is symbol-oriented (you open a function, then expand calls). So the natural primary action on a file is **list the functions/methods defined in it**, then click one to load it as a root frame. A raw whole-file view is a possible secondary mode, but it doesn't expand-into-calls (the file isn't a single frame), so lead with the symbol list. Recommended: file → its symbols → open symbol.

**Backend.** Add `Files() []FileSymbols` to `model.Engine` (`FileSymbols{ Path string; Symbols []SearchResult }`) and a `GET /api/files` endpoint. Both engines already hold the data: the Go indexer groups its `funcs` by `decl` file; the TS sidecar groups `funcs` (+ templates) by source file. One call returns the whole map, so the frontend builds the tree with no per-file roundtrips. Paths are absolute; the client strips the longest common directory prefix for display (no new server state needed — the project root isn't currently tracked).

**Frontend.** A `FileTree` component that turns the flat path list into a collapsible folder tree (mirror the `CallTree` patterns — twisties, indent guides, the same panel chrome). Clicking a folder toggles; a file expands to its symbols; a symbol calls `store.setSymbol(targetId)`.

**Sidebar layout.** The left panel is getting busy (bookmarks + call tree). Proposal: keep **Bookmarks** pinned on top, then a two-tab switcher **Files | Calls** for the two big trees (Calls stays the default). Both still live under the one collapsible panel.

### Open questions

- File → symbol list (recommended) vs also a raw file viewer? The latter is a different rendering path (no call expansion) — defer unless wanted.
- Scope of the tree: only **indexed** files (ones with symbols unfold knows about) vs the full on-disk directory (needs a filesystem walk + a tracked project root). Indexed-only is simpler and matches what you can actually navigate; full-disk browsing is a bigger, separate feature.
- Large repos: `/api/files` returning every symbol could be big — fine for v1, paginate / lazy-load per file later if needed.
- Tabs (Files | Calls) vs stacked collapsible sections — tabs keep height sane when both trees are large.

---

## Depth legibility without indentation (2026-06-11) — SHIPPED

All three cues plus the settings surface are in (sticky headers via
`feat/sticky-headers`; rails, ruler, and the settings panel via
`feat/depth-rails`). Kept for the design record; the open questions below
that still matter are tracked inline.

### Sketch

**Depth source.** Already available: every rendered frame knows its `FramePath` (`viewState.tsx`), so depth = `path.length`. No backend work anywhere in this feature.

**1. Sticky stacked headers — SHIPPED (feat/sticky-headers).** As you scroll into a frame, a compact copy of its header pins to the top of the viewport; nested frames stack theirs beneath, so the pinned stack reads as the live call chain; clicking one scrolls back to that frame. The original CSS `position: sticky` sketch did NOT survive the audit it called for: `.frame { overflow: hidden }` and `.frame-body { overflow-x: auto }` are scroll containers, so nested headers would pin to their parent frame's box, not the viewport. Shipped as a JS overlay instead (`StickyHeaders.tsx`): walk the frame containment chain from `.app-root-frame` via `getBoundingClientRect`, render a fixed stack aligned to the content column, recompute on scroll/resize/mutation. Chains deeper than 6 collapse the middle into a "⋯ +N" row. Each pinned header carries a depth-colored rail (`depthColor()` exported) — rails (item 2) should reuse the same palette.

**2. Depth rails — SHIPPED (feat/depth-rails, default on).** Each `.frame--railed` draws a 3px `border-left` colored by `depthColor(path.length)`; since nested cards aren't indented, the lanes stack side by side automatically — no padding bookkeeping needed. Same palette as the stuck headers.

**3. Depth ruler — SHIPPED (feat/depth-rails, default off).** A small `.frame-depth` badge in the frame header rendering `path.length`, tinted with the level's rail color.

**Settings surface — SHIPPED (feat/depth-rails).** `web/src/settings.tsx` is a global localStorage store (`unfold.settings`, mirroring the `bookmarks.tsx` `useSyncExternalStore` pattern); a gear in the app header opens a right-side panel so toggles take effect on the visible code live. Shape: `{ depthRails, depthRuler, indentMode: "rails" | "indent" }` — classic indentation survives as `indentMode: "indent"`.

### Still open (follow-up candidates)

- Palette: shipped as a fixed 6-color cycle (repeats at depth 7). Revisit if deep traces make collisions confusing; needs a dedicated dark-theme pair if contrast complaints show up.
- Hover-to-highlight-enclosing-chain (variation 3 from the brainstorm) layers cleanly on top of rails — same depth plumbing. Deferred.
- A modal presentation for settings as itself a setting (`settingsUi: "panel" | "modal"`) — deferred until someone wants it.

---

## Platform unfold — one repo to the whole system (2026-08-05) — SLICE B SHIPPED

Extend unfolding past the boundary of a single repository, so a reading session follows execution *and information* across services, brokers, databases, and observability tooling. Have a Pub/Sub subscriber? See the topic, where it's published, and every other consumer. Call another microservice you own? Unfold into the handler in that service. A `vstore`-tagged struct field? Jump to that model's page, and to BigQuery where a secondary index exists. Same thesis as today — collapse the distance between pieces of information you'd otherwise hunt down — at platform scale.

Full design record in the vault: `docs/2026-08-05-unfold-platform-graph.md`.

### Status — 2026-08-06, branch `feat/platform-service-view` (unmerged)

Well past the original slice: L1 service view, the declared tier
(`microservice.yaml` + protos, `--proto-root` pickable in-browser), workspaces
(`--workspace`, `<repo>::<id>` ids, lazy/eager indexing, cross-repo jump into
the implementation), the L0 platform graph, and the anchor at every level in
both directions.

Since then: the zoom level and service pick live in the URL, with navigations
pushing history and expansions replacing it (alt+←/→); the two anchor walks
render beside the code as an **entrypoints** sidebar tab and an **outbounds**
right panel, which is the L1.5 rung arriving as panels rather than a screen;
the saved-anchor list marks the live anchor; and the L0 graph routes
layer-skipping edges around intermediate nodes, breaks cycles before layering
(sacrificing the lightest edge), and shows edge counts on demand in channels
kept clear of nodes.

**Read the vault doc before touching outbound gRPC**:
`docs/2026-08-05-unfold-platform-graph.md`. It records the rule set, why each
rule exists, the approach that failed and why, and the core-index bugs this
work uncovered (chained-call id collision, map-dependent binding order, slice
aliasing on id qualification).

The **crossing relation** joins the two columns of the service view: for each
inbound binding, the outbound bindings its handler forward-reaches. Pick a row
in either column and the connected rows on the other side light. Only one
direction is stored; the reverse is derived in the browser. It's also the
per-repo half of transitive anchor marking at L0.

**Package-level initializers are indexed.** A variable whose initializer holds
a call is a target in its own right — it opens as a frame, its calls resolve,
it appears in usages and the callers tree, and it seeds reachability the way
`init` does. That closes the cobra `var cmd = &cobra.Command{RunE: …}` gap and
the usages limitation with it.

**Anchor marking at L0 is transitive.** With `gateway → inbox → conversation`
and the anchor in conversation, gateway is marked even though it never calls
conversation — propagation is a fixpoint over the service graph, seeded by the
crossing relation per repo. It tracks *which inbound keys* lead onward
separately from *whether the service reaches*, so a call made from a service's
own `init` marks that service without lighting its callers.

**Repos can be linked after launch.** `+ link repo…` opens another repository
without restarting — from a plain single-repo session too, which promotes it
to a workspace. Persisted per project, so the link sticks. Implemented as an
engine rebuild rather than live workspace mutation: the repo set and the
cross-repo join are read lock-free everywhere on the assumption they're fixed
after startup.

Next up, in rough order of value: HTTP edges joining across repos; third-party
router recognizers; multiple anchors at once (the UI list is already an array
— it needs the backend walks to take a set and union the results).

### Sketch

**Two node types.** The load-bearing simplification: every node is either a **`Frame`** (source you read linearly — what exists today) or a **`Resource`** (an identity in another system, carrying deep links out plus the code sites that touch it).

```go
type Resource struct {
    Kind     string  // pubsub.topic | vstore.model | bigquery.table | log.site | service | http.route
    Key      string  // "orders-v1" | "billing.Invoice" | "acme:analytics.invoices"
    Links    []Link  // deep links out — the leaf exits
    Emitters []Site  // code that writes / publishes / queries it
    Handlers []Site  // code that reads / subscribes / serves it
}
```

Topics, routes, vstore models, BQ tables and log sites are all this one type with different *recognizers*. A topic isn't source, so it can't be a `Frame` — expanding a cross-boundary call yields a **junction card** (resource + config + endpoints) that you expand *through* into the next code frame. That's also honest UI: it marks where execution left the process.

**Leaf nodes are hubs.** Nodes you can't traverse past (open BigQuery yourself) should still exist — they're dead ends in only one direction. Traversing *inward* gives every model, writer and query site across the platform, which is usually the question you actually had.

**Link-out before inline-preview.** Governing constraint: connect all the things without reimplementing all the things. Every integration has two depths — link-out (a URL template, near-free) and inline preview (real work). Ship link-out everywhere first. unfold stays an index of identities plus a link graph; it never becomes a log viewer or a DB browser. (This is why the log story starts as a pre-filled Cloud Logging query URL, not a live query panel.)

**1. Federating engine.** `model.Engine` is one project, one index. Platform mode needs N engines behind one, `TargetID` namespaced by repo. `Frame`, `Usages` and diff mode ride along unchanged. This is the plumbing everything else depends on.

**2. Key-joined edges.** A publish and its subscriber share no AST edge — they share a key. Engines emit bindings alongside calls (`emit pubsub.topic:orders-v1 @ billing/publish.go:42`, `serve http:POST /v1/orders @ orders/routes.go:31`) and a platform resolver joins emitters to handlers. One mechanism, several key namespaces, covering Pub/Sub, HTTP and gRPC.

**3. `KindFanout` already fits.** `KindFanout` + `Receivers` (one site reaches many targets, all of which run, each with `Provenance` and `Confidence`) was built for RxJS subscribers and is exactly the shape of a topic with N subscribers. Publish → subscribers needs no new rendering concept.

**4. Struct tags ride on `TypeInfo`.** `vstore` tags are a *type-level* recognizer, not a call edge. `TypeInfo` already resolves an identifier to its struct definition, so it grows a "linked resources" section on a card that already renders.

**Where the metadata comes from.** Keep the manifest thin — a hand-maintained platform map rots in a quarter. Tier 1, derived from code: route tables are already in the source (`mux.HandleFunc`, gRPC registration, `@Controller`), topic names are in the code at both ends, and *which* pubsub tech is a recognizer plugin auto-detected from `go.mod` / `package.json` (same gating the TS engine already does for Angular by type symbol + origin module). Tier 2, derived from infra-as-code: Terraform *is* the topology — `google_pubsub_topic`, `google_pubsub_subscription` push endpoints, Cloud Run/GKE services, DLQs, project id. Tier 3, hand-declared, only what neither knows: repo list, terraform dir, logging backend + project, occasional env-var→service mappings, and per-org URL templates for resource deep links.

**Cross-service calls.** Through a Go SDK — better than it looks: `go/types` already resolves `c.CreateOrder(...)` into `ordersclient.(*Client).CreateOrder`, and unfold can unfold that body today if the SDK is in the module graph (interface-held clients are covered by `KindInterface`/`Candidates`). The gap is a single hop from the transport call inside the SDK method to the remote handler — usually *exact*, since an SDK and its server typically share a route constant or generated stub. For raw HTTP, invert the problem: don't resolve the hostname, join on **method + path literal** against the global route table and fall back to host resolution only on ambiguity.

**The honesty problem.** unfold's credibility is that resolution is deterministic — `go/types` doesn't guess. Platform edges often aren't literal (topic names from constants, env vars, Terraform). So resolution is tiered and *visible*: **exact** (literal↔literal or shared constant/stub), **declared** (manifest or Terraform), **inferred** (heuristic). `Confidence` on `Receiver` stops being decoration. Derived always beats declared, and the manifest is validated against the index — declare a service whose routes nobody registers and it's flagged stale rather than silently drawing a wrong edge.

**Zoom levels — platform → service → frame.** Zooming out to "show me this microservice as a whole" (inbound API surface, outgoing calls, who it talks to, a link to the pods in GCP) is mostly a *byproduct* of the recognizer work: once bindings like `serve http:POST /v1/orders` are extracted per repo, a service node is a `GROUP BY repo` over data already in the index — you query the same graph at a coarser granularity rather than building a second thing. The pods link is `Resource{Kind: "service"}` with `Links`.

**The ladder is reachability, not containment.** The tempting ladder is function ⊂ file ⊂ package ⊂ service ⊂ platform — but the file rung should be rejected. The file is the unit unfold exists to dissolve (execution doesn't respect file boundaries; that's the founding premise), and a whole-file view is already reachable via `Frame("file:<path>")`, so it can stay a *lateral* move rather than spend a rung. unfold's strengths — unfolding down, the callers tree, re-rooting — are all reachability, so:

```
L2   frame        a function + its unfolded callees      (today)
L1.5 entrypoints  which routes/subs/crons reach this fn  (new)
L1   service      inbound surface + outbound deps
L0   platform     services and their edges
```

**L1.5 is the valuable rung.** Deep in `validateCoupon`, "it's in coupon.go with 8 other functions" is worth nearly nothing — already visible. "It's reached by `POST /v1/orders` and the nightly-reprice subscription" is the orienting fact and the question you actually had. Cheap, too: the existing callers traversal with a different termination rule (stop at binding sites, not at no-more-callers). Package sits *off* the ladder — a grouping/filter inside L1, not a rung.

**The anchor.** Carry the frame you zoomed out from as an anchor through every level: at L1.5 the entrypoints reaching it are marked, at L1 the inbound entries reaching it are highlighted and the rest dimmed, at L0 its service is lit. Zoom out and back in is lossless because the anchor never left — that's the mechanism behind "preserve expansion state and location". It also dissolves the L1 legibility problem: you aren't reading 80 routes, you're seeing the 2 that concern you with 78 as context.

**The trail is a zoom stack, not a breadcrumb.** Standard breadcrumbs truncate when you click up — click `orders` and `validateCoupon` vanishes. Wrong here, because *the trailing entries are the anchor*: L1 is only useful because it highlights the routes reaching `validateCoupon`, and truncating deletes what makes the level you just zoomed to meaningful. So keep every entry and mark the active level — above solid (ancestors), current marked, below ghosted (clicking returns you there exactly, since nothing was destroyed). Rule: zooming out preserves the tail; descending a *different* branch rewrites it from that slot down. A consequence: **L1.5 is the picker for an unfilled trail slot** — `platform › orders › ⋯ › validateCoupon`, where choosing an entrypoint fills the `⋯`.

**History: vertical vs lateral.** Anchors get a history stack so lateral moves are reversible (back from a re-root, forward to it again). Keep the axes separate — *the trail handles vertical, history handles lateral*: zooming isn't destructive once the tail is preserved, so clicking down the trail is already the undo, while history earns its keep on moves that genuinely replace state (re-root through a caller, impl switch). Back/forward still undoes zooms as a safety net, just isn't the primary vertical affordance, or two "go back" gestures disagree. Likely near-free: expansion state already serializes into the URL hash, so if navigations `pushState` and expansions `replaceState`, browser back/forward *is* the anchor history and every entry is already a shareable URL. Entries must be whole view-states, not just the anchor.

Design rules for it: (a) **a zoom level, not a tab** — clicking a route in the map makes it your root frame, and any frame can surface its enclosing service; bidirectional or don't bother, otherwise it's a diagram you look at once instead of an entry point into the core loop. (b) The value over an architecture diagram is that this is a *projection of the index* — regenerated each load, can't drift, every node clickable into source, nobody maintains it (unlike Backstage-style catalogs typed into YAML). (c) **L1 probably shouldn't be a graph** — 80 routes and 30 outbound deps force-directed is a hairball; structure it as inbound-left / service-middle / outbound-right, grouped by domain. Save node-link rendering for L0 where the node count is the number of services.

**Smallest slice worth building.** Two candidates:

- **A — Pub/Sub, literal topic strings only,** across a workspace manifest of local checkouts. No cloud credentials (the topic name is in the code at both ends), reuses fan-out rendering end-to-end, forces the federating-engine plumbing every other edge type needs. GCP metadata (topic exists? DLQ? retention?) is an enrichment layer on the junction card later.
- **B — the L1 service view for a single repo.** Notably this does *not* need the federating engine: index one service and its inbound surface + outbound calls render immediately, with unresolved outbound edges degrading gracefully to named-but-not-traversable. It exercises binding extraction — the foundation everything else sits on — without committing to multi-repo plumbing, and produces something useful on day one.

Leaning **B first, then A**: cheaper, and it de-risks the recognizer layer before the expensive plumbing lands.

### Decided (2026-08-05)

- **Zoom-out gesture:** build *both* a frame-header control and a breadcrumb, try them, keep one. Keybinding regardless. (The breadcrumb has a structural edge: it *is* the zoom stack and the natural home for the anchor — `platform › orders › POST /v1/orders › validateCoupon` — one widget doing both jobs.)
- **Web view only** for now; extending to the GoLand plugin is a later decision, after this works well in the web UI.
- **Zooming out preserves expansion state and location**, via the anchor.
- **No file rung** in the ladder — reachability, not containment.
- **Filtering, not domain clustering,** at L0 — unless `microservice.yaml` turns out to carry a team/domain field, in which case clustering is *declared* rather than inferred and becomes free instead of arbitrary. Check before ruling it out.
- **Service naming:** most microservices here declare it in a `microservice.yaml` at the repo root — use that as canonical, repo name as fallback. (The shipped slice uses the repo directory only.)
- **No declared clustering** (confirmed 2026-08-05): `microservice.yaml` carries no owner/team field, so clustering can't be made declared. Filtering it is, permanently.

**SHIPPED 2026-08-05 (`feat/platform-service-view`)** — `internal/manifest` + `internal/protoapi`, `--proto-root`, visibility grouping, stale badges. See the README's "What the service declares about itself".

**What `microservice.yaml` carries** — `microservice.name`, `microservice.protopaths.{path,excludeFromSdk}`, `microservice.publicRoutes`. `protopaths` reorders the priorities: with protos declared per repo, a cross-service SDK call isn't "one hop from the transport call to the remote handler", it's a **declared** join on `grpc:<proto package>.<Service>/<Method>` — the proto is the artifact both sides are generated from, so no dataflow and no heuristics. gRPC/SDK edges end up *easier* to resolve than HTTP ones here. `excludeFromSdk` is useful negative space (surface that exists but isn't cross-service callable, so later "nobody calls this" conclusions don't fire falsely). `publicRoutes` splits inbound by reachability — public / platform / internal — which is a better primary grouping than kind, and gives the first real drift check: a declared route nothing registers is stale, a proto method with no implementation is stale, a code-registered route in neither list is internal.

Settled while building: `protoPaths` is a list of entries with `path` and `excludeFromSdk` (both proto-file path strings, either can appear on any entry); `publicRoutes` is a list of bare path strings; `gopkg.in/yaml.v3` and `github.com/bufbuild/protocompile` are the dependencies. Proto paths are relative to a *separate* shared proto repo, so `--proto-root` points at it.
- **The trail keeps every entry**, styling the active level rather than truncating on zoom-out. Descending a different branch rewrites from that slot down.
- **Anchor history with back/forward** so lateral moves are reversible; trail = vertical, history = lateral; entries are whole view-states.

### Open questions

- Federating engine: index N checkouts in-process, or one engine per repo behind a coordinator? The latter scales and matches watch mode's per-repo reload, but adds a transport.
- `TargetID` namespacing — repo alias from the manifest vs a content-addressed project id. Bookmarks and URL-hash sharing both need it stable (same fragility as the bookmarking entry above).
- Terraform: parse HCL directly, or consume `terraform show -json` / state? State is accurate but needs credentials and drifts from the branch you're reading.
- Indexing cost across N repos, and whether watch mode stays viable over a whole workspace.
- L0 at real scale — how many services before the node-link view needs filtering ("only edges touching X")? Filtering is the chosen mechanism; the threshold that forces it is unknown.
- L1.5 termination: an entrypoint search that finds nothing (a helper reachable only from other helpers, or through a `ref` edge that breaks the chain — see the usages limitations in the README) needs an honest empty state, not a blank panel.
- **The sidebar doesn't follow the zoom level.** At L1 it still shows the frame-scoped files/calls/callers/notes tabs, so the "one continuous surface" claim is only half true — the main panel zooms and the sidebar doesn't. Noted as acceptable for now (2026-08-05). The promising fill is that at L1 the sidebar becomes the *filter* surface (by kind, by confidence, reaching-anchor only), which would make this and the L0-filtering question the same question. Whatever it becomes, tabs that don't apply at a level should disable rather than display stale frame-scoped content.
- Does `pushState` on every zoom make browser back tediously granular? May need rapid zoom transitions coalesced into one history entry.

---

## User-defined recognizers (2026-08-07)

Services communicate in many ways — a dozen in-house HTTP wrappers, a custom pub/sub library — and hardcoding a Go recognizer per library doesn't scale. Describe the pattern *in the app* instead ("here's a call through this wrapper"; "here's a subscriber using our library, and here's where it publishes"), and let unfold find everything matching. Emitters of a topic then become the inbound callers of its subscribers automatically.

Full design record in the vault: `docs/2026-08-07-unfold-user-defined-recognizers.md`.

**Status — being built.** The engine (`internal/rules`), the authoring UI, and the leaf action are on `feat/user-defined-recognizers`. Decisions taken: JSON with a `"//"` comment field; rules toggleable including built-ins, which gained stable ids for it; composition depth per-rule. Known gap, pinned by the acceptance test: phase 1 only sees owned code, so a transport call several hops inside a *dependency* is invisible to a rule while the built-in still finds it — the fix is to let phase 1 see dependency bodies while attribution still stops at the owned caller.

**The seam already exists.** `platform.Recognizer` is `func(Call) []model.Binding` over a syntax-free `Call` (package path, receiver, func name, constant-folded args, func-value targets). No AST, so a data-driven rule is a Recognizer built from config rather than written in Go — no matching engine to design, and the TS engine could feed the same facts. The package doc already anticipates this: which rules are active "is ultimately a per-project question".

**The join needs no work.** `servedBy` already joins matching keys across repos, the crossing relation connects each end to its entrypoints, and `KindFanout` already renders one site → many receivers. Getting the keys right is the whole feature.

**Options**, in the vault doc: declarative YAML rules (A) authored by example in the UI (B); a query language (C, overkill); plugin code (D, "share a rule" becomes "run someone's code"); or **deriving instead of configuring** (E) — the call-graph walk that replaced literal-scanning for gRPC generalizes to HTTP wrappers, and may cover most of them with no rules at all. Recommendation: E first, then A authored by B.

**The constraint:** `Call` is positional and constant-folded, so a rule can say "arg 0 is the topic" but not "the topic is a field on the receiver". Read the real wrappers before fixing a rule shape.

**Rules need two phases, not a fatter `Call`.** A real wanted rule — *imported from `foo/$(domain)/sdks/go` **and** the callee's body contains a call matching another rule* — can't be expressed over a single call site. It wants (1) classify functions by rules over their own bodies, memoized, then (2) classify call sites whose callee carries a classification, attributing to the owned caller. That is exactly what the gRPC pass already does by hand: `invokePath`/`invokeCache` is phase 1, and "walk back to the owned caller and stop" is phase 2. So the wanted rule is the *generalization of the hardcoded gRPC ruleset*, and both primitives exist. The depth lesson transfers with it: "matching inside it" must mean its own body, not everything transitively reachable.

**Acceptance test for the language:** express the built-in gRPC rule as config and check it yields bindings identical to the hardcoded pass on `testdata/declared`. If it can't state the rule set that was hardest to get right, it won't survive the next library.

**Second use — deciding what counts as a leaf.** A rule has a *match* half and an *action* half; today the action is always "emit a binding". Adding `leaf: true/false` / `render: resource` reuses the entire matching half. Today that decision is one hardcoded heuristic (`CallSite.External`, true for stdlib/deps), which is crude: an in-house SDK is a dependency you always want to expand, a logging library is expandable but never worth expanding, and a client fronting another service should show a junction card rather than transport plumbing. It would also make "why can't I expand this?" answerable, which it currently isn't.
