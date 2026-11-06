import { describe, expect, test } from "bun:test";
import { cueLabel, parseCue, resolveCue } from "./cues";

describe("parseCue", () => {
  test("ordinary refs are not cues", () => {
    expect(parseCue("Validate")).toBeNull();
    expect(parseCue("file:a/b.go")).toBeNull();
    expect(parseCue("pkg.(*T).Method")).toBeNull();
  });
  test("line forms", () => {
    expect(parseCue("line:42")).toEqual({
      kind: "line",
      relative: false,
      start: 42,
      end: undefined,
    });
    expect(parseCue("line: +3")).toEqual({
      kind: "line",
      relative: true,
      start: 3,
      end: undefined,
    });
    expect(parseCue("line:42-48")).toEqual({
      kind: "line",
      relative: false,
      start: 42,
      end: 48,
    });
    expect(parseCue("line:+2 - 5")).toEqual({
      kind: "line",
      relative: true,
      start: 2,
      end: 5,
    });
  });
  test("at form keeps the text verbatim", () => {
    expect(parseCue("at: return nil, err")).toEqual({
      kind: "at",
      text: "return nil, err",
    });
  });
  test("malformed cues are invalid, not refs", () => {
    expect(parseCue("line:")).toBe("invalid");
    expect(parseCue("line:0")).toBe("invalid");
    expect(parseCue("line:9-3")).toBe("invalid");
    expect(parseCue("line:abc")).toBe("invalid");
    expect(parseCue("at:")).toBe("invalid");
  });
});

describe("resolveCue", () => {
  const frame = {
    anchor: 10,
    source: "func f() {\n  a()\n  return nil, err\n}",
  };
  test("absolute lines pass through", () => {
    expect(resolveCue(parseCue("line:42") as any, frame)).toEqual({
      line: 42,
      end: undefined,
    });
    expect(resolveCue(parseCue("line:42-44") as any, frame)).toEqual({
      line: 42,
      end: 44,
    });
  });
  test("relative lines count from the anchor", () => {
    expect(resolveCue(parseCue("line:+0") as any, frame)).toEqual({
      line: 10,
      end: undefined,
    });
    expect(resolveCue(parseCue("line:+1-2") as any, frame)).toEqual({
      line: 11,
      end: 12,
    });
  });
  test("at finds the first line containing the text", () => {
    expect(resolveCue(parseCue("at:return nil") as any, frame)).toEqual({
      line: 12,
    });
    expect(resolveCue(parseCue("at:nowhere") as any, frame)).toBeNull();
    expect(resolveCue(parseCue("at:a()") as any, { anchor: 10 })).toBeNull();
  });
  test("a file stop: at counts from the file's first line, +N from the stop's", () => {
    // The whole file is the source; the stop is on line 3.
    const file = { anchor: 3, sourceLine: 1, source: frame.source };
    expect(resolveCue(parseCue("at:return nil") as any, file)).toEqual({
      line: 3,
    });
    expect(resolveCue(parseCue("at:func f") as any, file)).toEqual({ line: 1 });
    expect(resolveCue(parseCue("line:+1") as any, file)).toEqual({
      line: 4,
      end: undefined,
    });
  });
});

test("cueLabel", () => {
  expect(cueLabel(parseCue("line:42") as any)).toBe("line 42");
  expect(cueLabel(parseCue("line:+2-5") as any)).toBe("lines +2–+5");
  expect(cueLabel(parseCue("at:x") as any)).toBe("at: x");
});
