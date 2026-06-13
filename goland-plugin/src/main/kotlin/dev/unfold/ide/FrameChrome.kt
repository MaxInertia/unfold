package dev.unfold.ide

import com.intellij.openapi.editor.Editor
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.ui.ColorUtil
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBLabel
import com.intellij.util.ui.JBFont
import com.intellij.util.ui.JBUI
import com.intellij.util.ui.UIUtil
import java.awt.BorderLayout
import java.awt.Color
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.border.CompoundBorder

/**
 * Wraps an expanded frame's content (the embedded editor) in card chrome that
 * mirrors unfold's web view: a header row carrying the callee title and source
 * location, a thin card border, and a depth-colored left rail. The web app
 * hard-codes its palette; here the structure is copied but the colors derive
 * from the active editor color scheme, so the card matches the user's IDE
 * theme (light or dark) instead of looking like a transplanted web page.
 *
 * Structure (matches `.frame` / `.frame-header` in web/src/index.css):
 *   ┌─ 3px depth rail ──────────────────────────────┐
 *   │ Title                            file.go:42    │  ← header (tinted, bold title)
 *   ├────────────────────────────────────────────────┤
 *   │ <embedded native code>                         │  ← content
 *   └────────────────────────────────────────────────┘
 */
object FrameChrome {

    /**
     * Depth → rail color, mirroring the web `depthColor()` cycle
     * (StickyHeaders.tsx). Each is a [JBColor] light/dark pair so the rail
     * reads on either theme. Depth is 0 for a single expansion today; the
     * cycle is here so nested frames can later stack distinct rails like the
     * web view does.
     */
    private val RAIL: List<JBColor> = listOf(
        JBColor(Color(0x2563EB), Color(0x58A6FF)), // blue   (accent)
        JBColor(Color(0x0D9488), Color(0x2DD4BF)), // teal
        JBColor(Color(0xD97706), Color(0xF59E0B)), // amber
        JBColor(Color(0x9333EA), Color(0xC084FC)), // purple
        JBColor(Color(0xDC2626), Color(0xF87171)), // red
        JBColor(Color(0x65A30D), Color(0x84CC16)), // green
    )

    fun railColor(depth: Int): JBColor = RAIL[((depth % RAIL.size) + RAIL.size) % RAIL.size]

    /** "file.go:42" for the header, or "" when the callee has no on-disk file. */
    fun location(callee: Callee): String {
        val vf = callee.sourceFile ?: return ""
        val range = callee.range ?: return vf.name
        val doc = FileDocumentManager.getInstance().getDocument(vf) ?: return vf.name
        if (range.startOffset > doc.textLength) return vf.name
        return "${vf.name}:${doc.getLineNumber(range.startOffset) + 1}"
    }

    /**
     * Wrap [content] in the card. Caller still owns [content]'s preferred
     * *height* (the fitted editor height); the returned panel adds the header
     * and borders on top via BorderLayout, so its own preferred size accounts
     * for both. Re-fitting: after changing the content's preferred size, call
     * [JComponent.revalidate] on the returned panel before the inlay update.
     */
    fun wrap(host: Editor, content: JComponent, title: String, location: String, depth: Int): JPanel {
        val scheme = host.colorsScheme
        val bg = scheme.defaultBackground
        val cardBorder = JBColor.border()
        val rail = railColor(depth)

        val header = JPanel(BorderLayout()).apply {
            isOpaque = true
            background = headerTint(bg)
            border = JBUI.Borders.empty(2, 8)
            add(
                JBLabel(title).apply {
                    font = JBFont.label().asBold()
                    foreground = scheme.defaultForeground
                },
                BorderLayout.WEST,
            )
            if (location.isNotEmpty()) {
                add(
                    JBLabel(location).apply {
                        font = JBFont.small()
                        foreground = UIUtil.getContextHelpForeground()
                        toolTipText = location
                    },
                    BorderLayout.EAST,
                )
            }
        }

        return JPanel(BorderLayout()).apply {
            isOpaque = true
            background = bg
            add(header, BorderLayout.NORTH)
            add(content, BorderLayout.CENTER)
            // Card sides (top/right/bottom) + a 3px left rail in place of the
            // card's left border — the same role the rail plays in the web view.
            border = CompoundBorder(
                JBUI.Borders.customLine(cardBorder, 1, 0, 1, 1),
                JBUI.Borders.customLine(rail, 0, 3, 0, 0),
            )
        }
    }

    /**
     * Header background: the editor background nudged ~6% toward the
     * foreground, reproducing the web header's `rgba(127,127,127,.06)`
     * overlay in a way that works on both light and dark schemes.
     */
    private fun headerTint(bg: Color): Color =
        ColorUtil.mix(bg, JBColor.foreground(), 0.06)
}
