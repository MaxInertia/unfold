# Plan: TypeScript / Angular support in the GoLand plugin

Status: **implemented** on `feat/goland-plugin-typescript` (2026-06-29), v0.2.0 —
compiles and builds against the real JS PSI SDK; runtime behavior to be confirmed
in a live IDE (`./gradlew runIde`). Owner: maxinertia.
Spike companion to [`PLAN.md`](./PLAN.md) and [`scaffold-notes.md`](./scaffold-notes.md).

## What shipped vs. this plan

- **`CalleeResolver` seam** (`PsiResolve.kt`) — dispatches by file language id
  (`go` → `GoCalleeResolver`, JS/TS ids → `TsCalleeResolver`), referencing the JS
  resolver only inside the JS/TS branch so its classes load lazily.
- **`GoCalleeResolver`** — the former `PsiResolve` body, verbatim, now sets
  `Callee.fileType`.
- **`TsCalleeResolver`** — JS PSI: `JSCallExpression.methodExpression` →
  `JSReferenceExpression.resolve()` → `JSFunction`/arrow-const via `JSVariable`;
  direct expand when it has a body, else implementations via
  `DefinitionsScopedSearch` (with fallback to expanding the function itself).
  Class title via `JSClass` (`…psi.ecmal4.JSClass`).
- **`Callee.fileType`** added; the detached fallback now builds its
  `LightVirtualFile` from it instead of hardcoding `GoFileType`.
- **Manifest/build** — `bundledPlugin("JavaScript")`, optional
  `<depends … config-file="unfold-js.xml">JavaScript</depends>`, broadened
  description, version 0.1.2 → **0.2.0**.

