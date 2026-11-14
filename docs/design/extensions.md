# Extensions: how fleet hosts integrations

Design review, written 2026-11-14 on branch `fleet/arch-extensions`. It
maps what exists today, names the problems, compares how other tools do extensions, and
proposes one extension model with a migration path. The ADR at the end is ADR-126 in DESIGN.md §14.
M1 to M10 are built (section 5); M11 waits for a user.

Line references are to commit `4034546` (local main on 2026-11-14). Counts were measured on that
commit: MCP tool counts by marshalling `toolDefs`, `agentToolDefs` and `squadToolDefs` in a
throwaway test, history counts with `git grep` at each tag, imports with `go list`.

## Summary

Fleet does not need a plugin system. It needs one written contract for the kinds of
integration it already has:

1. **Capabilities an agent uses** (a browser, publishing a page, an issue tracker) belong to the
   agent tool's own extension model: skills and MCP servers. Fleet's only job is to let a
   profile keep a server switched on (`keep_mcp`, which exists). No fleet code.
2. **Companion CLIs fleet reads or writes** (cass now; task tracker and time tracker next; also
   gh, git, rsync and ssh, the summary model CLI, clipboard and terminal) become one adapter
   file per tool on a shared helper: lookup, an `off` switch, a version floor, JSON only, a
   timeout, a one-line install hint, a `fleet doctor` line, and fixture tests through a fake
   runner. This is ADR-119's cass adapter made general.
3. **Agent tools** (copilot, claude, agy, vibe) stay profiles as data, with one new key,
   `family`, that selects the small amount of Go each tool family needs (trust store, session
   files, spend, admiral launch). Today that Go is reached by comparing the status-pattern name
   or the command name in nine places.
4. **overlay features** keep the build-tag overlay, but through a typed registry that public CI
   compiles against an example extension, so a public refactor cannot silently break the overlay.

Out-of-process plugins on PATH (`fleet-<name>`, as git and kubectl do) are the right model for
third parties later; the cheap prerequisite is a schema version on fleet's JSON outputs.
task tracker is the first integration to build on the companion contract.

## 1. What exists today

### 1.1 Packages and where integrations sit

`internal/fleet` is the hub: 47 non-test files importing 23 internal packages. `internal/mcp`
imports 20 and `cmd/fleet` 33. Integrations hang off all three.

