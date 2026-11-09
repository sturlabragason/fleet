#!/usr/bin/env bash
# Prints the next release tag from the conventional commits since the last
# v* tag (ADR-73). svu's rules, kept pre-1.0 aware:
#   feat → minor; fix and everything else → patch;
#   breaking (`type!:` or `BREAKING CHANGE`) → major, but minor while on v0.
# Usage: scripts/next-version.sh [REV]   (default HEAD). Exits 1 when there is
# nothing new since the last tag.
set -euo pipefail
rev=${1:-HEAD}
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$rev" 2>/dev/null || echo v0.0.0)
range="$last..$rev"
[[ $last == v0.0.0 ]] && range=$rev
subjects=$(git log --format=%s "$range")
bodies=$(git log --format=%b "$range")
if [[ -z $subjects ]]; then
	echo "no commits since $last" >&2
	exit 1
fi
IFS=. read -r major minor patch <<<"${last#v}"
if grep -qE '^[a-z]+(\([^)]*\))?!:' <<<"$subjects" || grep -q 'BREAKING CHANGE' <<<"$bodies"; then
	if ((major == 0)); then bump=minor; else bump=major; fi
elif grep -qE '^feat(\([^)]*\))?:' <<<"$subjects"; then
	bump=minor
else
	bump=patch
fi
case $bump in
major) echo "v$((major + 1)).0.0" ;;
minor) echo "v$major.$((minor + 1)).0" ;;
patch) echo "v$major.$minor.$((patch + 1))" ;;
esac
