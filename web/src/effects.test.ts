import { describe, expect, test } from "bun:test";
import {
  accessOf,
  evidenceLabel,
  hopLabel,
  shortLoc,
  summarize,
} from "./effects";
import type { Effect } from "./types";

function eff(over: Partial<Effect>): Effect {
  return {
    kind: "file",
    callee: "os.Open",
    file: "/repo/internal/tours/store.go",
    line: 10,
    site: "pkg.f",
    depth: 0,
    ...over,
  };
}

describe("summarize", () => {
  test("one badge per kind, in a fixed order, with access folded in", () => {
    const badges = summarize([
      eff({ kind: "time", callee: "time.Sleep" }),
      eff({ kind: "file", access: "read" }),
      eff({ kind: "file", access: "write", callee: "os.WriteFile", line: 12 }),
      eff({ kind: "db", access: "read", callee: "sql.DB.Query" }),
      eff({ kind: "custom", callee: "cache.Put" }),
    ]);
    expect(badges.map((b) => b.label)).toEqual([
      "db·r",
      "file·rw",
      "sleep",
      "custom",
    ]);
    expect(badges[1].effects).toHaveLength(2);
  });

  test("counts direct and inherited evidence separately", () => {
    const [b] = summarize([
      eff({ kind: "env", access: "read" }),
      eff({
        kind: "env",
        access: "read",
        depth: 1,
        via: "pkg.g",
        viaTitle: "g",
      }),
      eff({
        kind: "env",
        access: "read",
        depth: 2,
        via: "pkg.g",
        viaTitle: "g",
      }),
    ]);
    expect(b.direct).toBe(1);
    expect(b.inherited).toBe(2);
    expect(b.title).toBe("environment (reads) — 1 direct, 2 inherited");
  });

  test("an inherited-only kind has no direct evidence", () => {
    const [b] = summarize([eff({ kind: "net", depth: 1, via: "pkg.g" })]);
    expect(b.direct).toBe(0);
    expect(b.label).toBe("net");
  });

  test("nothing in, nothing out", () => {
    expect(summarize([])).toEqual([]);
  });
});

describe("accessOf", () => {
  test("unions the directions and ignores silence", () => {
    expect(accessOf([eff({ access: "read" })])).toBe("r");
    expect(accessOf([eff({ access: "write" })])).toBe("w");
    expect(accessOf([eff({ access: "read" }), eff({ access: "write" })])).toBe(
      "rw",
    );
    expect(accessOf([eff({}), eff({ access: "write" })])).toBe("w");
    expect(accessOf([eff({})])).toBe("");
  });
});

describe("labels", () => {
  test("evidence shows the key when there is one", () => {
    expect(evidenceLabel(eff({ callee: "os.Getenv", key: "PORT" }))).toBe(
      "os.Getenv PORT",
    );
    expect(evidenceLabel(eff({ callee: "os.Getenv" }))).toBe("os.Getenv");
  });

  test("hop names the callee and the extra distance", () => {
    expect(hopLabel(eff({ depth: 0 }))).toBe("direct");
    expect(hopLabel(eff({ depth: 1, via: "pkg.load", viaTitle: "load" }))).toBe(
      "via load",
    );
    expect(hopLabel(eff({ depth: 3, via: "pkg.load" }))).toBe(
      "via pkg.load +2",
    );
  });

  test("location keeps the last two path segments", () => {
    expect(shortLoc("/repo/internal/tours/store.go", 7)).toBe(
      "tours/store.go:7",
    );
    expect(shortLoc("store.go", 7)).toBe("store.go:7");
  });
});
