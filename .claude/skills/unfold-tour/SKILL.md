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

Rules the server enforces (a file that breaks one still lists in the tour
tab, marked unreadable, with the error shown, so the reader sees it rather
than nothing):

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

A bare name is fine when it is unique in the project. When several
symbols match, the server silently picks the first full name
alphabetically, so qualify anything that might be ambiguous: `Save` is
ambiguous in most repos, `notes.Store.Save` is not. Suffix matching is on
dot and slash boundaries, so `Save` does not match `AutoSave`.

For TypeScript, symbols are indexed as the function name, `Class.method`
for methods, or the variable name for an arrow function assigned to a
const. The query must equal that name or its part after the last dot;
there are no package or file prefixes.

In a multi-repo workspace, prefix the repo alias with `::`
(`billing::pubsub.Publish`). Without a prefix the primary repo is searched.

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

Stops are resolved in the browser, not by the server, so
`GET /api/tours` returns the files as written plus the tours directory;
its `error` field only means a file could not be read or failed the rules
above. To check a symbol name before writing it, ask the server directly:
`curl -s 'localhost:<port>/api/symbol?name=pubsub.Publish'` returns the
frame it would open, or an error if nothing matches. `GET /api/files`
lists the indexed paths a file stop is matched against by suffix.
`POST /api/tours` with a body that includes `name` also writes a tour,
for when the file system is not reachable; it works from curl.

## Keep or discard

Tours are plain files meant to be committable, like notes. Commit a tour
when it documents something durable (how a subsystem works). Leave it
uncommitted, or delete it after the review, when it was a one-off walk
through a change.
