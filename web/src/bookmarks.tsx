import { useSyncExternalStore } from "react";
import type { TargetID } from "./types";

// A bookmark is a saved symbol you can jump back to. We keep a display
// `title` and `file`/`line` so the list reads well and survives even if the
// raw targetId later goes stale (a future open path can re-resolve by title).
export interface Bookmark {
  targetId: TargetID;
  title: string;
  file: string;
  line?: number;
}

// Bookmarks are personal and per-project, so they live in localStorage keyed
// by the loaded project (the /api/health target) — repos don't bleed into
// each other. This is a tiny module-level store with a subscribe hook, the
// same shape the rest of the app uses for cross-component state.
let projectKey = "default";
let cache: Bookmark[] = [];
const listeners = new Set<() => void>();

function storageKey(): string {
  return `unfold.bookmarks.${projectKey}`;
}

function read(): Bookmark[] {
  if (typeof localStorage === "undefined") return [];
  try {
    const raw = localStorage.getItem(storageKey());
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function write() {
  try {
    localStorage.setItem(storageKey(), JSON.stringify(cache));
  } catch {
    /* storage full / unavailable — keep the in-memory copy */
  }
  notify();
}

function notify() {
  for (const fn of listeners) fn();
}

// Point the store at a project (its bookmarks namespace) and load them.
export function setBookmarkProject(key: string | null) {
  projectKey = key && key.trim() ? key : "default";
  cache = read();
  notify();
}

export function listBookmarks(): Bookmark[] {
  return cache;
}

export function isBookmarked(id: TargetID): boolean {
  return cache.some((b) => b.targetId === id);
}

export function addBookmark(b: Bookmark) {
  if (!isBookmarked(b.targetId)) {
    cache = [...cache, b];
    write();
  }
}

export function removeBookmark(id: TargetID) {
  cache = cache.filter((b) => b.targetId !== id);
  write();
}

export function toggleBookmark(b: Bookmark) {
  if (isBookmarked(b.targetId)) removeBookmark(b.targetId);
  else addBookmark(b);
}

cache = read();

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

// Subscribe a component to bookmark changes. useSyncExternalStore is the
// canonical way to read an external store: it can't miss an update between
// render and subscribe, and it won't tear under concurrent rendering — both
// of which a hand-rolled useState+useEffect subscription can. `cache` is a
// stable reference that only changes when bookmarks change, so the snapshot
// is correctly memoized.
export function useBookmarks() {
  const bookmarks = useSyncExternalStore(
    subscribe,
    () => cache,
    () => cache,
  );
  return {
    bookmarks,
    isBookmarked,
    toggle: toggleBookmark,
    remove: removeBookmark,
  };
}
