import type {
  CallID,
  PlatformView,
  Resolution,
  Frame,
  Note,
  RepoInfo,
  RuleReport,
  RuleSpec,
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

// repo is the service the reader is currently in, which ranks first in the
// results. Null (a plain repo, or nothing open yet) means the primary one.
export async function search(
  q: string,
  limit = 25,
  repo?: string | null,
): Promise<SearchResult[]> {
  const params = new URLSearchParams({ q, limit: String(limit) });
  if (repo) params.set("repo", repo);
  const res = await getJSON<{ results: SearchResult[] }>(`/api/search?${params.toString()}`);
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
// role is the side the *caller* is on, because the far end is a direction
// rather than a place: an emit resolves to the subscribers, a subscription to
// the publishers.
export function resolveBinding(
  kind: string,
  key: string,
  role?: string,
): Promise<Resolution> {
  const qs = new URLSearchParams({ kind, key });
  if (role) qs.set("role", role);
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

// Open another repository, or stop opening one, without restarting. The
// server rebuilds the engine and pushes a reload over /api/events, so the
// views refresh themselves — there's nothing to return but the new repo list.
export async function linkRepo(path: string, unlink = false): Promise<{ repos: RepoInfo[] }> {
  const res = await fetch("/api/repos", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path, unlink }),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(body?.error ?? `link failed: ${res.status}`);
  }
  return res.json();
}

export function fetchRules(): Promise<RuleReport> {
  return getJSON<RuleReport>("/api/rules");
}

// Saving a rule rebuilds the index — what a rule matched is only knowable by
// running it, which is also why the match count comes back after saving rather
// than as a preview.
export async function saveRule(rule: RuleSpec): Promise<RuleReport> {
  return postRules({ rule });
}

export async function deleteRule(id: string): Promise<RuleReport> {
  return postRules({ delete: id });
}

async function postRules(body: unknown): Promise<RuleReport> {
  const res = await fetch("/api/rules", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const b = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(b?.error ?? `rules failed: ${res.status}`);
  }
  return res.json();
}
