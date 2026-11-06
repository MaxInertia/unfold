// The city: a spatial map of one repository that stays put.
//
// Every package is a district and every function a building. The point of
// the map is spatial memory — after a week in a codebase you know that the
// server is up and to the left of the indexer the way you know where the
// kitchen is — and spatial memory only forms if things don't move. So the
// layout here is not "good" in the force-directed sense; it is *stable*: a
// position depends on the thing's own identity, not on how many neighbours
// it happens to have this commit. Adding a function doesn't move the others.
// A new package lands in a cell nobody was using.
//
// Pure: no DOM, no React, no network. The component draws what this returns.
//
// ---- The layout rule, and what it guarantees ----
//
// Districts. Each package path is hashed (FNV-1a, 32-bit) to a *home cell* on
// a fixed HOME_SIDE × HOME_SIDE grid. Packages are then placed in sorted path
// order: each takes its home cell if free, else the first free cell on a
// spiral walking outward from it — ring 1 clockwise from the top-left corner,
// then ring 2, and so on over an unbounded plane. So a package's cell is a
// function of its own path and of the cells already taken by the packages
// that sort before it. That gives:
//
//   - Same input, same output. Input order is irrelevant (it's sorted), and
//     nothing is random.
//   - Adding a package never moves a package that sorts before it.
//   - If the new package's home cell was free, nothing moves at all: a probe
//     chain only ever ends at the first free cell, so no existing chain had
//     passed through that one. The same holds for whichever free cell it ends
//     on when its home is taken.
//   - The only way an existing package moves is if a new package that sorts
//     before it ends on a cell the existing one's probe chain had crossed. At
//     the load a repository puts on a 64-cell home grid that is rare, and it
//     is the one caveat.
//   - The home grid never resizes, so there is no threshold at which the
//     whole map is laid out again. A repository with more packages than home
//     cells spirals outward; it gets denser, not reshuffled.
//
// Buildings. Within a district the functions are sorted by name (then id)
// and fill a SLOT_COLS × SLOT_ROWS grid row by row. Adding a function moves
// only the functions in the same district that sort after it, by one slot —
// never anything in another district. Beyond SLOT_COLS × SLOT_ROWS functions
// a district switches to one block per file (this is the size budget: no
// district draws more than that many shapes, so a 2,000-function repo is a
// couple of thousand rectangles at most, and a 200-function package is
// never 200). That switch is the one discontinuity within a district, and it
// happens at a documented count.
//
// Shapes. A building's height is its line count, log-scaled, and its width
// its fan-in — how many functions call it — capped, so a hub is wide and a
// long function is tall. A file block is always full width and carries the
// file's total lines in its height.

export interface CityFunc {
  id: string;
  name: string;
  file: string;
  lines: number;
  callers: number;
  callees: number;
  changed?: boolean;
}

export interface CityPackage {
  path: string;
  name: string;
  funcs: CityFunc[];
}

export interface CityData {
  module: string;
  root: string;
  packages: CityPackage[];
}

// ---- geometry constants (world units) ----

export const HOME_SIDE = 8;
export const SLOT_COLS = 8;
export const SLOT_ROWS = 8;
export const SLOTS_PER_DISTRICT = SLOT_COLS * SLOT_ROWS;
export const SLOT = 10;
export const DISTRICT_PAD = 5;
export const LABEL_H = 9;
export const DISTRICT_W = SLOT_COLS * SLOT + 2 * DISTRICT_PAD;
export const DISTRICT_H = LABEL_H + SLOT_ROWS * SLOT + 2 * DISTRICT_PAD;
export const CELL_W = DISTRICT_W + 14;
export const CELL_H = DISTRICT_H + 14;
// Fan-in beyond this draws no wider; line counts beyond LINES_CAP no taller.
export const CALLERS_CAP = 8;
export const LINES_CAP = 400;
const BUILDING_MIN_W = 3;
const BUILDING_MAX_W = 8;
const BUILDING_MIN_H = 2;
const BUILDING_MAX_H = SLOT - 1;

