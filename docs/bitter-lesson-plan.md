# Bitter Lesson audit of fleet

This audit checks fleet against Rich Sutton's "The Bitter Lesson" (2019) and finds where the harness
encodes knowledge about today's models and tools that will rot as models improve. It ends with a
phased plan; [bitter-lesson-wps.md](bitter-lesson-wps.md) turns the plan into work packages.

Checked 2026-11-16 on `main` at `882eb4c9`: two read-only code inventories (taken at `2fa087e7`,
three commits earlier, none of which touch the cited lines except `internal/mcp/send.go`,
`internal/mcp/tools.go` and `internal/mcp/instructions.md`, which were re-read), plus a re-read of
the injected prose and the 27 ADRs it cites. Not measured: how often any nudge, guard or block fires in real
sessions. Fleet does not record that today, and finding 12 is about exactly that gap.

## Bottom line

Fleet is better placed than most harnesses: it already routes by meaning, asks tools which models
they have, reads spend from the tools' own files and verifies work by running it in CI. Its debt is
in four places: status still comes from regexes over terminal screens even where the tool reports
events, the admiral and worker prose carry procedure written against specific model failures, and
the built-in playbook carries this repo's own conventions to every repo, and there is no
measurement of whether any of the procedure still earns its tokens. The plan puts
measurement first, moves model and repo knowledge out of Go into data, makes protocol events
authoritative, and then deletes procedure on evidence.

## The essay, applied to a harness

Sutton's argument in four steps: researchers build in what they know about a domain; it helps in the
short run; it plateaus and gets in the way; general methods that scale with computation (search and
learning) win in the end. His second point is that we should "build in only the meta-methods that can
find and capture this arbitrary complexity", not the complexity itself.

A coding harness is not a learner, so the mapping needs care.

| Essay | In a harness like fleet |
|---|---|
| Computation that keeps getting cheaper | Model capability per generation, context window size, falling price per token |
| Search | Verification by execution: run the check, run CI, try again. It gets better as the model writing the code gets better |
| Learning | Measured outcomes deciding policy, instead of a number someone picked after one incident |
| Built-in human knowledge | Regexes for a tool's UI, prose rules against one model's habits, price tables, fixed thresholds, fixed character caps, procedure the coordinator must follow step by step |
| Meta-methods | Protocols (ACP, hooks), isolation (worktrees, panes), persistence (board, mail), tools the model calls when it decides to, and a ledger of what happened |

The test for every item in the inventories: if next year's model is twice as capable, does this item
gain value, stay the same, become dead weight, or get in the way? Items that gain or stay are
mechanism. Items that become dead weight are knowledge to move or delete. Items that get in the way
are the urgent ones: they cap what a better model can do.

Two things the essay does not argue against, and this audit keeps:

- **Guards.** A write fence, a confirmation gate or a memory guard exists because of risk, cost or a
  shared machine, not because the model is weak. A better model does not make a force-push less
  irreversible. These stay; the plan only asks that each one says which kind it is.
- **Adapters.** Fleet has to drive other people's CLIs. Reading a tool's session file is plumbing,
  not a theory of mind. The Bitter Lesson point is narrower: put that knowledge in data and protocols,
  prefer what the tool reports over what fleet infers, and add no new hand-written per-tool code.

## Classification

Every inventory item falls into one of four classes. Counts are of distinct items, roughly.

| Class | Meaning | What to do | Items |
|---|---|---|---|
| a. Mechanism | Scales with better models | Keep | ~25 |
| b. Knowledge that rots | Facts about today's models or tools | Move to data, a protocol or the model | ~30 |
| c. Policy the harness decides | A capable coordinator could decide it | Make it a default, expose the data, or delete on evidence | ~25 |
| d. Guard | Safety, cost or shared-resource protection | Keep, label as a guard | ~15 |

The rest of this document goes area by area, largest first.

## Findings

### 1. Status is read from the screen first, events second (b)

