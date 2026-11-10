#!/usr/bin/env bash
# Tests for scripts/release-lock.sh against a throwaway repo and worktree.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$(realpath "${1:-$here/release-lock.sh}")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export HOME=$tmp GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.org GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.org
unset FLEET_RELEASE_LOCK_TTL
failed=0
d=$tmp/r
git init -q -b main "$d" && git -C "$d" commit -q --allow-empty -m base
git -C "$d" worktree add -q "$tmp/wt" -b other

ok() { if "${@:2}" >/dev/null 2>&1; then :; else echo "FAIL: $1"; failed=1; fi; }
no() { if "${@:2}" >/dev/null 2>&1; then echo "FAIL: $1"; failed=1; fi; }
at() { (cd "$1" && "$script" "${@:2}"); }

no "status free" at "$d" status
ok "a acquires" at "$d" acquire a
ok "a re-acquires" at "$d" acquire a
no "b blocked from another worktree" at "$tmp/wt" acquire b
no "b can't release a's lock" at "$tmp/wt" release b
ok "status held" at "$tmp/wt" status
ok "a releases" at "$d" release a
ok "b acquires after release" at "$tmp/wt" acquire b
ok "stale lock taken" env FLEET_RELEASE_LOCK_TTL=-1 bash -c "cd '$d' && '$script' acquire a"
[[ $(at "$d" status | sed -n 's/^agent=//p') == a ]] || { echo "FAIL: holder after stale take"; failed=1; }

[[ $failed == 0 ]] && echo "release-lock: ok"
exit $failed
