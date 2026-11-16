Repo rules (fleet)
- Checks: locally `make build && make lint && go vet ./<changed pkgs>/...`, no test runs beyond the one test you are fixing; tests and `-race` of the packages your change reaches (`scripts/affected-pkgs.sh`, ADR-149) and golangci-lint on new lines (ADR-134) run in GitHub Actions on your pushed `fleet/<name>` branch and must be green before you merge (ADR-69); main runs the full suite. `make test` always exits 0, so it is not a check.
- Tests use only `fleet-test-*` tmux sockets and the fake agent; pin `FLEET_CONFIG` to a nonexistent path or a temp file.
- A user-facing feature or fix adds one line to `CHANGELOG.md` (top of today's `## YYYY-MM-DD` section, what the user can do now) in the same commit; `fleet changes` / `Alt-g` show it from the binary.
- New external tool or optional feature: use the companion contract or an existing §15 seam (DESIGN.md §15 Extensions, ADR-126), never a new mechanism; `internal/companion/arch_test.go` enforces the first.
- Behaviour changes update DESIGN.md (decisions as ADRs in §14) and, for CLI or MCP changes, the usage text and `internal/mcp/instructions.md`.
- DESIGN.md, ADR numbers and CHANGELOG.md are shared by every fleet agent: use the ADR number in your task prompt, or take one with `fleet adr claim --for <your-name>`; never pick one by reading §14. Run `scripts/check-public.sh` before committing.