**Status (2026-11-26): done in ADR-205.** Events decide working and idle; the screen keeps menus,
prompts and errors. The week's ledger showed one wrong event, Claude's `idle_prompt` read as waiting
(267 of 281 disagreements), fixed at the mapping. Pattern retirement is still WP9.

Under tmux, a pane's status is the result of about 77 regexes in six per-tool sets
(`internal/status/status.go:96-144`), a 14-line scan window (`status.go:217`), menu scraping
(`internal/status/menu.go`, `menu_plain.go`, about 274 lines) and a box-glyph cleaner
(`status/screen.go:13-55`). The comment block above the patterns (`status.go:40-95`) is a catalogue of
rot: spinner glyphs that differ by OS, a non-breaking space in Claude's empty prompt, a vibe warning
banner that must not read as an error, a vibe pattern "untested until someone snaps one". 45 pane
fixtures and about 1,300 test lines in `internal/status` pin it. Three more screen regexes sit in Go
outside the pattern data, although AGENTS.md says patterns are data: Copilot's mode footer
(`internal/fleet/info.go:137`), the folder-trust dialog (`internal/fleet/trust.go:131-135`) and its
hint line (`trust.go:193`, `trustHint`).

The hook state, which the tool itself emits, only overrides the screen when the pane said nothing
fleet recognises or the hook fired after the pane last changed (`internal/fleet/watchpaths.go:82-102`,
applied at `internal/fleet/refresh.go:324-329`). So for Claude under tmux, which sends hooks
for every turn, the regexes still decide most refreshes.

ADR-7 chose this on purpose ("scrape panes first, events and hooks later") because scraping worked
with all four tools on day one. ADR-49 then rejected "making hooks authoritative over the screen"
(rejected option (g), `DESIGN.md:1550`) because a killed tool leaves its last hook behind and the
agent would stay working forever. Since then `HookWorkingMax` (`watchpaths.go:104-106`) bounds exactly
that case, ACP became the default for copilot and agy (ADR-74), Claude and Copilot under tmux get a
plugin with hooks (ADR-99), and Pi has an extension (ADR-148). Only vibe has nothing but its screen.

Replacement: invert the precedence, on evidence and with two rules kept. Where a profile has an event
source, the event decides working and idle; the screen fills in what events cannot say (menu labels,
typed-but-unsent input). Kept: a menu or prompt on screen beats any event (`refresh.go:302-308`; the
folder-trust and import dialogs of ADR-47 are modals no hook reports), and a stale working hook times
out to the screen (`HookWorkingMax`, the ADR-49 (g) protection). Turn detection and the no-report wake
run off status transitions (`refresh.go` `newTurn`, ADR-68), so the inversion has to keep their timing.
Evidence it is safe: a disagreement counter (finding 12) showing events and screen agree for a week
of normal use, per profile, before the switch. The stray Go regexes move into the pattern data.

Menu scraping is the one piece that stays useful longer: `fleet_answer` needs the option labels the
user sees. ACP already carries permission options; hooks carry the question text. The menu reader can
shrink to the tmux-only fallback once those are used.

### 2. Per-tool adapters written in Go (b)

The largest single block of tool knowledge is the Copilot `events.jsonl` subagent state machine
(`internal/status/subagents.go` and `subagents_read.go`, about 495 lines). Next are four per-tool usage
parsers (`internal/spend/spend.go:178-442`, about 265 lines), the hook payload reader that reads
two field spellings from three senders (`internal/status/hooksubs.go`, about 217 lines), two session-file
parsers (`internal/sessions/sessions.go:239-351`) and a model-to-profile match by the first dash
token of a model name (`sessions.go:376-397`).

