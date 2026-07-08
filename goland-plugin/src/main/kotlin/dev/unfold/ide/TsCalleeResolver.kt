package dev.unfold.ide

import com.intellij.lang.javascript.psi.JSCallExpression
import com.intellij.lang.javascript.psi.ecmal4.JSClass
import com.intellij.lang.javascript.psi.JSFunction
import com.intellij.lang.javascript.psi.JSReferenceExpression
import com.intellij.lang.javascript.psi.JSVariable
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.fileTypes.PlainTextFileType
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDocumentManager
import com.intellij.psi.PsiElement
import com.intellij.psi.search.searches.DefinitionsScopedSearch
import com.intellij.psi.util.PsiTreeUtil

/**
 * Resolve the TypeScript/JavaScript call under the caret to one or more callee
 * functions. Mirrors [GoCalleeResolver]'s three outcomes (none / one direct /
 * many implementations), but over JS PSI.
 *
 * Class-loaded only when a JS/TS file is under the caret (see [PsiResolve]), so
 * referencing `com.intellij.lang.javascript` here is safe even in an IDE without
 * the JavaScript plugin.
 */
object TsCalleeResolver : CalleeResolver {

    override fun calleesAtCaret(editor: Editor, project: Project): List<Callee> {
        val psiFile = PsiDocumentManager.getInstance(project).getPsiFile(editor.document) ?: return emptyList()
        val element = psiFile.findElementAt(editor.caretModel.offset) ?: return emptyList()
        val call = PsiTreeUtil.getParentOfType(element, JSCallExpression::class.java) ?: return emptyList()
        val ref = call.methodExpression as? JSReferenceExpression ?: return emptyList()
        val fn = resolveToFunction(ref.resolve()) ?: return emptyList()

        // Direct call: a function/method with a concrete body. (Block-bodied
        // arrows, functions, and methods all land here.)
        if (fn.block != null) {
            return listOf(calleeOf(fn, project))
        }

        // No body — an interface method or abstract/overload signature. Enumerate
        // the concrete implementations, same as the Go path. If the search finds
        // none (e.g. an expression-bodied arrow `() => expr` with no block),
        // fall back to expanding the function itself.
        val impls = DefinitionsScopedSearch.search(fn).findAll()
            .filterIsInstance<JSFunction>()
            .filter { it.block != null }
            .map { calleeOf(it, project) }
            .sortedBy { it.title }
        return impls.ifEmpty { listOf(calleeOf(fn, project)) }
    }

    /**
     * Map a resolved reference target to the function to expand. Handles direct
     * function/method declarations and the TS-specific shape of a const bound to
     * a function/arrow expression (`const f = () => {…}`), where the reference
     * resolves to the variable rather than the function.
     */
    private fun resolveToFunction(resolved: PsiElement?): JSFunction? = when (resolved) {
        is JSFunction -> resolved
        is JSVariable -> resolved.initializer as? JSFunction
        else -> null
    }

    private fun calleeOf(fn: JSFunction, project: Project): Callee {
        val vf = fn.containingFile?.virtualFile
        val range = fn.textRange
        return Callee(
            title = titleOf(fn),
            text = fn.text,
            project = project,
            sourceFile = vf,
            range = range,
            fileType = fn.containingFile?.fileType ?: PlainTextFileType.INSTANCE,
            id = "${vf?.path ?: "?"}#${range?.startOffset ?: -1}",
        )
    }

    private fun titleOf(fn: JSFunction): String {
        val name = fn.name ?: "fn"
        val cls = PsiTreeUtil.getParentOfType(fn, JSClass::class.java)?.name
        return if (cls.isNullOrBlank()) name else "$cls.$name"
    }
}
