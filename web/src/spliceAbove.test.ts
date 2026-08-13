import { describe, expect, test } from "bun:test";
import { spliceAbove } from "./Callers";
import { emptySlice, type FrameSlice } from "./viewState";
import type { Usage } from "./types";

// Re-rooting on a caller is a *wrap*, not a reset: the trace you have built
// nests inside the caller at the site that leads to it. Losing it turns "show
// me what calls this" into "start again from there", which is the opposite of
// what the gesture is for.

function usage(over: Partial<Usage> = {}): Usage {
  return {
    callId: "pkg.caller@3",
    choice: 0,
    caller: "pkg.caller",
    callerTitle: "caller",
    file: "a.go",
    line: 1,
    kind: "call",
    excerpt: "",
    excerptLine: 1,
    ...over,
  };
}

const subtree: FrameSlice = {
  folds: [],
  expansions: {
    // An ordinary expansion, with one of its own beneath it.
    "pkg.f@0": {
      folds: [],
      expansions: { "pkg.g@2": { folds: [], expansions: {}, choice: 0 } },
      choice: 0,
    },
    // A boundary's far side, spliced in — the slot a recognizer rule produces.
    "pkg.f@1#leaf": { folds: [], expansions: {}, choice: 1 },
  },
};

describe("spliceAbove", () => {
  test("nests the whole subtree under the caller's call site", () => {
    const wrapped = spliceAbove(usage(), subtree);
    const nested = wrapped.expansions["pkg.caller@3"];

    expect(nested).toBeDefined();
    expect(nested.expansions["pkg.f@0"]).toBeDefined();
    // Depth is preserved, not just the first level.
    expect(nested.expansions["pkg.f@0"].expansions["pkg.g@2"]).toBeDefined();
  });

  test("keeps a boundary open, and on the end that was picked", () => {
    const nested = spliceAbove(usage(), subtree).expansions["pkg.caller@3"];
    const slot = nested.expansions["pkg.f@1#leaf"];

    expect(slot).toBeDefined();
    // The choice is which far end was chosen. Restoring the first one instead
    // would quietly show a different service's code in the same place.
    expect(slot.choice).toBe(1);
  });

  test("carries the candidate that selects the target at the call site", () => {
    // An interface usage reproduces itself only if the implementation it
    // dispatched to is the one re-opened.
    const wrapped = spliceAbove(usage({ choice: 2, kind: "interface" }), subtree);
    expect(wrapped.expansions["pkg.caller@3"].choice).toBe(2);
  });

  test("opens bare when the usage has no site to splice through", () => {
    // Not every engine gives a value reference a site; without one there is
    // nowhere to nest, and an empty slice says so rather than inventing a key.
    expect(spliceAbove(usage({ callId: undefined }), subtree)).toEqual(emptySlice);
  });
});
