package dev.unfold.ide

import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.openapi.util.Disposer
import com.intellij.ui.jcef.JBCefApp
import com.intellij.ui.jcef.JBCefBrowser
import java.awt.Dimension

/**
 * Experiment C — embed a JCEF browser rendering the callee in a faithful copy
 * of unfold's web `.frame` card (same structure and palette as
 * web/src/index.css, plus a prefers-color-scheme dark variant). This is the
 * "what the web view actually looks like" reference to compare against the
 * native-themed [EditorInlayRenderer] card: it won't pick up the IDE theme or
 * give native editor behavior, but it shows the real web styling in place.
 * Falls back to the painted renderer if JCEF isn't available.
 */
class JcefRenderer : FrameRenderer {
    override fun render(host: Editor, anchorOffset: Int, callee: Callee, depth: Int): Frame {
        if (!JBCefApp.isSupported()) {
            return PaintedRenderer().render(host, anchorOffset, callee, depth)
        }
        val browser = JBCefBrowser()
        browser.loadHTML(html(callee.title, FrameChrome.location(callee), callee.text, depth))
        val component = browser.component
        // +3 lines: header row plus the card's vertical padding/border.
        component.preferredSize = Dimension(800, host.lineHeight * (callee.text.count { it == '\n' } + 3))

        val inlay = EditorEmbeddedComponentManager.getInstance().addComponent(
            host as EditorEx,
            component,
            EditorEmbeddedComponentManager.Properties(
                EditorEmbeddedComponentManager.ResizePolicy.none(),
                null,
                /* relatesToPrecedingText = */ true,
                /* showAbove = */ false,
                /* priority = */ 0,
                anchorOffset,
            ),
        )
        // A JCEF frame is a picture, not a real editor, so it exposes no inner
        // editor and can't itself be nested into — depth only tints its rail.
        return Frame(innerEditor = null) {
            inlay?.dispose()
            Disposer.dispose(browser)
        }
    }

    /** Rail accent for [depth] as (light, dark) hex, mirroring FrameChrome.RAIL
     *  and the web `depthColor()` cycle. */
    private fun accent(depth: Int): Pair<String, String> {
        val light = listOf("#2563eb", "#0d9488", "#d97706", "#9333ea", "#dc2626", "#65a30d")
        val dark = listOf("#58a6ff", "#2dd4bf", "#f59e0b", "#c084fc", "#f87171", "#84cc16")
        val i = ((depth % light.size) + light.size) % light.size
        return light[i] to dark[i]
    }

    private fun html(title: String, location: String, code: String, depth: Int): String {
        fun esc(s: String) = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        val locSpan = if (location.isNotEmpty()) "<span class=\"frame-loc\">${esc(location)}</span>" else ""
        val (accentLight, accentDark) = accent(depth)
        return """<!doctype html><html><head><meta charset="utf-8"><style>
            :root{--bg:#fafaf9;--fg:#1c1c1c;--muted:#6b7280;--accent:$accentLight;--card-bg:#fff;--card-border:#e5e7eb;}
            @media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8b949e;--accent:$accentDark;--card-bg:#161b22;--card-border:#30363d;}}
            html,body{margin:0;background:var(--bg);color:var(--fg);
              font:13px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;}
            .frame{margin:6px;border:1px solid var(--card-border);border-left:3px solid var(--accent);
              border-radius:8px;background:var(--card-bg);overflow:hidden;}
            .frame-header{display:flex;align-items:baseline;gap:.8rem;padding:.4rem .7rem;
              background:rgba(127,127,127,.06);border-bottom:1px solid var(--card-border);font-size:.85em;}
            .frame-title{font-weight:600;}
            .frame-loc{margin-left:auto;color:var(--muted);font-size:.8em;}
            pre{margin:0;padding:.4rem .7rem;white-space:pre;overflow:auto;}
            </style></head><body>
            <div class="frame">
              <div class="frame-header"><span class="frame-title">${esc(title)}</span>$locSpan</div>
              <pre>${esc(code)}</pre>
            </div></body></html>"""
    }
}
