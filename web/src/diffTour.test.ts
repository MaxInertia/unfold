import { describe, expect, test } from "bun:test";
import { asTour, blastFor, entryLabel, pathExpand, signature } from "./diffTour";
import { mergeTours } from "./tours";
import type { DiffFunction, DiffTour, Tour } from "./types";

// The pure half of the diff tour: how the server's answer becomes a tour in
// the list, how a route becomes something the view can open, and how a frame
// finds its own blast radius.

function fn(id: string, entrypoints: DiffFunction["entrypoints"] = []): DiffFunction {
  return {
    id,
    title: id,
    file: "a.go",
    line: 1,
    status: "modified",
    added: 2,
    removed: 1,
    symbol: id,
    entrypoints,
  };
}

const names = { "p.Main": "p.Main", "p.A": "p.A", "p.B": "p.B" };

const data: DiffTour = {
  base: "main",
  commit: "abc123",
  entrypointsKnown: true,
  functions: [
    fn("p.B", [
      {
        id: "p.Main",
        title: "main",
        kind: "main",
        path: ["p.Main", "p.A", "p.B"],
      },
    ]),
    fn("p.X"),
  ],
  unreached: 1,
  deleted: 0,
  names,
  tour: {
    name: "diff:main",
    title: "Changes vs main",
    stops: [{ symbol: "p.B" }, { symbol: "p.X" }],
  },
};

describe("pathExpand", () => {
  test("names the hops after the entrypoint, nested", () => {
    expect(pathExpand(["p.Main", "p.A", "p.B"], names)).toBe("p.A > p.B");
  });
  test("is empty when the entrypoint is the function", () => {
    expect(pathExpand(["p.Main"], names)).toBe("");
  });
  test("falls back to the id for an unnamed hop", () => {
    expect(pathExpand(["p.Main", "q.Z"], names)).toBe("q.Z");
  });
});

describe("blastFor", () => {
  test("finds the frame's change", () => {
    expect(blastFor(data, "p.B")?.entrypoints.length).toBe(1);
    expect(blastFor(data, "p.X")?.entrypoints.length).toBe(0);
  });
  test("is null for an unchanged frame or without data", () => {
    expect(blastFor(data, "p.A")).toBeNull();
    expect(blastFor(null, "p.B")).toBeNull();
  });
});

describe("asTour", () => {
  test("marks the tour generated and stamps it with the changed set", () => {
    const t = asTour(data);
    expect(t.generated).toBe(true);
    expect(t.name).toBe("diff:main");
    expect(t.updatedAt).toBe(signature(data));
  });
  test("the stamp moves when a change does", () => {
    const grown = { ...data, functions: [...data.functions, fn("p.New")] };
    expect(signature(grown)).not.toBe(signature(data));
    const edited = {
      ...data,
      functions: [{ ...data.functions[0], added: 3 }, data.functions[1]],
    };
    expect(signature(edited)).not.toBe(signature(data));
  });
});

describe("mergeTours", () => {
  const disk: Tour[] = [{ name: "retry", stops: [] }];
  test("leads with the generated tour", () => {
    expect(mergeTours(disk, asTour(data)).map((t) => t.name)).toEqual(["diff:main", "retry"]);
  });
  test("is the disk list when there is none", () => {
    expect(mergeTours(disk, null)).toBe(disk);
  });
  test("never shadows a tour someone wrote", () => {
    const clash: Tour[] = [{ name: "diff:main", stops: [] }];
    expect(mergeTours(clash, asTour(data))).toBe(clash);
  });
});

describe("entryLabel", () => {
  test("kind plus key for a bound entrypoint, kind alone otherwise", () => {
    expect(entryLabel({ kind: "http.route", key: "/api/health" })).toBe("http.route /api/health");
    expect(entryLabel({ kind: "main" })).toBe("main");
    expect(entryLabel({ kind: "init", key: "cmd" })).toBe("init");
  });
});
