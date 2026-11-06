import { useEffect, useRef, useState } from "react";
import { fetchEffects } from "./api";
import {
  evidenceLabel,
  hopLabel,
  shortLoc,
  summarize,
  type Badge,
} from "./effects";
import { useReloadRevision } from "./reload";
import { useSettings } from "./settings";
import type { CallID, EffectReport, Frame as FrameT } from "./types";

// EffectBadges is the row of side-effect badges in a frame header: what the
// function touches outside its own stack — database, network, files,
// environment, globals, time, processes, goroutines, channels — directly or
// through what it calls. A badge with no direct evidence is drawn muted:
// "something this calls sleeps" is a weaker claim than "this sleeps".
//
// Hovering a badge lists the evidence; clicking pins the list. Each row jumps
// to the line that does it when the evidence is in this frame, and opens the
// callee it came through when it isn't.
//
// Self-contained on purpose: the header is shared ground with other features,
// and one component with three props is the smallest footprint to merge.

interface Props {
  frame: FrameT;
  // Opens one of the frame's own call sites as a nested frame.
  onOpenCall: (callId: CallID) => void;
  // Scrolls to an absolute file line inside this frame.
  onJumpToLine: (line: number) => void;
}

// One in-flight or settled fetch per frame id, shared across the frames that
// show the same function — a popular helper can be open in several places —
// and dropped wholesale when the index rebuilds.
const cache = new Map<string, Promise<EffectReport | null>>();
let cacheRevision = -1;

function load(id: string, revision: number): Promise<EffectReport | null> {
  if (revision !== cacheRevision) {
    cache.clear();
    cacheRevision = revision;
  }
  let p = cache.get(id);
  if (!p) {
    // A 501 (an engine with no effects) or a 404 is "nothing to show", not
    // an error to surface: the badges simply aren't there.
    p = fetchEffects(id).catch(() => null);
    cache.set(id, p);
  }
  return p;
}

export function EffectBadges({ frame, onOpenCall, onJumpToLine }: Props) {
  const settings = useSettings();
  const revision = useReloadRevision();
  const [report, setReport] = useState<EffectReport | null>(null);
  // open is the kind whose evidence is showing; pinned keeps it open after
  // the pointer leaves, so a row can be clicked without a steady hand.
  const [open, setOpen] = useState<string | null>(null);
  const [pinned, setPinned] = useState(false);
  const closeTimer = useRef(0);
  const isFile = frame.id.startsWith("file:");
  const enabled = settings.effectBadges && !isFile;

  useEffect(() => {
    if (!enabled) return;
    let live = true;
    load(frame.id, revision).then((r) => {
      if (live) setReport(r);
    });
    return () => {
      live = false;
    };
  }, [frame.id, revision, enabled]);

  useEffect(() => () => window.clearTimeout(closeTimer.current), []);

  if (!enabled || !report || report.effects.length === 0) return null;
  const badges = summarize(report.effects);
  const current = open ? badges.find((b) => b.kind === open) : undefined;

  function show(kind: string) {
    window.clearTimeout(closeTimer.current);
    if (!pinned) setOpen(kind);
  }
  function hide() {
    if (pinned) return;
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setOpen(null), 180);
  }
  function toggle(kind: string) {
    if (pinned && open === kind) {
      setPinned(false);
      setOpen(null);
    } else {
      setPinned(true);
      setOpen(kind);
    }
  }

  return (
    <span
      className="effect-badges"
      onMouseLeave={hide}
      onMouseEnter={() => window.clearTimeout(closeTimer.current)}
    >
      {badges.map((b) => (
        <button
          key={b.kind}
          type="button"
          className={[
            "effect-badge",
            `effect-badge--${b.kind}`,
            b.direct === 0 ? "effect-badge--inherited" : "",
            open === b.kind ? "effect-badge--open" : "",
          ]
            .filter(Boolean)
            .join(" ")}
          title={b.title}
          aria-label={b.title}
          aria-expanded={open === b.kind}
          onMouseEnter={() => show(b.kind)}
          onFocus={() => show(b.kind)}
          onClick={(e) => {
            e.stopPropagation();
            toggle(b.kind);
          }}
        >
          <span className="effect-icon" aria-hidden="true">
            {b.icon}
          </span>
          {b.label}
        </button>
      ))}
      {report.truncated && (
        <span
          className="effect-truncated"
          title="the walk through callees stopped at its depth or budget cap, so a missing badge means 'not seen', not 'not there'"
        >
          …
        </span>
      )}
      {current && (
        <EvidenceList
          badge={current}
          frame={frame}
          pinned={pinned}
          onOpenCall={onOpenCall}
          onJumpToLine={onJumpToLine}
          onClose={() => {
            setPinned(false);
            setOpen(null);
          }}
        />
      )}
    </span>
  );
}

function EvidenceList({
  badge,
  frame,
  pinned,
  onOpenCall,
  onJumpToLine,
  onClose,
}: {
  badge: Badge;
  frame: FrameT;
  pinned: boolean;
  onOpenCall: (callId: CallID) => void;
  onJumpToLine: (line: number) => void;
  onClose: () => void;
}) {
  return (
    <div
      className="effect-popover"
      role="dialog"
      aria-label={`${badge.title}: evidence`}
    >
      <div className="effect-popover-head">
        <span className="effect-icon" aria-hidden="true">
          {badge.icon}
        </span>
        <span className="effect-popover-title">{badge.title}</span>
        {pinned && (
          <button
            type="button"
            className="effect-popover-close"
            onClick={onClose}
            aria-label="close"
          >
            ×
          </button>
        )}
      </div>
      <ul className="effect-evidence">
        {badge.effects.map((e, i) => {
          const inThisFrame =
            e.depth === 0 &&
            e.file === frame.file &&
            e.line >= frame.startLine &&
            e.line <= frame.endLine;
          const canOpen = !inThisFrame && !!e.viaCall;
          const title = inThisFrame
            ? "jump to this line"
            : canOpen
              ? `open ${e.viaTitle ?? "the callee"} inline`
              : "";
          return (
            <li
              key={i}
              className={`effect-row${e.depth === 0 ? "" : " effect-row--inherited"}`}
            >
              <button
                type="button"
                className="effect-row-main"
                disabled={!inThisFrame && !canOpen}
                title={title}
                onClick={() => {
                  if (inThisFrame) onJumpToLine(e.line);
                  else if (e.viaCall) onOpenCall(e.viaCall);
                  // The row did its job; the list in the way of what it
                  // opened would be the next thing to close.
                  onClose();
                }}
              >
                <span className="effect-row-callee">{evidenceLabel(e)}</span>
                {e.access && (
                  <span className="effect-row-access">{e.access}</span>
                )}
                <span className="effect-row-loc">
                  {shortLoc(e.file, e.line)}
                </span>
                <span className="effect-row-hop">{hopLabel(e)}</span>
              </button>
              {e.rule && (
                <span
                  className="effect-row-rule"
                  title="the rule that decided this"
                >
                  {e.rule}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
