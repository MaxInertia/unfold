import { currentSettings, updateSettings } from "./settings";
import type { Frame } from "./types";

// Sonify: a sound for the shape of the code, so the ears carry structure
// while the eyes read text. Two halves, deliberately separate:
//
//  - the score builder, pure functions from lines to notes, which is where
//    every mapping lives (indent → pitch, kind of line → timbre, time per
//    line) and the only part worth unit-testing;
//  - the player, the thin Web Audio layer that turns a score into sound and
//    knows about the autoplay policy, the master volume and the mute key.
//
// Everything is off until the reader turns it on in settings: a sound that
// starts unasked is worse than no sound.

// ---- the score ----

// Each sound has a timbre, which the player maps to a waveform and a filter.
// Scan timbres describe a line of code; the rest are navigation cues.
export type Timbre =
  | "plain" // an ordinary line
  | "call" // a line with a call site
  | "error" // inside an `if err != nil` block
  | "return" // a return statement
  | "unfold" // a call was unfolded
  | "fold" // a frame was folded away
  | "root" // a new root frame
  | "tick" // a tour step
  | "cursor"; // the karaoke highlight moved

export interface Note {
  t: number; // ms from the start of the score
  pitch: number; // Hz
  dur: number; // ms
  timbre: Timbre;
  // Diff mode: the line changed on this branch, so it is played brighter.
  bright?: boolean;
  // The scanned line, for anyone who wants to follow the scan on screen.
  line?: number;
}

export type Score = Note[];

// C major pentatonic, C3 to E5: thirteen steps of about two octaves. Any
// two notes of a pentatonic set sound fine together, so a run of lines at
// mixed depths is a melody rather than a cluster.
export const PITCHES = [
  130.81, 146.83, 164.81, 196.0, 220.0, 261.63, 293.66, 329.63, 392.0, 440.0, 523.25, 587.33,
  659.26,
];
const TOP = PITCHES.length - 1;

// A step of the pitch set, counted from the bottom, clamped into range.
export function pitchAt(index: number): number {
  return PITCHES[Math.max(0, Math.min(TOP, Math.round(index)))];
}

// Nesting is lower, in both senses: each level of indentation and each
// level of unfolding steps the pitch down. Unfolding costs two steps so a
// callee's lines sit audibly beneath the caller's, not interleaved with them.
export function pitchFor(indent: number, depth = 0): number {
  return pitchAt(TOP - indent - 2 * depth);
}

// What the builder knows about a line. `call` and `changed` come from the
// frame (call sites, diff); the text carries the rest.
export interface LineInfo {
  text: string;
  call?: boolean;
  changed?: boolean;
}

export interface ScanOpts {
  // How far down the unfolded view this frame sits; offsets its pitch.
  depth?: number;
  // Where the score starts, in ms, so sections can be chained.
  t0?: number;
  // The ms per line, if the caller has already chosen one for a longer
  // sequence this frame is part of.
  step?: number;
}

// Leading whitespace as nesting levels: gofmt indents with one tab per
// level; four spaces are counted as a level for anything else.
export function indentOf(text: string): number {
  let tabs = 0;
  let spaces = 0;
  for (const ch of text) {
    if (ch === "\t") tabs++;
    else if (ch === " ") spaces++;
    else break;
  }
  return tabs + Math.floor(spaces / 4);
}

export type LineKind = "plain" | "call" | "error" | "return" | "rest";

const ERR_CHECK = /\berr\s*!=\s*nil\b/;

// One kind per line. Error blocks are found by text: an `if ... err != nil`
// line opens one and it runs to the closing brace at the same indent, so
// the `return nil, err` inside is heard as part of the error path rather
// than as a return of the function's own. Blank lines and lone braces are
// rests: silence carries the paragraph break better than another note.
export function classifyLines(lines: LineInfo[]): LineKind[] {
  const out: LineKind[] = [];
  let errIndent = -1;
  for (const l of lines) {
    const trimmed = l.text.trim();
    const indent = indentOf(l.text);
    if (errIndent >= 0 && trimmed.startsWith("}") && indent <= errIndent) {
      errIndent = -1;
      out.push("rest");
      continue;
    }
    if (trimmed === "" || /^[})\]]+[,;]?$/.test(trimmed)) {
      out.push("rest");
      continue;
    }
    if (errIndent >= 0) {
      out.push("error");
      continue;
    }
    if (/^(if|}\s*else\s+if)\b/.test(trimmed) && ERR_CHECK.test(trimmed)) {
      out.push("error");
      if (trimmed.endsWith("{")) errIndent = indent;
      continue;
    }
    if (/^return\b/.test(trimmed)) out.push("return");
    else if (l.call) out.push("call");
    else out.push("plain");
  }
  return out;
}

