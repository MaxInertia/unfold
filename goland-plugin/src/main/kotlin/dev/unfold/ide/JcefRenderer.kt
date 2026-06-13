package dev.unfold.ide

import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.openapi.util.Disposer
import com.intellij.ui.jcef.JBCefApp
import com.intellij.ui.jcef.JBCefBrowser
import java.awt.Dimension

/**
 * Experiment C — embed a JCEF browser. The "reuse the existing web frontend"
 * path: here it just renders the code as HTML, but this is where unfold's
 * React/Shiki view would be loaded. It won't match the IDE theme/feel and has
 * no native editor behavior — a contrast/fallback. Falls back to the painted
 * renderer if JCEF isn't available.
 */
class JcefRenderer : FrameRenderer {
    override fun render(host: Editor, anchorOffset: Int, callee: Callee): Disposable {
        if (!JBCefApp.isSupported()) {
            return PaintedRenderer().render(host, anchorOffset, callee)
        }
        val browser = JBCefBrowser()
        browser.loadHTML(html(callee.text))
        val component = browser.component
        component.preferredSize = Dimension(800, host.lineHeight * (callee.text.count { it == '\n' } + 2))

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
        return Disposable {
            inlay?.dispose()
            Disposer.dispose(browser)
        }
    }

    private fun html(code: String): String {
        val esc = code.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        return "<!doctype html><html><body style=\"margin:0;background:#1e1e1e;color:#dcdcdc;\">" +
            "<pre style=\"margin:0;padding:8px;font:13px monospace;\">$esc</pre></body></html>"
    }
}
