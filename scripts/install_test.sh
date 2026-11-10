#!/usr/bin/env bash
# Tests for install.sh's provenance check (ADR-103), offline: a fake curl
# serves a fake release and a fake gh answers `gh attestation verify`.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$(realpath "${1:-$here/../install.sh}")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
failed=0

# A PATH with only the tools install.sh needs, so the real gh is never found.
bin=$tmp/bin
mkdir -p "$bin" "$tmp/release"
for t in sh awk sha256sum cut sed mktemp rm mkdir chmod cp mv uname cat; do
	ln -s "$(command -v "$t")" "$bin/$t"
done
cat >"$bin/curl" <<'EOF'
#!/bin/sh
while [ $# -gt 1 ]; do [ "$1" = -o ] && out=$2; shift; done
cp "$FAKE_RELEASE/${1##*/}" "$out"
EOF
cat >"$tmp/gh" <<'EOF'
#!/bin/sh
echo "$*" >>"$FAKE_GH_LOG"
case $1 in
auth) exit "${FAKE_GH_AUTH:-0}" ;;
attestation) exit "${FAKE_GH_VERIFY:-0}" ;;
esac
exit 2
EOF
chmod +x "$bin/curl" "$tmp/gh"

asset=fleet-$(uname -s | tr A-Z a-z)-$(case $(uname -m) in x86_64 | amd64) echo amd64 ;; *) echo arm64 ;; esac)
printf '#!/bin/sh\necho "fleet v9.9.9"\n' >"$tmp/release/$asset"
(cd "$tmp/release" && sha256sum "$asset" >SHA256SUMS)

# run <name> [VAR=value...]: install into a fresh dir; output in $out, status in $rc.
run() {
	name=$1; shift
	dir=$tmp/$name
	: >"$tmp/gh.log"
	out=$(env -i PATH="$bin" HOME="$tmp" FLEET_VERSION=v9.9.9 FLEET_INSTALL_DIR="$dir" \
		FLEET_BASE_URL=https://example.invalid/releases FAKE_RELEASE="$tmp/release" \
		FAKE_GH_LOG="$tmp/gh.log" "$@" sh "$script" 2>&1)
	rc=$?
}
fail() { echo "FAIL: $*"; echo "$out" | sed 's/^/    /'; failed=1; }
with_gh() { ln -sf "$tmp/gh" "$bin/gh"; }
without_gh() { rm -f "$bin/gh"; }

with_gh
run verified
[[ $rc == 0 && -x $dir/fleet ]] || fail "verified: not installed (rc=$rc)"
grep -q "^attestation verify .*/$asset -R sturlabragason/fleet " "$tmp/gh.log" || fail "verified: gh not called as expected: $(cat "$tmp/gh.log")"
[[ $out == *"provenance ok"* ]] || fail "verified: no 'provenance ok' line"

run mismatch FAKE_GH_VERIFY=1
[[ $rc != 0 ]] || fail "mismatch: exit 0"
[[ -z $(ls -A "$dir" 2>/dev/null) ]] || fail "mismatch: $dir is not empty: $(ls -A "$dir")"
[[ $out == *"not installing"* ]] || fail "mismatch: no 'not installing' in the error"

run logged-out FAKE_GH_AUTH=1
[[ $rc == 0 && -x $dir/fleet ]] || fail "logged-out: not installed (rc=$rc)"
grep -q "^attestation" "$tmp/gh.log" && fail "logged-out: gh attestation was called"
[[ $out == *"provenance not checked"* ]] || fail "logged-out: no 'provenance not checked' note"

run skipped FLEET_SKIP_ATTEST=1 FAKE_GH_VERIFY=1
[[ $rc == 0 && -x $dir/fleet ]] || fail "skipped: not installed (rc=$rc)"
grep -q "^attestation" "$tmp/gh.log" && fail "skipped: gh attestation was called"
[[ $out == *"provenance not checked"* ]] || fail "skipped: no 'provenance not checked' note"

without_gh
run no-gh
[[ $rc == 0 && -x $dir/fleet ]] || fail "no-gh: not installed (rc=$rc)"
[[ $out == *"provenance not checked"* ]] || fail "no-gh: no 'provenance not checked' note"

[[ $failed == 0 ]] && echo "install: ok"
exit $failed
