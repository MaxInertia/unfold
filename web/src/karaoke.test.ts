import { describe, expect, test } from "bun:test";
import { buildTimeline, lineAt } from "./karaoke";
import { stopSpeech } from "./speech";
import type { CallSite } from "./types";

const source = ["func read() {", "  a()", "  b()", "  Validate(t)", "  return", "}"].join("\n");
const call = (displayName: string, line: number): CallSite => ({
  id: displayName,
  spanStart: source.split("\n").slice(0, line).join("\n").length + 1,
  spanEnd: 0,
  displayName,
  kind: "direct",
  targetId: "pkg." + displayName,
});
const frame = { source, calls: [call("a", 1), call("b", 2), call("Validate", 3)], lineCount: 6 };

describe("stopSpeech refs", () => {
  test("remembers where each ref is spoken", () => {
    const s = stopSpeech({ symbol: "read", description: "Calls [[Validate]] then returns." }, 0, 1);
    expect(s.text).toBe("Stop 1 of 1: read. Calls Validate then returns.");
    expect(s.refs).toEqual([{ char: s.text.indexOf("Validate then"), name: "Validate" }]);
  });
});

describe("buildTimeline", () => {
  test("start, each named call, end", () => {
    const s = stopSpeech({ symbol: "read", description: "Calls [[Validate]] then returns." }, 0, 1);
    const tl = buildTimeline(s, frame);
    expect(tl).toEqual([
      { char: 0, line: 0 },
      { char: s.refs[0].char, line: 3 },
      { char: s.text.length, line: 5 },
    ]);
  });
  test("a ref that is not a call in the frame is skipped", () => {
    const s = stopSpeech({ symbol: "read", description: "See [[elsewhere]]." }, 0, 1);
    expect(buildTimeline(s, frame)).toEqual([
      { char: 0, line: 0 },
      { char: s.text.length, line: 5 },
    ]);
  });
});

describe("lineAt", () => {
  const tl = [
    { char: 0, line: 0 },
    { char: 100, line: 3 },
    { char: 200, line: 5 },
  ];
  test("drifts from the start to the first waypoint", () => {
    expect(lineAt(tl, 0)).toBe(0);
    expect(lineAt(tl, 50)).toBe(2);
    expect(lineAt(tl, 99)).toBe(3);
  });
  test("holds on a named call before drifting on", () => {
    expect(lineAt(tl, 100)).toBe(3);
    expect(lineAt(tl, 120)).toBe(3);
    expect(lineAt(tl, 200)).toBe(5);
    expect(lineAt(tl, 999)).toBe(5);
  });
  test("a single waypoint is a fixed line", () => {
    expect(lineAt([{ char: 0, line: 4 }], 50)).toBe(4);
  });
});
