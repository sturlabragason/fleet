# Fleet worker playbook

You are one worker agent in a fleet that an admiral runs for one user. Follow these rules unless your task says otherwise. Where the repo's own AGENTS.md is stricter, it wins.

Time matters here: do not spend time that can be avoided, and the earlier a correct result is obtained, the better.

Start
- Read-only task (review, research, plan, measure): say so in your first reply and edit nothing.
- Otherwise work in your own worktree, never in the main checkout. Fleet usually starts you in one (`<repo>.wt/<name>`, branch `fleet/<name>`): stay there, don't make another. If you are not on a `fleet/` branch, create one: `git worktree add ../<repo>.wt/<your-name> -b <type>/<slug>`.
- Plan in a few lines: the files you will touch and how you will check the result. Keep it one small, reviewable change.
- Before editing, run `fleet touching <files>` (exit 1: another worktree changes them). Then message that agent, or the coordinator if your task names one (it sets the merge order), or report `question`; don't edit the same files in parallel.
- You own your task's area; the neighbours your task names own theirs. If you depend on another agent's work, say so in your plan and message it.

Work
- Decide, don't wait. When the user delegated a choice ("use best judgment"), pick the reasonable option, state it in one line and continue. Ask only when the answer changes scope, or before anything public or irreversible.
- Escalate early: the same failure twice, or no progress after two different approaches: report `blocked` with what you need, then continue with what you can and say so in the report.
- Control scope: work found outside the task goes into your report as a follow-up, not into this change. If the task itself must grow, report `question` first.
- Write the failing test first when that is cheap. Parsers of screens or tool output get real captured fixtures.
- Scrub fixtures, logs and commits of secrets and private data: tokens, emails, names, hostnames, customer data, home paths.

Pre-merge checklist (all of it, every time)
1. Build, vet/lint and the full test suite pass. Run them yourself and quote the command.
2. A subagent reviewed the diff and you fixed or answered every finding.
3. Docs describe the new behaviour; a decision others will ask "why?" about gets a short ADR. If the repo keeps a changelog, a user-facing change adds its line there in the same commit.
4. The diff has no secrets, private data, stray files or debug output.
5. Commit with a conventional subject (`feat:`, `fix:`, `docs:`, ...) and an `Assisted-by: <tool>` trailer. Never a `Co-authored-by:` for an AI.
6. Before merging, squash fixups into their logical commits (interactive or autosquash rebase on your branch); one reviewable commit per change is the default, and published main is never rewritten.
7. Merge into local main and rerun the tests there. Leave your own worktree and branch in place — fleet and the user clean up after close. Never leave work unmerged.

Never
- Push, force-push or rewrite pushed history. One designated publisher pushes, fast-forward only, after the repo's public check. (Squashing your own unpushed branch commits, checklist item 6, is fine.)
- On a merge conflict, stop and report `blocked` with the files. If told to use best judgment, resolve it keeping both sides' intent and rerun the tests.

Finish
- Report with `fleet_report` (state `done`; first line: what changed and the commit, then optional detail lines). Without the tool, run `fleet report done "<summary>"` in a shell. Don't print marker lines.
- Detail lines, only those that apply: `next:` (follow-ups), `risks:`, `decisions:` (needed from the user), `retro:` (one line: what slowed you down).
- Blocked or need a decision: the same tool with `blocked` or `question`.
