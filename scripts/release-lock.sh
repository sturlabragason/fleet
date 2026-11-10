#!/usr/bin/env bash
# One publisher at a time: a lock in the repo's common git dir, shared by every worktree.
# Usage:
#   scripts/release-lock.sh acquire <agent>   take the lock (exit 1 if another agent holds it)
#   scripts/release-lock.sh release <agent>   drop it (exit 1 if another agent holds it)
#   scripts/release-lock.sh status            print the holder, exit 1 if free
# A lock older than FLEET_RELEASE_LOCK_TTL seconds (default 7200) is stale and may be taken.
# The same agent may acquire again; that refreshes the time.
set -euo pipefail

ttl=${FLEET_RELEASE_LOCK_TTL:-7200}
lock="$(git rev-parse --path-format=absolute --git-common-dir)/fleet-release.lock"

holder() { sed -n 's/^agent=//p' "$lock" 2>/dev/null || true; }
age() {
	local t
	t=$(sed -n 's/^time=//p' "$lock" 2>/dev/null || true)
	echo $(($(date +%s) - ${t:-0}))
}

write() {
	local tmp
	tmp=$(mktemp "$lock.XXXXXX")
	printf 'agent=%s\npid=%s\ntime=%s\nhost=%s\n' "$1" "$PPID" "$(date +%s)" "$(hostname)" >"$tmp"
	# ln is atomic and fails if the lock exists: two publishers can't both win.
	if ln "$tmp" "$lock" 2>/dev/null; then rm -f "$tmp"; return 0; fi
	rm -f "$tmp"
	return 1
}

case ${1:-} in
acquire)
	agent=${2:?usage: release-lock.sh acquire <agent>}
	for _ in 1 2 3; do
		if write "$agent"; then echo "release lock taken by $agent"; exit 0; fi
		h=$(holder)
		[[ -n $h ]] || continue # released between ln and read: try again
		if [[ $h == "$agent" || $(age) -gt $ttl ]]; then
			[[ $h == "$agent" ]] || echo "taking stale release lock from $h ($(age)s old)" >&2
			# mv is atomic: of two takers only one moves the old lock away; the other
			# finds the winner's fresh lock on the next pass.
			seen=$(cat "$lock" 2>/dev/null || true)
			if mv "$lock" "$lock.old.$$" 2>/dev/null; then
				# Moved a lock someone took after we read it: put it back.
				[[ $(cat "$lock.old.$$") == "$seen" ]] || ln "$lock.old.$$" "$lock" 2>/dev/null || true
				rm -f "$lock.old.$$"
			fi
			continue
		fi
		break
	done
	echo "release lock held by $(holder) for $(age)s ($lock); ask them, don't publish over them" >&2
	exit 1
	;;
release)
	agent=${2:?usage: release-lock.sh release <agent>}
	[[ -e $lock ]] || exit 0
	if [[ $(holder) != "$agent" ]]; then
		echo "release lock is held by $(holder), not $agent" >&2
		exit 1
	fi
	rm -f "$lock"
	echo "release lock dropped by $agent"
	;;
status)
	[[ -e $lock ]] || { echo "free"; exit 1; }
	echo "held by $(holder) for $(age)s"
	cat "$lock"
	;;
*)
	sed -n '3,8s/^# \{0,1\}//p' "$0" >&2
	exit 2
	;;
esac
