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
| `patterns` | Status pattern set: `copilot`, `claude`, `agy`, `vibe`, `fake` or your own. Defaults to the profile name. |

Placeholders in `command` and `args`: `{fleet}` is the fleet binary, `{name}` the agent name, `{id}` the session ID.

The file uses a small TOML subset: `[section]`, `key = "string"`, `key = ["list"]`, `key = { K = "v" }` and `#` comments.

### Examples

Claude model through the Copilot CLI:

```toml
[claude-copilot]
command = "copilot"
args = ["--model", "claude-opus-5"]
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "copilot"
```

Two Claude Code accounts, kept apart by `CLAUDE_CONFIG_DIR`:

```toml
[claude-personal]
command = "claude"
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-personal" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"

[claude-work]
command = "claude"
args = ["--model", "claude-sonnet-5"]
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-work" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"
```

agy or vibe with extra flags (no `launch_id`, since neither sets a session ID at launch):

```toml
[vibe-fast]
command = "vibe"
args = ["--your-flag"]
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
