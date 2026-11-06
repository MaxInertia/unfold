import type { CallID, CallSite, Frame, TourStop } from "./types";
import type { FrameSlice } from "./viewState";

// Turning a tour stop — a symbol name, or a file and a line, as an author
// would write them — into something the view can open. Kept free of React
// and of the network so the matching rules are testable on their own; the
// fetchers are injected.

export interface Resolvers {
  fetchSymbol: (name: string) => Promise<Frame>;
  fetchFiles: () => Promise<string[]>;
  fetchBody: (callId: CallID, choice: number) => Promise<Frame>;
}

export type ResolvedStop =
  | {
      kind: "symbol";
      targetId: string;
      title: string;
      file: string;
      line: number; // the function's first line — where the card renders
      tree: FrameSlice;
      // Expand entries that matched no call site, so the panel can say which
      // part of the stop it couldn't do rather than quietly showing less.
      missing: string[];
    }
  | { kind: "file"; file: string; line: number }
  | { kind: "unresolved"; reason: string };

const emptySlice: FrameSlice = { folds: [], expansions: {} };

export async function resolveStop(stop: TourStop, r: Resolvers): Promise<ResolvedStop> {
  if (stop.symbol) {
    let frame: Frame;
    try {
      frame = await r.fetchSymbol(stop.symbol);
    } catch (e) {
      return { kind: "unresolved", reason: `no symbol matches "${stop.symbol}"` };
    }
    const { tree, missing } = await buildTree(frame, stop.expand ?? [], r.fetchBody);
    return {
      kind: "symbol",
      targetId: frame.id,
      title: frame.title ?? frame.id,
      file: frame.file,
      line: frame.startLine,
      tree,
      missing,
    };
  }
  if (stop.file) {
    const files = await r.fetchFiles().catch(() => [] as string[]);
    const abs = matchFile(files, stop.file);
    if (!abs) return { kind: "unresolved", reason: `no indexed file matches "${stop.file}"` };
    return { kind: "file", file: abs, line: stop.line ?? 1 };
  }
  return { kind: "unresolved", reason: "stop names neither a symbol nor a file" };
}

// A path as written resolves against the indexed files by suffix, so
// "internal/pubsub/retry.go" finds the absolute path without the author
// knowing where the checkout lives. Longest match wins when several files
// share a suffix; an exact absolute path always wins.
export function matchFile(files: string[], path: string): string | null {
  const clean = path.replace(/^\.\//, "");
  if (files.includes(clean)) return clean;
  let best: string | null = null;
  for (const f of files) {
    if (f.endsWith("/" + clean) && (best === null || f.length < best.length)) best = f;
  }
  return best;
}

// "retry.Do > backoff.Next" → ["retry.Do", "backoff.Next"]: each name is a
// call inside the frame the previous one opened.
export function parseExpand(entry: string): string[] {
  return entry
    .split(">")
    .map((s) => s.trim())
    .filter(Boolean);
}

// Find the call site an author's name refers to. Call sites carry the name
// as it appears at the site ("retry.Do", or just "Publish" for a method on
// a field), so the match is suffix-tolerant in both directions: "Do" finds
// retry.Do, and "retry.Do" finds a site displayed as Do whose target ends in
// retry.Do. "#n" picks the nth match when a function is called more than
// once in the body.
export function findCall(calls: CallSite[], name: string): CallSite | undefined {
  return findCallChoice(calls, name)?.call;
}

// findCall plus which implementation to open: an interface call names its
// method at the site ("lister.Functions") and its implementations only as
// candidates, so an author naming one of those — "indexer.Indexer.Functions"
// — picks that candidate rather than whichever is first.
export function findCallChoice(
  calls: CallSite[],
  name: string,
): { call: CallSite; choice: number } | undefined {
  const m = name.match(/^(.*?)#(\d+)$/);
  const want = m ? m[1].trim() : name.trim();
  const nth = m ? Math.max(1, Number(m[2])) : 1;
  const matching: { call: CallSite; choice: number }[] = [];
  for (const c of calls) {
    const choice = callChoice(c, want);
    if (choice !== null) matching.push({ call: c, choice });
  }
  return matching[nth - 1];
}

// The candidate index the name picks at this site, 0 when it names the site
// itself, null when it names neither.
function callChoice(c: CallSite, name: string): number | null {
  const shown = c.displayName;
  if (shown === name) return 0;
  if (name.endsWith("." + shown) || shown.endsWith("." + name)) return 0;
  if (c.targetId && symbolMatches(c.targetId, name)) return 0;
  const i = (c.candidates ?? []).findIndex((cand) => symbolMatches(cand.targetId, name));
  return i >= 0 ? i : null;
}

// Whether a full name as the Go engine keys it — "path/to/pkg.Func",
// "(*path/to/pkg.T).M" — is what a reader's query names. Same rule as the
// server's LookupSymbol: the query is any suffix of the dotted form, with
// receiver punctuation ignored, so "M", "T.M", "pkg.T.M", "(*T).M" and
// "(*pkg.T).M" all name the method and "AutoSave" doesn't name Save.
export function symbolMatches(full: string, query: string): boolean {
  if (full === query) return true;
  const dotted = dottedName(full);
  const q = query.trim().replace(/\(\*?/g, "").replace(/\)/g, "");
  if (!q) return false;
  return dotted === q || dotted.endsWith("." + q) || dotted.endsWith("/" + q);
}

function dottedName(full: string): string {
  if (!full.startsWith("(")) return full;
  const close = full.indexOf(")");
  if (close < 0) return full;
  const recv = full.slice(1, close).replace(/^\*/, "");
  return recv + full.slice(close + 1);
}

// Build the view tree a stop asks for. Every expand entry is a path from the
// root frame; paths that share a prefix share the nodes. Missing names are
// collected rather than thrown: an author can name a call that moved, and
// the rest of the stop should still open.
export async function buildTree(
  root: Frame,
  expand: string[],
  fetchBody: Resolvers["fetchBody"],
): Promise<{ tree: FrameSlice; missing: string[] }> {
  const tree: FrameSlice = { folds: [], expansions: {} };
  const missing: string[] = [];
  // Bodies fetched while walking, so two paths through the same call don't
  // fetch it twice.
  const bodies = new Map<string, Promise<Frame>>();
  const bodyOf = (callId: CallID, choice: number) => {
    const key = `${callId}#${choice}`;
    let p = bodies.get(key);
    if (!p) {
      p = fetchBody(callId, choice);
      bodies.set(key, p);
    }
    return p;
  };

  for (const entry of expand) {
    const steps = parseExpand(entry);
    if (steps.length === 0) continue;
    let frame = root;
    let slice = tree;
    for (let i = 0; i < steps.length; i++) {
      const found = findCallChoice(frame.calls, steps[i]);
      if (!found) {
        missing.push(entry);
        break;
      }
      const { call, choice } = found;
      // One site opens one implementation: a later path naming another
      // candidate at the same site takes it over, the last word wins.
      let child = slice.expansions[call.id];
      if (!child) {
        child = { folds: [], expansions: {}, choice };
        slice.expansions[call.id] = child;
      } else if (child.choice !== choice) {
        child.choice = choice;
      }
      slice = child;
      // Only fetch the body if there is a further step to find inside it.
      if (i < steps.length - 1) {
        try {
          frame = await bodyOf(call.id, choice);
        } catch {
          missing.push(entry);
          break;
        }
      }
    }
  }
  return { tree: Object.keys(tree.expansions).length ? tree : emptySlice, missing };
}
