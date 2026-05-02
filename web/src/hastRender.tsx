import { Fragment, createElement, type ReactNode } from "react";
import type { Element, Nodes, Root, Text } from "hast";
import type { CallID, CallSite } from "./types";

export type LineAction =
  | { kind: "render" }
  | { kind: "skip" } // mid-fold — produce nothing
  | { kind: "fold-start"; endLine: number }; // first line of a fold — produce placeholder

interface RenderCtx {
  // Frame source + calls so we can compute which line each call lives on.
  callsByLine: Map<number, CallSite[]>;
  lineCursor: { value: number };
  callsById: Map<CallID, CallSite>;
  // Hooks called by the walker for each emit.
  lineAction: (idx: number) => LineAction;
  renderLineExtras: (lineIdx: number) => ReactNode;
  renderCallSpan: (
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ) => ReactNode;
  renderLineGutter: (idx: number) => ReactNode;
  renderFoldPlaceholder: (startLine: number, endLine: number) => ReactNode;
}

export interface RenderOptions {
  hast: Root;
  source: string;
  calls: CallSite[];
  lineAction: (idx: number) => LineAction;
  renderLineExtras: (lineIdx: number) => ReactNode;
  renderCallSpan: (
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ) => ReactNode;
  renderLineGutter: (idx: number) => ReactNode;
  renderFoldPlaceholder: (startLine: number, endLine: number) => ReactNode;
}

export function renderHast(opts: RenderOptions): ReactNode {
  const callsByLine = new Map<number, CallSite[]>();
  const callsById = new Map<CallID, CallSite>();
  for (const c of opts.calls) {
    callsById.set(c.id, c);
    const lineIdx = lineForOffset(opts.source, c.spanStart);
    const list = callsByLine.get(lineIdx) ?? [];
    list.push(c);
    callsByLine.set(lineIdx, list);
  }
  const ctx: RenderCtx = {
    callsByLine,
    callsById,
    lineCursor: { value: 0 },
    lineAction: opts.lineAction,
    renderLineExtras: opts.renderLineExtras,
    renderCallSpan: opts.renderCallSpan,
    renderLineGutter: opts.renderLineGutter,
    renderFoldPlaceholder: opts.renderFoldPlaceholder,
  };
  return walk(opts.hast, ctx, "r");
}

function walk(node: Nodes, ctx: RenderCtx, key: string): ReactNode {
  if (node.type === "text") return node.value;
  if (node.type === "root") {
    return (
      <Fragment key={key}>
        {node.children.map((c, i) => (
          <Fragment key={i}>{walk(c, ctx, `${key}.${i}`)}</Fragment>
        ))}
      </Fragment>
    );
  }
  if (node.type !== "element") return null;
  if (node.tagName === "code") return walkCode(node, ctx, key);
  return walkElement(node, ctx, key);
}

function walkElement(node: Element, ctx: RenderCtx, key: string): ReactNode {
  const props = hastPropsToReact(node.properties ?? {});

  // Call-site span — defer rendering to the caller's renderCallSpan.
  const callId = props["data-call-id"] as CallID | undefined;
  if (node.tagName === "span" && callId) {
    const call = ctx.callsById.get(callId);
    if (call) {
      const children = node.children.map((c, i) =>
        walk(c, ctx, `${key}.${i}`),
      );
      return (
        <Fragment key={key}>
          {ctx.renderCallSpan(call, children, props)}
        </Fragment>
      );
    }
  }

  const children = node.children.map((c, i) => walk(c, ctx, `${key}.${i}`));
  return createElement(
    node.tagName,
    { ...props, key },
    children.length > 0 ? children : undefined,
  );
}

// walkCode handles <code> children specially so we can insert
// per-line gutters, fold placeholders, and skip mid-fold lines.
function walkCode(node: Element, ctx: RenderCtx, key: string): ReactNode {
  const props = hastPropsToReact(node.properties ?? {});
  const out: ReactNode[] = [];
  const kids = node.children;

  for (let i = 0; i < kids.length; i++) {
    const child = kids[i];
    if (child.type === "element" && child.tagName === "span" && isLineSpan(child)) {
      const lineIdx = ctx.lineCursor.value++;
      const action = ctx.lineAction(lineIdx);
      // Skip the trailing newline regardless of action — line rows are
      // block-level now, so a literal \n would just leave a blank line.
      const trailingNewline =
        i + 1 < kids.length && kids[i + 1].type === "text" && (kids[i + 1] as Text).value === "\n";

      if (action.kind === "skip") {
        if (trailingNewline) i++;
        continue;
      }
      if (action.kind === "fold-start") {
        out.push(
          <Fragment key={`fold:${lineIdx}`}>
            {ctx.renderFoldPlaceholder(lineIdx, action.endLine)}
          </Fragment>,
        );
        if (trailingNewline) i++;
        continue;
      }

      // "render"
      const lineProps = hastPropsToReact(child.properties ?? {});
      const lineChildren = child.children.map((c, ci) =>
        walk(c, ctx, `${key}.${i}.${ci}`),
      );
      out.push(
        <div className="line-row" key={`row:${lineIdx}`} data-line-idx={lineIdx}>
          {ctx.renderLineGutter(lineIdx)}
          <span {...lineProps}>{lineChildren}</span>
        </div>,
      );
      const extras = ctx.renderLineExtras(lineIdx);
      if (extras) out.push(<Fragment key={`x:${lineIdx}`}>{extras}</Fragment>);
      if (trailingNewline) i++;
      continue;
    }
    // Whitespace or other non-line children — pass through.
    out.push(walk(child, ctx, `${key}.${i}`));
  }

  return createElement(node.tagName, { ...props, key }, out);
}

function isLineSpan(el: Element): boolean {
  const cls = el.properties?.className ?? el.properties?.class;
  if (Array.isArray(cls)) return cls.includes("line");
  if (typeof cls === "string") return cls.split(/\s+/).includes("line");
  return false;
}

// hast properties use camelCase keys (e.g. dataCallId) and arrays for
// className. React's createElement wants string className, kebab data-*,
// and a parsed object for style.
function hastPropsToReact(
  props: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, val] of Object.entries(props)) {
    if (key === "className" || key === "class") {
      // hast normalises class to an array (per spec); shiki's
      // decorations API also delivers an array. Accept both that and a
      // bare string.
      if (Array.isArray(val)) out.className = val.join(" ");
      else if (typeof val === "string") out.className = val;
    } else if (key === "style" && typeof val === "string") {
      out.style = parseInlineStyle(val);
    } else if (/^data[A-Z]/.test(key)) {
      out["data-" + kebab(key.slice(4))] = val;
    } else {
      out[key] = val;
    }
  }
  return out;
}

function kebab(s: string): string {
  return s.replace(/[A-Z]/g, (m, i) => (i === 0 ? m.toLowerCase() : "-" + m.toLowerCase()));
}

function parseInlineStyle(s: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of s.split(";")) {
    const idx = part.indexOf(":");
    if (idx < 0) continue;
    const k = part.slice(0, idx).trim();
    const v = part.slice(idx + 1).trim();
    if (!k) continue;
    out[reactStyleKey(k)] = v;
  }
  return out;
}

function reactStyleKey(k: string): string {
  if (k.startsWith("--")) return k;
  return k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}