// Time per line: forty lines in about two seconds, never faster than a
// grain can be heard as separate, and a hard cap on the whole so a
// thousand-line function doesn't hold the room for a minute.
export const SCAN_TARGET_MS = 2000;
export const SCAN_MAX_MS = 5000;

export function stepFor(lineCount: number): number {
  const n = Math.max(1, lineCount);
  const step = Math.max(12, Math.min(60, SCAN_TARGET_MS / n));
  return Math.min(step, SCAN_MAX_MS / n);
}

// The scan of one frame: a grain per line, in order, pitch by indentation
// relative to the frame's shallowest line (a method body starts at the
// same pitch whether or not the file nests it), timbre by kind.
export function scoreForFrame(lines: LineInfo[], opts: ScanOpts = {}): Score {
  const depth = opts.depth ?? 0;
  const t0 = opts.t0 ?? 0;
  const step = opts.step ?? stepFor(lines.length);
  const kinds = classifyLines(lines);
  const base = lines.reduce(
    (m, l) => (l.text.trim() === "" ? m : Math.min(m, indentOf(l.text))),
    Infinity,
  );
  const grain = Math.max(45, Math.min(120, step * 1.8));
  const score: Score = [];
  lines.forEach((l, i) => {
    const kind = kinds[i];
    if (kind === "rest") return;
    const indent = indentOf(l.text) - (base === Infinity ? 0 : base);
    score.push({
      t: t0 + i * step,
      pitch: pitchFor(indent, depth),
      // A return rings a little longer: it is where the function ends.
      dur: kind === "return" ? grain * 1.6 : grain,
      timbre: kind,
      bright: l.changed || undefined,
      line: i,
    });
  });
  return score;
}

// A frame with the callees unfolded inside it, as the reader sees it: a
// child hangs off the line of its call site.
export interface ScanNode {
  lines: LineInfo[];
  children: { line: number; node: ScanNode }[];
}

function countLines(node: ScanNode): number {
  return node.lines.length + node.children.reduce((n, c) => n + countLines(c.node), 0);
}

// The scan of an unfolded view, in reading order: a frame's lines up to a
// call site, then the callee's whole section a couple of steps lower, then
// the rest of the caller. A short rest either side of a section marks the
// edges, the way the frame border does on screen. One step length for the
// whole sequence, chosen from the total line count, so the pace is steady.
export function scoreForTree(root: ScanNode, opts: ScanOpts = {}): Score {
  const step = opts.step ?? stepFor(countLines(root));
  const out: Score = [];
  let t = opts.t0 ?? 0;
  const walk = (node: ScanNode, depth: number) => {
    const children = [...node.children].sort((a, b) => a.line - b.line);
    let from = 0;
    for (const c of children) {
      const upTo = Math.min(node.lines.length, c.line + 1);
      if (upTo > from) {
        out.push(
          ...scoreForFrame(node.lines.slice(from, upTo), { depth, t0: t, step }).map((n) => ({
            ...n,
            line: (n.line ?? 0) + from,
          })),
        );
        t += (upTo - from) * step;
        from = upTo;
      }
      t += step;
      walk(c.node, depth + 1);
      t += step;
    }
    if (from < node.lines.length) {
      out.push(
        ...scoreForFrame(node.lines.slice(from), { depth, t0: t, step }).map((n) => ({
          ...n,
          line: (n.line ?? 0) + from,
        })),
      );
      t += (node.lines.length - from) * step;
    }
  };
  walk(root, opts.depth ?? 0);
  return out;
}

// When the last note ends.
export function scoreDuration(score: Score): number {
  return score.reduce((m, n) => Math.max(m, n.t + n.dur), 0);
}

// A frame's lines with what the frame knows about them: which carry a call
// site, which the diff marked as changed.
export function linesForFrame(frame: Frame): LineInfo[] {
  const lines = frame.source.split("\n").map((text): LineInfo => ({ text }));
  for (const c of frame.calls) {
    const idx = lineForOffset(frame.source, c.spanStart);
    if (lines[idx]) lines[idx].call = true;
  }
  if (frame.diff?.status === "added") for (const l of lines) l.changed = true;
  else if (frame.diff?.status === "modified")
    for (const i of frame.diff.addedLines ?? []) if (lines[i]) lines[i].changed = true;
  return lines;
}

