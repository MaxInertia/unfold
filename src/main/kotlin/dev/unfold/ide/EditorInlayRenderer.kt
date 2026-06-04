package dev.unfold.ide

import com.goide.GoFileType
import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorFactory
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.highlighter.EditorHighlighterFactory
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.testFramework.LightVirtualFile
import java.awt.Dimension

/**
 * Experiment B — the goal. Embed a real read-only editor over the **real
 * callee file's document**, folded to just the function range. Because the
 * editor's PSI is the actual project file, you get native semantic colors
 * (function names, types, …), hover/quick-doc, go-to-definition, find-usages,
 * and folding — the same features as the surrounding code — not just the
 * lexer coloring a detached snippet gets.
 *
 * Falls back to a detached snippet when the callee has no on-disk file
 * (e.g. generated/library code).
 *
 * Runtime note: compiles against the SDK; the fold/size/scroll bounds will
 * want tuning in a running IDE.
 */
class EditorInlayRenderer : FrameRenderer {

    override fun render(host: Editor, anchorOffset: Int, callee: Callee): Disposable {
        val vf = callee.sourceFile
        val range = callee.range
        if (vf == null || range == null) {
            return renderDetached(host, anchorOffset, callee)
        }
        val document = FileDocumentManager.getInstance().getDocument(vf)
            ?: return renderDetached(host, anchorOffset, callee)

        val project = callee.project
        val sub = EditorFactory.getInstance().createViewer(document, project) as EditorEx
        sub.highlighter = EditorHighlighterFactory.getInstance().createEditorHighlighter(project, vf)
        sub.backgroundColor = host.colorsScheme.defaultBackground
        sub.setBorder(null)
        sub.settings.apply {
            isLineNumbersShown = false
            isLineMarkerAreaShown = false
            isFoldingOutlineShown = true // real folding within the frame
            isCaretRowShown = false
            isRightMarginShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
        }
        sub.setVerticalScrollbarVisible(false)
        sub.setHorizontalScrollbarVisible(false)

        // Show only the function: fold everything before/after its line range.
        val startLine = document.getLineNumber(range.startOffset)
        val endLine = document.getLineNumber(range.endOffset.coerceAtMost(document.textLength))
        val funcStart = document.getLineStartOffset(startLine)
        val funcEnd = document.getLineEndOffset(endLine)
        var topFold = false
        var bottomFold = false
        sub.foldingModel.runBatchFoldingOperation {
            if (funcStart > 0) {
                sub.foldingModel.addFoldRegion(0, funcStart, "")?.let { it.isExpanded = false; topFold = true }
            }
            if (funcEnd < document.textLength) {
                sub.foldingModel.addFoldRegion(funcEnd, document.textLength, "")?.let { it.isExpanded = false; bottomFold = true }
            }
        }

        val rows = (endLine - startLine + 1) + (if (topFold) 1 else 0) + (if (bottomFold) 1 else 0)
        sub.component.preferredSize = Dimension(
            sub.component.preferredSize.width.coerceAtLeast(600),
            host.lineHeight * rows.coerceAtLeast(1),
        )

        val inlay = EditorEmbeddedComponentManager.getInstance().addComponent(
            host as EditorEx,
            sub.component,
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
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }

    /** Fallback: a detached Go snippet — native font + lexer colors, but no
     *  semantic analysis (used only when there's no real file to back it). */
    private fun renderDetached(host: Editor, anchorOffset: Int, callee: Callee): Disposable {
        val project = callee.project
        val vf = LightVirtualFile("unfold-frame.go", GoFileType.INSTANCE, callee.text)
        val document = EditorFactory.getInstance().createDocument(callee.text)
        val sub = EditorFactory.getInstance().createViewer(document, project) as EditorEx
        sub.setFile(vf)
        sub.highlighter = EditorHighlighterFactory.getInstance().createEditorHighlighter(project, vf)
        sub.backgroundColor = host.colorsScheme.defaultBackground
        sub.setBorder(null)
        sub.settings.apply {
            isLineNumbersShown = false
            isLineMarkerAreaShown = false
            isFoldingOutlineShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
        }
        sub.component.preferredSize = Dimension(
            sub.component.preferredSize.width.coerceAtLeast(600),
            host.lineHeight * document.lineCount.coerceAtLeast(1),
        )
        val inlay = EditorEmbeddedComponentManager.getInstance().addComponent(
            host as EditorEx,
            sub.component,
            EditorEmbeddedComponentManager.Properties(
                EditorEmbeddedComponentManager.ResizePolicy.none(), null, true, false, 0, anchorOffset,
            ),
        )
        return Disposable {
            inlay?.dispose()
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }
}
