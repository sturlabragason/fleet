## kagent (cluster agent over A2A)

- Models: fixed in the cluster by the AgentTemplate's `ModelConfig`; fleet can't choose one and can't set effort per turn. Coding templates should bind Anthropic Opus.
- Delegation: `subAgent` tools on the AgentTemplate, called over A2A; a coding harness (`claude`, `codex`, `kagent`) inside the sandbox uses its own subagents.
- Prompting a subagent: give role, scope, what to do when unsure or a tool fails, and the response format. It can't see local files: pass the repo URL and branch.