function lineForOffset(source: string, offset: number): number {
  let line = 0;
  const stop = Math.min(offset, source.length);
  for (let i = 0; i < stop; i++) if (source.charCodeAt(i) === 10) line++;
  return line;
}

// ---- the navigation cues ----

export function unfoldScore(depth: number): Score {
  return [{ t: 0, pitch: pitchFor(0, depth), dur: 140, timbre: "unfold" }];
}

export function foldScore(depth: number): Score {
  return [{ t: 0, pitch: pitchFor(0, depth), dur: 110, timbre: "fold" }];
}

// Two notes a fifth apart, up: "we're somewhere new".
export function rootScore(): Score {
  return [
    { t: 0, pitch: pitchAt(5), dur: 160, timbre: "root" },
    { t: 90, pitch: pitchAt(8), dur: 220, timbre: "root" },
  ];
}

export function tickScore(): Score {
  return [{ t: 0, pitch: pitchAt(TOP), dur: 30, timbre: "tick" }];
}

export function cursorScore(lineText: string, depth = 0): Score {
  return [{ t: 0, pitch: pitchFor(indentOf(lineText), depth), dur: 60, timbre: "cursor" }];
}

// ---- the player ----

// How each timbre sounds: the oscillator's waveform, where the lowpass
// sits (lower is darker), and how loud relative to the rest. The error
// path is a sawtooth filtered right down, the one deliberately uneasy
// sound in the set; everything else is sine or triangle, kept quiet.
export interface Voicing {
  wave: OscillatorType;
  cutoff: number;
  peak: number;
}

export const VOICINGS: Record<Timbre, Voicing> = {
  plain: { wave: "sine", cutoff: 2200, peak: 0.5 },
  call: { wave: "triangle", cutoff: 3200, peak: 0.55 },
  error: { wave: "sawtooth", cutoff: 600, peak: 0.4 },
  return: { wave: "square", cutoff: 1200, peak: 0.3 },
  unfold: { wave: "triangle", cutoff: 2800, peak: 0.6 },
  fold: { wave: "sine", cutoff: 1400, peak: 0.35 },
  root: { wave: "triangle", cutoff: 2600, peak: 0.5 },
  tick: { wave: "square", cutoff: 4000, peak: 0.25 },
  cursor: { wave: "sine", cutoff: 1800, peak: 0.3 },
};

// A changed line is the same note with the lid off: a wider filter and a
// waveform with harmonics for it to let through.
export function voicingFor(note: Note): Voicing {
  const v = VOICINGS[note.timbre];
  if (!note.bright) return v;
  return {
    wave: v.wave === "sine" ? "triangle" : v.wave,
    cutoff: v.cutoff * 2.5,
    peak: v.peak * 1.1,
  };
}

// The envelope: a few ms to full, then an exponential decay to nothing by
// the end of the grain. Short enough to click along at 12 ms a line.
export const ATTACK_MS = 4;

let ctx: AudioContext | null = null;
let master: GainNode | null = null;
// Browsers refuse to start audio before the page has been interacted with,
// so nothing is created until a click or key has happened; a cue asked for
// before then is dropped rather than queued to all come out at once later.
let gestured = false;

if (typeof window !== "undefined") {
  const arm = () => {
    gestured = true;
    if (ctx?.state === "suspended") void ctx.resume();
  };
  window.addEventListener("pointerdown", arm, { capture: true, passive: true });
  window.addEventListener("keydown", arm, { capture: true, passive: true });
}

function context(): AudioContext | null {
  if (!gestured || typeof AudioContext === "undefined") return null;
  if (!ctx) {
    ctx = new AudioContext();
    master = ctx.createGain();
    master.connect(ctx.destination);
  }
  if (ctx.state === "suspended") void ctx.resume();
  return ctx;
}

// Volume is perceived on a log scale, so the slider's value is squared
// before it becomes a gain; the ceiling keeps "full" still quiet.
function masterGain(): number {
  const v = Math.max(0, Math.min(1, currentSettings().soundVolume));
  return v * v * 0.35;
}

function enabled(): boolean {
  return currentSettings().sound;
}

// Oscillators of the scan in flight, so a second scan (or the stop key)
// can cut them off instead of layering over them.
let scanNodes: OscillatorNode[] = [];
let scanEnd = 0;

