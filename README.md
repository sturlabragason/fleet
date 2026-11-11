<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/lockup-on-ink.svg">
  <img src="docs/img/lockup.svg" height="64" alt="fleet">
</picture>

fleet is a terminal UI for when you run several coding agents at once: every Copilot CLI, Claude Code, agy or vibe session in one tmux window, a status tile for each, and an admiral agent that can answer and act on them for you.

[![release](https://img.shields.io/github/v/release/sturlabragason/fleet)](https://github.com/sturlabragason/fleet/releases)
[![ci](https://github.com/sturlabragason/fleet/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/sturlabragason/fleet/actions/workflows/ci.yml)
[![license](https://img.shields.io/github/license/sturlabragason/fleet)](LICENSE)

![fleet: the admiral on the left, a tile per agent top right, a 2×2 wall of live agent panes below. Two agents ask for approval, they are answered, and work resumes](docs/img/demo.gif)

<sub>Recorded with fake agents and a fake admiral, so no tokens were spent (`scripts/screenshot.sh`). Still frame: [docs/img/hero.png](docs/img/hero.png).</sub>

```sh
curl -fsSL https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh | sh
```

Linux (tested) and macOS (built, not tested). Other ways to install, and what you need: [Install](#install). To try it without spending tokens: [Quickstart](#quickstart-fake-agents-no-tokens).

- **A tile per agent**, coloured by status (working, waiting on you, error, stuck, idle) and sorted so the agents waiting on you come first.
- **A live wall** of the real panes you are working with. Click into one and type to answer it.
- **An admiral**: one agent with MCP tools to report on the fleet, answer prompts and message agents. Risky actions need your confirmation.
- **A git worktree per agent**, so parallel agents never share an index.
- **Spend per agent** in credits or tokens, read from the tools' own records, and a CLI for scripting.

## Why

Running 20–40 agent sessions at once, the terminals aren't the problem. Attention is:

- You can't tell which agents are working, stuck, waiting on you or done without visiting each terminal.
- Every answer and approval has to come from you, typed into the right window.
- A single overseer agent that reads every raw pane fills its context and gets worse at its job.

Session switchers make moving between agents faster, but you still do all the supervising. fleet puts every agent in one tmux server, shows its status as a coloured tile, puts the agents you care about on a live wall of real panes, and gives one agent, the admiral, a small set of MCP tools to report on and act on the fleet. Risky actions need your confirmation. The full reasoning is in [DESIGN.md](DESIGN.md).

## What it does today

- **Spawn** agents from tool profiles into a dedicated tmux server (`tmux -L fleet`), one hidden window per agent.
- **Status** for every agent (working, waiting, error, stuck, idle). Copilot and agy agents (every profile that runs them, claude-copilot included) run over the Agent Client Protocol (ACP), so their status, questions and answers come from the tool itself and their pane shows fleet's transcript of the session. Claude and vibe run in their own TUI, read from the bottom of the pane with per-tool regex patterns. Refreshed every second.
- **Overview TUI**: coloured tiles sorted by attention (waiting agents first, longest wait first), arrow keys, mouse click, `Tab` through waiting agents.
- **Live wall**: real, interactive tmux panes for the agents you are working with. Layouts 1, 2, 4, 6, 9 and main+stack, as caps rather than fixed shapes: the grid is sized to the window so panes stay readable, and a readability floor shows fewer panes rather than unreadable ones. The pane that needs attention gets the big slot. Click a pane to make it the `▶` target and the next card you pick from the overview goes there; from the overview, `p` pins a pane so it is never evicted and `u` undoes the last replace. Waiting and failing panes stay. Processes never restart.
- **Copy out of any pane**: drag-select, double-click or triple-click copies to the system clipboard, even over an agent that asked for the mouse.
- **Adopt**: `fleet sessions` lists Copilot and Claude sessions started outside fleet, and `fleet adopt` resumes one as an agent (agy too, by session id).
- **A queue**: with `max_working` set, a spawn past the limit waits and starts when a slot frees.
- **A worktree per agent**: spawning into a git repo gives the agent `<repo>.wt/<name>` on branch `fleet/<name>`, so parallel agents never share an index. `--no-worktree` opts out.
- **Admiral**: an agent (Copilot CLI by default) in the left pane with nineteen `fleet_*` MCP tools from `fleet mcp`. It keeps a context meter, can hand off to a fresh session (`Alt+R`) and can swap its own tool profile (`Alt+P`). Unread agent reports show as a `✉` badge in the overview header and the tmux status line.
- **Cheap summaries**: `fleet peek --summary <name>` returns about five lines (what the agent is doing, what it waits for, any error) from a small model instead of a screenful of text.
- **Teams**: group agents (`fleet team create frontend teal`), then send to, show or close a whole team at once. Members are marked with a coloured `◆`.
- **Agent mail**: agents message each other with `fleet mail` or peer MCP tools. Free inside a team or a repo, otherwise one agent asks and you decide.
- **Squads**: `fleet spawn --leader <name> <profile>` makes an agent a leader, with its own tools to spawn, watch, message and close up to four agents of its own. Their reports wake the leader, which sends the admiral one summary; leaders are marked `▲` and never answer prompts, merge or push for you.
- **CLI** for scripting: `spawn`, `adopt`, `sessions`, `ls`, `send`, `peek`, `close`, `team`, `mail`, `access`, `report`, `spend`, `admiral`, `note`, `playbook`, `touching`, `merge`, `profiles`, `keys`, `changes`, `snap`, `wall`, `doctor`.
- **Spend**: `fleet spend` shows what each agent has cost so far and the fleet total — AI credits for Copilot agents, tokens for Claude and agy — read from the agents' own session files or the usage agy reports over ACP, never from a model.
- **Fake agents** that cycle through states and print scripted work (reads, edits, test runs), so you can try everything without spending tokens. `fleet send <name> "!wait"` (or `!work`, `!error`, `!done`) holds one in a chosen state, which is how the demo above is composed.

Planned, not built yet: a spawn wizard and approve/reject keys in the TUI, desktop notifications and workflow files (`fleet run`). See [Roadmap](#roadmap).

## Requirements

- Linux (the only platform tested).
- tmux 3.3 or newer. Developed and tested with tmux 3.8.
- Go 1.26.10 or newer to build (see `go.mod`).
- The agent CLIs you want to run (`copilot`, `claude`, `agy`, `vibe`) on your login `PATH`. Not needed for fake agents.

`fleet doctor` checks these: the tmux version, a UTF-8 locale, `~/.local/bin` on `PATH`, your config files and each profile's command, one line each with the fix.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh | sh
```

It downloads the latest release's binary for your OS and architecture (linux or macOS, amd64 or arm64), checks it against the release's `SHA256SUMS` and refuses on a mismatch, and puts it in `~/.local/bin`. No sudo, and it doesn't touch your shell's rc files; if the directory isn't on your `PATH` it says what to add. Run it again to upgrade. `FLEET_VERSION=v1.3.0` pins a release, `FLEET_INSTALL_DIR` picks another directory, `--dry-run` shows what it would do.

Releases carry GitHub [artifact attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) that tie each binary and `SHA256SUMS` to the workflow run that built them. If [`gh`](https://cli.github.com) is installed and logged in, the installer checks the binary's attestation too and refuses on a failure; otherwise it says that provenance was not checked. Releases made before attestations were added have none: `FLEET_SKIP_ATTEST=1` installs them on the SHA-256 check alone. To check a download yourself:

```sh
gh attestation verify fleet-linux-amd64 -R sturlabragason/fleet
```

To read it before you run it:

```sh
curl -fsSLO https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh
less install.sh
sh install.sh
```

or with Go:

```sh
go install github.com/sturlabragason/fleet/cmd/fleet@latest
```

or download a binary from [Releases](https://github.com/sturlabragason/fleet/releases), check it against `SHA256SUMS` and put it on your `PATH`. `fleet version` says which release you have.

or from a checkout:

```sh
make build      # bin/fleet and bin/fakeagent
make install    # symlink bin/fleet to ~/.local/bin/fleet
```

The admiral needs no setup: `fleet up` passes the MCP server (`fleet mcp`) to it on the command line, and its rules travel as the server's instructions. Its working directory is `~/.fleet/admiral`, outside any git checkout so repo churn doesn't reset its prompt cache (ADR-93); `FLEET_ADMIRAL_DIR` overrides it.

A binary install is all of fleet. What it lacks is the checkout's extras: `supervisor/`, and the skills in `.claude/skills` and agents in `.claude/agents`, which are for working on fleet itself. Example config is in [examples/](examples): [`tools.toml`](examples/tools.toml) and [`config.toml.example`](examples/config.toml.example).

## Quickstart (fake agents, no tokens)

Fake agents run from the fleet binary itself, so an install is enough. From a checkout, run `make build && export PATH=$PWD/bin:$PATH` first.

```sh
fleet up --admiral-profile fake --no-attach   # fake admiral too, no tokens
for n in api-auth web-nav infra-dns docs; do fleet spawn --fake $n; done
fleet ls
tmux -L fleet attach -t fleet:main
```

In the overview, select a tile and press `Enter` to put it on the wall. Click into a wall pane and type `y` to answer its prompt. `Alt+4` picks a 2×2 wall.

Drop `--admiral-profile fake` to get a real Copilot CLI admiral. To stop everything: `tmux -L fleet kill-server`.

```text
$ fleet ls
NAME         PROFILE  DRIVER  STATUS   FOR  PANE  LAST LINE
api-auth     fake     tmux    working  3s   %2    ⠇ Thinking… step 8 of refactor auth middleware (esc to i
web-nav      fake     tmux    waiting  11s  %3    Approve? [y/n]
```

## Screen layout

`fleet up` builds one tmux window, `fleet:main`:

```mermaid
flowchart LR
  sup["admiral<br/>copilot + fleet MCP tools"]
  subgraph right[" "]
    direction TB
    ov["overview: tiles, then chips"]
    subgraph wall["live wall (2×2 shown)"]
      direction LR
      w1["agent pane"] --- w2["agent pane"]
      w3["agent pane"] --- w4["agent pane"]
    end
    ov --- wall
  end
  sup --- right
```

- **Admiral** (left, 90 columns, 64 below 220 columns wide). `Alt+s` hides it, `Alt+S` moves it to the other side, `Alt+R` resets it, `Alt+P` swaps its profile.
- **Overview** (top right, 14 rows). Tiles for as many agents as fit, one-line chips for the rest. `Alt+c` switches to chips only.
- **Wall** (below). Live agent panes. Each pane border shows the agent's name and status in its status colour. `fleet up` starts `fleet watch`, which keeps those borders, `fleet ls`, the board and the admiral's view agreeing within about a second whether or not the overview is open.
- Agents not on the wall live in hidden windows `ag-<name>`.

### Two monitors

```sh
fleet up --monitors 2    # this terminal: the admiral, full screen
                         # a new terminal window: overview + wall, full screen
```

Drag the new window to the second screen. If no window opens (fleet tries `$FLEET_TERMINAL`, then `$TERMINAL`, then Ptyxis, GNOME Terminal, kitty, foot, Alacritty, WezTerm, Ghostty, Konsole, xterm), run `fleet wall attach` in a second terminal yourself. Every wall key (`Alt+1/2/4/6/9/m`, `Alt+X`, `Alt+w`) and the overview work from either screen, and each screen keeps its own size. Closing either window only detaches it: agents keep running; `fleet up` brings back the admiral's screen and `fleet wall attach` the wall's. `fleet up --monitors 1` puts the admiral back in `fleet:main` and closes the wall's window (ADR-108).

All geometry is in [`internal/layout`](internal/layout/config.go).

## Keys

**Press `Alt+?` anywhere in the fleet window for a popup with every key**, grouped by area (panes, wall, admiral, overview). Any key closes it. In the overview, `?` shows the same list; `q`, `Esc` or `?` closes it. `fleet keys` prints it in a terminal. The status line at the bottom shows the most used keys in plain words.

Overview (when the overview pane has focus):

| Key | Action |
|---|---|
| `←` `↑` `↓` `→` / `h` `j` `k` `l` | Move between tiles |
| `Tab` | Next waiting agent (longest wait first) |
| `Enter`, left click | Put the selected agent on the wall |
| `x` | Take the selected agent off the wall |
| `1` `2` `4` `6` `9` `m` | Wall layout |
| `g` | What's new in fleet |
| `R` / `P` | Reset the admiral / pick its profile |
| `?` | Show all keys |
| `q`, `Ctrl+C` | Quit the overview (`fleet up` restarts it) |

Anywhere in the fleet window (tmux bindings, no prefix; only on the `fleet` server):

| Key | Action |
|---|---|
| `Alt+←↑↓→` | Move between panes |
| `Alt+z` | Zoom the focused pane |
| `Alt+x` | Take the focused wall pane off the wall |
| `Alt+1` `2` `4` `6` `9` `m` | Wall layout |
| `Alt+s` / `Alt+S` | Hide/show the admiral / move it to the other side |
| `Alt+R` / `Alt+P` | Reset the admiral to a fresh chat / pick its profile |
| `Alt+g` | What's new in fleet |
| `Alt+c` | Overview: tiles or chips only |
| `Alt+o` | Jump to the marked pane |
| `Alt+?` | Show all keys in a popup |

Mouse is on: click a pane to type in it, drag borders to resize, scroll for history. Details and planned keys: [docs/keys.md](docs/keys.md).

## Upgrading to v1.0.0 (breaking)

From v1.0.0, new `copilot` and `agy` agents run over ACP: the agent's pane shows fleet's transcript of the session, not the tool's own TUI. `fleet snap` and the screen patterns no longer apply to them. Agents that exist when you upgrade keep running, parking and waking on tmux. To keep the old behaviour for new agents, add to `~/.config/fleet/config.toml`:

```toml
[defaults]
driver = "tmux"        # every profile; or put it under [profile.copilot] / [profile.claude-copilot] / [profile.agy]
```

or spawn with `--driver tmux`. Details: [DESIGN.md §7d](DESIGN.md#7d-acp-agents-fleet-drives-the-tool-over-the-agent-client-protocol-adr-74-the-default-for-copilot-and-agy-since-v100).

### Typing into an ACP agent

Click the agent's pane and type at the `›` line at the bottom:

| Key | Does |
|---|---|
| Enter | Sends the line as a prompt (a pasted block stays one prompt) |
| a digit, Enter | Answers the open permission question with that option |
| Esc, or Ctrl-C on an empty line | Cancels the running turn |
| Ctrl-C, Ctrl-U, Ctrl-W | Clear the line, or the last word |
| Ctrl-T | Takes the agent over in the tool's own screen |

While you type there, the admiral won't send to the agent, the same as a tmux agent you type into. To work in the tool's own TUI instead, `fleet takeover <name>` (or Ctrl-T) parks the idle agent and resumes the same session on tmux in the same wall slot; `fleet takeover <name> --acp` hands it back to fleet's ACP host.

Claude agents stay on tmux. From v1.1.0 a Claude profile can opt in to ACP through the pinned claude-agent-acp adapter: `--driver acp` or `driver = "acp"` under `[profile.<name>]` ([docs/profiles.md](docs/profiles.md), ADR-76).

## Tool profiles

A profile says how to start one tool variant. Built-ins: `copilot`, `claude`, `agy`, `vibe` and `fake`. Add or override profiles in `~/.fleet/tools.toml`:

```toml
[claude-work]
command = "claude"
args = ["--model", "opus"]
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-work" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"
```

```sh
fleet profiles
fleet spawn api-auth claude-work ~/src/api "add rate limiting to /login"
```

Agents start through `bash -lc`, so shell aliases don't apply; make a profile instead. More in [docs/profiles.md](docs/profiles.md) and [examples/tools.toml](examples/tools.toml).

## The admiral and its MCP tools

`fleet up` starts the admiral under the `copilot` profile unless you say otherwise (`--admiral-profile NAME`, `$FLEET_ADMIRAL_PROFILE`, an `[admiral]` entry in your config, or `Alt+P` while running). It talks to `fleet mcp`, a stdio MCP server, with these tools:

| Tool | What it does |
|---|---|
| `fleet_status` | All agents: status, wall, age, spend so far, what it waits for; ends with the fleet total |
| `fleet_peek` | Last N lines (default 20, max 80) of one agent's screen |
| `fleet_send` | Send a prompt to an agent |
| `fleet_answer` | Answer a y/n prompt, a numbered option or an option by `label`. Needs `confirmed=true` |
| `fleet_spawn` | Start an agent from a profile (`effort` sizes its playbook: `light`, `standard`, `pro`) |
| `fleet_profiles` | List profiles |
| `fleet_close` | Stop an agent. Needs `confirmed=true` |
| `fleet_wall` | Show/hide an agent on the wall, or set the layout |
| `fleet_sessions` | List Copilot/Claude sessions started outside fleet |
| `fleet_adopt` | Resume one of those sessions as an agent |
| `fleet_note` | Save a note to `~/.fleet/decisions.log`; `pin` keeps it in every brief |
| `fleet_brief` | Handoff + recent notes + status, for a fresh admiral |
| `fleet_handoff` | Write a handoff and restart the admiral fresh |
| `fleet_team` | List, create or change teams; send, show, hide or close one |
| `fleet_reports` | What agents reported (done, blocked, question, progress) |
| `fleet_access` | Who may message whom |
| `fleet_access_decide` | Grant, deny or revoke that. Needs `confirmed=true` |
| `fleet_admiral_profile` | Show or swap the admiral's own tool profile |
| `fleet_info` | One agent's facts: driver (acp or tmux), status, git state, session, context fill, spend so far |

A leader agent (`fleet_spawn` with `leader=true`, or `fleet spawn --leader`) gets a squad-scoped set of its own: `fleet_squad_spawn`, `fleet_squad_profiles`, `fleet_squad_status`, `fleet_squad_info`, `fleet_squad_peek`, `fleet_squad_send`, `fleet_squad_reports` and `fleet_squad_close`. Each refuses any agent the leader did not spawn, and none of them can answer a prompt: that escalates to the admiral and to you. `[squad] max_members` (4) and `max_depth` (1) cap it.

The admiral must ask you before it answers a prompt, closes an agent or changes who may message whom. See [docs/admiral.md](docs/admiral.md).

## Files and logs

| Path | What |
|---|---|
| `~/.fleet/` | State dir. Override with `FLEET_HOME`. |
| `~/.fleet/board.json` | One entry per agent: profile, dir, pane, status, last line |
| `~/.fleet/events.jsonl` | Status transitions |
| `~/.fleet/status/<name>.json` | What an agent's own hooks last said about it (Claude: `Stop`, `Notification`, …) |
| `~/.fleet/watch.json` | Heartbeat of `fleet watch`, the process that keeps status live |
| `~/.fleet/tools.toml` | Your profiles |
| `~/.fleet/patterns.toml` | Your status patterns |
| `~/.fleet/decisions.log` | Notes the admiral kept for you |
| `~/.fleet/worker.md` | Extra playbook text prepended to every spawn prompt, at every effort level (`fleet playbook [--effort light\|standard\|pro]`) |
| `~/.config/fleet/config.toml` | Local preferences, never committed: starting mode (`mode = "auto"` runs every agent without permission prompts, whatever its tool), extra tool args, prompt auto-submit, `theme` (`catppuccin`, `stealth`, `neon-drive`, `phosphor`, `graphite`: set it, run `fleet up` again, agents keep running). `$FLEET_CONFIG` overrides the path; see [`examples/config.toml.example`](examples/config.toml.example). |
| `~/.fleet/logs/fleet.log` | Everything fleet does, one `component=` per line |

See [docs/troubleshooting.md](docs/troubleshooting.md).

## Development

```sh
make test   # go test ./..., never fails the build; output also in ~/.fleet/logs/test.log
make dev    # tmux -L fleet-dev: rebuild, test and restart the TUI on every save (needs watchfiles)
make hooks  # pre-push check: commit emails, messages, added lines, deny list, secrets (scripts/check-public.sh)
```

Tests that need tmux use throwaway `fleet-test-*` servers. Architecture: [docs/architecture.md](docs/architecture.md). Contributor notes: [AGENTS.md](AGENTS.md).

## Roadmap

Shipped so far: fake agents, scraped status and the overview; the live wall (screen-sized grid, status-aware eviction, a readability floor, pinning, a `▶` target slot and undo); `fleet adopt` and `fleet sessions`; the admiral's MCP server with handoff, notes, a context meter and profile swap; teams; agent mail with access control; the worker playbook and agent reports with a `✉` badge; a git worktree per agent on spawn; copy to the system clipboard from any pane; `fleet peek --summary` from a cheap model; and `fleet snap`, which turns a misread screen into a test fixture (Claude, agy and vibe read properly because of it). Since then: task cards with a done-check and a result file per agent (ADR-43); park/wake with a live cap and a RAM guard (ADR-44); a stuck rule (ADR-45); spend per agent (ADR-46); Claude status from its own hooks (ADR-49); `fleet web`, an optional browser view (ADR-66, alpha); `fleet wait` and `fleet_wait` (ADR-72); release binaries and a one-line installer (ADR-73, ADR-78); and ACP agents, the default for Copilot and agy since v1.0.0 (ADR-74).

Planned:

- Approve/reject keys and a spawn wizard in the TUI
- Workflow files (`fleet run plan.yaml`): tasks with dependencies, fanned out and back in
- A review step after a passing task, and recipes (named spawn presets)
- A per-task spend cap
- Auto-slotting the longest-waiting agent, saved walls, desktop notifications
- tmux control-mode events instead of polling

The long version, with reasoning, is [DESIGN.md §18](DESIGN.md#18-review-after-the-prototype-2026-11-04-what-would-make-it-a-real-agentic-fleet-manager).

## License

MIT, see [LICENSE](LICENSE).
