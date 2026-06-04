package dev.unfold.ide

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.editor.Inlay
import com.intellij.openapi.util.Key

/**
 * Phase 0: toggle a block inlay below the caret's line. Run via Ctrl+Alt+U or
 * the editor context menu. This proves the inlay insert/remove loop; it does
 * NOT yet resolve the real callee — wire [resolveCalleeText] in Phase 1.
 */
class ExpandCallAction : AnAction() {

    override fun actionPerformed(e: AnActionEvent) {
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val doc = editor.document
        val line = doc.getLineNumber(editor.caretModel.offset)
        val anchor = doc.getLineEndOffset(line)

        // Toggle: if we already have a demo inlay, dispose it.
        editor.getUserData(INLAY_KEY)?.let {
            if (it.isValid) it.dispose()
            editor.putUserData(INLAY_KEY, null)
            return
        }

        val body = resolveCalleeText(e)
            ?: "// Phase 1: resolve the GoCallExpr under the caret via PSI\n" +
            "// and return its function/method body text here.\n"

        val inlay = editor.inlayModel.addBlockElement(
            anchor,
            /* relatesToPrecedingText = */ true,
            /* showAbove = */ false,
            /* priority = */ 0,
            CodeBlockInlayRenderer(editor, body),
        )
        editor.putUserData(INLAY_KEY, inlay)
    }

    /**
     * TODO Phase 1: find the [com.goide.psi.GoCallExpr] under the caret,
     * resolve its callee ([com.goide.psi.GoReferenceExpression].resolve() ->
     * GoFunctionDeclaration / GoMethodDeclaration), and return its body text.
     * Interface methods: enumerate implementations and let the user pick.
     */
    private fun resolveCalleeText(e: AnActionEvent): String? = null

    companion object {
        private val INLAY_KEY = Key.create<Inlay<*>?>("unfold.demo.inlay")
    }
}
