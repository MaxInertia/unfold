import { Fragment, useEffect, useState } from "react";
import { fetchVoiceProviders, type VoiceProvider } from "./api";
import { updateSettings, useSettings } from "./settings";
import { speak, stopSpeaking, useSpeech } from "./speech";
import { bindingsByScope, matches, SCOPE_LABELS } from "./keybindings";

// SettingsPanel is a right-side panel (not a modal) so the code view stays
// visible while toggling — every control takes effect live.
export function SettingsPanel({ onClose }: { onClose: () => void }) {
  const settings = useSettings();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (matches("ui.dismiss", e)) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <aside className="settings-panel" aria-label="settings">
      <div className="settings-head">
        <span className="settings-title">settings</span>
        <button
          type="button"
          className="settings-close"
          onClick={onClose}
          aria-label="close settings"
        >
          ✕
        </button>
      </div>

      <div className="settings-section">
        <div className="settings-section-title">depth</div>
        <label className="settings-row">
          <input
            type="checkbox"
            checked={settings.depthRails}
            onChange={(e) => updateSettings({ depthRails: e.target.checked })}
          />
          <span className="settings-label">
            depth rails
            <span className="settings-desc">
              colored lane per nesting level on each frame — same palette as
              the pinned headers
            </span>
          </span>
        </label>
        <label className="settings-row">
          <input
            type="checkbox"
            checked={settings.depthRuler}
            onChange={(e) => updateSettings({ depthRuler: e.target.checked })}
          />
          <span className="settings-label">
            depth ruler
            <span className="settings-desc">numeric depth in frame headers</span>
          </span>
        </label>
        <label className="settings-row settings-row--select">
          <span className="settings-label">
            nesting indent
            <span className="settings-desc">
              rails keep every level at full reading width; classic indents
              each level
            </span>
          </span>
          <select
            value={settings.indentMode}
            onChange={(e) =>
              updateSettings({ indentMode: e.target.value as "rails" | "indent" })
            }
          >
            <option value="rails">rails (no indent)</option>
            <option value="indent">classic indent</option>
          </select>
        </label>
      </div>

      <SpeechSettings />

      <div className="settings-section">
        <div className="settings-section-title">keyboard</div>
        {/* Rendered from the same registry the handlers match against, so
            this list is the app's actual behaviour rather than a description
            of it that can fall out of date. */}
        {bindingsByScope().map(([scope, bindings]) => (
          <div key={scope} className="settings-keys-group">
            <div className="settings-keys-scope">{SCOPE_LABELS[scope]}</div>
            <ul className="settings-keys">
              {bindings.map((b) => (
                <li key={b.id} className="settings-key">
                  <span className="settings-key-chord">
                    {b.keys.map((k, i) => (
                      <Fragment key={k}>
                        {i > 0 && <span className="settings-key-plus">+</span>}
                        <kbd>{k}</kbd>
                      </Fragment>
                    ))}
                  </span>
                  <span className="settings-key-desc">{b.description}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </aside>
  );
}

const PREVIEW_KEY = "settings:preview";
const PREVIEW_TEXT = "Stop 2 of 4: Store dot read. Reads one tour file and validates it.";

// How tours are read aloud: whether, how fast, and in which voice. The
// browser voice is always there; server voices appear when the server has
// a key (see /api/tts). The read-aloud toggle also lives in the tour
// header, where it's flipped per sitting; this is the same setting.
function SpeechSettings() {
  const settings = useSettings();
  const { speaking, available, fallback } = useSpeech();
  const [providers, setProviders] = useState<VoiceProvider[] | null>(null);
  useEffect(() => {
    let alive = true;
    fetchVoiceProviders()
      .then((p) => alive && setProviders(p))
      .catch(() => alive && setProviders([]));
    return () => {
      alive = false;
    };
  }, []);
  const previewing = speaking?.key === PREVIEW_KEY;
  const serverVoices = (providers ?? []).flatMap((p) =>
    p.voices.map((v) => ({ id: `${p.id}:${v.id}`, label: `${p.label}: ${v.name}` })),
  );
  const chosenMissing = settings.voice !== "browser" && !serverVoices.some((v) => v.id === settings.voice);
  return (
    <div className="settings-section">
      <div className="settings-section-title">speech</div>
      <label className="settings-row">
        <input
          type="checkbox"
          checked={settings.tourReadAloud}
          onChange={(e) => updateSettings({ tourReadAloud: e.target.checked })}
        />
        <span className="settings-label">
          read tour stops aloud
          <span className="settings-desc">each stop is narrated as it opens; also toggled in the tour tab</span>
        </span>
      </label>
      <label className="settings-row settings-row--select">
        <span className="settings-label">
          voice
          <span className="settings-desc">
            {providers === null
              ? "looking for server voices…"
              : serverVoices.length > 0
                ? "server voices are synthesized with the key the server was started with"
                : "only the browser's voice: start unfold with ELEVENLABS_API_KEY set (or ~/.config/unfold/secrets.json) for an ElevenLabs voice"}
            {(providers ?? []).map((p) => p.error && ` · ${p.label}: ${p.error}`)}
            {fallback && ` · the server voice failed (${fallback}); the browser voice spoke instead`}
          </span>
        </span>
        <span className="settings-voice-row">
          <select
            value={settings.voice}
            onChange={(e) => updateSettings({ voice: e.target.value })}
            disabled={!available}
          >
            <option value="browser">browser voice</option>
            {chosenMissing && <option value={settings.voice}>{settings.voice} (not offered now)</option>}
            {serverVoices.map((v) => (
              <option key={v.id} value={v.id}>
                {v.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="settings-preview"
            onClick={() =>
              previewing ? stopSpeaking() : speak(PREVIEW_KEY, PREVIEW_TEXT, { rate: settings.tourSpeechRate })
            }
            disabled={!available}
            title={previewing ? "stop" : "hear this voice"}
          >
            {previewing ? "■" : "▶"} preview
          </button>
        </span>
      </label>
      <label className="settings-row settings-row--select">
        <span className="settings-label">
          narration speed
          <span className="settings-desc">applies to every narration; also in the tour tab</span>
        </span>
        <select
          value={String(settings.tourSpeechRate)}
          onChange={(e) => updateSettings({ tourSpeechRate: Number(e.target.value) })}
        >
          {[0.8, 1, 1.25, 1.5, 2].map((r) => (
            <option key={r} value={String(r)}>
              {r}×
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
