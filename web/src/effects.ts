import type { Effect } from "./types";

// Pure logic behind the side-effect badges: how a list of evidence rows
// becomes a row of badges, and how one row is labelled. Kept out of the
// component so it can be tested without a DOM.

// What each kind looks like. The order is the order badges appear in, chosen
// so the ones that matter most for "is it safe to call this" come first.
interface KindStyle {
  icon: string;
  label: string;
  name: string;
}

const KINDS: Record<string, KindStyle> = {
  db: { icon: "🗄", label: "db", name: "database" },
  net: { icon: "🌐", label: "net", name: "network" },
  file: { icon: "📄", label: "file", name: "file system" },
  env: { icon: "⚙", label: "env", name: "environment" },
  global: { icon: "◎", label: "global", name: "package-level variables" },
  time: { icon: "⏱", label: "sleep", name: "time: sleeps, tickers, timers" },
  process: { icon: "▶", label: "proc", name: "processes: exec, exit, signals" },
  goroutine: { icon: "⚡", label: "go", name: "goroutines launched" },
  channel: { icon: "⇄", label: "chan", name: "channel sends and receives" },
};

const ORDER = Object.keys(KINDS);

export type Access = "" | "r" | "w" | "rw";

export interface Badge {
  kind: string;
  icon: string;
  // "db·w", "file·r", "net" — the kind's short label with the access suffix
  // when any evidence has one.
  label: string;
  // Hover text: the kind's long name and the counts.
  title: string;
  access: Access;
  direct: number;
  inherited: number;
  // All evidence for this kind, direct first (the server's order).
  effects: Effect[];
}

// summarize folds evidence into one badge per kind. A kind no style knows —
// one a user rule invented — still gets a badge, after the known ones, with
// its own name as the label.
export function summarize(effects: Effect[]): Badge[] {
  const byKind = new Map<string, Effect[]>();
  for (const e of effects) {
    const list = byKind.get(e.kind);
    if (list) list.push(e);
    else byKind.set(e.kind, [e]);
  }
  const kinds = [...byKind.keys()].sort((a, b) => {
    const ia = ORDER.indexOf(a);
    const ib = ORDER.indexOf(b);
    if (ia >= 0 && ib >= 0) return ia - ib;
    if (ia >= 0) return -1;
    if (ib >= 0) return 1;
    return a.localeCompare(b);
  });
  return kinds.map((kind) => {
    const list = byKind.get(kind)!;
    const style = KINDS[kind] ?? { icon: "•", label: kind, name: kind };
    const access = accessOf(list);
    const direct = list.filter((e) => e.depth === 0).length;
    const inherited = list.length - direct;
    const parts: string[] = [];
    if (direct) parts.push(`${direct} direct`);
    if (inherited) parts.push(`${inherited} inherited`);
    return {
      kind,
      icon: style.icon,
      label: access ? `${style.label}·${access}` : style.label,
      title: `${style.name}${accessName(access)} — ${parts.join(", ")}`,
      access,
      direct,
      inherited,
      effects: list,
    };
  });
}

// accessOf is the union of what the evidence says: a read and a write make
// "rw"; evidence that says nothing about direction contributes nothing.
export function accessOf(effects: Effect[]): Access {
  let r = false;
  let w = false;
  for (const e of effects) {
    if (e.access === "read") r = true;
    if (e.access === "write") w = true;
  }
  if (r && w) return "rw";
  if (r) return "r";
  if (w) return "w";
  return "";
}

function accessName(a: Access): string {
  switch (a) {
    case "r":
      return " (reads)";
    case "w":
      return " (writes)";
    case "rw":
      return " (reads and writes)";
    default:
      return "";
  }
}

// evidenceLabel is the row's headline: what was called, and the static detail
// when there is one — "os.Getenv APP_NAME", "exec.Command ls".
export function evidenceLabel(e: Effect): string {
  return e.key ? `${e.callee} ${e.key}` : e.callee;
}

// hopLabel says how the evidence reaches the frame: in its own body, or
// through which of its callees, and how many calls further down.
export function hopLabel(e: Effect): string {
  if (e.depth === 0) return "direct";
  const via = e.viaTitle || e.via || "?";
  return e.depth > 1 ? `via ${via} +${e.depth - 1}` : `via ${via}`;
}

// shortLoc trims a path to its last two segments, matching the frame header.
export function shortLoc(file: string, line: number): string {
  const idx = file.lastIndexOf("/");
  let p = file;
  if (idx >= 0) {
    const slash2 = file.lastIndexOf("/", idx - 1);
    p = slash2 < 0 ? file.slice(idx + 1) : file.slice(slash2 + 1);
  }
  return `${p}:${line}`;
}
