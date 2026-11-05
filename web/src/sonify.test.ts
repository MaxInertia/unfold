import { describe, expect, test } from "bun:test";
import {
  PITCHES,
  SCAN_MAX_MS,
  classifyLines,
  cursorScore,
  foldScore,
  indentOf,
  linesForFrame,
  pitchFor,
  scoreDuration,
  scoreForFrame,
  scoreForTree,
  stepFor,
  unfoldScore,
  voicingFor,
  type LineInfo,
} from "./sonify";
import type { Frame } from "./types";

const lines = (src: string, calls: number[] = [], changed: number[] = []): LineInfo[] =>
  src.split("\n").map((text, i) => ({
    text,
    call: calls.includes(i) || undefined,
    changed: changed.includes(i) || undefined,
  }));

const FN = [
  "func (s *Server) handle(w http.ResponseWriter, r *http.Request) error {",
  "\tbody, err := read(r)",
  "\tif err != nil {",
  '\t\treturn fmt.Errorf("read: %w", err)',
  "\t}",
  "",
  "\tfor _, item := range body.Items {",
  "\t\tif item.Skip {",
  "\t\t\tcontinue",
  "\t\t}",
  "\t\ts.process(item)",
  "\t}",
  "\treturn nil",
  "}",
].join("\n");

describe("indentOf", () => {
  test("counts tabs, and four spaces as a level", () => {
    expect(indentOf("x")).toBe(0);
    expect(indentOf("\t\tx")).toBe(2);
    expect(indentOf("        x")).toBe(2);
    expect(indentOf("\t  x")).toBe(1);
  });
});

describe("pitchFor", () => {
  test("deeper indentation is lower, within the pentatonic set", () => {
    const p0 = pitchFor(0);
    const p1 = pitchFor(1);
    const p3 = pitchFor(3);
    expect(p0).toBe(PITCHES[PITCHES.length - 1]);
    expect(p1).toBeLessThan(p0);
    expect(p3).toBeLessThan(p1);
    for (const p of [p0, p1, p3]) expect(PITCHES).toContain(p);
  });

  test("each level of unfolding drops two steps and clamps at the bottom", () => {
    expect(pitchFor(0, 1)).toBe(PITCHES[PITCHES.length - 3]);
    expect(pitchFor(0, 1)).toBeLessThan(pitchFor(0, 0));
    expect(pitchFor(40, 40)).toBe(PITCHES[0]);
  });

  test("the whole set spans about two octaves", () => {
    const lo = PITCHES[0];
    const hi = PITCHES[PITCHES.length - 1];
    expect(hi / lo).toBeGreaterThan(4);
    expect(hi / lo).toBeLessThan(6);
  });
});

describe("classifyLines", () => {
  test("names each kind, with the error block running to its closing brace", () => {
    expect(classifyLines(lines(FN, [1, 3, 10]))).toEqual([
      "plain", // func
      "call", // read(r)
      "error", // if err != nil {
      "error", // return fmt.Errorf (inside the block: error wins over return)
      "rest", // }
      "rest", // blank
      "plain", // for
      "plain", // if item.Skip
      "plain", // continue
      "rest", // }
      "call", // s.process
      "rest", // }
      "return", // return nil
      "rest", // }
    ]);
  });

  test("a one-line err check without a block is a single error line", () => {
    expect(classifyLines(lines("if err != nil { return err }\nx := 1"))).toEqual([
      "error",
      "plain",
    ]);
  });

  test("an err check in an else-if opens a block too", () => {
    expect(classifyLines(lines("} else if err != nil {\n\tlog(err)\n}"))).toEqual([
      "error",
      "error",
      "rest",
    ]);
  });
});

