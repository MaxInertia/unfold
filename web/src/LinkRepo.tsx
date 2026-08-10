import { useState } from "react";
import { DirPicker } from "./DirPicker";
import { linkRepo } from "./api";

// Open another repository mid-session.
//
// You find out while reading that the call you're following lands in a repo
// you didn't open, and the only answer used to be quitting and relaunching
// with --workspace pointed somewhere that happened to contain both. A linked
// repo need not be a sibling of anything.
//
// Nothing is returned to render: the server rebuilds the engine and pushes a
// reload over /api/events, which every view already listens for. The link
// also persists, so it survives a restart rather than being something you
// redo each morning.
export function LinkRepo({ label = "+ link repo…" }: { label?: string }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function link(path: string) {
    setBusy(true);
    setError(null);
    try {
      await linkRepo(path);
      setOpen(false);
    } catch (e) {
      // Picking a folder that isn't a Go module, or one already open, is a
      // normal mis-click. Stay open so the next attempt starts from here.
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        className="proto-open link-repo-open"
        onClick={() => setOpen(true)}
        title="open another repository so calls into it can be followed — persists across restarts"
      >
        {label}
      </button>
    );
  }

  return (
    <DirPicker
      label="repository to open"
      placeholder="~/src/orders"
      action="open this repository"
      // "adding…", not "indexing…": the request returns as soon as the repo
      // is part of the workspace, and the index is built behind it. The
      // services button carries that wait, where it can be watched instead of
      // held open in a dialog whose question has already been answered.
      busyLabel="adding…"
      error={error}
      busy={busy}
      onSubmit={(p) => void link(p)}
      onCancel={() => setOpen(false)}
    />
  );
}
