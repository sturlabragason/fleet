# Fleet usage metrics and friction data model

Fleet already has the improvement loop (weekly sprint reviews, `retro:` report lines, `fleet doctor` and saved improvement routes) and two append-only JSONL stores (`events.jsonl` and `runs.jsonl`). Do not add a new database, telemetry service or third log file. Instead, extend the two existing ledgers with low-frequency friction events, close-time rollups and weekly spend queries over `usage.json`.

Design written 2026-11-21 on branch `fleet/usage-metrics-design`. **Phase 1 is built** in `v1.66.0` (ADR-187 in `DESIGN.md` §14); **Phases 2 and 3 are planned**. Baseline measurements come from `ai-fleet` `b31327cf` (`v1.65.1`) and live `~/.fleet` state on 2026-11-21: 16,673 lines in `events.jsonl` (1.57 MB over 17 days), 99 lines in `runs.jsonl` (50.8 KB over 2 days) and 57 lines in `graphs/reads.jsonl`. Built Phase 1 fields and code paths are verified against `1d606b58` (`v1.66.0`).

## 1. Existing friction and improvement process

The process turns friction into changes on branches and GitHub issues without adding meetings or extra forms:

| Mechanism | Role in the loop | Where it lives |
|---|---|---|
| `retro:` lines | One line per report or result file naming what slowed the agent down; repeated lines trigger playbook edits (ADR-35) | `docs/process.md`, `internal/playbook/`, `mail/*/inbox.jsonl` |
| Weekly sprint review | Scheduled agent gathers 7 days of fleet state, tool transcripts, `retro:` lines and git logs, ranks friction by cost, commits text fixes on `fleet/` branches, files fleet code issues and compares week over week | `docs/process.md` ("A weekly review"), `~/.fleet/sprint-reviews/` |
| `skill-doctor` route | Monthly read-only audit of skills, prompt audits, hub lint, usage and evals; outputs proposals | `~/.fleet/routes.toml` (`[route.skill-doctor]`) |
| `ingest` route | Four-step read-only evaluation (read, overlap, assess, propose) before adopting external tools, plugins or skills | `~/.fleet/routes.toml` (`[route.ingest]`) |
| `fleet doctor` | Checks runtime health (tmux, locale, watcher, board, profiles, companions) and 7-day code-graph read counts (ADR-165) | `cmd/fleet/doctor.go`, `~/.fleet/graphs/reads.jsonl` |
| `fleet stats` | Counts 11 policy-intervention kinds in `events.jsonl` (ADR-154) and scores closed runs in `runs.jsonl` by profile, model, effort, version, repo, week, playbook, `AGENTS.md` or route (ADR-170) | `cmd/fleet/stats.go`, `internal/runs/`, `internal/board/events.go` |
| RAID and decisions | Admiral logs risks, assumptions, issues and decisions; pinned notes survive resets | `~/.fleet/decisions.log`, `~/.fleet/pinned.log` |

Before `v1.66.0` (ADR-187), the loop had three concrete data gaps:

1. **Missing friction signals forced transcript scraping (closed in ADR-187).** Neither `events.jsonl` nor `runs.jsonl` recorded follow-up sends, mid-turn steers, user pane rescues, driver takeovers, dead-pane crashes or time spent waiting on the user. The weekly review's gather script had to scan raw tool transcripts and regex-match user messages to approximate them.
2. **Done-refusal counts were wiped on recovery (fixed in ADR-187).** When `doneGate` (`internal/fleet/donegate.go`) rejected a `done` report, it incremented `a.DoneBounces` and logged only to the rotating 5 MB `fleet.log`. When the agent fixed the issue and its next `done` passed, `resetBounces` zeroed `a.DoneBounces`, so `recordRun` (`internal/fleet/runs.go`) wrote `0` for every recovered run (0 of 82 `done` runs in live `runs.jsonl` on `v1.65.1` showed a bounce despite 11 `done refused` entries in two days of `fleet.log`). ADR-187 now derives lifetime bounces and refusal reasons from `done_refused` events while keeping `board.Agent.DoneBounces` as the consecutive cap.
3. **Two gaps sat outside worker runs (partially closed).** First, `fleet spend` inspected live agents only, leaving weekly spend unmeasured even though `~/.fleet/usage.json` already kept 40 days of daily totals; `fleet spend --since` closed this in ADR-187. Second, the admiral handles most user interaction (87% of non-worker user messages in the 2026-11-21 weekly review), yet `runs.jsonl` records closed workers only; admiral session telemetry stays planned for Phase 3.

