import { useCallFrame } from "./frameCache";
import type { CallSite, Frame as FrameT } from "./types";
import {
  pathKey,
  useFrameSlice,
  useViewStore,
  type FramePath,
} from "./viewState";

// CallTree is the sidebar's "calls" tab body: it mirrors the inline-view
// expansion state. The top level lists the calls inside the focused (root)
// function; expanding a node unfolds its callee both here and in the main
// view (both read/write the same ViewStore), nesting callees beneath. The
// surrounding card / tab chrome lives in App.
export function CallTree({ rootFrame }: { rootFrame: FrameT }) {
  return (
    <ul className="tree-list tree-list--root">
      <RootNode rootFrame={rootFrame} />
    </ul>
  );
}

function RootNode({ rootFrame }: { rootFrame: FrameT }) {
  return (
    <li className="tree-node tree-node--root">
      <div
        className="tree-row tree-row--root"
        onClick={() => scrollToFrame([])}
        title="scroll to the focused function"
      >
        <span className="tree-twisty">▾</span>
        <span className="tree-label">{prettyFn(rootFrame.id)}</span>
      </div>
      <FrameChildren frame={rootFrame} path={[]} />
    </li>
  );
}

// FrameChildren renders a frame's call sites as the child nodes of the
// tree node that owns that frame.
function FrameChildren({ frame, path }: { frame: FrameT; path: FramePath }) {
  if (frame.calls.length === 0) {
    return (
      <ul className="tree-list">
        <li className="tree-note">no calls</li>
      </ul>
    );
  }
  return (
    <ul className="tree-list">
      {frame.calls.map((call) => (
        <CallNode key={call.id} call={call} parentPath={path} />
      ))}
    </ul>
  );
}

function CallNode({
  call,
  parentPath,
}: {
  call: CallSite;
  parentPath: FramePath;
}) {
  const store = useViewStore();
  const slice = useFrameSlice(parentPath);
  const expandable = isExpandable(call);
  const exp = slice.expansions[call.id];
  const isExpanded = !!exp;
  const choice = exp?.choice ?? 0;
  const childPath: FramePath = [...parentPath, { callId: call.id, choice }];

  function toggle(e: React.MouseEvent) {
    e.stopPropagation();
    if (!expandable) return;
    if (isExpanded) store.collapse(parentPath, call.id);
    else store.expand(parentPath, call.id, 0);
  }

  function onLabel(e: React.MouseEvent) {
    // When already open, a label click scrolls the inline frame into view
    // instead of collapsing it; otherwise fall through to toggle.
    if (isExpanded) {
      e.stopPropagation();
      scrollToFrame(childPath);
    }
  }

  const badge = kindBadge(call);
  return (
    <li className={`tree-node tree-node--${call.kind}`}>
      <div
        className={[
          "tree-row",
          expandable ? "tree-row--expandable" : "tree-row--leaf",
          isExpanded ? "tree-row--expanded" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        onClick={toggle}
        title={rowTitle(call)}
      >
        <span className="tree-twisty">
          {expandable ? (isExpanded ? "▾" : "▸") : "·"}
        </span>
        <span className="tree-label" onClick={onLabel}>
          {call.displayName || "(call)"}
        </span>
        {badge && <span className={`tree-kind tree-kind--${call.kind}`}>{badge}</span>}
      </div>
      {isExpanded && <ExpandedChild call={call} childPath={childPath} />}
    </li>
  );
}

function ExpandedChild({
  call,
  childPath,
}: {
  call: CallSite;
  childPath: FramePath;
}) {
  const choice = childPath[childPath.length - 1].choice;
  const { frame, loading, error } = useCallFrame(call.id, choice);
  if (loading) {
    return (
      <ul className="tree-list">
        <li className="tree-note">…</li>
      </ul>
    );
  }
  if (error) {
    return (
      <ul className="tree-list">
        <li className="tree-note tree-note--error">{error}</li>
      </ul>
    );
  }
  if (!frame) return null;
  return <FrameChildren frame={frame} path={childPath} />;
}

// --- helpers ---

function isExpandable(call: CallSite): boolean {
  if (call.kind === "indirect") return false;
  if (call.kind === "interface") return (call.candidates?.length ?? 0) > 0;
  if (call.kind === "direct") return !!call.targetId;
  return false;
}

function kindBadge(call: CallSite): string {
  if (call.kind === "interface") return "iface";
  if (call.kind === "indirect") return "indirect";
  if (call.kind === "direct" && !call.targetId) return "ext";
  return "";
}

function rowTitle(call: CallSite): string {
  if (call.kind === "interface") {
    const n = call.candidates?.length ?? 0;
    return n > 0
      ? `interface call — ${n} impl${n === 1 ? "" : "s"}`
      : "interface call — no known implementations";
  }
  if (call.kind === "indirect") return "indirect call — not expandable";
  if (call.kind === "direct" && !call.targetId)
    return "external call — source not in the loaded module";
  return String(call.targetId ?? call.displayName);
}

// Scroll the inline frame matching `path` into view and flash it. Inline
// frames are tagged with data-frame-key={pathKey(path)} in Frame.tsx.
function scrollToFrame(path: FramePath) {
  const key = pathKey(path);
  const el = document.querySelector(
    `[data-frame-key="${cssEscape(key)}"]`,
  ) as HTMLElement | null;
  if (!el) return;
  el.scrollIntoView({ behavior: "smooth", block: "start" });
  el.classList.add("frame--flash");
  window.setTimeout(() => el.classList.remove("frame--flash"), 900);
}

function cssEscape(s: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") {
    return CSS.escape(s);
  }
  return s.replace(/["\\]/g, "\\$&");
}

function prettyFn(id: string): string {
  const parts = id.split("/");
  if (parts.length <= 2) return id;
  return ".../" + parts.slice(-2).join("/");
}
