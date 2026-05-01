import { createHighlighter, type Highlighter } from "shiki";
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

// Highlights `source` and wraps each call-site byte range with a span
// carrying data-call-id and data-call-kind. The returned HTML is meant
// to be placed inside a <pre> element via dangerouslySetInnerHTML.
export async function highlightCode(opts: HighlightOptions): Promise<string> {
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
        "data-call-target": c.targetId ?? "",
        "data-display": c.displayName,
        class: `call-site call-site--${c.kind}${expandable ? " call-site--resolvable" : ""}`,
      },
    };
  });
  return hl.codeToHtml(opts.source, {
    lang,
    themes: { light: "github-light", dark: "github-dark" },
    decorations,
  });
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
