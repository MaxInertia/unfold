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
- **`EditorInlayRenderer`** (the only renderer) — a real read-only `EditorEx`
  embedded via `EditorEmbeddedComponentManager` over the callee file (native
  code: semantic colors, hover, go-to-def, folding). The early painted/JCEF
  experiments were removed in 0.1.2 once this proved out as the best approach.
- **`Ctrl+Alt+U`** expands the call under the caret (again collapses);
  `Ctrl+Alt+Down/Up` focus into/out of a frame, `Ctrl+Alt+Backspace` collapses.

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

## Releasing to the JetBrains Marketplace

`plugin.xml` and `build.gradle.kts` are Marketplace-ready (HTML description,
change-notes, vendor email/url, `signing`/`publishing` blocks). What's left is
secret material + the one-time submission, all driven by environment variables
so nothing sensitive lands in git (`.gitignore` blocks `*.pem`, `chain.crt`, …).

### One-time: generate a signing certificate

```sh
openssl genpkey -aes-256-cbc -algorithm RSA -out private.pem -pkeyopt rsa_keygen_bits:4096
openssl req -key private.pem -new -x509 -days 3650 -out chain.crt
```

Keep `private.pem` + its password somewhere safe (a password manager); losing
them means you can't sign upgrades under the same key.

### Each release

```sh
export CERTIFICATE_CHAIN_FILE=/abs/path/chain.crt
export PRIVATE_KEY_FILE=/abs/path/private.pem
export PRIVATE_KEY_PASSWORD=…           # the password set above
export PUBLISH_TOKEN=…                   # plugins.jetbrains.com → My Tokens (permanent)

./gradlew signPlugin                     # produces a signed zip under build/distributions/
./gradlew publishPlugin                  # uploads it (after first approval — see below)
```

Bump `version` in `build.gradle.kts` and add a `<change-notes>` entry first
(the CLAUDE.md convention), or the upload is rejected as a duplicate.

### First submission is manual + moderated

The very first version must be uploaded by hand and pass JetBrains moderation
(a few business days) before `publishPlugin` works:

1. `./gradlew signPlugin`
2. plugins.jetbrains.com → **Upload plugin** → pick the signed zip.
3. Choose a **license** (required) and confirm the listing.
4. Wait for approval; thereafter `publishPlugin` handles uploads.
