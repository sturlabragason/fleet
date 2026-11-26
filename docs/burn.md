# Quota Burn (`fleet burn`)

`fleet burn` uses leftover subscription quota before a rolling or weekly reset by spawning worktree-only agents for pending backlog items tagged `+burn` (ADR-190).

Phase 1 runs in **dry-run mode by default**: it reads quotas, computes how many runs fit above the reserve, selects candidate `+burn` tasks, and logs what it would do without spawning or messaging any agent.

## How it works

On each tick (`fleet burn --tick`), for every profile listed in `[burn] profiles`:

1. **Read quota** using the profile's reader:
   - `agy`: runs `agy -p /quota` (no model call) and parses every weekly and 5-hour window.
   - `nanogpt` (for `pi`): calls the NanoGPT subscription usage API (`https://nano-gpt.com/api/subscription/v1/usage`, or a management endpoint via `usage_url` + `token_env`).
   - `ai-usagebar` (for `claude` profiles): runs `ai-usagebar usage --json` through the companion contract and reads the entry `anthropic@<account>`. Each Claude seat is one `[[anthropic.accounts]]` entry in `~/.config/ai-usagebar/config.toml` whose `label` is the `account` set in `[burn.<profile>]`; an `Extra usage` metric counts as overage.
2. **Safety gates**:
   - Skips profiles whose reader reports paid overage or extra usage enabled (unless `allow_overage = true`).
   - Skips profiles outside the burn window (`time_to_reset > window`).
   - Stops at the reserve (`remaining_pct <= reserve_pct`).
3. **Wrap-up before reset**: when `time_to_reset <= wrap_up_before`, asks any running `burn-*` agent on that profile to commit its work to its branch and report done.
4. **Size N**:
   - Computes `N = floor((left - reserve) / mean_cost)` using the profile's mean run cost from `~/.fleet/runs.jsonl` (falling back to `default_cost_tokens` / `default_cost_pct` when fewer than `min_samples` runs have spend data).
   - Caps `N` by `max_per_tick`, `max_concurrent` burn agents, and `[defaults] max_working`.
5. **Select tasks**: queries the `task` companion (`task +burn status:pending export`, ordered by urgency), skips tasks already running as `burn-<id8>`, and maps the optional `effort` field (`light` or `standard`, default `standard`; `pro` runs as `standard` so a burn agent never merges itself).
6. **Worktree-only isolation**: spawned burn agents work strictly in `<repo>.wt/burn-<id8>` on branch `fleet/burn-<id8>`. They never merge to `main`, push `main`, or write to external systems.

## Configuration

Add `[burn]` to `~/.config/fleet/config.toml`. No profile is burned unless listed in `profiles`, and `--profile P` is refused for a profile not listed there:

```toml
[burn]
dry_run = true          # set false (or pass --live) when ready to spawn
profiles = ["agy", "pi", "claude"]
window = "18h"          # start burning this long before reset
reserve_pct = 15        # keep at least 15% untouched
wrap_up_before = "30m"  # ask running burn agents to wrap up before reset
max_per_tick = 2        # max new spawns per profile per tick
max_concurrent = 3      # max running burn agents across all profiles
default_cost_pct = 5.0
default_cost_tokens = 2000000
min_samples = 3
tag = "burn"
allow_overage = false

[burn.pi]
reader = "nanogpt"
token_env = "NANOGPT_MANAGEMENT_TOKEN" # optional scoped read-only token

[burn.claude]
reader = "ai-usagebar"
account = "work"          # the [[anthropic.accounts]] label in ai-usagebar's config
projects = ["my-project"] # optional: keep scoped/customer seats restricted to approved projects
max_per_tick = 1          # optional: tighter cap per tick
```

## Backlog setup (`task` companion)

`fleet burn` reads pending items via the `task` companion (`task +burn status:pending export`). Define optional `effort` (`light`, `standard`, `pro`) and `dir` (repository path) fields in your task CLI config (`~/.taskrc`), and tag safe, self-contained items with `+burn`.

## Commands

```sh
fleet burn                      # dry-run status and plan
fleet burn --tick               # timer tick (respects dry_run; appends to ~/.fleet/logs/burn.jsonl)
fleet burn --tick --live        # live tick for one run
fleet burn --json               # machine-readable plan
fleet burn --profile agy        # check one profile
fleet burn --review             # write morning Accept/Reject HTML review page for fleet/burn-* branches
```

## Systemd user timer
Unit files are shipped in `examples/systemd/` and are not installed automatically. Edit `WorkingDirectory` in the service to the repo that `+burn` tasks without a `dir` field run in, and make sure its `PATH` reaches `task`, `agy` and `ai-usagebar`. Then enable the 30-minute timer:

```sh
mkdir -p ~/.config/systemd/user
cp examples/systemd/fleet-burn.service examples/systemd/fleet-burn.timer ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now fleet-burn.timer
```

Check logs with `journalctl --user -u fleet-burn.service -n 50` or `~/.fleet/logs/burn.jsonl`.

## How to tell it ran

- `systemctl --user list-timers fleet-burn.timer` shows the last and the next run.
- Every tick appends one JSON line to `~/.fleet/logs/burn.jsonl`: `tail -n1 ~/.fleet/logs/burn.jsonl | jq '.profiles[] | {profile, skip_reason, planned_spawns}'`.
- `journalctl --user -u fleet-burn.service -n 20` prints the same plan as `fleet burn`.
- A live tick that spawns shows `burn-<id8>` agents on the board and `fleet/burn-<id8>` branches; `fleet burn --review` lists them.
