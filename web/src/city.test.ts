import { describe, expect, test } from "bun:test";
import {
  buildingAt,
  buildingHeight,
  buildingWidth,
  clickTarget,
  clientToWorld,
  fitViewBox,
  homeCell,
  layoutCity,
  markCity,
  placeDistricts,
  shortPackage,
  SLOT,
  SLOTS_PER_DISTRICT,
  spiralCell,
  tooltipText,
  truncateLabel,
  zoomViewBox,
  type CityData,
  type CityFunc,
  type CityPackage,
  type Cell,
} from "./city";

const MOD = "github.com/MaxInertia/unfold";

// The packages this repo actually has — the realistic load on the home grid.
const UNFOLD_PACKAGES = [
  "cmd/cli",
  "internal/diff",
  "internal/engine",
  "internal/gitbase",
  "internal/indexer",
  "internal/manifest",
  "internal/model",
  "internal/notes",
  "internal/platform",
  "internal/prefs",
  "internal/protoapi",
  "internal/rules",
  "internal/server",
  "internal/tours",
  "internal/tsengine",
  "internal/workspace",
].map((p) => `${MOD}/${p}`);

function fn(
  pkg: string,
  name: string,
  extra: Partial<CityFunc> = {},
): CityFunc {
  return {
    id: `${pkg}.${name}`,
    name,
    file: `/repo/${pkg.slice(MOD.length + 1)}/${name.toLowerCase()}.go`,
    lines: 10,
    callers: 1,
    callees: 1,
    ...extra,
  };
}

function pkg(path: string, names: string[]): CityPackage {
  return {
    path,
    name: path.slice(path.lastIndexOf("/") + 1),
    funcs: names.map((n) => fn(path, n)),
  };
}

function repo(packages: CityPackage[]): CityData {
  return { module: MOD, root: "/repo", packages };
}

function positions(data: CityData): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>();
  const layout = layoutCity(data);
  for (const d of layout.districts) out.set(d.path, { x: d.x, y: d.y });
  for (const b of layout.buildings) out.set(b.id, { x: b.x, y: b.y });
  return out;
}

