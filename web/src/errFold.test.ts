import { describe, expect, test } from "bun:test";
import { errCountLabel, planErrFolds } from "./errFold";
import type { ErrReturn } from "./types";

// What "assume err == nil" hides, and what it is not allowed to hide.

const blocks: ErrReturn[] = [
  { startLine: 2, endLine: 4, summary: "returns nil, err" },
  { startLine: 7, endLine: 9, summary: 'wraps "open config"' },
  { startLine: 12, endLine: 12, summary: "panics" }, // one-liner
];

describe("planErrFolds", () => {
  test("hides the lines after each if, keeps the if line, counts blocks", () => {
    const plan = planErrFolds(blocks, new Set(), new Set());
    expect([...plan.hidden].sort((a, b) => a - b)).toEqual([3, 4, 8, 9]);
    expect(plan.hidden.has(2)).toBe(false);
    expect(plan.hidden.has(7)).toBe(false);
    expect([...plan.glyphs.keys()]).toEqual([2, 7, 12]);
    expect(plan.glyphs.get(7)?.summary).toBe('wraps "open config"');
    expect(plan.folded).toBe(3);
  });

  test("a block the reader opened renders its lines", () => {
    const plan = planErrFolds(blocks, new Set([7]), new Set());
    expect(plan.hidden.has(8)).toBe(false);
    expect(plan.glyphs.has(7)).toBe(false);
    expect(plan.folded).toBe(2);
  });

  test("a block with a pinned line inside stays open", () => {
    // A tour stop on line 4 — hiding it would leave the card with no row.
    const plan = planErrFolds(blocks, new Set(), new Set([4]));
    expect(plan.hidden.has(3)).toBe(false);
    expect(plan.glyphs.has(2)).toBe(false);
    // A pin on the if line itself does not count: that line stays visible
    // either way.
    const onIf = planErrFolds(blocks, new Set(), new Set([7]));
    expect(onIf.glyphs.has(7)).toBe(true);
    expect(plan.folded).toBe(2);
  });

  test("no blocks means nothing hidden", () => {
    expect(planErrFolds(undefined, new Set(), new Set()).folded).toBe(0);
    expect(planErrFolds([], new Set(), new Set()).hidden.size).toBe(0);
  });
});

describe("errCountLabel", () => {
  test("pluralises", () => {
    expect(errCountLabel(1)).toBe("1 err return folded");
    expect(errCountLabel(12)).toBe("12 err returns folded");
  });
});
