import { useSyncExternalStore } from "react";

// Whether the recognizers panel is open, and which rule it should be looking
// at. A store rather than a prop because the request comes from a hover card
// inside a Frame, and frames nest arbitrarily deep — threading a callback down
// every level to reach the one component that owns the panel would put an
// unrelated prop on every frame in the tree.
//
// Session-only, deliberately: which rule you last inspected is not something
// to restore three days later, unlike the settings this pattern is borrowed
// from.
export interface RulesPanelState {
  open: boolean;
  // The rule to scroll to and expand when the panel opens. Cleared once the
  // panel has honoured it, so reopening the panel by hand doesn't re-focus
  // whatever a card asked for an hour ago.
  focus: string | null;
}

const CLOSED: RulesPanelState = { open: false, focus: null };
let cache: RulesPanelState = CLOSED;
const listeners = new Set<() => void>();

function emit() {
  for (const fn of listeners) fn();
}

export function openRules(focus?: string) {
  cache = { open: true, focus: focus ?? null };
  emit();
}

export function closeRules() {
  cache = CLOSED;
  emit();
}

export function toggleRules() {
  cache = cache.open ? CLOSED : { open: true, focus: null };
  emit();
}

// Called by the panel once it has scrolled to the requested rule, so the focus
// is a one-shot instruction rather than sticky state.
export function clearRulesFocus() {
  if (cache.focus === null) return;
  cache = { ...cache, focus: null };
  emit();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

export function useRulesPanel(): RulesPanelState {
  return useSyncExternalStore(
    subscribe,
    () => cache,
    () => CLOSED,
  );
}
