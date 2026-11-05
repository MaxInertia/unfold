# Semantic zoom, continuous

Status: design, not implemented. Scope: Go frames first; TypeScript follows
the same model once a transform exists for `tsengine`.

## The idea

A frame today shows one thing: the function's source, with calls as pills
you expand inline. Semantic zoom adds two more renderings of the *same*
function, so a reader can take in a whole unfolded path at a coarse level
and drop to code only where the eye needs it:

- **prose** — one sentence per block. "Retries with backoff until the
  context ends."
- **pseudocode** — control flow and names kept; error returns, logging,
  conversions, cleanup gone.
- **code** — what `Frame.tsx` renders now.

The goal is reading speed: more understanding per second. A first read at
prose takes a tenth of the screen; zooming in is cheap because nothing moves
out from under the reader — every level is addressed by the same file lines.

## 1. The levels

Every level is a list of **rows**. A row has text, the range of source lines
it stands for, and the call sites inside that range.

```ts
export type DetailLevel = "prose" | "pseudo" | "code";

export interface Row {
  text: string;                // rendered text (code: the source line verbatim)
  lines: [number, number];     // inclusive 0-based indices into Frame.source
  calls: CallID[];             // call sites whose span falls inside `lines`
  hidden?: [number, number][]; // sub-ranges elided from this row (pseudo)
}
```

The invariant: **every source line belongs to exactly one row at every
level.** Code is the identity (one row per line). Pseudocode drops and
rewrites, but a dropped statement is attached to the preceding kept row as
`hidden`, not lost. Prose rows cover contiguous blocks. Because the mapping
is total, anything addressed by file line still lands: a tour stop's `line`,
a note anchor (`after-line`, `range`), a karaoke highlight — each resolves to
"the row containing that line". `Frame.tsx` already groups per-line extras
through `renderLineExtras(lineIdx)` and `buildLineCalls`; at a coarser level
the same hooks are called per row, with the extras of all its lines merged.

`tours.(*Store).Save` (`internal/tours/tours.go:144-167`) at each level:

```
code  ─────────────────────────────────────────────────────
144  func (s *Store) Save(t Tour) (Tour, error) {
145    if err := validateName(t.Name); err != nil {
146      return Tour{}, err
147    }
148    if err := Validate(t); err != nil {
...
157    path := filepath.Join(s.dir, t.Name+".json")
158    tmp := path + ".tmp"
159    if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
160      return Tour{}, err
161    }
163    if err := os.Rename(tmp, path); err != nil { ...
166    return s.read(t.Name + ".json"), nil

pseudo ────────────────────────────────────────────────────
144  Save(t Tour) (Tour, error)
145    [validateName](t.Name)            ↯
148    [Validate](t)                     ↯
151    os.MkdirAll(s.dir)                ↯
154    buf := json.MarshalIndent(tourFile{…})
157    path, tmp := <name>.json, <name>.json.tmp
159    os.WriteFile(tmp, buf)            ↯
163    os.Rename(tmp, path)              ↯
166    return s.[read](name)

prose ─────────────────────────────────────────────────────
144  Save(t Tour)                                   ⟨older version⟩
  ▸ Rejects a bad name or an invalid tour before touching disk.
      [validateName] [Validate]
  ▸ Writes the tour to a temp file and renames it into place, so a
    reader never sees a half-written file.
  ▸ Returns the tour as re-read from disk.  [read]
```

`↯` marks a statement whose error branch was elided; hovering shows it.
Pills survive at every level. At pseudo, a kept call keeps its span (offsets
are translated through the edit list, section 4). At prose, calls inside a
block render as trailing pills on the sentence, or inline where the author
wrote `[[retry.Do]]` — the reference syntax notes and tour descriptions
already use. Clicking a pill expands exactly as it does today: the child
`Frame` mounts under the row via `renderLineExtras`, and the `FrameSlice`
path is unchanged because the `CallID` is unchanged.

Each frame owns its level. `FrameSlice` gains `level?: DetailLevel`; unset
inherits from the parent, and the root defaults to the global setting. So a
prose parent can hold a code child: the sentence stays one line and the
callee's full body hangs beneath it. That is the intended reading posture —
coarse outside, fine where you are looking.

