# unfold-goland — inline call expansion, natively in GoLand

A GoLand/IntelliJ plugin that brings unfold's idea into the editor: click a call
site and the callee's body is spliced **between the lines**, recursively,
**without modifying the file**. The inserted code should look and behave exactly
like the surrounding code.

> Status: plan + starter scaffold. The scaffold was written without a GoLand SDK
> to test against — treat versions/API names as a starting point and expect to
> bump them. Where an exact API name is uncertain it's marked `// verify`.

---

## Why this is *easier* in GoLand than the standalone web app

The web app had to build two hard things itself. In-IDE, both are free:

1. **Resolution / indexing → PSI.** GoLand's Go plugin already has the fully
   typed AST (PSI). Resolving a call to its callee is a couple of calls
   (`GoReferenceExpression.resolve()`), implementations come from the platform's
   implementation search, and it's all live + incremental. **Drop the
   go/packages + ts-morph indexers entirely.**
2. **Rendering between lines without editing the file → inlays.** `InlayModel`
   block elements are pure visual overlays tied to a document offset; the file's
   text is never touched. This is the mechanism the whole thing hangs on.

So the plugin is mostly: *make call sites clickable → on click, resolve via PSI →
render the callee body in a block inlay → recurse.* The interesting, open part is
**how to render** so it looks/behaves native — that's the experiment.

---

## Architecture

```
ExpandController (per Editor)
  ├─ call-site affordances  → mark resolvable GoCallExpr as clickable
  ├─ expansion model        → tree of {offset(RangeMarker) → ExpandedFrame},
  │                            each frame can hold nested expansions
  └─ FrameRenderer (swappable)   ← the experiment lives here
        ├─ A. PaintedRenderer   (block inlay + Graphics2D, IDE highlighter)
        ├─ B. EditorInlayRenderer (real read-only EditorEx between the lines) ★
        └─ C. JcefRenderer      (embed unfold's existing web frontend)
```

`FrameRenderer` is one interface (`render(frame): Disposable`, `dispose()`), so
A/B/C are swappable behind identical expand/collapse logic and you can compare
them side by side.

---

## The crux: making the inserted code "look the same and support the same stuff"

"Supports the same stuff" = native syntax colors + font + theme, **and** caret /
selection / copy / hover / Ctrl-click go-to-def / find-usages / folding / and
nested expansion inside the inserted body. Be clear-eyed about what each
rendering approach can actually deliver:

### A. Painted block inlay (fastest to look right, weakest behavior)
`InlayModel.addBlockElement(offset, …, EditorCustomElementRenderer)` and in
`paint()` draw the callee's text token-by-token using the IDE's own
`SyntaxHighlighter` + `EditorColorsScheme` + editor font. Because you reuse the
real highlighter and scheme, **colors/font match exactly**.
- ✅ Looks native, true between-the-lines, no document edits, easy to nest
  (a tree of block inlays at increasing indent).
- ❌ It's a *picture*, not an editor: no caret/selection/copy/hover/go-to-def
  unless you build each by hand (map mouse x/y → token → action). Parity is a
  lot of manual work.
- **Use it to validate the expand/collapse/recursion mechanics and confirm the
  look — not as the end state.**

### B. Real read-only editor embedded between the lines (the ideal) ★
Host an actual `EditorEx` viewer showing the callee's source inside the editor
flow. Because it *is* an editor, you get native highlighting **and** selection,
copy, hover, go-to-def, folding, even nested inlays — for free.
- Mechanism: reserve vertical space with a block inlay, then add the sub-editor's
  Swing component to the host editor's content component and keep its bounds in
  sync with the inlay region on scroll/resize. (Component-backed inlays /
  "inlay + overlay component" technique.)
- Make go-to-def etc. resolve correctly by backing the sub-editor with the **real
  callee file's `Document`**, shown as a range (fold everything outside the
  function range) rather than a detached copy of the text — a copy highlights
  fine but won't resolve references.
- Prior art to study (JetBrains ships exactly this pattern):
  - **Jupyter/notebook cells** in PyCharm/DataSpell — each cell is an editor
    embedded in a scrollable column.
  - **In-editor rendered Javadoc** (2020.1+) and inline **diff/preview** editors.
  - Look at how these reserve space and sync component bounds.
- ✅ The only approach that meets the full "looks the same + supports the same
  stuff" bar.
- ❌ Hardest: component lifecycle, sizing, scroll sync, showing just a range,
  performance with many/deep frames. This is the make-or-break experiment.

### C. JCEF webview rendering unfold's existing frontend (reuse, but off-theme)
Embed a JCEF browser in the inlay showing the React/Shiki view.
- ✅ Reuses everything already built; rich web interactivity.
- ❌ Won't match GoLand's font/theme/feel; no native selection/hover/go-to-def
  into the IDE; JCEF-in-inlay is finicky (sizing, focus, transparency, one
  browser per frame is heavy).
