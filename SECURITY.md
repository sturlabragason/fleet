# Security policy

## Reporting a vulnerability

Report it privately through GitHub: **Security → Report a vulnerability** on
[sturlabragason/fleet](https://github.com/sturlabragason/fleet/security/advisories/new).
Please don't open a public issue for it.

Say what an attacker can do, the fleet version (`fleet version`) and the steps to
reproduce it. You get an answer within a week.

## Supported versions

Only the latest release gets fixes.

## Scope

fleet runs locally as your user and drives tmux and coding agents. Of most interest:

- command injection into tmux or a shell from agent names, prompts, profiles or paths
- reading or writing files outside what a tool allows (for example `fleet_brief file=`)
- an agent getting admiral tools over MCP or ACP, or acting on agents outside its squad
- `fleet web` authentication, host and origin checks
- secrets in logs or state, and state readable by other local users

An agent that can already run shell commands as your user is not a security boundary
fleet can enforce.
