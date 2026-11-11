# Troubleshooting

## Where to look

| What | Where |
|---|---|
| Everything fleet does (spawns, sends, status changes, tmux errors, caught panics) | `~/.fleet/logs/fleet.log` (older lines in `fleet.log.1`, `.2`) |
| `make test` output | `~/.fleet/logs/test.log` |
| `make dev` build and test results | `~/.fleet/logs/dev.log` |
| Agent state | `~/.fleet/board.json` |
| Status history | `~/.fleet/events.jsonl` |

`FLEET_HOME` moves all of these. Each log line has a `component=` attribute (`fleet`, `status`, `tmux`, `ui`, `wall`, `up`, `board`):

```sh
tail -f ~/.fleet/logs/fleet.log
grep 'component=status' ~/.fleet/logs/fleet.log | tail
```

## First checks

```sh
fleet ls                      # status of every agent
fleet ls --json               # full board entries
fleet peek <name>             # the agent's screen as fleet sees it
fleet peek <name> -n 200      # plus 200 lines of scrollback
fleet profiles                # effective profiles after tools.toml
tmux -L fleet ls              # is the fleet server running?
tmux -L fleet list-panes -a -F '#{pane_id} #{@role} #{@fleet_agent} #{pane_dead}'
```

## Common problems

**An agent shows the wrong status.** Status is a guess from the last six non-empty lines of the pane (see [profiles.md](profiles.md#status-patterns-fleetpatternstoml)). Limits:

- Tool UIs change; a new prompt or spinner may not match the built-in patterns. Run `fleet peek <name>`, then add a pattern in `~/.fleet/patterns.toml`.
- With no match, any change in the pane counts as working, and no change keeps the previous status. A tool that redraws a clock or cursor can look busy forever.
- Stuck means the agent is grinding: the same error banner twice in a row, or working with no screen change for `stuck_after` (default 10m, per profile in `config.toml`). A build that prints output is progressing and never counts; a long *silent* one does show as stuck. Set `stuck_after = "off"` for a profile that runs silent jobs. Nothing is paused for it — the tile goes red and the admiral gets one report.
- `waiting` patterns such as `(?i)approve` can match ordinary output that happens to be on the bottom lines.
- Every pattern set has recorded test captures in `internal/status/testdata`. When a status is wrong, `fleet snap <name> <state>` adds the screen as a fixture so the fix is a test case.

**`unknown profile`.** Run `fleet profiles`. Check the section name in `~/.fleet/tools.toml`; a parse error there is reported as `tools.toml: …`.

**The agent exits at once / the pane shows `process exited`.** The command was not found or failed. Panes stay open after exit (`remain-on-exit`), so read the error with `fleet peek <name>`. Agents start through `bash -lc`, so aliases don't apply and `PATH` comes from your login shell.

**`Enter` in the overview says the wall needs `fleet up`.** The wall only works when the overview runs inside the fleet tmux server. Use `fleet up`, not `fleet ui` in a plain terminal.

**The admiral has no fleet tools.** `fleet up` passes the MCP server to it on the command line, so check the admiral's argv in `fleet.log` (`component=up`). Tools only work under a `copilot` or `claude` profile; with any other profile (including `fake`) the admiral is an ordinary agent. `fleet admiral` shows the current profile and session.

**The overview pane is gone or dead.** Run `fleet up` again. It recreates or respawns the overview pane and doesn't touch running agents.

**The header says `⊘ offline since HH:MM`.** The network went away (ADR-110). While it is down, fleet probes a few public API hosts every 5 s (every 30 s when online). Agents whose turn fails show `offline` with ⊘ instead of `error`, an ACP host that dies is not reported as a crash or closed, new spawns wait in the queue ("waiting for the network"), and reports to the admiral wait too. After 20 s offline the admiral gets one "network down" report. When the network is back, fleet sends `continue` to the agents that failed, restarts dead hosts on their saved session, starts the queue and tells the admiral once which agents it resumed. An agent that fails again gets `continue` again after 30 s, 2 min and 8 min, then fleet leaves it alone. Behind a proxy fleet probes the `HTTPS_PROXY` host. `FLEET_NET_PROBE=host:port,...` picks other probe targets, `FLEET_NET_PROBE=off` turns the check off.

**An agent is listed as `gone`.** Its pane no longer exists (closed outside fleet, or the server was restarted). `fleet close <name>` removes it from the board.

**Start over.** `tmux -L fleet kill-server` stops every agent and the admiral. Then delete `~/.fleet/board.json` if you want an empty board.
