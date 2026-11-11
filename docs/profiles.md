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
| `context_args` | Extra arguments per effort level (`light`, `standard`, `pro`, and `leader` for a squad leader), as an inline table of space-separated flags. The built-in Copilot profiles use it to leave out built-in tools an agent at that level does not need (ADR-60). Setting it replaces the whole table, so `context_args = { light = "" }` turns it off for every level. |
| `model_args` | Arguments that select a model, with `{model}`: `["--model", "{model}"]` on the built-in `copilot`, `claude-copilot` and `claude`, and inherited by your own profiles on those commands. Empty means the tool has no model flag; fleet then leaves its model alone and refuses a spawn that names one (ADR-100). |
| `default_model` | The Opus model fleet pins when neither `args` nor `extra_args` names one: `claude-opus-5.5` for Copilot, `opus` for Claude. Every fleet agent runs Opus, at every effort and as a leader; a spawn or profile that names a model which is not Opus is refused (ADR-100). |
| `acp_model_env` | The variable an `acp_command` adapter reads its model from (`ANTHROPIC_MODEL` on the built-in `claude`); fleet sets it to the pinned model. |
| `prompt_args` | Arguments that carry the first prompt, with `{prompt}`, so the tool submits it itself instead of fleet pasting it |
| `patterns` | Status pattern set: `copilot`, `claude`, `agy`, `vibe`, `fake` or your own. Defaults to the profile name. |
| `acp`, `acp_mode_args` | Arguments that start the tool's ACP server instead of its TUI, and its starting modes under ACP. Only a profile with `acp` or `acp_command` can run over ACP (DESIGN §7d). |
| `acp_command` | The whole argv under ACP, replacing `command`, `args` and the peer, extra and context args. The built-in `claude` runs the pinned claude-agent-acp adapter here (DESIGN §7d, ADR-76). |
| `acp_env` | Extra env under ACP only. `{command_path}` is the profile's `command` found in `~/.local/bin` or on PATH. |
| `acp_modes` | Fleet mode → ACP session mode, set with `session/set_mode` before the first prompt; `default` is used when no mode is set. |
| `user_mcp_config` | The tool's file of user-global MCP servers (`~/.copilot/mcp-config.json`, `~/.claude.json`). |
| `user_mcp_off` | Arguments that switch those servers off for a worker, added after the peer and plugin args under both drivers. Holding `{server}`, they repeat per server (`["--disable-mcp-server", "{server}"]`); without it they are added once. Claude's is `["--disallowedTools=mcp__{server}"]`. `[]` keeps every server. `keep_mcp` in `config.toml` keeps named ones (ADR-91). |
| `plugin_args` | Arguments that load fleet's per-agent plugin (hooks, and fleet-peer for Copilot), with `{plugin}` the plugin dir: `["--plugin-dir", "{plugin}"]` on the built-in `copilot`, `claude-copilot` and `claude`. Inherited like `peer_args`; `[]` turns the plugin off (ADR-99). |
| `plugin_mcp` | `true`: fleet-peer goes in the plugin's `.mcp.json` instead of `peer_args` (Copilot). |
| `native_binary` | `true`: start the Copilot CLI's native binary directly, under tmux and ACP, instead of the Node launcher `copilot` on PATH; saves about 65 MB per agent. **This skips the launcher's version pick and auto-update**: the agent runs whatever native binary npm last installed until you update with `npm install -g @github/copilot`. When fleet can't find the binary it logs why and starts the launcher. Default off. |
| `acp_meta` | `claude`: send the plugin and write fence in `session/new` `_meta.claudeCode.options` (the claude-agent-acp adapter). |
| `acp_mcp` | `session`: pass the peer MCP server in the ACP session's `mcpServers` instead of `peer_args`. |
| `acp_unset_env` | Env vars removed before the ACP server starts (the built-in `claude` drops API keys and OAuth tokens). |
| `acp_from` | Take every unset `acp*` key from another profile, for example `acp_from = "claude"` on a per-account Claude profile. |
| `driver` | `acp` or `tmux`: how agents of this profile run unless config.toml or `--driver` says otherwise. `acp` on the built-in `copilot` and `agy`. Unset, a profile whose `command` is `copilot` or `agy` (claude-copilot, your own copilot profiles) defaults to `acp` and takes the built-in's acp keys if it sets none; every other profile defaults to tmux (ADR-74 (i)). |

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
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-personal" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"

[claude-work]
command = "claude"
args = ["--model", "opus"]
mode_args = { auto = "--permission-mode bypassPermissions" }
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-work" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
patterns = "claude"
```

To run one of them over ACP (opt-in, ADR-76), add `acp_from = "claude"` to the profile, log in once by hand with `CLAUDE_CONFIG_DIR=$HOME/.claude-personal claude`, and put `driver = "acp"` under `[profile.claude-personal]` in config.toml, or spawn with `--driver acp`. It needs node 22+ and npx. The profile's `args` (`--model` included) are claude flags and don't reach the adapter: the model comes from `ANTHROPIC_MODEL`, `opus` from the built-in, and fleet sets it to the profile's Opus model (`--model` in `args`, else `default_model`), so a fleet agent runs Opus over ACP too (ADR-100). An own `acp_env` replaces the inherited one, so keep `CLAUDE_CODE_EXECUTABLE = "{command_path}"` in it. Fleet runs the installed claude through the adapter version pinned in fleet, never reads or passes tokens, and drops `ANTHROPIC_API_KEY` from the agent's env, so a subscription stays a subscription. Agent SDK use may count against your plan's limits faster, and a Team or Enterprise account follows its employer's policy. Use one config dir per account and don't switch accounts to get around limits ([Anthropic's terms](https://code.claude.com/docs/en/legal-and-compliance)).

Prefer the claude CLI's aliases (`opus`, `sonnet`) over full ids: they always resolve to the newest model. The Copilot CLI needs an exact id; the built-in `claude-copilot` uses `CopilotClaudeModel` in `internal/tools` (ADR-63).

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

The argv is the same for every agent of one profile, in one order under both drivers (command, peer MCP, plugin, user MCP switches, hooks, args, mode, context, session id, extra args, prompt), apart from the agent's own name, session id, plugin dir and prompt. Agents of one profile started a few minutes apart then share the tool's cached prompt prefix. The built-in `claude` adds `--exclude-dynamic-system-prompt-sections` for the same reason: it moves the working dir and git status out of the system prompt.

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
