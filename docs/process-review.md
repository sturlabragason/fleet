# Process review, 2026-11-05

One day of fleet building fleet: ~30 agents, two batches, ADR-36 to ADR-42 merged and ADR-43 to
ADR-46 assigned to a second batch still building, v0.1.0 published and v0.2.0 cut. This page is the
retro. Evidence is the `retro:`, `risks:` and `issue:` lines agents reported, the git log of `main`,
and the branch and worktree state while eight agents merged into the same `main`.

`docs/process.md` says what the process *is*. This page says where it broke and what was built in
response. Each fix is a file in this repo or a named follow-up, never a new meeting or form.

## Findings

| # | Problem | Evidence | Fix |
|---|---|---|---|
| 1 | ADR-number churn: agents picked the same next number and renumbered mid-merge | `retro:` from three agents in batch 1; 38→40→41 renumbered four times; batch 2 avoided it entirely because the admiral assigned its four numbers (ADR-43 to ADR-46) **before** spawning | Admiral assigns ADR numbers in the spawn prompt (`fleet-admiral` skill, *Batch a slice of work*). Agent claims the number from its prompt, never by reading §14. `fleet-merge` skill re-checks at merge time. Follow-up task prompt: `fleet adr claim` or one file per ADR |
| 2 | Coordinators left agents idle: fleet-keys-doc waited 15 min for fleet-snap, which never answered | `retro: fleet-keys-doc sat idle 15 min because coordinator fleet-snap never answered` (11:46) | Merge order comes from **branch state, not replies**: `fleet touching --json` already names every worktree and the files it changes. `fleet-merge` makes that the first step and keeps the coordinator only as a tie-break. An agent that would wait more than 5 min merges on the branch-state order and says so in its report |
| 3 | `CHANGELOG.md` and `DESIGN.md` conflicted on every merge: all eight agents insert at the top of today's section | Eight merges on 2026-11-05, every one a conflict in the same two files | `fleet-merge` *Shared-file conflicts*: these two files are append-at-top by construction, so the resolution is always "keep both lines, newest first" — resolve in place, never `blocked`. Changes with no user-visible behaviour (docs, skills, agents) add no CHANGELOG line at all, which removes the conflict |
| 4 | The playbook contradicted the code it described: after ADR-39 gave each agent a worktree, step 7 still told agents to delete their own working directory | `issue:` 11:41, fixed by fleet-playbook-wt (5de735a) | Anything that changes spawn or worktree behaviour must update `internal/playbook/worker.md` in the same commit. Added to the `fleet-reviewer` checklist (item 7) |
| 5 | Spawn prompts restated playbook rules, costing tokens twice and drifting from the real rules | Prompts in the decision log repeat "commit with `Assisted-by:`", "merge into local main" | The playbook is already prepended to every spawn prompt. `fleet-admiral` *Write a ready prompt*: five lines, task / done-when / neighbours / dependencies / exceptions, and **nothing the playbook already says** |
| 6 | Work was lost when an agent closed before a hand-off reached it | `issue:` 10:56 (tui-design's visual pass never started); 10:55 (fleet-llm-sessions handed tiling work to an agent already gone) | `fleet-admiral` *Close safely*: before closing, read the agent's inbox and unread reports, and re-home any `next:` line onto a live agent or a note. A hand-off to an agent that may close is a `fleet_note`, not a message |
| 7 | Published history can never be rewritten; this was nearly discovered the expensive way | A read-only plan found that v0.1.0 is already served by `proxy.golang.org` and recorded in `sum.golang.org`, so re-pointing the tag would break every consumer's checksum with no fix available from this side | Decided: no squash of published history; squash at **branch** level before merging (playbook item 6). `fleet-release` carries the sumdb caution and is fast-forward only |
| 8 | Reading code could not find the status bugs; a real captured screen did | ADR-42 (0692239): claude, agy and vibe looked idle while thinking until real fixtures were captured | `fleet-fixtures` skill around `fleet snap`: capture the screen, never hand-write a fixture, one per state per tool |
| 9 | Personal strings reached `main` and blocked the next publish | `issue:` 10:31, `risk:` 10:56: `check-public.sh` failed on 6e1bdce over an employer name in two test fixtures; scrubbed in 7050f15 | New read-only `fleet-publish-check` agent runs the public check before the publisher is spawned, so the find happens on a branch instead of on `main`. Playbook already says to scrub fixtures; the reviewer now checks it (item 4) |
| 10 | Eight concurrent agents is double the measured heat budget, and RAM is uncapped | `risk:` 11:20; `docs/perf.md` budget is four | Known and accepted by the user; park/wake and a RAM guard are assigned as ADR-44, not yet merged. The admiral watches `max_working` and parks rather than closing |
| 11 | The queue only drains from the overview TUI, and `max_working` is read once at startup | `issue:` 11:20 | Assigned to the same ADR-44 work. Listed here so it is not rediscovered |
| 12 | Closing an idle-but-alive agent destroyed work the user was using | 09:38: two adopted sessions the user was working in were closed and had to be re-adopted | Rule, now in `fleet-admiral`: close only agents whose process is dead or that the user names. "Finished and idle" is not "done" |

## What this produced

Repo (`.claude/`, mirrored into `.github/agents`):

| Thing | For |
|---|---|
| `skills/fleet-release` | Cutting a release: public check, fast-forward push, tag, assets, CI, the sumdb rule |
| `skills/fleet-merge` | Merge order from branch state, ADR claiming, the two shared-file conflicts |
| `skills/fleet-fixtures` | Teaching fleet a tool's screens with `fleet snap` |
| `agents/fleet-publish-check` | Read-only gate: would this push pass `check-public.sh`? |
| `agents/fleet-reviewer` | Extended with worktree/playbook sync, ADR number, CHANGELOG placement and scrub checks |

User-wide (drafted in the agent hub, not installed):

| Thing | For |
|---|---|
| `fleet-admiral` | Running a fleet in any repo: brief, batch, ADRs up front, reports, RAID, safe close |
| `fleet-agent-comms` | The worker side of the MCP tools: report shape, mail, access, hand-offs |
| `fleet-profile-smoke` | Checking every tool profile still spawns, reports and reads status |

## Still open

- ~~A `fleet merge` command (rebase onto `main`, retry, report the conflict) would replace most of
  the `fleet-merge` skill with one command.~~ **Built 2026-11-05** (ADR-48): `fleet merge`, with
  `--dry-run` for the order. The skill now points at it and keeps only the order and ADR-number
  rules the command cannot decide.
- A `fleet adr claim` command (or one file per ADR) would replace finding 1's convention with a lock.
- `fleet reports --all` does not exist on the CLI; only the admiral's `fleet_reports` tool reads them.
  A worker cannot see its own report history.