- **Keep as a contrast/fallback and a fast way to demo the interaction model.**

**Recommendation:** build the mechanics on **A** (quick, looks native), then pour
the effort into **B** — that's the only path to the stated ideal. Keep **C** in
your back pocket.

---

## Phased plan (ordered so each step is runnable)

**Phase 0 — Scaffold + the smallest loop.** `./gradlew runIde` launches a GoLand
sandbox with the plugin. An action (keymap or gutter) on a Go call inserts a
**plain** block inlay below the line showing the callee's raw text; invoke again
to remove it. Goal: prove inlay insert/remove + it tracks edits. (Scaffold below
gets you most of the way.)

**Phase 1 — PSI resolution.** From a `GoCallExpr` under the caret/click, resolve
the callee `GoFunctionDeclaration`/`GoMethodDeclaration`, grab its body text +
`TextRange`. Handle direct funcs, methods, package funcs. Interface methods →
enumerate implementations (platform implementation search) → quick picker, like
unfold's impl switcher.

**Phase 2 — Clickable call sites.** Make resolvable calls visibly expandable like
unfold's underlined spans. Try: (a) a `RangeHighlighter` underline on call tokens
+ an `EditorMouseListener` for clicks (closest to unfold); or (b) an interactive
`InlayHintsProvider` "↳" after each call; or (c) a gutter icon. Click toggles
expansion.

**Phase 3 — Rendering experiments.** Implement `FrameRenderer` and build A, then
B, then (optional) C behind it. Compare look + behavior. **B is the prize.**

**Phase 4 — Recursion, nesting, state.** Per-editor expansion tree keyed by
`RangeMarker`s; expanding a call *inside* an expanded frame nests another frame;
indentation/visual depth cue; collapse subtree; (optional) persist per file.

**Phase 5 — Parity polish.** Impl picker; in-frame line folding (unfold's fold
feature); keyboard nav; theme sync; perf (lazy-build frames, dispose on collapse,
cap depth).

---

## Key APIs (cheat-sheet — verify exact names against the SDK)

- Go PSI (`com.goide.psi`): `GoCallExpr`, `GoReferenceExpression.resolve()`,
  `GoFunctionDeclaration` / `GoMethodDeclaration`, `.getBlock()` / `.getTextRange()`. // verify
- Implementations: `DefinitionsScopedSearch` or the Go plugin's gotoImpl. // verify
- Inlays: `editor.getInlayModel().addBlockElement(offset, relatesToPrecedingText,
  showAbove, priority, renderer)` with `EditorCustomElementRenderer`
  (`calcWidthInPixels`/`calcHeightInPixels`/`paint`).
- Native highlighting for painting (A): `SyntaxHighlighterFactory.getSyntaxHighlighter(GoLanguage, …)`,
  `highlighter.getHighlightingLexer()`, colors from `EditorColorsManager.getGlobalScheme()`.
- Sub-editor (B): `EditorFactory.getInstance().createViewer(document, project)`,
  cast to `EditorEx`; add `editor.getComponent()` to the host's content component.
- JCEF (C): `JBCefBrowser` (needs `ide.browser.jcef.enabled`).
- Click/region tracking: `RangeMarker`, `RangeHighlighter`, `EditorMouseListener`.
- Plugin must `depends` on `org.jetbrains.plugins.go` and run in GoLand
  (or IDEA Ultimate + Go plugin).

---

## Risks / unknowns to retire early

1. **Embedding a real editor in the inlay flow (B)** — sizing + scroll-sync +
   showing only a range. This is the whole ballgame for the "ideal"; spike it in
   isolation before building features on it. Study the notebook-cell impl.
2. **Go PSI API names** above are from memory — confirm against the Go plugin SDK.
3. **Performance / depth** — many deep sub-editors are heavy; build lazily,
   dispose on collapse, maybe cap auto-depth.
4. **Dev requires GoLand** (or IDEA Ultimate + Go plugin) to compile against the
   Go PSI API.

---

## First-morning checklist

1. `cd unfold-goland && ./gradlew runIde` → a sandbox GoLand opens (bump the
   platform version in `gradle.properties` if it complains).
2. Open any Go file, put the caret on a function call, run the **Unfold: Expand
   Call** action (Phase 0 scaffold) → a block inlay with the callee text appears.
3. Once that loop works, wire **Phase 1** (real PSI resolution) and **Phase 3A**
   (painted, highlighted), then attack **3B** (embedded editor) — the part that
   makes it look and behave like real code.

See `scaffold-notes.md` for the included starter files and what to fill in.

---

