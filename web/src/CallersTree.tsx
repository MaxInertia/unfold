import { useEffect, useState } from "react";
import { fetchUsages } from "./api";
import { kindLabel, spliceAbove } from "./Callers";
import type { Frame as FrameT, TargetID, Usage } from "./types";
import { useViewStore } from "./viewState";

// CallersTree is the sidebar's "callers" tab: an inverted call tree rooted
// at the focused symbol. Each level lists the places the level above is
// referenced; expanding a node fetches *its* callers, walking toward entry
// points. Clicking a node's label loads the whole chain as one pre-unfolded
// view — the clicked caller becomes the root and every link down to the
// focused symbol is spliced in at its call site, with the focused symbol
// keeping its current expansion subtree.
export function CallersTree({ rootFrame }: { rootFrame: FrameT }) {
  return (
    <ul className="tree-list tree-list--root">
      <li className="tree-node tree-node--root">
        <div
          className="tree-row tree-row--root"
          title="the focused function — its callers nest below"
        >
          <span className="tree-twisty">▾</span>
          <span className="tree-label">{frameLabel(rootFrame)}</span>
        </div>
        <CallerChildren target={rootFrame.id} chain={[]} />
      </li>
    </ul>
  );
}

// CallerChildren lists the usages of `target`. `chain` is the usage path
// from the focused symbol up to (and including) the usage that produced
// `target` — what openChain needs to rebuild the view.
function CallerChildren({ target, chain }: { target: TargetID; chain: Usage[] }) {
  const [usages, setUsages] = useState<Usage[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setUsages(null);
    setError(null);
    fetchUsages(target)
      .then((u) => {
        if (alive) setUsages(u);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      });
    return () => {
      alive = false;
    };
  }, [target]);

  if (error) {
    return (
      <ul className="tree-list">
        <li className="tree-note tree-note--error">{error}</li>
      </ul>
    );
  }
  if (usages === null) {
    return (
      <ul className="tree-list">
        <li className="tree-note">…</li>
      </ul>
    );
  }
  if (usages.length === 0) {
    return (
      <ul className="tree-list">
        <li className="tree-note">no callers</li>
      </ul>
    );
  }
  return (
    <ul className="tree-list">
      {usages.map((u, i) => (
        <CallerNode key={`${u.file}:${u.line}:${i}`} usage={u} chain={chain} />
      ))}
    </ul>
  );
}

function CallerNode({ usage, chain }: { usage: Usage; chain: Usage[] }) {
  const store = useViewStore();
  const [open, setOpen] = useState(false);
  const nextChain = [...chain, usage];

  // Load the chain pre-unfolded: innermost is the focused symbol's current
  // subtree, then each chain link wraps it at its call site.
  function openChain(e: React.MouseEvent) {
    e.stopPropagation();
    let tree = store.getSlice([]);
    for (const u of nextChain) tree = spliceAbove(u, tree);
    store.setView(usage.caller, tree);
  }

  return (
    <li className={`tree-node tree-node--${usage.kind}`}>
      <div
        className="tree-row tree-row--expandable"
        onClick={() => setOpen((v) => !v)}
        title={`${usage.file}:${usage.line} — click the name to load this chain as the view`}
      >
        <span className="tree-twisty">{open ? "▾" : "▸"}</span>
        <span className="tree-label" onClick={openChain}>
          {usage.callerTitle}
        </span>
        {usage.kind !== "call" && (
          <span className={`tree-kind tree-kind--${usage.kind}`}>
            {kindLabel(usage.kind)}
          </span>
        )}
      </div>
      {open && <CallerChildren target={usage.caller} chain={nextChain} />}
    </li>
  );
}

function frameLabel(f: FrameT): string {
  if (f.title && f.title.trim()) return f.title;
  const parts = f.id.split("/");
  if (parts.length <= 2) return f.id;
  return ".../" + parts.slice(-2).join("/");
}
