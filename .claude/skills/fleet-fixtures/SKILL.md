---
name: fleet-fixtures
description: Teaches fleet to read a coding tool's screen - capture real pane fixtures with `fleet snap`, then fit patterns in internal/status. Use when a tool shows the wrong status (idle while working, no error, a missed approval prompt), when adding a profile for a new tool, or when asked to "fix status detection" or "add fixtures".
---

# fleet-fixtures

Status detection is pattern-matching against a terminal screen. Reading the tool's source, or
guessing what it prints, has failed every time it was tried: ADR-42 found claude, agy and vibe all
looked idle while thinking, and only a captured screen showed why. **Capture first, pattern second.**

Fixtures live in `internal/status/testdata/<set>/<state>.txt`, one directory per pattern set
(`copilot`, `claude`, `agy`, `vibe`, `fake`). `TestCaptures` asserts every file matches the state in
its own name, so a fixture is both the evidence and the test. (`testdata/copilot-events/` is not a
pattern set: it holds subagent event logs read by `subagents_test.go`, and `snap` will refuse it.)

## 1. Capture

Never hand-write a fixture. Drive the tool into the state, then snap its pane:

```sh
fleet snap <agent> <state>                             # a live agent on fleet's own board
fleet snap --set claude --socket fleet-test-x %3 idle  # a pane on your own test server
fleet snap --set claude <agent> working                # file it under a set other than the agent's
```

A `%pane` argument always needs `--set`: fleet has no board entry to read the tool from, and a
fixture filed under a set that does not exist is refused rather than written.

`<state>` is `working`, `waiting`, `idle`, `error` or `stuck`, optionally with a variant suffix that
the test strips: `idle-typed`, `waiting-approval`, `waiting-trust`, `error-dead`. Use a variant
whenever two screens mean the same state for different reasons — that is what makes the corpus
useful later.

The states worth having for a new tool, in the order they bite:

| State | How to get there |
|---|---|
| `working` | give it a long task and snap mid-run |
| `idle` | let it finish; the prompt box must be **empty** |
| `idle-typed` | type into the prompt without submitting |
| `waiting-approval` | make it ask to run a command |
| `waiting-trust` | start it in a directory it has not been trusted in |
| `error` | `ls /nope`, or whatever makes it print its error marker |

`idle` and `idle-typed` are not optional for a tool with input patterns: `TestInputBoxCaptures`
requires that an `idle*` capture reads back an empty box (and `-typed` the text), because the report
wake (ADR-36) uses that to decide whether it may type into the pane.

Never spawn a real claude/copilot/agy/vibe to generate a fixture inside a test. Capture by hand from
a pane you started, on a `fleet-test-*` socket or the live board, and commit the file.

## 2. Scrub

A capture is a screenshot of your machine. Before committing, read the file and remove home paths,
hostnames, employer and client names, emails, repo names that are not this one, tokens and ticket
ids. `scripts/check-public.sh` blocks a push over exactly this, and it has already blocked one.
Replace with the neutral placeholders the existing fixtures use (`/home/user`, `api-auth`, `web-nav`).

## 3. Fit the pattern

Patterns are data, not code: built-in TOML in `internal/status`, overridden by `~/.fleet/patterns.toml`.
Add or adjust the regex for the set, then:

```sh
go test ./internal/status/
```

Let the whole corpus decide. A pattern that fixes your new fixture and breaks another tool's `idle`
is a worse bug than the one you started with — the table test tells you immediately. Prefer a
pattern anchored on a marker the tool owns (a spinner glyph, a box character, its error prefix) over
one matching ordinary prose, which any agent can print by accident.

Non-breaking spaces matter: screens use `\u00a0` inside prompt boxes, and a pattern written with an
ordinary space will not match. Existing patterns spell it out as `\x{a0}`.

## 4. Done

- `make build && go vet ./... && go test ./...` exits 0.
- The new fixture files and the pattern change are in the same commit.
- A user-visible improvement ("fleet now sees agy working") earns its `CHANGELOG.md` line; a fixture
  added only to lock in current behaviour does not.
