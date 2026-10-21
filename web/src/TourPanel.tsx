import { useEffect, useRef } from "react";
import { NoteText } from "./NotesUI";
import {
  activeTour,
  goToStop,
  nextStop,
  prevStop,
  selectTour,
  useTours,
  type TourStopCardInfo,
} from "./tours";
import type { TourStop } from "./types";

// The sidebar's tour tab: pick a tour, see its stops with their
// descriptions, and step through them. The list is the whole tour at once —
// a reading list you can scan — while the frame shows one stop at a time.
export function TourPanel() {
  const s = useTours();
  const tour = activeTour(s);
  const listRef = useRef<HTMLOListElement>(null);

  // Keep the current stop in view as the reader steps with the keyboard.
  useEffect(() => {
    if (s.index < 0) return;
    const el = listRef.current?.querySelector<HTMLElement>(`[data-stop="${s.index}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [s.index, s.active]);

  if (!s.loaded) return <p className="tree-placeholder">…</p>;
  if (s.tours.length === 0) return <TourHowTo dir={s.dir} />;

  return (
    <div className="tour">
      {s.tours.length > 1 && (
        <select
          className="tour-select"
          value={s.active ?? ""}
          onChange={(e) => selectTour(e.target.value || null)}
          title="which tour to follow"
        >
          <option value="">pick a tour…</option>
          {s.tours.map((t) => (
            <option key={t.name} value={t.name}>
              {t.title || t.name}
              {t.error ? " (unreadable)" : ` · ${t.stops.length}`}
            </option>
          ))}
        </select>
      )}
      {!tour ? (
        <p className="tree-placeholder">Pick a tour to follow it.</p>
      ) : tour.error ? (
        <div className="tour-error">
          <div className="tour-title">{tour.title || tour.name}</div>
          <p>
            Couldn't read <code>{tour.name}.json</code>: {tour.error}
          </p>
        </div>
      ) : (
        <>
          <div className="tour-head">
            <div className="tour-title" title={`${s.dir}/${tour.name}.json`}>
              {tour.title || tour.name}
            </div>
            <TourNav index={s.index} total={tour.stops.length} />
          </div>
          {tour.description && (
            <div className="tour-desc">
              <NoteText text={tour.description} />
            </div>
          )}
          <ol className="tour-stops" ref={listRef}>
            {tour.stops.map((stop, i) => {
              const r = s.resolved[i];
              const unresolved = r && r !== "pending" && r.kind === "unresolved";
              const missing = r && r !== "pending" && r.kind === "symbol" ? r.missing : [];
              return (
                <li
                  key={i}
                  data-stop={i}
                  className={`tour-stop${i === s.index ? " tour-stop--current" : ""}${
                    unresolved ? " tour-stop--unresolved" : ""
                  }`}
                >
                  <button
                    type="button"
                    className="tour-stop-open"
                    onClick={() => goToStop(i)}
                    aria-current={i === s.index ? "step" : undefined}
                    title={
                      unresolved
                        ? r.reason
                        : stop.symbol
                          ? `open ${stop.symbol}`
                          : `open ${stop.file}${stop.line ? `:${stop.line}` : ""}`
                    }
                  >
                    <span className="tour-stop-n">{i + 1}</span>
                    <span className="tour-stop-where">
                      {whereLabel(stop)}
                      {r === "pending" && <span className="tour-stop-flag"> …</span>}
                      {unresolved && (
                        <span className="tour-stop-flag" title={r.reason}>
                          {" "}
                          ? not found
                        </span>
                      )}
                      {missing.length > 0 && (
                        <span
                          className="tour-stop-flag"
                          title={`couldn't expand: ${missing.join(", ")}`}
                        >
                          {" "}
                          ⚠ {missing.length} not expanded
                        </span>
                      )}
                    </span>
                  </button>
                  {stop.description && (
                    <div className="tour-stop-desc">
                      <NoteText text={stop.description} />
                    </div>
                  )}
                </li>
              );
            })}
          </ol>
        </>
      )}
    </div>
  );
}

function TourNav({ index, total }: { index: number; total: number }) {
  return (
    <span className="tour-nav">
      <button
        type="button"
        className="tour-nav-btn"
        onClick={prevStop}
        title="previous stop  ["
        aria-label="previous stop"
      >
        ‹
      </button>
      <span className="tour-pos">{index < 0 ? `${total} stops` : `${index + 1} of ${total}`}</span>
      <button
        type="button"
        className="tour-nav-btn"
        onClick={nextStop}
        title="next stop  ]"
        aria-label="next stop"
      >
        ›
      </button>
    </span>
  );
}

function whereLabel(stop: TourStop): string {
  if (stop.symbol) return stop.symbol;
  const file = stop.file ?? "";
  const short = file.split("/").slice(-2).join("/");
  return stop.line ? `${short}:${stop.line}` : short;
}

// Shown when there are no tours: how to write one. This is the authoring
// documentation an agent in the repo needs, so it is also what the README
// says.
function TourHowTo({ dir }: { dir: string }) {
  return (
    <div className="tour-howto">
      <p>
        No tours yet. A tour is an ordered list of places to look, each with a reason — written by
        you, a colleague, or an LLM working in the repo.
      </p>
      <p>
        Put one at <code>{dir || ".unfold/tours"}/&lt;name&gt;.json</code>; it shows up here as soon
        as the file lands:
      </p>
      <pre className="tour-schema">{`{
  "title": "How a publish is retried",
  "stops": [
    { "symbol": "pubsub.(*Client).Publish",
      "description": "Entry point. The ctx deadline is set here." },
    { "file": "internal/pubsub/retry.go", "line": 41,
      "description": "The backoff table — see [[backoff.Next]]." },
    { "symbol": "Publish",
      "expand": ["retry.Do > backoff.Next"],
      "description": "Same entry, with the retry loop spliced in." }
  ]
}`}</pre>
      <p>
        A stop is a <code>symbol</code> (qualified or bare) or a <code>file</code> + <code>line</code>.{" "}
        <code>expand</code> names calls to open with it; <code>&gt;</code> nests. Descriptions may
        reference code with <code>[[Name]]</code>.
      </p>
    </div>
  );
}

// The card a frame renders at a stop's line, in the same voice as a note:
// which tour, which stop, why you're looking here, and a way to the next.
export function TourStopCard({ card }: { card: TourStopCardInfo }) {
  const { stop, index, total, current, missing } = card;
  return (
    <div className={`note note--tour${current ? " note--tour-current" : ""}`}>
      <div className="note-head">
        <span className="note-label">tour</span>
        <span className="tour-card-pos">
          {index + 1} of {total}
        </span>
        <span className="tour-card-title">{card.tourTitle}</span>
        {missing.length > 0 && (
          <span className="note-drifted" title={`couldn't expand: ${missing.join(", ")}`}>
            ⚠ {missing.length} not expanded
          </span>
        )}
        <span className="note-when" />
        <button
          type="button"
          className="note-action"
          onClick={() => goToStop(index - 1)}
          disabled={index === 0}
          title="previous stop  ["
          aria-label="previous stop"
        >
          ‹
        </button>
        <button
          type="button"
          className="note-action"
          onClick={() => goToStop(index + 1)}
          disabled={index === total - 1}
          title="next stop  ]"
          aria-label="next stop"
        >
          ›
        </button>
      </div>
      {stop.description && (
        <div className="note-body">
          <NoteText text={stop.description} />
        </div>
      )}
    </div>
  );
}