## 2. Data inventory

Every signal below is either built under `~/.fleet` or planned as an additive extension to an existing Go call site.

| Signal | State on `v1.66.0` | File and code path | Remaining work (planned) |
|---|---|---|---|
| Status transitions | Built (40-day rolling) | `~/.fleet/events.jsonl`; `internal/board/board.go`, `internal/board/events.go` | Phase 2: optional `run_id` for joins across reused agent names |
| Policy interventions (11 kinds) | Built (`fleet stats`, ADR-154) | `~/.fleet/events.jsonl`; `internal/board/events.go`, `internal/fleet/intervene.go`, `cmd/fleet/stats.go` | Phase 2: surface 7-day `status_disagree` and crash anomalies in `fleet doctor` |
| Run ledger and config hashes | Built (`fleet stats --runs`, ADR-170, ADR-187) | `~/.fleet/runs.jsonl`; `internal/runs/runs.go`, `internal/fleet/runs.go`, `internal/board/runconfig.go` | Phase 2: friction columns and `--compare` in `fleet stats --runs` |
| Reports and `retro:` lines | Built (counts + `has_retro` in `runs.jsonl`; prose in mail) | `~/.fleet/mail/<box>/inbox.jsonl`; `internal/fleet/report.go`, `internal/fleet/runs.go` (`tallyReports`) | None |
| Task cards and done-checks | Built (`KindDoneCheck` + `check_result`) | `board.json`, `closed.json`, `events.jsonl`, `runs.jsonl`; `internal/board/board.go`, `internal/fleet/report.go` (`doneCheck`) | Optional: check duration and exit code on `donecheck` detail (e.g. `fail:exit=1`) |
| Queue items and tweaks | Built (`deliver` events in `Deliver`, `team.go`, `mcp/send.go`; queue in `todo.Store`) | `internal/fleet/queue_todo.go`, `internal/fleet/team.go`, `internal/mcp/send.go` | Optional: count leftover tweaks handed off on close |
| Spend and compactions | Built per live session, per closed run and per day (`fleet spend --since`, ADR-187) | `internal/spend/spend.go`, `~/.fleet/usage.json` (`internal/usage/ledger.go`), `runs.jsonl`, `cmd/fleet/spend.go` | Phase 2: consume `fleet spend --since 7d --json` in `sprint-review` |
| Crashes and dead panes | Built (`crash` event + `exit` class in `runs.jsonl`, ADR-187) | `internal/fleet/deadpane.go`, `internal/fleet/runs.go` (`exitClass`) | Phase 2: show crash rate in `fleet stats --runs` and `fleet doctor` |
| Done-refused bounces | Built (`done_refused` event + `done_bounces` and `done_refusals` in `runs.jsonl`, ADR-187) | `internal/fleet/donegate.go`, `internal/fleet/runs.go` (`tallyEvents`) | Phase 2: show bounce rate and reason breakdown in `fleet stats --runs` |
| Sends, steers and interrupts | Built (`deliver` event + `sends` map in `runs.jsonl`, ADR-187) | `internal/fleet/queue_todo.go`, `team.go`, `mcp/send.go`, `runs.go` | Phase 2: optional `actor` (`admiral`, `leader`, `user`) once callers thread it through |
| User interventions and takeovers | Built (`takeover` + debounced `user_pane` events + `sends` map in `runs.jsonl`, ADR-187) | `internal/fleet/takeover.go`, `interactive.go`, `refresh.go`, `runs.go` | Phase 2: rescue/takeover rate column in `fleet stats --runs` |
| Time-to-done and time-in-state | Built (`created`, `first_done`, `closed`, `time_s` per status in `runs.jsonl`, ADR-187) | `internal/fleet/runs.go` (`tallyEvents`, `addSecs`), `internal/runs/summary.go` | Phase 2: split `to_done` into working, waiting and queued columns in `fleet stats --runs` |
| Code-graph reads and notes | Built (`fleet doctor` shows 7-day graph reads, ADR-165) | `~/.fleet/graphs/reads.jsonl` (`internal/graph/use.go`), `decisions.log`, `pinned.log` | Keep as template for per-feature usage checks |

