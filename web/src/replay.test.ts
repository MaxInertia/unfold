import { describe, expect, test } from "bun:test";
import {
  Timeline,
  closureBase,
  diffTrees,
  findCallSite,
  place,
  shortFn,
} from "./replay";
import type { CallSite, Frame, TraceEvent } from "./types";
import type { FrameSlice } from "./viewState";

// The mapping from "events so far" to "what the view shows". The frames are
// fakes with just enough shape: a source whose call sites sit on known
// lines, and targets the trace names.

const PKG = "example.com/m/calc";

function call(
  id: string,
  line: number,
  targetId?: string,
  extra: Partial<CallSite> = {},
): CallSite {
  // spanStart counts newlines, so a site on body line `line` (0-based) is
  // placed after that many of them.
  return {
    id,
    spanStart: line,
    spanEnd: line,
    displayName: id,
    kind: "direct",
    targetId,
    ...extra,
  };
}

function frame(
  fn: string,
  startLine: number,
  calls: CallSite[],
  lines = 10,
): Frame {
  return {
    id: fn,
    file: `/p/${shortFn(fn)}.go`,
    language: "go",
    startLine,
    endLine: startLine + lines - 1,
    source: "\n".repeat(lines - 1),
    calls,
  };
}

// Sum (lines 20-29) calls each on body line 2 (file line 22) and Acc.Add on
// body line 4 (file line 24); each (40-49) calls its callback on body line 2.
const frames: Record<string, Frame> = {
  [`${PKG}.Sum`]: frame(`${PKG}.Sum`, 20, [
    call("c-each", 2, `${PKG}.each`),
    call("c-add", 4, `(*${PKG}.Acc).Add`),
    call("c-iface", 6, undefined, {
      kind: "interface",
      candidates: [
        { targetId: `${PKG}.A`, label: "A" },
        { targetId: `${PKG}.B`, label: "B" },
      ],
    }),
  ]),
  [`${PKG}.each`]: frame(`${PKG}.each`, 40, []),
  [`(*${PKG}.Acc).Add`]: frame(`(*${PKG}.Acc).Add`, 5, []),
  [`${PKG}.B`]: frame(`${PKG}.B`, 60, []),
};
const lookup = (fn: string) => (fn in frames ? frames[fn] : null);

const enter = (g: number, fn: string, line: number): TraceEvent => ({
  g,
  kind: "enter",
  fn,
  line,
  t: 0,
});
const line = (g: number, l: number): TraceEvent => ({
  g,
  kind: "line",
  line: l,
  t: 0,
});
const exit = (g: number): TraceEvent => ({ g, kind: "exit", t: 0 });

// The test calls Sum, which calls each, which runs Sum's closure, which
// calls Add — the shape the Go integration test records.
const run: TraceEvent[] = [
  enter(1, `${PKG}.TestSum`, 5), // 0
  line(1, 6), // 1
  enter(1, `${PKG}.Sum`, 20), // 2
  line(1, 21), // 3
  line(1, 22), // 4
  enter(1, `${PKG}.each`, 40), // 5
  line(1, 41), // 6
  line(1, 42), // 7
  enter(1, `${PKG}.Sum.func1`, 23), // 8
  line(1, 24), // 9
  enter(1, `(*${PKG}.Acc).Add`, 5), // 10
  line(1, 6), // 11
  exit(1), // 12  Add
  exit(1), // 13  func1
  exit(1), // 14  each
  line(1, 26), // 15
  enter(1, `${PKG}.B`, 60), // 16
  exit(1), // 17  B
  exit(1), // 18  Sum
  exit(1), // 19  TestSum
];

