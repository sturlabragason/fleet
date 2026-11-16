# Pi

[Pi](https://pi.dev) ([earendil-works/pi](https://github.com/earendil-works/pi)) is a small coding-agent CLI. It runs one agent and leaves running several to tmux, so fleet supervises it like the other tools. The built-in `pi` profile runs it in a tmux pane (ADR-148).

```sh
npm install -g @earendil-works/pi-coding-agent
fleet spawn --model anthropic/claude-sonnet-4-5 a1 pi ~/src/app "fix the failing test"
```

Pi picks its model from its own settings unless the spawn names one with `--model` (Pi's ids, `provider/id`). Connect a provider first with `/login` inside Pi, or add a local endpoint such as Ollama in `~/.pi/agent/models.json`.

## What fleet adds

- **Status from Pi itself.** Each agent starts with `-e ~/.fleet/plugins/<name>/fleet-state.ts`, an extension fleet writes on every spawn and wake. It runs `fleet hook` when a turn starts (working), when Pi has settled and will not continue on its own (idle), and while an extension dialog waits for you (waiting). The screen patterns (`── ⠋ Working ─` and the context footer) are the fallback.
- **Peer tools.** The same extension registers the agent's `fleet-peer` MCP server for that process, with direct exposure, so the agent has `fleet_report`, mail and the rest. Nothing is written to `~/.pi/agent/mcp.json` or the project's `.pi/mcp.json`.
- **Sessions.** fleet sets the session id with `--session-id` and resumes with `--session`, so park, wake and resume continue the same conversation. The session file is `~/.pi/agent/sessions/<dir>/<timestamp>_<id>.jsonl`.
- **Spend.** `fleet spend` and the tile count Pi's tokens from that file: each assistant message, plus cache warming and compaction summaries. Pi's dollar cost is not shown.

Pi never asks before running a tool, so `mode = "auto"` changes nothing for it.

## Not yet

- ACP: Pi has no ACP server; the third-party `pi-acp` adapter would be the way, as for Claude.
- Your own MCP servers from `~/.pi/agent/mcp.json` stay on for workers; fleet can't switch them off one by one.
- fleet can't read Pi's prompt box (it has no prompt character), so a `fleet send` does not see a draft you left there, and a Pi admiral pane gets no typed wakes.
