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
    /**
     * A stable identity for the *declaration* (file path + start offset), used
     * to detect recursion: a frame whose callee id already appears among its
     * ancestor frames is a recursive expansion. Independent of [title], which
     * can collide across packages.
     */
    val id: String,
)

/**
 * A rendered frame. Disposing it removes the frame (and, through the Disposer
 * tree, any frames nested inside it). [innerEditor] is the embedded editor a
 * nested expansion can target — non-null only for [EditorInlayRenderer], whose
 * frame is a real editor over the callee file; painted/JCEF frames are pictures
 * and can't host native nested expansions.
 */
class Frame(
    val innerEditor: Editor?,
    private val onDispose: () -> Unit,
) : Disposable {
    private var disposed = false

    override fun dispose() {
        if (disposed) return
        disposed = true
        onDispose()
    }
}

/**
 * Renders an expanded callee frame between the lines, anchored just below
 * [anchorOffset], at nesting [depth] (drives the card's rail color). When
 * [recursive] is true the callee is already expanded higher in the stack, so
 * the chrome flags it (and the caller may choose not to descend further).
 * Returns a [Frame] that removes it.
 *
 * Implementations are the swappable experiment: [PaintedRenderer],
 * [EditorInlayRenderer] (the goal — native code), [JcefRenderer].
 */
interface FrameRenderer {
    fun render(host: Editor, anchorOffset: Int, callee: Callee, depth: Int, recursive: Boolean): Frame
}
