#!/usr/bin/env bash
# Regenerate docs/img/hero.png and docs/img/demo.gif from fake agents.
# Runs a separate tmux server (own TMUX_TMPDIR and FLEET_HOME), so a running
# fleet is never touched. The admiral pane runs the fake profile: no tokens.
# Needs: tmux, python3 with Pillow + fontTools, ImageMagick (magick).
#
# The shot is composed, not waited for: fake agents take "!work", "!wait",
# "!error" and "!done" directives (internal/fake), so the wall always shows the
# same believable mix of states. Geometry is overridable while drafting:
#   SHOT_COLS=200 SHOT_ROWS=52 SHOT_FONT=14 SHOT_OUT=/tmp/draft SHOT_GIF=0 ...
set -eu
cd "$(dirname "$0")/.."
ROOT=$PWD
COLS=${SHOT_COLS:-200} ROWS=${SHOT_ROWS:-40} FONT=${SHOT_FONT:-15}
OUT=${SHOT_OUT:-docs/img} GIF=${SHOT_GIF:-1} NAME=${SHOT_NAME:-hero}
make build >/dev/null
D=$(mktemp -d)
mkdir -m 700 "$D/tmux"
export TMUX_TMPDIR="$D/tmux" FLEET_HOME="$D/home" FLEET_ADMIRAL_PROFILE=fake
# A config of our own, so the user's max_working or starting mode can't change
# the picture. All eight agents run: the shot is about a busy fleet.
printf '[defaults]\nmax_working = 0\n' > "$D/config.toml"
export FLEET_CONFIG=$D/config.toml
unset TMUX
B=$ROOT/bin/fleet
# Address the servers by socket path, not by name: a caller with a cleared
# environment would otherwise resolve "-L fleet" to the user's real server and
# type directives into their admiral.
SOCK=$D/tmux/tmux-$(id -u)
T="tmux -S $SOCK/fleet"
S="tmux -S $SOCK/fleet-test-shot"
cleanup() {
  # `fleet up` started a detached watcher; it is not in either tmux server,
  # so stop it by name and wait, or it outlives the temp dir (ADR-83).
  W=$("$B" watch --stop 2>/dev/null | sed -n 's/.*(pid \([0-9]*\)).*/\1/p')
  if [ -n "$W" ]; then
    for _ in $(seq 50); do [ -d "/proc/$W" ] || break; sleep 0.1; done
    if [ -d "/proc/$W" ]; then kill -9 "$W" 2>/dev/null || true; fi
  fi
  $S kill-server 2>/dev/null || true
  $T kill-server 2>/dev/null || true
  rm -r "$D"
}
trap cleanup EXIT

# The wall agents carry the story; the rest fill the overview tiles.
WALL="api-auth web-nav infra-dns db-migrate"
"$B" up --no-attach
for n in $WALL ci-cache docs search-idx billing-fix; do
  "$B" spawn --fake "$n" /tmp >/dev/null
done
sleep 2
"$B" wall preset 4 >/dev/null
for n in $WALL; do "$B" wall add "$n" >/dev/null; done

$S new-session -d -x "$COLS" -y "$ROWS" -s shot \
  "env TERM=xterm-256color tmux -S $SOCK/fleet attach -t fleet:main"
sleep 3

# One of each state on the wall: working, waiting on you, done, failed. Every
# directive names its task, so no two panes tell the same story.
"$B" send --force web-nav '!work add search endpoint' >/dev/null
"$B" send --force api-auth '!wait refactor auth middleware' >/dev/null
"$B" send --force db-migrate '!done write migration' >/dev/null
"$B" send --force infra-dns '!error bump dependencies' >/dev/null
# The admiral is a pane, not a board agent, so it is driven through tmux.
SUP=$($T list-panes -a -F '#{pane_id} #{@role}' | awk '$2=="sup"{print $1; exit}')
if [ -z "$SUP" ]; then echo "no admiral pane: the shot would show a random state" >&2; exit 1; fi
$T send-keys -t "$SUP" '!work watch the fleet' Enter
# Tiles: a second agent waiting on you, the rest working.
"$B" send --force search-idx '!wait fix flaky e2e' >/dev/null
"$B" send --force ci-cache '!work update deploy config' >/dev/null
"$B" send --force docs '!work tidy README' >/dev/null
"$B" send --force billing-fix '!work add tests for board store' >/dev/null
sleep 17

mkdir -p "$OUT" "$D/frames"
$S capture-pane -e -p -t shot > "$D/shot.ans"
python3 scripts/ansi2png.py "$D/shot.ans" "$OUT/$NAME.png" "$FONT" "$COLS" "$ROWS"
echo "wrote $OUT/$NAME.png"
[ "$GIF" = 1 ] || exit 0

# The demo tells one story: an approval arrives, you answer it, work resumes.
for i in $(seq -w 1 24); do
  $S capture-pane -e -p -t shot > "$D/frames/$i.ans"
  case $i in
    06) "$B" send --force api-auth y >/dev/null ;;
    10) "$B" send --force infra-dns '!work fix flaky e2e' >/dev/null ;;
    14) "$B" send --force search-idx y >/dev/null ;;
    18) "$B" send --force db-migrate '!work write migration' >/dev/null ;;
    22) "$B" send --force web-nav '!done' >/dev/null ;;
  esac
  sleep 0.5
done
for f in "$D"/frames/*.ans; do python3 scripts/ansi2png.py "$f" "${f%.ans}.png" $((FONT - 3)) "$COLS" "$ROWS"; done
magick -delay 50 -loop 0 "$D"/frames/*.png -layers Optimize -colors 64 "$OUT/demo.gif"
echo "wrote $OUT/demo.gif"
