import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

// Revision counter that increments each time the server reindexes the project
// (watch mode). The backend pushes a "reload" Server-Sent Event on /api/events;
// components key their data fetches on this number so the view refreshes when
// source files change on disk. 0 means "no reload yet".
const ReloadContext = createContext(0);

// Repository state — which services are being read — moves on its own stream
// of events, because it isn't a reload: nothing about the code on screen
// changed, and a view that refetched itself every time a background load
// ticked would be redrawing to report someone else's progress.
const ReposContext = createContext(0);

// Bumped when a repository finishes indexing — a change in what the index can
// answer, as opposed to a change in what it is busy with.
const IndexedContext = createContext(0);

export function ReloadProvider({ children }: { children: ReactNode }) {
  const [revision, setRevision] = useState(0);
  const [repos, setRepos] = useState(0);
  const [indexed, setIndexed] = useState(0);
  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const connect = () => {
      es = new EventSource("/api/events");
      es.addEventListener("reload", () => setRevision((n) => n + 1));
      es.addEventListener("repos", () => setRepos((n) => n + 1));
      // A repository finished being read, so the index can now say things it
      // couldn't a moment ago — a boundary whose far service was still
      // loading reported no far end at all.
      es.addEventListener("index", () => setIndexed((n) => n + 1));
      es.onerror = () => {
        // The stream dropped (e.g. server restart). Reconnect after a short
        // delay; EventSource also retries on its own, but closing avoids a
        // tight error loop on a hard failure.
        es?.close();
        if (!closed) retry = setTimeout(connect, 1500);
      };
    };
    connect();

    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      es?.close();
    };
  }, []);
  return (
    <ReloadContext.Provider value={revision}>
      <ReposContext.Provider value={repos}>
        <IndexedContext.Provider value={indexed}>{children}</IndexedContext.Provider>
      </ReposContext.Provider>
    </ReloadContext.Provider>
  );
}

export function useReloadRevision(): number {
  return useContext(ReloadContext);
}

// Bumped when a repository starts or finishes indexing.
export function useReposRevision(): number {
  return useContext(ReposContext);
}

// Everything that can change what the index says: a rebuild, and a repository
// finishing its own. A view that refetches on this stays honest without
// redrawing to report someone else's progress.
export function useIndexRevision(): number {
  return useContext(ReloadContext) + useContext(IndexedContext);
}
