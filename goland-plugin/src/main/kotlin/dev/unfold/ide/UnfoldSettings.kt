package dev.unfold.ide

import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.components.service

/** App-level setting for the active renderer; persisted across restarts. */
@Service(Service.Level.APP)
@State(name = "UnfoldSettings", storages = [Storage("unfold.xml")])
class UnfoldSettings : PersistentStateComponent<UnfoldSettings.State> {

    class State {
        var renderer: RendererKind = RendererKind.EDITOR
    }

    private var state = State()

    override fun getState(): State = state
    override fun loadState(s: State) {
        state = s
    }

    var renderer: RendererKind
        get() = state.renderer
        set(value) {
            state.renderer = value
        }

    companion object {
        fun getInstance(): UnfoldSettings = service()
    }
}
