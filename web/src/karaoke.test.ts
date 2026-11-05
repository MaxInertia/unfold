import { describe, expect, test } from "bun:test";
import { buildTimeline, lineAt } from "./karaoke";
import { stopSpeech } from "./speech";
import type { CallSite } from "./types";

const source = [
  "func read() {",
  "  a()",
  "  b()",
  "  Validate(t)",
  "  return",
  "}",
].join("\n");
const call = (displayName: string, line: number): CallSite => ({
  id: displayName,
  spanStart: source.split("\n").slice(0, line).join("\n").length + 1,
  spanEnd: 0,
  displayName,
  kind: "direct",
  targetId: "pkg." + displayName,
});
// The function starts at file line 10.
const frame = {
  anchor: 10,
  source,
  calls: [call("a", 1), call("b", 2), call("Validate", 3)],
};

describe("stopSpeech refs and cues", () => {
  test("remembers where each ref is spoken", () => {
    const s = stopSpeech(
      { symbol: "read", description: "Calls [[Validate]] then returns." },
      0,
      1,
    );
    expect(s.text).toBe("Stop 1 of 1: read. Calls Validate then returns.");
    expect(s.refs).toEqual([
      { char: s.text.indexOf("Validate then"), name: "Validate" },
    ]);
    expect(s.cues).toEqual([]);
  });
  test("a cue is silent and sits where the narration reaches it", () => {
    const s = stopSpeech(
      {
        symbol: "read",
        description: "Set up here [[line:+1]]. Then [[at:return]] the result.",
      },
      0,
      1,
    );
    expect(s.text).toBe("Stop 1 of 1: read. Set up here. Then the result.");
    expect(s.cues).toEqual([
      // At the end of the word before the cue: "here" and "Then".
      {
        char: s.text.indexOf("here.") + 4,
        cue: { kind: "line", relative: true, start: 1, end: undefined },
      },
      {
        char: s.text.indexOf(" the result"),
        cue: { kind: "at", text: "return" },
      },
    ]);
    expect(s.refs).toEqual([]);
  });
  test("a cue with no words after it lands at the end", () => {
    const s = stopSpeech(
      { symbol: "read", description: "Look here [[line:12]]" },
      0,
      1,
    );
    expect(s.text).toBe("Stop 1 of 1: read. Look here");
    expect(s.cues[0].char).toBe(s.text.length);
  });
  test("a malformed cue is dropped silently", () => {
    const s = stopSpeech(
      { symbol: "read", description: "Odd [[line:x]] token." },
      0,
      1,
    );
    expect(s.text).toBe("Stop 1 of 1: read. Odd token.");
    expect(s.cues).toEqual([]);
  });
});

describe("buildTimeline", () => {
  test("starts on the anchor; a named call is a waypoint at its line", () => {
    const s = stopSpeech(
      { symbol: "read", description: "Calls [[Validate]] then returns." },
      0,
      1,
    );
    expect(buildTimeline(s, frame)).toEqual([
      { char: 0, line: 10 },
      { char: s.refs[0].char, line: 13 },
    ]);
  });
  test("no end waypoint: nothing drifts to the last line", () => {
    const s = stopSpeech(
      { symbol: "read", description: "See [[elsewhere]]." },
      0,
      1,
    );
    expect(buildTimeline(s, frame)).toEqual([{ char: 0, line: 10 }]);
  });
  test("cues become waypoints, merged with refs in text order", () => {
    const s = stopSpeech(
      {
        symbol: "read",
        description:
          "[[line:+1-2]] Two calls, then [[Validate]], then [[at:return]] out.",
      },
      0,
      1,
    );
    expect(buildTimeline(s, frame)).toEqual([
      { char: 0, line: 10 },
      { char: s.cues[0].char, line: 11, end: 12 },
      { char: s.refs[0].char, line: 13 },
      { char: s.cues[1].char, line: 14, end: undefined },
    ]);
  });
  test("a file stop resolves line cues without source", () => {
    const s = stopSpeech(
      {
        file: "x.go",
        line: 40,
        description: "Here [[line:+2]] and [[line:7]], not [[at:x]].",
      },
      0,
      1,
    );
    expect(buildTimeline(s, { anchor: 40 })).toEqual([
      { char: 0, line: 40 },
      { char: s.cues[0].char, line: 42, end: undefined },
      { char: s.cues[1].char, line: 7, end: undefined },
    ]);
  });
});

describe("lineAt", () => {
  const tl = [
    { char: 0, line: 10 },
    { char: 100, line: 13 },
    { char: 200, line: 15, end: 17 },
  ];
  test("holds the latest waypoint; no drift between them", () => {
    expect(lineAt(tl, 0)).toEqual({ char: 0, line: 10 });
    expect(lineAt(tl, 99)).toEqual({ char: 0, line: 10 });
    expect(lineAt(tl, 100)).toEqual({ char: 100, line: 13 });
    expect(lineAt(tl, 199)).toEqual({ char: 100, line: 13 });
    expect(lineAt(tl, 999)).toEqual({ char: 200, line: 15, end: 17 });
  });
  test("an empty timeline is nothing", () => {
    expect(lineAt([], 5)).toBeNull();
  });
});
