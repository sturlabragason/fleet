# How fleet runs agent work

This page maps project-management practice onto fleet's own parts, so you can see why the worker
playbook and the admiral's instructions say what they say, and change them knowingly. Every
practice below uses a mechanism that is already built; none adds a meeting, a form or a tool.

Written 2026-11-05 (ADR-35). The injected text lives in `internal/playbook/worker.md` (prepended
to every spawn prompt) and `internal/mcp/instructions.md` (the admiral). Both stay short: the
playbook costs tokens on every spawn, so a rule earns a line there only if a worker would act
differently without it. The reasons live here instead.

## The mapping

| Practice | What it means for an agent fleet | Fleet mechanism | Where it is written |
|---|---|---|---|
| Time matters | Avoidable time is not spent, and an earlier correct result is better; it applies to the admiral and to every spawned agent | Near the top of both injected texts | Playbook intro, admiral intro |
| Clear ownership | One agent owns one area; the spawn prompt names the neighbours that own the rest | Spawn prompt ("don't touch X"), `fleet touching` | Playbook *Start*, admiral *Spawning work* |
| Who decides (RACI-light) | The user decides scope and anything public or irreversible; the target agent or the user decides access across teams. The admiral decides who works on what and asks the user before telling an agent to merge, push to a shared branch or apply infrastructure. A worker decides delegated choices. A coordinator decides the merge order for overlapping work | `fleet_answer` / `fleet_access_decide` confirmations, `question` reports | Playbook *Work*, admiral *Confirmation* |
| Definition of ready | A spawn prompt states the task, done-when, neighbours, dependencies and exceptions | Spawn prompt | Admiral *Spawning work* |
| Definition of done | Build, vet and tests pass, reviewed, documented, clean diff, committed, merged into local main (the worktree and branch stay; fleet and the user clean up, ADR-29) | The pre-merge checklist; `auto_close_done` checks the work tree (ADR-29) | Playbook *Pre-merge checklist* |
| Dependencies and hand-offs | A worker names what it waits for in its plan and messages the owner; a done report says what the next agent needs | `fleet_msg_send` (free within a team, ADR-34), `next:` report lines | Playbook *Start*, *Finish* |
| Standard status report | Headline first, then only the lines that apply: `next:`, `risks:`, `decisions:`, `retro:`. Blocked work uses the `blocked` state | `fleet_report` states and detail lines (ADR-20) | Playbook *Finish* |
| Escalate early | The same failure twice, or no progress after two different approaches: report `blocked` now, continue with what you can and say so | `fleet_report blocked`, which wakes an idle admiral | Playbook *Work* |
| RAID log | The admiral records risks, assumptions, issues and decisions as they surface, one line each | `fleet_note` with a `risk:`, `assumption:`, `issue:` or `decision:` prefix; the last 30 notes come back in `fleet_brief` after a reset | Admiral *Running the work* |
| WIP limits, small batches | One reviewable change per agent; finish and merge before starting more | `max_working` and the spawn queue (ADR-23) | Admiral *Running the work*, playbook *Start* |
| Merge order for overlapping work | The order comes from branch state, which every agent can read, so nobody waits for a reply: `fleet touching --json` names the other worktrees and their files, and the branch with fewer touched files goes first. A coordinator is named only for a collision the branch state cannot settle, and resolves it rather than granting permission; an agent that gets no answer within a few minutes merges on the branch-state order and says so. If they can't message each other yet, the admiral asks the user before teaming them or granting the pair | `fleet touching`, `fleet merge --dry-run` (ADR-48), teams (§5c), mail access (ADR-34) | Playbook *Start*, admiral *Running the work*, `.claude/skills/fleet-merge` |
| Merging back | Rebase onto main, check, merge into local main, check again there — the combination nobody has tested before the merge. A CHANGELOG.md or DESIGN.md conflict keeps both sides; any other conflict aborts and is a `blocked` report | `fleet merge` (ADR-48) | Playbook checklist item 7, `.claude/skills/fleet-merge` |
| Clean published history | Before merging, squash fixups into their logical commits (interactive or autosquash rebase on your branch); one reviewable commit per change is the default, and published main is never rewritten | Branch-level rebase before the merge into local main | Playbook checklist item 6 |
| Decision records | A choice others will ask "why?" about gets an ADR | DESIGN.md §14 | Playbook checklist item 3 |
| Scope control | Work found outside the task becomes a follow-up in the report; growing the task needs a `question` first | `next:` lines, `question` reports | Playbook *Work* |
| Retro | One line on what slowed the agent down. When the same line shows up from several agents, the admiral proposes a playbook change to the user | `retro:` report line, `fleet_note` | Playbook *Finish*, admiral *Running the work* |
| Changelog | A user-facing change adds its line in the same commit | `CHANGELOG.md`, `fleet changes`, `Alt-g` (ADR-30) | Playbook checklist item 3 |

## Why these and not more

Agents follow written rules literally and pay for every line on every spawn, so the practices
are cut to the part that changes an agent's next action:

- **Ownership and the coordinator** exist because two agents editing the same files end in a merge
  conflict, and one of them redoes its work. `fleet touching`
  finds the overlap; a named coordinator turns it into an order instead of a race. `fleet merge`
  then takes that order without asking anyone, and resolves the one conflict everybody has.
- **Escalate early** exists because an agent that is stuck keeps trying. Retries cost tokens and
  time; one line to the admiral costs neither, and the admiral or the user may already know the answer.
- **The report shape** lets the admiral relay a report to the user in one to three lines without
  peeking at the pane: blockers, decisions and risks first.
- **Scope control** keeps a change reviewable. An agent that absorbs a neighbouring fix makes a
  diff nobody asked for, often in a file another agent owns.
- **The RAID log** reuses `fleet_note` rather than adding a register: the notes already survive an
  admiral reset (§6.5), and a prefix tells the admiral which kind each line is.
- **The retro line** is one line, not a meeting. It is how the playbook itself improves.

Left out on purpose: estimates and burndown (agent tasks are short, and the queue is the plan),
stand-ups (the board is the stand-up), separate status documents (`fleet_brief` and the board
already are one) and a RACI matrix per task (the spawn prompt names the owner; the rest is the
same every time and lives in the table above).

## Changing the process

Change the injected text first, keep it to one line per rule, and update the table here. A repo
adds its own rules in `.fleet/worker.md`, a user in `~/.fleet/worker.md` (DESIGN §4, ADR-22).
