# fleet performance: where the RAM and CPU go

Measured on 2026-11-05 on one laptop: Intel Core Ultra 7 165H (P-cores on CPUs 0-11, E-cores on 12-19, low-power E-cores on 20-21), 31 GB RAM, zram swap, copilot CLI 1.0.105. Six fleet panes were open (admiral, five copilot workers) next to sessions that fleet does not manage. All numbers are read-only samples: `ps`, `pidstat` over 60 s, `/proc/<pid>/smaps_rollup` and cgroup `cpu.stat` deltas over 60 s. They are a snapshot, not a benchmark.

## What the numbers say

The setup is not what the usual suspects suggest. Each agent spawns no MCP servers and no language servers, so there is nothing to deduplicate. The two configured MCP servers are remote HTTP endpoints. The only local MCP process is `fleet mcp` (10 MB), and only the admiral runs it. The 165 skills and 16 agents loaded at startup (under 1 MB on disk) cost tokens, not RAM.

Per agent, the cost is the CLI process itself: a floor of about 270 MB of anonymous memory, plus conversation state, plus whatever the agent runs (go build, go test). CPU is near zero while idle and high while working. Fleet's own overhead is small.

Sessions that fleet does not manage held as much memory as the fleet agents did (2.3 GB PSS each way, plus 2.8 GB swapped outside fleet) and used about a third of the CPU.

## Measured costs

| Component | Count | Memory (each) | CPU (each, % of one core) |
|---|---|---|---|
| copilot CLI, fleet worker (native binary) | 5 | 330-460 MB PSS, 270-430 MB anon (2.3 GB PSS for all six fleet CLIs) | idle 0.3-0.5; streaming 15-43 (CLI only) |
| same, whole cgroup scope incl. tools it runs | 5 | - | idle 0.8-1; working 29-88 |
| copilot CLI, admiral (slim profile: no custom instructions, fleet tool only) | 1 | 300 MB PSS, 272 MB anon | 0.5 |
| `node` launcher shim kept alive above each fleet CLI | 6 | 24 MB PSS, 64 MB RSS | 0 |
| local MCP servers (`fleet mcp`) | 1 | 10 MB | 0 |
| duplicated MCP servers / LSP instances | 0 / 0 | - | - |
| `fleet ui` (1 s poll, 10 Hz redraw) | 1 | 46 MB RSS | 2.5 own, 5.9 incl. the tmux commands it spawns |
| fleet tmux server (history-limit 2000, copilot uses the alternate screen, so 0 lines kept) | 1 | 9 MB | 1.7 |
| copilot `statusLine` script (every 5 s in every copilot session, 4 jq + awk + grep + id each) | about 3 runs/s machine-wide | - | about 20 forks/s |
| go build/test run by agents (tests start their own tmux servers) | bursts | - | largest fork source, about 60 tmux execs/s during test runs |
| **Outside fleet:** standalone copilot sessions in terminal tabs, 11-22 h old, idle | 13 | 2.3 GB PSS in total, plus 2.8 GB swapped to zram | 0.8 each, about 11 in total |
| **Outside fleet:** claude, agy, two vibe-rs, agent-deck in another tmux server | 7 | 0.7 GB PSS in total | about 8 in total (each vibe-rs uses 2.5 while idle) |
| **Outside fleet:** terminal emulator (ptyxis), gnome-software with packagekitd | - | - | 30 and 48 at sample time (67 and 36 CPU-minutes since boot) |

Machine totals over the 60 s window: 250 % of one core in use (about 11 % of 22 threads), 176 % of it in `copilot.slice`. RAM: 17 GB used, plus 9 GB swapped to zram (2.8 GB of real RAM after compression).

### The "pool" metric in the copilot status bar

`pool 66% (7.9/12G)` comes from the user's copilot `statusLine` script, not from fleet. It is `memory.current / memory.high` of the systemd user slice `copilot.slice` (MemoryHigh=12G, MemorySwapMax=5G). Every copilot session runs in that slice: fleet panes and the standalone terminal sessions alike. `memory.current` includes page cache (3.6 GB of the 7.9 GB at sample time). Swapped memory is not counted (3.0 GB in the slice). So the bar shows neither "RAM used by fleet agents" nor "RAM used by agents" in general. Its peak reached 12 GB and `memory.events` shows 1584 `high` events, so the kernel has been throttling and swapping copilot memory to keep the slice under MemoryHigh.

## Attribution

