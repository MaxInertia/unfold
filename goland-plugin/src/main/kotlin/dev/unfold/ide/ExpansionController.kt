package dev.unfold.ide

import com.intellij.openapi.Disposable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Disposer
import com.intellij.openapi.util.Key

/**
 * The expansion model for one editor: the frames it owns, keyed by the document
 * line the call sits on (so sibling calls on different lines expand
 * independently and re-invoking on the same line collapses).
 *
 * Nesting is a tree of controllers. The host editor gets a depth-0 controller.
 * Every [EditorInlayRenderer] frame is itself a real editor over the callee
 * file, so it gets its *own* controller at depth+1, seeded into that editor's
 * user data — expanding a call inside a frame nests another frame with the next
 * rail color. The child controller is a Disposer child of the frame, so
 * collapsing a frame tears down its whole subtree in one shot.
 */
class ExpansionController private constructor(
    private val editor: Editor,
    val depth: Int,
) : Disposable {

    private val frames = HashMap<Int, Frame>()

    fun isExpanded(line: Int): Boolean = frames.containsKey(line)

    /** Collapse the frame at [line] and, transitively, everything nested in it. */
    fun collapse(line: Int) {
        frames.remove(line)?.let { Disposer.dispose(it) }
    }

    /**
     * Build a frame at [line] via [make] (passed this controller's depth) and
     * track it. If the frame embeds an inner editor, give that editor its own
     * depth+1 controller parented to the frame, so nested expansions collapse
     * with their parent and pick up the next rail color.
     */
    fun expand(line: Int, make: (depth: Int) -> Frame) {
        if (frames.containsKey(line)) return
        val frame = make(depth)
        frames[line] = frame
        Disposer.register(this, frame)
        // Keep the map honest if the frame is disposed via a parent cascade
        // rather than collapse() (e.g. an ancestor frame collapsing).
        Disposer.register(frame) { frames.remove(line) }
        frame.innerEditor?.let { inner ->
            val child = ExpansionController(inner, depth + 1)
            Disposer.register(frame, child)
            inner.putUserData(KEY, child)
        }
    }

    override fun dispose() {
        // Registered frames cascade via the Disposer tree; just drop references.
        frames.clear()
    }

    companion object {
        private val KEY = Key.create<ExpansionController>("unfold.expansion.controller")

        /**
         * The controller for [editor], lazily creating a depth-0 root the first
         * time a host editor is expanded. Embedded frame editors are pre-seeded
         * with their own controller in [expand], so they never hit this path —
         * keeping their non-zero depth intact.
         */
        fun of(editor: Editor, project: Project): ExpansionController {
            editor.getUserData(KEY)?.let { return it }
            val root = ExpansionController(editor, depth = 0)
            Disposer.register(project, root)
            editor.putUserData(KEY, root)
            return root
        }
    }
}
