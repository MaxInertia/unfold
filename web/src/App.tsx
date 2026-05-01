import { useEffect, useState } from "react";
import { Frame } from "./Frame";
import { fetchSymbol, search } from "./api";
import type { Frame as FrameT, SearchResult } from "./types";

export function App() {
  const [target, setTarget] = useState<string | null>(null);
  const [rootFrame, setRootFrame] = useState<FrameT | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  // Load symbol on mount or when the URL hash changes (?symbol=...).
  useEffect(() => {
    function load() {
      const params = new URLSearchParams(location.hash.slice(1));
      const sym = params.get("symbol");
      if (!sym) {
        setRootFrame(null);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      fetchSymbol(sym)
        .then((f) => {
          setRootFrame(f);
          setLoading(false);
        })
        .catch((e: Error) => {
          setError(e.message);
          setLoading(false);
        });
    }
    load();
    window.addEventListener("hashchange", load);
    return () => window.removeEventListener("hashchange", load);
  }, []);

  // Read /api/health on mount to show the indexer target.
  useEffect(() => {
    fetch("/api/health")
      .then((r) => r.json())
      .then((h) => setTarget(h.target ?? null))
      .catch(() => {});
  }, []);

  function pickSymbol(name: string) {
    const params = new URLSearchParams();
    params.set("symbol", name);
    location.hash = params.toString();
  }

  return (
    <div className="app">
      <header className="app-header">
        <h1>unfold</h1>
        {target && <span className="app-target">target: <code>{target}</code></span>}
      </header>
      <SymbolPicker onPick={pickSymbol} />
      {error && <div className="app-error">{error}</div>}
      {loading && <div className="app-loading">loading…</div>}
      {rootFrame && (
        <div className="app-root-frame">
          <Frame frame={rootFrame} />
        </div>
      )}
      {!rootFrame && !loading && !error && (
        <p className="app-hint">
          Search for a function above and select one to start. The viewer expands direct
          call sites inline. Interface and indirect calls are visible but not expandable
          yet (Phase 2).
        </p>
      )}
    </div>
  );
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