export interface Cell {
  col: number;
  row: number;
}

export interface Building {
  // A function's id, or for a file block "file:<path>". Never clicked as-is
  // for a block: the component opens the block's first function instead.
  id: string;
  kind: "func" | "file";
  name: string;
  file: string;
  pkg: string;
  lines: number;
  callers: number;
  callees: number;
  changed: boolean;
  // Every function id this shape stands for: one for a function, the file's
  // functions for a block, so a mark on any of them can find its shape.
  ids: string[];
  // The slot the shape sits in, and the shape itself, in world units. cx/cy
  // is where a line or a dot attaches.
  slot: { x: number; y: number };
  x: number;
  y: number;
  w: number;
  h: number;
  cx: number;
  cy: number;
}

export interface District {
  path: string;
  name: string;
  label: string;
  cell: Cell;
  x: number;
  y: number;
  w: number;
  h: number;
  mode: "funcs" | "files";
  // Functions in the district, whatever is drawn.
  count: number;
  buildings: Building[];
}

export interface Bounds {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface CityLayout {
  module: string;
  districts: District[];
  buildings: Building[];
  // Function id → the shape that stands for it.
  byId: Map<string, Building>;
  bounds: Bounds;
}

// ---- hashing and placement ----

// FNV-1a over UTF-16 code units. It only has to be deterministic and spread
// paths that differ in a suffix, which is what package paths do.
export function hashPath(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

export function homeCell(path: string): Cell {
  const h = hashPath(path);
  // Different bits for the two axes, or paths with equal low bits would line
  // up in one column.
  return { col: h % HOME_SIDE, row: Math.floor(h / HOME_SIDE) % HOME_SIDE };
}

// The k-th cell of the spiral around `home`: k = 0 is home itself; then each
// ring at Chebyshev distance r is walked clockwise from its top-left corner.
// A fixed enumeration, so the probe sequence of a path never changes.
export function spiralCell(home: Cell, k: number): Cell {
  if (k === 0) return home;
  // Ring r holds 8r cells; the first index on ring r is 1 + 8·(r-1)·r/2 = 4r(r-1)+1.
  let r = 1;
  while (4 * r * (r + 1) < k) r++;
  const i = k - (4 * r * (r - 1) + 1); // 0 .. 8r-1 along the ring
  const side = 2 * r;
  const x0 = home.col - r;
  const y0 = home.row - r;
  if (i < side) return { col: x0 + i, row: y0 }; // top edge, left→right
  if (i < 2 * side) return { col: x0 + side, row: y0 + (i - side) }; // right edge, down
  if (i < 3 * side) return { col: x0 + side - (i - 2 * side), row: y0 + side }; // bottom, right→left
  return { col: x0, row: y0 + side - (i - 3 * side) }; // left edge, up
}

function cellKey(c: Cell): string {
  return `${c.col},${c.row}`;
}

// Place every path on the grid. Exported for the tests and because it is the
// whole stability argument in one function.
export function placeDistricts(paths: string[]): Map<string, Cell> {
  const sorted = [...new Set(paths)].sort();
  const taken = new Set<string>();
  const out = new Map<string, Cell>();
  for (const p of sorted) {
    const home = homeCell(p);
    for (let k = 0; ; k++) {
      const c = spiralCell(home, k);
      const key = cellKey(c);
      if (!taken.has(key)) {
        taken.add(key);
        out.set(p, c);
        break;
      }
    }
  }
  return out;
}

// ---- shapes ----

export function buildingWidth(callers: number): number {
  const t = Math.min(Math.max(callers, 0), CALLERS_CAP) / CALLERS_CAP;
  return BUILDING_MIN_W + (BUILDING_MAX_W - BUILDING_MIN_W) * t;
}

export function buildingHeight(lines: number): number {
  const t =
    Math.log1p(Math.min(Math.max(lines, 0), LINES_CAP)) / Math.log1p(LINES_CAP);
  return BUILDING_MIN_H + (BUILDING_MAX_H - BUILDING_MIN_H) * t;
}

function slotOrigin(
  d: { x: number; y: number },
  index: number,
): { x: number; y: number } {
  const col = index % SLOT_COLS;
  const row = Math.floor(index / SLOT_COLS);
  return {
    x: d.x + DISTRICT_PAD + col * SLOT,
    y: d.y + DISTRICT_PAD + LABEL_H + row * SLOT,
  };
}

function shape(
  slot: { x: number; y: number },
  w: number,
  h: number,
): Pick<Building, "slot" | "x" | "y" | "w" | "h" | "cx" | "cy"> {
  // Bottom-aligned and centred in the slot, so a row of buildings shares a
  // baseline and heights compare at a glance.
  const x = slot.x + (SLOT - w) / 2;
  const y = slot.y + SLOT - 1 - h;
  return { slot, x, y, w, h, cx: x + w / 2, cy: y + h / 2 };
}

export function sortFuncs(funcs: CityFunc[]): CityFunc[] {
  return [...funcs].sort((a, b) =>
    a.name < b.name
      ? -1
      : a.name > b.name
        ? 1
        : a.id < b.id
          ? -1
          : a.id > b.id
            ? 1
            : 0,
  );
}

// The district label: the import path with the module prefix dropped, which
// is how the code itself refers to it. Falls back to the package name for
// the module root.
export function shortPackage(path: string, module: string): string {
  if (module && path === module)
    return path.slice(path.lastIndexOf("/") + 1) || path;
  if (module && path.startsWith(module + "/"))
    return path.slice(module.length + 1);
  return path;
}

// Keep the tail: "…/server" says more than "github.com/Max…".
export function truncateLabel(label: string, maxChars: number): string {
  if (label.length <= maxChars) return label;
  return "…" + label.slice(label.length - (maxChars - 1));
}

export function baseName(file: string): string {
  return file.slice(file.lastIndexOf("/") + 1);
}

function layoutDistrict(
  pkg: CityPackage,
  module: string,
  cell: Cell,
): District {
  const x = cell.col * CELL_W;
  const y = cell.row * CELL_H;
  const d: District = {
    path: pkg.path,
    name: pkg.name,
    label: shortPackage(pkg.path, module),
    cell,
    x,
    y,
    w: DISTRICT_W,
    h: DISTRICT_H,
    mode: pkg.funcs.length > SLOTS_PER_DISTRICT ? "files" : "funcs",
    count: pkg.funcs.length,
    buildings: [],
  };
  const funcs = sortFuncs(pkg.funcs);

  if (d.mode === "funcs") {
    funcs.forEach((f, i) => {
      d.buildings.push({
        id: f.id,
        kind: "func",
        name: f.name,
        file: f.file,
        pkg: pkg.path,
        lines: f.lines,
        callers: f.callers,
        callees: f.callees,
        changed: !!f.changed,
        ids: [f.id],
        ...shape(
          slotOrigin(d, i),
          buildingWidth(f.callers),
          buildingHeight(f.lines),
        ),
      });
    });
    return d;
  }

  // One block per file, files sorted by path. If even the files overflow,
  // the last slot stands for all the rest — the budget is a hard cap, and a
  // block that says "+12 files" is more honest than a district that stops
  // drawing at the 64th file without a word.
  const byFile = new Map<string, CityFunc[]>();
  for (const f of funcs) {
    const list = byFile.get(f.file);
    if (list) list.push(f);
    else byFile.set(f.file, [f]);
  }
  const files = [...byFile.keys()].sort();
  const direct =
    files.length > SLOTS_PER_DISTRICT ? SLOTS_PER_DISTRICT - 1 : files.length;
  const block = (
    name: string,
    file: string,
    members: CityFunc[],
    index: number,
  ): Building => {
    const lines = members.reduce((n, f) => n + f.lines, 0);
    const callers = members.reduce((n, f) => n + f.callers, 0);
    const callees = members.reduce((n, f) => n + f.callees, 0);
    return {
      id: `file:${file}`,
      kind: "file",
      name,
      file,
      pkg: pkg.path,
      lines,
      callers,
      callees,
      changed: members.some((f) => f.changed),
      ids: members.map((f) => f.id),
      ...shape(slotOrigin(d, index), BUILDING_MAX_W, buildingHeight(lines)),
    };
  };
  for (let i = 0; i < direct; i++) {
    const file = files[i];
    d.buildings.push(block(baseName(file), file, byFile.get(file)!, i));
  }
  if (direct < files.length) {
    const rest = files.slice(direct);
    const members = rest.flatMap((f) => byFile.get(f)!);
    d.buildings.push(block(`+${rest.length} files`, rest[0], members, direct));
  }
  return d;
}

export function layoutCity(data: CityData): CityLayout {
  const cells = placeDistricts(data.packages.map((p) => p.path));
  const districts = [...data.packages]
    .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
    .map((p) => layoutDistrict(p, data.module, cells.get(p.path)!));

  const buildings: Building[] = [];
  const byId = new Map<string, Building>();
  for (const d of districts) {
    for (const b of d.buildings) {
      buildings.push(b);
      for (const id of b.ids) byId.set(id, b);
    }
  }

  // The camera's starting frame, not a coordinate origin: positions are in
  // absolute grid coordinates, and the bounds merely say where the occupied
  // part of the plane currently is.
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const d of districts) {
    minX = Math.min(minX, d.x);
    minY = Math.min(minY, d.y);
    maxX = Math.max(maxX, d.x + d.w);
    maxY = Math.max(maxY, d.y + d.h);
  }
  const bounds: Bounds =
    districts.length === 0
      ? { x: 0, y: 0, w: CELL_W, h: CELL_H }
      : { x: minX, y: minY, w: maxX - minX, h: maxY - minY };

  return { module: data.module, districts, buildings, byId, bounds };
}

// ---- marks: what the map says about the current view ----

export interface CityMarks {
  // The root frame's target id.
  root?: string | null;
  // Target ids of every frame unfolded under the root.
  unfolded?: Iterable<string>;
  // Target id → 1-based stop numbers of the active tour that land there.
  tour?: Map<string, number[]>;
}

export interface MarkedCity {
  root: Building | null;
  // Unfolded callees' shapes, each once, the root excluded.
  lit: Building[];
  // Root → callee, one per lit shape that isn't the root's own.
  links: { from: Building; to: Building }[];
  // Tour stops, grouped by the shape they land on, in stop order.
  stops: { building: Building; numbers: number[] }[];
}

export function markCity(layout: CityLayout, marks: CityMarks): MarkedCity {
  const root = marks.root ? (layout.byId.get(marks.root) ?? null) : null;
  const lit: Building[] = [];
  const seen = new Set<Building>();
  if (root) seen.add(root);
  for (const id of marks.unfolded ?? []) {
    const b = layout.byId.get(id);
    if (b && !seen.has(b)) {
      seen.add(b);
      lit.push(b);
    }
  }
  const links = root ? lit.map((to) => ({ from: root, to })) : [];

  const stopsBy = new Map<Building, number[]>();
  if (marks.tour) {
    for (const [id, numbers] of marks.tour) {
      const b = layout.byId.get(id);
      if (!b) continue;
      const list = stopsBy.get(b) ?? [];
      list.push(...numbers);
      stopsBy.set(b, list);
    }
  }
  const stops = [...stopsBy]
    .map(([building, numbers]) => ({
      building,
      numbers: [...new Set(numbers)].sort((a, b) => a - b),
    }))
    .sort((a, b) => a.numbers[0] - b.numbers[0]);

  return { root, lit, links, stops };
}

// ---- hover and click: the pure halves ----

// The shape under a world-space point. Hit on the whole slot rather than the
// drawn rectangle: a two-unit-tall leaf is not a target anyone can hover.
export function buildingAt(
  layout: CityLayout,
  x: number,
  y: number,
): Building | null {
  for (const d of layout.districts) {
    if (x < d.x || y < d.y || x >= d.x + d.w || y >= d.y + d.h) continue;
    for (const b of d.buildings) {
      if (
        x >= b.slot.x &&
        x < b.slot.x + SLOT &&
        y >= b.slot.y &&
        y < b.slot.y + SLOT
      )
        return b;
    }
    return null;
  }
  return null;
}

export function districtAt(
  layout: CityLayout,
  x: number,
  y: number,
): District | null {
  for (const d of layout.districts) {
    if (x >= d.x && y >= d.y && x < d.x + d.w && y < d.y + d.h) return d;
  }
  return null;
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

// What the hover card says.
export function tooltipText(b: Building): string {
  if (b.kind === "file") {
    return `${b.name} · ${plural(b.ids.length, "function")} · ${plural(b.lines, "line")}`;
  }
  return `${b.name} · ${plural(b.lines, "line")} · ${plural(b.callers, "caller")}`;
}

// Which frame a click opens: the function itself, or for a file block the
// first of its functions — there is no file-level frame worth landing on
// from a map, and the first function of a file is at least in it.
export function clickTarget(b: Building): string {
  return b.ids[0];
}

// ---- the camera ----

export interface ViewBox {
  x: number;
  y: number;
  w: number;
  h: number;
}

// A viewBox that shows all of `bounds` with `pad` world units around it, in
// the aspect ratio of the viewport so nothing is letterboxed off-centre.
export function fitViewBox(
  bounds: Bounds,
  aspect: number,
  pad = CELL_W / 4,
): ViewBox {
  const w0 = bounds.w + 2 * pad;
  const h0 = bounds.h + 2 * pad;
  const a = aspect > 0 && Number.isFinite(aspect) ? aspect : 1;
  let w = w0;
  let h = h0;
  if (w0 / h0 < a) w = h0 * a;
  else h = w0 / a;
  return {
    x: bounds.x - pad - (w - w0) / 2,
    y: bounds.y - pad - (h - h0) / 2,
    w,
    h,
  };
}

// Zoom by `factor` (>1 zooms in) keeping the world point (px, py) fixed on
// screen. Clamped so a wheel can't scroll the map into a single pixel or out
// to nothing.
export function zoomViewBox(
  vb: ViewBox,
  factor: number,
  px: number,
  py: number,
  bounds: Bounds,
): ViewBox {
  const minW = CELL_W / 2;
  const maxW = Math.max(bounds.w, bounds.h) * 4 + CELL_W;
  const f = Math.min(Math.max(vb.w / factor, minW), maxW) / vb.w;
  const w = vb.w * f;
  const h = vb.h * f;
  return { x: px - (px - vb.x) * f, y: py - (py - vb.y) * f, w, h };
}

export function panViewBox(vb: ViewBox, dx: number, dy: number): ViewBox {
  return { ...vb, x: vb.x + dx, y: vb.y + dy };
}

// Client pixel → world unit under preserveAspectRatio="xMidYMid meet", which
// is what the component renders with.
export function clientToWorld(
  vb: ViewBox,
  rect: { left: number; top: number; width: number; height: number },
  clientX: number,
  clientY: number,
): { x: number; y: number } {
  const scale = Math.min(rect.width / vb.w, rect.height / vb.h);
  const ox = (rect.width - vb.w * scale) / 2;
  const oy = (rect.height - vb.h * scale) / 2;
  return {
    x: vb.x + (clientX - rect.left - ox) / scale,
    y: vb.y + (clientY - rect.top - oy) / scale,
  };
}

// World units per client pixel, for turning a drag delta into a pan.
export function worldPerPixel(
  vb: ViewBox,
  rect: { width: number; height: number },
): number {
  return 1 / Math.min(rect.width / vb.w, rect.height / vb.h);
}
