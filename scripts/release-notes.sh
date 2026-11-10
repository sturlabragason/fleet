#!/usr/bin/env bash
# Prints release notes for REV: the CHANGELOG.md lines added since the last v* tag, in the
# changelog's own words (ADR-73), grouped by the conventional-commit type of the commit that
# added each line:
#   **Changed default:** or **BREAKING** in the line, or a breaking commit  -> Changed defaults / action needed
#   feat -> New;  fix, perf -> Fixed;  test, ci, build, chore, refactor, style, or a line starting
#   "Contributors:" -> one Internal line;  anything else -> Changed
# ADR-n references move out of the bullets into a footer that links DESIGN.md §14. A line that
# replaces an existing changelog line (an edit, not a new entry) is left out.
# It fails when a line names a profile that is not built in (`[profile.X]`), or matches the
# private deny list check-public.sh uses ($PUBLIC_DENY, .git/info/public-deny), when present.
# Usage: scripts/release-notes.sh [REV [TAG]]   REV defaults to HEAD; TAG (the version being
# released) makes the links end at the tag instead of REV's short sha.
set -euo pipefail
rev=${1:-HEAD}
tag=${2:-}
repo=https://github.com/sturlabragason/fleet
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' ${tag:+--exclude "$tag"} "$rev" 2>/dev/null || true)
end=${tag:-$(git rev-parse --short "$rev")}

# Added entries. In a block that removes m lines and adds p, the last m added are edits of old
# entries (new ones go at the top of the day), so only the first p-m count.
if [[ -z $last ]]; then
	notes=$(git show "$rev:CHANGELOG.md" | grep '^- ' || true)
else
	notes=$(git diff "$last" "$rev" -- CHANGELOG.md | awk '
		function flush(  i) { for (i = 1; i <= p - m; i++) if (add[i] ~ /^- /) print add[i]; p = 0; m = 0 }
		/^(\+\+\+|---) / { next }
		/^-/ { m++; next }
		/^\+/ { add[++p] = substr($0, 2); next }
		{ flush() }
		END { flush() }')
fi

# Type of the commit that last added each line, as "type<TAB>line".
types=""
if [[ -n $last ]]; then
	types=$(git log --reverse --no-merges --format='@@@%x09%s%x09%b%x1e' -p "$last..$rev" -- CHANGELOG.md | awk '
		BEGIN { RS = "\n" }
		/^@@@\t/ {
			split($0, f, "\t"); s = f[2]; t = "other"
			if (match(s, /^[a-z]+(\([^)]*\))?!?:/)) { t = substr(s, 1, RLENGTH - 1); sub(/\(.*/, "", t); if (s ~ /^[a-z]+(\([^)]*\))?!:/) t = "breaking" }
			if ($0 ~ /BREAKING CHANGE/) t = "breaking"
			body = 1; next }
		/^diff / { body = 0 }
		body && /BREAKING CHANGE/ { t = "breaking" }
		/^\+- / { print t "\t" substr($0, 2) }')
fi

private=""
denylist() { grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' | sed 's/^cs://' | paste -sd'|' || true; }
info="$(git rev-parse --git-common-dir)/info/public-deny"
[[ ! -s $info ]] || private=$(denylist <"$info")
[[ -z ${PUBLIC_DENY:-} ]] || private+="${private:+|}$(denylist <<<"$PUBLIC_DENY")"
private=${private%|}
if [[ -n $private ]]; then
	rc=0
	grep -qiE -- "$private" </dev/null 2>/dev/null || rc=$?
	if ((rc > 1)); then
		echo "release-notes: the private deny list is not a valid extended regex" >&2
		exit 1
	fi
fi
builtin=$(git show "$rev:internal/tools/tools.go" 2>/dev/null | sed -n 's/^\[\([a-z0-9-]*\)\]$/\1/p' | paste -sd'|')

fail=0 n=0
declare -A group=()
adrs=()
order=(defaults new changed fixed)
while IFS= read -r line; do
	[[ -n $line ]] || continue
	n=$((n + 1))
	if [[ -n $builtin ]] && grep -oE '\[profile\.[A-Za-z0-9_-]+\]' <<<"$line" | grep -qvE "^\[profile\.($builtin)\]$"; then
		echo "release-notes: changelog entry $n names a profile that is not built in; use a built-in one ($builtin)" >&2
		fail=1
	fi
	if [[ -n $private ]] && grep -qiE -- "$private" <<<"$line"; then
		echo "release-notes: changelog entry $n matches the private deny list (text not shown)" >&2
		fail=1
	fi
	# A renumber or reword re-adds a line in a docs commit; the first feat/fix/... commit decides.
	t=$(L=$line awk -F'\t' 'BEGIN { l = ENVIRON["L"] } function k(x) { gsub(/ADR-[0-9]+/, "", x); return x }
		k($2) == k(l) { if (t == "" || t == "docs" || t == "other") t = $1 } END { print t }' <<<"$types")
	case $line in
	*'**Changed default'* | *'**BREAKING'*) g=defaults ;;
	'- Contributors:'*) g=internal ;;
	*)
		case $t in
		breaking) g=defaults ;;
		feat) g=new ;;
		fix | perf) g=fixed ;;
		test | ci | build | chore | refactor | style) g=internal ;;
		*) g=changed ;;
		esac
		;;
	esac
	while read -r a; do [[ -z $a || " ${adrs[*]} " == *" $a "* ]] || adrs+=("$a"); done < <(grep -oE 'ADR-[0-9]+' <<<"$line")
	# Drop ADR-n from the trailing brackets, the marker words, and "fleet#n" -> "#n".
	line=$(sed -E -e 's/\*\*(Changed default|BREAKING)[^*]*\*\* *(.)/\U\2/' -e 's/^- Contributors: */- /' \
		-e 's/(, )?ADR-[0-9]+//g' -e 's/\(, /(/g' -e 's/ \(\)//g' -e 's/\bfleet#([0-9]+)/#\1/g' <<<"$line")
	group[$g]+="$line"$'\n'
done <<<"$notes"
((fail == 0)) || exit 1

headline=""
for g in "${order[@]}"; do
	if [[ -n ${group[$g]:-} ]]; then
		headline=$(head -1 <<<"${group[$g]}" | sed -E -e 's/^- //' -e 's/( \(|: |; ).*//' -e 's/\.$//')
		break
	fi
done
echo "## What's new"
echo
if [[ -n $headline ]]; then
	echo "**$headline.**"
else
	echo "**No user-facing changes; see the commits.**"
fi
declare -A title=([defaults]="Changed defaults / action needed" [new]=New [changed]=Changed [fixed]=Fixed)
for g in "${order[@]}"; do
	[[ -n ${group[$g]:-} ]] || continue
	echo
	echo "### ${title[$g]}"
	printf '%s' "${group[$g]}"
done
if [[ -n ${group[internal]:-} ]]; then
	echo
	echo "### Internal"
	echo "- $(sed -E -e '/^$/d' -e 's/^- //' -e 's/\.$//' <<<"${group[internal]}" | paste -sd';' | sed 's/;/; /g')."
fi
echo
echo "---"
if ((${#adrs[@]})); then
	ref=${tag:-$(git rev-parse --short "$rev")}
	list=$(printf '%s, ' "${adrs[@]}")
	echo "Design decisions (ADRs, numbered in [DESIGN.md §14]($repo/blob/$ref/DESIGN.md#14-decisions-adrs)): ${list%, }."
fi
if [[ -n $last ]]; then
	echo "Commits: $repo/compare/$last...$end"
fi
