## claude (Claude Code)

- Models: fleet's default here is `opus` (Opus 5.5); subagents inherit your model. Keep `opus` for code, design, review and debugging; `sonnet` is fine for read-only search. Never `haiku` for code, review or judgment. There are no Gemini models here.
- Delegation: the `Agent` tool with `Explore` (read-only search; it gets no CLAUDE.md or git status, so pass paths), `Plan`, `general-purpose`, or a `.claude/agents/*.md` agent (it takes `model` and `effort`).
- Effort: Opus 5.5 defaults to `medium`. For one hard step, start the prompt with `ultrathink`; "think step by step" changes nothing.
- Prompting: be direct and say why a rule matters; give exact paths, the symptom and the done check, not facts it can read. Never ask a model to print its internal reasoning (it is refused). In unattended runs, send status notes with the next tool call, not as a turn of their own.
