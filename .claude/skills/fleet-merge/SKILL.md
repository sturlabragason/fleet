---
name: fleet-merge
description: Merges a finished fleet branch into local main when several agents are merging at once - merge order from branch state, ADR number claiming, and the CHANGELOG/DESIGN top-of-day conflicts. Use at pre-merge checklist step 7, when `fleet touching` reports an overlap, when a coordinator does not answer, or when asked to coordinate a batch's merge order.
---

# fleet-merge

Eight agents merged into `main` on 2026-11-05. Every conflict was in the same two files, and one
agent sat idle for 15 minutes waiting for a coordinator to reply. This skill removes both waits.

It does not replace the playbook's pre-merge checklist. Finish items 1–6 first; this is item 7 when
you are not alone.

**Most of this is now one command.** `fleet merge` (ADR-48) does sections 3 and 4 for you:

```sh
fleet merge --dry-run     # the order it would take, from branch state
fleet merge               # rebase onto main, check, merge, check main again
```

It rebases onto `main`, resolves a `CHANGELOG.md` or `DESIGN.md` conflict by keeping both sides in
that file's own order (your line above in the CHANGELOG, your ADR below in §14), runs `make build && go vet ./... && go test ./...` on the rebased branch and again on
`main` after the merge, retries if `main` moves under it, and never pushes or removes your worktree.
Any other conflict aborts the rebase and exits 1 naming the files and the other worktree — that is
your `blocked` report. Use `--check ''` to skip the checks (don't) or `--check '<cmd>'` in a repo
that gates differently.

Read sections 1 and 2 anyway: the command does not pick your merge *order* or your ADR number.
Section 3 stays as the explanation of what it resolves, and as the manual fallback.

## 1. Merge order comes from branch state, not from replies

```sh
fleet touching --json            # every other worktree of this repo and the files it changes
git -C <repo> branch -vv         # where each fleet/* branch is relative to main
```

Order by what the branches actually are, in this order:

1. **No overlap** with your files: merge now. Nobody needs to answer.
2. **Overlap, their branch already contains yours**: they merge first; rebase onto `main` afterwards.
3. **Overlap, same base**: the branch with fewer touched files merges first, because it is the
   cheaper rebase. Tell the other agent in one line that you are going first or second.
4. **Overlap in a file a batch coordinator owns** (the spawn prompt names it): the coordinator's
   order wins when it gives one. Message it, then **keep working**. If it has not answered in 5
   minutes, merge on the order above and say so in your report. An idle agent costs more than a
   rebase.

A coordinator resolves a tie the branch state cannot; it is not a permission gate. Never block on a
reply you can derive. (`docs/process.md`, *Merge order for overlapping work*.)

## 2. Claim your ADR number from the prompt, or from `fleet adr claim`

The spawn prompt assigns it (`you own ADR-44`). Use that number from the start — in the ADR section,
the CHANGELOG line and the commit. Never read §14 to "find the next free one": four agents did that
on the same day and renumbered 38 → 40 → 41 between them.

If your prompt gave you no number, take one from the lock (ADR-50):

```sh
fleet adr claim --for <your-agent-name>   # prints the number; fleet adr ls shows live claims
```

It is atomic across every worktree, so two agents claiming at once get different numbers, and you can
claim at the *start* of the work rather than racing at merge time. Tell the admiral the number you
took. The claim frees itself when your ADR lands on main; `fleet adr release N` gives back one you
did not use.

Without the `fleet` binary, fall back to reading main and merging **immediately** — the gap between
reading and merging is the race:

```sh
git fetch -q . main && git --no-pager grep -h -o -E '^\*\*ADR-[0-9]+' main -- DESIGN.md | sort -t- -k2 -n | tail -1
```

## 3. The two shared-file conflicts

`CHANGELOG.md` and `DESIGN.md` conflict on almost every parallel merge because every agent inserts
at the top of the same section. Both are append-at-top by construction, so the resolution is
mechanical. The playbook's *Never* says to stop and report `blocked` on a conflict **unless you were
told to use best judgment** — for these two files, and only these two, treat the resolution below as
that judgment, keeping both sides' intent and rerunning the tests:

- **`CHANGELOG.md`**: keep both lines under today's `## YYYY-MM-DD`. Yours goes above theirs if your
  change is newer. Never drop a line, never merge two lines into one, never re-order the day's
  other entries.
- **`DESIGN.md` §14**: keep both ADR entries (they are bold `**ADR-NN: …**` paragraphs, not
  headings), ascending by number. If both sides used the same number, renumber *yours* (you are the
  one merging) to the number `fleet adr claim` prints — never to "the next free one in DESIGN.md",
  which can be another agent's unmerged claim (85 → 86 → 87 on 2026-11-10, fleet#40) — and fix the
  §14 entry, its CHANGELOG bracket and the commit. `fleet merge` checks this before it rebases and
  stops with the same instruction when your number is taken.
- **`DESIGN.md` §18 / roadmap table**: keep both rows. Only the row for your own item is yours to
  change.

Any other conflicting file is a real overlap: stop and report `blocked` with the file list, as the
playbook says.

**No CHANGELOG line, no conflict.** A change with no user-visible behaviour — docs, skills, agents,
tests, refactors — adds nothing to `CHANGELOG.md`. The file answers "what can the user do now".

## 4. Merge

```sh
fleet merge                          # does all four steps below
```

By hand, if you need to:

```sh
git -C <worktree> rebase main        # rebase your branch; never merge main into it
make build && go vet ./... && go test ./...
git -C <repo-main> merge --no-ff fleet/<name>
cd <repo-main> && make build && go vet ./... && go test ./...
```

Rerun the checks **on main after the merge**, not only on your branch: your change and the one that
landed while you rebased have never been tested together. That is the merge's whole risk.

Leave your worktree and branch in place (ADR-39). Never push main (`fleet-release` does that), and don't push your branch again after this rebase: CI already ran on what you pushed (ADR-69).

If the rebase conflicts outside the two files above: `git rebase --abort`, report `blocked` with the
files and the other worktree's name, and continue with anything else you can do.
