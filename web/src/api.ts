import type { CallID, Frame, Note, SearchResult, TargetID, TypeInfo, Usage } from "./types";

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = `${res.status}: ${body.error}`;
    } catch {
      /* body wasn't JSON */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export function fetchSymbol(name: string): Promise<Frame> {
  return getJSON<Frame>(`/api/symbol?name=${encodeURIComponent(name)}`);
}

export function fetchBodyByTarget(id: TargetID): Promise<Frame> {
  return getJSON<Frame>(`/api/body?targetId=${encodeURIComponent(id)}`);
}

export function fetchBodyByCall(id: CallID, choice = 0): Promise<Frame> {
  const params = new URLSearchParams({ callId: id });
  if (choice > 0) params.set("choice", String(choice));
  return getJSON<Frame>(`/api/body?${params.toString()}`);
}

export async function search(q: string, limit = 25): Promise<SearchResult[]> {
  const url = `/api/search?q=${encodeURIComponent(q)}&limit=${limit}`;
  const res = await getJSON<{ results: SearchResult[] }>(url);
  return res.results ?? [];
}

export async function fetchFiles(): Promise<string[]> {
  const res = await getJSON<{ files: string[] }>("/api/files");
  return res.files ?? [];
}

export async function fetchUsages(targetId: TargetID): Promise<Usage[]> {
  const url = `/api/usages?targetId=${encodeURIComponent(targetId)}`;
  const res = await getJSON<{ usages: Usage[] }>(url);
  return res.usages ?? [];
}

export async function fetchTypeInfo(targetId: TargetID, offset: number): Promise<TypeInfo | null> {
  const url = `/api/typeinfo?targetId=${encodeURIComponent(targetId)}&offset=${offset}`;
  const res = await getJSON<{ typeInfo: TypeInfo | null }>(url);
  return res.typeInfo ?? null;
}

export async function fetchNotes(): Promise<Note[]> {
  const res = await getJSON<{ notes: Note[] }>("/api/notes");
  return res.notes ?? [];
}

// Create (no id) or update (with id) a note. POST: mutations are
// same-origin-guarded server-side, like /api/open.
export async function saveNote(note: Partial<Note>): Promise<Note> {
  const res = await fetch("/api/notes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(note),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* not JSON */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<Note>;
}

export async function deleteNote(id: string): Promise<void> {
  const res = await fetch(`/api/notes?id=${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
}

export async function openInEditor(file: string, line: number): Promise<void> {
  const url = `/api/open?file=${encodeURIComponent(file)}&line=${line}`;
  // POST (not GET) so a cross-origin page can't trigger an editor-open via a
  // bare <img>/<form>; the server also enforces a same-origin check.
  const res = await fetch(url, { method: "POST" });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* not JSON */
    }
    throw new Error(msg);
  }
}
