package dev.unfold.ide

import com.goide.GoFileType
import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.EditorFactory
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.editor.highlighter.EditorHighlighterFactory
import com.intellij.openapi.editor.impl.EditorEmbeddedComponentManager
import com.intellij.testFramework.LightVirtualFile

/**
 * Experiment B — the goal. Embed a real read-only editor between the lines so
 * the expanded code uses the IDE's own rendering: native syntax colors, font,
 * caret, selection, copy, hover. Because it IS an editor, "supports the same
 * stuff" largely comes for free.
 *
 * Runtime note: this compiles against the SDK but the embed bounds / scroll
 * sync and sizing will need iteration in a running IDE. To also get go-to-def
 * etc. resolving, back the sub-editor with the real callee file (shown as a
 * range) rather than a detached copy — a deeper follow-up.
 */
class EditorInlayRenderer : FrameRenderer {
    override fun render(host: Editor, anchorOffset: Int, callee: Callee): Disposable {
        val project = callee.project

        // Detached Go-typed document → native Go highlighting, font, selection.
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
            isCaretRowShown = false
            isRightMarginShown = false
            additionalLinesCount = 0
            additionalColumnsCount = 0
        }
        // Size to the content (rough; refine at runtime).
        val lineCount = document.lineCount.coerceAtLeast(1)
        sub.component.preferredSize = java.awt.Dimension(
            sub.component.preferredSize.width,
            host.lineHeight * lineCount,
        )

        val manager = EditorEmbeddedComponentManager.getInstance()
        val properties = EditorEmbeddedComponentManager.Properties(
            EditorEmbeddedComponentManager.ResizePolicy.none(),
            null,
            /* relatesToPrecedingText = */ true,
            /* showAbove = */ false,
            /* priority = */ 0,
            anchorOffset,
        )
        val inlay = manager.addComponent(host as EditorEx, sub.component, properties)

        return Disposable {
            inlay?.dispose()
            EditorFactory.getInstance().releaseEditor(sub)
        }
    }
}
