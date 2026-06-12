import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { fetchFiles, fetchTypeInfo, search } from "./api";
import { removeNote, upsertNote, useNotes } from "./notes";
import type { Note, NoteAnchor, TargetID, TypeInfo } from "./types";
import { useViewStore } from "./viewState";

// ---- reference resolution ----
//
// Note text may contain [[SymbolName]] and [[file:path]] references.
// Symbol names resolve through /api/search (preferring exact basename
// matches); file paths resolve against the indexed file list by suffix.
// Resolutions are cached per session — a reindex changes target ids, but
// notes re-resolve on reload, which is the same staleness contract the
// rest of the UI has.

const symbolCache = new Map<string, Promise<{ targetId: TargetID; label: string } | null>>();
let filesCache: Promise<string[]> | null = null;

function resolveSymbol(name: string): Promise<{ targetId: TargetID; label: string } | null> {
  let p = symbolCache.get(name);
  if (!p) {
    p = search(name, 25)
      .then((results) => {
        if (results.length === 0) return null;
        const exact = results.find((r) => {
          const base = r.label.includes(".") ? r.label.slice(r.label.lastIndexOf(".") + 1) : r.label;
          return r.label === name || base === name || String(r.targetId).endsWith(name);
        });
        const hit = exact ?? results[0];
        return { targetId: hit.targetId, label: hit.label };
      })
      .catch(() => null);
    symbolCache.set(name, p);
  }
  return p;
}

function resolveFile(path: string): Promise<string | null> {
  if (!filesCache) filesCache = fetchFiles().catch(() => []);
  return filesCache.then((files) => {
    const clean = path.replace(/^\.\//, "");
    return files.find((f) => f === clean || f.endsWith("/" + clean)) ?? null;
  });
}

// ---- note text rendering ----

// NoteText renders a note body, turning [[...]] tokens into live
// references styled like the code view.
export function NoteText({ text }: { text: string }) {
  const parts = text.split(/(\[\[[^\]]+\]\])/g);
  return (
    <span className="note-text">
      {parts.map((p, i) => {
        const m = p.match(/^\[\[([^\]]+)\]\]$/);
        if (!m) return <span key={i}>{p}</span>;
        const ref = m[1].trim();
        return ref.startsWith("file:") ? (
          <FileRef key={i} path={ref.slice(5).trim()} />
        ) : (
          <SymbolRef key={i} name={ref} />
        );
      })}
    </span>
  );
}

// SymbolRef: a method/function reference. Same pill styling as a call
// site, same hover behavior (the type card, via typeinfo's describe-the-
// declaration mode), click opens it as the root frame.
function SymbolRef({ name }: { name: string }) {
  const store = useViewStore();
  const [resolved, setResolved] = useState<{ targetId: TargetID; label: string } | null | "pending">("pending");
  const [card, setCard] = useState<{ x: number; y: number; info: TypeInfo } | null>(null);
  const hoverTimer = useRef(0);

  useEffect(() => {
    let alive = true;
    resolveSymbol(name).then((r) => {
      if (alive) setResolved(r);
    });
    return () => {
      alive = false;
    };
  }, [name]);

  if (resolved === "pending") return <span className="note-ref note-ref--pending">{name}</span>;
  if (!resolved) {
    return (
      <span className="note-ref note-ref--unresolved" title="no indexed symbol matches this name">
        {name}
      </span>
    );
  }

  function onEnter(e: React.MouseEvent) {
    const x = e.clientX;
    const y = e.clientY;
    window.clearTimeout(hoverTimer.current);
    hoverTimer.current = window.setTimeout(() => {
      if (!resolved || resolved === "pending") return;
      fetchTypeInfo(resolved.targetId, -1)
        .then((info) => setCard(info ? { x, y, info } : null))
        .catch(() => setCard(null));
    }, 250);
  }

  function onLeave() {
    window.clearTimeout(hoverTimer.current);
    setCard(null);
  }

  return (
    <span
      className="call-site call-site--direct call-site--resolvable note-ref"
      onClick={() => store.setSymbol(resolved.targetId)}
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      title="open as root frame"
    >
      {name}
      {card &&
        // Portal to <body>: the ref sits inside nested stacking contexts
        // (note card, line rows), so a fixed-position child would get
        // painted over by later code lines.
        createPortal(
          <span
            className="type-card note-ref-card"
            style={{ left: card.x + 12, top: card.y + 16 }}
          >
            <span className="type-card-head">
              <span className="type-card-kind">{card.info.kind}</span>
              <span className="type-card-name">{card.info.name}</span>
            </span>
            {card.info.type && <span className="type-card-type">{card.info.type}</span>}
            {card.info.doc && <span className="type-card-doc">{card.info.doc}</span>}
            {card.info.definedAt && (
              <span className="type-card-loc-text">{shortDefined(card.info.definedAt)}</span>
            )}
          </span>,
          document.body,
        )}
    </span>
  );
}

