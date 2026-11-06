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

## Diff story: a branch's changes, told in execution order

Use this when the user wants to review a branch, a PR, or "what you just
changed" — anything where the reading order should be the order the code
runs in, not the order the files sort in. unfold's diff mode already
computes that: for every function the branch changed, which entrypoints
reach it and the call path from each. The server supplies the ordered
cast; you write the narrative.

1. Run unfold in diff mode against the base the PR targets, and note the
   port it prints:

   ```
   unfold --no-open --diff-base main
   ```

   (Diff mode is Go-only, and it diffs against the merge-base, so fetch
   the base ref first.)

2. Fetch the skeleton. It is a complete tour file with the prose left out:
   the changed functions as stops in execution order, each `expand` set to
   the changed functions it leads to, and a `[[line:+a-b]]` cue placeholder
   on every changed line range of a modified function. A function new on
   the branch has no placeholder — all of it is new.

   ```
   curl -s 'localhost:<port>/api/diff/tour?format=skeleton' > .unfold/tours/<name>.json
   ```

   Read the full `GET /api/diff/tour` too if you want the facts behind the
   order — per function, its entrypoints (kind, route, path) and +/− line
   counts — but don't paste them into the descriptions: the **Changes vs
   `<base>`** tour the tour tab already shows is those facts. Yours is the
   story.

3. Write the title and description: what the branch sets out to do, in a
   sentence or two.

4. Write each stop's description as a step in the story — **why** this
   place changed, then **what** to notice — keeping the cue placeholders
   and putting each right before the words about those lines:

   ```json
   { "symbol": "server.Server.handleDiffTour",
     "expand": ["server.Server.diffTourCached"],
     "description": "The one endpoint behind both features. It only has an answer in diff mode, so [[line:+5-8]] it refuses with the flag to pass rather than returning an empty list that reads as \"nothing changed\". The work is in [[server.Server.diffTourCached]], opened below." }
   ```

   `[[Name]]` refs for the functions a stop hands off to; `[[at:text]]`
   when a cue should follow a line's text rather than its offset (the
   placeholders are offsets, which is fine for a tour written and read on
   the same commit). Trim: a 30-function branch does not need 30 stops.
   Drop the stops whose story is "and this helper, too" — the `expand` on
   the stop above already opens them — and keep the ones where a decision
   was made. Reorder only if the execution order misleads (a helper listed
   under the first entrypoint that happens to reach it, when it belongs to
   the second's story).

5. Tell the user the tour is in the tour tab, next to the generated one.

A worked example — this recipe applied to the branch that added diff tours
to unfold, skeleton from `?format=skeleton`, narrative written by hand — is
[`docs/examples/diff-story-diff-tour.json`](../../../docs/examples/diff-story-diff-tour.json).
Copy it into `.unfold/tours/` with the unfold repo open in diff mode against
`main` to see it play.

## Keep or discard

Tours are plain files meant to be committable, like notes. Commit a tour
when it documents something durable (how a subsystem works). Leave it
uncommitted, or delete it after the review, when it was a one-off walk
through a change. A diff story is usually the second kind: it is about one
branch, and it goes stale the moment the branch merges.
