import { useEffect, useRef, useState } from "react";
import { deleteRule, fetchRules, saveRule } from "./api";
import type { RuleInfo, RuleReport, RuleSpec } from "./types";
import { clearRulesFocus, useRulesPanel } from "./rules";

// Every recognizer in force, built-in and configured: what it is, whether it's
// on, how much it matched, and — for the ones you wrote — what it actually
// says, editable in place.
//
// The match count is the point of the list. A rule that has quietly stopped
// matching after a library upgrade contributes nothing, and an empty surface
// looks exactly like one nothing was found in — so the number is shown next to
// every rule, and a zero on a rule that is supposed to be doing something is
// the signal that a library moved. Which is also why the body has to be here:
// a count tells you a rule stopped matching, and the next question is always
// what it was looking for.
export function RulesPanel({ onClose }: { onClose: () => void }) {
  const [report, setReport] = useState<RuleReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const { focus } = useRulesPanel();
  const focused = useRef<HTMLLIElement | null>(null);

  useEffect(() => {
    fetchRules()
      .then(setReport)
      .catch((e: Error) => setError(e.message));
  }, []);

  // A card asked for one rule by name. Expand it and bring it into view once
  // the list exists, then clear the request — it's an instruction, not state,
  // and reopening the panel by hand shouldn't replay it.
  useEffect(() => {
    if (!focus || !report) return;
    setOpen(focus);
    focused.current?.scrollIntoView({ block: "center" });
    clearRulesFocus();
  }, [focus, report]);

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
            <li
              key={r.id}
              ref={focus === r.id ? focused : null}
              className={`rule${r.enabled ? "" : " rule--off"}${
                open === r.id ? " rule--open" : ""
              }`}
            >
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
                  <span className="rule-badge" title="its body is Go; only on/off is yours to set">
                    built-in
                  </span>
                ) : (
                  <>
                    <button
                      type="button"
                      className="rule-edit"
                      title={open === r.id ? "hide the rule body" : "show and edit the rule body"}
                      onClick={() => setOpen(open === r.id ? null : r.id)}
                    >
                      {open === r.id ? "hide" : "edit"}
                    </button>
                    <button
                      type="button"
                      className="rule-delete"
                      title="delete this rule"
                      disabled={busy === r.id}
                      onClick={() => void apply(() => deleteRule(r.id), r.id)}
                    >
                      delete
                    </button>
                  </>
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
              {open === r.id && r.spec && (
                <RuleEditor
                  rule={r}
                  busy={busy === r.id}
                  onSave={(spec) =>
                    apply(async () => {
                      const next = await saveRule(spec);
                      // Saving upserts by id, so a renamed rule would leave the
                      // original behind as a second, still-matching copy.
                      if (spec.id !== r.id) return deleteRule(r.id);
                      return next;
                    }, r.id)
                  }
                  onDone={() => setOpen(null)}
                />
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// The rule as JSON, editable.
//
// Raw JSON rather than a field-per-property form, because the vocabulary is
// small but not flat — captures, nested calleeMatches, key templates — and a
// form that covered it would be most of a rule language editor. Authoring the
// common shape already has a form ("recognize as…" on a call site); this is
// where you go to see exactly what got written and change it.
//
// Validation stays on the server. It refuses a rule that can never fire or one
// that would match every call site, and re-running it is the only way to know
// what the edit now matches — so the count that comes back is the answer, and
// checking anything here would just be a second opinion that can disagree.
function RuleEditor({
  rule,
  busy,
  onSave,
  onDone,
}: {
  rule: RuleInfo;
  busy: boolean;
  onSave: (spec: RuleSpec) => Promise<void>;
  onDone: () => void;
}) {
  const original = JSON.stringify(rule.spec, null, 2);
  const [text, setText] = useState(original);
  const [problem, setProblem] = useState<string | null>(null);
  const dirty = text !== original;

  async function save() {
    let parsed: RuleSpec;
    try {
      parsed = JSON.parse(text) as RuleSpec;
    } catch (e) {
      setProblem((e as Error).message);
      return;
    }
    if (!parsed || typeof parsed !== "object" || !parsed.id) {
      setProblem("a rule needs an id");
      return;
    }
    setProblem(null);
    await onSave(parsed);
    onDone();
  }

  return (
    <div className="rule-editor">
      <textarea
        className="rule-spec"
        value={text}
        spellCheck={false}
        rows={Math.min(20, text.split("\n").length + 1)}
        onChange={(e) => setText(e.target.value)}
      />
      {problem && <p className="rules-error">{problem}</p>}
      <div className="proto-actions">
        <button type="button" className="proto-use" disabled={busy || !dirty} onClick={() => void save()}>
          {busy ? "reindexing…" : "save"}
        </button>
        <button
          type="button"
          className="proto-cancel"
          onClick={() => {
            setText(original);
            setProblem(null);
            onDone();
          }}
        >
          {dirty ? "discard" : "close"}
        </button>
      </div>
    </div>
  );
}