These are adapters and fleet needs them. The rot is that each new tool or tool version means new Go
code and a release, and that each one infers what the tool could report. Part of the protocol path
exists: ACP `usage_update` carries the context size and use (`internal/acphost/meter.go:75-78`) and the
ACP model list is asked for rather than hard-coded (`internal/fleet/models.go`). Subagents are not in
ACP at all: agy's are read from fleet's own ACP transcript by agy's tool name `invoke_subagent`
(`internal/acp/wire.go:381` `SubagentTool`, read by `ReadACPSubagents`), which is per-tool inference again. The general source
for subagents is the tool's own SubagentStart and SubagentStop hooks (`internal/fleet/subagents.go`
`ReadHookSubs`).

Replacement: a rule, not a rewrite. No new per-tool Go parser; a new tool comes in over ACP or a
hook, or it gets the screen and nothing else. Existing parsers are frozen and deleted one by one when
the protocol path covers the same fact for that tool. The Copilot `events.jsonl` subagent tracker is the
first candidate where the hook source covers Copilot's subagents; where only a tool-call name tells
fleet a subagent started, that name belongs in profile data, not in Go.

### 3. Model knowledge compiled into the binary (b)

| What | Where | Rots when |
|---|---|---|
| `CopilotClaudeModel = "claude-opus-5.5"`, an exact id | `internal/tools/tools.go:245-253` | The next Opus ships; a fleet release bumps the default, and a test (`tools_test.go`) fails if any profile pins another |
| `ClaudeACPVersion = "0.103.0"` | `internal/tools/tools.go:240-243` | The adapter "releases daily, with breaking changes" (its own comment) |
| Copilot built-in tool names to exclude per effort | `internal/tools/tools.go:266-284` | Copilot renames, adds or defers a built-in tool |
| Summariser model `claude-haiku-5.5` via `copilot` | `internal/summary/model.go:23-29` | A cheaper or better small model ships |
| Price per family, AIC rate, month allowance | `internal/usage/price.go:20-38` | Prices change, a family is added |
| Rate-limit window names the footer knows | `internal/acphost/meter.go:37-43` | A tool adds a window type; unknown ones are dropped |

This finding is smaller than it looks. The model and version constants are already spliced into the
embedded `Defaults` TOML (`tools.go:259-263`, `315`, `349`), and a user can already override
`default_model`, `context_args` and `[summary] model` (`internal/prefs/summary.go`) without a release.
Moving them to an embedded file would still need a release to change the default. The window list is
the one item a user cannot override at all.

`ClaudeModel = "opus"` is the pattern to copy, not a problem: an alias the tool resolves to its newest
model. Copilot needs an exact id, so the scalable equivalent is to resolve "newest of a family" from
the model list the tool reports over ACP (fleet already asks, `internal/fleet/models.go`), with the
pinned id as the fallback. That changes which model agents get without a release, so it is Sturla's
call (ADR-100 and ADR-112 are his decisions). Unknown rate-limit windows should be shown by their raw
name instead of dropped.

The exclusion list is different in kind: it exists because Copilot sends every built-in tool schema
on every call (ADR-60, measured about 20k tokens at the light level). That cost is real today. The
better mechanism is the client deferring tool schemas, which Claude Code already does for MCP tools.
Keep the list and re-measure the start-up tokens each Copilot release; delete an entry when its tool
is deferred.

### 4. Model and tool words in prose sent to everyone (b)

The worker playbook and the admiral instructions name specific models and client features:

- `ultrathink` (`internal/mcp/instructions.md:78`, and for squad leads `internal/playbook/worker.md:21`):
  a Claude Code keyword the admiral or a lead is told to send to a stuck agent. It is right for a Claude agent and noise for any other.
- `tool_search_tool` (`internal/playbook/worker.md:10`): a Copilot deferral detail.
- `task_complete` (`worker.md:40`, `internal/mcp/agent_instructions.md:18`, `instructions.md:198`): a
  Copilot autopilot tool.
- "Opus by default, never Haiku. Sonnet only for..." (since 2026-11-19: "search and summary subagents run Gemini 4 Hydrogen; all else Opus") (`worker.md:21`, and the admiral's spawning
  section): model names as rules. This is Sturla's standing decision (ADR-100), so its meaning stays;
  only where it lives is in question.

