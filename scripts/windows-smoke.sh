#!/usr/bin/env bash
# Smoke test of a Windows fleet.exe on a runner with no tmux (CI windows job):
# the CLI, the MCP server and an acp agent's whole life with the fake agent
# (spawn, status, send, peek, info, close), all without tmux (ADR-117).
# usage: scripts/windows-smoke.sh path/to/fleet.exe
set -uo pipefail
exe=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
home=$(mktemp -d)
export HOME=$home USERPROFILE=$home FLEET_HOME=$home/.fleet TMUX_TMPDIR=$home/tmp
mkdir -p "$TMUX_TMPDIR" "$home/work"
work=$home/work
command -v cygpath >/dev/null && work=$(cygpath -w "$work")
fail=0

ok() { echo "ok    $1"; }
bad() { echo "FAIL  $1"; [[ -n ${2:-} ]] && echo "$2" | head -30 | sed 's/^/      /'; fail=1; }
must() { # must <desc> <cmd...>: exits 0; output in $out
	local d=$1; shift
	if out=$("$@" 2>&1); then ok "$d"; else bad "$d" "$out"; fi
}
until_ok() { # until_ok <secs> <cmd...>: retry until it exits 0
	local n=$1; shift
	for ((i = 0; i < n * 2; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; done
	return 1
}

must "version" "$exe" version
[[ $out == "fleet "* ]] || bad "version line" "$out"
must "--help" "$exe" --help
must "changes" "$exe" changes
must "ls --json (empty)" "$exe" ls --json

must "config explain claude" "$exe" config explain claude
grep -Eq 'driver +acp' <<<"$out" || bad "claude defaults to acp" "$out"
if out=$("$exe" spawn --fake --driver tmux t1 "$work" 2>&1); then bad "--driver tmux refused" "$out"; else
	grep -q 'needs tmux' <<<"$out" && ok "--driver tmux refused" || bad "--driver tmux message" "$out"
fi

# Real claude can't run here; its spawn must fail on the missing tool, not on tmux.
if out=$("$exe" spawn c1 claude "$work" 2>&1); then bad "claude spawn without claude installed" "$out"; "$exe" close c1 >/dev/null 2>&1; else
	grep -q 'not found' <<<"$out" && ! grep -qi tmux <<<"$out" && ok "claude spawn checks for the tool" || bad "claude spawn message" "$out"
fi

must "spawn --fake a1" "$exe" spawn --fake a1 "$work"
idle() { "$exe" ls --json | grep -q '"status": *"idle"'; }
until_ok 30 idle && ok "a1 is idle" || bad "a1 is idle" "$("$exe" ls 2>&1)"
must "info a1" "$exe" info a1
grep -q 'driver: acp' <<<"$out" || bad "a1 runs under acp" "$out"
must "send a1" "$exe" send a1 "hello windows"
answered() { "$exe" peek a1 | grep -q 'fake: hello windows'; }
until_ok 30 answered && ok "a1 answered" || bad "a1 answered" "$("$exe" peek a1 2>&1)"

mcp=$(printf '%s\n' \
	'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
	'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	'{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fleet_status","arguments":{}}}' |
	timeout 30 "$exe" mcp 2>&1)
grep -q '"id":2' <<<"$mcp" && grep -q 'a1' <<<"$mcp" && ok "mcp fleet_status lists a1" || bad "mcp fleet_status" "$mcp"

must "close a1" "$exe" close a1
must "ls after close" "$exe" ls --json
grep -q '"a1"' <<<"$out" && bad "a1 gone after close" "$out"
gone() { ! grep -q '"pid": *[1-9]' "$TMUX_TMPDIR/fleet-headless/fleet/state.json"; }
gone && ok "no host left" || bad "no host left" "$(cat "$TMUX_TMPDIR/fleet-headless/fleet/state.json")"

if ((fail)); then
	echo "--- fleet.log"; tail -40 "$FLEET_HOME/logs/fleet.log" 2>/dev/null
	for f in "$TMUX_TMPDIR"/fleet-headless/fleet/*.log; do [[ -f $f ]] && { echo "--- $f"; tail -20 "$f"; }; done
fi
exit $fail
