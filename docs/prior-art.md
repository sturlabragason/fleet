# Prior art: what fleet can borrow

Written 2026-11-05. Is fleet reinventing established patterns? Partly. tmux plus a worktree per agent is now the standard substrate; a lean admiral LLM (ADR-4, ADR-11) and a status-first wall are less common. The weak spot: fleet reads screens while the tools now publish structured events.

**Verification.** ✓ = checked on 2026-11-05 against the project's docs, README, `--help` or a local log. ○ = from memory, not re-checked. Tools in this space churn: Crystal and Coder AgentAPI are deprecated and Vibe Kanban is sunsetting (✓).

## 1. Comparison

| Prior art | What it is | Borrow | Avoid |
|---|---|---|---|
| Claude Squad ✓, Uzi ✓ | Terminal tools; tmux plus worktree per agent | A diff tab per agent (Squad); a dev-server port per worktree (Uzi `$PORT`) | Auto-accepting prompts by keystroke (`--autoyes`, `uzi auto`); ADR-9, ADR-13 |
| Conductor ○, Sculptor ✓, Nimbalyst ✓, Vibe Kanban ✓ | Desktop or web apps: workspace per task, review diff, merge, archive | Named task lifecycle; "archive" is not delete; task cards before sessions (§18 #5–7) | Owning the agent UI; fleet's value is the unchanged CLI in a real pane (R3) |
| Coder AgentAPI ✓ | Runs a CLI in an emulated terminal, diffs the screen, reports `stable`/`running` | Screen diff works as a generic fallback | Scraping as the main path: AgentAPI is deprecated and Coder now points to its own agent harness |
| Claude Code agent teams, cross-session messaging ✓ | Lead plus teammates in tmux panes; file-locked shared task list (`pending`, `in progress`, `completed`, blocked deps); `SendMessage` read between tool calls | File locks for shared state (fleet's mailbox already does this); deliver messages at a safe point in a turn, not only when idle | Competing on Claude-only teams; fleet's job is the mixed fleet and the user's attention |
| Claude Code hooks ✓ | `Stop`, `StopFailure`, `PermissionRequest`, `Notification`, `SessionEnd`, `TaskCompleted` and more; per launch via `claude --settings <json>` | Status from events instead of patterns | Hooks that block or run slowly |
| Copilot CLI hooks and `events.jsonl` ✓ | Hooks incl. `agentStop`, `permissionRequest`; the session log has turn, permission and shutdown events | Status from the log fleet already tails | User-level hooks: they fire for every Copilot session |
| MCP ✓ | fleet's tool channel today | Keep; appending warnings to tool results (ADR-11) works | Server push notifications (the CLIs don't turn them into turns; ADR-19) |
| ACP ✓ | JSON-RPC editor-to-agent; `copilot --acp` (preview) | A headless path for agents that need no pane | Local use: the client renders the session, so the CLI's own TUI is likely gone ○ |
| A2A ✓ | HTTP agent-to-agent tasks; states `submitted, working, input-required, auth-required, completed, failed, canceled, rejected` | The state names | An endpoint per agent (ADR-19) |
| Erlang/OTP, supervisord, systemd ○ | Restart `permanent/transient/temporary`; max restarts per period; `STARTING`, `BACKOFF`, `FATAL`; `Restart=on-failure` | Restart only after an abnormal exit, with a cap; a visible backoff/gave-up state | `one_for_all`; a daemon (§16 #7) |
| Kubernetes controllers and Jobs ○ | Level-triggered reconcile; Job `ttlSecondsAfterFinished` | Act on observed state each refresh, with a TTL, not on one event | CRDs locally |
| CI runners, asciinema ○ | Done = exit code; terminal recordings | The done-check (§18 #5); optional `pipe-pane` recording to feed `testdata` (§18 #2) | Recording by default (secrets on screen) |
| kubernetes-sigs/agent-sandbox ✓ | `Sandbox` CRD: one stateful pod, stable identity, persistent volume, pause/resume, warm pools, gVisor or Kata | One pod per agent with tmux and the unchanged CLI inside, session store on the volume | Nothing found |
| devcontainers, Coder, Ona, Daytona, E2B, Modal ○ | Environments from a spec; API sandboxes | `devcontainer.json` as the agent image | Short-lived or IDE-centred platforms; data leaving the user's infrastructure |
| kagent ✓ (ADR-145) | Kubernetes-native agents; a Claude harness behind an A2A gateway, one Session per conversation | The `kagent` profile: an agent in a cluster over A2A, driven as an ACP agent | Applying its CRDs from fleet |

## 2. Top 5, ranked by value for effort

1. **Copilot status from `events.jsonl`** (high value, low effort). `internal/status/subagents.go` already tails it. Open `permission.requested` or running `ask_user` → `waiting`; `assistant.turn_start` → `working`; `assistant.turn_end` with nothing open → `idle`. Scraping keeps the `waiting_for` text; disagreements become `testdata` cases. Needs an ADR extending ADR-18. ○ Not checked that every approval dialog logs `permission.requested`.
2. **Claude hooks per launch** (high value, low effort; §18 #3). Claude profiles get `--settings` with `Stop`, `PermissionRequest`, `Notification` and `SessionEnd` hooks as data, like `peer_args`. Each runs `fleet hook <event>`, which writes `~/.fleet/status/<name>.json` and exits.
3. **Restart policy** (medium value, low effort). Per profile, `restart = "never" | "on-failure"`, capped at 2 in 10 min. An abnormal exit resumes by session ID; past the cap the tile says "gave up" and the admiral gets a `failed` report. A clean exit or `close` never restarts.
4. **Auto-close as a reconcile rule** (medium value, medium effort). Each refresh, act only when the last report is `done`, the agent is `idle`, no subagents run, no mail is unread and the work tree is clean and merged, for longer than `close_after` (e.g. 10 min). Never act on `waiting`, `error` or `stuck`. Once park/wake exists (§7, §18 #10), make park the default action, because it can be undone.
5. **A2A state names, on the board only** (medium value, medium effort). Keep tile states; add a task state (`working`, `input-required`, `completed`, `failed`, `canceled`) set by reports and the done-check, shared by the admiral, auto-close and a cloud runtime.

## 3. In-flight work

| Work | Verdict | Change |
|---|---|---|
| Status detection (scrape-only) | Change course, as ADR-7 and §16 #3 already planned | Stop growing Copilot and Claude patterns; do recommendations 1–2 next. Scraping stays for agy, vibe, fake. |
| Report-back (§6.7, ADR-20, merged) | Keep, adjust | Post a board event on each turn end (recommendations 1–2), so the admiral learns an agent stopped even without `fleet_report`. Tell Claude agents to use fleet mail, not native `SendMessage`. |
| Auto-close (§6.7, opt-in, fires on `done`) | Change course while it is off by default | Move the trigger into the refresh loop with the conditions in recommendation 4. |
| Cloud runtime (§18 idea) | Keep direction | agent-sandbox pod per agent, image from `devcontainer.json`. Open: syncing status files from the pod; the wall still needs the terminal stream. |
