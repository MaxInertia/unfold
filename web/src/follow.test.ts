import { describe, expect, test } from "bun:test";
import type { Element } from "hast";
import {
  computeLineStarts,
  markLine,
  marksForFrame,
  mergeRanges,
  offsetToLineCol,
  spanOffset,
  unionSpans,
} from "./follow";
import type { FlowSpan } from "./types";

// The pure half of following a value: turning the server's file-space spans
// into marks on a frame's lines, and wrapping those marks around the right
// characters of a highlighted line.

const frame = { file: "/p/a.go", startLine: 10, endLine: 20 };

function span(
  line: number,
  col: number,
  len: number,
  file = frame.file,
): FlowSpan {
  return { file, line, col, len };
}

describe("marksForFrame", () => {
  test("maps file lines onto frame line indexes and drops spans outside the window", () => {
    const m = marksForFrame(frame, [
      span(10, 6, 3), // first line of the frame
      span(15, 2, 4),
      span(21, 1, 2), // past the end
      span(12, 1, 2, "/p/other.go"), // another file
    ]);
    expect([...m.keys()].sort()).toEqual([0, 5]);
    expect(m.get(0)).toEqual([{ start: 5, end: 8 }]);
    expect(m.get(5)).toEqual([{ start: 1, end: 5 }]);
  });

  test("flags the range under the click as the origin", () => {
    const m = marksForFrame(frame, [span(12, 3, 3), span(12, 10, 3)], {
      lineIdx: 2,
      col: 11,
    });
    expect(m.get(2)).toEqual([
      { start: 2, end: 5 },
      { start: 9, end: 12, origin: true },
    ]);
  });

  test("the caret just after a name still counts as on it", () => {
    const m = marksForFrame(frame, [span(12, 3, 3)], { lineIdx: 2, col: 5 });
    expect(m.get(2)?.[0].origin).toBe(true);
  });
});

describe("mergeRanges", () => {
  test("sorts, merges overlaps, keeps adjacent ranges apart", () => {
    expect(
      mergeRanges([
        { start: 10, end: 14 },
        { start: 0, end: 3 },
        { start: 12, end: 20, origin: true },
        { start: 3, end: 5 },
      ]),
    ).toEqual([
      { start: 0, end: 3 },
      { start: 3, end: 5 },
      { start: 10, end: 20, origin: true },
    ]);
  });

  test("does not mutate its input", () => {
    const input = [
      { start: 5, end: 9 },
      { start: 1, end: 7 },
    ];
    mergeRanges(input);
    expect(input).toEqual([
      { start: 5, end: 9 },
      { start: 1, end: 7 },
    ]);
  });
});

describe("offsets", () => {
  const source = "func f() {\n\tx := 1\n\treturn x\n}";
  const starts = computeLineStarts(source);

  test("line starts", () => {
    expect(starts).toEqual([0, 11, 19, 29]);
  });

  test("spanOffset places a file span in the frame's source", () => {
    expect(spanOffset(frame, starts, span(11, 2, 1))).toBe(12);
    expect(source[12]).toBe("x");
    expect(spanOffset(frame, starts, span(9, 1, 1))).toBeNull();
    expect(spanOffset(frame, starts, span(11, 2, 1, "/p/b.go"))).toBeNull();
  });

  test("offsetToLineCol inverts it", () => {
    expect(offsetToLineCol(starts, 12)).toEqual({ lineIdx: 1, col: 1 });
    expect(offsetToLineCol(starts, 0)).toEqual({ lineIdx: 0, col: 0 });
    expect(offsetToLineCol(starts, 29)).toEqual({ lineIdx: 3, col: 0 });
  });

  test("unionSpans dedupes by position", () => {
    const u = unionSpans([span(1, 1, 1)], [span(1, 1, 1), span(2, 1, 1)]);
    expect(u).toHaveLength(2);
  });
});

// A shiki-shaped line: a span of token spans holding text, with a call-site
// span nested where the highlighter decorated one.
function text(value: string) {
  return { type: "text" as const, value };
}
function el(
  tagName: string,
  children: Element["children"],
  properties: Element["properties"] = {},
): Element {
  return { type: "element", tagName, properties, children };
}
function flatten(node: Element["children"][number]): string {
  if (node.type === "text") return node.value;
  if (node.type !== "element") return "";
  const inner = node.children.map(flatten).join("");
  if (node.tagName === "mark") {
    const cls = (node.properties?.className as string[]) ?? [];
    return cls.includes("follow-mark--origin") ? `[[${inner}]]` : `[${inner}]`;
  }
  return inner;
}

describe("markLine", () => {
  const line = el(
    "span",
    [
      el("span", [text("\tmsg")]),
      el("span", [text(" := ")]),
      el("span", [text("greet")], { dataCallId: "f@0" }),
      el("span", [text("(name, msg)")]),
    ],
    { className: ["line"] },
  );

  test("wraps the covered characters, splitting text nodes at the boundaries", () => {
    // "\tmsg := greet(name, msg)" — mark both `msg`.
    const out = markLine(line, [
      { start: 1, end: 4, origin: true },
      { start: 20, end: 23 },
    ]);
    expect(out.children.map(flatten).join("")).toBe(
      "\t[[msg]] := greet(name, [msg])",
    );
    // The line element itself and the call-site span are kept as they were.
    expect(out.properties).toEqual({ className: ["line"] });
    const call = out.children[2] as Element;
    expect(call.properties).toEqual({ dataCallId: "f@0" });
  });

  test("a range spanning two tokens marks the piece in each", () => {
    // Mark "g := g" across the first three tokens.
    const out = markLine(line, [{ start: 3, end: 10 }]);
    expect(out.children.map(flatten).join("")).toBe(
      "\tms[g][ := ][gr]eet(name, msg)",
    );
  });

  test("no ranges returns the same line untouched", () => {
    expect(markLine(line, [])).toBe(line);
  });

  test("marks never wrap elements, so a call-site span stays a direct child", () => {
    const out = markLine(line, [{ start: 8, end: 13 }]);
    expect(out.children.map(flatten).join("")).toBe(
      "\tmsg := [greet](name, msg)",
    );
    const call = out.children[2] as Element;
    expect(call.tagName).toBe("span");
    expect((call.children[0] as Element).tagName).toBe("mark");
  });
});
