## agy (Gemini 4)

On agy, this section replaces any rule elsewhere in your prompt that puts subagents on Opus.
- Models: pin by exact model id; never use the `flash` tier (in subagent frontmatter and `/run-workflow`, `flash` is legacy 3.8 Flash and `pro` is Argon; verify in `/agents`).
- Roles: orchestrator `gemini-pro-orchestrator-neon` (Neon plans/reviews, Hydrogen executes; switch to `gemini-pro-orchestrator-g1-pro` Argon planner when Gemini quota is low); coding `gemini-4-hydrogen-high` (`xhigh` only after `high` fails); search/summary `gemini-4-hydrogen-low` (`medium` for multi-file synthesis); tests/docs `gemini-4-hydrogen-medium` (`high` on failures); reviewer on another model (`gemini-4-argon-medium` or `claude-opus-5-5-high`); hard bug `gemini-4-neon-xhigh` or `/boost`.
- Thinking levels (`low`, `medium`, `high`, `xhigh`): set internal reasoning budget; higher adds latency/tokens (`xhigh` only on Neon/Hydrogen). Use the lowest level that passes; avoid `xhigh` on simple prompts (causes overthinking).
- Async subagents: never end your turn while a subagent you started is still running. Fleet reads a turn that ends without a report as "stopped mid-task without a report". Wait on the subagent (or poll it) in the same turn, use its result, then report.

Prompting Gemini:
1. Never ask for visible reasoning ("think step by step", thinking tags); Gemini reasons internally — raise the effort level instead ("Think very hard before answering" for one hard prompt).
2. Be precise and direct; drop persuasive, emphatic or motivational framing.
3. Use one consistent delimiter style per prompt (XML tags or Markdown headings, never mix).
4. Put role, hard constraints and output format first, long context next, and the actual question last after an anchor ("Based on the information above...").
5. Expect terse answers by default; ask explicitly when you want explanations or detail.
6. Include 1–3 identical-format few-shot examples when output shape matters (too many overfit).
7. Spell out agentic policy: when to assume vs ask, reads low-risk / writes high-risk, retry limit, and change strategy on non-transient errors instead of repeating calls.
8. Give Hydrogen full context: it flags missing context instead of guessing, so pass file paths, done-check commands and allowed assumptions.
9. Put today's date in system prompts for time-sensitive searches.
10. Use native tools and structured output; never write JSON tool calls in prompt text (`MALFORMED_FUNCTION_CALL`).
11. API calls only: drop `temperature`, `top_p` and `top_k` (ignored now, future 400), and don't cap `max_output_tokens` low (thinking tokens count against it).