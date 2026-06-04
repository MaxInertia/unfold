package dev.unfold.ide

import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorCustomElementRenderer
import com.intellij.openapi.editor.Inlay
import com.intellij.openapi.editor.colors.EditorFontType
import com.intellij.openapi.editor.markup.TextAttributes
import java.awt.Graphics
import java.awt.Rectangle

/**
 * Phase 0 / experiment "A": paint multi-line text as a block element between
 * the lines, using the editor's font and default foreground.
 *
 * Phase 3A: replace the flat [paint] with per-token coloring driven by the
 * IDE's SyntaxHighlighter + EditorColorsScheme so the inserted code matches the
 * surrounding code exactly. (SyntaxHighlighterFactory.getSyntaxHighlighter(...)
 * -> getHighlightingLexer(); map each token's TextAttributesKey to the scheme.)
 *
 * Experiment "B" replaces this renderer entirely with a real embedded EditorEx
 * viewer (native highlighting + selection + go-to-def + nested expansion).
 */
class CodeBlockInlayRenderer(
    private val editor: Editor,
    private val text: String,
) : EditorCustomElementRenderer {

    private val lines = text.split("\n")

    override fun calcHeightInPixels(inlay: Inlay<*>): Int =
        editor.lineHeight * lines.size.coerceAtLeast(1)

    override fun calcWidthInPixels(inlay: Inlay<*>): Int {
        val cols = lines.maxOfOrNull { it.length } ?: 0
        val fm = editor.contentComponent.getFontMetrics(editor.colorsScheme.getFont(EditorFontType.PLAIN))
        return fm.charWidth('m') * (cols + 2)
    }

    override fun paint(inlay: Inlay<*>, g: Graphics, region: Rectangle, attrs: TextAttributes) {
        val scheme = editor.colorsScheme
        g.font = scheme.getFont(EditorFontType.PLAIN)
        g.color = scheme.defaultForeground
        val lh = editor.lineHeight
        val ascent = (editor as? com.intellij.openapi.editor.ex.EditorEx)?.ascent ?: lh
        lines.forEachIndexed { i, ln ->
            g.drawString(ln, region.x, region.y + i * lh + ascent)
        }
    }
}
