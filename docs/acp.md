# ACP agents

How to work with an agent that fleet drives over the Agent Client Protocol (ACP), and how to keep the old tmux behaviour.

Copilot and agy agents (every profile that runs them, `claude-copilot` included) run over ACP by default since v1.0.0. Their status, questions and answers come from the tool itself, and their pane shows fleet's transcript of the session instead of the tool's own screen. Claude and vibe agents stay on tmux. The full design is in [DESIGN.md §7d](../DESIGN.md#7d-acp-agents-fleet-drives-the-tool-over-the-agent-client-protocol-adr-74-the-default-for-copilot-and-agy-since-v100).

## Typing into an ACP agent

Click the agent's pane and type at the `›` line at the bottom:

| Key | Does |
|---|---|
| `Enter` | Sends the line as a prompt (a pasted block stays one prompt) |
| a digit, `Enter` | Answers the open permission question with that option |
| `Esc`, or `Ctrl-C` on an empty line | Cancels the running turn |
| `Ctrl-C`, `Ctrl-U`, `Ctrl-W` | Clear the line, or the last word |
| `Ctrl-T` | Takes the agent over in the tool's own screen |
| `Ctrl-V` | Attaches the image on the clipboard (needs wl-clipboard or xclip on Linux); pasting or dropping an image file's path attaches that file |

While you type there, the admiral won't send to the agent, the same as a tmux agent you type into.

## Taking over in the tool's own screen

`fleet takeover <name>` (or `Ctrl-T` in its pane) parks the idle agent and resumes the same session on tmux in the same wall slot. `fleet takeover <name> --acp` hands it back to fleet's ACP host.

## Claude over ACP

From v1.1.0 a Claude profile can opt in to ACP through the pinned claude-agent-acp adapter: `--driver acp`, or `driver = "acp"` under `[profile.<name>]` ([profiles.md](profiles.md), ADR-76).

## Upgrading from before v1.0.0

From v1.0.0, new `copilot` and `agy` agents run over ACP. `fleet snap` and the screen patterns no longer apply to them. Agents that exist when you upgrade keep running, parking and waking on tmux. To keep the old behaviour for new agents, add to `~/.config/fleet/config.toml`:

```toml
[defaults]
driver = "tmux"        # every profile; or put it under [profile.copilot] / [profile.claude-copilot] / [profile.agy]
```

or spawn with `--driver tmux`.
