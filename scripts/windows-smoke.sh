#!/usr/bin/env bash
# Smoke test of a Windows fleet.exe on a runner with no tmux (CI windows job).
# usage: scripts/windows-smoke.sh path/to/fleet.exe
set -uo pipefail
exe=$1
home=$(mktemp -d)
export HOME=$home USERPROFILE=$home
fail=0

must() { # must <desc> <cmd...>: the command exits 0
	local d=$1; shift
	if out=$("$@" 2>&1); then echo "ok    $d"; else echo "FAIL  $d"; echo "$out" | sed 's/^/      /'; fail=1; fi
}
probe() { # probe <desc> <cmd...>: print what happens, never fail
	local d=$1; shift
	out=$("$@" 2>&1); rc=$?
	echo "probe $d -> exit $rc"; echo "$out" | head -15 | sed 's/^/      /'
}

must "version" "$exe" version
"$exe" version | grep -q '^fleet ' || { echo "FAIL  version line"; fail=1; }
must "--help" "$exe" --help
probe "changes" "$exe" changes
probe "ls --json" "$exe" ls --json
probe "config" "$exe" config
probe "spawn --fake --driver acp" "$exe" spawn --fake --driver acp a1
probe "ls after spawn" "$exe" ls --json
probe "close" "$exe" close a1
exit $fail
