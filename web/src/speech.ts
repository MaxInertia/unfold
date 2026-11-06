import { useSyncExternalStore } from "react";
import { synthesize } from "./api";
import { parseCue, type Cue } from "./cues";
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
  // Fetch the audio for text without playing it, so the next stop plays
  // with no gap. Voices that synthesize locally have nothing to do.
  prefetch?(text: string): void;
}

export interface SpeechState {
  // What is being read, so the button that started it can show "stop".
  speaking: { key: string } | null;
  available: boolean;
  // The voice in use, as the settings name it.
  voice: string;
  // Set when the chosen server voice failed and the browser voice spoke
  // instead; the reason, for the UI to show.
  fallback: string | null;
}

let state: SpeechState = {
  speaking: null,
  available: false,
  voice: "browser",
  fallback: null,
};
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
  return useSyncExternalStore(
    subscribe,
    () => state,
    () => state,
  );
}

// ---- the browser voice ----

// Characters per second of ordinary read-aloud English at rate 1, for the
// voices that never report word boundaries (Chrome's network voices). The
// highlight then runs on a clock instead of on the words — approximate,
// but it still moves with the narration.
const CHARS_PER_SECOND = 14;

const browserVoice: Voice = {
  available: () =>
    typeof speechSynthesis !== "undefined" &&
    typeof SpeechSynthesisUtterance !== "undefined",
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
            const est =
              ((performance.now() - started) / 1000) *
              CHARS_PER_SECOND *
              opts.rate;
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

// ---- a server voice ----

// The character being spoken at a moment of the audio, from the voice's
// own timing: alignment[i] is when text[i] starts, so the character is the
// last one that has started. Binary search — it runs on every tick of a
// paragraph-long text.
export function charAtTime(alignment: number[], ms: number): number {
  let lo = 0;
  let hi = alignment.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (alignment[mid] <= ms) lo = mid + 1;
    else hi = mid;
  }
  return Math.max(0, lo - 1);
}

// Without timing, the audio's position scaled to the text's length: words
// are not all the same length, but it moves with the narration.
export function charAtFraction(
  currentTime: number,
  duration: number,
  textLength: number,
): number {
  if (!(duration > 0)) return 0;
  return Math.min(
    textLength,
    Math.floor((currentTime / duration) * textLength),
  );
}

// Audio synthesized by the server (/api/tts) and played through one shared
// element. Progress for the karaoke highlight comes from the voice's word
// timing when it gave one (ElevenLabs does), so a [[Name]] lights up as
// the name is said; otherwise it is position in the audio scaled to the
// text. A failed fetch falls back to the browser voice for that
// utterance, so a bad key or a lost network never silences a tour.
function serverVoice(provider: string, voiceId: string): Voice {
  let el: HTMLAudioElement | null = null;
  const audio = () => (el ??= new Audio());
  interface Clip {
    url: string;
    alignment: number[] | null;
  }
  // Object URLs by text, most recent last; bounded so a long tour doesn't
  // pin every utterance in memory.
  const clips = new Map<string, Promise<Clip>>();
  const fetchClip = (text: string) => {
    let p = clips.get(text);
    if (!p) {
      p = synthesize(provider, voiceId, text).then((s) => ({
        url: URL.createObjectURL(s.blob),
        alignment: s.alignment,
      }));
      p.catch(() => clips.delete(text));
      clips.set(text, p);
      if (clips.size > 24) {
        const oldest = clips.keys().next().value!;
        clips.get(oldest)?.then(
          (c) => URL.revokeObjectURL(c.url),
          () => {},
        );
        clips.delete(oldest);
      }
    }
    return p;
  };
  return {
    available: () => typeof Audio !== "undefined",
    prefetch: (text) => void fetchClip(text).catch(() => {}),
    async speak(text, opts) {
      let clip: Clip;
      try {
        clip = await fetchClip(text);
      } catch (e) {
        set({ fallback: e instanceof Error ? e.message : String(e) });
        return browserVoice.speak(text, opts);
      }
      if (state.fallback) set({ fallback: null });
      const a = audio();
      return new Promise<boolean>((resolve) => {
        let done = false;
        let last = -1;
        // timeupdate fires about four times a second — late enough for a
        // one-word name to be over before its highlight lands — so the
        // position is also read on a faster clock while playing.
        const tick = () => {
          if (!opts.onProgress || a.paused) return;
          const c = clip.alignment
            ? charAtTime(clip.alignment, a.currentTime * 1000)
            : charAtFraction(a.currentTime, a.duration, text.length);
          if (c !== last) {
            last = c;
            opts.onProgress(c);
          }
        };
        const clock = opts.onProgress ? window.setInterval(tick, 50) : 0;
        const finish = (natural: boolean) => {
          if (done) return;
          done = true;
          window.clearInterval(clock);
          a.ontimeupdate = a.onended = a.onerror = a.onpause = null;
          resolve(natural);
        };
        a.src = clip.url;
        a.playbackRate = opts.rate;
        a.ontimeupdate = tick;
        a.onended = () => finish(true);
        a.onerror = () => finish(false);
        // stop() pauses; a pause before the end is a cut-off.
        a.onpause = () => {
          if (!a.ended) finish(false);
        };
        a.play().catch(() => finish(false));
      });
    },
    stop() {
      if (el && !el.paused) el.pause();
      browserVoice.stop();
    },
  };
}