## 3. Minimal data model

The model extends two existing append-only JSONL files and reuses `usage.json`. Every new field is additive (`omitempty`) so older lines and older binaries remain compatible.

### Entities

Three entities cover the fleet lifecycle without adding a new store:

1. **Event (`~/.fleet/events.jsonl`, 40-day rolling, ADR-154, ADR-187).** Point-in-time status transitions, policy interventions (11 kinds) and friction events (4 kinds). `detail` is a fixed low-cardinality code capped at 200 bytes (stays under `PIPE_BUF` for safe `O_APPEND` writes). **Planned (Phase 2):** optional `run_id` (linking events to one run when an agent name is reused) and `actor` (`admiral`, `leader`, `user`, `fleet`).
2. **Run (`~/.fleet/runs.jsonl`, permanent, ADR-170, ADR-187).** One line per closed agent, joined to its spawn configuration fingerprint (`RunConfig`: hashes of playbook layers, `AGENTS.md`/`CLAUDE.md` and `route@hash`). Pre-aggregates state durations and friction counters at close time so long-term trend analysis reads one small file and outlives the 40-day event rotation.
3. **DailyUsage (`~/.fleet/usage.json`, 40-day rolling, `internal/usage`).** Stores daily spend (`aic`, `tokens`, `usd`) per profile across both live and closed sessions, queried by `fleet spend --since` (ADR-187).

### Friction event kinds

Four friction event kinds are **built** in `v1.66.0` (ADR-187), excluded from `fleet stats`'s policy-intervention count so ADR-154 counts keep their meaning; one admiral kind is **planned** for Phase 3:

| Kind | Status | Hook point | `detail` enum |
|---|---|---|---|
| `done_refused` | Built (ADR-187) | `internal/fleet/donegate.go` (`doneGate`) | `uncommitted`, `unmerged`, `open_queue`, `no_merge_perm`, `max_bounces` |
| `deliver` | Built (ADR-187) | `internal/fleet/queue_todo.go` (`Deliver`), `team.go`, `mcp/send.go`, `interactive.go` (`user_pane` debounced >= 60 s) | `sent`, `steered`, `interrupted`, `queued`, `held`, `user_pane` |
| `takeover` | Built (ADR-187) | `internal/fleet/takeover.go` (`Takeover`) | `to_tmux`, `to_acp` |
| `crash` | Built (ADR-187) | `internal/fleet/deadpane.go` | `exit=<N>`, `sig=<N>`, `acp_exit` |
| `admiral_reset` | Planned (Phase 3) | Admiral handoff / reset path (`internal/fleet/admiral*`) | `context=<pct>` or `manual` |

Example `events.jsonl` friction line (ADR-187; `run_id` and `actor` are planned for Phase 2):

```json
{"t":"2026-11-21T14:22:09+01:00","agent":"api-auth","profile":"claude-copilot","model":"opus","kind":"done_refused","detail":"unmerged"}
```

Example additive fields on a `runs.jsonl` record (built in ADR-187):

```json
{
  "done_bounces": 1,
  "done_refusals": {"unmerged": 1},
  "sends": {"steered": 1, "queued": 2},
  "time_s": {"working": 312, "waiting": 85, "queued": 14, "idle": 42},
  "exit": "clean",
  "has_retro": true
}
```

### Friction metrics and the decisions they drive

