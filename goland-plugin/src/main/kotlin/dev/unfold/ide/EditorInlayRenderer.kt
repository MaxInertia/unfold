package dev.unfold.ide

import com.goide.GoFileType
import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorFactory
import com.intellij.openapi.editor.Inlay
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.highlighter.EditorHighlighterFactory
import com.intellij.openapi.editor.event.VisibleAreaListener
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.openapi.util.Disposer
import com.intellij.testFramework.LightVirtualFile
import java.awt.Dimension

/**
 * Experiment B — the goal. Embed a real read-only editor over the **real
 * callee file's document**, folded so only the function range shows. Because
 * the editor's PSI is the actual project file, the frame gets native semantic
 * colors, hover/quick-doc, go-to-definition, find-usages, and folding — the
 * same features as the surrounding code.
 *
 * The frame height tracks the function's *visual* extent (fold-aware) and
 * re-fits on every fold change, so folding the function shrinks the frame
 * instead of leaving empty space / revealing the rest of the file.
 */
class EditorInlayRenderer : FrameRenderer {

    override fun render(host: Editor, anchorOffset: Int, callee: Callee): Disposable {
        val vf = callee.sourceFile
        val range = callee.range
        if (vf == null || range == null) return renderDetached(host, anchorOffset, callee)
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
            isFoldingOutlineShown = true
            isCaretRowShown = false
            isRightMarginShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
        }
        sub.setVerticalScrollbarVisible(false)
        sub.setHorizontalScrollbarVisible(false)

        // Collapse everything outside the function's line range.
        val startLine = document.getLineNumber(range.startOffset)
        val endLine = document.getLineNumber(range.endOffset.coerceAtMost(document.textLength))
        val funcStart = document.getLineStartOffset(startLine)
        val funcEnd = document.getLineEndOffset(endLine)
        sub.foldingModel.runBatchFoldingOperation {
            if (funcStart > 0) sub.foldingModel.addFoldRegion(0, funcStart, "")?.isExpanded = false
            if (funcEnd < document.textLength) sub.foldingModel.addFoldRegion(funcEnd, document.textLength, "")?.isExpanded = false
        }

        // Height = visual lines from the top through the end of the function,
        // i.e. up to but not including the trailing collapsed remainder.
        fun fittedHeight(): Int =
            host.lineHeight * (sub.offsetToVisualPosition(funcEnd).line + 1).coerceAtLeast(1)

        sub.component.preferredSize = Dimension(
            sub.component.preferredSize.width.coerceAtLeast(600),
            fittedHeight(),
        )

        var inlay: Inlay<*>? = EditorEmbeddedComponentManager.getInstance().addComponent(
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

        // Re-fit whenever the visible area changes — folding/unfolding inside
        // the frame fires this, so folding the function shrinks the frame
        // rather than revealing the file below it.
        val listenerLifetime = Disposer.newDisposable()
        sub.scrollingModel.addVisibleAreaListener(
            VisibleAreaListener {
                val h = fittedHeight()
                if (sub.component.preferredSize.height != h) {
                    sub.component.preferredSize = Dimension(sub.component.preferredSize.width, h)
                    sub.component.revalidate()
                    inlay?.update()
                }
            },
            listenerLifetime,
        )

        return Disposable {
            Disposer.dispose(listenerLifetime)
            inlay?.dispose()
            inlay = null
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }

    /** Fallback when the callee has no on-disk file: a detached Go snippet
     *  (native font + lexer colors, but no semantic analysis). */
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
