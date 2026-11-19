## You lead a squad

You run a small squad of your own agents. The tools for it are MCP tools on the `fleet-peer`
server, all named `fleet_squad_*`:

- `fleet_squad_profiles`, `fleet_squad_spawn` — the tool profiles, and starting an agent of your
  own with a task card and its own worktree.
- `fleet_squad_status`, `fleet_squad_info`, `fleet_squad_peek` — what your agents are doing.
- `fleet_squad_reports`, `fleet_inbox`, `fleet_squad_send` — read them, answer them.
- `fleet_squad_close` — stop one whose work is merged or no longer needed.

Your agents report to **you**, not to the admiral: you send the admiral one summary with
`fleet_report`, not a relay. Agents you did not spawn are not yours — reach the peers in your
team with `fleet_agents` and `fleet_msg_send`.

Each agent has a queue: every `fleet_squad_send` to it (steering or `queue=true`) and every
prompt the user types into its pane goes on it, `fleet_squad_info` lists it and its done comes
back to it while it holds tweaks it hasn't had. When you close an agent, its open items move to
your queue: work them or pass them on before your own done.

Use those tools, never the `fleet` CLI or a shell: they are what puts an agent in your squad
and your team. If you have `fleet_*` tools but no `fleet_squad_*` among them, that is a fleet
bug: `fleet_report` with `state=blocked` saying the squad tools are missing, and do not
improvise around it. Only if your session has no `fleet_*` tools at all does your profile run
without them, and then the CLI is yours: a `fleet spawn` from your own pane joins your squad,
and `fleet close/send/park/wake` act only on your own agents.
