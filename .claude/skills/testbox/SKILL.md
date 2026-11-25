---
name: testbox
description: Use the disposable Finallyshell Docker test machine when a test or manual flow needs a real Linux server, live SSH, Caddy, Docker, systemd, firewall or sudo, or when reproducing a user's server issue.
---

# Finallyshell testbox

Use `fs-testbox` for every Finallyshell test or manual flow that needs a real
Linux server. Prefer existing in-process fixtures when live services are not
needed. For setup and prerequisites, see [Testing](../../../docs/en/testing.md).

`fs-testbox` is a disposable Debian 12 container built from
`finallyshell-testbox:debian12`. It runs systemd as PID 1, OpenSSH with root and
password login, and a `tester` account whose `sudo` prompts for its password.
The container is privileged, uses the host cgroup namespace and a writable
`/sys/fs/cgroup` mount, and has tmpfs mounts for `/run` and `/run/lock`. Its
published ports are **loopback only**: SSH `127.0.0.1:2222`, HTTP
`127.0.0.1:8080`, HTTPS `127.0.0.1:8443`.

## Rules

- Never run live tests or mutating manual flows against real or production
  servers. Use this box even for live tests that do not need a mutation flag:
  older tests, including `TestSSH`, can upload files without it.
- Require `FINALLYSHELL_TEST_MUTATE=1` for tests that change server state.
  Enable it for a single Go command only after checking `status` and using
  `ssh-env` to select the local box. The flag never makes another host safe.
- Keep credentials outside the checkout in `TESTBOX_ENV` or, by default,
  `$HOME/.finallyshell-testbox.env`. Keep that file private (`0600` on Unix,
  a restricted ACL on Windows). Never commit it, copy it into the box, paste
  passwords into logs or PRs, or use `set -x` around commands that read them.
  On the coordinator's Windows host the existing file is
  `C:/Users/Administrator/codex-briefs/testbox/testbox.env`; set `TESTBOX_ENV`
  to that path before using the existing box. `up` applies the selected file's
  passwords even when the box already exists; use `status` and `ssh-env` if
  you must not change them.
- Keep test data synthetic. Do not mount host data or the host Docker socket
  into this privileged container. Do not share snapshots.

## Commands

From the repo root, run `.claude/skills/testbox/testbox.sh <command>` in Bash
(Git Bash on Windows).

| Command | Effect |
| --- | --- |
| `up` | Starts Docker Desktop on Windows if needed, builds the base image if absent, creates or starts the box, generates missing credentials, applies the selected file's credentials, and returns when systemd is running or degraded and SSH is active. |
| `down` | Stops the box without removing it. |
| `reset` | Removes and recreates the box from the base image, keeping the external credential file. Removes Caddy and any other changes made after creation. |
| `ssh-env [root\|tester]` | Prints the Bash export for the requested account, defaulting to root. **Stdout contains the SSH password.** Consume it without displaying it. Does not change the box. |
| `exec [COMMAND [ARG...]]` | Runs a command as root in the box; opens Bash with no arguments. |
| `status` | Shows the container, port mappings and service states without changing anything. |
| `caddy` | Installs Caddy 2.11.9 from the official Cloudsmith stable apt repository and enables it. Overwrites the sample Caddyfile, `sites/apps.caddy` and `/srv/www/index.html`; safe to repeat when those fixtures are wanted. |
| `snapshot NAME` | Saves the box's filesystem as the local image `finallyshell-testbox:NAME`. Reusing a name replaces the tag; `debian12` is reserved. |
| `restore NAME` | Replaces the box with one created from the named local snapshot and applies the selected SSH credentials. |

## Live Go tests

```bash
(
    set -e
    set +x
    box=.claude/skills/testbox/testbox.sh
    "$box" status
    ssh_export=$("$box" ssh-env tester)
    eval "$ssh_export"
    unset ssh_export
    FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestSSH$' -count=1 -v
)
```

`connectTestServer` in `internal/app/ssh_test.go` reads
`FINALLYSHELL_TEST_SSH` in `user:password@127.0.0.1:2222` format. For a Caddy
run, use `caddy`, select `ssh-env root`, and target `^TestCaddyLive` on
branches that have those tests. Run read-only cases without the mutation
flag, and set `FINALLYSHELL_TEST_MUTATE=1` only for cases that make changes.
Check the selected tests first: older SSH cases do not enforce the opt-in,
and live tunnel cases that expect host and server to share loopback do not
work unchanged across the container's network boundary.

## Components and snapshots

Use `exec apt-get update` and `exec apt-get install -y PACKAGE` for extra
components. For Docker flows, install `docker.io` inside the privileged box
and start its own daemon with `exec systemctl enable --now docker`.

The Caddy fixture uses `/etc/caddy/Caddyfile` and `sites/apps.caddy` under
`/etc/caddy`. It exercises global options, reusable security and parameterized
logging snippets, imports, static files, internal TLS, a matcher with load
balancing and health checks, SPA fallback, basic auth, and redirects. The
proxy backends are placeholders; start synthetic backends inside the box
if you need successful upstream responses. The basic-auth fixture is not
an SSH credential; configure a separate throwaway login for auth tests.

Snapshot before a destructive variant if you need to preserve a synthetic
setup. `docker commit` saves the writable filesystem, not running processes,
tmpfs mounts or nested Docker volumes. A snapshot can contain local password
hashes and Caddy CA keys; keep it local, never push or export it. `reset`
returns to the clean base image, unlike `down` or `restore`. Coordinate with
other agents before changing the shared box.

## Reproduce a user's server issue

Only with authorized read-only access, gather the minimum relevant facts:
OS and package versions (`cat /etc/os-release`, `dpkg-query -W`), service
state (`systemctl is-active`), listeners (`ss -lnt`), or selected non-secret
configuration and log excerpts. Never write files, install packages,
restart services, run tests, or change firewall rules on the user's server.
Keep any inventory outside the repository, redact private details and
replace credentials, addresses and data with synthetic values. Recreate
only the relevant package versions, service configuration and fixtures
inside `fs-testbox`; reproduce and verify the fix there.

## Troubleshooting

- **Docker unavailable:** On Windows `up` can launch Docker Desktop from
  `C:\Program Files\Docker\Docker\Docker Desktop.exe`. Start it manually if
  it is installed elsewhere; wait for `docker info` to succeed. Use a local
  Linux container engine, not a remote context or Windows containers.
- **Windows paths:** The script sets `MSYS_NO_PATHCONV=1`. Set it yourself
  for direct `docker run` or `docker exec` calls in Git Bash so paths such
  as `/sys/fs/cgroup` reach the Linux engine unchanged.
- **systemd or SSH not ready:** `up` waits up to two minutes and accepts
  `degraded` only when SSH is active. Check `status`, `docker logs fs-testbox`,
  and `exec journalctl -u ssh.service -b`. Verify the local engine supports
  privileged containers and the cgroup mount.
- **Port 2222 already in use:** Check `docker ps` and the host's listeners
  (`Get-NetTCPConnection -LocalPort 2222` on Windows, `lsof -iTCP:2222` on
  macOS or Linux). Resolve the conflict before retrying. Never use the
  service already on that port as a substitute test target.