Every agent pays tokens for every tool's quirks, and each line goes stale with one release.

Replacement: worker-side lines move to a per-profile note in the profile data, appended only for that
tool's agents (a worker's first prompt is already per-profile, so this does not touch ADR-86, which
is about the admiral's rules). `ultrathink` is different, because the admiral sends it to another
agent: it becomes a profile key (for example `effort_prefix`) that `fleet_info` shows for the target,
and the admiral line says "send the target's effort prefix if it has one". The model preference keeps
today's meaning, with the family names in config and the prose naming the config key; changing the
wording needs Sturla's OK.

### 5. Prose that compensates for observed failures (c)

`internal/mcp/instructions.md` is 203 lines and goes into every admiral session. Its tool table
(about 40 rows) repeats what each tool's own `description` already says in the MCP schema
(`internal/mcp/tools.go:49` onward), so the admiral reads most tool guidance twice. The table is
there on purpose: ADR-86 requires it, `TestEveryToolInPromptTable`
(`internal/mcp/instructions_test.go:102`) enforces it, and ADR-86 rejected "a per-profile prompt (one
set of rules is the point)" (`DESIGN.md:1870`). Removing the table means amending ADR-86, which is
Sturla's call; the case for it is that clients now defer tool schemas and read descriptions on demand,
so the table is the copy that goes stale. The rest mixes
three kinds of rule:

| Kind | Example | Class |
|---|---|---|
| Goal or constraint | "One owner per area", "never write to an external tracker without the exact target" | a or d: keep |
| Fleet protocol the model cannot infer | What "[fleet] report from" means, what `confirmed=true` is for | a: keep, but next to the tool that emits it |
| Procedure against a habit | "Never draft the solution in this chat and then offer to spawn", the Bad/Good example, "Reply in 1-3 lines", "never call task_complete" | c: dated, measure, sunset |

The worker playbook (`internal/playbook/worker.md`, 40 lines) has the same mix: the safety core
(ADR-59) is guard; "Decide, don't wait", "Escalate early" and the eight-step pre-merge checklist are
procedure written for how agents behaved in November 2026. ADR-57 already found the cost of procedure:
an agent told to start a timer "makes a worktree, a branch and an ADR for it". That is the essay's
"it inhibits further progress" in one sentence.

Replacement, in two steps:

1. Cut the duplication, with Sturla's OK on amending ADR-86. Anything a tool's `description` lacks
   moves into the description, where it is also current for clients that defer tools; the table
   shrinks to tool names grouped by job, so the ADR-86 test still has something to check.
   Model-specific worker lines move to profile notes (finding 4).
2. Give every remaining procedural rule a provenance tag (the ADR or incident it came from) in
   source, and an ablation test: run the same small task set with and without the rule and compare
   outcomes from the ledger (finding 12). A rule that changes nothing for the current models goes.

"Delegate by default" is the exception: its real enforcement is ADR-135 (a Claude admiral gets no
built-in tools) and the Copilot admiral's `--available-tools fleet`, so the admiral cannot write code
itself whatever the prose says. That is a guard, and an ablation of the prose rule would measure
nothing.

### 6. The MCP tool surface (c)

The admiral server has 40 tools, a worker 9, a leader 17 (the 9 plus 8 `fleet_squad_*`). The schemas
are sent on every model call. Overlaps:

| Overlap | Tools | Note |
|---|---|---|
| Squad copies | `fleet_squad_spawn`, `_status`, `_info`, `_peek`, `_send`, `_reports`, `_close`, `_profiles` | A leader has no admiral tools, so the leader's schema would not shrink |
| Access, split over five tools | `fleet_access`, `fleet_access_decide` (admiral decides between agents), `fleet_access_request`, `_respond`, `_revoke` (admiral to admiral, agent to agent) | Two different operations; merging them needs a role-by-action table |
| Thin CLI wrappers | `fleet_touching`, `fleet_worktrees`, `fleet_branches`, `fleet_takeover`, `fleet_changes`, `fleet_admiral_add`, `fleet_admiral_close` (`internal/mcp/clitools.go`) | One generic tool saves schema tokens but loses the description that says when to call each |
| Handoff | `fleet_handoff`, `fleet_admiral_profile` | One tool with an optional profile |
| Repo process | `fleet_adr` | A repo convention (finding 10), not a fleet primitive |

