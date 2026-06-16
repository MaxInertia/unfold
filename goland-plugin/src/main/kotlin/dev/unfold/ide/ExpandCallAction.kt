package dev.unfold.ide

import com.intellij.codeInsight.hint.HintManager
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.ui.SimpleListCellRenderer

/**
 * Resolve the Go call under the caret via PSI and render the callee body
 * between the lines with the renderer chosen in settings. Direct calls expand
 * immediately; interface/abstract method calls pop a chooser of the concrete
 * implementations (like unfold's impl switcher). Invoke again on the same line
 * to collapse.
 *
 * The action targets whichever editor holds focus — including an embedded frame
 * editor. Because that editor is a real editor over the callee file, resolving
 * and expanding a call *inside* a frame nests another frame (one rail color
 * deeper); collapsing the outer frame collapses the whole subtree. See
 * [ExpansionController].
 */
class ExpandCallAction : AnAction() {

    override fun actionPerformed(e: AnActionEvent) {
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val project = e.project ?: return
        val controller = ExpansionController.of(editor, project)
        val line = editor.document.getLineNumber(editor.caretModel.offset)

        // Toggle off if this call site (and anything nested under it) is showing.
        if (controller.isExpanded(line)) {
            controller.collapse(line)
            return
        }

        val callees = PsiResolve.calleesAtCaret(editor, project)
        if (callees.isEmpty()) {
            HintManager.getInstance().showInformationHint(editor, "Unfold: no resolvable Go call at the caret")
            return
        }

        val anchor = editor.document.getLineEndOffset(line)

        if (callees.size == 1) {
            expand(controller, editor, line, anchor, callees[0])
            return
        }

        // Interface dispatch — let the user pick the implementation.
        JBPopupFactory.getInstance()
            .createPopupChooserBuilder(callees)
            .setTitle("Implementations (${callees.size})")
            .setRenderer(SimpleListCellRenderer.create("") { c -> "${c.title}    ${c.sourceFile?.name ?: ""}" })
            .setItemChosenCallback { chosen -> expand(controller, editor, line, anchor, chosen) }
            .createPopup()
            .showInBestPositionFor(e.dataContext)
    }

    private fun expand(controller: ExpansionController, editor: Editor, line: Int, anchor: Int, callee: Callee) {
        val renderer = UnfoldSettings.getInstance().renderer
        controller.expand(line) { depth -> renderer.create().render(editor, anchor, callee, depth) }
    }
}