## Web-style frame chrome (2026-06-13)

The inline expander now wraps each frame in card chrome that mirrors unfold's
web `.frame` (`web/src/index.css`): a header row with the callee title and
`file:line`, a thin card border, and a 3px depth-colored left rail. Two paths,
shared by `FrameChrome.kt`:

- **`EditorInlayRenderer` (default, the goal).** The real embedded editor —
  native semantic colors, hover, go-to-def — now sits inside the card. Chrome
  colors come from the **active editor color scheme** (`defaultBackground`,
  `defaultForeground`, `JBColor.border()`, a 6%-foreground header tint), so the
  card matches the user's IDE theme rather than transplanting the web palette.
  Structure mirrors the web; colors stay native.
- **`JcefRenderer` (reference/contrast).** Renders the callee in a faithful copy
  of the web card via HTML/CSS — the literal web palette with a
  `prefers-color-scheme` dark variant. Useful to compare the "real web look"
  against the native-themed card in the same editor.

`FrameChrome.RAIL` mirrors the web `depthColor()` cycle (blue/teal/amber/…) as
`JBColor` light/dark pairs. Depth is 0 today (one frame per host editor); the
cycle is in place so nested frames can later stack distinct rails like the web
sticky-header/rail system does.

**Open:** verifying the native card visually needs `./gradlew runIde` (a GUI
sandbox) — it compiles against the platform but isn't headlessly screenshotable.
Nesting/depth tracking, a clickable `file:line` (open in editor), and a
recursion badge are the natural next parity steps.

---

## Phase 4 — recursion, nesting, depth rails (2026-06-16)

Expanding a call *inside* an expanded frame now nests another frame, one rail
color deeper; collapsing a frame collapses its whole subtree. Implemented:

- **`ExpansionController`** — the per-editor expansion model, keyed by the
  document line a call sits on (sibling calls on different lines expand
  independently; re-invoking on the same line collapses). One controller per
  editor: the host editor gets depth 0; each embedded frame editor gets its own
  controller at depth+1, seeded into that editor's user data.
- **`Frame`** (in `FrameRenderer.kt`) — replaces the bare `Disposable` return.
  Carries `innerEditor`: the embedded editor a nested expansion can target,
  non-null only for `EditorInlayRenderer`'s real-file frame (painted/JCEF frames
  are pictures → null, so they don't nest). `FrameRenderer.render` now takes
  `depth`, which drives `FrameChrome.wrap`'s rail color (and the JCEF card's
  accent), so `FrameChrome.RAIL` finally cycles instead of always rendering 0.
- **Subtree collapse** rides the IntelliJ `Disposer` tree: a frame's child
  controller is a Disposer child of the frame, and each frame is a child of its
  controller — so disposing any frame cascades to every frame nested beneath it.

Why nesting falls out cheaply: `PsiResolve.calleesAtCaret` resolves against
`editor.document`'s PSI file, and the embedded editor is backed by the **real
callee file's document** — so the same expand action, run with the caret inside
a frame, resolves and expands against real PSI with no special-casing. The
action targets `CommonDataKeys.EDITOR`, i.e. whichever editor holds focus.

**Compiles offline against GoLand 2025.3** (`./gradlew clean compileKotlin`).

### Confirmed live + two fixes (2026-06-18)

`runIde` testing settled the open risk: an embedded `EditorEx` **can** host a
further `EditorEmbeddedComponentManager.addComponent` — frame-in-a-frame renders,
so the fallback (host-editor inlays) isn't needed. Two issues surfaced and were
fixed in `EditorInlayRenderer`:

1. **(necessary) Card didn't grow for a nested frame.** `fittedHeight()` counted
   visual *lines* only, which can't see a block inlay's pixels, so a nested
   expansion rendered clipped/overlapping. Fix: add the pixel height of block
   inlays in the function range (`inlayModel.getBlockElementsInRange(funcStart,
   funcEnd).sumOf { heightInPixels }`) to the line height, and re-fit on inlay
   add/update/remove via an `InlayModel.Listener` (not just the existing
   `VisibleAreaListener`, which folding fires but inlay insertion may not).
   `onUpdated` also fires when a *deeper* frame resizes its own inlay, so the
   re-fit + `inlay.update()` propagates the growth all the way up the stack.
2. **(nice-to-have) Empty right-click menu** ("Nothing here"). A bare viewer
   editor has no context-menu group; go-to-def worked only via keybinding. Fix:
   `sub.setContextMenuGroupId(IdeActions.GROUP_EDITOR_POPUP)` so the frame has the
   standard editor popup (copy / go-to / find-usages).

Both compile offline; the height propagation still wants an eyeball on deep
(3+ level) nesting in `runIde`.

