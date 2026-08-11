import { useState } from "react";
import { DirPicker } from "./DirPicker";
import { setProtoRoot } from "./api";

// Picks the shared proto repository that a microservice.yaml's protoPaths
// resolve against. The browsing itself lives in DirPicker, which repo linking
// shares — the only thing specific to this one is what a bad choice means.
export function ProtoRootPicker({
  current,
  onChanged,
}: {
  current?: string;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function save(path: string) {
    setSaving(true);
    setError(null);
    try {
      await setProtoRoot(path);
      setOpen(false);
      onChanged();
    } catch (e) {
      // A root that doesn't resolve the declared protos is a normal outcome
      // of picking the wrong folder — report it and stay open so the next
      // attempt starts from where they were.
      setError((e as Error).message);
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
    <DirPicker
      label="shared proto repository"
      placeholder="~/src/platform-protos"
      initial={current}
      action="use this directory"
      busyLabel="checking…"
      error={error}
      busy={saving}
      onSubmit={(p) => void save(p)}
      onCancel={() => setOpen(false)}
    />
  );
}
