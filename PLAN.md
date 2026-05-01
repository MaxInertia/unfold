# Unfold — Plan

A code-reading tool that lets you follow execution paths *linearly* by expanding function calls inline into their implementations, recursively, across files. Plannotator-shaped UX (browser app, future annotation flow), but for code instead of markdown.

Project: **unfold**. Repo: `~/projects/unfold`.

## Goal

When reading code that's heavily decomposed (DI, layered services, lots of small functions), you spend most of your time jumping between files trying to hold the call stack in your head. This tool lets you pick a starting symbol, then click any call site to splice the callee's body in directly below the call — recursively — so a multi-file execution path reads top-to-bottom in one view.

## Scope decisions (from Q&A 2026-04-30)

| Decision | Choice | Notes |
|---|---|---|
| Primary language | **Go** | TypeScript (Angular) is the planned second language. Index layer is language-pluggable. |
| Call resolution | **`go/packages` + `go/types`** (same machinery `gopls` uses) | Full type-resolved ASTs. Avoids the LSP RPC overhead and gives us direct programmatic access to the type system. |
| DI / interface methods | Pick first impl, surface a switcher | Mirrors GoLand's "Go to Implementation" picker. |
| Frontend | Browser app (Bun + Vite + React + TypeScript) | Matches plannotator's stack; easier syntax highlighting + interaction than TUI. |
| Annotations | **Phase 2** | Phase 1 is read-only viewing. |
| IDE plugin | Phase 4+ | Out of scope for MVP. |

## Architecture

```
┌────────────────────────────────────────────────────────┐
│  CLI: unfold <pkg>[:<symbol>]                 │
│   - boots local server, opens browser at entry point   │
└──────────────────────┬─────────────────────────────────┘
                       │
        ┌──────────────┴───────────────┐
        │                              │
┌───────▼────────────┐         ┌───────▼─────────────┐
│  Indexer (Go)      │  HTTP   │  Frontend (React)   │
│  - go/packages     │◄───────►│  - Shiki highlight  │
│  - go/types        │  JSON   │  - Expandable calls │
│  - call-site index │         │  - Impl switcher    │
│  - impl index      │         │  - Breadcrumb       │
└────────────────────┘         └─────────────────────┘
```

### Indexer (Go binary)

Runs in-process with the HTTP server. Owns all type-system work.

**Loading**: `packages.Load` with `NeedTypes | NeedTypesInfo | NeedSyntax | NeedDeps | NeedImports | NeedFiles | NeedName | NeedCompiledGoFiles` for the target module. Cache `*packages.Package` set in memory, keyed by module path + go.mod hash.

**Call-site index**: walk every `*ast.CallExpr` in every file. For each call:
- Resolve the call's selector via `types.Info.Selections` / `Uses`.
- If the receiver type is concrete → exactly one target (a `*types.Func` with `decl *ast.FuncDecl`).
- If the receiver type is an interface → enumerate implementers.

**Implementer index**: build once on load. For each interface `I` in the package set, walk all named types `T` and check `types.Implements(T, I.Type())` (and pointer receivers via `types.NewPointer`). Memoize `interface → []concreteType`. This is the same approach `gopls`'s "implementations" feature uses.

**Function-body lookup**: given a `*types.Func`, get its `*ast.FuncDecl` via the package's `Syntax`. Resolve to byte range in the source file. Return source text + child call-site annotations.

**HTTP surface** (all JSON):
- `GET /symbol?pkg=...&name=...` → entry point. Returns `{file, line, col}` and an initial `Frame`.
- `GET /file?path=...&start=...&end=...` → source text + call-site spans (offsets, candidate target IDs, kind: `direct` | `interface`).
- `GET /resolve?callId=...` → list of candidate target IDs with display names (qualified).
- `GET /body?targetId=...` → `Frame` for the chosen callee: source text, call-site spans, language, file path, line range.
- `GET /search?q=...` → fuzzy symbol search (entry-point picker).

`Frame` shape:
```jsonc
{
  "id": "stable-target-id",
  "file": "internal/foo/bar.go",
  "language": "go",
  "startLine": 42,
  "endLine": 87,
  "source": "func DoThing(...) {...}",
  "calls": [
    {
      "id": "call-uuid",
      "spanStart": 120, "spanEnd": 145,         // byte offsets in `source`
      "displayName": "svc.Process",
      "kind": "interface",                       // or "direct"
      "candidates": [                            // null if `direct`
        { "targetId": "...", "label": "*foo.RealProcessor.Process" },
        { "targetId": "...", "label": "*foo.MockProcessor.Process" }
      ]
    }
  ]
}
```

The frontend never has to understand Go semantics — it just renders frames and asks the indexer to expand call IDs.

### Frontend

**Stack**: Vite + React + TypeScript. Shiki for syntax highlighting (works for Go and TS with the same engine; we already need TS support eventually). Tailwind or CSS modules — defer.

**Render model — call frames as nested cards**:

```
┌─ HandlerLogin (login.go:42)  [×]
│  func HandlerLogin(req *Req) {
│    user, err := auth.Validate(req.Token)   ← clickable
│    ┌─ *AuthService.Validate (auth.go:88)  [↳ pick impl] [×]
│    │  func (a *AuthService) Validate(t string) {
│    │    return a.repo.Lookup(t)             ← clickable
│    │    ┌─ *PgRepo.Lookup (pg_repo.go:14)   [↳ 3 impls] [×]
│    │    │  ...
│    │    └─
│    │  }
│    └─
│    if err != nil { ... }
│  }
└─
```

Decision: **nested cards, not text-spliced inline**. Reasons:
- Inline splicing breaks for calls inside expressions / loops / conditionals (where would the body even go?).
- Nested cards preserve the outer function's continuity — you still see what comes *after* the call.
- The indentation *is* the call stack visualization.
- Cards are individually collapsible without re-rendering the parent.

