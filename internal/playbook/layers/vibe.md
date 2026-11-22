## vibe (Mistral Vibe)

- Models: use `mistral-large-4-0` (1M context). `mistral-medium-3.5` is deprecated since 2026-10-31. Fleet can't pass `--model` (headless only), so the model is `active_model` in `~/.vibe/config.toml`.
- Delegation: the `task` tool runs `explore` (read-only search) or a custom `~/.vibe/agents/*.toml` profile, which sets its own model and tool allowlist; subagents otherwise inherit `active_model`. There is no plan agent.
- Thinking comes from `models[].thinking` in config.toml; `--thinking` is headless only.
- Prompting: keep reasoning short and don't draft the answer inside it; lead replies with code or evidence. Never edit a file you haven't read; the closest AGENTS.md wins.