let voice: Voice = browserVoice;
state = { ...state, available: browserVoice.available() };

// Pick the voice the settings name: "browser", or "<provider>:<voiceId>".
// Stops anything playing when the voice actually changes.
export function configureVoice(spec: string) {
  if (spec === state.voice) return;
  stopSpeaking();
  const sep = spec.indexOf(":");
  voice =
    spec === "browser" || sep <= 0
      ? browserVoice
      : serverVoice(spec.slice(0, sep), spec.slice(sep + 1));
  set({ voice: spec, available: voice.available(), fallback: null });
}

// Warm the cache for text that is about to be spoken.
export function prefetchSpeech(text: string) {
  voice.prefetch?.(text);
}

// ---- speaking ----

let seq = 0;

// Read `text` aloud under `key` (a tour stop's identity). Speaking the key
// that is already playing stops it instead — one button is play and stop.
// Resolves true when the text was read to the end, false when it was
// stopped, superseded, or couldn't start.
export function speak(
  key: string,
  text: string,
  opts: Partial<SpeakOptions> = {},
): Promise<boolean> {
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

// A cue the description placed, and where in the spoken text the
// narration reaches it. Cues are silent: they only move the highlight.
export interface SpokenCue {
  char: number;
  cue: Cue;
}

export interface SpokenStop {
  text: string;
  refs: SpokenRef[];
  cues: SpokenCue[];
}

// The spoken form of a stop: where it is, then why. Descriptions are written
// for the eye — "[[pkg.(*T).Method]]" is a link — so references are read as
// the bare name, and the code-ish punctuation that would be spelled out
// letter by letter is dropped.
export function stopSpeech(
  stop: TourStop,
  index: number,
  total: number,
): SpokenStop {
  const where = stop.symbol
    ? spokenName(stop.symbol)
    : stop.file
      ? spokenPath(stop.file, stop.line)
      : "";
  const head = `Stop ${index + 1} of ${total}${where ? `: ${where}` : ""}.`;
  if (!stop.description) return { text: head, refs: [], cues: [] };
  const desc = spokenDescriptionParts(stop.description);
  if (!desc.text)
    return {
      text: head,
      refs: [],
      cues: desc.cues.map((c) => ({ ...c, char: head.length })),
    };
  const offset = head.length + 1;
  return {
    text: `${head} ${desc.text}`,
    refs: desc.refs.map((r) => ({ ...r, char: r.char + offset })),
    cues: desc.cues.map((c) => ({
      ...c,
      char: Math.min(c.char + offset, head.length + 1 + desc.text.length),
    })),
  };
}

export function stopSpeechText(
  stop: TourStop,
  index: number,
  total: number,
): string {
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

// The description as speech, remembering where each [[ref]] landed and
// where each cue sits. Built piece by piece rather than by regex
// replacement so the positions are exact after whitespace is collapsed. A
// cue says nothing; the words after it close up over it, so "here
// [[line:3]]." reads "here." with the cue at the end of "here".
function spokenDescriptionParts(text: string): SpokenStop {
  const refs: SpokenRef[] = [];
  const cues: SpokenCue[] = [];
  let out = "";
  const push = (s: string) => {
    // Collapse runs of whitespace across piece boundaries as one.
    let cleaned = s.replace(/`([^`]*)`/g, "$1").replace(/\s+/g, " ");
    if (cleaned.startsWith(" ") && (out === "" || out.endsWith(" ")))
      cleaned = cleaned.slice(1);
    // Punctuation after a cue closes up: "here [[line:3]]." → "here."
    if (/^[.,;:!?)]/.test(cleaned) && out.endsWith(" ")) out = out.trimEnd();
    out += cleaned;
  };
  const parts = text.split(/(\[\[[^\]]+\]\])/g);
  for (const p of parts) {
    const m = p.match(/^\[\[([^\]]+)\]\]$/);
    if (!m) {
      push(p);
      continue;
    }
    const ref = m[1].trim();
    const cue = parseCue(ref);
    if (cue === "invalid") continue;
    if (cue) {
      cues.push({ char: out.trimEnd().length, cue });
      continue;
    }
    const spoken = ref.startsWith("file:")
      ? spokenPath(ref.slice(5).trim())
      : spokenName(ref);
    refs.push({ char: out.length, name: ref });
    out += spoken;
  }
  const trimmed = out.trim();
  return {
    text: trimmed,
    refs,
    cues: cues.map((c) => ({ ...c, char: Math.min(c.char, trimmed.length) })),
  };
}