Mail is already one vocabulary: admiral and worker share `fleet_msg_send`, `fleet_inbox` and the
three access-request tools by name. The token payoff of a collapse is close to zero for a leader and
small for the admiral. The real argument is one vocabulary for every role, so a better leader model
needs no second set of names. That makes this an experiment worth a measured try, not a plan item
with a promised saving.

### 7. Nudges and the stop hook (c, measured)

Fleet types lines into agents and the admiral on its own initiative. Most of them deliver a fact the
coordinator cannot see for itself, which is mechanism, not a crutch:

| Line | Trigger | Where | Kind |
|---|---|---|---|
| Stop-hook block "Call fleet_report ... before stopping." | Turn ends with work and no done, blocked or question report | `internal/board/stopblock.go:5-33`, ADR-105 | Behavioural nudge (c) |
| Recycle line "hand over to a fresh agent" | 2 compactions or 1,000 AIC | `internal/prefs/recycle.go:21`, `internal/fleet/recycle.go` | Advice (c): the fact plus a fixed recommendation (ADR-71) |
| "[fleet] X stopped mid-task without a report" | Idle 20 s, no report since prompt | `internal/board/board.go:270`, `internal/fleet/refresh.go:126-135` | Information (a) |
| Stuck line | Same error banner twice, or unchanged screen 10 min | `internal/status/stuck.go`, ADR-45 | Information (a) |
| Re-surface "has waited 2h on the user" | Open question older than `resurface_after` | `internal/fleet/resurface.go`, ADR-130 | Information (a) |
| "(N unread)" and report suffix on every result | Unread mail | `internal/mcp/agent.go`, `internal/mcp/reports.go:130-148` | Information (a) |
| Admiral context warning | 70% fill or 80k tokens | `internal/supervisor/fill.go:124-155`, ADR-93 | Cost guard (d), see finding 8 |

The stop-hook block is the one that changes an agent's behaviour rather than inform its owner, and it
is the cleanest to measure: it fires at most once per prompt and fails open, so counting how often it
fires per profile and model is cheap, and a model that almost never trips it does not need it.

Replacement: the stop-hook block and the recycle advice get a per-profile switch and a ledger
counter; defaults stay on, and a default flips per profile only on ledger data. The information lines
stay as they are and are counted, so the admiral's reaction to them can be studied.

### 8. Fixed thresholds instead of information (c)

Several numbers decide or prompt a decision the coordinator could make with the data:

- Admiral handoff at 70% or 80,000 tokens (`internal/supervisor/fill.go:124-155`), measured only for a
  Copilot admiral (`internal/supervisor/pane.go:72-74` reads only `CopilotHome()`). A Claude admiral gets
  no fill and no warning at all.
- Worker recycle at 2 compactions or 1,000 AIC (`internal/prefs/recycle.go:21`), from a 14-day median
  (ADR-71), Copilot-only because Claude transcripts carry no compaction marker.
- The playbook level chosen by whether there is a worktree (`internal/playbook/playbook.go:53`).
- `NoReportAfter = 20s` (`internal/board/board.go:270`), a guess at how long a turn boundary lasts.

The admiral threshold is the one that limits a better model most: on a 1M window, 70% arrives far
too late, which is why ADR-93 added the absolute 80k. The absolute number is a cache-cost guard; the
percentage is a guess about when a model's quality drops, and that guess changes with every model.

