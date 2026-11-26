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
| v2.0.13 | The Docker tab: containers (create, edit by recreating, batch actions, usage charts, files in a container), images (pull, build, push, prune), Compose (deploy, take down, edit compose files, new projects), networks, volumes, system (disk use, cleanup, events, registries, daemon config); shared escalation: root, passwordless sudo, sudo password, su, and a message only when all fail |
| v2.0.14 | Run on many hosts with results; the System tab: services, logs, scheduled jobs, disk usage, updates, security, firewall; SSH key manager, key deployment, turning off password login, agent and X11 forwarding; ZMODEM (rz/sz), terminal keyword highlighting, session recording; the Docker tab escalates when needed; hosts made in a row on Windows no longer share an ID and crash the UI |
| v2.0.15 | The System tab's websites and certificates (Nginx sites, a refused config put back at once, certificate expiry, certbot); server-to-server transfers; alerts; app lock; SSH verification codes filled in; the Database tab (MySQL / MariaDB, PostgreSQL, Redis); Telnet and serial connections |
| v2.0.16 | Professional tunnels: HTTP proxies, mixed SOCKS5/HTTP ports with authentication, reverse SOCKS5/HTTP proxies (the server uses this computer's network, optionally through its proxy), Unix sockets (remote Docker), access lists, connection and rate limits, connection logs; the tunnel manager (background connections, autostart, auto-reconnect); 21 recipes; system proxy with PAC; `ssh -L/-R/-D` import and export; public access helper (GatewayPorts, a server relay, firewall, outside test); LAN sharing; Remote Desktop through SSH |
| v2.0.17 | The Caddy tab: detection of every install (packages, static binary, Docker, custom units), one-click install (official repositories, SHA-512-checked binaries) and update (rolled back on failure), uninstall; start, stop, restart, reload, validate, autostart; site forms and 11 templates (static, reverse proxy, PHP, SPA, redirect, basic auth, IP allow lists, security headers, TLS…); global options and snippets; certificates, logs; config backups and restore; migration to one file per site, from a hand-installed binary to the official package, and from Nginx (switched back on failure). The code editor: file tree and tabs, Caddyfile/Nginx/systemd highlighting, directive completion, a diff before saving, validation before saving, root reads and writes |
| v2.0.18 | The Caddy tab's Sites section no longer crashes, and detection works on real servers; the Docker test machine (testbox) and the live testing guide |
| v2.0.19 | Desktop mode (after GMSSH): every SSH session can open a "Desktop" with wallpapers, icons and icon folders, windows (move, resize from eight directions, maximize, minimize, snap, keep on top), a dock and launcher, context menus, shortcuts and personalization; This PC and an Explorer-like file manager (views, favorites, recent, search, notes, properties and permissions, compress and extract, remote download, copy and paste between servers); a trash (freedesktop specification); a task manager (performance, processes and their details, releasing memory); settings (system info, operation log, DNS, time zone and time sync); terminal windows with a shell each; a local browser through the server; an app center and 17 built-in tools as apps; an AI assistant (the user's own OpenAI-compatible API, Ctrl+Shift+I for commands, confirmation before running) |
| v2.0.20 | The connection manager opens with every folder collapsed; every app center app is on the desktop by default |

## In progress

- An "Archive transfer" switch: one setting in the file panel's toolbar, on the Transfers page of the options and in the desktop Files app's toolbar; with it on, drops, the upload and download buttons, Download and Download to… and the desktop's uploads and downloads send files and folders, single files too, compressed with tar and gzip, unpacked at the other end (a symbolic link on its own, and connections that may not run commands, still use SFTP).

## Next

Nothing yet. Every feature the roadmap listed is built; add new ideas to Candidates.

## Candidates

The first batch and the next one hold every candidate listed before. Add new ideas here.

| Feature | Notes |
|---|---|
| Global TUN / VPN mode | Every program's traffic through the tunnel. `ssh -w` needs `PermitTunnel` on the server, root on both ends, and carries TCP over TCP; a global mode on Windows needs the wintun driver, administrator rights and a gVisor network stack. v2.0.16 covers browsers and most programs with the system proxy and PAC instead |

## Not planned

- Users and permissions: the client is for one person.
- An app store, one-click websites: costly to maintain and far from an SSH client.
- Anything resident on the server: every feature works over SSH alone.
- Mosh: there is no Mosh client library for Go; its UDP state synchronization protocol and crypto would have to be written from scratch, a high risk for little gain. SSH keepalives and reconnecting cover poor networks instead.

## How it is built

- **In parallel**: the main session researches, splits the work, reviews, merges and releases; each section goes to a Codex CLI agent (GPT-6.2 Sol) in its own Git worktree and branch. Task briefs live outside the repository.
- **Without conflicts**: the main session first commits a skeleton (the tab, the entry points, a placeholder file per section); each agent changes only its own files; locale keys use the agent's own prefix, inserted after an anchor key given to it in both language files.
- **Review**: before merging, the code, command boundaries, confirmations, tests and screenshots (light, dark, English, Chinese, wide and narrow) are checked one by one; for changing commands only tests with fakes count.
- **Release**: after merging into `dev` and pushing, GitHub runs the full tests (`go test -race ./...`), then builds every platform and publishes `v2.0.N`; when the macOS runners can't be had, the other platforms are published anyway.