describe("Timeline", () => {
  test("rebuilds the stack at any index", () => {
    const tl = new Timeline(run);
    expect(tl.stackAt(-1)).toEqual([]);
    expect(tl.stackAt(1)).toEqual([{ fn: `${PKG}.TestSum`, line: 6 }]);
    expect(tl.stackAt(11).map((e) => shortFn(e.fn))).toEqual([
      "TestSum",
      "Sum",
      "each",
      "Sum.func1",
      "Acc.Add",
    ]);
    expect(tl.stackAt(11)[4].line).toBe(6);
    expect(tl.stackAt(14).map((e) => shortFn(e.fn))).toEqual([
      "TestSum",
      "Sum",
    ]);
    expect(tl.stackAt(19)).toEqual([]);
    // Past the end clamps.
    expect(tl.stackAt(99)).toEqual([]);
  });

  test("checkpoints agree with a straight replay, forward and backward", () => {
    const events: TraceEvent[] = [];
    for (let i = 0; i < 300; i++) {
      events.push(enter(1, `f${i}`, i), line(1, i + 1), line(1, i + 2));
    }
    for (let i = 0; i < 300; i++) events.push(exit(1));
    const small = new Timeline(events, 7);
    const plain = new Timeline(events, 1e9);
    for (const i of [0, 6, 7, 8, 13, 14, 450, 899, 900, 1199, 1100, 50]) {
      expect(small.stackAt(i)).toEqual(plain.stackAt(i));
    }
  });
});

describe("place", () => {
  const tl = new Timeline(run);
  const at = (i: number) => place(tl.stackAt(i), lookup);

  test("the test function is not indexed, so the first known function is the root", () => {
    expect(at(1).root).toBeNull();
    expect(at(1).cursor).toBeNull();
    expect(at(1).unplaced).toEqual([`${PKG}.TestSum`]);
    const v = at(3);
    expect(v.root).toBe(`${PKG}.Sum`);
    expect(v.tree.expansions).toEqual({});
    expect(v.cursor).toEqual({ key: "", file: "/p/Sum.go", line: 21 });
  });

  test("entering a callee unfolds its call site and moves the cursor inside", () => {
    const v = at(6);
    expect(v.tree.expansions["c-each"]).toEqual({
      folds: [],
      expansions: {},
      choice: 0,
    });
    expect(v.cursor).toEqual({ key: "c-each#0", file: "/p/each.go", line: 41 });
  });

  test("a closure highlights in the frame that declares it, and its callees unfold there", () => {
    const v = at(9);
    expect(v.cursor).toEqual({ key: "", file: "/p/Sum.go", line: 24 });
    expect(v.unplaced).toEqual([`${PKG}.TestSum`]);
    const w = at(11);
    expect(w.tree.expansions["c-add"]).toBeDefined();
    expect(w.tree.expansions["c-each"].expansions).toEqual({});
    expect(w.cursor).toEqual({
      key: "c-add#0",
      file: "/p/Acc.Add.go",
      line: 6,
    });
  });

  test("exits fold what they leave", () => {
    expect(Object.keys(at(12).tree.expansions)).toEqual(["c-each"]);
    expect(at(12).cursor).toEqual({ key: "", file: "/p/Sum.go", line: 24 });
    expect(at(14).tree.expansions).toEqual({});
    expect(at(15).cursor).toEqual({ key: "", file: "/p/Sum.go", line: 26 });
  });

  test("an interface call picks the implementation that ran", () => {
    const v = at(16);
    expect(v.tree.expansions["c-iface"]).toEqual({
      folds: [],
      expansions: {},
      choice: 1,
    });
    expect(v.cursor).toEqual({ key: "c-iface#1", file: "/p/B.go", line: 60 });
  });

  test("after the root returns there is nothing to show", () => {
    expect(at(18).root).toBeNull();
    expect(at(19).stack).toEqual([]);
  });

  test("a callee the index lacks leaves the cursor on the call line", () => {
    const events = [
      enter(1, `${PKG}.Sum`, 20),
      line(1, 22),
      enter(1, "fmt.Println", 1),
      line(1, 2),
      exit(1),
      line(1, 23),
    ];
    const t = new Timeline(events);
    const v = place(t.stackAt(3), lookup);
    expect(v.root).toBe(`${PKG}.Sum`);
    expect(v.unplaced).toEqual(["fmt.Println"]);
    expect(v.cursor).toEqual({ key: "", file: "/p/Sum.go", line: 22 });
    expect(place(t.stackAt(5), lookup).cursor?.line).toBe(23);
  });

  test("a known callee with no matching call site is unplaced, and so is everything above it", () => {
    const events = [
      enter(1, `${PKG}.Sum`, 20),
      line(1, 21),
      enter(1, `${PKG}.B`, 60),
      enter(1, `${PKG}.each`, 40),
      line(1, 41),
    ];
    const t = new Timeline(events);
    // B is called through the interface site; each from B has no site in B.
    const v = place(t.stackAt(4), lookup);
    expect(v.unplaced).toEqual([`${PKG}.each`]);
    expect(v.cursor?.key).toBe("c-iface#1");
  });

  test("a frame not yet fetched is treated as unknown until it arrives", () => {
    const t = new Timeline(run);
    const pending = () => undefined;
    expect(place(t.stackAt(3), pending).root).toBeNull();
  });
});

