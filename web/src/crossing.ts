import type { Binding, ServiceView } from "./types";

// The crossing relation, from the reader's side.
//
// The backend stores it one way — each inbound binding lists the outbound
// bindings it can cause — because the relation is symmetric and sending both
// directions would double the largest thing on the view. So the reverse is
// derived here, once, rather than at every render.
//
// Both directions answer a real question and they are not the same question:
//
//   inbound  → "this route is hit; what does the service then call?"
//   outbound → "this call happened; what could have caused it?"
//
// The second is the one that's hard to answer by hand, because it means
// walking callers until you hit something registered as an entrypoint.

export interface Crossing {
  // Which bindings the given id connects to, whichever side it's on.
  linked(id: string): Set<string>;
  // Whether the relation is knowable for this binding. Inbound bindings say
  // so directly; an outbound one is knowable when at least one inbound
  // binding could be walked, since the answer is assembled from those walks.
  known(b: Binding): boolean;
}

export function buildCrossing(view: ServiceView | null): Crossing {
  const forward = new Map<string, Set<string>>();
  const reverse = new Map<string, Set<string>>();
  let anyWalked = false;

  for (const b of view?.inbound ?? []) {
    if (!b.id) continue;
    if (b.crossingKnown) anyWalked = true;
    const outs = new Set(b.reaches ?? []);
    forward.set(b.id, outs);
    for (const o of outs) {
      const cur = reverse.get(o);
      if (cur) cur.add(b.id);
      else reverse.set(o, new Set([b.id]));
    }
  }

  const empty: Set<string> = new Set();
  return {
    linked: (id) => forward.get(id) ?? reverse.get(id) ?? empty,
    known: (b) => (b.role === "inbound" ? !!b.crossingKnown : anyWalked),
  };
}

// What to tell the reader about a selected binding. Kept next to the relation
// because the wording depends on the same distinction the data encodes: a
// determined empty and an undetermined one read very differently, and only
// one of them is a fact about the code.
export function crossingSummary(b: Binding, count: number, known: boolean): string {
  const label = b.role === "inbound" ? "calls" : "entrypoints";
  if (!known) {
    return b.role === "inbound"
      ? "no indexed handler for this entry, so what it calls is unknown"
      : "no entrypoint could be walked, so what causes this is unknown";
  }
  if (count === 0) {
    return b.role === "inbound"
      ? "reaches no outbound call"
      : "no recognized entrypoint reaches this call";
  }
  return b.role === "inbound"
    ? `reaches ${count} outbound ${count === 1 ? "call" : label}`
    : `reached from ${count} ${count === 1 ? "entrypoint" : label}`;
}
