import { useSyncExternalStore } from "react";
import type { TourStop } from "./types";

// Reading a tour stop aloud. The voice is a pluggable provider so the
// browser's own speech synthesis — zero setup, works offline — can be
// swapped for a server-backed one (ElevenLabs via /api/tts) without the
// tour code knowing. Only one thing speaks at a time: a new request stops
// whatever is playing, because the reader has moved on.

export interface SpeakOptions {
  // Playback speed, 1 = the voice's natural pace.
  rate: number;
  // Called as the voice advances through the text, with the index of the
  // character being spoken — what lets a highlight follow the narration.
  onProgress?: (charIndex: number) => void;
}

export interface Voice {
  // Speak the text; resolves when playback ends or is cut off. True means
  // it reached the end on its own.
  speak(text: string, opts: SpeakOptions): Promise<boolean>;
  stop(): void;
  available(): boolean;
}

export interface SpeechState {
  // What is being read, so the button that started it can show "stop".
  speaking: { key: string } | null;
  available: boolean;
}

let state: SpeechState = { speaking: null, available: false };
const listeners = new Set<() => void>();

function set(patch: Partial<SpeechState>) {
  state = { ...state, ...patch };
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function useSpeech(): SpeechState {
  return useSyncExternalStore(subscribe, () => state, () => state);
}

// ---- the browser voice ----

// Characters per second of ordinary read-aloud English at rate 1, for the
// voices that never report word boundaries (Chrome's network voices). The
// highlight then runs on a clock instead of on the words — approximate,
// but it still moves with the narration.
const CHARS_PER_SECOND = 14;

const browserVoice: Voice = {
  available: () => typeof speechSynthesis !== "undefined" && typeof SpeechSynthesisUtterance !== "undefined",
  speak(text, opts) {
    return new Promise<boolean>((resolve) => {
      const u = new SpeechSynthesisUtterance(text);
      u.rate = opts.rate;
      let done = false;
      let sawBoundary = false;
      const started = performance.now();
      const clock = opts.onProgress
        ? window.setInterval(() => {
            if (sawBoundary || done) return;
            const est = ((performance.now() - started) / 1000) * CHARS_PER_SECOND * opts.rate;
            opts.onProgress!(Math.min(Math.floor(est), text.length));
          }, 100)
        : 0;
      const finish = (natural: boolean) => {
        if (done) return;
        done = true;
        window.clearInterval(clock);
        resolve(natural);
      };
      u.onboundary = (e) => {
        sawBoundary = true;
        opts.onProgress?.(e.charIndex);
      };
      u.onend = () => finish(true);
      u.onerror = () => finish(false);
      speechSynthesis.cancel();
      speechSynthesis.speak(u);
    });
  },
  stop() {
    speechSynthesis.cancel();
  },
};

let voice: Voice = browserVoice;
state = { ...state, available: browserVoice.available() };

// Swap the provider (a server-backed voice). Stops anything playing.
export function setVoice(v: Voice) {
  stopSpeaking();
  voice = v;
  set({ available: v.available() });
}

// ---- speaking ----

let seq = 0;

// Read `text` aloud under `key` (a tour stop's identity). Speaking the key
// that is already playing stops it instead — one button is play and stop.
// Resolves true when the text was read to the end, false when it was
// stopped, superseded, or couldn't start.
export function speak(key: string, text: string, opts: Partial<SpeakOptions> = {}): Promise<boolean> {
  if (state.speaking?.key === key) {
    stopSpeaking();
    return Promise.resolve(false);
  }
  if (!voice.available()) return Promise.resolve(false);
  const my = ++seq;
  set({ speaking: { key } });
  return voice
    .speak(text, {
      rate: opts.rate ?? 1,
      onProgress: opts.onProgress && ((c) => seq === my && opts.onProgress!(c)),
    })
    .then((natural) => {
      if (seq !== my) return false;
      set({ speaking: null });
      return natural;
    });
}

export function stopSpeaking() {
  seq++;
  voice.stop();
  if (state.speaking) set({ speaking: null });
}

// ---- what gets said ----

// A reference the description made, and where its spoken form starts in
// the text — so a highlight can land on the call when the name is said.
export interface SpokenRef {
  char: number;
  name: string; // as written: a symbol name, or "file:path"
}

export interface SpokenStop {
  text: string;
  refs: SpokenRef[];
}

// The spoken form of a stop: where it is, then why. Descriptions are written
// for the eye — "[[pkg.(*T).Method]]" is a link — so references are read as
// the bare name, and the code-ish punctuation that would be spelled out
// letter by letter is dropped.
export function stopSpeech(stop: TourStop, index: number, total: number): SpokenStop {
  const where = stop.symbol ? spokenName(stop.symbol) : stop.file ? spokenPath(stop.file, stop.line) : "";
  const head = `Stop ${index + 1} of ${total}${where ? `: ${where}` : ""}.`;
  if (!stop.description) return { text: head, refs: [] };
  const desc = spokenDescriptionParts(stop.description);
  if (!desc.text) return { text: head, refs: [] };
  const offset = head.length + 1;
  return {
    text: `${head} ${desc.text}`,
    refs: desc.refs.map((r) => ({ ...r, char: r.char + offset })),
  };
}

export function stopSpeechText(stop: TourStop, index: number, total: number): string {
  return stopSpeech(stop, index, total).text;
}

// "pubsub.(*Client).Publish" → "Client dot Publish"; "retry.Do" → "retry dot
// Do". The package path is noise aloud; the last two segments are what a
// reader would say.
export function spokenName(name: string): string {
  const dotted = name
    .trim()
    .replace(/^.*::/, "") // workspace alias
    .replace(/\(\*?/g, "")
    .replace(/\)/g, "");
  const afterSlash = dotted.slice(dotted.lastIndexOf("/") + 1);
  const segs = afterSlash.split(".").filter(Boolean);
  return segs.slice(-2).join(" dot ");
}

function spokenPath(file: string, line?: number): string {
  const base = file.split("/").pop() ?? file;
  const name = base.replace(/\.[a-z0-9]+$/i, (m) => ` dot ${m.slice(1)}`);
  return line ? `${name}, line ${line}` : name;
}

export function spokenDescription(text: string): string {
  return spokenDescriptionParts(text).text;
}

// The description as speech, remembering where each [[ref]] landed. Built
// piece by piece rather than by regex replacement so the positions are
// exact after whitespace is collapsed.
function spokenDescriptionParts(text: string): SpokenStop {
  const refs: SpokenRef[] = [];
  let out = "";
  const push = (s: string) => {
    // Collapse runs of whitespace across piece boundaries as one.
    const cleaned = s.replace(/`([^`]*)`/g, "$1").replace(/\s+/g, " ");
    if (cleaned.startsWith(" ") && (out === "" || out.endsWith(" "))) out += cleaned.slice(1);
    else out += cleaned;
  };
  const parts = text.split(/(\[\[[^\]]+\]\])/g);
  for (const p of parts) {
    const m = p.match(/^\[\[([^\]]+)\]\]$/);
    if (!m) {
      push(p);
      continue;
    }
    const ref = m[1].trim();
    const spoken = ref.startsWith("file:") ? spokenPath(ref.slice(5).trim()) : spokenName(ref);
    refs.push({ char: out.length, name: ref });
    out += spoken;
  }
  return { text: out.trim(), refs };
}