| Integration | Kind | How it is wired | When the tool is missing |
|---|---|---|---|
| tmux | core substrate | `internal/tmux` runs `tmux -L <socket>` (ADR-1); found on PATH (`cmd/fleet/up.go:481`, `cmd/fleet/monitors.go:111`) | `fleet doctor` fails (`cmd/fleet/doctor.go:124-129`) |
| copilot, claude, agy, vibe, fake | agent tools | built-in profiles as TOML inside Go (`internal/tools/tools.go:290-368`) merged with `~/.fleet/tools.toml` and `[profile.<name>]` in config.toml (`tools.go:382-393`); a 34-field `Profile` struct (`tools.go:19`) | spawn fails with "not found" (`internal/fleet/acp.go:189`, `internal/tools/native.go:64`); doctor checks user-defined profiles and the admiral's (`doctor.go:297-321`) |
| ACP drivers | agent protocol | `internal/acp` (client) and `internal/acphost` (host in the pane), ADR-74, ADR-76 | the profile runs under the tmux driver |
| status patterns | data | built-in TOML plus `~/.fleet/patterns.toml` (`internal/status/status.go:149-154`), ADR-7 | n/a |
| tool hooks and fleet-peer | fleet extends the agent tool | a Claude-format plugin dir per agent with `hooks.json` and `.mcp.json` (`internal/tools/plugin.go:29-75`) that calls back into `fleet hook` (`cmd/fleet/watch.go:230`), ADR-49, ADR-99 | n/a |
| the user's global MCP servers | agent tool config | switched off for workers (ADR-91, `internal/tools/usermcp.go:47`); `keep_mcp` keeps named ones (`internal/prefs/prefs.go:341`, used at `internal/fleet/fleet.go:480`) | n/a |
| trust dialogs | tool-private files | one Go file per tool writes its trust store (`internal/trust/claude.go`, `copilot.go`, `vibe.go`), ADR-47 | `ErrUnsupported`; the dialog is answered on screen |
| session discovery | tool-private files | `internal/sessions` parses Copilot and Claude session folders (`sessions.go:240`, `:304`) | an empty list |
| spend | tool-private files | `internal/spend` switches on the profile's pattern name (`internal/spend/cache.go:159`), ADR-46 | no spend column |
| cass | companion CLI | one adapter file, `internal/recall/cass.go`: `$FLEET_CASS`, `off`, else PATH (`:37-50`), timeout and first stderr line (`:53-67`), `nice` and `ionice` on request (`:148-168`), ADR-119 | a hint where it is asked for (`recall.go:309`, `day.go:106`); the rest of the recall answer still arrives |
| summary model | companion CLI | `copilot -p` with `[summary]` config (`internal/summary/model.go:63-70`, `internal/prefs/summary.go:11`), ADR-40 | a "not installed" error; the caller fails open |
| gh | companion CLI | attestation check on self-update (`internal/update/update.go:591-600`), ADR-103 | skipped without a word |
| rsync, ssh | companion CLI | `fleet exec` argv in `internal/rexec/rexec.go:49-52`, `[exec]` config (`internal/prefs/exec.go:9`), ADR-107 | the command errors |
| clipboard | companion CLI | first of pbcopy, wl-copy, xclip, xsel on PATH (`cmd/fleet/clipboard.go:14-29`), ADR-28 | no copy |
| terminal | companion CLI | `$FLEET_TERMINAL`, `$TERMINAL`, then a list (`cmd/fleet/monitors.go:191`), ADR-108 | the command errors |
| git | core companion | `exec.Command("git", …)` in `internal/adr/design.go:36`, `internal/whatsnew/commits.go:49`, `cmd/fleet/exec.go:45` and `internal/worktree` | worktree falls back to the dir (ADR-39) |
| artifact publishing, browser automation | agent capability | not in fleet: both are skills the agent tool loads from the user's skill dirs. The `artifact-ext-only` audit (2026-11-14) found no fleet code for either | n/a |
| overlay | build-tag overlay | package-level hook variables in `cmd/fleet/ext.go:12-23`, `internal/fleet/ext.go:5-11`, `internal/mcp/ext.go:6-17`, `internal/ui/ext.go:7-25`, plus `keys` and `update` (DESIGN.md:1225) | a default build leaves them nil |

### 1.2 Configuration surface

Fleet reads three TOML files in `~/.fleet` (`tools.toml`, `patterns.toml`, `routes.toml`) and
one local `config.toml` with nine section kinds (`examples/config.toml.example`: `[defaults]`,
`[profile.*]`, `[summary]`, `[memory]`, `[exec]`, `[admiral]`, `[usage]`, `[squad]`,
`[update]`). 28 distinct `FLEET_*` variable names appear in non-test Go code, some of them
only passed between fleet's own processes. Integration settings are spread across all of
these: cass only through `FLEET_CASS`, the summary CLI in `[summary]`, the remote host in
`[exec]`, the terminal in `FLEET_TERMINAL`.

### 1.3 The admiral's tool surface

At `4034546` the admiral has 40 MCP tools whose definitions marshal to 27,266 bytes (roughly 7k
tokens, part of every admiral request). Agents get 9 (3,748 bytes) and squad leaders 8 more
(6,098 bytes). Tool definitions across `internal/mcp` grew from 40 entries at v1.0.0 to 49 at
v1.23.0, three days later. `CLIParity` (`internal/mcp/parity.go:11`) makes every new CLI command
either gain an admiral tool or state why not, and a test enforces it. That keeps the admiral
complete, and it also pushes every integration command towards becoming a tool.

## 2. Problems

**P1. The tool family is inferred from other fields.** `Profile.Patterns` names a status
pattern set, but code reads it as "which tool is this": `cmd/fleet/up.go:79`, `:89`, `:128`,
`cmd/fleet/admiral_profile.go:111-114`, `:168`, `internal/fleet/info.go:227` and
`internal/spend/cache.go:159`. `internal/fleet/acp.go:212` compares the command's base name
instead, and `internal/tools/plugin.go:108` reads `ACPMeta`. A user profile for a new tool that
reuses Claude's patterns silently gets Claude's admiral launch, spend parser and context fill;
one that brings its own pattern set gets none of them. "Tool-specific things are data"
(docs/architecture.md) holds for argv and patterns, not for these.

