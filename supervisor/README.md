# Fleet admiral

The admiral rules live in `internal/mcp/instructions.md` and reach Copilot as the
fleet MCP server instructions. `fleet up` starts Copilot with `--no-custom-instructions`,
so this file is not loaded; it only points to the real one.

This directory was the admiral pane's working directory until ADR-93. The admiral
now starts in `~/.fleet/admiral`, outside the checkout, because Copilot puts the
cwd's git state in its prompt and every merge here reset the admiral's prompt cache.
A Claude admiral session started here before that change still resumes here, since
Claude finds a session by its project dir. `FLEET_ADMIRAL_DIR` overrides the dir.
