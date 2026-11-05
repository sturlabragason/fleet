---
name: fleet-release
description: Cuts a fleet release - public check, fast-forward push of main, tag, linux binaries and SHA256SUMS, GitHub release, CI wait. Use when asked to "publish", "release vX.Y.Z", "push main", "cut a release" or "tag" in this repo, or when spawned as the designated publisher.
---

# fleet-release

You are the **designated publisher**. Nobody else pushes. The worker playbook forbids pushing; this
skill is the exception a spawn prompt grants, and only for the version the user named.

Local-only, never committed: `PUBLISHING.md` (the original cutover record, in the main checkout and
in `.gitignore`), `.git/info/public-emails`, `.git/info/public-deny`, `.git/info/public-allow`.
Read `PUBLISHING.md` once before your first release; the day-to-day flow is below.

## Rules that cost money to break

- **Never rewrite published history.** `v0.1.0` is in `proxy.golang.org` and `sum.golang.org`.
  Re-pointing a published tag, or force-pushing `main`, gives every consumer a checksum mismatch
  that no fix on your side clears. Squash at *branch* level before merging instead (playbook item 6).
- **Fast-forward only.** `git push` with no `--force`, no `--force-with-lease`, ever.
- **Never `git push --tags` or `--mirror`.** The `private/*` tags hold the pre-publication history.
  The pre-push hook refuses them; do not rely on it.
- **Never `--no-verify`.** If the hook blocks you, the commit is wrong, not the hook.
- Push `main` only. No other branch has been checked.

## Before you push

Push a known commit, not "whatever is on main now" — other agents are merging while you work. The
spawn prompt names the release commit, or a fallback. Pin it:

```sh
cd <the main checkout>          # releases are cut from main, not from an agent worktree
git log --oneline -1            # confirm this is the commit the user approved
REL=$(git rev-parse HEAD)
```

Then, in order — stop at the first failure and report `blocked` with the output:

```sh
git status --short                         # must be empty
make build && go vet ./... && go test ./...
scripts/check-public.sh origin/main..$REL  # emails, home paths, deny list, stray emails, gitleaks
```

`check-public.sh` failing is the common case. It has caught an employer name in a test fixture more
than once. Fix the content on a branch, merge it, and re-pin `REL` — never push past it.

For a second opinion before the push, dispatch the **fleet-publish-check** agent with the range.

## Push and release

```sh
git push origin main                        # the pre-push hook re-runs the check
run=$(gh run list -R sturlabragason/fleet -b main -L1 --json databaseId -q '.[0].databaseId')
gh run watch -R sturlabragason/fleet "$run" --exit-status
```

`gh run watch` needs a run id: without one it fails in a non-interactive shell, and the CI gate is
silently skipped. Wait for CI green before tagging — a tag on a red commit is a tag you cannot move.

```sh
git tag -a vX.Y.Z -m "vX.Y.Z" $REL
git push origin vX.Y.Z                      # the one tag, by name; never --tags

mkdir -p dist && rm -f dist/*
for a in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$a go build -trimpath -ldflags "-s -w" -o dist/fleet-linux-$a ./cmd/fleet
done
(cd dist && sha256sum fleet-* > SHA256SUMS)

gh release create vX.Y.Z -R sturlabragason/fleet dist/* --title vX.Y.Z --notes "$(…)"
```

Release notes come from `CHANGELOG.md`: the lines added since the previous tag, in the changelog's
own words. Do not write new prose for them.

## After

- `rm -rf dist` — literal path only.
- Report: the commit pushed, the tag, the release URL, CI result, and anything the check made you
  leave out of this release.
- Record it with `fleet note` so the next publisher knows where the line is.

## Stop and ask the user

A release is public and irreversible. Ask before: pushing a commit the user did not name, tagging a
version they did not name, releasing while another agent is still merging into `main`, or anything
that would need a force-push to undo. "Use best judgment" does not cover any of these.