function schedule(score: Score, track: boolean): OscillatorNode[] {
  const ac = context();
  if (!ac || !master) return [];
  master.gain.value = masterGain();
  const start = ac.currentTime + 0.02;
  const nodes: OscillatorNode[] = [];
  for (const note of score) {
    const v = voicingFor(note);
    const at = start + note.t / 1000;
    const end = at + note.dur / 1000;
    const osc = ac.createOscillator();
    osc.type = v.wave;
    osc.frequency.setValueAtTime(note.pitch, at);
    const filter = ac.createBiquadFilter();
    filter.type = "lowpass";
    filter.frequency.setValueAtTime(v.cutoff, at);
    filter.Q.value = 0.7;
    const env = ac.createGain();
    env.gain.setValueAtTime(0.0001, at);
    env.gain.linearRampToValueAtTime(v.peak, at + ATTACK_MS / 1000);
    env.gain.exponentialRampToValueAtTime(0.0001, end);
    osc.connect(filter);
    filter.connect(env);
    env.connect(master);
    osc.start(at);
    osc.stop(end + 0.01);
    osc.onended = () => {
      osc.disconnect();
      filter.disconnect();
      env.disconnect();
    };
    if (track) nodes.push(osc);
  }
  return nodes;
}

// Play a score now. Cues are fire-and-forget; a scan replaces the scan
// before it.
export function play(score: Score) {
  if (!enabled()) return;
  schedule(score, false);
}

export function playScan(score: Score) {
  if (!enabled()) return;
  stopScan();
  scanNodes = schedule(score, true);
  scanEnd = performance.now() + scoreDuration(score);
}

export function scanning(): boolean {
  return scanNodes.length > 0 && performance.now() < scanEnd;
}

export function stopScan() {
  const now = ctx?.currentTime ?? 0;
  for (const o of scanNodes) {
    try {
      o.stop(now);
    } catch {
      /* already stopped */
    }
  }
  scanNodes = [];
}

// ---- cues, as the app calls them ----

export function unfolded(depth: number) {
  play(unfoldScore(depth));
}

export function folded(depth: number) {
  play(foldScore(depth));
}

export function enteredRoot() {
  play(rootScore());
}

export function stepped() {
  play(tickScore());
}

export function cursorMoved(lineText: string, depth = 0) {
  play(cursorScore(lineText, depth));
}

// The mute key. It flips the setting itself rather than a separate flag,
// so the panel and the key never disagree about whether sound is on; a
// short cue confirms "on", and "off" is confirmed by the silence.
export function toggleSound(): boolean {
  const on = !currentSettings().sound;
  updateSettings({ sound: on });
  if (on) play(unfoldScore(0));
  else stopScan();
  return on;
}

// ---- what is on screen, for the scan ----

// Frames register themselves as they mount so the scan can be built from
// the view without the sound code reaching into React. Keyed the way the
// view store keys them (pathKey), with the parent's key and the call that
// opened them, which is all the tree needs.
export interface MountedFrame {
  key: string;
  parentKey: string | null;
  callId: string | null;
  frame: Frame;
}

const mounted = new Map<string, MountedFrame>();

export function registerFrame(m: MountedFrame) {
  mounted.set(m.key, m);
}

export function unregisterFrame(key: string) {
  mounted.delete(key);
}

// The mounted frames as a scan tree from the root. A child whose call the
// parent doesn't know (a leaf's spliced remote body, keyed `<call>#leaf`)
// hangs off that call's line all the same.
export function viewTree(): ScanNode | null {
  const root = mounted.get("");
  if (!root) return null;
  const build = (m: MountedFrame): ScanNode => {
    const children: ScanNode["children"] = [];
    for (const child of mounted.values()) {
      if (child.parentKey !== m.key || !child.callId) continue;
      const callId = child.callId.replace(/#leaf$/, "");
      const call = m.frame.calls.find((c) => c.id === callId);
      if (!call) continue;
      children.push({ line: lineForOffset(m.frame.source, call.spanStart), node: build(child) });
    }
    return { lines: linesForFrame(m.frame), children };
  };
  return build(root);
}

// The scan key: play the view, or stop the scan that is playing.
export function scanView(): Score | null {
  if (!enabled()) return null;
  if (scanning()) {
    stopScan();
    return null;
  }
  const tree = viewTree();
  if (!tree) return null;
  const score = scoreForTree(tree);
  playScan(score);
  return score;
}
