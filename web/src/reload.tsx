import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

// Revision counter that increments each time the server reindexes the project
// (watch mode). The backend pushes a "reload" Server-Sent Event on /api/events;
// components key their data fetches on this number so the view refreshes when
// source files change on disk. 0 means "no reload yet".
const ReloadContext = createContext(0);

export function ReloadProvider({ children }: { children: ReactNode }) {
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const connect = () => {
      es = new EventSource("/api/events");
      es.addEventListener("reload", () => setRevision((n) => n + 1));
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
  return <ReloadContext.Provider value={revision}>{children}</ReloadContext.Provider>;
}

export function useReloadRevision(): number {
  return useContext(ReloadContext);
}
