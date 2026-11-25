#!/usr/bin/env bash
# Print the weekly digest: the newest user-facing CHANGELOG.md lines from the last 7 days.
# Usage: scripts/weekly-digest.sh [since YYYY-MM-DD] [changelog]
# Prints nothing when there is nothing to report, so the caller posts nothing.
# Line 1 is the title; the rest is the Markdown body. Used by .github/workflows/digest.yml.
set -euo pipefail

since=${1:-$(date -u -d '7 days ago' +%F)}
file=${2:-CHANGELOG.md}
max=5

# CHANGELOG.md is newest first: "## YYYY-MM-DD" headings, "- " lines under each.
# Contributors: lines only matter to people working on fleet itself.
mapfile -t lines < <(awk -v since="$since" '
  /^## [0-9]{4}-[0-9]{2}-[0-9]{2}$/ { day = $2; next }
  day >= since && /^- / && !/^- Contributors:/ { print }
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
