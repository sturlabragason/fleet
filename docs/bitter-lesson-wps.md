# Bitter Lesson work packages

Twelve briefs that carry out [bitter-lesson-plan.md](bitter-lesson-plan.md). Each is a fleet task:
the admiral spawns a worker with the brief as its task, claims an ADR number with
`fleet adr claim --for <worker>` and puts it in the prompt. The worker gets the fleet playbook and
the repo's `AGENTS.md`, so each brief carries only what is specific to it.

Written 2026-11-16 against `main` at `882eb4c9`. Line numbers will drift; each brief names the
identifier as well, so search for it if the line has moved.

## How to run them

| Step | Package | Depends on | Effort | Sturla's OK |
|---|---|---|---|---|
| 1 | WP1 Intervention ledger | none | M | no |
| 2 | WP2 Prose diet | none | S | yes: amends ADR-86 |
| 3 | WP3 Repo conventions out of the core | none | M | yes: built-in playbook text |
| 4 | WP4 Summariser prompt as data | none | S | no |
| 5 | WP5 Model defaults that follow the tool | none | S | yes: which model agents get |
| 6 | A week of normal use with WP1 recording | WP1 | none | no |
| 7 | WP6 Events decide status | WP1 and step 6 | M | yes: amends ADR-49 |
| 7 | WP7 Fill for every admiral | WP1 | M | no |
| 7 | WP8 Per-profile nudge switches | WP1 | S | no |
| 8 | WP9 Patterns all in data, retired on evidence | WP1, WP6 | S | no |
| 8 | WP10 Adapter freeze and first removal | WP1 | M | yes: one real Copilot session |
| 9 | WP11 Rule measurement | WP1, WP2, WP8 | M | yes: spends tokens |
| 10 | WP12 One tool vocabulary (experiment) | WP2 | L | yes: changes every session's tools |

S is under half a day of agent time, M about a day, L two or more. Steps 1 to 5 touch disjoint
files except WP2 and WP3 (both edit `internal/playbook/worker.md`) and WP2 and WP5 (both edit
`internal/tools/tools.go`): run WP2 first, or have them coordinate with `fleet touching`. Run at most four at once (`max_working`).

Rules every package follows, from `AGENTS.md`: work in the `fleet/<name>` worktree; one
`CHANGELOG.md` line for a user-facing change; `make build && make lint && go vet` on the changed
packages locally, at most the one named test with `nice go test -run`; all tests in CI on the pushed
branch; `fleet merge` after green CI; tests use `fleet-test-*` tmux sockets, the fake agent and
`FLEET_CONFIG` pinned to a nonexistent path, never the live `fleet` socket or `~/.fleet`.

Every done condition below ends with this CI check on the branch head:

```sh
test "$(gh run list --branch "$(git branch --show-current)" -L1 --json headSha,conclusion -q '.[0].headSha+" "+.[0].conclusion')" = "$(git rev-parse HEAD) success"
```

## WP1: intervention ledger

**Goal.** Fleet records every time its own policy acts on an agent, so later packages can delete
policy on evidence. No model call; this is counting.

**Context.** Plan finding 12. Fleet logs status changes to `~/.fleet/events.jsonl`
(`internal/board/board.go:618-627`, `Store.AppendEvent`), with no size bound. ADR-45, ADR-68, ADR-71
and ADR-105 each added a nudge or block after an incident; none can be retired without a count.

