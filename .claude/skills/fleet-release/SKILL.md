---
name: fleet-release
description: Cuts a fleet release - public check, fast-forward push of main, CI wait, then the release workflow (tag, linux+darwin binaries, SHA256SUMS, GitHub release; ADR-73). Use when asked to "publish", "release vX.Y.Z", "push main", "cut a release" or "tag" in this repo, or when spawned as the designated publisher.
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
scripts/check-public.sh origin/main..$REL  # emails, messages, every added line, deny list, gitleaks
scripts/check-public.sh --gh               # issue, comment and release text already on GitHub
```

CI runs the same check with the deny list and emails from the `PUBLIC_DENY` and `PUBLIC_EMAILS`
repository secrets. When you add a line to `.git/info/public-deny`, update the secret too:
`grep -v -e '^#' -e '^$' .git/info/public-deny | gh secret set PUBLIC_DENY`.

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

The release workflow (`.github/workflows/release.yml`, ADR-73) does the rest: it picks the next
version from the conventional commits since the last tag (`scripts/next-version.sh`), tags main's
head, builds linux and darwin × amd64 and arm64 with goreleaser, writes `SHA256SUMS`, and takes the
notes from the `CHANGELOG.md` lines added since the last tag (`scripts/release-notes.sh`). It
refuses a ref other than main, a commit without green CI, or a tag that exists.

Dry-run first; it is the default and publishes nothing:

```sh
gh workflow run release.yml -R sturlabragason/fleet
run=$(gh run list -R sturlabragason/fleet -w release.yml -L1 --json databaseId -q '.[0].databaseId')
gh run watch -R sturlabragason/fleet "$run" --exit-status
gh run view -R sturlabragason/fleet "$run"          # the summary shows the version and notes
```

Check that the version and the notes are what the user approved, then release:

```sh
gh workflow run release.yml -R sturlabragason/fleet -f dry_run=false          # next version
gh workflow run release.yml -R sturlabragason/fleet -f dry_run=false -f version=vX.Y.Z  # named one
```

Watch it the same way. Without `gh workflow run` (offline Actions, a fork), the old manual steps
are `scripts/next-version.sh`, `git tag -a`, `git push origin <tag>` and
`goreleaser release --clean --release-notes <(scripts/release-notes.sh)`.

## After

- Check the release: `gh release view vX.Y.Z -R sturlabragason/fleet` lists five files
  (`fleet-{linux,darwin}-{amd64,arm64}`, `SHA256SUMS`).
- Report: the commit pushed, the tag, the release URL, CI result, and anything the check made you
  leave out of this release.
- Record it with `fleet note` so the next publisher knows where the line is.
- Sweep the CI branches: `fleet branches` lists the `fleet/` branches on origin; `fleet branches --prune`
  deletes the merged ones no live or parked agent is on (ADR-87). Unmerged ones stay: name them in
  the report.

## Stop and ask the user

A release is public and irreversible. Ask before: pushing a commit the user did not name, tagging a
version they did not name, releasing while another agent is still merging into `main`, or anything
that would need a force-push to undo. "Use best judgment" does not cover any of these.
