---
name: unfold-tour
description: Write an unfold tour — an ordered, described list of places in the code that the unfold web UI steps through. Use when asked to "walk me through", "show me where X lives", "give me a tour of", "explain how X works in the code", or to hand over the notable places of an implementation, a bug, or a review. Also use after finishing a non-trivial change when the user wants to see what you touched and why, in reading order.
---

# unfold tours

A tour is one JSON file under `<project>/.unfold/tours/<name>.json`. The
unfold web UI watches that directory and lists every tour in its **tour**
sidebar tab; the reader clicks a stop or presses `]` / `[` to step. You
write the file with ordinary file tools. Nothing else is required: no
restart, no API call, and the directory need not exist yet.

## When to write one

- The user asks to be walked through an implementation, a subsystem, a
  bug's path, or the changes you just made.
- You have finished a change that touches more than two or three places
  and the user will want to review it in order.
- You found something notable while exploring and the user should see the
  trail, not just the conclusion.

A tour replaces a long chat message of `file:line` references. Prefer it
when unfold is in use on the project (there is a `.unfold/` directory, or
the user mentioned unfold).

## The file

```json
{
  "title": "How a publish is retried",
  "description": "Optional. Shown above the stop list.",
  "stops": [
    { "symbol": "pubsub.(*Client).Publish",
      "description": "Entry point. The ctx deadline is set here, not by callers." },
    { "file": "internal/pubsub/retry.go", "line": 41,
      "description": "The backoff table. Jitter is applied *after* the cap — see [[backoff.Next]]." },
    { "symbol": "Publish",
      "expand": ["retry.Do", "retry.Do > backoff.Next"],
      "description": "Same entry point, read with the retry loop spliced in." }
  ]
}
```

Rules the server enforces (a file that breaks one still lists, dimmed,
with the error, so the reader sees it rather than nothing):

- `stops` must have at least one entry.
- Each stop names **either** `symbol` **or** `file`, never both.
- `line` is 1-based and only goes with `file`.
- `expand` entries must be non-empty strings.
- The filename is the tour's identity. Any `name` inside the file is
  ignored. Use a short kebab-case filename: `retry-path.json`.

## Naming a symbol

Write the name the way you would at a call site. Matching is suffix-based
on the dotted form with receiver punctuation ignored, so all of these name
the same method:

```
Publish
Client.Publish
pubsub.Client.Publish
(*Client).Publish
(*pubsub.Client).Publish
```

A bare name is fine when it is unique in the project. Qualify it when it
is not: `Save` is ambiguous in most repos, `notes.Store.Save` is not.
Suffix matching is on dot boundaries, so `Save` does not match `AutoSave`.

For TypeScript, use the exported name, or `Class.method`.

## `expand`

`expand` pre-opens calls inside a symbol stop, so the stop is a reading,
not a position. Each entry is a path from the stop's function:

- `retry.Do` opens the body of `retry.Do` at its call site inside the stop.
- `retry.Do > backoff.Next` opens `backoff.Next` inside the opened
  `retry.Do`. Paths sharing a prefix share nodes.
- `Do#2` picks the second call to `Do` in the body when it is called more
  than once.

Call names match the way symbols do: `Do` finds `retry.Do`. An entry that
matches nothing is flagged on the stop and the rest of the stop still
opens, so a stale `expand` does not break the tour.

`expand` only applies to symbol stops. A file stop opens the whole file
and scrolls to `line`.

## Descriptions

Say *why* this place matters and what to notice, not what the code does
line by line. One to three sentences. The description also renders as a
card in the code at the stop's line, so write it to be read beside the
code. `[[Name]]` and `[[file:path]]` make clickable references, same as
notes.

### Cues: where the eye goes while a stop is read

Tours can be played aloud, with a highlight in the code that follows the
narration. The narration is your prose, so nothing in it says which line
it is about — you say it, with a **cue** inline in the description. A cue
is silent when spoken and renders as a small `⌖` mark the reader can
click. The highlight jumps to it when the narration gets there and stays
until the next cue; without cues it sits on the stop's first line.

```
[[line:+3]]            the stop's anchor line plus 3 — the function's
                       first line (or a file stop's `line`) is +0
[[line:+2-5]]          a range, lit whole
[[line:131]]           an absolute file line (fragile; prefer +N or at:)
[[at:return nil, err]] the first line of the function (or, for a file
                       stop, of the file) containing that text — survives
                       edits above it
[[Name]]               a plain reference is a cue too: the highlight
                       lands on that call when its name is spoken
```

Put the cue right before the words about that line:
`"Stats the file for its timestamp [[line:+3-5]], then [[at:json.Unmarshal]]
parses it, then hands the result to [[Validate]]."` Prefer `at:` for a
single line and `+N` for a short range near the top; both hold up when
the file changes elsewhere. One cue per thing you talk about is plenty —
don't cue every line.

## Writing it

1. Pick the reading order. Entry point first, then the path a request
   takes, then the edge cases. Five to ten stops is typical.
2. Confirm each symbol exists and is unambiguous (grep for `func .*Name`
   or the TS export). Prefer a symbol stop over a file+line stop: symbols
   survive edits, line numbers do not.
3. Write `.unfold/tours/<name>.json`. If `.unfold/` does not exist yet,
   create it.
4. Tell the user the tour name and that it is in the tour tab. Do not
   paste the stop list into chat as well.

If the server is running you can check resolution with
`curl -s localhost:<port>/api/tours`; a stop that failed to resolve
carries an `error`. `POST /api/tours` with a body that includes `name`
also writes a tour, for when the file system is not reachable.

## Keep or discard

Tours are plain files meant to be committable, like notes. Commit a tour
when it documents something durable (how a subsystem works). Leave it
uncommitted, or delete it after the review, when it was a one-off walk
through a change.
