import { useSyncExternalStore } from "react";
import type { TourStop } from "./types";

// Reading a tour stop aloud. The voice is a pluggable provider so the
// browser's own speech synthesis — zero setup, works offline — can be
// swapped for a server-backed one (ElevenLabs via /api/tts) without the
// tour code knowing. Only one thing speaks at a time: a new request stops
// whatever is playing, because the reader has moved on.

export interface Voice {
  // Speak the text; resolves when playback ends or is cut off.
  speak(text: string): Promise<void>;
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

const browserVoice: Voice = {
  available: () => typeof speechSynthesis !== "undefined" && typeof SpeechSynthesisUtterance !== "undefined",
  speak(text) {
    return new Promise<void>((resolve) => {
      const u = new SpeechSynthesisUtterance(text);
      u.rate = 1.05;
      u.onend = () => resolve();
      u.onerror = () => resolve();
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
export function speak(key: string, text: string) {
  if (state.speaking?.key === key) {
    stopSpeaking();
    return;
  }
  if (!voice.available()) return;
  const my = ++seq;
  set({ speaking: { key } });
  voice.speak(text).then(() => {
    if (seq === my) set({ speaking: null });
  });
}

export function stopSpeaking() {
  seq++;
  voice.stop();
  if (state.speaking) set({ speaking: null });
}

// ---- what gets said ----

// The spoken form of a stop: where it is, then why. Descriptions are written
// for the eye — "[[pkg.(*T).Method]]" is a link — so references are read as
// the bare name, and the code-ish punctuation that would be spelled out
// letter by letter is dropped.
export function stopSpeechText(stop: TourStop, index: number, total: number): string {
  const where = stop.symbol ? spokenName(stop.symbol) : stop.file ? spokenPath(stop.file, stop.line) : "";
  const head = `Stop ${index + 1} of ${total}${where ? `: ${where}` : ""}.`;
  const desc = stop.description ? spokenDescription(stop.description) : "";
  return desc ? `${head} ${desc}` : head;
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
  return text
    .replace(/\[\[\s*file:([^\]]+)\]\]/g, (_, p: string) => spokenPath(p.trim()))
    .replace(/\[\[([^\]]+)\]\]/g, (_, n: string) => spokenName(n))
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}
