package dev.unfold.ide

import com.intellij.openapi.options.Configurable
import com.intellij.openapi.ui.ComboBox
import java.awt.BorderLayout
import javax.swing.BorderFactory
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel

/** Settings > Tools > Unfold — pick the renderer. */
class UnfoldConfigurable : Configurable {

    private val combo = ComboBox(RendererKind.values())

    override fun getDisplayName(): String = "Unfold"

    override fun createComponent(): JComponent {
        val panel = JPanel(BorderLayout(8, 8))
        panel.border = BorderFactory.createEmptyBorder(10, 10, 10, 10)
        panel.add(JLabel("Expansion renderer:"), BorderLayout.WEST)
        panel.add(combo, BorderLayout.CENTER)
        reset()
        return panel
    }

    override fun isModified(): Boolean = combo.selectedItem != UnfoldSettings.getInstance().renderer

    override fun apply() {
        UnfoldSettings.getInstance().renderer = combo.selectedItem as RendererKind
    }

    override fun reset() {
        combo.selectedItem = UnfoldSettings.getInstance().renderer
    }
}
