# Testing on a real machine

English · [简体中文](../zh-CN/testing.md)

## Purpose

Use the disposable Docker test machine for all Finallyshell automated tests
and manual flows that need a real Linux server. It runs real services for
SSH, file transfers, systemd, sudo, firewall, Caddy and Docker scenarios.
Unit and in-process tests do not need it. Agents should also read the
[testbox skill](../../.claude/skills/testbox/SKILL.md).

## Requirements

- A local Linux Docker engine with privileged container and cgroup support:
  Docker Desktop on Windows or macOS, or Docker Engine on Linux.
- Bash (Git Bash on Windows) and Python 3 to generate credentials on first
  use. Use the Go toolchain from `go.mod` to run the Go tests.
- Local ports `2222`, `8080` and `8443` must be free unless the testbox
  already owns them. `docker info` should succeed. On Windows `up` can
  launch Docker Desktop from its standard install path if needed.

## Quick start

Run from the repo root in Bash. To reuse the box already set up on the
coordinator's Windows host, first set `TESTBOX_ENV` to
`C:/Users/Administrator/codex-briefs/testbox/testbox.env`.

```bash
(
    set -e
    set +x
    box=.claude/skills/testbox/testbox.sh
    "$box" up
    "$box" status
    ssh_export=$("$box" ssh-env tester)
    eval "$ssh_export"
    unset ssh_export
    FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestSSH$' -count=1 -v
)
```

`up` creates or starts the container and applies the credentials from the
selected file. If it is missing, `up` creates it outside the checkout at
`$HOME/.finallyshell-testbox.env` by default. `ssh-env` prints a password
to stdout: capture it as shown, do not run it on its own or with shell tracing.

## What's inside

- `fs-testbox` runs Debian 12 (`finallyshell-testbox:debian12`), with systemd
  as PID 1 and OpenSSH. Root can log in with a password; `tester` is in the
  `sudo` group and must enter its own password to escalate.
- The image includes networking and process tools, Python, UFW, cron,
  rsyslog, logrotate, archive tools, SQLite, jq and Git. Nginx and Fail2ban
  are installed but disabled; getty and logind are masked.
- Host bindings are `127.0.0.1:2222` → SSH, `127.0.0.1:8080` → HTTP and
  `127.0.0.1:8443` → HTTPS. The container is privileged so it can run
  systemd and support nested Docker.
- The coordinator's box already has Caddy 2.11.9 running. On a fresh box,
  run `.claude/skills/testbox/testbox.sh caddy` to install and enable it from
  the official Cloudsmith stable apt repository and write the sample sites
  under `/etc/caddy`. They cover static files, imports, internal TLS,
  reverse proxies, SPA fallback, basic auth and redirects.

## Safety rules

- Never point live tests or mutating manual flows at a real or production
  server. For a user report, collect only authorized read-only information
  and recreate a sanitized case in the box.
- Keep the local credential file private (`0600` on Unix; restrict the ACL on
  Windows). It holds `ROOT_PW`, `TESTER_PW` and `SSH`. Never commit or log
  passwords, copy the file into a container, or publish it in a PR.
- Keep published ports on loopback. Do not use this privileged container as
  a security sandbox for untrusted code, or attach host data or Docker sockets.

## Live tests and mutation opt-in

`FINALLYSHELL_TEST_SSH` enables live cases and has the form
`user:password@127.0.0.1:2222`. Use `ssh-env tester` for normal SSH flows and
`ssh-env root` when root is required. Without that variable, live-only cases
skip, and tests with in-process fixtures can still run.

`TestSSH` in `internal/app` runs terminal, monitoring, and file-transfer
checks. On branches with `TestCaddyLive*`, install the Caddy fixture, load
the root credentials, then run `go test ./internal/app -run '^TestCaddyLive' -count=1 -v`.
For cases that change the server, prefix the command with
`FINALLYSHELL_TEST_MUTATE=1`. Some older SSH tests write files or start processes
without checking that flag, so do not treat an unset flag as a read-only
guarantee. For tunnel cases that assume a shared loopback network, leave
`FINALLYSHELL_TEST_SSH` unset and use the in-process fixture.

With the Caddy fixture running, load credentials with `ssh-env` and run
`FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestDesktopLive(Task|Settings|Browser|Apps)' -count=1 -v -timeout 900s`
once as root and once as tester. Run the mutating passes sequentially:
they change and restore the time zone. Mounted DNS is never rewritten;
the apply/restore flow uses a writable scratch copy on that box. Omit the
mutation flag for the read-only cases. Set `SHOT_DIR` to an existing local
directory to capture the windows. The all-apps smoke test allows about
20 seconds per window to catch slow reads and UI freezes.

For the desktop Files, This PC, Trash and Terminal live tests, run the
following from Bash against the running testbox. Run it once per account;
the `tester` run exercises the sudo prompt. The archive and interoperability
cases use `zip`, `unzip` and `trash-cli`; the remote download uses the Caddy
fixture. Set `SHOT_DIR` to an existing local directory to save screenshots.
Set `TESTBOX_ENV` first if the credentials are not in the default location.

```bash
for account in root tester; do
    (
        set -e
        set +x
        ssh_export=$(.claude/skills/testbox/testbox.sh ssh-env "$account")
        eval "$ssh_export"
        unset ssh_export
        FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestDesktopLive(Files|Trash|Terminal)' -count=1 -v -timeout 900s
    ) || exit 1
done
```

## Adding components

Install extras inside the disposable container, not on the host:

```bash
.claude/skills/testbox/testbox.sh exec apt-get update
.claude/skills/testbox/testbox.sh exec apt-get install -y docker.io
.claude/skills/testbox/testbox.sh exec systemctl enable --now docker
```

Use the Docker daemon inside the box for Docker tests. The `caddy` command
rewrites its sample configuration and web page, so save a snapshot first
if you need to keep custom test settings.

## Reset and snapshots

- `down` stops the box and keeps its files. `up` starts it again.
- `reset` discards the container and recreates the clean base image. It
  keeps the external credentials but removes later additions, including Caddy.
- `snapshot NAME` saves the writable filesystem to the local image
  `finallyshell-testbox:NAME`. `restore NAME` replaces the box with a new one
  from that image. Running processes, tmpfs contents and mounted volumes
  are not saved. The images can contain password hashes and local TLS keys;
  never push or share them.