**Scope.**
- Extend `events.jsonl` with intervention lines (a `kind` field next to today's transitions) rather
  than a second file, unless the existing readers break on new kinds; say which in the ADR.
- Each line: time, agent, profile, model, kind, detail. Never prompt, report or screen text.
- Kinds: `stop_block` (`internal/board/stopblock.go`, `cmd/fleet/watch.go` `hookBlockStop`),
  `noreport_wake` (`internal/fleet/refresh.go`), `stuck_wake` (`internal/status/stuck.go`),
  `recycle_wake` (`internal/fleet/recycle.go`), `resurface_wake` (`internal/fleet/resurface.go`),
  `send_refused` with the reason (`internal/fleet/interactive.go`, `internal/mcp/send.go`),
  `confirm_refused`, `model_refused` (`checkModel`), `donecheck` pass or fail
  (`internal/fleet/report.go`), `context_warning` (`internal/mcp/server.go` `contextWarning`), and
  `status_disagree` when hook or ACP state and the screen differ (`internal/fleet/watchpaths.go`
  `hookState`, `refresh.go`). A disagreement records both values and which pattern set, kind
  (busy, idle, waiting, error, menu) and index matched, so WP9 can count pattern hits.
- Rotation with one owner: the `fleet watch` process rotates (rename to `events.jsonl.1`, keep 40
  days like `internal/usage/ledger.go` `Keep`); other writers only append with `O_APPEND` and a
  line under `PIPE_BUF`, so a rename mid-write loses nothing.
- `fleet stats [--since D] [--by profile|model|kind] [--json]`, an entry in the command table
  (AGENTS.md Go rule 3).

**Out of scope.** Changing any nudge, threshold or status rule.

**Files.** `internal/board/board.go` and test, the call sites above, `cmd/fleet` command table and
usage text, `internal/mcp/instructions.md` only if the admiral gets a stats tool (it need not),
`DESIGN.md` (ADR, §7 logs), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/... ./cmd/...` exits 0; the branch
adds `TestEventKinds` (every kind above written once by a table-driven test) and `TestEventRotate`
(rotation while another goroutine appends loses no line) in `internal/board`; and
`FLEET_HOME=$(mktemp -d) FLEET_CONFIG=/nonexistent ./bin/fleet stats --json` prints `{}` or an empty
list. Then the CI check.

**Needs Sturla's OK.** No.

## WP2: prose diet

**Goal.** The admiral instructions and worker playbook stop repeating the MCP schema and stop sending
one tool's or model's quirks to every agent.

**Context.** Plan findings 4 and 5. `internal/mcp/instructions.md` (203 lines) carries a tool table
of about 40 rows that repeats each tool's `description` (`internal/mcp/tools.go`, `admirals.go`,
`clitools.go`, `adr.go`). ADR-86 requires that table, `TestEveryToolInPromptTable`
(`internal/mcp/instructions_test.go:102`) enforces it, and ADR-86 rejected per-profile prompts for
the admiral (`DESIGN.md:1870`). Tool words in shared prose: `ultrathink` (`instructions.md:78`),
`tool_search_tool` (`internal/playbook/worker.md:10`), `ultrathink` for squad leads (`worker.md:21`), `task_complete` (`worker.md:40`,
`internal/mcp/agent_instructions.md:18`, `instructions.md:198`).

**Scope.**
1. Write the ADR amending ADR-86: the table shrinks to tool names grouped by job (no per-tool
   guidance); each tool's `description` carries the guidance. Change
   `TestEveryToolInPromptTable` to check names only. Move any guidance a description lacks into it.
2. Add an optional `prompt_note` key to the profile data (`internal/tools` `Profile`, embedded
   `Defaults` TOML, overridable in `~/.fleet/tools.toml`). Spawn appends it after the playbook for
   that profile's workers (`internal/fleet/fleet.go` `launch`). The admiral does not get one
   (ADR-86's single rule set stands).
3. Worker lines move to notes: copilot family gets `tool_search_tool` and `task_complete`.
4. `ultrathink`: add an `effort_prefix` profile key (claude family: `ultrathink`), show it in
   `fleet_info`, and make the admiral line (and the squad-lead line in `worker.md:21`) "send the target's effort
   prefix, if it has one". The
   admiral's `task_complete` line says "never end your turn through an autopilot finish tool".
5. Model preference: keep ADR-100's meaning. If you move family names into config, the prose names
   the key. Any change of wording is listed in the report for Sturla.
6. Do not touch the safety core (ADR-59).

**Out of scope.** Deleting procedural rules (WP11), the MCP tool set (WP12).

**Files.** `internal/mcp/*.md`, `internal/mcp/instructions_test.go`, MCP tool descriptions in
`internal/mcp/*.go`, `internal/playbook/*.md`, `internal/tools/tools.go` and test,
`internal/fleet/fleet.go`, `internal/fleet/info.go`, `DESIGN.md` (ADR, §4), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/mcp/... ./internal/tools/... ./internal/fleet/... ./internal/playbook/...`
exits 0; `! grep -nE 'ultrathink|tool_search_tool|task_complete' internal/mcp/*.md internal/playbook/*.md`
exits 0; `test "$(wc -l < internal/mcp/instructions.md)" -lt 170`; the branch adds
`TestPromptNoteAppended` (a profile with a note gets it, one without does not). Then the CI check.
The report gives line and byte counts before and after.

**Needs Sturla's OK.** Yes, for the ADR-86 amendment and any change to the model-preference wording.

## WP3: repo conventions out of the core

**Goal.** The built-in playbook and `fleet merge` and `fleet adr` stop carrying this repo's layout to
every repo fleet runs on.

**Context.** Plan finding 10. `internal/playbook/worker.md` names `DESIGN.md §14` (line 18),
`scripts/inline-html.py` (19), `go build ./... && go vet` (24) and the CHANGELOG.md and DESIGN.md
conflict rule (31, 35). A per-repo layer exists: `.fleet/worker.md` (`internal/playbook/playbook.go:182`).
`internal/worktree/merge.go:17-27` `SharedDocs`, `cmd/fleet/merge.go:23-25` `defaultMergeCheck` and
`internal/adr/design.go:12-14` `entryRe` hard-code the rest (ADR-48, ADR-50).

**Scope.**
1. Move those playbook lines into this repo's `.fleet/worker.md`; the built-in text states the
   general rule ("run the repo's checks", "the repo's append-only docs: keep both sides"); the
   `--check '<build> && go vet'` example on line 31 becomes "the repo's cheap check".
2. A `.fleet/repo.toml` with `merge_check`, `[[append_only]]` (`path`, `order`) and `[adr]` (`file`,
   `entry` regex with one number group), read by `fleet merge` and `fleet adr` from the repo they
   act on. Add this repo's file with today's values.
3. With no file, today's values stay the default and `fleet merge` prints a one-line note naming
   the file. Dropping that fallback is a later decision.

**Out of scope.** Merge order rules, the ADR claim store.

**Files.** `internal/playbook/worker.md`, `.fleet/worker.md`, `internal/worktree/merge.go`,
`cmd/fleet/merge.go`, `internal/adr/*.go`, tests, `.fleet/repo.toml`, `docs/process.md`, `DESIGN.md`
(ADR amending ADR-48 and ADR-50, §4 playbook), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/worktree/... ./internal/adr/... ./internal/playbook/... ./cmd/...`
exits 0; `! grep -nE 'DESIGN\.md|CHANGELOG\.md|inline-html|go vet' internal/playbook/worker.md`
exits 0; the branch adds `TestRepoConfig` (a temp repo with a `repo.toml` naming other files is
honoured; one without falls back) in `internal/worktree`; `./bin/fleet merge --dry-run` from this
worktree behaves as before. Then the CI check.

**Needs Sturla's OK.** Yes, for the built-in playbook wording (every agent reads it).

## WP4: summariser prompt as data

**Goal.** The summariser's prompt is data a user can change, and its input is labelled truthfully.

**Context.** Plan finding 11. `internal/summary/prompt.go:16-40` is a Go constant. It labels the
board status `Scraped status`, but `internal/fleet/summary.go:56` passes `a.Status`, the status after
hook and ACP precedence. ADR-6, ADR-40.

**Scope.** Rename the label to `Board status`. Move the instruction text into the `[summary]`
defaults with a `prompt` key (`internal/prefs/summary.go`). Keep `Clean()` and the output contract.

**Out of scope.** The model, command, cache and rate limit.

**Files.** `internal/summary/prompt.go`, tests, `internal/prefs/summary.go`,
`examples/config.toml.example`, `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/summary/... ./internal/prefs/...`
exits 0; `! grep -n 'Scraped status' internal/summary/*.go`; the branch adds `TestSummaryPromptOverride`.
Then the CI check.

**Needs Sturla's OK.** No.

## WP5: model defaults that follow the tool

**Goal.** A newer model from the tool reaches agents without a fleet release, where Sturla wants it
to, and unknown rate-limit windows are shown instead of dropped.

**Context.** Plan finding 3. `CopilotClaudeModel` and `ClaudeACPVersion` (`internal/tools/tools.go:240-253`)
are spliced into the overridable `Defaults` TOML (lines 259-263, 315, 349). `ClaudeModel = "opus"` is
an alias Claude resolves to its newest model: the pattern to copy. Copilot needs an exact id; fleet
already asks tools for their model list over ACP (`internal/fleet/models.go`). `windowOrder` and
`windowLabel` (`internal/acphost/meter.go:37-43`) drop windows they don't know. ADR-100 and ADR-112
are Sturla's model decisions.

**Scope.**
- A profile key (for example `model_family = "claude-opus"`): when set and the tool lists models, the
  default model is the newest id in that family by version number, else the pinned id. Off by
  default unless Sturla says on.
- Unknown rate-limit windows are shown by their raw name, after the known ones.

**Out of scope.** Moving constants into a separate file (they are already overridable), prices, the
Copilot exclusion list (ADR-60; re-measure per Copilot release instead).

**Files.** `internal/tools/tools.go` and test, `internal/fleet/models.go` and test,
`internal/acphost/meter.go` and test, `docs/profiles.md`, `DESIGN.md` (ADR), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/tools/... ./internal/fleet/... ./internal/acphost/...`
exits 0; the branch adds `TestNewestOfFamily` (table: ordering, other families ignored, empty list
falls back to the pin) and `TestUnknownWindowShown`. Then the CI check.

**Needs Sturla's OK.** Yes: whether the newest-of-family rule is on by default.

## WP6: events decide status

**Goal.** For a profile with an event source (ACP, hooks or an extension), the tool's own events
decide working and idle; the screen fills in what events cannot say.

**Context.** Plan finding 1. `internal/fleet/watchpaths.go:82-102` `hookState` overrides the screen
only when it matched nothing or the hook is newer; `refresh.go:324-329` applies it. ADR-7 chose the
screen first; ADR-49 rejected option (g) "making hooks authoritative over the screen" because a
killed tool leaves its last hook behind. `HookWorkingMax` (`watchpaths.go:104-106`) now bounds that.
Turn detection and the no-report wake run off status transitions (`refresh.go`, ADR-68).

**Scope.**
- Start only after a week of WP1 data: report `status_disagree` counts per profile and state
  (`fleet stats --by profile`) in the ADR. If disagreement is not rare, stop and report.
- A profile trait (data, not Go) says it has an event source. For those, the event wins for working
  and idle, with `HookWorkingMax` kept.
- Kept as rules with tests: a menu or prompt on screen forces `waiting` before any event
  (`refresh.go:302-308`; ADR-47 trust dialogs are modals no hook reports); no event since spawn means
  the screen decides; ADR-68 turn timing is unchanged.
- vibe and any profile without events behave exactly as today.

**Out of scope.** Deleting patterns (WP9). ACP status (`internal/acp/tracker.go` already uses events).

**Files.** `internal/fleet/watchpaths.go`, `refresh.go`, tests, profile data in `internal/tools`,
`DESIGN.md` (§4, ADR amending ADR-7 and ADR-49), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/fleet/... ./internal/tools/...`
exits 0; the branch adds `TestEventBeatsScreen`, `TestMenuBeatsEvent`, `TestStaleHookFallsBack`
and `TestNoEventUsesScreen` in `internal/fleet`, with a temp `FLEET_HOME` and a `fleet-test-*`
socket if any starts tmux. Then the CI check.

**Needs Sturla's OK.** Yes: it amends ADR-49. Ask with the week's disagreement numbers.

## WP7: fill for every admiral

**Goal.** Every admiral sees its own context fill and decides when to hand off; the token line is a
cost guard set in config.

**Context.** Plan finding 8. `internal/supervisor/pane.go:72-74` `CurrentFillOf` reads only the
Copilot session, so a Claude admiral gets no fill and no warning. `internal/supervisor/fill.go:124-155`
`Threshold` (70%) and `HandoffTokens` (80,000) come from environment variables. ADR-11, ADR-93.

**Scope.**
- Fill for a Claude admiral from its transcript (the usage fields `internal/spend` reads) and for an
  ACP admiral from `usage_update` (`internal/acphost/meter.go`).
- `handoff_tokens` and `handoff_pct` in `config.toml` `[admiral]`; the environment variables stay
  as overrides for one release.
- `fleet_info` and the warning line show fill, tokens and compactions; the instructions' Memory
  section says to weigh them, not obey a number. Worker `fleet_info` shows compactions, spend and
  fill.

**Out of scope.** Forcing a handoff; changing the default numbers.

**Files.** `internal/supervisor/*.go`, `internal/mcp/server.go`, `internal/mcp/instructions.md`
(Memory only), `internal/prefs`, `internal/fleet/info.go`, tests, `DESIGN.md` (§6.5, ADR amending
ADR-93), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/supervisor/... ./internal/mcp/... ./internal/prefs/... ./internal/fleet/...`
exits 0; the branch adds `TestFillPerAdmiralProfile` (copilot, claude and ACP fixtures each give a
fill). Then the CI check.

**Needs Sturla's OK.** No.

## WP8: per-profile nudge switches

**Goal.** The two nudges that change an agent's behaviour can be turned off per profile, and every
nudge is counted.

**Context.** Plan finding 7. Behavioural: the stop-hook block (`internal/board/stopblock.go`,
ADR-105) and the recycle advice (`internal/fleet/recycle.go`, ADR-71). Information, kept as is: the
no-report wake (ADR-68), the stuck wake (ADR-45), the 2-hour re-surface (`internal/fleet/resurface.go`,
`[reports] resurface_after`, ADR-130) and the unread suffix.

**Scope.**
- Profile keys `stop_block` and `recycle_advice` (booleans, default on), read through
  `internal/prefs` like `stuck_after`. Recycle with the advice off sends the fact only.
- Each nudge records its WP1 kind when it fires, whether switched or not.
- `fleet doctor` lists profiles with a nudge off.

**Out of scope.** Turning any default off.

**Files.** `internal/prefs`, `internal/board/stopblock.go`, `cmd/fleet/watch.go`,
`internal/fleet/recycle.go`, tests, `examples/config.toml.example`, `DESIGN.md` (ADR), `CHANGELOG.md`.

**Done condition.** `make build && make lint && go vet ./internal/prefs/... ./internal/fleet/... ./internal/board/... ./cmd/...`
exits 0; the branch adds `TestStopBlockSwitch` and `TestRecycleAdviceSwitch` (on and off each).
Then the CI check.

**Needs Sturla's OK.** No.

## WP9: patterns all in data, retired on evidence

**Goal.** Every screen regex is pattern data, and patterns that no longer decide anything are removed.

**Context.** Plan finding 1. AGENTS.md says status patterns are data, but `internal/fleet/info.go:137`
(`modeRe`, Copilot's mode footer) and `internal/fleet/trust.go:131-135` and `193` (`trustMenu`,
`trustHint`) are Go. Pattern
sets are in `internal/status/status.go:96-144`, 45 fixtures pin them.

**Scope.**
1. Move `modeRe`, `trustMenu` and `trustHint` into the pattern sets (new kinds if needed), loaded like the rest.
   Can run at any time.
2. After WP6 has run a week: from WP1's pattern-hit counts, list patterns of event-sourced profiles
   that never decided a status. Remove those and their fixtures; keep menu patterns and vibe's set.

**Out of scope.** Changing status precedence (WP6).

**Files.** `internal/status/*.go`, `internal/fleet/info.go`, `internal/fleet/trust.go`, tests,
fixtures, `DESIGN.md`, `CHANGELOG.md` for step 2.

**Done condition.** `make build && make lint && go vet ./internal/status/... ./internal/fleet/...`
exits 0; `! grep -n 'regexp.MustCompile' internal/fleet/info.go internal/fleet/trust.go`; the
existing fixture tests pass in CI. Step 2's report lists every removed pattern with its hit count.
Then the CI check.

**Needs Sturla's OK.** No.

## WP10: adapter freeze and first removal

**Goal.** No new per-tool Go parser is added again, and the first one goes where a tool-reported
source covers it.

**Context.** Plan finding 2. `internal/status/subagents.go` and `subagents_read.go` (about 495 lines)
parse Copilot's `events.jsonl`; agy's subagents are read by its tool name `invoke_subagent`
(`internal/acp/wire.go:381` `SubagentTool`, used in `internal/acphost/control.go` and
`internal/fake/acp.go`), because ACP has no subagent notion. Hook-based subagent state already
exists: `internal/fleet/subagents.go` `ReadHookSubs`. ADR-126.

**Scope.**
1. Add to `DESIGN.md` §15 Extensions: a new tool reports status, usage and subagents over ACP or
   hooks; a tool with neither gets screen status only. A tool-call name that marks a subagent is
   profile data, not Go.
2. Find out, in one short real Copilot session (Sturla's OK), whether Copilot's SubagentStart and
   SubagentStop hooks fire for its subagents. Record the date and version in the ADR.
3. If yes, route Copilot to `ReadHookSubs` and delete the `events.jsonl` path. If no, stop and
   report what is missing. Move `invoke_subagent` into agy's profile data either way.

**Out of scope.** Spend and session parsers.

**Files.** `DESIGN.md`, `internal/status/subagents*.go`, `internal/fleet/subagents.go`,
`internal/acp/wire.go`, `internal/acphost/control.go`, `internal/fake/acp.go`,
`internal/tools` profile data, tests, `CHANGELOG.md` if behaviour changes.

**Done condition.** `make build && make lint && go vet ./internal/status/... ./internal/fleet/... ./internal/tools/...`
exits 0; `grep -qi 'no new per-tool' DESIGN.md`; `! grep -rn '"invoke_subagent"' internal/acp internal/acphost --include='*.go' --exclude='*_test.go'`.
Then the CI check.

**Needs Sturla's OK.** Yes, for the real Copilot session.

## WP11: rule measurement

**Goal.** Each procedural rule in the admiral instructions and worker playbook is tied to a number
that shows whether it still matters, and rules that don't are proposed for deletion.

**Context.** Plan findings 5 and 7. After WP2 the prose holds goals, fleet protocol, the safety core
(ADR-59), guards and procedural rules; only procedural rules are in scope. "Delegate by default" is
out: ADR-135 and `--available-tools fleet` enforce it whatever the prose says.

**Scope.**
1. Tag each procedural rule with its source ADR in an HTML comment the loader strips (check it does).
2. Primary evidence: WP1 rates per profile and model with WP8's switches off for one profile for a
   week (stop blocks per turn, reports missing, done-check fails).
3. Smoke ablation only where step 2 can't see a rule: five fixed tasks with machine checks, each run
   three times with and without the rule; a rule is "no effect" only if all runs agree. Dry run
   first; real runs only on Sturla's OK and budget.
4. Report a table: rule, evidence, proposed keep or delete. Delete only what Sturla approves.

**Out of scope.** The safety core, guards.

**Files.** `internal/mcp/*.md`, `internal/playbook/*.md`, `scripts/ablate.sh`, fixtures,
`docs/process.md`, `DESIGN.md` (ADR).

**Done condition.** `make build && make lint && test -x scripts/ablate.sh && scripts/ablate.sh --dry-run`
exits 0 and prints the runs and a token estimate without spending anything; the report has the
table. Then the CI check.

**Needs Sturla's OK.** Yes: real runs spend tokens and deletions change every agent.

## WP12: one tool vocabulary (experiment)

**Goal.** Find out whether one set of tool names for every role is worth the churn. The token saving
is close to zero for a leader; the argument is one vocabulary.

**Context.** Plan finding 6. 40 admiral tools, 9 worker, 17 leader (`grep -c '"fleet_squad_'
internal/mcp/squad.go` gives 16 occurrences for 8 tools). Mail already shares names across roles.
Access has two models: the admiral deciding between agents (`fleet_access`, `fleet_access_decide`)
and peer requests (`fleet_access_request`, `_respond`, `_revoke`); keep them separate.

**Scope.** In a spike worktree, rename the eight squad tools to the admiral names on the leader
server, scoped by the handler (leader refusals of ADR-53 stay). Measure schema bytes per server
before and after, run a fake-agent squad, and write the result into an ADR as accepted or rejected.
Merge only if Sturla accepts, with the old names as aliases for one release.

**Out of scope.** CLI wrapper tools, access, role permissions.

**Files.** `internal/mcp/squad.go`, `internal/mcp/leader_instructions.md`, `internal/playbook/leader.md`,
`internal/mcp/parity.go`, tests, `DESIGN.md`.

**Done condition.** The branch adds `TestToolsPerServer` (tools/list names per server match a
table) and the report gives schema bytes before and after. Then the CI check, if merged.

**Needs Sturla's OK.** Yes, before merge.
