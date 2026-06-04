package dev.unfold.ide

import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.TextRange
import com.intellij.openapi.vfs.VirtualFile

/** The resolved target of a call: the callee declaration's text + location. */
data class Callee(
    val title: String,
    val text: String,
    val project: Project,
    val sourceFile: VirtualFile?,
    val range: TextRange?,
)

/**
 * Renders an expanded callee frame between the lines, anchored just below
 * [anchorOffset]. Returns a [Disposable] that removes the frame.
 *
 * Implementations are the swappable experiment: [PaintedRenderer],
 * [EditorInlayRenderer] (the goal — native code), [JcefRenderer].
 */
interface FrameRenderer {
    fun render(host: Editor, anchorOffset: Int, callee: Callee): Disposable
}
