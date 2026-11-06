# Tool profiles

A profile tells fleet how to start one tool variant: which command, which arguments, which environment, and which status patterns to use. `fleet spawn <name> <profile>` and the admiral's `fleet_spawn` both take a profile name.

## Built-in profiles

| Profile | Command | Session ID at launch | Resume args | Patterns |
|---|---|---|---|---|
| `copilot` | `copilot` | `--session-id {id}` | `--resume {id}` | `copilot` |
| `claude` | `claude` | `--session-id {id}` | `--resume {id}` | `claude` |
| `agy` | `agy` | not supported | `--conversation {id}` | `agy` |
| `vibe` | `vibe` | not supported | `--resume {id}` | `vibe` |
| `fake` | `{fleet} fakeagent {name}` | not supported | none | `fake` |

`fleet profiles` prints the effective list, including yours. Resume arguments are stored for park/wake, which is planned and not built yet.

## Adding your own: `~/.fleet/tools.toml`

Each `[section]` is a profile. A section with a built-in name overrides only the fields you set; a new name defines a new profile. A copy-ready file is in [examples/tools.toml](../examples/tools.toml).

| Field | Meaning |
|---|---|
| `command` | Program to run, looked up on the login `PATH` |
| `args` | Arguments passed on every start |
| `env` | Environment for the agent, as an inline table. `$VAR` and `${VAR}` are expanded from fleet's environment. |
| `launch_id` | Arguments that set a session ID at launch. fleet generates a UUID for `{id}` and stores it on the board. |
| `resume` | Arguments that resume a session by `{id}` (for park/wake, planned) |
| `mode_args` | Starting modes, as an inline table of space-separated flags. Give every profile an `auto` key holding that tool's allow-all flag, or `mode = "auto"` in `config.toml` cannot reach it and your agents keep asking for permission (ADR-56). |
| `prompt_args` | Arguments that carry the first prompt, with `{prompt}`, so the tool submits it itself instead of fleet pasting it |
| `patterns` | Status pattern set: `copilot`, `claude`, `agy`, `vibe`, `fake` or your own. Defaults to the profile name. |

Placeholders in `command` and `args`: `{fleet}` is the fleet binary, `{name}` the agent name, `{id}` the session ID.

The file uses a small TOML subset: `[section]`, `key = "string"`, `key = ["list"]`, `key = { K = "v" }` and `#` comments.

### Examples

Claude model through the Copilot CLI:

```toml
[claude-copilot]
command = "copilot"
args = ["--model", "claude-opus-5.5"]
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "copilot"
```

Two Claude Code accounts, kept apart by `CLAUDE_CONFIG_DIR`:

```toml
[claude-personal]
command = "claude"
args = ["--model", "opus"]
mode_args = { auto = "--permission-mode bypassPermissions" }
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-personal", CLAUDE_CODE_SUBAGENT_MODEL = "sonnet" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"

[claude-work]
command = "claude"
args = ["--model", "opus"]
mode_args = { auto = "--permission-mode bypassPermissions" }
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-work", CLAUDE_CODE_SUBAGENT_MODEL = "sonnet" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"
```

Prefer the claude CLI's aliases (`opus`, `sonnet`) over full ids: they always resolve to the newest model. The Copilot CLI needs an exact id; the built-in `claude-copilot` uses `CopilotClaudeModel` in `internal/tools` (ADR-58).

agy or vibe with extra flags (no `launch_id`, since neither sets a session ID at launch):

```toml
[vibe-fast]
command = "vibe"
args = ["--your-flag"]
mode_args = { auto = "--auto-approve" }
resume = ["--resume", "{id}"]
patterns = "vibe"
```

Changing a built-in for every spawn:

```toml
[copilot]
args = ["--allow-all"]
```

## How agents are started

Each agent runs as `bash -lc 'exec <command> <args>…'` in a new hidden tmux window `ag-<name>` on the `fleet` server, in the directory you give (default: the current directory). The login shell gives the agent your normal `PATH`, with `~/.local/bin` put first.

Shell aliases and functions are not available in that shell. If you use `alias cw='CLAUDE_CONFIG_DIR=… claude'`, make it a profile instead.

## Folder trust (ADR-47)

Tools ask "do you trust this folder?" the first time they run somewhere new. Nobody is watching a spawn, so fleet answers that question before the tool can ask it: just before launch it writes the agent's dir (the worktree, when there is one) into the tool's own trust store. Which store depends on the profile's `command` and `env`, not its name — `claude-copilot` runs `copilot`, so it is trusted the Copilot way.

| Profile | Where fleet writes | What it sets |
|---|---|---|
| `claude` | `$CLAUDE_CONFIG_DIR/.claude.json`, else `~/.claude.json` | `projects["<dir>"]`: `hasTrustDialogAccepted`, and `hasClaudeMdExternalIncludesApproved`/`…WarningShown` for the modal behind it |
| `claude-personal`, `claude-work` | the same file under that profile's `CLAUDE_CONFIG_DIR` (`~/.claude-personal`, `~/.claude-work`) | the same keys |
| `copilot`, `claude-copilot` | `~/.copilot/config.json` | the dir appended to `trustedFolders` |
| `vibe` | `$VIBE_HOME/trusted_folders.toml` (default `~/.vibe`) | the dir added to `trusted`, removed from `untrusted` |
| `agy` | — | no trust store on disk; the dialog is answered on screen instead |

Every other key in those files is kept, and the file is rewritten under a lock and renamed into place, so a running agent writing its own config at the same moment cannot lose anything. A config dir that does not exist means you have never run that tool; fleet logs it and does not create one.

If fleet cannot write that config — agy keeps no trust store on disk — it answers on screen instead: for the first seconds after launch it watches the pane and picks the dialog's "Yes, I trust …" option, before the first prompt, which would otherwise be typed into the menu. A spawn fleet could pre-mark never waits for this.

Turn it off per profile (you then answer the prompts yourself):

```toml
# ~/.config/fleet/config.toml
[profile.claude-work]
auto_trust = false
```

**Fleet never starts an agent in your home directory.** A spawn or adopt dir that is `$HOME` or `/` is moved to `~/.fleet/scratch/<name>`; the reply and `fleet.log` say so. Agents started in `$HOME` read every dotfile as project config, share one project identity, and trip each tool's own "dangerous directory" guard.

## Status patterns: `~/.fleet/patterns.toml`

Status is read from the last six non-empty lines of the pane. The lowest line that matches any pattern decides; within one line the order is waiting, error, working, idle. If nothing matches but the pane changed, the agent counts as working. An agent is stuck when the same error banner shows twice in a row, or when it is working and its pane has not changed for `stuck_after` (default 10 minutes; see `config.toml`).

Built-in sets are in [`internal/status/status.go`](../internal/status/status.go). Override a list or add a set:

```toml
[claude]
waiting = ['Do you want to', '❯ 1\. Yes', '\[y/n\]', 'Allow this']

[mytool]
waiting = ['\(y/N\)']
error = ['^ERROR']
working = ['\.\.\.$']
idle = ['^\$ $']
```

Patterns are Go regular expressions in single-quoted strings. A key you set replaces that whole list. Then set `patterns = "mytool"` in the profile.