// FileRef: opens the referenced file as a whole-file frame.
function FileRef({ path }: { path: string }) {
  const store = useViewStore();
  const [abs, setAbs] = useState<string | null | "pending">("pending");

  useEffect(() => {
    let alive = true;
    resolveFile(path).then((r) => {
      if (alive) setAbs(r);
    });
    return () => {
      alive = false;
    };
  }, [path]);

  if (abs === "pending") return <span className="note-ref note-ref--pending">{path}</span>;
  if (!abs) {
    return (
      <span className="note-ref note-ref--unresolved" title="no indexed file matches this path">
        {path}
      </span>
    );
  }
  return (
    <span
      className="call-site call-site--direct call-site--resolvable note-ref note-ref--file"
      onClick={() => store.setSymbol(`file:${abs}`)}
      title={`open ${abs}`}
    >
      {path}
    </span>
  );
}

// ---- note card + composer ----

export function NoteCard({ note, drifted }: { note: Note; drifted?: boolean }) {
  const [editing, setEditing] = useState(false);
  if (editing) {
    return (
      <NoteComposer
        anchor={note.anchor}
        initial={note}
        onDone={() => setEditing(false)}
      />
    );
  }
  return (
    <div className="note">
      <div className="note-head">
        <span className="note-label">note</span>
        {drifted && (
          <span
            className="note-drifted"
            title="the anchored line's text changed since this note was saved — it may point at the wrong place"
          >
            ⚠ drifted
          </span>
        )}
        <span className="note-when">{(note.updatedAt ?? "").slice(0, 10)}</span>
        <button
          type="button"
          className="note-action"
          onClick={() => setEditing(true)}
          title="edit note"
          aria-label="edit note"
        >
          ✎
        </button>
        <button
          type="button"
          className="note-action"
          onClick={() => removeNote(note.id).catch(() => {})}
          title="delete note"
          aria-label="delete note"
        >
          ×
        </button>
      </div>
      <div className="note-body">
        <NoteText text={note.text} />
      </div>
    </div>
  );
}

export function NoteComposer({
  anchor,
  initial,
  onDone,
}: {
  anchor: NoteAnchor;
  initial?: Note;
  onDone: () => void;
}) {
  const [text, setText] = useState(initial?.text ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function save() {
    if (!text.trim()) {
      onDone();
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await upsertNote({ id: initial?.id, anchor, text });
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <div className="note note--composing">
      <textarea
        className="note-input"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="note… reference code with [[SymbolName]] or [[file:path]]"
        rows={3}
        autoFocus
        onKeyDown={(e) => {
          if (e.key === "Escape") onDone();
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) save();
        }}
      />
      {error && <div className="note-error">save failed: {error}</div>}
      <div className="note-compose-bar">
        <button type="button" className="note-save" onClick={save} disabled={busy}>
          save
        </button>
        <button type="button" className="note-cancel" onClick={onDone}>
          cancel
        </button>
        <span className="note-hint">⌘/ctrl-enter to save · esc to cancel</span>
      </div>
    </div>
  );
}

// ---- sidebar list ----

// NotesList is the sidebar's "notes" tab: every note in the project,
// grouped by file order, jump-to on click (opens the whole-file frame).
export function NotesList() {
  const notes = useNotes();
  const store = useViewStore();
  if (notes.length === 0) {
    return <p className="tree-placeholder">No notes yet — select a line and hit "note".</p>;
  }
  const sorted = [...notes].sort((a, b) =>
    a.anchor.file !== b.anchor.file
      ? a.anchor.file < b.anchor.file
        ? -1
        : 1
      : (a.anchor.startLine ?? 0) - (b.anchor.startLine ?? 0),
  );
  return (
    <ul className="notes-list">
      {sorted.map((n) => (
        <li key={n.id} className="notes-item">
          <button
            type="button"
            className="notes-item-open"
            onClick={() => store.setSymbol(`file:${n.anchor.file}`)}
            title={`open ${n.anchor.file}`}
          >
            <span className="notes-item-loc">
              {shortPath(n.anchor.file)}
              {anchorLabel(n.anchor)}
            </span>
            <span className="notes-item-text">{firstLine(n.text)}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function anchorLabel(a: NoteAnchor): string {
  if (a.kind === "file-start") return " · top";
  if (a.kind === "file-end") return " · end";
  if (a.kind === "range") return `:${a.startLine}–${a.endLine}`;
  return `:${a.startLine}`;
}

function firstLine(text: string): string {
  const line = text.split("\n")[0];
  return line.length > 80 ? line.slice(0, 77) + "…" : line;
}

function shortPath(p: string): string {
  const parts = p.split("/");
  return parts.slice(-2).join("/");
}

function shortDefined(at: string): string {
  const i = at.lastIndexOf(":");
  const file = i > 0 ? at.slice(0, i) : at;
  const line = i > 0 ? at.slice(i + 1) : "";
  const base = file.slice(file.lastIndexOf("/") + 1);
  return line ? `${base}:${line}` : base;
}
