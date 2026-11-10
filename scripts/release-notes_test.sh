#!/usr/bin/env bash
# Tests for scripts/release-notes.sh against a throwaway repo.
# Usage: scripts/release-notes_test.sh [path/to/release-notes.sh]
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$(realpath "${1:-$here/release-notes.sh}")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export HOME=$tmp GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.org GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.org
unset PUBLIC_DENY
failed=0
d=$tmp/r
git init -q -b main "$d"
mkdir -p "$d/internal/tools"
printf 'const x = `\n[claude]\ncommand = "claude"\n[copilot]\n`\n' >"$d/internal/tools/tools.go"
printf '# What'"'"'s new\n\n## 2026-01-01\n- Old entry (ADR-1).\n' >"$d/CHANGELOG.md"
git -C "$d" add -A && git -C "$d" commit -q -m "feat: base" && git -C "$d" tag v1.0.0

add() { # add <commit message> <changelog line>: adds the line at the top of the day
	sed -i "4i - $2" "$d/CHANGELOG.md"
	git -C "$d" commit -q -am "$1"
}
add "feat: a thing" "You can do a new thing (ADR-5)."
add "feat: path" "Paths like C:\\\\tmp work."
add "fix: a bug" "The bug is gone (#7, ADR-6, fleet#8)."
add "test: no leaks" "Tests leave nothing running."
add "ci: guard" "Contributors: the CI checks more."
add "feat: driver" "**Changed default:** agents use the new driver; set \`driver = \"old\"\` to keep the old one."
sed -i 's/^- Old entry (ADR-1)\./- Old entry, reworded (ADR-1)./' "$d/CHANGELOG.md"
git -C "$d" commit -q -am "docs: reword"
add "docs: renumber" "You can do a new thing (ADR-9)."
sed -i '/(ADR-5)/d' "$d/CHANGELOG.md" && git -C "$d" commit -q -am "docs: drop old number"

out=$(cd "$d" && "$script" HEAD v1.1.0 2>&1)
check() { # check <name> <grep -E pattern> [absent]
	if grep -qE -- "$2" <<<"$out"; then got=present; else got=absent; fi
	if [[ $got == "${3:-present}" ]]; then echo "ok   $1"; else echo "FAIL $1: $2 is $got"; failed=1; fi
}
check "headline is the changed default" '^\*\*Agents use the new driver\.\*\*$'
check "changed defaults section" '^### Changed defaults / action needed$'
check "marker removed" 'Changed default:\*\*' absent
check "feat is New, renumbered line keeps its feat type" '^### New$'
check "renumbered line is listed once" '^- You can do a new thing\.$'
check "fix is Fixed with issue refs kept" '^- The bug is gone \(#7, #8\)\.$'
check "test and Contributors fold into one Internal line" '^- The CI checks more; Tests leave nothing running\.$'
check "edited old line is left out" 'reworded' absent
check "ADRs are a linked footer" 'DESIGN.md#14-decisions-adrs\)\): ADR-9, ADR-6\.$'
if awk '/^### /{sec=$0} /Paths like/{print sec}' <<<"$out" | grep -qx '### New'; then echo "ok   a line with a backslash keeps its feat type"
else echo "FAIL a line with a backslash is not under New"; failed=1; fi
check "no ADR in bullets" '^- .*ADR-' absent
check "links end at the tag" 'compare/v1.0.0\.\.\.v1.1.0$'
[[ -z ${DEBUG:-} ]] || echo "$out"

add "feat: leak" "Set it under \`[profile.my-own]\`."
if (cd "$d" && "$script" HEAD v1.1.0 >/dev/null 2>"$tmp/err"); then echo "FAIL non-built-in profile passes"; failed=1
elif grep -q my-own "$tmp/err"; then echo "FAIL error message repeats the profile name"; failed=1
else echo "ok   non-built-in profile fails without echoing it"; fi
git -C "$d" reset -q --hard HEAD^

add "feat: client" "Works for zebracorp now."
if (cd "$d" && PUBLIC_DENY=zebracorp "$script" HEAD v1.1.0 >/dev/null 2>"$tmp/err"); then echo "FAIL private deny pattern passes"; failed=1
elif grep -q zebracorp "$tmp/err"; then echo "FAIL error message repeats the denied text"; failed=1
else echo "ok   private deny pattern fails without echoing it"; fi

if (cd "$d" && PUBLIC_DENY='foo(' "$script" HEAD v1.1.0 >/dev/null 2>"$tmp/err"); then echo "FAIL invalid deny regex passes"; failed=1
else echo "ok   invalid deny regex fails"; fi

exit $failed