**P2. Tool-private file formats are parsed in several places.** The `cwd:` line of a Copilot
session's `workspace.yaml` has three readers: `internal/fleet/adopt.go:190`,
`internal/recall/cass.go:188` and `internal/sessions/sessions.go:469`. Copilot and Claude
session files are parsed in `internal/sessions`, `internal/spend` and `internal/supervisor`
(`Fill`, called at `internal/fleet/info.go:228`). ADR-119 rejected this for recall ("one parser
per tool per format change, which is what cass maintains"), but the older readers remain.

**P3. Optional tools fail in different ways.** cass names itself and says how to install it
where it is asked for, and the rest of the answer still arrives. The summary CLI returns an
error. gh is skipped without a word, so a self-update on a machine without gh is checked by
SHA-256 only and nothing says so (`internal/update/update.go:591-593`), while ADR-103's
installer does say it. rsync, ssh and the terminal fail when the command runs. `fleet doctor`
checks tmux, locale, PATH, board, watcher, orphans, config and profiles
(`cmd/fleet/doctor.go:105-120`) and none of the optional tools, so there is no one place that
shows which features are off.

**P4. Every adapter rebuilds the same plumbing.** Lookup with an env override, `off`, PATH, a
timeout, stderr capture and lenient JSON decoding are written out in `recall/cass.go:37-67`,
and in part again in `summary/model.go:63-80`, `update/update.go:591-600`,
`cmd/fleet/clipboard.go:14` and `cmd/fleet/monitors.go:191`. task tracker and time tracker
would be the sixth and seventh copies.

**P5. Integration config has no home.** See 1.2: an env var for one tool, a section for
another, nothing for a third. A task tracker adapter needs at least a data dir and an on/off
switch, and has nowhere obvious to put them.

**P6. The overlay seams are untested in public and single-slot.** No public test sets any `ext*`
hook, so a broken seam shows up only when the private build runs. The hooks are read at 34
call sites in 9 files. Each hook is one variable, so two extensions would overwrite each other.
The overlay is allowed to use package internals (DESIGN.md:1225), so any internal rename can
break it.

**P7. MCP tool growth.** See 1.3. Each integration that adds an admiral tool adds schema to
every admiral request and one more choice for the model. tracker's plan adds
`fleet_thread`; task tracker or time tracker verbs would add more.

**P8. Fleet's own JSON outputs are unversioned.** `board.json`, `fleet ls --json` and the
`fleet web` event stream carry no schema version; the only version in `internal/board/board.go`
is the per-agent `fleet_version` (`board.go:125-127`). ADR-62 makes readers keep unknown keys,
which covers additive change, not a rename. An out-of-process consumer (a PATH plugin, a
separate UI, a script) cannot tell which shape it is reading.

**P9. The architecture doc lags.** docs/architecture.md lists 22 internal packages; `internal/`
has 42, including most of the integration packages above (`recall`, `routes`, `spend`,
`usage`, `trust`, `rexec`, `acp`, `acphost`, `summary`, `web`, `watch`, `update`). A reader
looking for the extension points does not find them there.

What works and should be kept: profiles and patterns as data, cass behind one file with a
hint, the `keep_mcp` pass-through, fail-soft as a rule (DESIGN.md §16 rule 4), and not running
indexers or daemons unasked (ADR-119 (d)).

## 3. How other tools do it

From the projects' documentation as remembered on 2026-11-14, not re-checked against source.

| Model | Mechanism | Strengths | Costs for a Go CLI with a stdlib-plus-Charm rule |
|---|---|---|---|
| git, kubectl, gh, cargo | a `<tool>-<name>` executable on PATH runs as `<tool> <name>`; kubectl adds `kubectl plugin list` and krew an index; gh installs from repos named `gh-*` | any language, crash isolation, nothing to keep stable beyond argv, env and stdout | no hooks into the host's loop or UI; discovery is a PATH scan; versioning is ad hoc |
| Docker CLI plugins | `docker-<name>` in known dirs, plus a metadata subcommand that prints JSON (schema version, vendor, version, short description) | the PATH model plus a cheap handshake: the host can list, describe and version-check a plugin without running it for real | as above |
| HashiCorp go-plugin (Terraform, Vault, Packer) | the plugin is a subprocess; host and plugin handshake (magic cookie, protocol version), then talk gRPC or net/rpc; Terraform resolves providers from a registry with version constraints and a lock file | typed, versioned contract; crash isolation | gRPC and protobuf break the dependency rule; heavy for a handful of adapters; plugin authors need an SDK |
| Go `plugin` package | load a `.so` at runtime | in-process, typed | same Go toolchain and module versions on both sides, cgo, Linux and macOS only; no static binary (ADR-10). Rejected |
| WebAssembly (wazero and similar) | sandboxed modules in-process | portable, sandboxed | a new dependency, and the modules still need a host API. Not now |
| Neovim | Lua plugins in-process; remote plugins over msgpack-RPC on stdio; lazy loading on events; `:checkhealth` runs each plugin's own health check | a health check per plugin; load only when used | in-process Lua needs an embedded runtime fleet does not have |
| VS Code | a manifest declares contribution points and activation events; extensions run in a separate extension host; `engines.vscode` pins the host range | a static declaration of what a plugin adds; lazy activation; a host-version range | a large host API to keep stable |
| MCP servers | a server exposes tools to a model over stdio or HTTP; the agent tool loads them | agent tools already host them; any language | every tool schema costs context on every request; many servers mean many tools to choose from |
| Unix pipes with JSON | `tracker export`, `timetrack export`, `cass … --json`, `gh … --json`, jq between them | the contract is the other tool's documented output; nothing to install in fleet | no push events, so fleet polls or is called; formats drift between versions |
| task tracker hooks | executables in the hooks dir get the task as JSON on stdin and return it on stdout (`on-add`, `on-modify`, …) | a small, stable contract owned by the tool | runs inside the other tool, on its terms |

What carries over to fleet:

- **The Unix-pipe model is already fleet's model** (ADR-119) and fits the dependency rule: the
  other tool's `--json` or `export` is the contract.
- **A health check per integration** (Neovim) maps onto `fleet doctor`.
- **A version handshake** (go-plugin, Docker metadata, `engines`) is cheap as `<tool> --version`
  against a floor, cached by binary mtime.
- **A static declaration of what an extension adds** (VS Code) is what the overlay seams lack.
- **MCP is the agent's extension model, not fleet's.** Fleet should not proxy a browser or a
  publishing tool through its own MCP server when the agent tool loads that server directly.
- **PATH plugins** are the right shape for third parties, once fleet's JSON is versioned.
- **In-process dynamic loading** (Go `plugin`, go-plugin, WASM) costs more than it returns for a
  single static binary with a handful of adapters.

## 4. Recommendation

### 4.1 Where an integration goes

| If the integration… | It is | Fleet code |
|---|---|---|
| is something the agent does (browse, publish a page, file a ticket) | a skill or MCP server in the agent tool | none; `keep_mcp` per profile when a worker needs a user MCP server |
| is an agent CLI fleet drives | a profile (data) with a `family` | only when a new family needs new tool-private code |
| is a CLI fleet reads or writes (cass, task, timetrack, gh, git) | a companion adapter | one file on the shared helper |
| needs fleet's loop, UI or admiral hooks and is in the overlay | a registered extension in the overlay | the overlay |
| is a separate program that wants fleet's state | a consumer of fleet's versioned JSON, optionally `fleet-<name>` on PATH | none in fleet (planned, later) |

### 4.2 The companion contract

One small package (proposed name `internal/companion`, stdlib only) and one adapter file per
tool in the package that uses it, as `recall/cass.go` is today. Sketch:

```go
// Spec is what an adapter declares about the tool it calls.
type Spec struct {
	Name    string   // "cass", "task", "timetrack"
	Env     string   // override: a path, or "off" (FLEET_CASS, FLEET_TASK)
	Version []string // argv that prints the version, e.g. {"--version"}
	Min     string   // oldest version the adapter's fixtures cover
	Feature string   // what is off without it, for doctor: "session search"
	Hint    string   // one line on how to install it; fleet never installs
}

var ErrMissing, ErrOff, ErrTooOld error

// Register adds a Spec to the list fleet doctor reports on.
func Register(s Spec) Spec

// Find resolves the binary: env, then [integrations.<name>] path, then PATH.
// The version probe is cached by path and mtime.
func Find(s Spec) (Tool, error)

// Run runs the tool with a timeout and no stdin; the error carries the
// first stderr line. Low CPU and IO priority on request.
func (t Tool) Run(ctx context.Context, o Opts, args ...string) ([]byte, error)

// JSON runs the tool and decodes stdout into v, ignoring unknown fields.
func (t Tool) JSON(ctx context.Context, v any, args ...string) error

// Runner is swapped by tests to serve fixtures instead of exec.
var Runner func(ctx context.Context, path string, args []string, o Opts) ([]byte, error)
```

Rules for an adapter:

1. **Read the tool's documented machine output only:** `--json`, `export`. No scraping of
   human output, and no reading the tool's private files when it has an export (P2).
2. **Write through the tool's own CLI** (`tracker add`, `task <uuid> modify`), never into its data
   files, and idempotently: fleet tags what it owns (a user-defined attribute such as
   `fleet_agent`) and matches on it.
3. **Never block a spawn, the TUI loop or the watcher.** Calls happen on request or on a user
   event; each has a timeout; an indexer or other expensive call runs only when asked
   (ADR-119 (d)).
4. **One file per tool.** Swapping the tool (task tracker for beads, cass for something else) is
   replacing that file.

Failure modes, the same for every adapter:

| Case | What the user sees | Where |
|---|---|---|
| not installed | the feature's section says it is off, with the hint; everything else answers | where it is asked for, and `fleet doctor` at info level |
| `off` | nothing; doctor says "off by config" | doctor |
| older than `Min` | treated as missing; the hint names the version | where it is asked for, doctor |
| times out or exits non-zero | that section shows the first stderr line; the rest answers | where it is asked for, `fleet.log` with a `component` |
| unreadable output | the same, with "unreadable output" | as above |
| a write fails | the admiral or the CLI gets the error; fleet's own state is unchanged | the caller |

`fleet doctor` gains an "optional tools" block listing every registered Spec with its path,
version and feature, at `ok` or `info`, never `fail`. gh joins it, so a self-update without gh
says that provenance was not checked (P3).

### 4.3 Configuration

One section per companion in the local config.toml, read through `internal/prefs` and shown by
`fleet config explain`:

```toml
[integrations.cass]
path = "auto"            # auto (PATH) | off | /path/to/cass

[integrations.tracker]
path = "auto"
data = "~/.tracker"         # passed as TRACKER_DATA; tests point it at a temp dir
```

Precedence: environment variable, then config, then PATH. `FLEET_CASS` keeps working.
`[summary]` and `[exec]` stay where they are; their command lookups move onto `Find` so doctor
sees them.

### 4.4 Agent tools: an explicit family

Add a `family` profile key (`copilot`, `claude`, `agy`, `vibe`, `fake`), defaulting to the
`patterns` value so no existing profile changes. Gather the tool-private Go behind one table in
`internal/tools`. Sketch:

```go
type Family struct {
	Trust    func(dir string) (trust.Result, error)     // ADR-47
	Sessions sessions.Reader                           // adopt, recall cwd
	Spend    spend.Kind                                // ADR-46
	Context  func(sessionID string) (used, max int, ok bool)
	Admiral  AdmiralLaunch                             // MCP config, rules, env
	Events   bool                                      // copilotEvents today
}
```

The nine call sites in P1 ask `p.Family()` instead of comparing strings. A new tool that
behaves like an existing one is then a profile with `family = "claude"`; a new family is one Go
file plus fixtures. Over time, prefer the tool's protocol (ACP usage and session events) and
cass over parsing its files, and delete readers as they fall out of use. (The `sessions` and
`spend` packages import `tools` today, so the table may need to live in `internal/fleet` to
avoid an import cycle; that is an implementation detail for M8.)

### 4.5 Overlay

Keep the build-tag overlay: a static binary with no runtime loading is the right trade for one
downstream extension. Change the seams from single variables to a registry. Sketch:

```go
// Extension declares everything an overlay adds, in one value.
type Extension struct {
	Name     string
	Commands map[string]Command // cmd/fleet
	Tools    []mcp.Tool         // admiral tools, counted against the budget
	Gate     func(...) error
	OnOpen   func(*fleet.Fleet)
	Wake     func(*fleet.Fleet)
	UI       ui.Ext
}
func Register(e Extension)
```

Public CI builds and tests a small example extension under a test-only tag
(`go vet -tags extexample ./...` plus one test per seam), so a public refactor that breaks a
seam fails on the public side first. overlay features that need a companion tool use the public companion contract; pro
decides only whether a feature is compiled in, not how a tool is reached. The task tracker
adapter itself is in the overlay (section 7, decision 1): an adapter file in the overlay on the public
helper.

### 4.6 The admiral's tool budget

- No integration adds an admiral tool by default. Its data reaches the admiral through tools
  that exist (`fleet_recall`, `fleet_brief`, the day digest), as cass does.
- Hold the admiral at 40 tools, with a test: an extension's tools count against the same
  number, and adding one means merging or retiring another.
- When an integration needs admiral verbs (park a thread, let it go, start a timer), group them
  under one tool per integration with an `action` enum, as `fleet_route list|show|set|rm` does
  (ADR-120), not one tool per verb.
- Add a class to `CLIParity` for integration commands, so a new `fleet <integration>` command
  can point at that grouped tool.

### 4.7 Versioning

| Contract | Check | On mismatch |
|---|---|---|
| companion tool output | `Min` per adapter, probed once per binary; fixtures per tested version in `testdata/<tool>/<version>/` | treated as missing, with the hint |
| fleet's JSON (`board.json`, `ls --json`, web events) | a top-level `schema` integer; additive changes keep it, renames and removals bump it | a consumer refuses a schema it does not know |
| overlay seams | Go types, compiled by public CI against the example extension | public CI fails |
| PATH plugins (later) | `fleet-<name> fleet-metadata` prints JSON with `schema` and the fleet range it supports, as Docker does | fleet does not list or run it |

### 4.8 Testing

- Adapter unit tests swap `companion.Runner` and serve output captured from the real tool
  (`testdata/<tool>/<version>/*.json`), the same approach as the status fixtures (ADR-42).
- Command-level tests point `FLEET_<TOOL>` at a fake script in `testdata`, with `FLEET_CONFIG`
  pinned to a nonexistent path as today.
- Missing and `off` are table cases in every adapter test; the doctor block has its own.
- An opt-in contract test per tool (like `make smoke`) runs the real binary against a temp data
  dir (`TRACKER_DATA`, `time trackerDB`, a temp cass index). It never touches the user's data.

## 5. Migration, smallest first

| Step | Change | Size | Behaviour change |
|---|---|---|---|
| M1 | one `workspace.yaml` cwd reader in `internal/sessions`, used by adopt and recall (P2) | about 30 lines removed | none |
| M2 | `internal/companion` with `Register`; port `recall/cass.go:37-67` onto it; `FLEET_CASS` kept; docs/architecture.md package table brought up to date (P9) | one package, one file changed | none |
| M3 | `fleet doctor` "optional tools" block; gh's silent skip says so (P3) | doctor and update | new info lines |
| M4 | port the summary CLI, gh, rsync and ssh, clipboard and terminal lookups onto `Find` (P4) | one small commit each | none |
| M5 | `[integrations.<name>]` in prefs, `examples/config.toml.example`, `fleet config explain` (P5) | prefs | new optional keys |
| M6 | task tracker adapter on the contract (tracker slice 2), then time tracker read-only | new files | a new optional feature |
| M7 | `schema` on `board.json`, `ls --json` and web events (P8) | small | an additive key |
| M8 | `family` profile key and the family table; the nine call sites in P1 | medium | none for existing profiles |
| M9 | overlay registry and the public example extension in CI (P6) | medium; needs a matching overlay change | none in a default build |
| M10 | admiral tool budget test and the `CLIParity` class (P7) | small | none |
| M11 | `fleet-<name>` PATH plugins with a metadata handshake | only when someone needs it | new |

M1 to M5 are one commit each and unblock M6. M7 to M10 are independent of M6 and of each other.

### Upcoming integrations on this model

| Integration | Tier | Notes |
|---|---|---|
| task tracker | overlay companion (M6) | reads `tracker export`, writes `tracker add` and `task <uuid> modify` with fleet attributes; `TRACKER_DATA` from config; the first new adapter |
| time tracker | companion, read-only | task tracker's own on-modify hook drives it; fleet reads `timetrack export` for display only |
| cass | companion (M2) | already one file; moves onto the helper |
| syncall, a sync tool | the user's own setup | they sync into task tracker on the user's schedule; fleet never runs them |
| artifact publishing, browser automation | agent capability | skills in the agent tool; `keep_mcp` if a worker needs a browser MCP server |
| new agent CLIs | profile plus `family` (M8) | data only, when the family exists |
| a separate UI or dashboard | JSON consumer (M7) | reads `ls --json` or web events, acts through the fleet CLI |

## 6. The ADR

**ADR-126: integrations are adapters over other tools' CLIs, not plugins fleet loads** (§3, §15
Extensions, §16; builds on ADR-10, ADR-91, ADR-119).