Replacement: measure fill for every admiral that has a source (ACP `usage_update` for an ACP
admiral, the session file for Copilot, the transcript for Claude) and put fill and cache-miss cost in
the context of results the admiral already reads (`fleet_info` and the warning line), so the model
decides when to hand off. Keep the 80k token line as a cost guard and make it a `config.toml` key
instead of an environment variable. Worker recycle becomes data in `fleet_info` (compactions, spend,
context fill), with the line kept as a default. The playbook default level is fine as a default; the
admiral already overrides it.

### 9. Fixed caps on what the admiral sees (d, mostly)

`fleet_brief` is capped at 12 KB (`internal/mcp/brief.go:16`), headlines at 100 to 120 runes, peeks at
20 lines (80 max), handoffs at 4 KB, recall at 10 per section. Most of these are guards against a
client limit (Copilot saves a tool result over about 20 KB to a file, `brief.go:12-15`) or against
control characters in a pane, not guesses about the model. They also already follow the right pattern:
a cut section ends with a pointer ("call fleet_reports") so the model can ask for more. That is
progressive disclosure, which scales with model capability.

Keep them, with one change: caps that exist for a client limit should come from the client's profile
(a `max_result_bytes` in tool data) instead of one constant, so a client with a bigger limit gets more.

### 10. This repo's conventions compiled into a general tool (b)

Two places carry fleet's own repo layout to every repo fleet is used on.

The built-in worker playbook, which every pro agent gets in any repo, names this repo's files and
gates: `DESIGN.md §14` (`internal/playbook/worker.md:18`), `go build ./... && go vet`
(`worker.md:24`), the CHANGELOG.md and DESIGN.md conflict rule (`worker.md:31`, `35`) and
`scripts/inline-html.py` (`worker.md:19`). A per-repo layer already exists for exactly this
(`.fleet/worker.md`, `internal/playbook/playbook.go:182`). This is the largest leak in the audit and
the cheapest to fix.

`fleet merge` and `fleet adr` carry the rest:

- `SharedDocs` names `CHANGELOG.md` and `DESIGN.md` and their conflict order
  (`internal/worktree/merge.go:17-27`).
- `defaultMergeCheck = "make build && go vet ./..."` (`cmd/fleet/merge.go:23-25`) is this repo's gate.
- The ADR entry regex `^\*\*ADR-N` for `DESIGN.md` (`internal/adr/design.go:12-14`).

Replacement: the playbook lines move to this repo's `.fleet/worker.md`, with the built-in text
saying the general rule ("your repo's checks", "the repo's append-only docs"). A `.fleet/repo.toml` (Planned)
read by `fleet merge` and `fleet adr` holds the append-only files and their order, the merge check, and
the ADR file and entry pattern. With no file, today's values stay the default for one release with a
note, because users without the file would otherwise lose ADR-48's keep-both handling; dropping the
fallback is Sturla's call.

### 11. The summariser prompt is a constant (b)

`fleet_peek summary=true` asks a small model to describe the screen (ADR-6, ADR-40). Its prompt
(`internal/summary/prompt.go:16-40`) is a Go constant with a fixed output shape. It labels the board
status `Scraped status`, but the value is `a.Status` (`internal/fleet/summary.go:56`), the status after
hook and ACP precedence, so the label is wrong, not the input.

Replacement: fix the label and move the prompt text into the `[summary]` defaults with a `prompt` key.
Longer term, ADR-6's "summaries, never raw panes" is a context-budget rule; as admiral windows grow
and the admiral reads `last_message` directly, check in the ledger whether summary calls are still
used before investing more.

### 12. No measurement of the harness itself (missing mechanism)

This is the most important finding. Fleet records reports, spend and status changes, but not the
events that would show whether its own policy still helps: how often the stop hook blocks, how often
a no-report or stuck wake fires and what the admiral did next, how often a send is refused, how often
screen status and hook or ACP status disagree, how often a done-check fails, how often CI is red on
first push, per profile and model. Every ADR in this audit that added procedure cites an incident;
none can be retired, because nothing measures whether the incident still happens.

