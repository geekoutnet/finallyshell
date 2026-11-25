---
name: docker-build
description: Use for every local packaging or release-artifact build of Finallyshell, including when the owner asks to package the app or build installers locally. Builds all five targets in Docker, including macOS arm64 and Intel DMGs without macOS, Xcode or an Apple SDK.
---

# Finallyshell Docker packaging

Use the local Linux Docker engine and Bash (Git Bash on Windows). Run from
the repository root:

```bash
pack=.claude/skills/docker-build/build.sh
"$pack" build --version 2.0.18-local
"$pack" verify dist/docker/2.0.18-local
```

| Command | Effect |
| --- | --- |
| `image` | Build the image tagged by the Dockerfile SHA-256; `build` also does this if the tag is absent. |
| `build [--version X] [-j N] [--no-dmg] [TARGET ...]` | Package all five targets or choose `mac`, `linux`, `win` or explicit `OS/ARCH` targets. |
| `verify DIR [--no-dmg] [TARGET ...]` | Verify checksums, package layouts, architectures and ad-hoc Mach-O signatures. Supply the same target selection and `--no-dmg` as for the build. |
| `shell` | Start Bash with the three build caches and a read-only checkout at `/checkout`. Work under `/work/src`, not on the mount. |
| `clean` | Remove this Dockerfile's image tag and the three labelled Docker caches; keep artifacts. |

- Targets: `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, and `windows/amd64`.
- Without `--version`, use the largest local `v2.0.*` numeric tag plus one, with `-local` appended. No tags are fetched or created.
- Uncommitted tracked and unignored files are included. The checkout is not modified. Each successful run replaces `dist/docker/<version>/` with its selected artifacts; a failed build leaves previous artifacts in place. Use different versions to keep separate runs.
- Builds use temporary containers with separate platform source copies. Named Docker volumes retain Go modules, compiled Go objects, and MyGo natives. CPU count and available memory cap concurrency; `-j 1` runs sequentially. Set `FINALLYSHELL_BUILD_CACHE=finallyshell-build.<name>` for isolated measurements; use the same setting for cleanup.
- No upload, update signing key, or macOS notarization. The Mac bundles use ad-hoc signatures, including the Ghostty library; `--no-dmg` saves them as `.app.tar` to preserve Unix permissions on Windows. See [development](../../../docs/en/development.md#building-with-docker) for first launch and the verification limits.
- The pinned `rcodesign verify` works on Mach-O files, not bundle directories. It reports an empty CMS slot as an error even for its own ad-hoc signatures; `verify` allows only that exact diagnostic and also checks code pages, the sealed `Info.plist` and `CodeResources`, and resource hashes independently.
- `GOPROXY`, `GOFLAGS`, and HTTP(S)/no-proxy settings are passed through when set. Failed downloads use the CI retry script. Do not substitute an Apple SDK or a remote Docker engine.
