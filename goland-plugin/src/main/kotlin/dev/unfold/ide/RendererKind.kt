package dev.unfold.ide

/** The selectable rendering strategies. Default is the native embedded editor. */
enum class RendererKind(private val display: String) {
    EDITOR("Embedded editor (native code)"),
    PAINTED("Painted (syntax-colored block)"),
    JCEF("Web view (JCEF)");

    fun create(): FrameRenderer = when (this) {
        EDITOR -> EditorInlayRenderer()
        PAINTED -> PaintedRenderer()
        JCEF -> JcefRenderer()
    }

    override fun toString(): String = display
}