This is the "learning" half of the essay. Without it, every rule added is permanent, and the
harness's knowledge only grows.

Replacement: an append-only intervention ledger, one line per guard, nudge, refusal and status
disagreement, with the agent's profile and model, and a `fleet stats` view (Planned) that groups it. The
existing `~/.fleet/events.jsonl` (`internal/board/board.go:618-627`, written by
`board.Store.AppendEvent`) is the natural home; it has no size bound today, and several processes
(the watcher, hooks, the MCP servers) append to it, so rotation needs one owner. A status
disagreement records which pattern set and kind matched, so a later change can tell which patterns
still decide anything. No model call; it is counting. With it, findings 1, 5, 7 and 8 each
get a removal test.

## What fleet already gets right

These are worth naming so later work does not undo them:

- Routes are data the admiral matches "by meaning, not keywords" (ADR-120). The inventory found no
  keyword or scoring classifier of task text anywhere in the code.
- Models are asked for, not listed: `checkModel` refuses a model the tool does not offer, from the
  tool's own list (ADR-112, `internal/fleet/models.go`).
- Spend comes from the tool's own files, never from a model (ADR-46).
- Verification is by execution: done-check runs the card's command, CI tests the pushed branch
  (ADR-43, ADR-69, ADR-149). This is the harness's search, and it gets better with better models.
- Stuck "only says so once" and never kills (ADR-45). The harness informs; the coordinator decides.
- The admiral handoff is the model's own call; fleet only warns (`internal/mcp/tools.go`
  `fleet_handoff`, no forced reset).
- Tool profiles and patterns are data with user override (`~/.fleet/tools.toml`, `patterns.toml`).
- Integrations are adapters over other CLIs, not a plugin system (ADR-126).

## Guards to keep, labelled

| Guard | Where | Protects against |
|---|---|---|
| Write fence | `internal/tools/tools.go` `ClaudeFence`, ADR-79 | Writes outside the worktree |
| Confirmation (`confirmed=true`) | `internal/mcp/send.go`, ADR-9 | Irreversible acts without the user |
| Send blocker while the user types | `internal/fleet/interactive.go`, ADR-61 | Pasting into a user's half-typed input |
| `UserHold = 10m` | `internal/board/board.go:257-259`, ADR-61 | Fleet acting on a pane the user is using |
| No built-in tools for the admiral | ADR-135, Copilot `--available-tools fleet` | The admiral doing work it should delegate |
| Working cap `max_working` 4 | `internal/prefs/prefs.go:117` | A shared laptop running out of CPU and RAM |
| Memory and runaway guards | `internal/fleet/guard.go`, `runaway.go` | Swap, a spinning CLI |
| Squad caps | `internal/prefs/squad.go` | Fan-out cost |
| Report rune caps and control-rune strip | `internal/mail/report.go:54-60`, `70-111` | Keys typed into the admiral pane |
| Admiral 80k token line | `internal/supervisor/fill.go:137-146` | Cache-rewrite cost (ADR-93) |
| Wake batching | `internal/fleet/wake.go`, ADR-93 | One cold turn per report |
| Release lock, public check | `scripts/`, ADR-73 | Leaks and double releases |

A guard is not exempt from evidence (the working cap of 4 is a guess about one laptop), but its
removal test is about cost and risk, not model capability.

## The plan

Order is by payoff per effort and risk. Phase 1 adds counting and moves text and data; phase 2 changes
behaviour once a week of counts exists; phase 3 deletes on that evidence. Work packages are in
[bitter-lesson-wps.md](bitter-lesson-wps.md).

### Phase 1: measure, and move knowledge to data

1. **Intervention ledger** (finding 12, WP1). Every later deletion depends on it, so it goes first.
2. **Prose diet** (findings 4, 5, WP2). Shrink the admiral tool table to names (amends ADR-86), move
   guidance into tool descriptions, move tool words into per-profile notes for workers.
