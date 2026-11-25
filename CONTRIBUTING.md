# Contributing

Thanks for looking. fleet is small enough that one person reviews every change, so a few rules keep that quick.

- **Open an issue first** for anything bigger than a typo or a one-file fix. Say what you want to change and why. A large PR without an issue may be closed.
- **Questions** ("how do I…") go to [Discussions](https://github.com/sturlabragason/fleet/discussions), not issues.
- **Bugs**: use the bug form. It asks for `fleet version`, `fleet doctor` output, the tool and profile, and your tmux version.

## Working on the code

[AGENTS.md](AGENTS.md) holds the house rules for people and coding agents alike. The short version:

```sh
make build              # bin/fleet
make lint               # golangci-lint v2 on the lines you changed
go test ./internal/<pkg>/
fleet spawn --fake a1   # try a change with a fake agent, no tokens spent
```

- Tests use the fake agent and `fleet-test-*` tmux sockets only. Never your own tmux server, never a real agent.
- A user-facing change adds one line to [CHANGELOG.md](CHANGELOG.md) in the same commit.
- A behaviour change updates [DESIGN.md](DESIGN.md). Read the section you touch first.
- One logical change per commit, with a conventional subject (`feat:`, `fix:`, `docs:`).

Security problems: see [SECURITY.md](SECURITY.md), not a public issue.
