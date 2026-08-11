import { useEffect, useState } from "react";
import { browseDirs } from "./api";

// A server-backed directory browser.
//
// It's server-backed rather than a native picker because a browser
// deliberately won't hand a page a real filesystem path — `<input
// webkitdirectory>` yields file contents, and the File System Access API
// yields an opaque handle. Neither is something the Go side can resolve a
// proto root or open a repository against. So the server lists directories
// and the UI posts back the path that was chosen.
//
// Shared by the proto-root picker and repo linking: both ask the same
// question ("which directory?") and differ only in what they do with the
// answer and what counts as a failure.
export function DirPicker({
  label,
  placeholder,
  initial,
  action,
  busyLabel,
  error,
  busy,
  onSubmit,
  onCancel,
}: {
  label: string;
  placeholder: string;
  initial?: string;
  action: string;
  busyLabel: string;
  // Owned by the caller, because what makes a directory unacceptable is the
  // caller's question — a root that resolves no protos, a folder that isn't a
  // module — and the picker stays open so the next attempt starts from where
  // they were rather than at square one.
  error?: string | null;
  busy?: boolean;
  onSubmit: (path: string) => void;
  onCancel: () => void;
}) {
  // The typed path is the source of truth: browsing writes into it, and it
  // can also just be pasted, which is faster when you know where you're going.
  const [path, setPath] = useState(initial ?? "");
  const [listing, setListing] = useState<{ path: string; parent?: string; dirs: string[] } | null>(
    null,
  );
  const [browseError, setBrowseError] = useState<string | null>(null);

  // Debounced so typing a path by hand doesn't fire a request per keystroke.
  useEffect(() => {
    let alive = true;
    const t = setTimeout(() => {
      browseDirs(path)
        .then((l) => {
          if (!alive) return;
          setListing(l);
          setBrowseError(null);
        })
        .catch((e: Error) => alive && setBrowseError(e.message));
    }, 200);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [path]);

  return (
    <div className="proto-picker">
      <label className="proto-field">
        <span className="proto-label">{label}</span>
        <input
          type="text"
          value={path}
          placeholder={placeholder}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onSubmit(path);
          }}
          autoFocus
          spellCheck={false}
        />
      </label>

      {browseError ? (
        <p className="proto-error">{browseError}</p>
      ) : (
        listing && (
          <>
            <div className="proto-cwd" title={listing.path}>
              {listing.path}
            </div>
            <ul className="proto-dirs">
              {listing.parent && (
                <li>
                  <button type="button" onClick={() => setPath(listing.parent!)}>
                    ../
                  </button>
                </li>
              )}
              {listing.dirs.map((d) => (
                <li key={d}>
                  <button
                    type="button"
                    onClick={() => setPath(`${listing.path.replace(/\/$/, "")}/${d}`)}
                  >
                    {d}/
                  </button>
                </li>
              ))}
              {listing.dirs.length === 0 && !listing.parent && (
                <li className="proto-empty">no subdirectories</li>
              )}
            </ul>
          </>
        )
      )}

      {error && <p className="proto-error">{error}</p>}

      <div className="proto-actions">
        <button
          type="button"
          className="proto-use"
          onClick={() => onSubmit(path)}
          disabled={busy}
        >
          {busy ? busyLabel : action}
        </button>
        <button type="button" className="proto-cancel" onClick={onCancel}>
          cancel
        </button>
      </div>
    </div>
  );
}
