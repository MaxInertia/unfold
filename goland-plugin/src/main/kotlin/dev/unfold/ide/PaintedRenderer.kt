package dev.unfold.ide

import com.goide.GoLanguage
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorCustomElementRenderer
import com.intellij.openapi.editor.Inlay
import com.intellij.openapi.editor.colors.EditorFontType
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.markup.TextAttributes
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import java.awt.Color
import java.awt.Graphics
import java.awt.Rectangle

/**
 * Experiment A: paint the callee as a block element, colored token-by-token
 * with the IDE's own Go SyntaxHighlighter + color scheme, so it matches the
 * surrounding code. It looks native but is a picture — no caret/selection/
 * go-to-def. Good for validating the expand/recurse mechanics.
 */
class PaintedRenderer : FrameRenderer {
    // [depth]/[recursive] are unused: the painted block is a flat picture with
    // no card chrome or rail to color, and being a picture it can't host nested
    // expansions.
    override fun render(host: Editor, anchorOffset: Int, callee: Callee, depth: Int, recursive: Boolean): Frame {
        val inlay = host.inlayModel.addBlockElement(
            anchorOffset,
            /* relatesToPrecedingText = */ true,
            /* showAbove = */ false,
            /* priority = */ 0,
            HighlightedCodeRenderer(host, callee),
        ) ?: return Frame(innerEditor = null) { }
        return Frame(innerEditor = null) { if (inlay.isValid) inlay.dispose() }
    }
}

private class HighlightedCodeRenderer(
    private val host: Editor,
    private val callee: Callee,
) : EditorCustomElementRenderer {

    private data class Seg(val text: String, val color: Color)

    private val lines: List<List<Seg>> = tokenize()

    private fun tokenize(): List<List<Seg>> {
        val scheme = host.colorsScheme
        val out = ArrayList<MutableList<Seg>>()
        out.add(ArrayList())
        val sh = SyntaxHighlighterFactory.getSyntaxHighlighter(GoLanguage.INSTANCE, callee.project, callee.sourceFile)
        if (sh == null) {
            callee.text.split("\n").forEachIndexed { i, ln ->
                if (i > 0) out.add(ArrayList())
                if (ln.isNotEmpty()) out.last().add(Seg(ln, scheme.defaultForeground))
            }
            return out
        }
        val lexer = sh.highlightingLexer
        lexer.start(callee.text)
        while (lexer.tokenType != null) {
            val tokenText = callee.text.substring(lexer.tokenStart, lexer.tokenEnd)
            val keys = sh.getTokenHighlights(lexer.tokenType)
            val color = keys.reversed()
                .firstNotNullOfOrNull { scheme.getAttributes(it)?.foregroundColor }
                ?: scheme.defaultForeground
            tokenText.split("\n").forEachIndexed { i, part ->
                if (i > 0) out.add(ArrayList())
                if (part.isNotEmpty()) out.last().add(Seg(part, color))
            }
            lexer.advance()
        }
        return out
    }

    override fun calcHeightInPixels(inlay: Inlay<*>): Int =
        host.lineHeight * lines.size.coerceAtLeast(1)

    override fun calcWidthInPixels(inlay: Inlay<*>): Int {
        val fm = host.contentComponent.getFontMetrics(host.colorsScheme.getFont(EditorFontType.PLAIN))
        val maxCols = lines.maxOfOrNull { row -> row.sumOf { it.text.length } } ?: 0
        return fm.charWidth('m') * (maxCols + 2)
    }

    override fun paint(inlay: Inlay<*>, g: Graphics, region: Rectangle, attrs: TextAttributes) {
        g.font = host.colorsScheme.getFont(EditorFontType.PLAIN)
        val fm = g.fontMetrics
        val lh = host.lineHeight
        val ascent = (host as? EditorEx)?.ascent ?: fm.ascent
        lines.forEachIndexed { i, row ->
            var x = region.x
            val y = region.y + i * lh + ascent
            for (seg in row) {
                g.color = seg.color
                g.drawString(seg.text, x, y)
                x += fm.stringWidth(seg.text)
            }
        }
    }
}
