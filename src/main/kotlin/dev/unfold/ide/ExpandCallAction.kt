package dev.unfold.ide

import com.intellij.codeInsight.hint.HintManager
import com.intellij.openapi.Disposable
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.util.Disposer
import com.intellij.openapi.util.Key

/**
 * Resolve the Go call under the caret via PSI, then render the callee body
 * between the lines using the renderer selected in settings (Painted /
 * embedded Editor / JCEF). Invoke again to collapse.
 *
 * Single-frame for now; recursion/nesting + per-call affordances are the next
 * step (see PLAN.md phases 2 and 4).
 */
class ExpandCallAction : AnAction() {

    override fun actionPerformed(e: AnActionEvent) {
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val project = e.project ?: return

        // Toggle off if a frame is already showing.
        editor.getUserData(FRAME_KEY)?.let { existing ->
            Disposer.dispose(existing)
            editor.putUserData(FRAME_KEY, null)
            return
        }

        val callee = PsiResolve.calleeAtCaret(editor, project)
        if (callee == null) {
            HintManager.getInstance().showInformationHint(editor, "Unfold: no resolvable Go call at the caret")
            return
        }

        val line = editor.document.getLineNumber(editor.caretModel.offset)
        val anchor = editor.document.getLineEndOffset(line)
        val frame = UnfoldSettings.getInstance().renderer.create().render(editor, anchor, callee)
        editor.putUserData(FRAME_KEY, frame)
    }

    companion object {
        private val FRAME_KEY = Key.create<Disposable?>("unfold.frame")
    }
}
