## pi (Pi coding agent)

- Models: provider-agnostic; `pi --list-models` shows what is configured. Without `--model` you run Pi's saved default, which may be a fast model: for code, switch to the strongest configured coding model.
- Delegation: Pi has no built-in subagents or plan mode by design. Work single-agent; as a squad leader use `fleet_squad_*`. A subagent extension, if installed, takes an explicit `provider/id` per role.
- Thinking: `--thinking <off|minimal|low|medium|high|xhigh|max>` or `--model provider/id:<level>`.
- Prompting: AGENTS.md and CLAUDE.md load on start and skills load on demand, so keep prompts to paths, constraints and the verify command.
