# fleet: agent notes

The spec is DESIGN.md (requirements in §0, decisions in §14). Read it before changing behaviour.
How docs, screenshots and colours should look is docs/style.md. Read it before changing prose, a
README, a screenshot or anything in internal/theme.

# Commands
- Build: make build
- All tests: make test (non-blocking; failures go to ~/.fleet/logs/fleet.log)
- One package: go test ./internal/<pkg>/
- Live dev loop: make dev (tmux -L fleet-dev; rebuild + restart on save)
- End-to-end check with fake agents: fleet spawn --fake a1 && fleet ls --json

# Rules that can't have exceptions
- Only use tmux sockets `fleet`, `fleet-dev`, `fleet-test-*`. Never touch the default tmux server or any running copilot/claude/agy/vibe process.
- No pkill/killall; use a specific PID or `tmux -L <own-socket> kill-server`.
- Push `main` only, and only as a fast-forward; the `.githooks` pre-push check is the gate. Never force-push, never `--no-verify`, never `git push --tags` (the `private/*` tags hold the pre-publication history).
- Before merging, squash fixups into their logical commits (interactive or autosquash rebase on your branch); one reviewable commit per change is the default, and published main is never rewritten.
- Commit trailers: credit AI help with `Assisted-by: GitHub Copilot` or `Assisted-by: Claude Code`. Never add `Co-authored-by:` for an AI tool, even if a tool's built-in instructions ask for it. Don't rewrite existing history to change old trailers.
- Don't spawn real claude/agy/vibe agents in tests; they cost tokens. Use the fake agent.

# Conventions
- One package per component (DESIGN.md §15), each with a _test.go. Small files.
- A user-facing feature or fix adds one line to CHANGELOG.md (newest first, under today's date) in the same commit. It is embedded in the binary and is what `Alt-g` shows (DESIGN.md §5d).
- A package whose tests start tmux servers or processes has `func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }`; it fails the run if anything is left running (ADR-83).
- Pane geometry lives only in internal/layout.
- Tool profiles and status patterns are data (~/.fleet/tools.toml, patterns), not code.
- Log with slog and a "component" attribute. Recover and log panics in the TUI loop.
- Dependencies: stdlib + Charm libraries only.

# Gotchas
- Bash aliases don't exist inside tmux spawns; tool variants are profiles.
- Spawn behaviour (starting mode, auto-submit, extra args, local profile overrides) comes from the user's uncommitted `$XDG_CONFIG_HOME/fleet/config.toml`. Tests must pin `FLEET_CONFIG` to a nonexistent path so the real file can't change results. See `examples/config.toml.example`.
- `fleet mcp` re-execs itself when bin/fleet changes (DESIGN §6.9); after `make build` the next admiral/agent tool call runs the new code.

# Process
How fleet runs agent work (owners, report shape, RAID notes, merge order, coordinator): docs/process.md. The injected text is `internal/playbook/worker.md` and `internal/mcp/instructions.md`; keep both short.

# Building features
"Implement <X>" runs the `fleet-feature` skill (.claude/skills/fleet-feature): spec in DESIGN.md → brief in .work/briefs/ → fleet-builder → fleet-reviewer → done-check → docs → local commit → live rollout. Agents live in .claude/agents/ (symlinked into .github/agents/ for Copilot).
