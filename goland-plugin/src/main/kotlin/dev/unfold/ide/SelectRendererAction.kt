package dev.unfold.ide

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.ui.popup.JBPopupFactory

/** Quick in-editor renderer switch (Ctrl+Alt+R). */
class SelectRendererAction : AnAction() {

    override fun actionPerformed(e: AnActionEvent) {
        JBPopupFactory.getInstance()
            .createPopupChooserBuilder(RendererKind.values().toList())
            .setTitle("Unfold renderer")
            .setItemChosenCallback { chosen -> UnfoldSettings.getInstance().renderer = chosen }
            .createPopup()
            .showInBestPositionFor(e.dataContext)
    }
}
