#!/bin/sh
# Install fleet from a GitHub release (ADR-78).
#
#   curl -fsSL https://raw.githubusercontent.com/sturlabragason/fleet/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --dry-run
#
# Environment:
#   FLEET_VERSION      release tag to install (vX.Y.Z); default: the latest release
#   FLEET_INSTALL_DIR  where the binary goes; default: ~/.local/bin
#   FLEET_SKIP_ATTEST  set to 1 to skip the provenance check
#
# Downloads fleet-<os>-<arch> and SHA256SUMS, refuses on a checksum mismatch
# and, when gh is installed and logged in, on a failed provenance check
# (gh attestation verify, ADR-103). Installs without sudo and never edits shell rc files. Running it again
# replaces the binary in place.
set -eu

REPO=sturlabragason/fleet
BASE_URL=${FLEET_BASE_URL:-https://github.com/$REPO/releases}

say() { printf 'fleet-install: %s\n' "$*"; }
die() { printf 'fleet-install: error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<EOF
Install fleet from a GitHub release.

Usage: install.sh [--dry-run] [--help]

  --dry-run   show what would be downloaded and where it would go; change nothing

Environment:
  FLEET_VERSION      release tag (vX.Y.Z); default: the latest release
  FLEET_INSTALL_DIR  install directory; default: \$HOME/.local/bin
  FLEET_SKIP_ATTEST  1 = skip the provenance check (gh attestation verify)
EOF
}

# Targets match .goreleaser.yaml: linux and darwin, amd64 and arm64.
detect_target() {
	os=$(uname -s)
	case $os in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported OS '$os': releases are built for linux and darwin (build from source: go install github.com/$REPO/cmd/fleet@latest)" ;;
	esac
	arch=$(uname -m)
	case $arch in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture '$arch': releases are built for amd64 and arm64 (build from source: go install github.com/$REPO/cmd/fleet@latest)" ;;
	esac
	echo "$os-$arch"
}

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
	# The redirect of /releases/latest names the tag: no API call, no rate limit.
	latest_tag() {
		u=$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "$BASE_URL/latest") || return 1
		echo "${u##*/}"
	}
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
	# wget and wget2 print redirects differently, so ask the API instead.
	latest_tag() {
		wget -q -O - "https://api.github.com/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
	}
else
	die "need curl or wget"
fi

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/^.*= *//'
	else
		die "need sha256sum, shasum or openssl to verify the download"
	fi
}

resolve_version() {
	if [ -n "${FLEET_VERSION:-}" ]; then
		v=$FLEET_VERSION
	else
		v=$(latest_tag) || v=
		[ -n "$v" ] || die "could not find the latest release of $REPO (set FLEET_VERSION=vX.Y.Z)"
	fi
	case $v in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	[0-9]*.[0-9]*.[0-9]*) v=v$v ;;
	*) die "not a release tag: '$v' (want vX.Y.Z)" ;;
	esac
	echo "$v"
}

# Checks that the release workflow of $REPO built the binary (ADR-103).
# Without gh, or with gh logged out, the SHA-256 check is all there is.
verify_provenance() {
	if [ "${FLEET_SKIP_ATTEST:-}" = 1 ]; then
		say "provenance not checked (FLEET_SKIP_ATTEST=1)"
	elif ! command -v gh >/dev/null 2>&1; then
		say "provenance not checked (needs gh: https://cli.github.com)"
	elif ! gh auth token >/dev/null 2>&1; then
		say "provenance not checked (gh is not logged in: gh auth login)"
	elif gh attestation verify "$1" -R "$REPO" --signer-workflow "$REPO/.github/workflows/release.yml" >/dev/null; then
		say "provenance ok (built by $REPO release.yml)"
	else
		die "gh attestation verify failed for ${1##*/}; not installing (releases made before ADR-103 have no attestation; FLEET_SKIP_ATTEST=1 relies on the SHA-256 check alone)"
	fi
}

# Everything runs from main, called on the last line, so a download cut
# short by the network runs nothing.
main() {
	dry_run=0
	for arg in "$@"; do
		case $arg in
		--dry-run | -n) dry_run=1 ;;
		--help | -h) usage; exit 0 ;;
		*) usage >&2; die "unknown argument: $arg" ;;
		esac
	done

	target=$(detect_target)
	version=$(resolve_version)
	dir=${FLEET_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}
	asset=fleet-$target
	url=$BASE_URL/download/$version

	say "fleet $version for $target"
	say "from    $url/$asset"
	say "to      $dir/fleet"

	if [ "$dry_run" = 1 ]; then
		say "dry run: nothing downloaded or installed"
		exit 0
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t fleet-install)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 130' INT TERM

	fetch "$url/$asset" "$tmp/$asset" || die "download failed: $url/$asset (is $version a release with a $target binary?)"
	fetch "$url/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $url/SHA256SUMS"

	want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
	[ -n "$want" ] || die "SHA256SUMS has no line for $asset"
	got=$(sha256 "$tmp/$asset")
	[ "$got" = "$want" ] || die "checksum mismatch for $asset: SHA256SUMS says $want, the download is $got; not installing"
	say "sha256  ok ($got)"
	verify_provenance "$tmp/$asset"

	mkdir -p "$dir" || die "cannot create $dir (set FLEET_INSTALL_DIR to a directory you can write)"
	chmod 755 "$tmp/$asset"
	# Copy next to the target, then rename: an upgrade replaces the file in one
	# step, even while an old fleet is running from it.
	cp "$tmp/$asset" "$dir/.fleet.new.$$" || die "cannot write to $dir (set FLEET_INSTALL_DIR to a directory you can write)"
	mv -f "$dir/.fleet.new.$$" "$dir/fleet"

	say "installed: $("$dir/fleet" version 2>/dev/null || echo "$dir/fleet")"

	case :${PATH:-}: in
	*:"$dir":*) ;;
	*)
		say "$dir is not on your PATH. Add it in your shell's rc file, e.g.:"
		say "  export PATH=\"$dir:\$PATH\""
		;;
	esac

	cat <<EOF

Next:
  fleet up        start the fleet (needs tmux and an agent CLI such as copilot)
  fleet --help    all commands; README: https://github.com/$REPO#quickstart-fake-agents-no-tokens
EOF
}

main "$@"