3. **Repo conventions out of the core** (finding 10, WP3). Playbook lines to this repo's
   `.fleet/worker.md`; `fleet merge` and `fleet adr` read a `.fleet/repo.toml` (Planned).
4. **Summariser prompt as data** (finding 11, WP4).
5. **Model defaults that follow the tool** (finding 3, WP5). Newest of a family from the tool's own
   list, behind a profile key; unknown rate-limit windows shown.

Then a week of normal use with the ledger recording.

### Phase 2: protocols over inference

6. **Events decide status** (finding 1, WP6), only if the week shows events and screen agree. Done: ADR-205.
7. **Fill for every admiral** (finding 8, WP7).
8. **Per-profile nudge switches** (finding 7, WP8) for the stop-hook block and the recycle advice.

### Phase 3: delete on evidence

9. **Patterns all in data, then retired** (finding 1, WP9).
10. **Adapter freeze and first removal** (finding 2, WP10).
11. **Rule measurement** (findings 5, 7, WP11): ledger rates first, a small ablation only where the
    ledger can't see a rule.
12. **One tool vocabulary** (finding 6, WP12), as an experiment with a recorded result.

Not in a package: per-client result caps (finding 9) and the Copilot exclusion list (finding 3) wait
for a client change; both are noted where they are.

### What success looks like

- A new model release needs no fleet release.
- A new tool comes in over ACP or hooks with no new Go parser.
- The admiral instructions and worker playbook are shorter after each model generation, not longer,
  because each procedural rule has a removal test.
- `fleet stats` (Planned) answers "does the stop hook still fire for this model?" in one command.

## Risks

- **Removing a rule that was quietly working.** Nothing in phase 3 is deleted without a ledger count,
  and defaults change per profile, not globally.
- **Events that lie.** A hook can go stale; ADR-49 rejected hook authority for exactly that.
  `HookWorkingMax` (`watchpaths.go:104-106`) stays, a menu on screen still forces waiting, and WP6
  starts only after the disagreement counts are in.
- **Turn timing.** The no-report wake and turn detection hang off status transitions (ADR-68).
  Inverting precedence can move them; WP6 tests that it doesn't.
- **Ablation costs tokens and is noisy.** WP11 uses ledger rates first; real runs need Sturla's OK,
  a budget and repeats.
- **Tool-surface churn breaks running sessions.** WP12 is last, an experiment, with aliases for one
  release if it lands.
- **Prose cuts change behaviour the ledger cannot see.** The safety core (ADR-59) is untouched;
  phase 1 cuts only duplication and tool-specific words.
- **ADR conflicts.** WP2 amends ADR-86, WP3 ADR-48 and ADR-50, WP6 ADR-7 and ADR-49, WP7 ADR-93.
  Each says so in its own ADR and asks Sturla where the brief says so.

## Changes from the starting plan

The work package that started this audit proposed: sort items into four classes, name replacements,
order removals that need no new mechanism first, write at most 12 packages. This plan follows that,
with these changes:

- **Measurement comes first, not removals.** Almost every procedural item is backed by an incident
  ADR; deleting it blind would repeat the incident. The ledger is cheap counting and makes every
  later removal a decision on data.
- **Repo conventions were added** (finding 10), and the worker playbook turned out to be the biggest
  leak: it sends this repo's file names and checks to every repo.
- **Some claims shrank on review.** Model constants are already overridable data, so finding 3 is
  about following the tool's newest model, not moving constants. The summariser is not anchored on a
  scraped guess; only its label is wrong. The tool-surface collapse saves almost no tokens and became
  an experiment.
- **Existing decisions are named.** ADR-86 (the tool table), ADR-49 (g) (hooks not authoritative) and
  ADR-135 (the admiral has no built-in tools) shape what each package may change.
- **Nudges were split.** Most fleet-typed lines are information and stay; only the stop-hook block and
  the recycle advice are behavioural and get switches.
- **Context caps were mostly reclassified as guards** (finding 9). They track a client limit and
  already point to more, which is the scalable pattern.
