# What's new in fleet

User-facing changes, newest first. `fleet changes` and `Alt-g` show this file; it is built into the binary.

To add an entry: one line under today's `## YYYY-MM-DD` heading (add the heading if it is missing), at the top of that day, in the same commit as the change. Say what the user can do now, not how it was built. End with the ADR or commit in brackets when there is one: `(ADR-27)`, `(66a2ece, ADR-27)`.

## 2026-11-05
- Spawning an agent into a git repo gives it its own worktree, `<repo>.wt/<name>` on branch `fleet/<name>`, so parallel agents never share one checkout; `fleet close` and the idle sweep name that worktree and branch when work is left in it, and `--no-worktree`, `worktree = false` in config.toml or `worktree: false` on `fleet_spawn` opt out (ADR-39)
- Drag-select, double-click and triple-click now copy from any agent pane, including the admiral, straight to the system clipboard, so `Ctrl+Shift+V` pastes what you just selected (ADR-38)
- The wall and overview tell focus, selection and the ▶ target apart at a glance: only the ▶ target is lavender, the overview cursor is an underline in the agent's status colour, the pane you are typing in has a bright border while the others dim one step, and `· next out` recedes (ADR-37)
- Spawned agents and the admiral are told that time matters: avoidable time is not spent, and an earlier correct result is better
- Spawned agents squash fixup commits into their logical commits on their branch before merging, so published history stays one reviewable commit per change and is never rewritten
- Past 14 agents the overview keeps every agent clickable: agents that need you keep full cards, the rest become one-line rows, idle ones fold into `+N idle`, the wheel scrolls, and cards no longer move for 3 seconds after you click or press a key (ADR-32)
- Right-click a card for a menu (put in ▶, add, preview, pin, take off); `Space` previews an agent live in a popup, `u` undoes a replace, `p` pins a pane so it is never replaced (ADR-31)
- Click a wall pane to mark it `▶`, then click a card: that agent takes the `▶` pane, whether or not the wall is full. `a` adds a pane instead, `Tab` or `Alt-n` sends the next waiting agent there, `Esc` clears it, and clicking no longer resizes or moves other panes (ADR-31)
- Agent reports no longer land in text you are typing to the admiral: fleet types the wake line only into an empty, quiet input box, presses Enter only when the box holds exactly that line, sends one line for several reports, and shows unread reports on the status line (✉ N reports) (ADR-36)
- Agents end their reports with `next:`, `risks:`, `decisions:` and `retro:` lines, escalate blockers early, keep to their scope and leave merge order to a named coordinator; the admiral keeps risks, assumptions, issues and decisions in its notes. Why: docs/process.md (ADR-35)
- Agents in the same team, or the same repo when they have no team, message each other without asking; `agent_mail` in config.toml picks `team`, `open` or `ask`, and `fleet access log` shows every message (ADR-34)
- Agents you end with `/exit` close by themselves; crashed ones are reported to the admiral with their exit code and close after `dead_pane_grace` (default 5m), with a note when they left work behind (ADR-33)
- What's new view: `Alt-g`, `g` in the overview and `fleet changes` show fleet's own new features, whichever pane they are opened from, and mark the ones you have not seen yet (ADR-30)
- Agents that report done close themselves unless they left their own work behind, also read-only ones in a dirty checkout; reports to a busy admiral wait until it is idle; `auto_close_idle` also closes done agents once their work is merged (19456fb, ADR-29)
- Text you copy in tmux copy-mode lands on the system clipboard (94c7ac4, ADR-28)
- Pick the admiral's CLI with `Alt-P` or `fleet admiral profile`; a swap hands off to the new one, a restart under the same profile resumes the chat in place (66a2ece, ADR-27)
- The supervisor is now the admiral; `fleet supervisor ...` and the old env names keep working (f5b2bd8, ADR-26)
- Brand style guide and logo marks in the README (0066bec)
- The admiral's MCP server hot-reloads in place after `make build`, so new tools arrive without losing the chat (faa78ab, ADR-25)
- At most 4 agents work at once (`max_working`); further spawns wait in a queue and start in order (1e24fb4, ADR-23)
- Every spawned agent gets the worker playbook with its prompt; `fleet touching <files>` says whether another worktree is changing them (4b8c253, ADR-22)
- CPU cap: `env` in `config.toml` sets `GOMAXPROCS`/`GOFLAGS` for agent panes, so parallel builds don't take every core (e14eef0, ADR-23)
- The overview gives every agent a card, also beyond 5 agents, wrapped and sized to the pane (ecf9f56)
- New docs: prior-art survey (`docs/prior-art.md`) and measured RAM/CPU per agent (`docs/perf.md`) (e7f3013, a205a9e)
- Subagents show as status-coloured dots on their agent's tile (932b209, ADR-18)
- Agents report back with `fleet_report`; a report wakes an idle admiral, and `auto_close_done` closes agents whose work is merged (8280ba9, ADR-20)
- Agent mail: agents message each other once access is granted (cc09639, ADR-19)
- Teams: named groups of agents with a coloured mark on their tiles (3f61a30, ADR-17)
- Status detection works with the newer Copilot CLI UI (bc8f165)

## 2026-11-04
- Smarter wall tiling: a screen-sized grid, idle panes make room first, one emphasis pane for the agent that needs you (98696af, ADR-15)
- Spawned agents start ready to work: autopilot and the first prompt at launch, per your local mode preferences (1c8560b, ADR-13)
- `fleet sessions <words>` finds Copilot and Claude sessions to adopt (9d57b1e)
- Admiral memory: notes, handoff, a context meter and reset (5b1c6a3, ADR-11)
- `fleet adopt` resumes an outside Copilot or Claude session in a fleet pane (4056a2d)
