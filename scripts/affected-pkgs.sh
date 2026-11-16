#!/usr/bin/env bash
# affected-pkgs.sh [base]: print the Go packages whose tests a change since
# the merge base with base (default origin/main) can affect, one per line, or
# ./... when it can reach every package (ADR-149). Run at the module root.
# A package is affected when its test binary depends on a changed package,
# when one of its tests names a changed non-Go file by path, or, for any Go
# change, when its tests build or run a binary or walk the source tree.
set -euo pipefail
base=${1:-origin/main}
mb=$(git merge-base "$base" HEAD)
mapfile -t files < <(git diff --name-only "$mb" HEAD)

for f in "${files[@]}"; do
	case $f in
	go.mod | go.sum | Makefile | .github/workflows/ci.yml | scripts/affected-pkgs.sh)
		echo ./...
		exit
		;;
	esac
done

root=$PWD
declare -A pkg=() changed=() out=()
while read -r dir ip; do pkg[$dir]=$ip; done < <(go list -f '{{.Dir}} {{.ImportPath}}' ./...)
dirpkg() { [[ -n ${pkg[$root/$1]:-} ]] && echo "${pkg[$root/$1]}"; }

gochange=
for f in "${files[@]}"; do
	[[ $f == *.go ]] && gochange=1
	# A file belongs to the nearest package at or above it (testdata, embeds).
	d=$root/$(dirname "$f")
	while [[ -z ${pkg[$d]:-} && $d != "$root" ]]; do d=$(dirname "$d"); done
	[[ -n ${pkg[$d]:-} ]] && changed[${pkg[$d]}]=1
	if [[ $f != *.go ]]; then
		while read -r t; do
			p=$(dirpkg "$(dirname "${t#./}")") && out[$p]=1
		done < <(grep -rlF --include='*_test.go' -e "\"$f\"" -e "/$f\"" . || true)
	fi
done

if [[ -n $gochange ]]; then
	while read -r t; do
		p=$(dirpkg "$(dirname "${t#./}")") && out[$p]=1
	done < <(grep -rlE --include='*_test.go' '"build"|bin/fleet|WalkDir|filepath\.Walk' . || true)
fi

# One line per test binary: "<pkg>.test dep dep ...", test-only deps included.
while read -r ip deps; do
	p=${ip%.test}
	# A dep "q [p.test]" splits into "q" and "[p.test]"; the latter never matches.
	for d in $p $deps; do
		if [[ -n ${changed[$d]:-} ]]; then
			out[$p]=1
			break
		fi
	done
done < <(go list -test -f '{{.ImportPath}} {{join .Deps " "}}' ./... | grep '^[^ ]*\.test ')

for p in "${!out[@]}"; do echo "$p"; done | sort
