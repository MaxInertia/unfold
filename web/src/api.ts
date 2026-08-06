import type {
  CallID,
  PlatformView,
  Resolution,
  Frame,
  Note,
  SearchResult,
  ServiceView,
  TargetID,
  TypeInfo,
  Usage,
} from "./types";

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

// The zoomed-out service view. Passing the frame you zoomed out from as the
// anchor is what lets the view mark which entrypoints actually reach it.
// `repo` selects which workspace service the view is about; omitted, it's the
// one unfold was pointed at.
export function fetchServiceView(
  anchor?: TargetID | null,
  repo?: string | null,
): Promise<ServiceView> {
  const qs = new URLSearchParams();
  if (anchor) qs.set("anchor", anchor);
  if (repo) qs.set("repo", repo);
  const s = qs.toString();
  return getJSON<ServiceView>(`/api/service${s ? `?${s}` : ""}`);
}

// The workspace-level view: services and the calls between them.
export function fetchPlatformView(anchor?: TargetID | null): Promise<PlatformView> {
  const qs = anchor ? `?anchor=${encodeURIComponent(anchor)}` : "";
  return getJSON<PlatformView>(`/api/platform${qs}`);
}

// Index one service's code, filling in its outgoing edges. Expensive and
// state-changing, hence POST.
export async function indexRepo(alias: string): Promise<void> {
  const res = await fetch("/api/index-repo", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ alias }),
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
}

// Open the implementation of an outbound edge in whichever workspace repo
// serves it. Separate from the service view because this is where a lazily
// indexed repo's Go code actually gets built — it can take seconds.
export function resolveBinding(kind: string, key: string): Promise<Resolution> {
  const qs = new URLSearchParams({ kind, key });
  return getJSON<Resolution>(`/api/resolve?${qs.toString()}`);
}

// The subdirectories of a path, for the proto-root picker. A browser can't
// give the server a real filesystem path from a native picker, so browsing
// happens server-side.
export function browseDirs(path: string): Promise<{ path: string; parent?: string; dirs: string[] }> {
  return getJSON(`/api/dirs?path=${encodeURIComponent(path)}`);
}

// Point the declared gRPC surface at the shared proto repository. POST and
// same-origin-guarded server-side, like the other filesystem-touching calls.
export async function setProtoRoot(path: string): Promise<void> {
  const res = await fetch("/api/proto-root", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path }),
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
