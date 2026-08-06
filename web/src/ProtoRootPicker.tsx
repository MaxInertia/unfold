import { useEffect, useState } from "react";
import { browseDirs, setProtoRoot } from "./api";

// Picks the shared proto repository that a microservice.yaml's protoPaths
// resolve against.
//
// It's a server-backed browser rather than a native directory picker because
// a browser deliberately won't hand a page a real filesystem path — a
// `<input webkitdirectory>` yields file contents, and the File System Access
// API yields an opaque handle. Neither is something the Go side can resolve
// protos against. So the server lists directories and the UI posts back the
// path that was chosen.
export function ProtoRootPicker({
  current,
  onChanged,
}: {
  current?: string;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  // The typed path is the source of truth: browsing writes into it, and it
  // can also just be pasted, which is faster when you know where you're going.
  const [path, setPath] = useState(current ?? "");
  const [listing, setListing] = useState<{ path: string; parent?: string; dirs: string[] } | null>(
    null,
  );
  const [browseError, setBrowseError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Browse whatever the path box currently holds. Debounced so typing a path
  // by hand doesn't fire a request per keystroke.
  useEffect(() => {
    if (!open) return;
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
  }, [open, path]);

  async function save() {
    setSaving(true);
    setSaveError(null);
    try {
      await setProtoRoot(path);
      setOpen(false);
      onChanged();
    } catch (e) {
      // A root that doesn't resolve the declared protos is a normal outcome
      // of picking the wrong folder — report it and stay open so the next
      // attempt starts from where they were.
      setSaveError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  if (!open) {
    return (
      <button type="button" className="proto-open" onClick={() => setOpen(true)}>
        {current ? "change proto root…" : "select proto root…"}
      </button>
    );
  }

  return (
    <div className="proto-picker">
      <label className="proto-field">
        <span className="proto-label">shared proto repository</span>
        <input
          type="text"
          value={path}
          placeholder="~/src/platform-protos"
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void save();
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

      {saveError && <p className="proto-error">{saveError}</p>}

      <div className="proto-actions">
        <button type="button" className="proto-use" onClick={() => void save()} disabled={saving}>
          {saving ? "checking…" : "use this directory"}
        </button>
        <button type="button" className="proto-cancel" onClick={() => setOpen(false)}>
          cancel
        </button>
      </div>
    </div>
  );
}
