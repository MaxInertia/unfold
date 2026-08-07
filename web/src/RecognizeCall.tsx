import { useState } from "react";
import { saveRule } from "./api";
import type { RuleSpec, TypeInfo } from "./types";

// Author a recognizer from a call site you're looking at.
//
// This is the "provide the pattern in the app" half. Writing the match by hand
// means knowing the callee's import path and receiver type; the tool already
// resolved both to render the call, so it can fill them in — and a rule
// generated from a real call site is exact by construction rather than being
// someone's guess at what the package path was.
//
// What it deliberately does not do is guess the *key*. Which argument holds
// the topic is the one thing only the author knows, so it's asked rather than
// inferred: getting that wrong produces a rule that matches and emits
// nonsense, which is worse than one that doesn't match.
export function RecognizeCall({
  info,
  displayName,
  onDone,
  onCancel,
}: {
  // Resolved facts about the callee, from the same type lookup the hover card
  // uses. Without it there's nothing to pre-fill and the form isn't offered.
  info: TypeInfo | null;
  displayName: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const pkg = packageOf(info);
  const [id, setId] = useState(suggestID(pkg, displayName));
  const [role, setRole] = useState<"inbound" | "outbound">("outbound");
  const [kind, setKind] = useState("pubsub.topic");
  const [keyArg, setKeyArg] = useState(0);
  const [handlerArg, setHandlerArg] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    setError(null);
    const rule: RuleSpec = {
      id,
      "//": `recognized from ${displayName}`,
      match: { package: pkg || undefined, func: displayName.split(".").pop() },
      emit: {
        role,
        kind,
        key: `{arg${keyArg}}`,
        ...(handlerArg !== null ? { handler: `arg${handlerArg}` } : {}),
      },
    };
    try {
      await saveRule(rule);
      onDone();
    } catch (e) {
      // A rule the server refuses — one that would match everything, or names
      // a rule that can never fire — is a normal authoring mistake. Stay open.
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="recognize" onClick={(e) => e.stopPropagation()}>
      <div className="recognize-head">
        recognize <b>{displayName}</b>
      </div>
      {!pkg && (
        <p className="rules-warn">
          The callee's package couldn't be resolved, so this rule would match by name alone —
          which is how a rule fires on something that merely shares a method name. Edit the
          saved file to narrow it.
        </p>
      )}

      <label className="recognize-field">
        <span>id</span>
        <input value={id} onChange={(e) => setId(e.target.value)} spellCheck={false} />
      </label>
      <label className="recognize-field">
        <span>role</span>
        <select value={role} onChange={(e) => setRole(e.target.value as "inbound" | "outbound")}>
          <option value="outbound">outbound — this service reaches out</option>
          <option value="inbound">inbound — work enters here</option>
        </select>
      </label>
      <label className="recognize-field">
        <span>kind</span>
        <input value={kind} onChange={(e) => setKind(e.target.value)} spellCheck={false} />
      </label>
      <label className="recognize-field">
        <span>key is</span>
        <select value={keyArg} onChange={(e) => setKeyArg(Number(e.target.value))}>
          {[0, 1, 2, 3, 4].map((n) => (
            <option key={n} value={n}>
              argument {n}
            </option>
          ))}
        </select>
      </label>
      {role === "inbound" && (
        <label className="recognize-field">
          <span>handler is</span>
          <select
            value={handlerArg ?? ""}
            onChange={(e) => setHandlerArg(e.target.value === "" ? null : Number(e.target.value))}
          >
            <option value="">(none)</option>
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>
                argument {n}
              </option>
            ))}
          </select>
        </label>
      )}

      {error && <p className="rules-error">{error}</p>}
      <p className="recognize-note">
        Saving rebuilds the index — what a rule matched is only knowable by running it, so the
        match count appears in the recognizers panel afterwards.
      </p>
      <div className="proto-actions">
        <button type="button" className="proto-use" disabled={busy} onClick={() => void save()}>
          {busy ? "reindexing…" : "save rule"}
        </button>
        <button type="button" className="proto-cancel" onClick={onCancel}>
          cancel
        </button>
      </div>
    </div>
  );
}

// The package a resolved symbol lives in, taken from its defined-at path when
// the type info carries one. Best-effort: an unresolvable package yields a
// name-only rule, which the form warns about rather than silently saving.
function packageOf(info: TypeInfo | null): string {
  if (!info?.targetId) return "";
  const id = info.targetId;
  // Go ids are "pkg/path.Func" or "(*pkg/path.Type).Method".
  const m = id.match(/^\(\*?([^)]+)\)\./) ?? id.match(/^(.*)\.[^.]+$/);
  if (!m) return "";
  const qualified = m[1];
  const dot = qualified.lastIndexOf(".");
  return dot > 0 ? qualified.slice(0, dot) : qualified;
}

function suggestID(pkg: string, displayName: string): string {
  const last = pkg.split("/").pop() ?? "rule";
  const fn = displayName.split(".").pop() ?? "call";
  return `${last}.${fn}`.toLowerCase();
}
