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

repo; commit "$d" a.txt "++ zebracorp"; commit "$d" a.txt "gone"
expect fail "added line that starts with ++" "$d" main~2..main

placeholder="/ho"; placeholder+="me/user/x"
repo; commit "$d" a.txt "$placeholder"
expect pass "placeholder home path" "$d" main~1..main
commit "$d" a.txt "$placeholder and $home"
expect fail "leak on the same line as an allowed placeholder" "$d" main~1..main

repo; commit "$d" a.txt "zebracorp"
GITHUB_ACTIONS=true expect fail "CI mode still fails" "$d" main~1..main
if grep -q -i zebracorp "$tmp/out"; then echo "FAIL CI mode printed the matched text"; failed=1; else echo "ok   CI mode prints locations only"; fi

# The private deny list (this clone's .git/info/public-deny, or $CHECK_PUBLIC_REAL_DENY) must catch
# every sample line in the private .git/info/public-leak-samples (or $CHECK_PUBLIC_LEAK_SAMPLES), one
# leaked line per line, and pass the scrubbed files. Both files stay private, so this one names no leak.
src=$(cd "$here/.." && pwd)
info=$(git -C "$src" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)/info
real=${CHECK_PUBLIC_REAL_DENY:-$info/public-deny}
samples=${CHECK_PUBLIC_LEAK_SAMPLES:-$info/public-leak-samples}
if [[ -s $real && -s $samples ]]; then
  real_repo() { repo; cp "$real" "$d/.git/info/public-deny"; cp "$src/scripts/public-allow" "$d/scripts/"; }
  n=0
  while IFS= read -r line || [[ -n $line ]]; do
    [[ -n $line ]] || continue
    n=$((n + 1))
    real_repo; commit "$d" a.txt "$line"
    expect fail "private deny list catches leak sample $n" "$d" main~1..main
  done <"$samples"
  real_repo
  for f in docs/design/extensions.md CHANGELOG.md DESIGN.md internal/whatsnew/version.go internal/whatsnew/whatsnew_test.go .github/workflows/ci.yml \
    internal/prefs/prefs_test.go internal/prefs/integrations.go; do
    mkdir -p "$d/$(dirname "$f")"; cp "$src/$f" "$d/$f"
  done
  git -C "$d" add -A && git -C "$d" commit -q -m scrubbed >/dev/null
  expect pass "private deny list passes the scrubbed files" "$d" main~1..main
else
  echo "skip private deny list cases: no $real or $samples"
fi

exit $failed
