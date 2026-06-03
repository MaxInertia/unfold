import type { CallID, Frame, SearchResult, TargetID } from "./types";

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
