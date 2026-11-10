#!/usr/bin/env bash
# Tests for scripts/check-public.sh against throwaway repos.
# Usage: scripts/check-public_test.sh [path/to/check-public.sh]
# The private-looking strings are built at run time so this file passes the guard itself.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$(realpath "${1:-$here/check-public.sh}")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export HOME=$tmp GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.org GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.org
unset PUBLIC_DENY PUBLIC_EMAILS
PATH=$(tr ':' '\n' <<<"$PATH" | grep -v -e go/bin | paste -sd:) # keep gitleaks out of the unit test
n=0 failed=0

repo() { # fresh repo with a clean base commit, in $d
  d=$tmp/r$((++n))
  git init -q -b main "$d"
  mkdir -p "$d/scripts" "$d/.git/info"
  install -m 755 "$script" "$d/scripts/check-public.sh"
  printf 'zebracorp\ncs:\\bMonday\\b\n' >"$d/.git/info/public-deny"
  echo t@example.org >"$d/.git/info/public-emails"
  echo base >"$d/README"
  git -C "$d" add -A && git -C "$d" commit -q -m base >/dev/null
}
commit() { # commit <repo> <file> <content> [message]
  printf '%s\n' "$3" >"$1/$2"
  git -C "$1" add -A && git -C "$1" commit -q -m "${4:-change}" >/dev/null
}
expect() { # expect <pass|fail> <name> <repo> [script args...]
  local want=$1 name=$2 dir=$3 got
  shift 3
  if (cd "$dir" && scripts/check-public.sh "$@" >"$tmp/out" 2>&1); then got=pass; else got=fail; fi
  if [[ $got == "$want" ]]; then echo "ok   $name"; else
    echo "FAIL $name: want $want, got $got"; sed 's/^/     /' "$tmp/out"; failed=1
  fi
}

repo; commit "$d" a.txt "hello"
expect pass "clean range" "$d" main~1..main

repo; commit "$d" a.txt "zebracorp"
expect fail "denied string in the tree" "$d" main~1..main

repo; commit "$d" a.txt "zebracorp"; commit "$d" a.txt "gone"
expect fail "denied string added and removed within the range" "$d" main~2..main

repo; commit "$d" a.txt "hello" "fix the ZebraCorp bug"
expect fail "denied string in a commit message" "$d" main~1..main

repo; commit "$d" a.txt "see you on monday"
expect pass "case-sensitive pattern ignores other case" "$d" main~1..main
commit "$d" a.txt "Meeting on Monday"
expect fail "case-sensitive pattern" "$d" main~1..main

ip="192.168"; ip+=".1.20"
repo; commit "$d" a.txt "host $ip"; commit "$d" a.txt "gone"
expect fail "private IP added in the range" "$d" main~2..main

home="/ho"; home+="me/alice/x"
repo; commit "$d" a.txt "$home"
expect fail "home path" "$d" main~1..main
repo; printf '%s\n' "alice/x" >"$d/scripts/public-allow"; commit "$d" a.txt "$home"
expect pass "committed allowlist" "$d" main~1..main

repo; commit "$d" a.txt "giraffeinc"
PUBLIC_DENY=$'# from CI\ngiraffeinc' expect fail "deny pattern from \$PUBLIC_DENY" "$d" main~1..main

at="@"; other="someone${at}corp.test"
repo; GIT_AUTHOR_EMAIL=$other commit "$d" a.txt "hello"
expect fail "author email not allowed" "$d" main~1..main
expect pass "--no-emails skips commit emails" "$d" --no-emails main~1..main

repo; rm "$d/.git/info/public-emails"; commit "$d" a.txt "hello"
expect fail "no allowed emails configured" "$d" main~1..main
PUBLIC_EMAILS=t@example.org expect pass "emails from \$PUBLIC_EMAILS" "$d" main~1..main

repo; commit "$d" a.txt "mail $other"
expect fail "unexpected email address added" "$d" main~1..main

exit $failed
