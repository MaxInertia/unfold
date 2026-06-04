package dev.unfold.ide

import com.goide.psi.GoCallExpr
import com.goide.psi.GoFunctionOrMethodDeclaration
import com.goide.psi.GoReferenceExpression
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDocumentManager
import com.intellij.psi.PsiNamedElement
import com.intellij.psi.util.PsiTreeUtil

/** Resolve the Go call under the caret to its callee declaration via PSI. */
object PsiResolve {

    fun calleeAtCaret(editor: Editor, project: Project): Callee? {
        val psiFile = PsiDocumentManager.getInstance(project).getPsiFile(editor.document) ?: return null
        val element = psiFile.findElementAt(editor.caretModel.offset) ?: return null
        val call = PsiTreeUtil.getParentOfType(element, GoCallExpr::class.java) ?: return null
        val ref = call.expression as? GoReferenceExpression ?: return null
        // Interface methods resolve to a method spec; this lands on a concrete
        // decl for direct calls. Implementations picker is a follow-up.
        val decl = ref.resolve() as? GoFunctionOrMethodDeclaration ?: return null
        return Callee(
            title = (decl as? PsiNamedElement)?.name ?: "callee",
            text = decl.text,
            project = project,
            sourceFile = decl.containingFile.virtualFile,
            range = decl.textRange,
        )
    }
}