All eight metrics below are backed by `runs.jsonl` data captured as of `v1.66.0` (with drill-down in `events.jsonl`) and can be grouped by `profile`, `model`, `effort`, `repo`, `route`, `playbook`, `agents_md`, `version` or `week`. CLI summary columns for metrics 1 and 3 to 7 are **planned** for Phase 2:

| # | Metric | Definition | Source | Improvement decision it drives |
|---|---|---|---|---|
| 1 | Done-refused rate and reason mix | Share of runs with >= 1 `done_refused`, split by reason (`uncommitted`, `unmerged`, `open_queue`, `no_merge_perm`, `max_bounces`) | `runs.jsonl` (`done_bounces`, `done_refusals`) | Tighten playbook wording for the top failure mode, or add `merge-local` to routes that hit `no_merge_perm` |
| 2 | First-pass done rate | Share of non-interactive runs reaching `done` with 0 refusals, 0 failed done-checks and 1 `done` report (95% Wilson CI) | `runs.jsonl` (`last`, `reports`, `check_result`, `done_refusals`) | Accept or revert a playbook, route, `AGENTS.md` or model change on evidence once `n >= 20` |
| 3 | Follow-up sends and interrupts per run | Median and p90 of `sends.sent + sends.steered + sends.interrupted + sends.queued + sends.held` per run | `runs.jsonl` (`sends`) | Fix weak spawn briefs, missing route defaults or unclear `AGENTS.md` rules on high-re-send repos and routes |
| 4 | User rescue and takeover rate | Share of non-interactive runs with `sends.user_pane > 0` or `sends.takeover > 0` | `runs.jsonl` (`sends`, `interactive`) | Separate truly autonomous runs from bailed-out ones; diagnose broken ACP UX, auth stalls or missing permissions |
| 5 | Attention drag (time waiting on user) | Median and weekly sum of `time_s.waiting`, paired with `resurface_wake` and `question`/`blocked` counts | `runs.jsonl` (`time_s`, `interventions`, `reports`) | Adjust `may` grants, approval defaults or preflight checks when human wait dominates lead time |
| 6 | Wall-clock vs active work vs slot wait | `first_done - created` split into `time_s.working`, `time_s.waiting` and `time_s.queued`, grouped by `effort` | `runs.jsonl` (`created`, `first_done`, `time_s`, `effort`) | Tell slow tasks (`working`) apart from concurrency starvation (`queued` -> raise `max_working`) and effort mismatch |
| 7 | Crash and error-exit rate | Share of runs with `exit == "crash"` or `time_s.error > 0`, grouped by `profile`, `driver`, `fleet_version` | `runs.jsonl` (`exit`, `time_s`), `events.jsonl` (`crash`) | Spot regressions after a fleet release or ACP adapter update and switch defaults away from unstable drivers |
| 8 | Policy-intervention noise and compaction cost | Interventions per run by kind (`noreport_wake`, `stop_block`, `status_disagree`, `recycle_wake`) alongside `compactions` and `spend/done` | `events.jsonl` + `runs.jsonl` (`interventions`, `spend`) | Retire or retune noisy watchdogs (such as false `noreport_wake` during CI waits), fix stale `tools.toml` patterns (`status_disagree`) and split tasks that trigger compactions |

## 4. Phased implementation plan

Estimates are agent wall-clock time; human review and decision time is listed separately.