**Follow-up fix — frame collapsed to one line ~1s after opening.** The daemon's
code-folding pass runs after the editor settles, rebuilds fold regions from the
Go `FoldingBuilder`, discards our manual range-folds and can auto-collapse the
function body — which drops `funcEnd` to visual line 0, so the re-fit shrank the
frame to a single line. Fix: `isAutoCodeFoldingEnabled = false` on the embedded
editor, so our two range-folds are the only folds and nothing rebuilds them.

**In-frame section folding kept (the surgical version).** Disabling the daemon
would have dropped the *language* fold regions too, so you couldn't collapse
inner blocks in a frame. Instead, with the daemon off we add those folds
ourselves: `addLanguageFolds` asks the registered `FoldingBuilder`
(`LanguageFolding.INSTANCE.forLanguage`) for the fold descriptors in the
function range and adds them expanded-but-collapsible, alongside the boundary
folds, in the same batch. So the gutter fold arrows work inside a frame, the
boundaries never get clobbered, and collapsing a section re-fits the frame
smaller (the height calc is already fold-aware). This is the "B is the prize"
parity getting closer: in-frame folding now works *and* is stable.

Still open from Phase 5: clickable `file:line`, recursion badge (call already
expanded higher in the stack), keyboard nav, depth cap.

---

## Phase 5 — navigation, recursion, in-frame folding (2026-06-23)

One PR (`feat/goland-phase5`) covering the Phase-5 list **except the depth cap**
(deferred). Compiles offline; **still wants a `runIde` eyeball** (no GUI this
session) — see the risk list at the end.

1. **Clickable `file:line`.** The header location is now an `ActionLink`;
   clicking it runs `OpenFileDescriptor(project, vf, range.startOffset)
   .navigate(true)` to jump the main editor to the callee. `FrameChrome.wrap`
   took an `onNavigate: (() -> Unit)?`; a null one (detached snippet) keeps the
   plain `JBLabel`.

2. **Recursion badge.** `Callee` gained a stable `id` (`"<path>#<startOffset>"`,
   identifying the *declaration*, since `title` can collide across packages).
   `ExpansionController` threads `ancestorIds: Set<String>` down the controller
   tree (root empty; child = parent + the id just expanded). When a frame's id is
   already among its ancestors, `expand` passes `recursive = true` and the chrome
   shows an amber "↻ recursive" pill (JCEF renderer: HTML equivalent). Only the
   re-entry is flagged, not the first expansion.

3. **In-frame folding — the surgical fix predicted in Phase 4.** Folding is back
   **on** (`isAutoCodeFoldingEnabled = true`); instead of suppressing the daemon
   we re-assert our boundaries after it runs. `applyBoundaryFolds()` (guarded by a
   `reasserting` flag) force-expands any region *enclosing the whole function*
   (whose collapse would hide the body) and `ensureCollapsed`s the two boundary
   regions `[0,funcStart]` / `[funcEnd,end]`. A
   `FoldingListener.onFoldProcessingEnd` re-runs it + `refit()` after every pass.
   No infinite loop: `runBatchFoldingOperation` fires `onFoldProcessingEnd`
   **synchronously** while `reasserting` is still true, so the re-assert's own
   pass is a no-op. User folds inside the function are subsets of the function
   range, so the enclosing-region safeguard never reopens them; folding the body
   shrinks the frame through the existing `fittedHeight()` path.

4. **Keyboard navigation** (`FrameNavActions.kt`, `FrameKeys.kt`). Three
   context-gated actions (their `update()` disables them off-context, so the
   keystrokes fall through to the platform default when no frame is in play):
   - **Focus Frame** `Ctrl+Alt+PgDn` — descend into the frame on the caret line,
     caret on the body (`FrameKeys.BODY_OFFSET`, seeded by `EditorInlayRenderer`).
   - **Focus Parent** `Ctrl+Alt+PgUp` — ascend to the call site
     (`FrameKeys.PARENT_EDITOR` / `CALL_LINE`, seeded in `ExpansionController.expand`).
   - **Collapse Frame** `Ctrl+Alt+Backspace` — collapse the current frame (focus
     hops to the parent first, since collapse disposes this editor).

   `ExpansionController.existing()` added so action `update()` can read a
   controller without creating one.

### Still wants a runIde eyeball

- In-frame fold/unfold: body folds, frame re-fits, no flicker from the re-assert
  racing the daemon.
- Deep (3+ level) nesting height propagation (carried over from Phase 4).
- `Ctrl+Alt+PgUp/PgDn` no-op when off-context (these are unbound by default, so
  unlike the old `Ctrl+Alt+Up/Down` they don't trigger occurrence-nav).
- Recursion-badge header layout on a real recursive Go call.

### Deferred

Depth cap — out of scope for this PR.
