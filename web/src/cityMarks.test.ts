import { describe, expect, test } from "bun:test";
import { tourStopTargets, unfoldedTargets } from "./cityMarks";
import type { Frame } from "./types";
import type { FrameSlice } from "./viewState";

function frame(id: string, calls: Frame["calls"]): Frame {
  return {
    id,
    file: "/x.go",
    language: "go",
    startLine: 1,
    endLine: 2,
    source: "",
    calls,
  };
}

describe("unfoldedTargets", () => {
  const root = frame("pkg.Root", [
    {
      id: "c1",
      spanStart: 0,
      spanEnd: 1,
      displayName: "A",
      kind: "direct",
      targetId: "pkg.A",
    },
    {
      id: "c2",
      spanStart: 0,
      spanEnd: 1,
      displayName: "I.M",
      kind: "interface",
      candidates: [
        { targetId: "pkg.Impl1.M", label: "Impl1" },
        { targetId: "pkg.Impl2.M", label: "Impl2" },
      ],
    },
    {
      id: "c3",
      spanStart: 0,
      spanEnd: 1,
      displayName: "Publish",
      kind: "direct",
      receivers: [
        { targetId: "svc.OnA", label: "OnA" },
        { targetId: "svc.OnB", label: "OnB" },
      ],
    },
  ]);
  const a = frame("pkg.A", [
    {
      id: "c4",
      spanStart: 0,
      spanEnd: 1,
      displayName: "B",
      kind: "direct",
      targetId: "pkg.B",
    },
  ]);

  test("names targets from the call sites before their frames arrive", () => {
    const slice: FrameSlice = {
      folds: [],
      expansions: {
        c1: { folds: [], expansions: {}, choice: 0 },
        c2: { folds: [], expansions: {}, choice: 1 },
      },
      fanouts: { c3: { 1: { folds: [], expansions: {} } } },
    };
    expect(unfoldedTargets(root, slice, () => undefined)).toEqual([
      "pkg.A",
      "pkg.Impl2.M",
      "svc.OnB",
    ]);
  });

  test("descends through loaded frames", () => {
    const slice: FrameSlice = {
      folds: [],
      expansions: {
        c1: {
          folds: [],
          expansions: { c4: { folds: [], expansions: {}, choice: 0 } },
          choice: 0,
        },
      },
    };
    const peek = (callId: string) => (callId === "c1" ? a : undefined);
    expect(unfoldedTargets(root, slice, peek)).toEqual(["pkg.A", "pkg.B"]);
    // Without the child frame the grandchild can't be named yet.
    expect(unfoldedTargets(root, slice, () => undefined)).toEqual(["pkg.A"]);
  });

  test("no root, nothing lit", () => {
    expect(
      unfoldedTargets(null, { folds: [], expansions: {} }, () => undefined),
    ).toEqual([]);
  });
});

describe("tourStopTargets", () => {
  test("numbers the resolved symbol stops, skipping file stops and pending ones", () => {
    const m = tourStopTargets(
      { length: 4 },
      {
        0: { kind: "symbol", targetId: "pkg.A" },
        1: { kind: "file" },
        2: "pending",
        3: { kind: "symbol", targetId: "pkg.A" },
      },
    );
    expect([...m]).toEqual([["pkg.A", [1, 4]]]);
  });
});
