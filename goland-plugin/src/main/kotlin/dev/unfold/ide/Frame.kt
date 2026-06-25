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
 * A rendered frame, produced by [EditorInlayRenderer]. Disposing it removes the
 * frame (and, through the Disposer tree, any frames nested inside it).
 * [innerEditor] is the embedded editor a nested expansion can target — non-null
 * when the frame is a real editor over the callee's on-disk file; the detached
 * snippet fallback (a callee with no source file) has none and can't host
 * native nested expansions.
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
