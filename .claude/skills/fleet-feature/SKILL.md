---
name: fleet-feature
description: Builds one feature of the fleet TUI/CLI in this repo end to end - spec, brief, builder subagent, review, checks, docs, local commit, live rollout. Use when asked to "implement <X>", "build <X>", "add <X> to fleet", or to do any item from the DESIGN.md roadmap (§18), e.g. "implement fleet adopt".
---

# fleet-feature

You are the api-service. Subagents write the code; you keep the brief, the checks and the user in the loop. Keep chat replies short.

## 1. Find the spec (no code yet)
- Look the feature up in `DESIGN.md` (roadmap §18, requirements §0, CLI §4, keys §5, ADRs §14). Use `grep -n`; do not read the whole file.
- If it is missing or vague, write the spec into DESIGN.md first (a short subsection: behaviour, CLI/keys/MCP surface, out of scope). Ask the user only if two readings lead to different work; otherwise state your reading in one line and go.

## 2. Write the brief
Copy `brief-template.md` (next to this file) to `.work/briefs/<slug>.md` and fill it in. The done-check is mandatory and machine-checkable:
`make build && go test ./<changed pkgs>/...` (exit 0) plus any feature-specific command; the full `go vet ./... && go test ./...` and `-race` run in CI on the pushed `fleet/<name>` branch (ADR-69).
`make test` always exits 0 by design, so never use it as the gate.

## 3. Build
- Dispatch the **fleet-builder** agent with the brief path and full context (it starts fresh). Background mode if you have other work; otherwise sync.
- TUI, keys, layout or anything the user looks at: also dispatch **fleet-designer** on the brief *before* the builder, and fold its answer into the brief.
- Independent slices may run in parallel only in separate worktrees (`git worktree add ../fleet.wt/<slug> -b slice/<slug>`); the builder merges nothing.

## 4. Review and verify
- Dispatch **fleet-reviewer** on the uncommitted diff with the brief path. HOLD findings go back to the same builder (write_agent), at most 2 rounds; then stop and report the root cause.
- Run the done-check yourself. Do not trust a "tests pass" claim you didn't see.

## 5. Docs and commit
- DESIGN.md: mark the item built (§18 table or §9), record any new decision as an ADR in §14.
- `CHANGELOG.md`: one line for the user-facing change under today's date, newest first (what the user can do now, ADR in brackets). It is embedded; `Alt-g` shows it.
- `AGENTS.md`: only if a command or rule changed. Keep it short.
- `fleet --help` usage text and `internal/mcp/instructions.md` (admiral rules) if the CLI or MCP surface changed.
- Commit locally with a clear message and an `Assisted-by:` trailer (see AGENTS.md; never an AI `Co-authored-by:`). Push only the `fleet/<name>` branch, for CI; never main.

## 6. Roll out to the live fleet
The user runs real agents on tmux socket `fleet`. Never kill that server.
- `make install` keeps `~/.local/bin/fleet` pointing at `bin/fleet`, so new CLI commands are live after `make build`.
- Overview TUI changed: `tmux -L fleet respawn-pane -k -t <ov pane> fleet ui` (find it with `tmux -L fleet list-panes -a -F '#{pane_id} #{@role}'`). Safe; it holds no state.
- MCP server or admiral flags changed: the admiral must restart, which loses its chat. **Ask first.**
- tmux key bindings changed: `fleet up` re-applies them, or run the bind commands against `-L fleet`.

## 7. Report
Three to six lines: what works now, how to try it (one command), commit hash, anything the user must do (e.g. restart the admiral). No process narration.

## Rules
- Real claude/copilot/agy/vibe agents are never spawned by tests or by you while checking. Use `fleet spawn --fake` and `fleet-test-*` sockets.
- No pkill/killall. No `rm -rf $VAR`.
- One feature per run. If the request bundles several, do them in order and commit each.
