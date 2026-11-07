# The admiral

The admiral is an ordinary agent in the left pane of `fleet up`. It steers the fleet through MCP tools served by `fleet mcp`. It is not special inside fleet: it runs from a profile like any other agent, and you can throw it away and start it again without losing anything, because fleet's state is in tmux and `~/.fleet/`.

It was called the supervisor until ADR-26. The old names still work (`fleet supervisor`, `FLEET_SUPERVISOR_PROFILE`), and its working directory is still `supervisor/`, because a tool's folder trust is keyed by path.

## How it starts

- Profile: `copilot`, unless you pass `fleet up --admiral-profile NAME`, set `FLEET_ADMIRAL_PROFILE`, pick one with `Alt-P`, or put an `[admiral]` entry in your config (see [examples/config.toml.example](../examples/config.toml.example)). Any profile works, including `fake`.
- Directory: `supervisor/` next to the directory holding the `fleet` binary (so `make build` finds it), or `FLEET_ADMIRAL_DIR`, or your home directory when neither exists (`go install`).
- No config file is needed. `fleet up` passes the MCP server on the command line: `--mcp-config` with an inline JSON for claude profiles, `--available-tools fleet --allow-tool fleet` for copilot ones.
- Its rules (tools only, short replies, the confirmation rule) live in `internal/mcp/instructions.md`, built into the binary and sent as the MCP server's instructions.
- With a `copilot` profile, `fleet up` starts it lean: `--no-custom-instructions --allow-all-mcp-server-instructions --disable-builtin-mcps`, plus `--disable-mcp-server` for each server in `~/.copilot/mcp-config.json`. It sees only the fleet tools: no shell, no file reading, and about 8k input tokens per turn instead of ~160k.

`fleet admiral` prints its profile, session id and context fill; `fleet admiral reset` replaces it with a fresh chat, `fleet admiral profile NAME` swaps the tool.

To use a tool fleet doesn't start itself, register `fleet mcp --admiral main` (or another admiral's name) as a stdio MCP server in that tool's own config. Without `--admiral`, a server outside an admiral pane can't tell which admiral it serves: reads work, changes are refused.

## MCP tools

`fleet mcp` speaks MCP (protocol `2025-06-18`) over stdin/stdout.

| Tool | Arguments | What it does |
|---|---|---|
| `fleet_status` | none | Table of agents: name, team, profile, status, subagents, on wall, age, spend so far, what it waits for; ends with the fleet-wide spend |
| `fleet_info` | `name` | One agent in detail: dir, git state, session id, context %, spend so far, mode |
| `fleet_peek` | `name`, `lines` (max 80), `raw`, `last_message` | Last lines of the agent's screen, or its last reply from the session log |
| `fleet_send` | `name` / `names` / `team`, `text`, `keys`, `confirmed`, `force` | Paste a prompt and press Enter, or press keys; refused while you are typing in that pane unless `force` (ADR-61) |
| `fleet_answer` | `name`, `answer` (`y`/`n`) or `option` N, `expect`, `confirmed` | Answer a y/n prompt or a numbered menu |
| `fleet_spawn` | `name`, `profile`, `dir`, `prompt`, `worktree`, `effort`, `leader`, `interactive` | Start an agent; in a git repo it gets its own worktree `<repo>.wt/<name>` on branch `fleet/<name>` (`worktree: false` spawns straight into `dir`). `effort` sizes the worker playbook: `light` (errand), `standard` (contained change), `pro` (full process); default `pro` with a worktree, `light` without. `leader: true` lets it run its own squad. `interactive: true` for an agent you will talk to directly: never auto-closed, reports progress until you end the session (ADR-61) |
| `fleet_adopt` | `session_id` or `query`, optional `profile`, `dir`, `name` | Resume a session started outside fleet |
| `fleet_sessions` | `query`, `limit` | Copilot/Claude sessions started outside fleet, newest first |
| `fleet_profiles` | none | Profiles and their command lines |
| `fleet_close` | `name`, `confirmed`, `squad` | Stop the agent and remove it from the board; for a leader, `squad: true` closes its agents too |
| `fleet_wall` | `action`, `name` or `preset` | Change the wall |
| `fleet_team` | `action`, `team`, `agents`, `color`, `text`, `confirmed` | Teams: list, create, delete, add, remove, color, send, show, hide, close |
| `fleet_reports` | `all`, `limit` | What agents reported (done, blocked, question, progress); unread first, marked read |
| `fleet_access` | `limit` | Who may message whom, and recent agent-to-agent messages |
| `fleet_access_decide` | `a`, `b`, `action`, `confirmed` | Grant, deny or revoke that |
| `fleet_note` | `text`, `agent`, `pin` | Append a line to `~/.fleet/decisions.log`; `pin` also keeps it in every `fleet_brief` (`~/.fleet/pinned.log`) |
| `fleet_brief` | none | Last handoff, recent notes, then the status table. A fresh admiral calls this first |
| `fleet_handoff` | `summary` | Write a handoff and get replaced by a fresh admiral |
| `fleet_admiral_profile` | `profile`, `summary` | Switch the admiral to another tool profile |

