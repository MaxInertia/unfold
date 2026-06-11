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