**Context.** Fleet is gaining integrations (cass, task tracker and time tracker for tracker,
more agent CLIs) and has a downstream overlay. Each integration is wired its own way today: an
env var for cass, a config section for the summary CLI, a silent skip for gh. Tool-family code
is chosen by comparing the status-pattern name or command in nine places. overlay hooks are single
variables no public test exercises. The admiral's tool count grows with each feature.

**Decision.** (a) Capabilities an agent uses are the agent tool's skills and MCP servers; fleet
passes them through with `keep_mcp` and adds no code. (b) A CLI fleet reads or writes is a
companion: one adapter file on `internal/companion`, resolved from `FLEET_<NAME>`, then
`[integrations.<name>]`, then PATH, with `off`, a version floor, JSON-only output, a timeout, a
one-line install hint and a `fleet doctor` line. A missing companion turns off its feature and
nothing else. (c) Agent tools stay profiles; a `family` key selects the tool-private Go. (d)
the overlay stays a build-tag overlay, through a registry that public CI compiles against an example
extension. (e) No integration adds an admiral tool by default; verbs are grouped per
integration under one tool with an action. (f) Fleet's JSON outputs carry a `schema` integer.
(g) PATH plugins (`fleet-<name>`) are the model for third parties, when there is one.

**Rejected.** Go's `plugin` package (cgo, the same toolchain on both sides, no static binary).
HashiCorp go-plugin or any gRPC plugin protocol (dependencies, and an SDK for a handful of
in-tree adapters). WASM (a runtime dependency with no user yet). Proxying agent capabilities
through fleet's MCP server (the agent tool already hosts MCP, and it would add schema to every
admiral request). Reading tools' private files where they offer an export (ADR-119's reason).

**Trade-offs.** Adapters are in-tree, so a new companion needs a fleet release. JSON exports
change between tool versions, so each adapter carries fixtures per version and a floor. the overlay
registry is a Go API that must stay stable for the overlay. Grouped tools with an action enum
are slightly harder for the model than one tool per verb.

## 7. Decisions on the open questions (2026-11-14)

Answered by the user on 2026-11-14; 2, 5 and 6 take the suggested default.

1. **The task tracker adapter is pro.** It lives in the overlay, built on the public
   companion helper. The helper, cass and every other existing adapter stay public.
2. **An integration gets at most one admiral tool, with an `action` argument**, and only when
   `fleet_recall` or `fleet_brief` cannot carry what it needs.
3. **The environment wins over config**, then PATH, as `FLEET_CONFIG` and `FLEET_CASS` work
   today, so tests can pin a tool.
4. **Fleet never installs a companion.** It prints the one-line install hint.
5. **Profiles get an explicit `family` key** that defaults to the `patterns` value, so no
   existing profile changes.
6. **No `fleet-<name>` PATH plugins until someone asks for one.** Only the `schema` number on
   fleet's JSON (M7) is done now, since it is the one prerequisite.
