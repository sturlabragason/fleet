#!/usr/bin/env bash
# Does this page render when it is the only file a person gets?
#
# A sandboxed browser (Flatpak, Snap) receives a file through the document
# portal as file:///run/user/<uid>/doc/<id>/<name>. Only that one file is
# exported, so every relative <img>, <link> and <script> beside it 404s --
# and `xdg-open` exits 0 anyway, for a broken page and for no window at all.
# So we copy the page alone into an empty directory, which is the condition
# the portal creates, and load it there.
#
# Usage: scripts/check-renders.sh page.html [--min-images N]
# Pair with scripts/inline-html.py, which makes the page self-contained.
set -eu

case "${1:-}" in
  -h | --help)
    sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
esac

if [ $# -eq 0 ]; then
  echo "usage: check-renders.sh page.html [--min-images N]" >&2
  exit 2
fi
page=$1
shift
[ -f "$page" ] || { echo "no such file: $page" >&2; exit 2; }
[ -s "$page" ] || { echo "page is empty: $page" >&2; exit 1; }
command -v node >/dev/null || { echo "node is required" >&2; exit 2; }

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cp "$page" "$dir/page.html"

# Guard the guard: if the copy ever brings friends, the test proves nothing.
n=$(find "$dir" -mindepth 1 | wc -l)
[ "$n" -eq 1 ] || { echo "the copy is not a single file ($n entries)" >&2; exit 1; }

node "$(cd "$(dirname "$0")" && pwd)/check-renders.mjs" "file://$dir/page.html" "$@"
