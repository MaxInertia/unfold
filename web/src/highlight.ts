import { createHighlighter, type Highlighter } from "shiki";
import type { Root as HastRoot } from "hast";
import type { CallSite } from "./types";

let cached: Promise<Highlighter> | null = null;

function getHighlighter(): Promise<Highlighter> {
  if (!cached) {
    cached = createHighlighter({
      themes: ["github-light", "github-dark"],
      langs: ["go", "typescript", "javascript", "tsx"],
    });
  }
  return cached;
}

export interface HighlightOptions {
  source: string;
  language: string;
  calls: CallSite[];
}

// Returns a HAST tree with each call-site byte range wrapped in a span
// carrying data-call-id / data-call-kind / data-display attributes. The
// caller walks this tree to render React elements — we don't go through
// dangerouslySetInnerHTML, so call-site spans become real React
// components that can host inline child frames.
export async function highlightToHast(opts: HighlightOptions): Promise<HastRoot> {
  const hl = await getHighlighter();
  const lang = supportedLang(opts.language);
  const decorations = opts.calls.map((c) => {
    const expandable =
      (c.kind === "direct" && !!c.targetId) ||
      (c.kind === "interface" && (c.candidates?.length ?? 0) > 0);
    return {
      start: c.spanStart,
      end: c.spanEnd,
      properties: {
        "data-call-id": c.id,
        "data-call-kind": c.kind,
        "data-display": c.displayName,
        class: `call-site call-site--${c.kind}${expandable ? " call-site--resolvable" : ""}`,
      },
    };
  });
  return hl.codeToHast(opts.source, {
    lang,
    themes: { light: "github-light", dark: "github-dark" },
    decorations,
  }) as HastRoot;
}

function supportedLang(lang: string): string {
  switch (lang) {
    case "go":
    case "typescript":
    case "tsx":
    case "javascript":
      return lang;
    default:
      return "go";
  }
}