Whole-file frames (`file:<path>`) have no levels in the first cut; a file is
not a function.

## 2. Where summaries come from

Pseudocode has a deterministic baseline (section 4). Prose does not: a
sentence that says *why* needs a writer. Three options.

**A. Agent-written files** under `.unfold/summaries/`, like tours. An
`unfold-summarize` skill writes them; `internal/summaries` mirrors
`tours.Store` (list, watch, a `"summaries"` event on `/api/events`); a
content hash of the function body says whether a summary still fits.

**B. The server calls a model.** A key in the environment, a call on first
view of a frame at prose level, result cached to the same files.

**C. Both:** A now, a server hook later.

| | A: files | B: server model |
|---|---|---|
| freshness | lags until someone runs the skill; staleness visible via hash | fresh on demand |
| cost | borne by the agent session already open on the repo | per view, per repo, on the user's key |
| latency | zero at read time | seconds, on the critical path of a zoom |
| determinism | same file, same screen | varies per call; two readers see two summaries |
| offline | yes | no |
| trust | written by something that was *working in* the code; reviewable and blameable in git | unreviewed text in the reading surface |
| unvisited repo | nothing at prose until an agent visits | works immediately |

**Recommendation: A, then C — with the hook shaped as a command, not an API
client.** The server never holds a key. `unfold summarize --exec '<cmd>'`
runs a user-supplied command per missing or stale function, feeding it the
body on stdin and expecting summary JSON on stdout, and writes the file.
The server stays model-free, which this project chose deliberately: the UI
shows what is in the repo, and what is in the repo is reviewable.

The honest case against A: coverage starts at zero and grows only where an
agent has been pointed. A repo with four thousand functions will never be
hand-summarized session by session, and the reader who most wants prose —
new to the codebase, no agent open — is the one A serves last. The derived
pseudo level is the mitigation: it always exists, and most of the speed-up
lives there (a reader skims control flow faster than they read sentences).
Prose is the bonus on paths someone cared about.

The case against B is not just cost. A wrong sentence in the reading
surface is worse than no sentence — the reader believes it, because it sits
where the code sits. A file in git can be read, corrected, and blamed; a
per-request completion cannot. If B ever lands, it lands behind the command
hook, so its output goes through the same file and the same staleness
check.

## 3. Staleness and trust

The hash is `sha256(Frame.Source)` — the exact byte range the indexer reads
(`readRange(file, node.Pos(), node.End())` in `indexer.Frame`) — truncated
to 16 hex chars. It is added to `model.Frame` as `Hash` so an author can
read it from `GET /api/symbol`. Row `lines` are relative to the function
start, so moving a function within its file, or editing code above it,
changes nothing.

A mismatched hash **shows** the summary with a badge: "summary is for an
older version of this function", with the summary's `updatedAt`. Hiding it
would turn a small drift into a blank screen, and the stale sentence is
usually still mostly right.

To say *which* parts moved, each authored pseudo row carries a `snippet`:
the trimmed text of its first source line at write time — the trick
`NoteAnchor.snippet` uses for drifted notes (`isDrifted` in `Frame.tsx`).
On a hash mismatch the server re-anchors rows by finding each snippet in
the current source. Rows found are repositioned and drawn normally. Rows
not found are struck through ("this line changed"). Current lines covered
by no re-anchored row get derived pseudo rows marked "new since summary".
Prose rows inherit the state of the pseudo rows in their range, so a
sentence over a changed block is flagged. Nothing is guessed; the reader
sees three states: fits, moved, changed.

## 4. Pseudocode without a model

The indexer already holds the `*ast.FuncDecl` (`fi.node`, `fi.body`) and
`go/types` info for every function, so the transform runs server-side on
data already in memory. It is a list of **edits** on the original source
bytes — `(start, end, replacement)` — not a reprint via `go/printer`.
Splicing text keeps positions: a `CallSite.SpanStart` translates through
the edit list by offset arithmetic, so pills land without re-resolving.

