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
    /** Callee ids expanded on the path from the root down to this controller. */
    private val ancestorIds: Set<String>,
) : Disposable {

    private val frames = HashMap<Int, Frame>()

    fun isExpanded(line: Int): Boolean = frames.containsKey(line)

    /** The inner editor of the frame expanded at [line], or null if none. */
    fun innerEditorAt(line: Int): Editor? = frames[line]?.innerEditor

    /** True if [calleeId] is already expanded somewhere above this controller. */
    fun isRecursive(calleeId: String): Boolean = calleeId in ancestorIds

    /** Collapse the frame at [line] and, transitively, everything nested in it. */
    fun collapse(line: Int) {
        frames.remove(line)?.let { Disposer.dispose(it) }
    }

    /**
     * Build a frame at [line] for the callee identified by [calleeId] via [make]
     * (passed this controller's depth and whether the callee recurses) and track
     * it. If the frame embeds an inner editor, give that editor its own depth+1
     * controller — its ancestor set extends this one with [calleeId] — and wire
     * it back to this editor/line so keyboard nav can return to the call site.
     */
    fun expand(line: Int, calleeId: String, make: (depth: Int, recursive: Boolean) -> Frame) {
        if (frames.containsKey(line)) return
        val frame = make(depth, isRecursive(calleeId))
        frames[line] = frame
        Disposer.register(this, frame)
        // Keep the map honest if the frame is disposed via a parent cascade
        // rather than collapse() (e.g. an ancestor frame collapsing).
        Disposer.register(frame) { frames.remove(line) }
        frame.innerEditor?.let { inner ->
            val child = ExpansionController(inner, depth + 1, ancestorIds + calleeId)
            Disposer.register(frame, child)
            inner.putUserData(KEY, child)
            inner.putUserData(FrameKeys.PARENT_EDITOR, editor)
            inner.putUserData(FrameKeys.CALL_LINE, line)
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
            val root = ExpansionController(editor, depth = 0, ancestorIds = emptySet())
            Disposer.register(project, root)
            editor.putUserData(KEY, root)
            return root
        }

        /** The existing controller for [editor], or null — never creates one
         *  (so it's safe to call from action `update()`). */
        fun existing(editor: Editor): ExpansionController? = editor.getUserData(KEY)
    }
}
