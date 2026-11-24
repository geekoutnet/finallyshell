# Roadmap

This document records what Finallyshell (the `dev` branch of finallyshell) has done, what is in progress and what comes next, with the scope and constraints of each feature. Add new ideas to the matching section; move an item to "In progress" when work starts, and to "Released" with its version once published.

How features behave is in [features.md](features.md), settings and files in [configuration.md](configuration.md), the code in [development.md](development.md). This document is only about plans.

## Positioning and principles

- **An SSH client first, a client-side server panel second.** The terminal and file experience of FinalShell, Xshell, MobaXterm, Termius and WindTerm, plus the most used server management features of BaoTa, 1Panel, DPanel and Portainer, brought into the client. Nothing is installed on the server; everything goes over SSH.
- **The common 80% of each feature.** No feature catalog: rarely used parts that cost a lot to maintain are left out or postponed.
- **Production safety first.** The users' servers are production:
  - Changes (remove, prune, recreate, restart a service, change the firewall or system settings) always ask first, saying what will change and what follows. What can be undone can be undone; what can be backed up is backed up first.
  - Tests use fakes only (injectable command runners, in-memory services), never a mutating command on a real server.
  - Commands single-quote every argument and validate names and IDs; user input never goes into a shell unquoted.
- **Everything that needs root goes through escalation.** The order is in [Escalation](#escalation): escalate automatically when possible, and only when it really fails, say so and tell the user how to grant access.
- **Passwords and keys stay off disk.** They go only over the encrypted SSH channel to sudo, su or `--password-stdin`, never into command lines, process lists, logs, output views or screenshots. Those to remember go to the system credential store.
- **Both languages together.** UI text goes into `locales/en.json` and `locales/zh-Hans.json`; docs change in `docs/en/` and `docs/zh-CN/` together, with the same headings in the same order.

## Released

Every push to `dev` builds and publishes `v2.0.N`; downloads are on GitHub Releases.

| Version | What |
|---|---|
| v2.0.0 | Own release workflow: `main` mirrors upstream, only `dev` builds; a platform that fails does not hold back the others |
| v2.0.1 | Process list on CentOS 7 no longer empty; wider grip areas and visible handles on the split dividers |
| v2.0.2 | Update checks look at this repository |
| v2.0.3 | The Windows installer version downloads updates in the app through several proxies, installs silently after quitting and restarts |
| v2.0.4 | A build to try the upgrade |
| v2.0.5 | A timestamp on the version check so proxies don't serve a cached version |
| v2.0.6 | The Overview tab; view and copy connection info; the connection manager button |
| v2.0.7 | Windows Remote Desktop through the system's client |
| v2.0.8 | Read-only Docker details in the Overview: containers, ports, mounts, environment, networks |
| v2.0.9 | Overview layout; distinct icons and a type filter for SSH and Remote Desktop; the Docker container context menu |
| v2.0.10 | Select several or all connections in the manager and delete them at once |
| v2.0.11 | Sync backups: versions kept on the server, local snapshots, preview, restore, mass-deletion guard |
| v2.0.12 | The Overview's Memory card: physical memory, swap, virtual memory; drop caches and compact memory |

## In progress

### Docker tab

A "Docker" tab in the bottom panel, opened from a button on the Overview's Docker card. A segmented row switches between six sections, built in parallel by four Codex agents, each changing only its own files:

| Section | What | Files |
|---|---|---|
| Containers | New container form (ports, volumes, environment, networks, resource limits, with the matching `docker run`); edit by recreating safely with rollback; duplicate; batch start, stop and remove; live usage charts; browse files in a container, upload and download; commit to an image, export | `dockerpage_containers.go`, `docker*.go` |
| Images | Pull (progress, platform, search suggestions); build; tag; push; save and load; layers; prune; images in use can't be removed by default | `dockerpage_images.go` |
| Compose | Projects and their services; deploy, stop, pull, recreate, down (strong warning when removing volumes); edit compose.yaml and .env with a backup before saving; new project | `dockerpage_compose.go` |
| Networks, volumes, system | Create and remove networks, connect and disconnect containers; create and prune volumes; engine info, disk use, cleanup by type; live events; registry login (password on stdin only); registry mirrors and restarting Docker | `dockerpage_resources.go` |

Left out of DPanel: users and permissions (the client has one user), several Docker environments (each SSH connection is one host already), the app store, sites with reverse proxy and certificates (beyond Docker management).

### Escalation

One shared escalation layer used by every feature that needs root. Each session tries in order, remembers what worked, and forgets on disconnect:

1. The login is root: run as is.
2. `sudo -n` works (passwordless sudo): run with it.
3. sudo wants a password: try the server's login password once; a wrong one is not retried, so sudo does not lock the account.
4. Ask for the sudo password in a dialog, with "Remember" to keep it in the system credential store.
5. The user is not in sudoers or there is no sudo: the same dialog switches to the root password and escalates with `su` (through a pseudo-terminal).
6. Nothing works or the user cancels: say that root is needed and give copyable commands to grant it, such as `usermod -aG sudo <user>` (Debian/Ubuntu) or `usermod -aG wheel <user>` (RHEL/CentOS).

Free memory uses it first; the Docker actions switch to it once the Docker tab merges, and new features use it from the start.

## Next

Starts after the Docker tab is merged and released, one Codex agent per group.

### Run on many hosts with results

- Pick several hosts from the connection list (not necessarily connected) and run a command or a script on them in parallel, with a concurrency limit and a timeout.
- A table of each host's status, exit code, duration and the start of its output; the whole output on click; retry the failed ones; export to CSV or text.
- Optionally as root (through escalation); changing commands ask first, with the number of hosts.
- Unlike "Send to" several sessions, which types the command into terminals, this collects the results.

### Services, logs and scheduled jobs

- **Services (systemd)**: every service with its running, failed and enabled state; start, stop, restart, reload, enable, disable; its journalctl log and unit file. Systems without systemd are told so.
- **Log viewer**: follow any log file and journalctl; filter, highlight keywords, pause, errors only; common logs (nginx, syslog, auth.log) in one click.
- **Scheduled jobs (crontab)**: by user; add, edit, remove, enable and disable (commented out); `*/5 * * * *` shown as "every 5 minutes" with the next runs; a backup before saving.

### Disk, updates, security and firewall

- **Disk usage**: folder sizes level by level like ncdu, to find big files and logs; removal asks first.
- **System updates**: how many packages can be updated, security updates apart; update in one click with live output; a note when a reboot is needed. apt and dnf/yum.
- **Security**: failed SSH logins and their source IPs (with location), logged-in users, recent logins, fail2ban state and bans.
- **Firewall**: ufw / firewalld port rules, add and remove; the rule allowing the current SSH port can't be removed, so the user can't lock themselves out.

## Candidates

The owner can pick any of these for a next batch.

| Feature | Notes |
|---|---|
| SSH key manager | Generate ed25519 keys, deploy the public key, switch a connection to key login; optionally turn off password login on the server after checking the key works |
| ZMODEM (rz/sz) | `rz` and `sz` in the terminal. The terminal's stream goes through `termenc.Wrap`, where ZMODEM can be detected: feasible |
| Terminal keyword highlighting | error, warning, IPs, URLs and such, with custom rules |
| Session recording | Save a session as a local log or a replayable recording |
| Server-to-server transfer | Files from A straight to B, not through this computer |
| Alerts | System notifications when a server goes down or CPU, memory or disk cross a threshold (while the app runs) |
| Database client | MySQL, Redis, PostgreSQL through an SSH tunnel: browse tables, run queries; the largest piece of work |
| Certificates and sites | SSL certificate expiry checks; Nginx sites, config test, reload |
| App lock | A master password on start and after being away |
| Two-factor | Fill in TOTP codes |
| Other protocols and forwarding | Telnet, serial, Mosh, X11 forwarding, SSH agent forwarding |

## Not planned

- Users and permissions: the client is for one person.
- An app store, one-click websites: costly to maintain and far from an SSH client.
- Anything resident on the server: every feature works over SSH alone.

## How it is built

- **In parallel**: the main session researches, splits the work, reviews, merges and releases; each section goes to a Codex CLI agent (GPT-6.2 Sol) in its own Git worktree and branch. Task briefs live outside the repository.
- **Without conflicts**: the main session first commits a skeleton (the tab, the entry points, a placeholder file per section); each agent changes only its own files; locale keys use the agent's own prefix, inserted after an anchor key given to it in both language files.
- **Review**: before merging, the code, command boundaries, confirmations, tests and screenshots (light, dark, English, Chinese, wide and narrow) are checked one by one; for changing commands only tests with fakes count.
- **Release**: after merging into `dev` and pushing, GitHub runs the full tests (`go test -race ./...`), then builds every platform and publishes `v2.0.N`; when the macOS runners can't be had, the other platforms are published anyway.