| Phase | Status | Scope (files to touch) | Agent time | Human time | Done check |
|---|---|---|---|---|---|
| **Phase 1: Fix lossy data and emit core friction signals** | Built (`v1.66.0`, ADR-187) | Emit `done_refused`, `deliver` (including team, held and debounced `user_pane`), `takeover` and `crash`; derive lifetime `done_bounces` and `done_refusals`, `sends`, `time_s`, `exit` and `has_retro` in `recordRun`; add `fleet spend --since <dur>` reading `usage.json` | 30-50 min | 10-15 min | Shipped in commits `8bf4ad32` and `1d606b58` |
| **Phase 2: Query views and weekly-review wiring** | Planned | Add `run_id` and `actor` linking `Agent` -> `Event` -> `Run`; add friction columns and `--compare` (week or config-hash diff) to `fleet stats --runs` (`internal/runs/summary.go`, `cmd/fleet/stats.go`); surface 7-day crash and `status_disagree` anomalies in `fleet doctor` (`cmd/fleet/doctor.go`); update `sprint-review`'s `gather.py` (separate `agent-hub` repo) to read `fleet stats --runs --json` and `fleet spend --since 7d --json` directly | 35-60 min | 15 min | Green CI on `ai-fleet` branch; one dry run of `gather.py --days 7` showing lower transcript scan work and weekly spend populated |
| **Phase 3: Admiral session telemetry (only if needed)** | Planned | Log `admiral_reset` and a summary row on admiral handoff/reset (turn count, message count, `context_warning` count, spend, duration, open asks) to close the 87%-admiral blind spot; optional regression check when a new `fleet_version` or `playbook` hash crosses `MinRuns` | 45-90 min | 15-20 min | Green CI; one full admiral handoff emits one summary row with zero prompt or chat text |

### What this plan deliberately leaves out (YAGNI)

- No SQLite, Prometheus, Grafana or sidecar daemon: append-only JSONL files read by `fleet stats` handle years of single-user data in milliseconds.
- No prompt, report, screen or git-diff text in metrics files: prose stays in mail and tool transcripts.
- No per-keystroke logging and no LLM auto-tagging on every run: rule-based counters cost zero tokens and run in-process.
- No backfill of historical `runs.jsonl` rows: additive `omitempty` fields keep old rows readable while new runs fill the extra columns immediately.

## 5. Risks, privacy and overhead

### Privacy and public-repo boundary

- **Strict allowlist.** `events.jsonl` and `runs.jsonl` hold timestamps, durations, integer counts, short config hashes and fixed enum strings only. Never store prompts, report summaries, screen lines, file paths, commit subjects, tokens or emails.
- **Bounded detail field.** `board.Event.Detail` stays capped at 200 bytes (`detailMax`) and carries short codes (`unmerged`, `steered`, `exit=1`), never error prose.
- **Local-only files.** `runs.jsonl` is created `0600` under `~/.fleet/`. `repo` stores `filepath.Base` only (`internal/fleet/runs.go`). Never paste raw `runs.jsonl` repo or branch names into public GitHub issues or commits; `scripts/check-public.sh` gates repo commits.

### Runtime and disk overhead

- **Measured baseline.** On live `v1.65.1` state, `events.jsonl` averaged about 980 lines/day (95 KB/day) and `runs.jsonl` about 515 bytes/run.
- **Expected delta.** Four low-frequency event kinds add roughly 10-15% to `events.jsonl` (bounded by the watcher's 40-day `RotateEvents` cap) and about 150 bytes per line to `runs.jsonl` (roughly 3 MB/year at 50 runs/day).
- **Debounced pane typing.** `interactive.go` checks tmux `client_activity` on every refresh tick. ADR-187 emits `user_pane` only when a key press follows at least 60 seconds without one (from the housekeeping process only), matching how `noteDisagree` dedupes `status_disagree`.
- **Close-time rollup cost.** `recordRun` scans `events.jsonl` from `a.Created` in one pass (`internal/fleet/runs.go`), summing state durations and friction counters with zero extra file reads and single-digit millisecond latency at current file sizes.

### Measurement traps

- **Small-sample noise.** Keep ADR-170's `MinRuns = 20` threshold and 95% Wilson intervals so three-run fluctuations do not drive playbook churn.
- **Metric gaming.** Never put friction targets into worker playbooks (`internal/playbook/`). Pair speed (`to_done`) with quality (`first_pass_done`, `check_pass`, `rescue_rate`) so fast sloppy runs stand out instead of looking good.
- **Backward compatibility.** All new JSON keys use `omitempty`. Older records and older binaries reading the same `~/.fleet` directory stay compatible.
