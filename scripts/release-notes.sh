#!/usr/bin/env bash
# Prints release notes for REV: the CHANGELOG.md lines added since the last
# v* tag, in the changelog's own words (ADR-73).
# Usage: scripts/release-notes.sh [REV]   (default HEAD)
set -euo pipefail
rev=${1:-HEAD}
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$rev" 2>/dev/null || true)
if [[ -z $last ]]; then
	notes=$(git show "$rev:CHANGELOG.md" | grep '^- ' || true)
else
	notes=$(git diff "$last" "$rev" -- CHANGELOG.md | grep '^+- ' | sed 's/^+//' || true)
fi
echo "## What's new"
echo
if [[ -n $notes ]]; then
	echo "$notes"
else
	echo "- No user-facing changes; see the commits."
fi
if [[ -n $last ]]; then
	echo
	echo "Commits: https://github.com/sturlabragason/fleet/compare/$last...$(git rev-parse --short "$rev")"
fi
