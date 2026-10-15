// The keyboard shortcuts, in one place.
//
// This is a registry, not a documentation list: every consumer matches events
// through `matches(binding, event)`, so what the settings panel shows is by
// construction what the app actually does. A hand-written list next to
// hand-written handlers is exactly the kind of thing that quietly goes stale.

// The subset of a keyboard event a binding tests. Both DOM KeyboardEvent and
// React's synthetic event satisfy it, so handlers of either kind can match.
export type KeyLike = {
  key: string;
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  shiftKey: boolean;
};

export type KeyScope = "zoom" | "history" | "reading" | "search" | "notes" | "tour";

export interface Keybinding {
  id: string;
  scope: KeyScope;
  // Display form, e.g. ["alt", "↑"]. Rendered as separate <kbd> elements.
  keys: string[];
  description: string;
  matches: (e: KeyLike) => boolean;
}

// True on Apple platforms, where the conventional chord key is ⌘ rather than
// ctrl. Guarded for non-browser contexts (tests, SSR).
const isApple =
  typeof navigator !== "undefined" && /Mac|iPhone|iPad|iPod/.test(navigator.platform ?? "");

const modLabel = isApple ? "⌘" : "ctrl";

// A plain key with no modifiers held. Chords are spelled out explicitly so a
// binding never fires as a side effect of a different chord.
function bare(key: string) {
  return (e: KeyLike) => e.key === key && !e.altKey && !e.ctrlKey && !e.metaKey;
}

function withAlt(key: string) {
  return (e: KeyLike) => e.key === key && e.altKey && !e.ctrlKey && !e.metaKey;
}

function withMod(key: string) {
  return (e: KeyLike) => e.key === key && (e.metaKey || e.ctrlKey);
}

export const KEYBINDINGS: Keybinding[] = [
  {
    id: "zoom.out",
    scope: "zoom",
    keys: ["alt", "↑"],
    description: "zoom out to the service — its inbound surface and outbound dependencies",
    matches: withAlt("ArrowUp"),
  },
  {
    id: "zoom.in",
    scope: "zoom",
    keys: ["alt", "↓"],
    description: "zoom back into the code (your expansion state is untouched)",
    matches: withAlt("ArrowDown"),
  },
  // Vertical movement is the trail (alt+↑/↓); horizontal is history. Keeping
  // them on separate axes is the point: back/forward undoes *lateral* moves —
  // re-rooting, switching an impl, opening a service — and only doubles as a
  // zoom undo as a safety net, so there aren't two gestures for "go back"
  // that disagree about what they'll do.
  {
    id: "history.back",
    scope: "history",
    keys: ["alt", "←"],
    description: "go back to the previous view (re-roots, zooms, opened services)",
    matches: withAlt("ArrowLeft"),
  },
  {
    id: "history.forward",
    scope: "history",
    keys: ["alt", "→"],
    description: "go forward again after going back",
    matches: withAlt("ArrowRight"),
  },
  {
    id: "search.openFirst",
    scope: "search",
    keys: ["enter"],
    description: "open the first search result",
    matches: bare("Enter"),
  },
  {
    id: "notes.save",
    scope: "notes",
    keys: [modLabel, "enter"],
    description: "save the note being written",
    matches: withMod("Enter"),
  },
  // Bare keys, so they are ignored while typing — see inTextField.
  {
    id: "tour.next",
    scope: "tour",
    keys: ["]"],
    description: "go to the next stop of the active tour",
    matches: bare("]"),
  },
  {
    id: "tour.prev",
    scope: "tour",
    keys: ["["],
    description: "go back to the previous stop",
    matches: bare("["),
  },
  {
    id: "ui.dismiss",
    scope: "reading",
    keys: ["esc"],
    description: "close settings, clear a line selection, or cancel a note",
    matches: bare("Escape"),
  },
];

const byId = new Map(KEYBINDINGS.map((b) => [b.id, b]));

// Look up a binding and test an event against it. Throws on an unknown id so
// a typo fails loudly at first press rather than silently never matching.
export function matches(id: string, e: KeyLike): boolean {
  const binding = byId.get(id);
  if (!binding) throw new Error(`unknown keybinding: ${id}`);
  return binding.matches(e);
}

export const SCOPE_LABELS: Record<KeyScope, string> = {
  zoom: "zoom",
  history: "history",
  reading: "reading",
  search: "search",
  notes: "notes",
  tour: "tour",
};

// Bindings grouped for display, in the order scopes are declared above.
export function bindingsByScope(): [KeyScope, Keybinding[]][] {
  const groups = new Map<KeyScope, Keybinding[]>();
  for (const b of KEYBINDINGS) {
    const list = groups.get(b.scope);
    if (list) list.push(b);
    else groups.set(b.scope, [b]);
  }
  return [...groups];
}

// Whether a key event came from somewhere text is being typed. A bare-key
// binding on the window must step aside there, or typing "]" into the search
// box walks the tour. An *empty* field doesn't count: the search box has
// focus when the page opens, and nothing is being typed into it yet, so the
// first "]" should walk the tour rather than silently do nothing.
export function inTextField(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toLowerCase();
  if (tag === "input" || tag === "textarea") {
    return (el as HTMLInputElement).value !== "";
  }
  return tag === "select" || !!el.isContentEditable;
}
