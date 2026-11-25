#!/usr/bin/env bash
# Print the weekly digest: the newest user-facing CHANGELOG.md lines from one Monday-to-Sunday week.
# Usage: scripts/weekly-digest.sh [monday YYYY-MM-DD] [changelog]
# The default week is the last full one, so a rerun on any day of this week prints the same digest.
# Prints nothing when there is nothing to report, so the caller posts nothing.
# Line 1 is the title; the rest is the Markdown body. Used by .github/workflows/digest.yml.
set -euo pipefail

today=$(date -u +%F)
since=${1:-$(date -u -d "$today -$(($(date -u +%u) + 6)) days" +%F)}
until=$(date -u -d "$since +7 days" +%F)
file=${2:-CHANGELOG.md}
max=5

# CHANGELOG.md is newest first: "## YYYY-MM-DD" headings, "- " lines under each.
# Contributors: lines only matter to people working on fleet itself.
mapfile -t lines < <(awk -v since="$since" -v until="$until" '
  /^## [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/ { day = $2; next }
  day >= since && day < until && /^- / && !/^- Contributors:/ { print }
' "$file")

n=${#lines[@]}
((n > 0)) || exit 0

echo "What's new in fleet, week of $since"
echo
printf '%s\n' "${lines[@]:0:max}"
if ((n > max)); then
  echo
  echo "And $((n - max)) more this week: [CHANGELOG.md](https://github.com/sturlabragason/fleet/blob/main/CHANGELOG.md)."
fi
echo
echo "Update with \`fleet update\`, or see [the releases](https://github.com/sturlabragason/fleet/releases)."