Per statement, in `ast.Inspect` order:

| statement | action |
|---|---|
| `if err != nil { return … }` (cond is `X != nil`, `X` typed `error`, body a single return, possibly wrapping with `fmt.Errorf`) | drop; mark the previous row `↯` |
| `x, err := f()` | rewrite to `x := f()`; drop `_ =` |
| call whose callee matches a noise pattern (`log`, `slog`, `zap`, `fmt.Print*`, metrics) | drop; patterns use the `package` / `recv` / `func` match shape of `rules.Rule`, so a repo extends the list where it already keeps recognizers |
| `defer x.Close()`, `defer mu.Unlock()`, `defer cancel()`, `defer wg.Done()` | drop |
| conversion `T(x)` where `T` is a type (`go/types` says so) | rewrite to `x` |
| `for` / `range` with a single-statement body | collapse to one row: `for each x in xs: f(x)` |
| `if` with a single-statement body and no `else` | collapse to one row |
| inline and trailing comments | drop |
| control flow, assignments with calls, returns, `go`, `select`, `switch` | keep |

Deciding what *not* to do matters as much: no renaming, no reordering, no
inference of intent. This is lossy compression, not explanation — every row
is a thing the code literally does. On typical Go it removes a third to a
half of the lines (`handleTours` in `server.go` goes from 37 lines to about
15). It never produces "retries until the context ends"; that is what a
summary adds, and an authored `pseudo` in a summary file overrides the
derived one.

## 5. Interaction

- **Per frame:** a three-stop segmented control in the frame header, beside
  the existing zoom-out button. It writes `level` into that frame's
  `FrameSlice`, so it is in the URL and in history like folds and
  expansions.
- **Global:** the same control in the toolbar sets the root and clears
  per-frame overrides. A reader who zoomed one frame in and then zooms
  everything out expects everything out.
- **Keyboard:** `-` / `=` step the focused frame's level out / in; `shift`
  variants do the whole view. Bare keys, ignored in text fields, like `]` /
  `[`. They register in `KEYBINDINGS` under a new scope `"detail"`, so the
  settings panel lists them by construction.
- **Not the same slider as service / platform.** `ZoomLevel` in `zoom.ts` is
  a reachability ladder — *who reaches this code*. Detail level is
  *resolution of this code*. Zooming out of prose must not land on the
  service view; they answer different questions. `ZoomTrail` and
  `alt+↑/↓` stay as they are.
- **Tours:** `Stop` gains `level?: DetailLevel`, and an `expand` entry may
  carry a level suffix: `"retry.Do@code"`. A stop at prose with one callee
  at code is the shape of most explanations: here is the loop, and here is
  the one line that matters.
- **Read-aloud and karaoke:** the voice reads a prose row's text while the
  highlight sweeps that row's `lines` in the code beneath — the line mapping
  is what makes the pairing possible. For this the frame shows two levels
  at once: prose rows as sticky labels over their code. PR #55 already
  speaks stop text; the karaoke work in flight adds the moving highlight.

## 6. Data model and API

One file per package directory, keyed by symbol in the form tours accept
(resolved through `Engine.LookupSymbol`, so suffix matching applies):

```jsonc
// .unfold/summaries/internal-tours.json
{
  "functions": {
    "tours.(*Store).Save": {
      "hash": "9f3a1c0e7b2d4411",
      "prose": [
        { "text": "Rejects a bad name or an invalid tour before touching disk.",
          "lines": [1, 6] },
        { "text": "Writes to a temp file and renames it into place, so a reader never sees a half-written file.",
          "lines": [7, 21] },
        { "text": "Returns the tour as re-read from disk — see [[read]].",
          "lines": [22, 22] }
      ],
      "pseudo": [   // optional; derived when absent
        { "text": "[[validateName]](t.Name)  ↯", "lines": [1, 3],
          "snippet": "if err := validateName(t.Name); err != nil {" }
      ],
      "writtenBy": "claude-code/unfold-summarize",
      "updatedAt": "2026-11-04T18:02:11Z"
    }
  }
}
```

