import { useEffect, useState } from "react";
import { deleteRule, fetchRules, saveRule } from "./api";
import type { RuleInfo, RuleReport } from "./types";

// Every recognizer in force, built-in and configured.
//
// The match count is the point. A rule that has quietly stopped matching after
// a library upgrade contributes nothing, and an empty surface looks exactly
// like one nothing was found in — so the number is shown next to every rule,
// and a zero on a rule that is supposed to be doing something is the signal
// that a library moved.
export function RulesPanel({ onClose }: { onClose: () => void }) {
  const [report, setReport] = useState<RuleReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    fetchRules()
      .then(setReport)
      .catch((e: Error) => setError(e.message));
  }, []);

  async function apply(fn: () => Promise<RuleReport>, id: string) {
    setBusy(id);
    setError(null);
    try {
      setReport(await fn());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  // Toggling a built-in saves a settings-only entry: an id and enabled:false,
  // with no rule body to restate. That's what merging by id buys.
  const toggle = (r: RuleInfo) =>
    apply(() => saveRule({ id: r.id, enabled: !r.enabled }), r.id);

  const disabledBuiltins = (report?.rules ?? []).filter((r) => r.builtin && !r.enabled);

  return (
    <div className="rules-panel">
      <div className="rules-header">
        <span className="rules-title">recognizers</span>
        <button type="button" className="tree-collapse" onClick={onClose} aria-label="close">
          ×
        </button>
      </div>

      {error && <p className="rules-error">{error}</p>}

      {/* A surface that's empty because a rule was switched off must not look
          like one nothing was found in. */}
      {disabledBuiltins.length > 0 && (
        <p className="rules-warn">
          {disabledBuiltins.length} built-in rule{disabledBuiltins.length === 1 ? "" : "s"} switched
          off — part of the surface is missing by choice, not because nothing was found.
        </p>
      )}

      {report?.problems?.map((p) => (
        <p key={p} className="rules-error">
          {p}
        </p>
      ))}

      {!report ? (
        <p className="tree-placeholder">loading…</p>
      ) : report.rules.length === 0 ? (
        <p className="tree-placeholder">No recognizers — this engine has no platform surface.</p>
      ) : (
        <ul className="rules-list">
          {report.rules.map((r) => (
            <li key={r.id} className={`rule${r.enabled ? "" : " rule--off"}`}>
              <label className="rule-toggle">
                <input
                  type="checkbox"
                  checked={r.enabled}
                  disabled={busy === r.id}
                  onChange={() => void toggle(r)}
                />
                <span className="rule-id">{r.id}</span>
              </label>
              <span className="rule-meta">
                {r.builtin ? (
                  <span className="rule-badge">built-in</span>
                ) : (
                  <button
                    type="button"
                    className="rule-delete"
                    title="delete this rule"
                    disabled={busy === r.id}
                    onClick={() => void apply(() => deleteRule(r.id), r.id)}
                  >
                    delete
                  </button>
                )}
                <span
                  className={`rule-matches${r.enabled && r.matches === 0 ? " rule-matches--zero" : ""}`}
                  title={
                    r.matches === 0
                      ? "matched nothing — if this rule is supposed to be doing something, the library it describes may have moved"
                      : `${r.matches} bindings`
                  }
                >
                  {r.enabled ? `${r.matches} match${r.matches === 1 ? "" : "es"}` : "off"}
                </span>
              </span>
              {r.doc && <p className="rule-doc">{r.doc}</p>}
              {r.source && (
                <p className="rule-source" title={r.source}>
                  {r.source}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
