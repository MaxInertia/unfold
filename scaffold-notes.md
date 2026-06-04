# Scaffold notes

This is a **starting point written without a GoLand SDK to compile against** —
expect to bump versions and fix a few API names. The structure and the loop are
the point; the exact code is yours to iterate.

## Files

- `build.gradle.kts` / `settings.gradle.kts` / `gradle.properties` — IntelliJ
  Platform Gradle Plugin 2.x targeting **GoLand** (so the Go PSI API is on the
  classpath). If you'd rather use IDEA Ultimate, swap `goland(...)` for
  `idea("IU", ...)` and keep `bundledPlugin("org.jetbrains.plugins.go")`.
- `META-INF/plugin.xml` — declares the plugin, `depends` on the Go plugin, and
  registers the **Unfold: Expand Call** action (Ctrl+Alt+U + context menu).
- `ExpandCallAction.kt` — Phase 0: toggles a block inlay below the caret line.
  `resolveCalleeText()` is the Phase 1 hook — make it resolve the real callee
  via PSI.
- `CodeBlockInlayRenderer.kt` — experiment **A** (painted text). Phase 3A: color
  per-token from the IDE highlighter. Experiment **B** replaces this with an
  embedded `EditorEx` viewer.

## Run it

```
cd unfold-goland
./gradlew runIde      # launches a sandbox GoLand with the plugin
```
(You'll need the Gradle wrapper — run `gradle wrapper` once if `./gradlew`
is missing, or open the folder in IntelliJ/GoLand and let it generate one.)

Open a Go file, put the caret on a call, hit **Ctrl+Alt+U** → a block inlay
appears below the line; hit it again to remove it.

## Then, in order

1. **Phase 1** — implement `resolveCalleeText`: `PsiUtilCore.getElementAtOffset`
   → nearest `GoCallExpr` → `.getExpression()` as `GoReferenceExpression` →
   `.resolve()` → `GoFunctionDeclaration`/`GoMethodDeclaration` → `.getBlock()`
   text. (Verify class/method names against the Go plugin SDK.)
2. **Phase 3A** — make `CodeBlockInlayRenderer.paint` color tokens via
   `SyntaxHighlighterFactory` + `EditorColorsScheme` so it matches the file.
3. **Phase 3B** — the prize: swap in a real read-only `EditorEx` between the
   lines (see `PLAN.md` → "The crux" → B). This is what makes it look *and*
   behave like the surrounding code.

See `PLAN.md` for the full phased plan and the rendering deep-dive.

## Current status (all compile-verified against GoLand 2024.1.4 + Go plugin)

`./gradlew buildPlugin` produces an installable plugin. What's wired:

- **PSI resolution** (`PsiResolve.kt`) — the Go call under the caret →
  `GoFunctionOrMethodDeclaration` (direct calls; interface-impl picker TODO).
- **Three swappable renderers** behind `FrameRenderer`:
  - `PaintedRenderer` — block inlay, colored token-by-token via the IDE's Go
    `SyntaxHighlighter` + scheme (looks native; no native interactivity).
  - **`EditorInlayRenderer`** — the goal: a real read-only `EditorEx` embedded
    via `EditorEmbeddedComponentManager` (native code).
  - `JcefRenderer` — a JCEF browser (the reuse-the-web path; falls back to
    painted if JCEF is off).
- **Selectable in GoLand** — `Settings > Tools > Unfold` (`UnfoldConfigurable`)
  and a quick **Ctrl+Alt+R** popup (`SelectRendererAction`); choice persists
  (`UnfoldSettings`). **Ctrl+Alt+U** expands the call under the caret with the
  selected renderer; again collapses.

### What still needs a *running* IDE (I can compile but not run the GUI here)
1. **`EditorInlayRenderer` bounds/scroll/sizing** — it compiles and uses the
   right embed API, but the visual height/scroll-sync will need tuning live.
   This is the make-or-break experiment — try it first.
2. To get **go-to-def / find-usages** inside the embedded editor (not just
   highlighting), back the sub-editor with the real callee file shown as a
   range, instead of the current detached `LightVirtualFile` copy.
3. **Recursion/nesting** — currently one frame at a time (toggle). Phase 4.
4. **Per-call affordances** (underline/click like unfold) — Phase 2; today you
   trigger via caret + Ctrl+Alt+U.

Try `EDITOR` first (default), compare against `PAINTED`, and switch with
Ctrl+Alt+R.
