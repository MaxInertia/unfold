package dev.unfold.ide

import com.intellij.openapi.actionSystem.IdeActions
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorFactory
import com.intellij.openapi.editor.Inlay
import com.intellij.openapi.editor.InlayModel
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.ex.FoldingListener
import com.intellij.openapi.editor.highlighter.EditorHighlighterFactory
import com.intellij.openapi.editor.event.VisibleAreaListener
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.openapi.fileEditor.OpenFileDescriptor
import com.intellij.openapi.util.Disposer
import com.intellij.testFramework.LightVirtualFile
import java.awt.Dimension

/**
 * Embeds a real read-only editor over the **real callee file's document**,
 * folded so only the function range shows. Because
 * the editor's PSI is the actual project file, the frame gets native semantic
 * colors, hover/quick-doc, go-to-definition, find-usages, and folding — the
 * same features as the surrounding code.
 *
 * The frame height tracks the function's *visual* extent (fold-aware) and
 * re-fits on every fold change, so folding the function shrinks the frame
 * instead of leaving empty space / revealing the rest of the file.
 */
class EditorInlayRenderer {

    fun render(host: Editor, anchorOffset: Int, callee: Callee, depth: Int, recursive: Boolean): Frame {
        val vf = callee.sourceFile
        val range = callee.range
        if (vf == null || range == null) return renderDetached(host, anchorOffset, callee, depth, recursive)
        val document = FileDocumentManager.getInstance().getDocument(vf)
            ?: return renderDetached(host, anchorOffset, callee, depth, recursive)

        val project = callee.project
        val sub = EditorFactory.getInstance().createViewer(document, project) as EditorEx
        sub.highlighter = EditorHighlighterFactory.getInstance().createEditorHighlighter(project, vf)
        sub.backgroundColor = host.colorsScheme.defaultBackground
        sub.setBorder(null)
        // Give the frame the standard editor right-click menu (a bare viewer has
        // none — hence the "Nothing here" popup), so copy/go-to/find-usages are
        // reachable from the context menu, not just keybindings.
        sub.setContextMenuGroupId(IdeActions.GROUP_EDITOR_POPUP)
        sub.settings.apply {
            isLineNumbersShown = false
            isLineMarkerAreaShown = false
            isFoldingOutlineShown = true
            isCaretRowShown = false
            isRightMarginShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
            // Folding stays ON so the user can collapse blocks *inside* the
            // frame (Phase 5). The daemon's code-folding pass rebuilds fold
            // regions from the language FoldingBuilder ~1s after the editor
            // settles — discarding our boundary folds and sometimes
            // auto-collapsing the function body (which would drop funcEnd to
            // visual line 0 and shrink the frame to one line). Rather than
            // disabling folding wholesale, we re-assert the boundary folds after
            // every fold-processing pass via the FoldingListener installed
            // below.
            isAutoCodeFoldingEnabled = true
        }
        sub.setVerticalScrollbarVisible(false)
        sub.setHorizontalScrollbarVisible(false)

        // Collapse everything outside the function's line range.
        val startLine = document.getLineNumber(range.startOffset)
        val endLine = document.getLineNumber(range.endOffset.coerceAtMost(document.textLength))
        val funcStart = document.getLineStartOffset(startLine)
        val funcEnd = document.getLineEndOffset(endLine)

        // Re-asserting our boundary folds runs its own batch operation, which
        // fires onFoldProcessingEnd again — guard against re-entering.
        var reasserting = false

        /**
         * Keep only the function visible: collapse [0,funcStart] and
         * [funcEnd,end], and force-expand any language region that encloses the
         * whole function (else collapsing the body would shrink the frame). Safe
         * to call repeatedly; idempotent on already-correct state.
         */
        fun applyBoundaryFolds() {
            if (reasserting) return
            reasserting = true
            try {
                sub.foldingModel.runBatchFoldingOperation {
                    val fm = sub.foldingModel
                    // A region spanning the whole function body would hide it
                    // when collapsed — keep those open.
                    for (r in fm.allFoldRegions) {
                        if (r.startOffset <= funcStart && r.endOffset >= funcEnd &&
                            (r.startOffset < funcStart || r.endOffset > funcEnd) && !r.isExpanded
                        ) {
                            r.isExpanded = true
                        }
                    }
                    ensureCollapsed(sub, 0, funcStart)
                    ensureCollapsed(sub, funcEnd, document.textLength)
                }
            } finally {
                reasserting = false
            }
        }

        applyBoundaryFolds()

        // "Focus frame" should land the caret on real code, not in the leading
        // collapsed region.
        sub.putUserData(FrameKeys.BODY_OFFSET, funcStart)

        // Height = visual lines from the top through the end of the function
        // (up to but not including the trailing collapsed remainder), PLUS the
        // pixel height of any block inlays nested inside the function range —
        // i.e. child frames. A visual-line count alone can't see those inlay
        // pixels, so without this the card wouldn't grow to fit a nested
        // expansion (it would render clipped/overlapping).
        fun fittedHeight(): Int {
            val lines = host.lineHeight * (sub.offsetToVisualPosition(funcEnd).line + 1).coerceAtLeast(1)
            val nested = sub.inlayModel.getBlockElementsInRange(funcStart, funcEnd).sumOf { it.heightInPixels }
            return lines + nested
        }

        sub.component.preferredSize = Dimension(
            sub.component.preferredSize.width.coerceAtLeast(600),
            fittedHeight(),
        )

        // Wrap the native editor in web-style card chrome (header with the
        // callee title + file:line, a thin card border, and a depth-colored
        // left rail). The file:line is a link that jumps the main IDE to the
        // callee definition; a recursion badge flags re-expanded callees. The
        // card's preferred size = header + this content.
        val navigate: () -> Unit = {
            OpenFileDescriptor(project, vf, range.startOffset).navigate(true)
        }
        val card = FrameChrome.wrap(
            host, sub.component, callee.title, FrameChrome.location(callee),
            depth = depth, recursive = recursive, onNavigate = navigate,
        )

        var inlay: Inlay<*>? = EditorEmbeddedComponentManager.getInstance().addComponent(
            host as EditorEx,
            card,
            EditorEmbeddedComponentManager.Properties(
                EditorEmbeddedComponentManager.ResizePolicy.none(),
                null,
                /* relatesToPrecedingText = */ true,
                /* showAbove = */ false,
                /* priority = */ 0,
                anchorOffset,
            ),
        )

        // Recompute the card height and push the new size up to our own inlay.
        fun refit() {
            val h = fittedHeight()
            if (sub.component.preferredSize.height != h) {
                sub.component.preferredSize = Dimension(sub.component.preferredSize.width, h)
                sub.component.revalidate()
                card.revalidate()
                inlay?.update()
            }
        }

        val listenerLifetime = Disposer.newDisposable()
        // Folding/unfolding inside the frame changes the visible area — re-fit so
        // folding the function shrinks the frame rather than revealing the file
        // below it.
        sub.scrollingModel.addVisibleAreaListener(VisibleAreaListener { refit() }, listenerLifetime)
        // A nested expansion adds (or collapse removes) a block inlay inside this
        // editor; grow/shrink the card to fit it. onUpdated also fires when a
        // *deeper* nested frame resizes its own inlay, so this re-fit propagates
        // all the way up the frame stack.
        sub.inlayModel.addListener(
            object : InlayModel.Listener {
                override fun onAdded(inlay: Inlay<*>) = refit()
                override fun onUpdated(inlay: Inlay<*>) = refit()
                override fun onRemoved(inlay: Inlay<*>) = refit()
            },
            listenerLifetime,
        )
        // The daemon's code-folding pass discards our boundary folds (and may
        // auto-collapse the body); re-assert them after every pass, then re-fit.
        // `applyBoundaryFolds` guards against the re-assert's own pass.
        sub.foldingModel.addListener(
            object : FoldingListener {
                override fun onFoldProcessingEnd() {
                    applyBoundaryFolds()
                    refit()
                }
            },
            listenerLifetime,
        )

        // The embedded editor is a real editor over the callee file, so it can
        // host nested expansions — expose it as the frame's inner editor.
        return Frame(innerEditor = sub) {
            Disposer.dispose(listenerLifetime)
            inlay?.dispose()
            inlay = null
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }

    /**
     * Ensure a collapsed fold region with exactly the bounds [start,end] exists;
     * reuse one already at those bounds, otherwise add it. Must run inside a
     * batch folding operation.
     */
    private fun ensureCollapsed(editor: EditorEx, start: Int, end: Int) {
        if (end <= start) return
        val fm = editor.foldingModel
        val existing = fm.allFoldRegions.firstOrNull { it.startOffset == start && it.endOffset == end }
        val region = existing ?: fm.addFoldRegion(start, end, "")
        region?.isExpanded = false
    }

    /** Fallback when the callee has no on-disk file: a detached snippet in the
     *  callee's language (native font + lexer colors, but no semantic analysis,
     *  so no nesting). */
    private fun renderDetached(host: Editor, anchorOffset: Int, callee: Callee, depth: Int, recursive: Boolean): Frame {
        val project = callee.project
        val vf = LightVirtualFile("unfold-frame.${callee.fileType.defaultExtension}", callee.fileType, callee.text)
        val document = EditorFactory.getInstance().createDocument(callee.text)
        val sub = EditorFactory.getInstance().createViewer(document, project) as EditorEx
        sub.setFile(vf)
        sub.highlighter = EditorHighlighterFactory.getInstance().createEditorHighlighter(project, vf)
        sub.backgroundColor = host.colorsScheme.defaultBackground
        sub.setBorder(null)
        sub.setContextMenuGroupId(IdeActions.GROUP_EDITOR_POPUP)
        sub.settings.apply {
            isLineNumbersShown = false
            isLineMarkerAreaShown = false
            isFoldingOutlineShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
            isAutoCodeFoldingEnabled = false
        }
        sub.component.preferredSize = Dimension(
            sub.component.preferredSize.width.coerceAtLeast(600),
            host.lineHeight * document.lineCount.coerceAtLeast(1),
        )
        val card = FrameChrome.wrap(
            host, sub.component, callee.title, FrameChrome.location(callee),
            depth = depth, recursive = recursive,
        )
        val inlay = EditorEmbeddedComponentManager.getInstance().addComponent(
            host as EditorEx,
            card,
            EditorEmbeddedComponentManager.Properties(
                EditorEmbeddedComponentManager.ResizePolicy.none(), null, true, false, 0, anchorOffset,
            ),
        )
        // Detached snippet has no PSI file behind it, so nested calls can't
        // resolve — don't expose an inner editor.
        return Frame(innerEditor = null) {
            inlay?.dispose()
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }
}
