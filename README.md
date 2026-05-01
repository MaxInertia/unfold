# unfold

A code-reading tool that lets you follow execution paths *linearly* by expanding function calls inline into their implementations, recursively, across files.

When reading code that's heavily decomposed (DI, layered services, lots of small functions), you spend most of your time jumping between files trying to hold the call stack in your head. `unfold` lets you pick a starting symbol, then click any call site to splice the callee's body in directly below — recursively — so a multi-file execution path reads top-to-bottom in one view.

See [`PLAN.md`](./PLAN.md) for architecture, scope, and phasing.

## Stack

- **Indexer**: Go (`go/packages` + `go/types`)
- **Server**: Go (HTTP, embeds frontend assets)
- **Frontend**: Bun + Vite + React + TypeScript, Shiki for syntax highlighting
- **CLI**: single static Go binary
