import { useEffect, useState } from "react";
import { linkRepo } from "./api";
import { DirPicker } from "./DirPicker";

// Adding a repository to the workspace, from wherever the thought occurs.
//
// It is asked in three places — the service view, the platform view, and the
// list of what's open — and it is one question, so it is one modal rather than
// a picker that unfolds inside whichever surface happened to ask. That also
// keeps it out of the panel it is launched from: a directory browser nested in
// a dropdown has nowhere to grow.
//
// The dialog closes as soon as the repo is linked. Indexing carries on behind
// it and is watched from the workspace button, because the question here —
// which repository? — has already been answered by then.
export function AddRepoModal({ onClose }: { onClose: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Escape closes, like every other dismissible surface here.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function link(path: string) {
    setBusy(true);
    setError(null);
    try {
      await linkRepo(path);
      onClose();
    } catch (e) {
      // Picking a folder that isn't a Go module, or one already open, is a
      // normal mis-click. Stay open so the next attempt starts from here.
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-label="add a repository">
        <DirPicker
          label="repository to add"
          placeholder="~/src/orders"
          action="add to workspace"
          // "adding", not "indexing": the request returns as soon as the repo
          // is part of the workspace, and the index is built behind it.
          busyLabel="adding…"
          error={error}
          busy={busy}
          onSubmit={(p) => void link(p)}
          onCancel={onClose}
        />
      </div>
    </div>
  );
}

// The button that opens it, for the surfaces that offer adding a repo inline.
export function AddRepoButton({ label = "+ add repository" }: { label?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        className="proto-open link-repo-open"
        onClick={() => setOpen(true)}
        title="add another repository to this workspace so calls into it can be followed — persists across restarts"
      >
        {label}
      </button>
      {open && <AddRepoModal onClose={() => setOpen(false)} />}
    </>
  );
}
