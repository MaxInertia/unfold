import { useEffect } from "react";
import { updateSettings, useSettings } from "./settings";

// SettingsPanel is a right-side panel (not a modal) so the code view stays
// visible while toggling — every control takes effect live.
export function SettingsPanel({ onClose }: { onClose: () => void }) {
  const settings = useSettings();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
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
    </aside>
  );
}
