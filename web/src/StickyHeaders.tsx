import { useEffect, useState } from "react";

// StickyHeaders pins the call chain to the top of the viewport: as you
// scroll into a frame, a compact copy of its header sticks; nested frames
// stack theirs beneath it, so the pinned stack always reads as the live
// chain (root → … → the frame you're reading). Clicking a pinned header
// scrolls back to that frame.
//
// This is a JS overlay rather than CSS `position: sticky` because sticky
// constrains to the nearest scroll container, and frames sit inside
// `.frame { overflow: hidden }` / `.frame-body { overflow-x: auto }` —
// both scroll containers, so nested headers would pin to their parent
// frame's box instead of the viewport. The overlay reads geometry from
// the real DOM (frames carry data-frame-* attributes) and renders a
// fixed stack aligned to the content column.

// One pinned header: a frame whose own header has scrolled past the stack
// while its body still spans it.
interface Stuck {
  key: string; // data-frame-key, for jump-to-frame
  title: string;
  loc: string;
  depth: number; // nesting depth (index in the chain)
}

type Row =
  | { kind: "header"; stuck: Stuck }
  | { kind: "gap"; count: number };

// Must match the .sticky-header height in index.css.
const HEADER_H = 28;
// Beyond this many pinned headers the middle of the chain collapses into
// one "⋯ +N" row, so deep chains don't eat the viewport.
const MAX_VISIBLE = 6;

// Accent per depth, shared semantics with the (future) depth rails. One
// palette that holds up on both themes.
const DEPTH_COLORS = ["#2563eb", "#0d9488", "#d97706", "#9333ea", "#dc2626", "#65a30d"];

export function depthColor(depth: number): string {
  return DEPTH_COLORS[depth % DEPTH_COLORS.length];
}

export function StickyHeaders() {
  const [stack, setStack] = useState<Stuck[]>([]);
  const [box, setBox] = useState({ left: 0, width: 0 });

  useEffect(() => {
    let raf = 0;
    function recompute() {
      raf = 0;
      const rootFrame = document.querySelector(".app-root-frame > .frame");
      if (!rootFrame) {
        setStack((s) => (s.length ? [] : s));
        return;
      }
      const rootRect = rootFrame.getBoundingClientRect();
      setBox((b) =>
        b.left === rootRect.left && b.width === rootRect.width
          ? b
          : { left: rootRect.left, width: rootRect.width },
      );

      // Walk the containment chain from the root: a frame pins once its
      // header would scroll above the stack built so far, and unpins when
      // its bottom edge pushes it out. The next link is the (unique)
      // directly-nested frame spanning the new stack bottom.
      const chain: Stuck[] = [];
      let cur: Element | null = rootFrame;
      let y = 0;
      while (cur) {
        const r = cur.getBoundingClientRect();
        if (r.top >= y || r.bottom <= y + HEADER_H) break;
        chain.push({
          key: cur.getAttribute("data-frame-key") ?? "",
          title: cur.getAttribute("data-frame-title") ?? "?",
          loc: cur.getAttribute("data-frame-loc") ?? "",
          depth: chain.length,
        });
        y += HEADER_H;
        cur = childFrameAt(cur, y);
      }
      setStack((s) => (sameStack(s, chain) ? s : chain));
    }
    const schedule = () => {
      if (!raf) raf = requestAnimationFrame(recompute);
    };
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    // Expansions/collapses/panel toggles change frame geometry without a
    // scroll event — recompute on DOM changes too.
    const mo = new MutationObserver(schedule);
    mo.observe(document.body, { childList: true, subtree: true });
    recompute();
    return () => {
      window.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      mo.disconnect();
      if (raf) cancelAnimationFrame(raf);
    };
  }, []);

  if (stack.length === 0) return null;

  const rows: Row[] = [];
  if (stack.length <= MAX_VISIBLE) {
    for (const s of stack) rows.push({ kind: "header", stuck: s });
  } else {
    // Keep the outermost link (where am I overall) and the innermost ones
    // (the context right above the code being read); collapse the middle.
    rows.push({ kind: "header", stuck: stack[0] });
    rows.push({ kind: "gap", count: stack.length - MAX_VISIBLE });
    for (const s of stack.slice(-(MAX_VISIBLE - 1))) rows.push({ kind: "header", stuck: s });
  }

  function jumpTo(stuck: Stuck, rowIndex: number) {
    const el = document.querySelector(`[data-frame-key="${cssEscape(stuck.key)}"]`);
    if (!el) return;
    const r = el.getBoundingClientRect();
    // Land the frame's real header in the slot its pinned copy occupies.
    window.scrollBy({ top: r.top - rowIndex * HEADER_H, behavior: "smooth" });
    el.classList.add("frame--flash");
    window.setTimeout(() => el.classList.remove("frame--flash"), 900);
  }

  return (
    <div className="sticky-stack" style={{ left: box.left, width: box.width }}>
      {rows.map((row, i) =>
        row.kind === "gap" ? (
          <div key="gap" className="sticky-header sticky-header--gap">
            ⋯ {row.count} more
          </div>
        ) : (
          <button
            key={`${row.stuck.depth}:${row.stuck.key}`}
            type="button"
            className="sticky-header"
            onClick={() => jumpTo(row.stuck, i)}
            title={`jump back to ${row.stuck.title}`}
          >
            <span
              className="sticky-rail"
              style={{ background: depthColor(row.stuck.depth) }}
            />
            <span className="sticky-title">{row.stuck.title}</span>
            <span className="sticky-loc">{row.stuck.loc}</span>
          </button>
        ),
      )}
    </div>
  );
}

// The directly-nested frame (one level down, not any descendant) whose box
// spans viewport offset y. At most one frame can span y at each level.
function childFrameAt(parent: Element, y: number): Element | null {
  for (const f of parent.querySelectorAll(".frame")) {
    if (f.parentElement?.closest(".frame") !== parent) continue;
    const r = f.getBoundingClientRect();
    if (r.top < y && r.bottom > y) return f;
  }
  return null;
}

function sameStack(a: Stuck[], b: Stuck[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((s, i) => s.key === b[i].key && s.title === b[i].title);
}

function cssEscape(s: string): string {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") {
    return CSS.escape(s);
  }
  return s.replace(/["\\]/g, "\\$&");
}
