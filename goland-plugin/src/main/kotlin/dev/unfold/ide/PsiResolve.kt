package dev.unfold.ide

import com.goide.psi.GoCallExpr
import com.goide.psi.GoFunctionOrMethodDeclaration
import com.goide.psi.GoMethodDeclaration
import com.goide.psi.GoReferenceExpression
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDocumentManager
import com.intellij.psi.PsiNamedElement
import com.intellij.psi.search.searches.DefinitionsScopedSearch
import com.intellij.psi.util.PsiTreeUtil

/** Resolve the Go call under the caret to one or more callee declarations. */
object PsiResolve {

    /**
     * Returns the expandable targets for the call at the caret:
     * - empty  — no resolvable call (indirect, builtin, unresolved)
     * - one    — a direct function/method call
     * - many   — an interface/abstract method call; each concrete
     *            implementation (like unfold's impl switcher).
     */
    fun calleesAtCaret(editor: Editor, project: Project): List<Callee> {
        val psiFile = PsiDocumentManager.getInstance(project).getPsiFile(editor.document) ?: return emptyList()
        val element = psiFile.findElementAt(editor.caretModel.offset) ?: return emptyList()
        val call = PsiTreeUtil.getParentOfType(element, GoCallExpr::class.java) ?: return emptyList()
        val ref = call.expression as? GoReferenceExpression ?: return emptyList()
        val resolved = ref.resolve() ?: return emptyList()

        // Direct call: a function/method that has a body.
        if (resolved is GoFunctionOrMethodDeclaration && resolved.block != null) {
            return listOf(calleeOf(resolved, project))
        }

        // Interface/abstract method: enumerate the concrete implementations
        // (same search "Go to Implementation" uses).
        return DefinitionsScopedSearch.search(resolved).findAll()
            .filterIsInstance<GoFunctionOrMethodDeclaration>()
            .filter { it.block != null }
            .map { calleeOf(it, project) }
            .sortedBy { it.title }
    }

    private fun calleeOf(decl: GoFunctionOrMethodDeclaration, project: Project): Callee {
        val vf = decl.containingFile?.virtualFile
        val range = decl.textRange
        return Callee(
            title = titleOf(decl),
            text = decl.text,
            project = project,
            sourceFile = vf,
            range = range,
            id = "${vf?.path ?: "?"}#${range?.startOffset ?: -1}",
        )
    }

    private fun titleOf(decl: GoFunctionOrMethodDeclaration): String {
        val name = (decl as? PsiNamedElement)?.name ?: "fn"
        val recv = (decl as? GoMethodDeclaration)?.receiver?.type?.text?.trimStart('*')
        return if (recv.isNullOrBlank()) name else "$recv.$name"
    }
}
