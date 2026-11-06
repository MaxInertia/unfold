import { useEffect } from "react";
import { shortFn } from "./replay";
import {
  SPEEDS,
  eventAt,
  eventCount,
  loadTraces,
  seek,
  selectGoroutine,
  selectTrace,
  setSpeed,
  step,
  togglePlay,
  useReplay,
} from "./replayStore";

// The sidebar's replay tab: pick a recorded run, then play, scrub or step
// through it while the code view follows. The panel shows the position and
// the stack; the frame shows the line.
export function ReplayPanel() {
  const s = useReplay();

  // The list is read from disk by the server, and a trace is written by a
  // command run elsewhere, so it is refetched each time the tab opens.
  useEffect(() => {
    void loadTraces();
  }, []);

  if (!s.loaded) return <p className="tree-placeholder">…</p>;
  if (s.traces.length === 0) return <ReplayHowTo dir={s.dir} />;

  const total = eventCount();
  const ev = s.index >= 0 ? eventAt(s.index) : undefined;
  const top = s.view?.stack[s.view.stack.length - 1];
  const goroutines = s.active
    ? [...new Set(s.active.events.map((e) => e.g))]
    : [];
  const atEnd = s.index >= total - 1;

  return (
    <div className="replay">
      <select
        className="replay-select"
        value={s.active?.name ?? s.loading ?? ""}
        onChange={(e) => selectTrace(e.target.value || null)}
        title="which recorded run to replay"
      >
        <option value="">pick a run…</option>
        {s.traces.map((t) => (
          <option key={t.name} value={t.name}>
            {t.test || t.name}
            {t.error ? " (unreadable)" : ` · ${t.events} events`}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="replay-btn replay-btn--refresh"
        onClick={() => void loadTraces()}
        title="re-read .unfold/traces"
      >
        refresh list
      </button>
      {s.error && <p className="replay-note">{s.error}</p>}
      {s.loading && <p className="tree-placeholder">loading {s.loading}…</p>}
      {s.active && (
        <>
          <div
            className="replay-title"
            title={`${s.dir}/${s.active.name}.json`}
          >
            {s.active.test || s.active.name}
          </div>
          <div className="replay-sub">
            {s.active.package}
            {s.active.truncated && " · truncated at the event cap"}
          </div>
          {goroutines.length > 1 && (
            <select
              className="replay-select"
              value={s.g ?? ""}
              onChange={(e) => selectGoroutine(Number(e.target.value))}
              title="which goroutine to follow — the view is one stack at a time"
            >
              {goroutines.map((g) => (
                <option key={g} value={g}>
                  goroutine {g} ·{" "}
                  {s.active!.events.filter((e) => e.g === g).length} events
                </option>
              ))}
            </select>
          )}
          <div className="replay-controls">
            <button
              type="button"
              className="replay-btn"
              onClick={() => seek(-1)}
              disabled={s.index < 0}
              title="back to the start"
              aria-label="back to the start"
            >
              ⏮
            </button>
            <button
              type="button"
              className="replay-btn"
              onClick={() => step(-1)}
              disabled={s.index < 0}
              title="step back one event  ,"
              aria-label="step back"
            >
              ‹
            </button>
            <button
              type="button"
              className={`replay-btn${s.playing ? " replay-btn--playing" : ""}`}
              onClick={togglePlay}
              title={
                s.playing
                  ? "pause"
                  : atEnd
                    ? "play again from the start"
                    : "play"
              }
              aria-label={s.playing ? "pause" : "play"}
            >
              {s.playing ? "❚❚" : "▶"}
            </button>
            <button
              type="button"
              className="replay-btn"
              onClick={() => step(1)}
              disabled={atEnd}
              title="step forward one event  ."
              aria-label="step forward"
            >
              ›
            </button>
            <button
              type="button"
              className="replay-btn"
              onClick={() => seek(total - 1)}
              disabled={atEnd}
              title="jump to the end"
              aria-label="jump to the end"
            >
              ⏭
            </button>
            <select
              className="replay-speed"
              value={s.speed}
              onChange={(e) => setSpeed(Number(e.target.value))}
              title="playback speed, in events per second"
            >
              {SPEEDS.map((v) => (
                <option key={v} value={v}>
                  {v}/s
                </option>
              ))}
            </select>
          </div>
          <input
            type="range"
            className="replay-scrub"
            min={-1}
            max={Math.max(total - 1, -1)}
            value={s.index}
            onChange={(e) => {
              step(0);
              seek(Number(e.target.value));
            }}
            aria-label="position in the run"
          />
          <div className="replay-pos">
            <span data-replay-pos>
              {s.index < 0 ? "start" : `event ${s.index + 1} of ${total}`}
            </span>
            {ev && <span>{(ev.t / 1e6).toFixed(2)} ms</span>}
          </div>
          <div className="replay-event" data-replay-fn={top ? top.fn : ""}>
            {ev && top ? (
              <>
                <span className="replay-kind">{ev.kind}</span>
                <code>{shortFn(top.fn)}</code>
                {top.line > 0 && (
                  <span className="replay-stack-line">:{top.line}</span>
                )}
              </>
            ) : (
              <span className="replay-kind">
                {s.index < 0 ? "before the run" : "run over"}
              </span>
            )}
          </div>
          {s.view && s.view.stack.length > 0 && (
            <ol
              className="replay-stack"
              title="the goroutine's stack after this event, outermost first"
            >
              {s.view.stack.map((e, i) => {
                const unplaced = s.view!.unplaced.includes(e.fn);
                return (
                  <li
                    key={i}
                    className={[
                      i === s.view!.stack.length - 1 ? "replay-stack--top" : "",
                      unplaced ? "replay-stack--unplaced" : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                    title={
                      unplaced
                        ? `${e.fn} — not in the index, so it isn't shown`
                        : e.fn
                    }
                  >
                    <span>{shortFn(e.fn)}</span>
                    <span className="replay-stack-line">{e.line}</span>
                  </li>
                );
              })}
            </ol>
          )}
          {s.view && s.view.unplaced.length > 0 && s.view.root === null && (
            <p className="replay-note">
              Inside code that isn't indexed (test files aren't). The view
              follows once an indexed function is entered.
            </p>
          )}
        </>
      )}
    </div>
  );
}

// Shown when there are no traces: how to record one.
function ReplayHowTo({ dir }: { dir: string }) {
  return (
    <div className="replay-howto">
      <p>
        No recorded runs yet. A replay walks the lines a test actually executed,
        in order, unfolding each call as it is entered.
      </p>
      <p>Record one from the project directory:</p>
      <pre>{`unfold trace -run TestValidate ./internal/tours`}</pre>
      <p>
        It builds the package with every function instrumented (through a build
        overlay; the checkout is not modified), runs the test, and writes{" "}
        <code>{dir || ".unfold/traces"}/&lt;test&gt;.json</code>. Then pick it
        here.
      </p>
    </div>
  );
}
