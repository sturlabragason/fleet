#!/usr/bin/env bash
# Check commits (and optionally GitHub text) before they leave this machine.
# Usage: scripts/check-public.sh [--no-emails] <rev-list args>   e.g.  origin/main..HEAD   or   HEAD
#        scripts/check-public.sh --gh [owner/repo]   scan issues, PRs, comments and releases on GitHub
#
# A commit range fails when a commit in it:
#   - has an author or committer email that is not allowed (skip with --no-emails, e.g. for PRs)
#   - has a commit message, or adds a diff line, matching a deny pattern or an unexpected email
#   - leaves a tip tree containing a deny pattern or an unexpected email
#   - is flagged by gitleaks (if installed)
# Built-in deny patterns: home paths other than /home/user, private IPv4 addresses.
# Private patterns and emails are never committed. They come from the git dir or the environment:
#   .git/info/public-deny   or $PUBLIC_DENY     ERE, one per line, case-insensitive; prefix cs: for case-sensitive
#   .git/info/public-emails or $PUBLIC_EMAILS   allowed commit emails, one per line
#   .git/info/public-allow                      ERE; matching lines are ignored
# scripts/public-allow (committed) holds the shared allowlist for false positives.
set -euo pipefail

usage() { echo "usage: $0 [--no-emails] <rev-list args> | --gh [owner/repo]" >&2; exit 2; }
[[ $# -gt 0 ]] || usage
root=$(git rev-parse --show-toplevel)
info="$(git rev-parse --git-common-dir)/info"
self=scripts/check-public.sh
fail=0
err() { echo "check-public: $*" >&2; fail=1; }
lines() { grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' || true; }

deny='/home/[a-z]|\b10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b|\b192\.168\.[0-9]{1,3}\.[0-9]{1,3}\b|\b172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}\b'
deny_cs=''
allow='/home/user\b'
add_deny() {
  local p
  while IFS= read -r p; do
    if [[ $p == cs:* ]]; then deny_cs+="${deny_cs:+|}${p#cs:}"; else deny+="|$p"; fi
  done < <(lines)
}
add_allow() {
  local p
  while IFS= read -r p; do allow+="|$p"; done < <(lines)
}
[[ ! -s $info/public-deny ]] || add_deny <"$info/public-deny"
[[ -z ${PUBLIC_DENY:-} ]] || add_deny <<<"$PUBLIC_DENY"
[[ ! -s $root/scripts/public-allow ]] || add_allow <"$root/scripts/public-allow"
[[ ! -s $info/public-allow ]] || add_allow <"$info/public-allow"

emails=()
[[ ! -s $info/public-emails ]] || mapfile -t -O "${#emails[@]}" emails < <(lines <"$info/public-emails")
[[ -z ${PUBLIC_EMAILS:-} ]] || mapfile -t -O "${#emails[@]}" emails < <(lines <<<"$PUBLIC_EMAILS")
email_re='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[a-z]{2,}'
email_ok=(-e noreply -e example. -e git@github.com)
for a in "${emails[@]}"; do email_ok+=(-e "$a"); done

# Reads "<where>:<text>" lines, prints the ones that are denied and not allowed.
hits() {
  local in
  in=$(cat)
  {
    grep -i -E -- "$deny" <<<"$in" || true
    [[ -z $deny_cs ]] || grep -E -- "$deny_cs" <<<"$in" || true
    grep -E -- "$email_re" <<<"$in" | grep -v -F "${email_ok[@]}" || true
  } | grep -v -E -- "$allow" | sort -u || true
}
report() { # report <what> <hits>
  [[ -z $2 ]] || { echo "$2" >&2; err "$1 (above)"; }
}

if [[ $1 == --gh ]]; then
  repo=${2:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}
  text=$(
    gh api --paginate "repos/$repo/issues?state=all&per_page=100" \
      --jq '.[] | . as $i | ((.title + "\n" + (.body // "")) | split("\n")[] | "\($i.html_url): \(.)")'
    gh api --paginate "repos/$repo/issues/comments?per_page=100" \
      --jq '.[] | . as $c | ((.body // "") | split("\n")[] | "\($c.html_url): \(.)")'
    gh api --paginate "repos/$repo/pulls/comments?per_page=100" \
      --jq '.[] | . as $c | ((.body // "") | split("\n")[] | "\($c.html_url): \(.)")'
    gh api --paginate "repos/$repo/releases?per_page=100" \
      --jq '.[] | . as $r | (((.name // "") + "\n" + (.body // "")) | split("\n")[] | "\($r.html_url): \(.)")'
  )
  report "denied strings in GitHub text of $repo" "$(hits <<<"$text")"
  exit $fail
fi

check_emails=1
if [[ $1 == --no-emails ]]; then check_emails=0; shift; [[ $# -gt 0 ]] || usage; fi
if ((check_emails)) && ((${#emails[@]} == 0)); then
  echo "check-public: no allowed emails: set \$PUBLIC_EMAILS or write $info/public-emails (one per line)" >&2
  exit 1
fi

commits=$(git rev-list "$@")
[[ -n $commits ]] || exit 0

if ((check_emails)); then
  for c in $commits; do
    for e in $(git log -1 --format='%ae %ce' "$c"); do
      printf '%s\n' "${emails[@]}" | grep -qxF -- "$e" || err "$(git log -1 --format=%h "$c"): email $e is not allowed"
    done
  done
fi

# Commit messages, one "<sha>:msg:<line>" per line.
msgs=$(for c in $commits; do git log -1 --format=%B "$c" | sed "s/^/$(git rev-parse --short "$c"):msg: /"; done)
report "denied strings in commit messages" "$(hits <<<"$msgs")"

# Every line each commit adds, so a string added and removed again within the range is still caught.
added=$(git log --no-color --no-ext-diff --no-renames -p --format='@@@ %h' --no-walk=unsorted $commits -- . ":!$self" |
  awk '/^@@@ /{c=$2; next} /^\+\+\+ /{f=substr($0,7); next} /^\+/{print c ":" f ": " substr($0,2)}')
report "denied strings added by a commit" "$(hits <<<"$added")"

tip=$(head -1 <<<"$commits")
tree=$(git grep -n -I --full-name -e '' "$(git rev-parse --short "$tip")" -- . ":!$self" || true)
report "denied strings in the tree at $(git rev-parse --short "$tip")" "$(hits <<<"$tree")"

gl=$(command -v gitleaks || true)
[[ -n $gl || ! -x $HOME/go/bin/gitleaks ]] || gl=$HOME/go/bin/gitleaks
if [[ -n $gl ]]; then
  "$gl" git --no-banner --log-level error --log-opts="$*" . || err "gitleaks found something"
else
  echo "check-public: gitleaks not installed, secret scan skipped" >&2
fi

exit $fail