- **CLI runtime baseline.** About 270 MB anonymous memory per instance, even with the slim admiral profile. The 189 MB binary's file pages are shared between instances (about 100-160 MB shared clean). Working agents add 60-160 MB for conversation state. The CPU spent while streaming (15-43 % of a core) is the CLI's own rendering and processing, and it stops when the agent stops.
- **Work the agents do.** The heaviest scope (88 % averaged over 60 s) was an agent running `go build` and `go test ./...`. By default each `go test` run may use every one of the 22 threads, so several agents testing at once add up. Peaks were not sampled separately.
- **MCP servers, LSPs, skills, agents, hooks.** Negligible RAM. Skills and custom instructions cost context tokens and so make each turn slower and more expensive, but they do not hold memory.
- **Fleet polling.** Every second one process runs `list-panes`, one `capture-pane` per agent, a locked read-modify-write of `board.json` and a wall re-tile. `fleet ui`'s 10 Hz animation tick re-renders the TUI view. Together that is about 6 % of one core plus 1.7 % in the tmux server. Real, but small.
- **The status watcher** (`fleet watch`, started by `fleet up`, DESIGN §7c) is that process whenever it runs, and the overview then reads `board.json` instead of capturing, so the bill above does not double when the overview is open — and it is paid even when it is closed, which is the point. Its own extra cost is a `stat` of `~/.fleet/status/` plus one per running agent's session log, five times a second: a few hundred microseconds with 8 agents, far below the capture pass it schedules. Event-driven refreshes are floored at one per 500 ms, so a busy agent writing its session log continuously costs at most two capture passes a second, not five.
- **tmux scrollback.** Negligible (history-limit 2000, alternate screen).
- **Sessions outside fleet.** 13 idle standalone copilot sessions plus 7 other agent processes hold about 3 GB PSS plus 3 GB of swap, and use about 20 % of a core while doing nothing.

## Options, ranked by expected saving for the effort

Estimates assume the six fleet panes measured here. RAM is PSS unless stated; CPU is percent of one core.

| # | Option | Saves | Effort | Tradeoffs |
|---|---|---|---|---|
| 1 | **Close stale sessions outside fleet** (13 standalone copilot tabs, idle claude, agy, vibe-rs, agent-deck), and later bring them under fleet so auto-close covers them | about 3 GB PSS plus 3 GB swapped (about 1 GB of real RAM after zram compression); about 20 % CPU constant | none (manual), small to adopt into fleet | Lose their live context; copilot sessions can be resumed with `--resume`. |
| 2 | **Auto-close idle fleet agents** (fleet-comms is building this) | 330-460 MB PSS each; little CPU (idle is under 1 %) | in progress | Reopening costs a resume and context reload. |
| 3 | **Cap agent CPU in the slice:** `AllowedCPUs=12-19` (E-cores) and/or `CPUQuota=400%` on `copilot.slice` | not measured: it bounds how many cores agents can heat at once and keeps P-cores free for the desktop | small (one drop-in plus `systemctl --user daemon-reload`) | Builds and tests in agents run slower. Also pins the standalone sessions, which share the slice. CPUs 20-21 are low-power cores (2.5 GHz) and would slow agents further if included. Easy to revert. |
| 4 | **Throttle toolchains inside agent panes:** set `GOFLAGS=-p=4` and `GOMAXPROCS=4` (or run under `nice`) in the spawn environment | not measured: limits each build or test run to 4 threads instead of up to 22 | small (spawn env or profile data) | Slower test runs. Applies only to the toolchains configured. |
| 5 | **Limit concurrent *working* agents, with a queue** (for example 3) | each working agent used 0.3-0.9 core incl. its tools; going from 5 to 3 removes about 40 % of agent CPU if all 5 are working | medium (spawn queue in fleet) | Throughput drops. Idle agents waiting for input should not count, so the cap should apply to `working`, not to open panes. |
| 6 | Spawn the native copilot binary directly instead of through the `node` launcher | 24 MB PSS per agent (about 140 MB for six) | small (profile command) | Bypasses the launcher's platform detection; the path changes when the CLI updates. |
| 7 | Cheaper statusline: `refreshInterval` 30 s and one `jq` call instead of four, or drop the pool bar | about 15-20 forks/s machine-wide, low single-digit % CPU | small (user config, not fleet) | Context bar lags; the pool number is misleading anyway (see above). |
| 8 | Cheaper fleet polling: skip the 10 Hz tick when nothing is animating, poll off-wall panes every 5 s, write `board.json` only when something changed | about 3-5 % of one core | small to medium | Status of hidden agents updates later. |
| 9 | Subagents inside one CLI instead of separate agents, for fan-out work | about 300 MB per agent avoided | process change | Subagents share one context budget and one pane; less visible on the wall. |
| 10 | Remote agent runtime (DESIGN.md "Future work") | removes the agents' CPU and RAM from the laptop entirely | large | Needs a cluster, secrets, networking and a budget. |

