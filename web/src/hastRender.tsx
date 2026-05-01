import { Fragment, createElement, type ReactNode } from "react";
import type { Element, Nodes, Root } from "hast";
import type { CallID, CallSite } from "./types";

interface RenderCtx {
  // Frame source + calls so we can compute which line each call lives
  // on. The walker bumps `lineCursor` for every <span class="line"> it
  // visits — line spans are emitted in source order, so the index lines
  // up with `callsByLine`.
  callsByLine: Map<number, CallSite[]>;
  lineCursor: { value: number };
  renderLineExtras: (lineIdx: number) => ReactNode;
  renderCallSpan: (
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ) => ReactNode;
  callsById: Map<CallID, CallSite>;
}

export interface RenderOptions {
  hast: Root;
  source: string;
  calls: CallSite[];
  renderLineExtras: (lineIdx: number) => ReactNode;
  renderCallSpan: (
    call: CallSite,
    children: ReactNode,
    domProps: Record<string, unknown>,
  ) => ReactNode;
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
    renderLineExtras: opts.renderLineExtras,
    renderCallSpan: opts.renderCallSpan,
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
  return walkElement(node, ctx, key);
}

function walkElement(node: Element, ctx: RenderCtx, key: string): ReactNode {
  const props = hastPropsToReact(node.properties ?? {});

  // Call-site span — defer rendering to the caller's renderCallSpan so
  // it can attach a click handler, inject the inline child frame, etc.
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

  // Line span — emit the line, then any expanded children / errors that
  // belong to it. Indented matching the line's leading whitespace so the
  // child frame sits visually under the call expression.
  const className = (props.className as string | undefined) ?? "";
  if (node.tagName === "span" && hasClass(className, "line")) {
    const lineIdx = ctx.lineCursor.value++;
    const children = node.children.map((c, i) =>
      walk(c, ctx, `${key}.${i}`),
    );
    return (
      <Fragment key={key}>
        <span {...props}>{children}</span>
        {ctx.renderLineExtras(lineIdx)}
      </Fragment>
    );
  }

  // Generic element passthrough.
  const children = node.children.map((c, i) => walk(c, ctx, `${key}.${i}`));
  return createElement(
    node.tagName,
    { ...props, key },
    children.length > 0 ? children : undefined,
  );
}

function hasClass(className: string, want: string): boolean {
  return className === want || className.split(/\s+/).includes(want);
}

// hast properties use camelCase keys (e.g. dataCallId) and arrays for
// className. React's createElement wants string className, kebab data-*,
// and a parsed object for style.
function hastPropsToReact(
  props: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, val] of Object.entries(props)) {
    if (key === "className" && Array.isArray(val)) {
      out.className = val.join(" ");
    } else if (key === "className" && typeof val === "string") {
      out.className = val;
    } else if (key === "class" && typeof val === "string") {
      out.className = val;
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
  // CSS custom properties pass through as-is.
  if (k.startsWith("--")) return k;
  return k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}
