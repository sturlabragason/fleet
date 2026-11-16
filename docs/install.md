# Install

How to install fleet on Linux, macOS or Windows, keep it up to date and check where a binary came from.

## Platforms

| Platform | State |
|---|---|
| Linux | Tested. Everything works. |
| macOS | Built and released, not tested. |
| Windows | Agents run over ACP only: no tmux, so no overview or wall (ADR-117). |

## What you need

- tmux 3.3 or newer (Linux and macOS). Developed and tested with tmux 3.8.
- The agent CLIs you want to run (`copilot`, `claude`, `agy`, `vibe`) on your login `PATH`. Fake agents need none of them.
- Go 1.26.10 or newer, only to build from source (see `go.mod`).

`fleet doctor` checks these: the tmux version, a UTF-8 locale, `~/.local/bin` on `PATH`, your config files and each profile's command, one line each with the fix.

## Linux and macOS

```sh
curl -fsSL https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh | sh
```

The script downloads the latest release's binary for your OS and architecture (amd64 or arm64), checks it against the release's `SHA256SUMS`, refuses on a mismatch, and puts it in `~/.local/bin`. It needs no sudo and doesn't touch your shell's rc files; if the directory isn't on your `PATH` it says what to add.

| Variable or flag | Does |
|---|---|
| `FLEET_VERSION=v1.3.0` | Pins a release |
| `FLEET_INSTALL_DIR` | Installs into another directory |
| `FLEET_SKIP_ATTEST=1` | Installs a release made before attestations on the SHA-256 check alone |
| `--dry-run` | Shows what it would do |

To read the script before you run it:

```sh
curl -fsSLO https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh
less install.sh
sh install.sh
```

## Windows

In PowerShell, no admin needed:

```powershell
irm https://raw.githubusercontent.com/sturlabragason/fleet/main/install.ps1 | iex
```

It puts `fleet.exe` in `%LOCALAPPDATA%\Programs\fleet`, checks it against `SHA256SUMS` and adds the directory to your user PATH (open a new terminal afterwards). With [Scoop](https://scoop.sh) instead:

```powershell
scoop bucket add fleet https://github.com/sturlabragason/fleet
scoop install fleet/fleet
```

Or download `fleet-windows-amd64.exe` (or arm64) from [Releases](https://github.com/sturlabragason/fleet/releases), rename it `fleet.exe` and put it on your PATH.

On Windows every agent runs under the ACP driver, and you drive them with `fleet spawn`, `send`, `peek`, `ls`, `close` and the admiral's MCP tools. For Claude agents install [Node.js](https://nodejs.org), then Claude Code, and log in once:

```powershell
npm install -g @anthropic-ai/claude-code
claude          # log in, then exit
fleet spawn w1 claude C:\path\to\project "your task"
```

fleet starts the Claude ACP adapter with `npx`, which downloads it on first use.

## Other ways

With Go:

```sh
go install github.com/sturlabragason/fleet/cmd/fleet@latest
```

From a checkout:

```sh
make build      # bin/fleet and bin/fakeagent
make install    # symlink bin/fleet to ~/.local/bin/fleet
```

Or download a binary from [Releases](https://github.com/sturlabragason/fleet/releases), check it against `SHA256SUMS` and put it on your `PATH`. `fleet version` says which release you have.

A binary install is all of fleet. A checkout adds only what is for working on fleet itself: `supervisor/`, the skills in `.claude/skills` and the agents in `.claude/agents`. Example config is in [examples/](../examples): [`tools.toml`](../examples/tools.toml) and [`config.toml.example`](../examples/config.toml.example).

## Updates

A release install keeps itself up to date. Every 6 hours fleet checks for a new release, verifies it the same way the installer does, and swaps it in when no agent is working.

| Command or setting | Does |
|---|---|
| `fleet update` | Shows the auto-update state |
| `fleet update --check` | Asks GitHub for the latest release |
| `fleet update --now` | Installs it at once |
| `[update] auto = false` in `~/.config/fleet/config.toml` | Turns auto-update off |

Running the install script again also upgrades.

## Checking a download

Releases carry GitHub [artifact attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) that tie each binary and `SHA256SUMS` to the workflow run that built them. If [`gh`](https://cli.github.com) is installed and logged in, the installer checks the binary's attestation too and refuses on a failure; otherwise it says that provenance was not checked. To check a download yourself:

```sh
gh attestation verify fleet-linux-amd64 -R sturlabragason/fleet
```

## The admiral

The admiral needs no setup: `fleet up` passes the MCP server (`fleet mcp`) to it on the command line, and its rules travel as the server's instructions. Its working directory is `~/.fleet/admiral`, outside any git checkout so repo churn doesn't reset its prompt cache (ADR-93); `FLEET_ADMIRAL_DIR` overrides it. More in [admiral.md](admiral.md).
