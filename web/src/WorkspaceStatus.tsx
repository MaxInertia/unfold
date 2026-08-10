import { useEffect, useState } from "react";
import { fetchRepos, indexRepo } from "./api";
import { useReloadRevision, useReposRevision } from "./reload";
import type { RepoInfo } from "./types";

// What is open, and what it is doing.
//
// Indexing is the one thing unfold does that takes long enough to wonder
// about, and until now it happened entirely off screen: a service was either
// there or it wasn't, and a reader waiting for one had nothing to look at. The
// button carries that state — it spins while any repository is being read —
// and opens the list of every service with its own state beside it.
//
// It re-reads on the server's say-so rather than polling: the workspace fires
// an event when a repository starts or finishes, which is exactly when this is
// wrong.
export function WorkspaceStatus() {
  const [repos, setRepos] = useState<RepoInfo[] | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const reposRevision = useReposRevision();
  const revision = useReloadRevision();

  useEffect(() => {
    let alive = true;
    fetchRepos()
      .then((rs) => {
        if (!alive) return;
        setRepos(rs);
        setError(null);
      })
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
    // Both streams matter: a repo's own state changes on the first, and a
    // reload rebuilds the whole set (linking a repo, saving a rule).
  }, [reposRevision, revision]);

  // A single-repo session has no workspace to describe, and an indicator that
  // says "1 of 1" forever is furniture.
  if (!repos || repos.length <= 1) return null;

  const working = repos.some((r) => r.indexing);
  const ready = repos.filter((r) => r.indexed).length;
  const failed = repos.filter((r) => r.error).length;

  async function readNow(alias: string) {
    setBusy(alias);
    setError(null);
    try {
      await indexRepo(alias);
      setRepos(await fetchRepos());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="ws-status">
      <button
        type="button"
        className={`icon-button ws-status-button${working ? " ws-status-button--working" : ""}`}
        onClick={() => setOpen((v) => !v)}
        // Still a button while it spins: the whole reason to show progress is
        // that someone is waiting on it, and they should be able to see what
        // for.
        aria-label={working ? "indexing — show services" : "show services"}
        aria-expanded={open}
        title={
          working
            ? `indexing… ${ready} of ${repos.length} services ready`
            : `${ready} of ${repos.length} services indexed`
        }
      >
        {working ? <span className="spinner" aria-hidden="true" /> : "◫"}
        {failed > 0 && !working && <span className="ws-status-dot" aria-hidden="true" />}
      </button>
      {open && (
        <div className="ws-panel">
          <div className="ws-panel-head">
            <span className="ws-panel-title">services</span>
            <span className="ws-panel-count">
              {ready} of {repos.length} indexed
            </span>
          </div>
          {error && <div className="ws-panel-error">{error}</div>}
          <ul className="ws-list">
            {repos.map((r) => (
              <li key={r.alias} className="ws-item">
                <span className="ws-item-state">
                  {r.indexing ? (
                    <span className="spinner spinner--small" aria-label="indexing" />
                  ) : r.error ? (
                    <span className="ws-glyph ws-glyph--error" aria-label="failed">
                      ✕
                    </span>
                  ) : r.indexed ? (
                    <span className="ws-glyph ws-glyph--ready" aria-label="indexed">
                      ●
                    </span>
                  ) : (
                    <span className="ws-glyph ws-glyph--idle" aria-label="not indexed">
                      ○
                    </span>
                  )}
                </span>
                <span className="ws-item-name">
                  {r.name}
                  {r.primary && <span className="ws-item-primary">primary</span>}
                </span>
                {/* Only the resting state offers the work: a repo mid-read is
                    already doing it, and one that failed would fail again. */}
                {!r.indexed && !r.indexing && !r.error && (
                  <button
                    type="button"
                    className="ws-item-action"
                    disabled={busy === r.alias}
                    onClick={() => void readNow(r.alias)}
                  >
                    {busy === r.alias ? "…" : "index"}
                  </button>
                )}
                {r.error && (
                  <span className="ws-item-error" title={r.error}>
                    {r.error}
                  </span>
                )}
              </li>
            ))}
          </ul>
          <p className="ws-panel-note">
            A service that isn’t indexed is still named everywhere — it just has
            no code to open yet.
          </p>
        </div>
      )}
    </div>
  );
}
