import { useState } from "react";
import { saveRule } from "./api";
import type { ArgFacts, RuleSpec, TypeInfo } from "./types";

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
  // Resolved facts about the call, from the same type lookup the hover card
  // uses — and from the same extraction the rule evaluator runs, so what's
  // offered here is what will actually match.
  info: TypeInfo | null;
  displayName: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const call = info?.call;
  const args = call?.args ?? [];
  const pkg = call?.package ?? packageOf(info);
  const funcName = call?.func ?? displayName.split(".").pop() ?? displayName;

  const [id, setId] = useState(suggestID(pkg, funcName));
  const [role, setRole] = useState<"inbound" | "outbound">("outbound");
  const [kind, setKind] = useState("pubsub.topic");
  const [keyArg, setKeyArg] = useState(() => args.findIndex((a) => a.value) ?? 0);
  const [handlerArg, setHandlerArg] = useState<number | null>(null);
  // Which arguments to pin by type. A name like "Emit" or "Publish" belongs to
  // half the libraries in the ecosystem; the types crossing the call belong to
  // one. Nothing is checked by default — a rule that silently constrained more
  // than you asked would be as surprising as one that constrained less.
  const [pinned, setPinned] = useState<Record<number, boolean>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // The receiver identifies a *declaration*, not a library: a call through an
  // interface carries whatever package declared that interface, and nothing
  // stops a repo from declaring its own with the same method. So it's offered,
  // not assumed.
  const [pinRecv, setPinRecv] = useState(false);

  const argMatches = args
    .map((a, n) => ({ n, spec: typeSpecFor(a) }))
    .filter(({ n, spec }) => pinned[n] && spec)
    .map(({ n, spec }) => ({ index: n, ...spec }));

  const match: RuleSpec["match"] = {
    ...(pkg ? { package: pkg } : {}),
    ...(pinRecv && call?.recv ? { recv: call.recv } : {}),
    ...(pinRecv && call?.recvPkg ? { recvPkg: call.recvPkg } : {}),
    func: funcName,
    ...(argMatches.length ? { args: argMatches } : {}),
  };
  // "Matches on the name alone" is the state worth warning about, and it's now
  // a thing you can fix here rather than a fact to be told.
  const nameOnly = !pkg && argMatches.length === 0 && !pinRecv;

  async function save() {
    setBusy(true);
    setError(null);
    const rule: RuleSpec = {
      id,
      "//": `recognized from ${displayName}`,
      match,
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

  const positions = args.length > 0 ? args.map((_, n) => n) : [0, 1, 2, 3, 4];

  return (
    <div className="recognize" onClick={(e) => e.stopPropagation()}>
      <div className="recognize-head">
        recognize <b>{displayName}</b>
      </div>
      {nameOnly && (
        <p className="rules-warn">
          Nothing but the name is pinned, so this rule fires on anything called{" "}
          <code>{funcName}</code>. Pin an argument type below — the types crossing a call identify
          the library even when the interface is declared locally.
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
          {positions.map((n) => (
            <option key={n} value={n}>
              argument {n}
              {args[n]?.value ? ` — "${args[n].value}"` : ""}
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
            {positions.map((n) => (
              <option key={n} value={n}>
                argument {n}
              </option>
            ))}
          </select>
        </label>
      )}

      {/* What narrows the rule. Shown as the call's own types rather than as
          fields to fill in: the answer is already known, the only question is
          which parts of it to insist on. */}
      {(args.length > 0 || call?.recv) && (
        <fieldset className="recognize-pins">
          <legend>only match calls where</legend>
          {call?.recv && (
            <label className="recognize-pin">
              <input type="checkbox" checked={pinRecv} onChange={() => setPinRecv(!pinRecv)} />
              <span>
                receiver is <code>{call.recv}</code>
                {call.recvPkg ? <span className="recognize-pin-pkg"> ({call.recvPkg})</span> : null}
              </span>
            </label>
          )}
          {args.map((a, n) => {
            const spec = typeSpecFor(a);
            if (!spec) return null;
            return (
              <label key={n} className="recognize-pin">
                <input
                  type="checkbox"
                  checked={!!pinned[n]}
                  onChange={() => setPinned({ ...pinned, [n]: !pinned[n] })}
                />
                <span>
                  argument {n} is <code>{spec.type ?? spec.paramType}</code>
                </span>
              </label>
            );
          })}
        </fieldset>
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

// Which of an argument's two types to pin.
//
// The passed type is preferred: it is what the call site actually says, and it
// stays true if the callee's signature widens. The declared parameter type is
// the fallback for the case that has no other answer — a parameter typed `any`
// or an interface, where what was passed is a local implementation and only
// the declaration names the library.
function typeSpecFor(a: ArgFacts): { type?: string; paramType?: string } | null {
  if (a.type && !isVague(a.type)) return { type: a.type };
  if (a.paramType && !isVague(a.paramType)) return { paramType: a.paramType };
  return null;
}

// A type that constrains nothing worth constraining. Pinning "string" on the
// topic argument would be a rule that reads as specific and isn't, and
// context.Context is on the list for the same reason from the other end: it's
// on nearly every method in the language, so insisting on it excludes nothing
// while making the rule look considered.
function isVague(t: string): boolean {
  return (
    t === "" ||
    t === "any" ||
    t === "interface{}" ||
    t === "string" ||
    t === "int" ||
    t === "bool" ||
    t === "error" ||
    t === "context.Context" ||
    t === "untyped nil" ||
    t.startsWith("untyped ")
  );
}

// The package a resolved symbol lives in, taken from its defined-at path when
// the type info carries one. Best-effort fallback for engines that don't send
// resolved call facts; the Go engine does.
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

function suggestID(pkg: string, funcName: string): string {
  const last = pkg.split("/").pop() ?? "rule";
  return `${last}.${funcName}`.toLowerCase();
}
