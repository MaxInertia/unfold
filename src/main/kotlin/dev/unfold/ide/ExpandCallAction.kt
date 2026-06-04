package dev.unfold.ide

import com.intellij.codeInsight.hint.HintManager
import com.intellij.openapi.Disposable
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.openapi.util.Disposer
import com.intellij.openapi.util.Key
import com.intellij.ui.SimpleListCellRenderer

/**
 * Resolve the Go call under the caret via PSI and render the callee body
 * between the lines with the renderer chosen in settings. Direct calls expand
 * immediately; interface/abstract method calls pop a chooser of the concrete
 * implementations (like unfold's impl switcher). Invoke again to collapse.
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

        val callees = PsiResolve.calleesAtCaret(editor, project)
        if (callees.isEmpty()) {
            HintManager.getInstance().showInformationHint(editor, "Unfold: no resolvable Go call at the caret")
            return
        }

        val line = editor.document.getLineNumber(editor.caretModel.offset)
        val anchor = editor.document.getLineEndOffset(line)

        if (callees.size == 1) {
            expand(editor, anchor, callees[0])
            return
        }

        // Interface dispatch — let the user pick the implementation.
        JBPopupFactory.getInstance()
            .createPopupChooserBuilder(callees)
            .setTitle("Implementations (${callees.size})")
            .setRenderer(SimpleListCellRenderer.create("") { c -> "${c.title}    ${c.sourceFile?.name ?: ""}" })
            .setItemChosenCallback { chosen -> expand(editor, anchor, chosen) }
            .createPopup()
            .showInBestPositionFor(e.dataContext)
    }

    private fun expand(editor: Editor, anchor: Int, callee: Callee) {
        val frame = UnfoldSettings.getInstance().renderer.create().render(editor, anchor, callee)
        editor.putUserData(FRAME_KEY, frame)
    }

    companion object {
        private val FRAME_KEY = Key.create<Disposable?>("unfold.frame")
    }
}