Server side, `internal/summaries` mirrors `internal/tours`: a `Store` with
`List`, `Get(targetId)`, `Watch(onChange)`; `Server.SetSummaries`;
`NotifySummaries` emits `"summaries"` on `/api/events`. A row's `hidden`
ranges and `calls` are computed by the server, never written by the author.

```go
// GET /api/summary?targetId=<id>
type Summary struct {
    TargetID    TargetID   `json:"targetId"`
    Hash        string     `json:"hash"`                  // current source
    SummaryHash string     `json:"summaryHash,omitempty"` // what the file was written for
    Stale       bool       `json:"stale"`
    Source      string     `json:"source"`                // "file" | "derived"
    Prose       []Row      `json:"prose,omitempty"`
    Pseudo      []Row      `json:"pseudo"`                // always present
    Drift       []RowDrift `json:"drift,omitempty"`       // when Stale: fits | moved | changed
}

// GET /api/summaries/status?filter=missing,stale
type Status struct {
    TargetID TargetID `json:"targetId"`
    Symbol   string   `json:"symbol"` // as an author would write it
    File     string   `json:"file"`
    Line     int      `json:"line"`
    Hash     string   `json:"hash"`
    State    string   `json:"state"`  // "ok" | "stale" | "missing"
}
```

`unfold summaries status` prints the same table without a running server,
which is what the skill reads. `POST /api/summaries` writes a file for
authors that prefer HTTP, same-origin-guarded like `POST /api/tours`.

The UI fetches `/api/summary` lazily when a frame's level leaves `code`,
caches by `${targetId}:${hash}` in a module map like `frameCache.tsx`, and
drops entries on `"summaries"` and `"reload"` events. The `code` level
costs nothing new.

Skill outline, `.claude/skills/unfold-summarize/SKILL.md`: when to write
(after a non-trivial change; when asked to make a path "readable at a
glance"; alongside a tour, for its stops); run `unfold summaries status
--filter missing,stale` scoped to the files touched; read each body; write
one present-tense sentence per block, two to five blocks, naming calls with
`[[Name]]`; copy `hash` from the status output; leave `pseudo` out unless
the derived one misleads; verify with `GET /api/summary`; commit with the
change when it documents something durable.

## 7. Plan

1. **Derived pseudo, per-frame and global level.** `Row` model,
   `internal/pseudo` transform per the table above, `/api/summary`
   returning `derived` only, `level` in `FrameSlice` and URL, header
   control, `-`/`=` keys, extras re-homed onto rows. No files, no prose.
   *About 6 days.* Shippable: every Go repo gets a faster first read.
2. **Summary files and prose.** `internal/summaries` store, watcher, event,
   `Frame.Hash`, status endpoint and CLI, prose rendering with pills, stale
   badge, the skill. *About 5 days.*
3. **Drift and tours.** Snippet re-anchoring, `Drift` in the response, three
   row states in the UI; `Stop.level` and `@level` on expand; karaoke driven
   by prose rows once that PR lands. *About 3 days.*
4. **Command hook.** `unfold summarize --exec`, batching over `status`,
   written through the same store. *About 2 days.* Optional; ship only if
   slice 2 shows coverage is the problem.

## 8. Open questions

- Who decides block boundaries at prose? The author does, and derived
  pseudo rows need not align with them. Is a sentence spanning two and a
  half pseudo rows a problem, or fine?
- Noise patterns: a fixed list, or genuinely reusing `rules.Rule` with a
  new `emit.kind: "noise"`? Reuse is elegant and couples two features.
- TypeScript: `tsengine` has no AST in memory the way the Go indexer does.
  Does pseudo wait for that, or does TS get prose-only?
- Should code inside a prose parent indent under the sentence, or render
  full-width with the sentence as a sticky header? The second reads better
  for karaoke; the first keeps the tree visible.
- Is one hash per function enough, or should a summary also pin the hashes
  of the callees it names, so a changed callee marks the sentence stale?
- Where does `writtenBy` show, if anywhere? Provenance matters for trust
  but is noise on every row.