Each frame:
- Header: qualified symbol name, file:line link (copy / open in editor), [×] collapse, [↳] impl-switch (only for interface calls)
- Body: syntax-highlighted source. Call sites are decorated spans (underlined / colored) — clicking expands a child frame *inside* the parent body, not after it. The span stays visible above the expanded child (so the call site remains anchored).

**Impl switcher**: clicking [↳] on an expanded interface call shows a dropdown of candidates. Switching swaps the child frame in place; expansions inside the discarded subtree are lost (or stashed for back/forward — defer).

**Persistence**: expansion state lives in URL hash (deeply nested, but encoded compactly). Reload preserves view; URLs are shareable. (Plannotator-style.)

**Keyboard**:
- `j` / `k` move cursor across visible call sites
- `Enter` / `Space` expand/collapse
- `i` cycle implementations
- `[` collapse all under cursor; `]` expand-all-direct (one level)
- Defer fancier nav.

### CLI

```
unfold ./...                 # index whole module, open picker
unfold ./internal/auth       # narrow scope
unfold ./...:HandlerLogin    # open at symbol
unfold ./...:auth.Validate   # qualified
```

Boots indexer, starts local server on a free port, opens default browser. Single binary (Go). Frontend assets embedded via `embed.FS`.

## Data flow on first expansion

1. User clicks call span in `HandlerLogin` (in browser).
2. Frontend `POST /resolve?callId=...` — wait, just `GET /body?callId=<callId>&choice=0` (default first candidate).
3. Indexer:
   - Look up `callId` → cached `(callee *types.Func, candidates []targetID)`.
   - Pick `candidates[0]`; load body source range; walk its `*ast.CallExpr`s; emit `Frame`.
4. Frontend inserts the new `Frame` into the parent's React tree at the call's anchor.

Cold latency target: < 50ms once index is warm. Index build for a ~50k-LOC module: aim for < 5s.

## Phased delivery

**Phase 1 — viewer MVP** (1–2 weeks of focused work)
- Repo scaffold (`unfold/`): `cmd/cli/`, `internal/indexer/`, `internal/server/`, `web/`.
- Indexer: load packages, build call-site index, build implementer index, body lookup.
- HTTP server with the four core endpoints (`/symbol`, `/file`, `/body`, `/search`).
- Frontend: Shiki highlighting, nested-card render, click-to-expand for *direct* calls only.
- CLI: boot + open browser.
- Smoke test on 2–3 real Go projects (one small, one mid, one DI-heavy).

**Phase 2 — interface dispatch & impl switching**
- Surface candidates in `/body` response.
- Frontend impl-switcher dropdown.
- Persist impl choice per call in URL hash.

**Phase 3 — annotations**
- Reuse plannotator's annotation model (read their `review-editor` package — they've solved this).
- Selection → comment → packaged feedback export.
- Decide later: feed back into agent loop (plannotator-style hook) vs plain JSON dump.

**Phase 4 — TypeScript support**
- Second indexer behind an interface (`Indexer` trait): `LoadProject`, `ResolveCall`, `GetBody`, `FindImplementers`.
- TS implementation via `ts-morph` (full-fat TS compiler API) or tsserver protocol.
- Frontend stays the same — it only consumes `Frame`s.

**Phase 5 — IDE plugin**
- JetBrains plugin that opens the browser viewer pre-pointed at the symbol under the caret. Cheap and reuses everything.

## Known unknowns / decisions to revisit

- **Generics**: `func Foo[T any](x T) {...}` — at a call site the type-parameter is instantiated. For body display we show the generic source, not the instantiation. Probably fine for v1.
- **Recursion**: a function calling itself, directly or transitively. Detect cycle on the active expansion path; render the recursive call as expandable-but-marked-cyclic so the user opts in.
- **Anonymous functions / function literals**: clicking should still expand if we have the AST node. Probably free since `*ast.FuncLit` is in the same syntax tree.
- **Method values / function values**: `f := obj.Foo; f()` — call site target is dynamic. v1: don't resolve, mark as "indirect call (unresolved)".
- **`go` and `defer`**: same as a regular call from an expansion standpoint. UI maybe annotates the frame header (`go` badge / `defer` badge) for context.
- **External deps**: should `fmt.Println` be expandable (into the stdlib)? Probably yes since `go/packages` already loaded deps with `NeedDeps`. Cap depth or mark stdlib frames distinctly.
- **vendored / module-cached deps**: source path resolution must point at the cached source, not assume working-tree. `pkg.CompiledGoFiles` handles this.
- **Watch mode**: do we re-index on file change? v1: no, restart the server. Watcher is straightforward later (`fsnotify` + invalidate touched packages).

## Why not just use gopls?

Considered. `gopls` already has a "References" / "Implementations" / "Hover" surface over LSP. Could drive everything from `gopls` and skip the Go indexer.

Trade-offs:
- ✅ Less code, no risk of diverging from gopls semantics.
- ❌ LSP is request/response per call; we'd issue thousands during indexing.
- ❌ Body source ranges and AST-level call-site enumeration aren't first-class LSP requests — we'd be reverse-engineering them from `documentSymbol` + text positions.
- ❌ Couples us to a running gopls process per project.

Direct `go/packages` is more code but a much better fit for "build a static call/impl index once, query it cheaply many times." Reconsider if the Go indexer becomes a maintenance burden.

## Open questions deferred to start of Phase 1

- License (MIT? Apache-2.0? unlicensed personal tool?)
- Should the local server require an auth token in the URL (plannotator-style) or is bind-to-localhost sufficient? Lean toward token for safety if we ever ship sharing.