Measured and **not** recommended:

- **Share MCP servers across agents.** None are duplicated today, so there is nothing to save.
- **Lighter tool profile for workers.** The slim admiral profile uses 272 MB of anonymous memory, the same as the floor of a worker, so a slimmer profile has no measurable RAM saving. Worth doing for tokens and speed, not for heat.
- **Lazy or disabled LSPs.** None are running.
- **SIGSTOP or cgroup freeze for idle agents.** Idle agents already use under 1 % CPU, and frozen memory stays allocated. Auto-close (option 2) gets more for less risk.
- **Per-agent memory caps or `--max-old-space-size`.** The slice already has MemoryHigh. A hard heap cap would crash an agent with a large context instead of slowing it down.
- **Smaller tmux history.** Already negligible.

## Implemented, and how to undo it

Options 2, 3, 4, 5, 7 and part of 8 are in place. Option 1 is a manual decision left to the user.

| # | What | Where | Undo |
|---|---|---|---|
| 2 | `auto_close_done = true` closes an agent after a done report that left no uncommitted or unmerged work of its own | user `config.toml` `[defaults]` | remove the line |
| 3 | `CPUQuota=600%` and `AllowedCPUs=12-19` for all copilot sessions | `~/.config/systemd/user/copilot.slice.d/50-cpu.conf`, example in `examples/systemd/copilot.slice.d-50-cpu.conf` | `rm` the file, then `systemctl --user daemon-reload` |
| 4 | `env = { GOFLAGS = "-p=4", GOMAXPROCS = "4" }`, added to every agent pane fleet starts | user `config.toml` `[defaults]` (or `[profile.X]`) | remove the line; affects agents started afterwards |
| 5 | At most `max_working` agents (default 4) working at once; extra spawns wait as `queued` and start, oldest first, when a slot frees while `fleet ui` runs | built in; `max_working` in `[defaults]`, `0` turns the cap off | `max_working = 0` |
| 7 | Copilot statusline refreshes every 30 s instead of 5 s, and the script runs `jq` once instead of four times | `~/.copilot/settings.json`, `~/.copilot/statusline.sh` | restore the `*.pre-perf` backups next to them |
| 8 | `fleet ui` captures panes that are not on the wall every 5 s instead of every second | built in | none needed; status of hidden agents lags by up to 5 s |
| 9 | With `fleet watch` live, `fleet ui` makes no tmux calls per poll: it reads the watcher's `~/.fleet/tmux.json` snapshot and goes to tmux only to change the wall (ADR-94). Measured: 4.6 % → 0.75 % of a core with 3 agents | built in | none needed |

`CPUQuota` applies straight away. `AllowedCPUs` needs the `cpuset` controller, which the user systemd instance does not get by default. Until a root drop-in delegates it, systemd stores the setting but the kernel ignores it. To enable it (needs sudo, then a re-login or reboot):

```sh
sudo mkdir -p /etc/systemd/system/user@.service.d
printf '[Service]\nDelegate=cpu cpuset io memory pids\n' | sudo tee /etc/systemd/system/user@.service.d/delegate.conf
sudo systemctl daemon-reload
# check after re-login: should print 12-19
cat /sys/fs/cgroup/user.slice/user-$UID.slice/user@$UID.service/copilot.slice/cpuset.cpus
```

Undo by deleting `delegate.conf`, then `sudo systemctl daemon-reload` and a re-login.

## How to re-measure

```sh
# memory per agent pane: PSS of each pane's process tree
tmux -L fleet list-panes -a -F '#{pane_pid} #{@fleet_agent}'
grep -E '^(Pss|Pss_Anon|Swap):' /proc/<pid>/smaps_rollup

# CPU per agent incl. its tools: cgroup scope delta over 60 s
S=/sys/fs/cgroup/user.slice/user-$(id -u).slice/user@$(id -u).service/copilot.slice
grep usage_usec "$S"/*.scope/cpu.stat; sleep 60; grep usage_usec "$S"/*.scope/cpu.stat

# what is forking
pidstat -u -p ALL 60 1
```