## Squads

A leader is an ordinary agent with one extra right: it runs a small squad of its own (DESIGN §6.11, ADR-53). Spawn one with `fleet_spawn(..., leader: true)` or `fleet spawn --leader`.

| Tool | Arguments | What it does |
|---|---|---|
| `fleet_squad_spawn` | `name`, `profile`, `dir`, `prompt`, `worktree`, `effort`, `card` | Start an agent of the leader's own squad, with the same worktrees, task cards and playbook as an admiral spawn |
| `fleet_squad_profiles` | none | Profiles the leader may spawn from |
| `fleet_squad_status` | none | The leader's agents: status, age, what each waits for, last report |
| `fleet_squad_info` | `name` | One of its agents in detail |
| `fleet_squad_peek` | `name`, `lines`, `raw` | Last lines of one of its agents' screens |
| `fleet_squad_send` | `name` (or `*`), `text` | Prompt one of its agents. Refused when that agent sits on a permission prompt |
| `fleet_squad_reports` | `all`, `limit` | Its agents' reports, without marking them read (`fleet_inbox` does that) |
| `fleet_squad_close` | `name` (or `*`), `confirmed` | Stop one of its agents; the first call is refused and names any uncommitted or unmerged work |

Every tool that names an agent refuses a name outside the squad. A squad agent's `fleet_report` lands in the leader's inbox and wakes the leader's pane the way reports wake the admiral; the admiral sees the leader's summary, not the squad's. `fleet_status` has a `squad` column, and the overview marks a leader `▲`. Caps: `[squad] max_members` (4) and `max_depth` (1, so a squad agent cannot itself lead); `max_working`, spend and the memory guard count squad agents like any other agent.

A leader may not answer a permission prompt, merge, push or apply infrastructure: those escalate squad → leader → admiral → you.

## Confirmation rule

`fleet_answer`, `fleet_close`, `fleet_squad_close`, `fleet_access_decide`, `fleet_team` `close`, and any `fleet_send` to an agent already sitting on a prompt refuse to run unless `confirmed=true`. The instructions tell the admiral to ask you in one line first, for example "Answer y to web-nav (run `make test`)?", and to set the flag only after an explicit yes. `fleet_answer` also takes `expect`, the question you confirmed: if the prompt on screen has changed, the call is refused.

Beyond `expect`, this is enforced in code only to the extent that the flag has to be set. The model sets it, so the real safeguard is the instruction plus your reply. Ordinary prompts through `fleet_send` need no confirmation. The instructions also tell the admiral to ask before telling any agent to merge, push to a shared branch or apply infrastructure.

Planned ([DESIGN.md §8, ADR-9](../DESIGN.md#14-decisions-adrs)): risky actions shown as approval cards in the TUI, and a blue flash on tiles the admiral acts on.

## Keeping its context small

`fleet_peek` returns at most 80 lines, and the instructions ask the admiral to peek only when needed. Planned: `peek` returns a ~5-line summary from a cheap model instead of raw text.

Fleet doesn't compact the admiral's chat; it replaces it ([DESIGN.md §6.5, ADR-11](../DESIGN.md#65-supervisor-memory-notes-handoff-context-meter-r11)):

- The admiral records decisions, your preferences and promises with `fleet_note`, and pins your standing rules so they stay in every brief.
- Its context fill is read from the tool's own session log and shown on the pane border (`admiral · ctx 34%`, green/yellow/red) and by `fleet admiral`.
- At 70% (`FLEET_ADMIRAL_HANDOFF_PCT`) every fleet tool result ends with a warning. The admiral then calls `fleet_handoff` with open questions, promises and plans in flight. Fleet respawns the pane with a new session whose first prompt is to call `fleet_brief`.
- `Alt-R` (tmux), `R` (overview) or `fleet admiral reset` does the same by hand, after a y/n prompt. The old session isn't deleted: `copilot --resume <id>` still opens it.

Agents are not affected: they live on the board and in tmux, not in the admiral's chat.

## Try it without tokens

`fleet up --admiral-profile fake` puts a fake agent in the admiral pane. To exercise the MCP server directly:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fleet_status","arguments":{}}}' \
  | fleet mcp
```