describe("findCallSite", () => {
  const f = frames[`${PKG}.Sum`];
  test("prefers the site on the caller's line, then the next one after it", () => {
    const two: Frame = {
      ...f,
      calls: [
        call("first", 2, `${PKG}.each`),
        call("second", 7, `${PKG}.each`),
      ],
    };
    expect(findCallSite(two, 22, `${PKG}.each`)?.callId).toBe("first");
    expect(findCallSite(two, 27, `${PKG}.each`)?.callId).toBe("second");
    // Statement started on line 25, call written two lines later.
    expect(findCallSite(two, 25, `${PKG}.each`)?.callId).toBe("second");
    // Nothing after: the one before.
    expect(findCallSite(two, 29, `${PKG}.each`)?.callId).toBe("second");
  });
  test("a reference site is a last resort", () => {
    const refd: Frame = {
      ...f,
      calls: [
        call("as-value", 2, `${PKG}.each`, { kind: "ref" }),
        call("called", 8, `${PKG}.each`),
      ],
    };
    expect(findCallSite(refd, 22, `${PKG}.each`)?.callId).toBe("called");
    expect(
      findCallSite({ ...f, calls: [refd.calls[0]] }, 22, `${PKG}.each`)?.callId,
    ).toBe("as-value");
  });
  test("nothing matches nothing", () => {
    expect(findCallSite(f, 22, "nope")).toBeNull();
  });
});

describe("diffTrees", () => {
  const leaf = (choice = 0): FrameSlice & { choice: number } => ({
    folds: [],
    expansions: {},
    choice,
  });
  test("opens and closes what changed, parents first", () => {
    const prev: FrameSlice = { folds: [], expansions: { a: leaf() } };
    const next: FrameSlice = {
      folds: [],
      expansions: { b: { folds: [], expansions: { c: leaf(1) }, choice: 0 } },
    };
    expect(diffTrees(prev, next)).toEqual([
      { op: "expand", path: [], callId: "b", choice: 0 },
      {
        op: "expand",
        path: [{ callId: "b", choice: 0 }],
        callId: "c",
        choice: 1,
      },
      { op: "collapse", path: [], callId: "a" },
    ]);
  });
  test("an unchanged tree is no ops; a changed choice reopens", () => {
    const t: FrameSlice = { folds: [], expansions: { a: leaf(0) } };
    expect(diffTrees(t, t)).toEqual([]);
    expect(diffTrees(t, { folds: [], expansions: { a: leaf(2) } })).toEqual([
      { op: "expand", path: [], callId: "a", choice: 2 },
    ]);
  });
});

describe("names", () => {
  test("closureBase", () => {
    expect(closureBase("p.Outer.func1")).toBe("p.Outer");
    expect(closureBase("p.Outer.func12.3")).toBe("p.Outer");
    expect(closureBase("(*p.T).M.func1")).toBe("(*p.T).M");
    expect(closureBase("p.Outer")).toBeNull();
    expect(closureBase("p.funcs")).toBeNull();
  });
  test("shortFn", () => {
    expect(shortFn("github.com/x/y/tours.Validate")).toBe("Validate");
    expect(shortFn("(*github.com/x/y/tours.Store).List")).toBe("Store.List");
    expect(shortFn("(github.com/x/y/tours.Tour).Name")).toBe("Tour.Name");
    expect(shortFn("github.com/x/y/tours.Validate.func1")).toBe(
      "Validate.func1",
    );
    expect(shortFn("(*github.com/x/y/tours.Store).List.func2.1")).toBe(
      "Store.List.func2.1",
    );
  });
});