describe("scoreForFrame", () => {
  test("one grain per sounding line, in order, pitched by indentation", () => {
    const score = scoreForFrame(lines(FN, [1, 3, 10]));
    // 14 lines, 5 rests.
    expect(score).toHaveLength(9);
    expect(score.map((n) => n.line)).toEqual([0, 1, 2, 3, 6, 7, 8, 10, 12]);
    for (let i = 1; i < score.length; i++) expect(score[i].t).toBeGreaterThan(score[i - 1].t);
    const by = new Map(score.map((n) => [n.line, n]));
    expect(by.get(0)!.pitch).toBe(pitchFor(0));
    expect(by.get(1)!.pitch).toBe(pitchFor(1));
    expect(by.get(8)!.pitch).toBe(pitchFor(3));
    expect(by.get(8)!.pitch).toBeLessThan(by.get(1)!.pitch);
  });

  test("timbre follows the kind of line", () => {
    const score = scoreForFrame(lines(FN, [1, 3, 10]));
    const by = new Map(score.map((n) => [n.line, n]));
    expect(by.get(0)!.timbre).toBe("plain");
    expect(by.get(1)!.timbre).toBe("call");
    expect(by.get(2)!.timbre).toBe("error");
    expect(by.get(3)!.timbre).toBe("error");
    expect(by.get(10)!.timbre).toBe("call");
    expect(by.get(12)!.timbre).toBe("return");
    // A return rings longer than a plain grain.
    expect(by.get(12)!.dur).toBeGreaterThan(by.get(0)!.dur);
  });

  test("diff-changed lines are marked bright, and bright opens the filter", () => {
    const score = scoreForFrame(lines(FN, [1], [1, 6]));
    const by = new Map(score.map((n) => [n.line, n]));
    expect(by.get(1)!.bright).toBe(true);
    expect(by.get(6)!.bright).toBe(true);
    expect(by.get(0)!.bright).toBeUndefined();
    expect(voicingFor(by.get(6)!).cutoff).toBeGreaterThan(voicingFor(by.get(0)!).cutoff);
    expect(voicingFor(by.get(6)!).wave).not.toBe("sine");
  });

  test("pitch is relative to the frame's shallowest line", () => {
    const nested = scoreForFrame(lines("\t\tfunc() {\n\t\t\tx()\n\t\t}"));
    expect(nested[0].pitch).toBe(pitchFor(0));
    expect(nested[1].pitch).toBe(pitchFor(1));
  });

  test("a deeper frame plays lower", () => {
    const top = scoreForFrame(lines(FN))[0];
    const deep = scoreForFrame(lines(FN), { depth: 2 })[0];
    expect(deep.pitch).toBeLessThan(top.pitch);
  });

  test("forty lines take about two seconds; nothing takes more than the cap", () => {
    const forty = lines(Array.from({ length: 40 }, (_, i) => `\tx${i} := ${i}`).join("\n"));
    const d = scoreDuration(scoreForFrame(forty));
    expect(d).toBeGreaterThan(1800);
    expect(d).toBeLessThan(2300);
    const huge = lines(Array.from({ length: 1200 }, (_, i) => `\tx${i} := ${i}`).join("\n"));
    expect(scoreDuration(scoreForFrame(huge))).toBeLessThanOrEqual(SCAN_MAX_MS + 200);
    // Short functions don't race.
    expect(stepFor(5)).toBe(60);
  });
});

describe("scoreForTree", () => {
  test("a callee's section plays after its call line, two steps lower, then the caller resumes", () => {
    const caller = lines("func a() {\n\tb()\n\treturn\n}", [1]);
    const callee = lines("func b() {\n\tx := 1\n}");
    const score = scoreForTree({
      lines: caller,
      children: [{ line: 1, node: { lines: callee, children: [] } }],
    });
    const step = stepFor(7);
    expect(score.map((n) => [n.line, n.timbre])).toEqual([
      [0, "plain"],
      [1, "call"],
      [0, "plain"],
      [1, "plain"],
      [2, "return"],
    ]);
    // Callee notes are lower than the caller's at the same indentation.
    expect(score[2].pitch).toBe(pitchFor(0, 1));
    expect(score[2].pitch).toBeLessThan(score[0].pitch);
    expect(score[3].pitch).toBe(pitchFor(1, 1));
    // Time keeps moving: two caller lines, a rest, the callee's three lines,
    // a rest, then the caller again.
    expect(score[2].t).toBe(3 * step);
    expect(score[4].t).toBe(7 * step);
    for (let i = 1; i < score.length; i++) {
      expect(score[i].t - score[i - 1].t).toBeGreaterThanOrEqual(step);
    }
  });
});

describe("linesForFrame", () => {
  test("marks call lines from spans and changed lines from the diff", () => {
    const source = "func a() {\n\tb()\n\tc()\n}";
    const frame = {
      id: "a",
      file: "a.go",
      language: "go",
      startLine: 1,
      endLine: 4,
      source,
      calls: [
        { id: "1", spanStart: source.indexOf("b()"), spanEnd: 0, displayName: "b", kind: "direct" },
      ],
      diff: { status: "modified", addedLines: [2] },
    } as unknown as Frame;
    expect(linesForFrame(frame).map((l) => [!!l.call, !!l.changed])).toEqual([
      [false, false],
      [true, false],
      [false, true],
      [false, false],
    ]);
  });
});

describe("cues", () => {
  test("unfold and fold share a pitch per depth, deeper lower, fold softer", () => {
    expect(unfoldScore(1)[0].pitch).toBe(foldScore(1)[0].pitch);
    expect(unfoldScore(2)[0].pitch).toBeLessThan(unfoldScore(1)[0].pitch);
    expect(voicingFor(foldScore(1)[0]).peak).toBeLessThan(voicingFor(unfoldScore(1)[0]).peak);
  });

  test("the cursor grain is pitched by the line's indentation", () => {
    expect(cursorScore("\t\tx")[0].pitch).toBeLessThan(cursorScore("\tx")[0].pitch);
    expect(cursorScore("\tx")[0].timbre).toBe("cursor");
  });
});