function shuffled<T>(xs: T[], seed: number): T[] {
  const out = [...xs];
  let s = seed;
  for (let i = out.length - 1; i > 0; i--) {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    const j = s % (i + 1);
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

describe("spiral", () => {
  test("visits each ring's cells exactly once, clockwise from the top-left", () => {
    const home: Cell = { col: 5, row: 5 };
    expect(spiralCell(home, 0)).toEqual(home);
    const seen = new Set<string>();
    for (let k = 0; k < 1 + 8 + 16 + 24; k++) {
      const c = spiralCell(home, k);
      const key = `${c.col},${c.row}`;
      expect(seen.has(key)).toBe(false);
      seen.add(key);
      const r = Math.max(
        Math.abs(c.col - home.col),
        Math.abs(c.row - home.row),
      );
      const expectedRing = k === 0 ? 0 : k <= 8 ? 1 : k <= 24 ? 2 : 3;
      expect(r).toBe(expectedRing);
    }
    expect(spiralCell(home, 1)).toEqual({ col: 4, row: 4 });
    expect(spiralCell(home, 2)).toEqual({ col: 5, row: 4 });
    expect(spiralCell(home, 3)).toEqual({ col: 6, row: 4 });
    expect(spiralCell(home, 4)).toEqual({ col: 6, row: 5 });
    expect(spiralCell(home, 8)).toEqual({ col: 4, row: 5 });
  });
});

describe("placeDistricts", () => {
  test("is a function of the set, not the order", () => {
    const a = placeDistricts(UNFOLD_PACKAGES);
    const b = placeDistricts(shuffled(UNFOLD_PACKAGES, 7));
    expect([...a].sort()).toEqual([...b].sort());
  });

  test("gives every package its own cell", () => {
    const cells = placeDistricts(UNFOLD_PACKAGES);
    const keys = new Set([...cells.values()].map((c) => `${c.col},${c.row}`));
    expect(keys.size).toBe(UNFOLD_PACKAGES.length);
  });

  test("a package whose home cell is free lands there, and nothing else moves", () => {
    const before = placeDistricts(UNFOLD_PACKAGES);
    const taken = new Set([...before.values()].map((c) => `${c.col},${c.row}`));
    // Find a candidate name whose home cell is free in the current layout.
    let candidate = "";
    for (const name of [
      "city",
      "tsindexer",
      "events",
      "bridge",
      "graph",
      "search",
      "cache",
      "watch",
    ]) {
      const p = `${MOD}/internal/${name}`;
      const h = homeCell(p);
      if (!taken.has(`${h.col},${h.row}`)) {
        candidate = p;
        break;
      }
    }
    expect(candidate).not.toBe("");
    const after = placeDistricts([...UNFOLD_PACKAGES, candidate]);
    for (const p of UNFOLD_PACKAGES)
      expect(after.get(p)).toEqual(before.get(p)!);
    expect(after.get(candidate)).toEqual(homeCell(candidate));
  });

  test("a package that sorts last never moves anything, even when it collides", () => {
    // "zzz…" sorts after every real path. Over many names its home cell is
    // often taken; it must then spiral onward rather than displace.
    const before = placeDistricts(UNFOLD_PACKAGES);
    let collided = 0;
    for (let i = 0; i < 40; i++) {
      const p = `zzz/pkg${i}`;
      const h = homeCell(p);
      if (before.has(p)) continue;
      const taken = new Set(
        [...before.values()].map((c) => `${c.col},${c.row}`),
      );
      if (taken.has(`${h.col},${h.row}`)) collided++;
      const after = placeDistricts([...UNFOLD_PACKAGES, p]);
      for (const q of UNFOLD_PACKAGES)
        expect(after.get(q)).toEqual(before.get(q)!);
    }
    expect(collided).toBeGreaterThan(0);
  });

  test("removing a package leaves the ones that sort before it in place", () => {
    const sorted = [...UNFOLD_PACKAGES].sort();
    const before = placeDistricts(sorted);
    const victim = sorted[10];
    const after = placeDistricts(sorted.filter((p) => p !== victim));
    for (const p of sorted.slice(0, 10))
      expect(after.get(p)).toEqual(before.get(p)!);
  });

  test("a repo with more packages than home cells still places all of them", () => {
    const many = Array.from({ length: 300 }, (_, i) => `example.com/m/p${i}`);
    const cells = placeDistricts(many);
    expect(cells.size).toBe(300);
    const keys = new Set([...cells.values()].map((c) => `${c.col},${c.row}`));
    expect(keys.size).toBe(300);
  });
});

describe("layoutCity", () => {
  const base = repo([
    pkg(UNFOLD_PACKAGES[0], ["main", "run"]),
    pkg(UNFOLD_PACKAGES[12], [
      "New",
      "Server.Handler",
      "writeJSON",
      "writeError",
    ]),
    pkg(UNFOLD_PACKAGES[4], ["Indexer.Frame", "Indexer.Load", "New"]),
  ]);

  test("same input, same output; input order is irrelevant", () => {
    const a = layoutCity(base);
    const b = layoutCity({
      ...base,
      packages: shuffled(base.packages, 3).map((p) => ({
        ...p,
        funcs: shuffled(p.funcs, 5),
      })),
    });
    expect(a.districts.map((d) => [d.path, d.x, d.y])).toEqual(
      b.districts.map((d) => [d.path, d.x, d.y]),
    );
    expect(a.buildings.map((x) => [x.id, x.x, x.y, x.w, x.h])).toEqual(
      b.buildings.map((x) => [x.id, x.x, x.y, x.w, x.h]),
    );
  });

  test("adding a function moves nothing outside its district and nothing before it inside", () => {
    const before = positions(base);
    const server = base.packages[1];
    // "zz" sorts after every existing name in the district.
    const grown = repo(
      base.packages.map((p) =>
        p === server ? { ...p, funcs: [...p.funcs, fn(p.path, "zzLast")] } : p,
      ),
    );
    const after = positions(grown);
    for (const [id, pos] of before) expect(after.get(id)).toEqual(pos);
  });

  test("inserting a function in the middle shifts only later names in that district", () => {
    const before = positions(base);
    const server = base.packages[1];
    const grown = repo(
      base.packages.map((p) =>
        p === server
          ? { ...p, funcs: [...p.funcs, fn(p.path, "Server.Reload")] }
          : p,
      ),
    );
    const after = positions(grown);
    for (const [id, pos] of before) {
      const inServer = id.startsWith(server.path + ".");
      const laterName =
        inServer && id.slice(server.path.length + 1) > "Server.Reload";
      if (laterName) expect(after.get(id)).not.toEqual(pos);
      else expect(after.get(id)).toEqual(pos);
    }
  });

  test("adding a package whose home cell is free moves no existing district or building", () => {
    const before = positions(base);
    const layout = layoutCity(base);
    const taken = new Set(
      layout.districts.map((d) => `${d.cell.col},${d.cell.row}`),
    );
    // The guarantee is for a home cell nobody holds; a name that hashes onto
    // an existing district's cell and sorts before it is the documented
    // caveat, so the test picks a name the guarantee covers.
    const candidate = ["city", "map", "grid", "atlas", "plan", "tile"]
      .map((n) => `${MOD}/internal/${n}`)
      .find((p) => !taken.has(`${homeCell(p).col},${homeCell(p).row}`))!;
    expect(candidate).toBeDefined();
    const grown = repo([...base.packages, pkg(candidate, ["layoutCity"])]);
    const after = positions(grown);
    for (const [id, pos] of before) expect(after.get(id)).toEqual(pos);
  });

  test("height follows lines (log), width follows fan-in (capped)", () => {
    expect(buildingHeight(1)).toBeLessThan(buildingHeight(10));
    expect(buildingHeight(10)).toBeLessThan(buildingHeight(100));
    expect(buildingHeight(100) - buildingHeight(10)).toBeLessThan(
      buildingHeight(10) - buildingHeight(1) + 1,
    );
    expect(buildingHeight(5000)).toBe(buildingHeight(400));
    expect(buildingWidth(0)).toBeLessThan(buildingWidth(4));
    expect(buildingWidth(8)).toBe(buildingWidth(800));
    expect(buildingHeight(1)).toBeLessThan(SLOT);
    expect(buildingWidth(999)).toBeLessThan(SLOT);
  });

  test("labels drop the module prefix", () => {
    expect(shortPackage(`${MOD}/internal/server`, MOD)).toBe("internal/server");
    expect(shortPackage(MOD, MOD)).toBe("unfold");
    expect(shortPackage("other.org/x", MOD)).toBe("other.org/x");
    expect(truncateLabel("internal/server", 20)).toBe("internal/server");
    expect(truncateLabel("internal/very/deep/server", 13)).toBe(
      "…/deep/server",
    );
  });
});

describe("size budget", () => {
  test("a 2,000-function repo draws at most SLOTS_PER_DISTRICT shapes per district", () => {
    const packages: CityPackage[] = [];
    let n = 0;
    for (let p = 0; p < 25; p++) {
      const count = p < 5 ? 200 : 50; // 5 big packages, 20 ordinary ones
      const names = Array.from(
        { length: count },
        (_, i) => `F${String(i).padStart(3, "0")}`,
      );
      const path = `example.com/big/p${p}`;
      const funcs = names.map((nm, i) => ({
        ...fn(path, nm),
        file: `/repo/p${p}/f${Math.floor(i / 7)}.go`, // ~7 functions per file
      }));
      packages.push({ path, name: `p${p}`, funcs });
      n += count;
    }
    expect(n).toBe(2000);
    const layout = layoutCity({
      module: "example.com/big",
      root: "/repo",
      packages,
    });
    expect(layout.buildings.length).toBeLessThanOrEqual(
      25 * SLOTS_PER_DISTRICT,
    );
    for (const d of layout.districts) {
      expect(d.buildings.length).toBeLessThanOrEqual(SLOTS_PER_DISTRICT);
      expect(d.mode).toBe(d.count > SLOTS_PER_DISTRICT ? "files" : "funcs");
    }
    // Every function is still findable through its shape.
    for (const p of packages)
      for (const f of p.funcs) expect(layout.byId.get(f.id)).toBeDefined();
    const big = layout.districts.find((d) => d.count === 200)!;
    expect(big.buildings.every((b) => b.kind === "file")).toBe(true);
    expect(big.buildings.length).toBe(Math.ceil(200 / 7));
  });

  test("a package with more files than slots folds the tail into one block", () => {
    const path = "example.com/m/huge";
    const funcs = Array.from({ length: 100 }, (_, i) => ({
      ...fn(path, `F${String(i).padStart(3, "0")}`),
      file: `/repo/huge/f${i}.go`, // one function per file: 100 files
    }));
    const layout = layoutCity({
      module: "example.com/m",
      root: "/repo",
      packages: [{ path, name: "huge", funcs }],
    });
    const d = layout.districts[0];
    expect(d.buildings.length).toBe(SLOTS_PER_DISTRICT);
    const tail = d.buildings[SLOTS_PER_DISTRICT - 1];
    expect(tail.name).toBe(`+${100 - (SLOTS_PER_DISTRICT - 1)} files`);
    expect(tail.ids.length).toBe(100 - (SLOTS_PER_DISTRICT - 1));
    expect(layout.byId.get(funcs[99].id)).toBe(tail);
  });

  test("at the threshold a district still draws functions", () => {
    const path = "example.com/m/edge";
    const funcs = Array.from({ length: SLOTS_PER_DISTRICT }, (_, i) =>
      fn(path, `F${String(i).padStart(3, "0")}`),
    );
    const layout = layoutCity({
      module: "example.com/m",
      root: "/repo",
      packages: [{ path, name: "edge", funcs }],
    });
    expect(layout.districts[0].mode).toBe("funcs");
    expect(layout.districts[0].buildings.length).toBe(SLOTS_PER_DISTRICT);
  });
});

describe("marks", () => {
  const data = repo([
    pkg(UNFOLD_PACKAGES[12], ["New", "Server.Handler", "writeJSON"]),
    pkg(UNFOLD_PACKAGES[4], ["Indexer.Frame", "Indexer.Load"]),
  ]);
  const layout = layoutCity(data);
  const id = (p: number, name: string) => `${UNFOLD_PACKAGES[p]}.${name}`;

  test("root, lit callees, links and tour stops resolve to shapes", () => {
    const m = markCity(layout, {
      root: id(12, "Server.Handler"),
      unfolded: [
        id(12, "writeJSON"),
        id(4, "Indexer.Frame"),
        id(12, "Server.Handler"),
        "nope",
      ],
      tour: new Map([
        [id(4, "Indexer.Load"), [1]],
        [id(12, "New"), [2, 3]],
      ]),
    });
    expect(m.root?.id).toBe(id(12, "Server.Handler"));
    // The root isn't lit as its own callee, and unknown ids are dropped.
    expect(m.lit.map((b) => b.id)).toEqual([
      id(12, "writeJSON"),
      id(4, "Indexer.Frame"),
    ]);
    expect(m.links.map((l) => [l.from.id, l.to.id])).toEqual([
      [id(12, "Server.Handler"), id(12, "writeJSON")],
      [id(12, "Server.Handler"), id(4, "Indexer.Frame")],
    ]);
    expect(m.stops.map((s) => [s.building.id, s.numbers])).toEqual([
      [id(4, "Indexer.Load"), [1]],
      [id(12, "New"), [2, 3]],
    ]);
  });

  test("no root means no links, but callees still light", () => {
    const m = markCity(layout, { unfolded: [id(12, "writeJSON")] });
    expect(m.root).toBeNull();
    expect(m.lit.length).toBe(1);
    expect(m.links).toEqual([]);
  });
});

describe("hover and click", () => {
  const data = repo([
    pkg(UNFOLD_PACKAGES[12], ["New", "Server.Handler", "writeJSON"]),
  ]);
  const layout = layoutCity(data);

  test("hit-testing finds the shape by its slot, and nothing between districts", () => {
    const b = layout.buildings[1];
    expect(buildingAt(layout, b.slot.x + 1, b.slot.y + 1)).toBe(b);
    // A short building's slot is a target even above the drawn rectangle.
    expect(buildingAt(layout, b.slot.x + SLOT / 2, b.slot.y + 0.5)).toBe(b);
    expect(buildingAt(layout, -1000, -1000)).toBeNull();
    // Inside the district but on an empty slot.
    const d = layout.districts[0];
    expect(buildingAt(layout, d.x + d.w - 1, d.y + d.h - 1)).toBeNull();
  });

  test("tooltip and click target", () => {
    const b = layout.byId.get(`${UNFOLD_PACKAGES[12]}.writeJSON`)!;
    expect(tooltipText({ ...b, lines: 1, callers: 1 })).toBe(
      "writeJSON · 1 line · 1 caller",
    );
    expect(tooltipText({ ...b, lines: 12, callers: 0 })).toBe(
      "writeJSON · 12 lines · 0 callers",
    );
    expect(clickTarget(b)).toBe(b.id);
    const block = {
      ...b,
      kind: "file" as const,
      name: "server.go",
      ids: ["a", "b", "c"],
      lines: 30,
    };
    expect(tooltipText(block)).toBe("server.go · 3 functions · 30 lines");
    expect(clickTarget(block)).toBe("a");
  });
});

describe("camera", () => {
  const bounds = { x: 100, y: 200, w: 300, h: 100 };

  test("fit covers the bounds in the viewport's aspect", () => {
    const vb = fitViewBox(bounds, 2, 10);
    expect(vb.w / vb.h).toBeCloseTo(2);
    expect(vb.x).toBeLessThanOrEqual(bounds.x - 10);
    expect(vb.y).toBeLessThanOrEqual(bounds.y - 10);
    expect(vb.x + vb.w).toBeGreaterThanOrEqual(bounds.x + bounds.w + 10);
    expect(vb.y + vb.h).toBeGreaterThanOrEqual(bounds.y + bounds.h + 10);
  });

  test("zoom keeps the point under the cursor fixed", () => {
    const vb = { x: 0, y: 0, w: 400, h: 200 };
    const z = zoomViewBox(vb, 2, 100, 50, bounds);
    expect(z.w).toBeCloseTo(200);
    // (100,50) was at 25%/25% of the box; still is.
    expect((100 - z.x) / z.w).toBeCloseTo(0.25);
    expect((50 - z.y) / z.h).toBeCloseTo(0.25);
    // Zooming far in stops at the minimum width rather than inverting.
    let tiny = vb;
    for (let i = 0; i < 30; i++) tiny = zoomViewBox(tiny, 2, 100, 50, bounds);
    expect(tiny.w).toBeGreaterThan(0);
  });

  test("client → world inverts the meet mapping", () => {
    const vb = { x: 0, y: 0, w: 400, h: 200 };
    const rect = { left: 10, top: 20, width: 800, height: 800 }; // taller than the box: letterboxed
    // The box fills 800×400 centred vertically: scale 2, y offset 200.
    expect(clientToWorld(vb, rect, 10, 220)).toEqual({ x: 0, y: 0 });
    expect(clientToWorld(vb, rect, 810, 620)).toEqual({ x: 400, y: 200 });
  });
});
