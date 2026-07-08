package dev.unfold.ide

import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDocumentManager

/** Resolves the call under the caret to its expandable callee(s). */
interface CalleeResolver {
    /**
     * Returns the expandable targets for the call at the caret:
     * - empty  — no resolvable call (indirect, builtin, unresolved)
     * - one    — a direct function/method call
     * - many   — an interface/abstract method call; each concrete implementation.
     */
    fun calleesAtCaret(editor: Editor, project: Project): List<Callee>
}

/**
 * Language-agnostic entry point: picks the resolver for the file under the caret
 * and delegates. Everything downstream (rendering, nesting, folding, navigation)
 * is language-blind, so adding a language is just adding a [CalleeResolver].
 *
 * Dispatch keys on the PSI file's language *id* (a plain string) so this object
 * never references a language plugin's classes directly — the JS/TS resolver is
 * only class-loaded when a JS/TS file is actually under the caret, which can only
 * happen when the JavaScript plugin is present. That keeps the plugin loadable in
 * an IDE without JS support.
 */
object PsiResolve {

    private val JS_TS_LANG_IDS = setOf(
        "JavaScript", "ECMAScript 6", "TypeScript", "TypeScript JSX", "JSX Harmony", "Flow JS",
    )

    fun calleesAtCaret(editor: Editor, project: Project): List<Callee> {
        val psiFile = PsiDocumentManager.getInstance(project).getPsiFile(editor.document) ?: return emptyList()
        val langId = psiFile.language.id
        val resolver: CalleeResolver = when {
            langId == "go" -> GoCalleeResolver
            langId in JS_TS_LANG_IDS -> TsCalleeResolver
            else -> return emptyList()
        }
        return resolver.calleesAtCaret(editor, project)
    }
}
