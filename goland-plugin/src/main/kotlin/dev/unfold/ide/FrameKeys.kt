package dev.unfold.ide

import com.intellij.openapi.editor.Editor
import com.intellij.openapi.util.Key

/**
 * User-data keys that wire an embedded frame editor back to where it came from,
 * so keyboard navigation can hop between the call site and the frame without the
 * mouse:
 *
 * - [PARENT_EDITOR] / [CALL_LINE] — set on a frame's inner editor when it is
 *   created (see [ExpansionController.expand]); they point at the host editor
 *   and the line the call sits on, so "focus parent" / "collapse this frame"
 *   work from inside the frame.
 * - [BODY_OFFSET] — set on a frame's inner editor by [EditorInlayRenderer]; the
 *   document offset of the first visible line of the callee body, so "focus
 *   frame" lands the caret on code rather than in a collapsed region.
 */
object FrameKeys {
    val PARENT_EDITOR: Key<Editor> = Key.create("unfold.frame.parentEditor")
    val CALL_LINE: Key<Int> = Key.create("unfold.frame.callLine")
    val BODY_OFFSET: Key<Int> = Key.create("unfold.frame.bodyOffset")
}
