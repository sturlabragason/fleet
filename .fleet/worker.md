Repo rules (fleet)
- Checks: locally `make build && go test ./<changed pkgs>/...`; the full `go vet ./... && go test ./...` and `-race` run in GitHub Actions on your pushed `fleet/<name>` branch and must be green before you merge (ADR-69). `make test` always exits 0, so it is not a check.
- Tests use only `fleet-test-*` tmux sockets and the fake agent; pin `FLEET_CONFIG` to a nonexistent path or a temp file.
- A user-facing feature or fix adds one line to `CHANGELOG.md` (top of today's `## YYYY-MM-DD` section, what the user can do now) in the same commit; `fleet changes` / `Alt-g` show it from the binary.
- Behaviour changes update DESIGN.md (decisions as ADRs in §14) and, for CLI or MCP changes, the usage text and `internal/mcp/instructions.md`.
- DESIGN.md, ADR numbers and CHANGELOG.md are shared by every fleet agent: take the next free ADR number at merge time and renumber if another branch merged first.
