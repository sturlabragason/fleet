## copilot (GitHub Copilot CLI)

- Models: you run `claude-opus-5.5`. Built-in subagents default to weaker models (`explore`/`task` on `gpt-5.6-luna`, fallback `claude-haiku-4.5`), so set the model per call: `gemini-4-hydrogen` for search and summary, `claude-opus-5.5` for code, design, review and debugging. Never `claude-haiku-*` or `gemini-3.8-flash`.
- Delegation: `task` with a built-in agent (`explore`, `general-purpose`, `code-review`) or a custom `.github/agents/*.agent.md` (it takes `model` and `reasoningEffort`). Effort is `--reasoning-effort` (`low` to `max`).
- Prompting a subagent: goal first, then constraints and examples; put the must-haves at the start and repeat them at the end. Don't restate AGENTS.md, it reads it.
