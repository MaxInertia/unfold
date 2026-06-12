import { useSyncExternalStore } from "react";
import { deleteNote as apiDeleteNote, fetchNotes, saveNote } from "./api";
import type { Note, NoteAnchor } from "./types";

// Server-backed notes store: the source of truth is the project's
// .unfold/notes.json (via /api/notes); this module keeps an in-memory copy
// with the same subscribe pattern as bookmarks/settings. Mutations are
// optimistic about nothing — they await the server, then update the cache,
// so a failed write never shows a phantom note.
let cache: Note[] = [];
let loadStarted = false;
const listeners = new Set<() => void>();

function notify() {
  for (const fn of listeners) fn();
}

// Fetch the project's notes once per session (App calls this on boot).
// A server without notes enabled (501) just leaves the store empty.
export function loadNotes() {
  if (loadStarted) return;
  loadStarted = true;
  fetchNotes()
    .then((notes) => {
      cache = notes;
      notify();
    })
    .catch(() => {});
}

export async function upsertNote(note: {
  id?: string;
  anchor: NoteAnchor;
  text: string;
}): Promise<Note> {
  const saved = await saveNote(note);
  cache = cache.some((n) => n.id === saved.id)
    ? cache.map((n) => (n.id === saved.id ? saved : n))
    : [...cache, saved];
  notify();
  return saved;
}

export async function removeNote(id: string): Promise<void> {
  await apiDeleteNote(id);
  cache = cache.filter((n) => n.id !== id);
  notify();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

export function useNotes(): Note[] {
  return useSyncExternalStore(
    subscribe,
    () => cache,
    () => cache,
  );
}
