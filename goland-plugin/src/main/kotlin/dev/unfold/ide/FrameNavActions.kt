package dev.unfold.ide

import com.intellij.openapi.actionSystem.ActionUpdateThread
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.wm.IdeFocusManager

/**
 * Keyboard navigation across the frame stack, so a trace can be read without the
 * mouse:
 *
 * - [FocusFrameAction] — descend from a call site into the frame expanded on
 *   that line (caret lands on the callee body).
 * - [FocusParentAction] — ascend from inside a frame back to its call site in
 *   the parent editor.
 * - [CollapseFrameAction] — collapse the frame you're inside (focus hops to the
 *   parent first, since collapsing disposes the current editor).
 *
 * The parent/line/body links come from [FrameKeys], seeded when a frame is
 * created in [ExpansionController.expand] and [EditorInlayRenderer].
 */

/** Move focus from a call site down into the frame expanded on the caret line. */
class FocusFrameAction : AnAction() {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabled = innerFrameAtCaret(e) != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val inner = innerFrameAtCaret(e) ?: return
        inner.getUserData(FrameKeys.BODY_OFFSET)?.let {
            inner.caretModel.moveToOffset(it.coerceIn(0, inner.document.textLength))
        }
        IdeFocusManager.getInstance(project).requestFocus(inner.contentComponent, true)
    }

    private fun innerFrameAtCaret(e: AnActionEvent): Editor? {
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return null
        val controller = ExpansionController.existing(editor) ?: return null
        val line = editor.document.getLineNumber(editor.caretModel.offset)
        return controller.innerEditorAt(line)
    }
}

/** Move focus from inside a frame back to its call site in the parent editor. */
class FocusParentAction : AnAction() {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabled = e.getData(CommonDataKeys.EDITOR)?.getUserData(FrameKeys.PARENT_EDITOR) != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val parent = editor.getUserData(FrameKeys.PARENT_EDITOR) ?: return
        focusCallSite(project, parent, editor.getUserData(FrameKeys.CALL_LINE))
    }
}

/** Collapse the frame the caret is inside, returning focus to the parent. */
class CollapseFrameAction : AnAction() {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabled = e.getData(CommonDataKeys.EDITOR)?.getUserData(FrameKeys.PARENT_EDITOR) != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val parent = editor.getUserData(FrameKeys.PARENT_EDITOR) ?: return
        val callLine = editor.getUserData(FrameKeys.CALL_LINE) ?: return
        // Hop focus out before disposing this editor, then collapse from the
        // parent's controller (the one that owns this frame).
        focusCallSite(project, parent, callLine)
        (ExpansionController.existing(parent) ?: ExpansionController.of(parent, project)).collapse(callLine)
    }
}

/** Put the caret on [line] of [parent] (if known) and focus that editor. */
private fun focusCallSite(project: com.intellij.openapi.project.Project, parent: Editor, line: Int?) {
    if (line != null && parent.document.lineCount > 0) {
        parent.caretModel.moveToOffset(parent.document.getLineStartOffset(line.coerceIn(0, parent.document.lineCount - 1)))
    }
    IdeFocusManager.getInstance(project).requestFocus(parent.contentComponent, true)
}
