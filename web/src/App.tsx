import { useEffect, useMemo, useState } from "react";
import { Frame } from "./Frame";
import { fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult } from "./types";
import {
  ViewStoreProvider,
  useRootSlice,
  useViewStore,
  type Annotation,
  type FrameSlice,
} from "./viewState";

export function App() {
  return (
    <ViewStoreProvider>
      <AppShell />
    </ViewStoreProvider>
  );
}

function AppShell() {
  const store = useViewStore();
  const symbol = store.symbol;
  const [target, setTarget] = useState<string | null>(null);
  const [rootFrame, setRootFrame] = useState<FrameT | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!symbol) {
      setRootFrame(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    fetchSymbol(symbol)
      .then((f) => {
        setRootFrame(f);
        setLoading(false);
      })
      .catch((e: Error) => {
        setError(e.message);
        setLoading(false);
      });
  }, [symbol]);

  useEffect(() => {
    fetch("/api/health")
      .then((r) => r.json())
      .then((h) => setTarget(h.target ?? null))
      .catch(() => {});
  }, []);

  return (
    <div className="app">
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
        <ExportFeedbackButton />
      </header>
      <SymbolPicker onPick={(s) => store.setSymbol(s)} />
      {error && <div className="app-error">{error}</div>}
      {loading && <div className="app-loading">loading…</div>}
      {rootFrame && (
        <div className="app-root-frame">
          <Frame frame={rootFrame} path={[]} />
        </div>
      )}
      {!rootFrame && !loading && !error && (
        <p className="app-hint">
          Search for a function above and select one to start. Click any
          underlined call site to expand its body inline; interface calls
          surface a dropdown to pick which implementation to view. Click a
          line number to start a selection, shift-click another to extend,
          then "fold" to collapse the range. URL hash carries your view —
          reload preserves it, and the link is shareable.
        </p>
      )}
    </div>
  );
}

function ExportFeedbackButton() {
  const store = useViewStore();
  const root = useRootSlice();
  const [copied, setCopied] = useState(false);

  const annotations = useMemo(() => collectAnnotations(root, []), [root]);

  if (annotations.length === 0) return null;

  async function exportFeedback() {
    const payload = {
      symbol: store.symbol,
      generated_at: new Date().toISOString(),
      annotations: annotations.map(({ ann, frameKey }) => ({
        frame: frameKey,
        line_range: [ann.start, ann.end],
        comment: ann.comment,
      })),
    };
    const text = JSON.stringify(payload, null, 2);
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard might be blocked — fall back to a downloadable blob.
      const blob = new Blob([text], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "unfold-feedback.json";
      a.click();
      URL.revokeObjectURL(url);
    }
  }

  return (
    <button type="button" className="app-export" onClick={exportFeedback}>
      {copied ? "copied!" : `export feedback (${annotations.length})`}
    </button>
  );
}

interface CollectedAnnotation {
  ann: Annotation;
  frameKey: string;
}

function collectAnnotations(slice: FrameSlice, path: string[]): CollectedAnnotation[] {
  const here = slice.annotations.map((ann) => ({
    ann,
    frameKey: path.length === 0 ? "<root>" : path.join(" → "),
  }));
  const fromChildren: CollectedAnnotation[] = [];
  for (const [callId, child] of Object.entries(slice.expansions)) {
    fromChildren.push(...collectAnnotations(child, [...path, `${callId}@${child.choice}`]));
  }
  return [...here, ...fromChildren];
}

function SymbolPicker({ onPick }: { onPick: (name: string) => void }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    if (!query.trim()) {
      setResults([]);
      return;
    }
    setBusy(true);
    const handle = setTimeout(() => {
      search(query, 25)
        .then((r) => {
          if (!alive) return;
          setResults(r);
          setBusy(false);
        })
        .catch(() => {
          if (alive) setBusy(false);
        });
    }, 120);
    return () => {
      alive = false;
      clearTimeout(handle);
    };
  }, [query]);

  return (
    <div className="picker">
      <input
        type="text"
        placeholder="search functions… (e.g. main, Validate, Indexer.Load)"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && results[0]) onPick(results[0].targetId);
        }}
        autoFocus
      />
      {busy && <span className="picker-busy">…</span>}
      {results.length > 0 && (
        <ul className="picker-results">
          {results.map((r) => (
            <li key={r.targetId}>
              <button onClick={() => onPick(r.targetId)} className="picker-pick">
                <span className="picker-label">{r.label}</span>
                <span className="picker-loc">
                  {r.file.split("/").slice(-2).join("/")}:{r.line}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
