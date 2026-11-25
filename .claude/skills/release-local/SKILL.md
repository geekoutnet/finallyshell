---
name: release-local
description: Publish a GitHub release of dev's HEAD from packages built locally with the docker-build skill (all five platforms, same files and notes as the dev-build workflow). Only when the owner invokes /release-local.
disable-model-invocation: true
---

# Release from local packages

The owner runs this by hand (`/release-local`, optionally with a version such
as `2.0.20`). Local Docker packaging takes seconds where the dev-build
workflow takes many minutes; this publishes those packages as the next
`v2.0.N` release of `geekoutnet/finallyshell`, exactly like a dev-build
release: the same twelve files, the same names, notes with the commits
since the last release and `.github/release-install.md`, marked latest,
unsigned (no update manifests).

Run it yourself from the repository root (Bash; Git Bash on Windows), in
the background, and report each step as it happens:

```bash
rel=.claude/skills/release-local/release.sh
"$rel" plan                       # checks, version, commit, file list, notes
"$rel" publish                    # build, verify, draft, upload, publish
"$rel" publish --version 2.0.20   # a chosen version
"$rel" publish --reuse            # upload dist/docker/<version>/ as built
```

1. Run `plan` and show the owner the version, commit and notes' first
   lines. Invoking the skill is the owner's go-ahead: continue with
   `publish` unless `plan` failed or something looks wrong.
2. `publish` stops before doing anything when: the checkout is not on
   `dev`, has uncommitted changes, or differs from `origin/dev` (push
   first); the commit already has a `v2.0.*` release; the version is taken
   by a published release or a tag. It never pushes commits or tags itself.
3. It builds the five platforms with the docker-build skill at the plain
   version (no `-local`), verifies them, writes `SHA256SUMS`, creates the
   release as a **draft** at the commit, uploads every file with retries,
   checks each uploaded size, then publishes it as the latest release.
   A failed upload leaves the draft: run `publish` again to resume it.
4. Report the release URL and the files.

Notes:
- `--reuse` skips the build; use it only for a `dist/docker/<version>/`
  built from this same commit.
- The dev-build workflow skips a commit that already has a release, and
  numbers its next release from the newest tag, so local and online
  releases don't collide.
- The macOS apps are ad-hoc signed and not notarized, as in every dev
  build; the release notes tell users how to open them.
- Never delete a published release or tag without the owner asking.
