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

## Stack

- **Indexer**: Go (`go/packages` + `go/types`)
- **Server**: Go (HTTP, embeds frontend assets)
- **Frontend**: Bun + Vite + React + TypeScript, Shiki for syntax highlighting
- **CLI**: single static Go binary
