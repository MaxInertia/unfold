# Screenshot sample

A small, self-contained Go module for capturing Marketplace/README screenshots
of the Unfold plugin. The code is intentionally photogenic — short functions, a
clean call chain, a `switch`, a recursive walk, and an interface with three
implementations — so each plugin feature has an obvious shot.

## Setup

1. Build/install the plugin (`../../README.md` → Install).
2. Open **this folder** (`examples/screenshots`) as a project in GoLand so the
   Go plugin indexes it and calls resolve. `go build ./...` should pass.
3. Pick a clean color scheme and hide the noise you don't want in frame
   (Project tool window, breadcrumbs) before capturing.

## Recipes

Put the caret on the named call and press **Ctrl+Alt+U** to expand (again to
collapse). Frame controls: **Ctrl+Alt+Down/Up** focus into/out of a frame,
**Ctrl+Alt+Backspace** collapse.

| Shot | Where | What it shows |
|------|-------|---------------|
| **Hero / nesting** | `processOrder(...)` in `main` | Expand it, then expand `validate`, `orderTotal`, and `pay.Charge` *inside* the frame — nested frames, each a deeper rail color. |
| **In-frame folding** | `validate` | Expand it, then use the gutter fold arrows on the `for`/`if` blocks inside the frame; the frame re-fits as they collapse. |
| **Recursion badge** | `quantityOf` | Expand it, then expand the recursive `quantityOf(sub)` call inside the frame — the re-entry shows the amber "↻ recursive" pill. |
| **Implementation picker** | `pay.Charge(total)` in `processOrder` | The call dispatches through the `PaymentMethod` interface, so expanding pops a chooser of `CreditCard` / `PayPal` / `Cash`. |
| **Go-to-definition** | header `file:line` of any frame | Click the location link in the frame header to jump the main editor to the callee. |

## Notes

- `quantityOf`, `PayPal`, and `Cash` are deliberately uncalled — Go allows
  unused functions/types, and they exist purely to give the recursion and
  implementation-picker shots something to resolve.
- Everything compiles (`go build ./...`); keeping it valid is what lets GoLand's
  PSI resolve the calls the plugin expands.