Still live-only to confirm (per the plan's risk list): interface-impl dispatch in
TS, import indirection, and the rare detached-TS path. Angular templates remain
out of scope (Phase 6).

---

## Original plan

## Goal

Make inline call unfolding work for **TypeScript** (and JS) calls in the same way
it works for Go today — caret on a call, callee body spliced in below as a native
embedded-editor frame, nesting and folding intact. Run in **the same GoLand IDE**:
GoLand bundles the JavaScript/TypeScript plugin, so TS resolves natively; no need
for WebStorm or IDEA Ultimate.

## Key finding from the spike

The plugin is **already ~90% language-agnostic**. The expansion machinery never
mentions Go:

- `ExpansionController` works on lines/offsets/`Callee.id` — no language.
- `Frame` / `FrameChrome` / `FrameKeys` / nav actions — pure UI over an editor.
- `EditorInlayRenderer.render()` (the **primary** path) embeds a viewer over the
  **real callee file's `Document`** and gets its highlighter from
  `EditorHighlighterFactory.createEditorHighlighter(project, vf)` — which is
  driven by the file's type. A TS callee file gets TS highlighting, folding,
  hover, and go-to-def **for free**. Since real TS functions always have an
  on-disk file, they take this path.

The Go coupling is confined to **one file plus three small spots**:

| Where | Coupling | Fix |
|---|---|---|
| `PsiResolve.kt` | 100% Go PSI (`GoCallExpr` → `GoFunctionOrMethodDeclaration`) | The real work — make it language-dispatched (below) |
| `EditorInlayRenderer.kt:227` | `LightVirtualFile(..., GoFileType.INSTANCE, ...)` in the **detached fallback only** | Derive file type from the callee (carry it on `Callee`) |
| `ExpandCallAction.kt:41` | user string "no resolvable **Go** call" | Make language-neutral ("no resolvable call") |
| `plugin.xml` / `build.gradle.kts` | hard `org.jetbrains.plugins.go` dep | Add the bundled `JavaScript` plugin (optional depends) |

So this is genuinely a resolver port, not a rewrite of the rendering core.

## Design: a language-dispatched resolver

Introduce a small seam so each language contributes its own call resolution; the
action stays language-blind.

```kotlin
interface CalleeResolver {
    /** Targets for the call at the caret, or empty if none. */
    fun calleesAtCaret(editor: Editor, project: Project): List<Callee>
}
```

- `GoCalleeResolver` — the current `PsiResolve` body, moved verbatim.
- `TsCalleeResolver` — the new JS/TS implementation (below).
- `PsiResolve.calleesAtCaret` becomes a dispatcher: pick the resolver by the PSI
  file's language at the caret, then `return resolver.calleesAtCaret(...)`.

**Loading safety (important):** referencing JS PSI classes when the JavaScript
plugin isn't present throws `NoClassDefFoundError`. Two ways to keep it safe:

- **MVP:** make `<depends>org.jetbrains.plugins.go</depends>` stay, add
  `<depends optional="true" config-file="unfold-js.xml">JavaScript</depends>`,
  and register `TsCalleeResolver` via a language-keyed extension point declared
  only in `unfold-js.xml`. The JS code is class-loaded only when the JS plugin
  is. This is the clean target.
- **Quicker spike:** keep one dispatcher but guard the JS branch behind a
  `PluginManagerCore.getPlugin(PluginId.getId("JavaScript"))?.isEnabled` check and
  isolate JS PSI references in a separate class only touched after that check.

Recommend the extension-point version for the real implementation; the guarded
version is fine to prove the resolution end-to-end first.

## TS/JS resolution (the heart of the work)

Mirror the Go resolver's three outcomes (none / one direct / many impls) using
JS PSI (`com.intellij.lang.javascript.psi.*`). API names below **need verifying
against the live JS SDK** — same caveat the Go scaffold carried.

1. **Find the call:** `PsiTreeUtil.getParentOfType(element, JSCallExpression)`.
2. **Get the callee ref:** `call.methodExpression` (a `JSExpression`, usually a
   `JSReferenceExpression`).
3. **Resolve:** `(methodExpression as JSReferenceExpression).resolve()`.
4. **Map the resolved element to a `Callee`**, handling TS's shapes:
   - **Function / method declaration** — `JSFunction` / `TypeScriptFunction` with
     a body (`fn.block != null`). Direct expand. Title = `name`, or
     `Class.name` via `JSUtils.getMemberContainingClass(fn)?.name`.
   - **Arrow / function expression assigned to a const** —
     `const f = () => {…}`: resolve lands on a `JSVariable`; follow
     `.initializer` to a `JSFunctionExpression` and use that. (No Go analogue —
     TS-specific wrinkle.)
   - **Interface / abstract method** (`TypeScriptFunctionSignature`, no body) —
     enumerate implementations and show the same chooser the Go path uses.
     Try the generic `DefinitionsScopedSearch.search(resolved)` first (JS
     registers a provider); fall back to `JSDefinitionsSearch` if needed.
   - **Imported symbol** — `.resolve()` should already cross the import to the
     real declaration; verify it doesn't stop at the `ES6ImportedBinding`.
5. **Build `Callee`** exactly as Go does: `text = decl.text`, `sourceFile =
   decl.containingFile.virtualFile`, `range = decl.textRange`, `id =
   "$path#$startOffset"`. The renderer handles the rest unchanged.

## Renderer change (tiny)

Carry the language on the callee so the **detached fallback** stops hardcoding Go:

```kotlin
data class Callee(..., val fileType: FileType)   // set from decl.containingFile.fileType
// renderDetached: LightVirtualFile("unfold-frame", callee.fileType, callee.text)
```

The primary `render()` path needs **no change** — it already derives everything
from the real file.

## Build / manifest

- `build.gradle.kts` — add `bundledPlugin("JavaScript")` next to the Go one
  (bundled in the GoLand the build already downloads, so no new download target).
- `plugin.xml` — add the optional `JavaScript` depends + `unfold-js.xml`; broaden
  the `<description>` and `text=`/messages from "Go" to "Go and TypeScript".
- Per `CLAUDE.md`: **minor** version bump (new feature) before the first build
  that ships this.

## Phasing

1. **Spike (prove resolution):** guarded dispatcher + `TsCalleeResolver` for the
   simplest case — a direct top-level `function foo()` call in a `.ts` file.
   Confirm a frame renders with native TS highlighting. *(De-risks the whole
   thing; everything downstream is shared code already working for Go.)*
2. **Method + class titles + arrow-const** resolution.
3. **Interface/abstract dispatch** via the implementations chooser.
4. **Harden loading** — move to the optional-config-file extension point.
5. **Detached fallback** file-type generalization + manifest/description/version.
6. **Angular (separate, optional):** TS class methods work via the above with no
   extra effort. Angular **template** expressions (`.html` bindings, selectors)
   rely on the Angular plugin, which is **not bundled in GoLand** (installable;
   bundled in WebStorm/IDEA Ultimate). Treat template-call unfolding as its own
   follow-up; out of scope for TS-in-`.ts` support.

## Risks / verify live

- **JS PSI API names** (`JSCallExpression.methodExpression`, `JSFunction.block`,
  `JSUtils.getMemberContainingClass`, `JSDefinitionsSearch`) — confirm against the
  SDK; treat as illustrative until then.
- **`DefinitionsScopedSearch` for TS** — confirm it returns implementers for an
  interface method; otherwise use the JS-specific search.
- **Import indirection** — ensure `.resolve()` reaches the declaration, not the
  import binding.
- **Detached path for TS** — rare (TS callees have files), but verify the
  `LightVirtualFile` file-type swap highlights correctly.

## Bottom line

Same IDE, no new heavy dependency, rendering core untouched. The deliverable is
one new resolver class (+ a small dispatch seam and a one-field `Callee` change).
Start with Phase 1 to prove TS resolution renders a native frame; the rest is
incremental.
