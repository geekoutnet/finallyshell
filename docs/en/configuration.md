# Configuration files

English · [简体中文](../zh-CN/configuration.md)

Finallyshell keeps its hosts and settings in plain JSON files. Passwords
are never written to them; they go to the system's credential store. This
page describes the files. [features.md](features.md) describes the options
behind each setting.

- [Config directory](#config-directory)
- [hosts.json](#hostsjson)
  - [Tunnels](#tunnels)
  - [Proxy](#proxy)
  - [Folders, deleted hosts and other fields](#folders-deleted-hosts-and-other-fields)
  - [Safety](#safety)
- [settings.json](#settingsjson)
- [db.json](#dbjson)
- [Sync](#sync)
- [Command history](#command-history)
- [quickcommands.json](#quickcommandsjson)
- [commandeditor.txt](#commandeditortxt)
- [updater.json](#updaterjson)
- [Updates in mygo.json](#updates-in-mygojson)
- [Where passwords are stored](#where-passwords-are-stored)
- [Where SSH keys are stored](#where-ssh-keys-are-stored)

## Config directory

All files live in `<user config dir>/finallyshell/`, where the user config
dir is Go's `os.UserConfigDir()`: `~/Library/Application Support` on macOS,
`%APPDATA%` on Windows, and `$XDG_CONFIG_HOME` or `~/.config` on Linux. If
the environment variable `FINALLYSHELL_DIR` is set, that directory is used
instead.

| File | Contents |
|---|---|
| `hosts.json` | Saved hosts and folders (see below). |
| `hosts.v1.json` | The original file, kept when a version 1 `hosts.json` is migrated. |
| `hosts.json.broken-<date-time>` | A copy of a `hosts.json` that could not be read. |
| `settings.json` | The options (see below). |
| `sysproxy-backup.json` | Local recovery snapshot of this computer’s system proxy before a tunnel replaces it. |
| `db.json` | Local database connection profiles by SSH host ID; no credentials or query history. |
| `log-paths/<host-hash>.json` | Up to 12 recently entered remote log paths per connection; local only. No log contents are stored here. Previous versions use the `.bak-YYYYMMDDHHMMSS` suffix. |
| `known_hosts` | Host keys you have trusted. |
| `background-<time>.<extension>` | The copy of the terminal background image. |
| `icon-themes/` | File icon themes installed from VSIX files, as `<publisher>.<name>.vsix`. |
| `sync-state.json` | What the last sync synced (see [Sync](#sync)). |
| `hosts.before-sync.json` | `hosts.json` as it was before the last sync that changed it. |
| `history/<host ID>.txt` | The command bar's history of each host, and `history/local.txt` of local terminals (see [Command history](#command-history)). |
| `quickcommands.json` | The Commands tab's quick commands (see [quickcommands.json](#quickcommandsjson)). |
| `quickcommands.json.broken-<date-time>` | A copy of a `quickcommands.json` that could not be read. |
| `commandeditor.txt` | The text of the Commands tab's command editor (see [commandeditor.txt](#commandeditortxt)). |
| `bulk-runs/` | Local metadata for the last 20 [bulk runs](features.md#run-on-many-hosts), one JSON file per run. Includes a script only when opted in, and never output. Not synced. |
| `geo/` | IP location data, downloaded only when you agree: `ip2region_v4.xdb`, `ip2region_v6.xdb`, and `cache.json`, the online lookups' answers (see [IP locations](features.md#ip-locations)). |

`mygo dev`, `go run` and the installed app all use the same directory, so
they share hosts and settings.

Files the editor downloads to open with the default app go to the cache
directory instead: `<cache dir>/finallyshell/opened/<time>/`. Folders older
than a day are deleted the next time a file is opened this way.

## hosts.json

Since version 2, `hosts.json` is an object. Version 1 was an array of
hosts. A version 1 file is migrated automatically the first time it is
read: the original is kept as `hosts.v1.json`, and the next save writes
version 2.

```json
{
  "version": 2,
  "folders": [{"id": "f1", "name": "Production"}, {"id": "f2", "name": "Databases", "parent": "f1"}],
  "hosts": [{
    "id": "h1", "name": "web", "addr": "203.0.113.10", "port": 2222, "user": "root",
    "auth": "key", "keyPath": "~/.ssh/id_ed25519",
    "folder": "f2", "notes": "notes", "noMonitor": true,
    "termType": "vt220", "backspace": "ctrl-h", "noRememberPassword": true,
    "monitorIfaces": ["eth0", "wg0"],
    "proxy": {"kind": "jump", "jumpHost": "h2"},
    "tunnels": [
      {"kind": "local", "listenPort": 15432, "targetHost": "db.internal", "targetPort": 5432},
      {"kind": "remote", "listenHost": "0.0.0.0", "listenPort": 8080, "targetHost": "127.0.0.1", "targetPort": 3000, "disabled": true},
      {"kind": "dynamic", "listenPort": 1080}
    ],
    "lastConnectedAt": "2026-11-07T10:00:00+08:00",
    "createdAt": "2026-10-01T09:00:00+08:00"
  }, {
    "id": "h2", "name": "bastion", "addr": "198.51.100.1", "user": "jump", "auth": "password",
    "proxy": {"kind": "socks5", "addr": "127.0.0.1", "port": 1080, "user": "alice"}
  }]
}
```

### Remote Desktop through SSH

On a host with `kind: "rdp"`, the optional `rdp.sshHostId` is the ID of a
saved SSH host. Without it, the Remote Desktop client connects directly as
before. `rdp.sshTarget` is the optional `host[:port]` as the SSH host sees
it; an omitted port defaults to 3389. With no target override, the app uses
the RDP host's `addr` and `port`. These fields contain no credentials. The
normal SSH and RDP credentials remain in the system credential store.

### Telnet and serial fields

- `kind` is absent for SSH, `telnet` for Telnet, or `serial` for a serial port.
- Telnet uses `addr`, `port` (23 if unset), `user`, `termType`, `encoding`,
  `backspace` and `delete`. The `telnet` object has `autoLogin`,
  `loginPrompt`, `passwordPrompt` and `keepAlive`. Passwords are never
  written to `hosts.json`; if remembered, the credential store uses
  `host/<id>/telnet-password`, separate from SSH and excluded from sync.
- Serial uses `addr` for the local device and has no network port. The
  `serial` object has `baud`, `dataBits`, `parity` (`none`, `odd`, `even`),
  `stopBits` (1 or 2, and 1.5 for 5 data bits on Windows), `flow`
  (`none`, `rtscts`, `xonxoff`), `localEcho` and `lineEnding` (`cr`, `lf`,
  `crlf`). `encoding`, `backspace` and `delete` are shared terminal fields.

### Tunnels

`hosts.json` stores each host's `tunnels`. Each entry has a stable `id`
(assigned when saving old entries), optional `name` and `description`, and
a `kind`: `local`, `remote`, `dynamic`, `http`, `remoteDynamic`, or `remoteHTTP`.
Unknown properties are preserved.

- `listenHost` defaults to `127.0.0.1`; `listenPort` of `0` means automatic.
  Remote listeners require an IP address or `localhost`.
- Fixed forwards use `targetHost` and `targetPort`; a local forward can instead
  use `targetSocket`. Proxy clients specify their own targets. `mixed` enables
  both protocols on a local SOCKS5 or HTTP listener.
- `proxyUser` enables incoming authentication. `upstream` on a remote tunnel
  is `socks5://host:port` or `http://host:port`, with optional `user@` and no
  password. An empty `upstream` means direct.
- `allowFrom` holds IP addresses or CIDRs; `maxConns`, `rateKBps` (KiB/s per
  direction) and `idleSeconds` default to unlimited. `disabled: true` skips
  automatic start with the session.
- `background: true` makes a tunnel eligible for autostart on a dedicated
  SSH connection instead of with a terminal. `settings.json` can opt out
  on this device with `"tunnelManager": {"noAutostart": true}`; this setting
  is not synced. Background SSH uses the same `known_hosts`, existing
  host credential keys and `keepAliveSeconds` as interactive SSH. The
  manager stores no additional SSH credentials.
- `systemProxy` and `pac` (a list of rules) make a running local proxy
  tunnel this computer's system proxy. The previous system settings are kept
  in the local `sysproxy-backup.json`, which is not synced and is removed
  only after restoring successfully; do not copy it to a different machine.
  On macOS, formerly empty proxy addresses can retain inactive values after
  restore; the corresponding proxies are disabled.

Tunnel credentials are not stored in `hosts.json` or synced. The system
credential store uses `host/<host-id>/tunnel/<tunnel-id>/password` and
`host/<host-id>/tunnel/<tunnel-id>/upstream/password`. A configured username
without a corresponding stored password prevents the listener from starting.
See [Tunnels](features.md#tunnels) for behavior and exposure warnings.

`serverRelay` selects a server relay for a remote tunnel. The local-only
`tunnel-firewall.json` records rules the public access assistant added:
the SSH host and endpoint, tunnel ID, port, backend, and rule tag or zone.
It contains no credentials and is not synced. Deleting this file does
not remove the server's rules.

### Proxy

`proxy` says how the host is reached:

- `kind` is `socks5` or `http`, which have `addr` and `port`, and `user`
  when the proxy needs authentication;
- or `kind` is `jump`, and `jumpHost` is the ID of the jump host.

Without `proxy`, the host is connected directly. Proxy passwords are not
written to the file either; like host passwords, they are kept in the
credential store (see [Where passwords are stored](#where-passwords-are-stored)).

### Folders, deleted hosts and other fields

- `auth` is how the host logs in: `password`, `key` (with the private key
  file in `keyPath`) or `agent`, the keys of the SSH agent at `sshAgent`
  in [settings.json](#settingsjson), with no password or passphrase
  stored. Versions before SSH agent support read `agent` as `password`:
  a host synced to such a version asks for a password there, and saving it
  there makes it a password host.
- `folder` is the ID of the folder the host is in; empty means the top
  level. Folders nest through `parent`.
- A host with `deletedAt` (the time it was deleted) is deleted. It is
  listed only when "Show deleted" is checked in the connection manager.
- `createdAt` is when the host was added. Quick connect sorts the hosts that
  were never connected by it. Hosts saved by older versions do not have it.
- `lastConnectedAt` is when the host last connected, used for the "Recent"
  list of quick connect.

Fields with empty values are not written. Missing fields mean:

| Field | Missing means | Set means |
|---|---|---|
| `noMonitor` | The server monitor is on. | `true`: no monitor, and no extra exec channel. |
| `monitorIfaces` | The network graph shows the busiest interface. | A list of interface names, such as `["eth0", "wg0"]`: the graph shows their sum. Names the server does not have are skipped, and with none left the graph shows the busiest. |
| `termType` | `xterm-256color` | The `TERM` sent to the server. |
| `backspace` | The global setting (`termBackspace`). | `del`: Backspace sends `^?`; `ctrl-h`: it sends `^H`. |
| `delete` | The global setting (`termDelete`). | `vt220`: Delete sends `ESC [3~`; `del`: `^?`; `ctrl-h`: `^H`. |
| `encoding` | The global setting (`termEncoding`). | `utf-8`, `gbk`, `gb18030`, `big5`, `shift_jis`, `euc-jp`, `euc-kr`, `iso-8859-1` or `windows-1252`; unknown values mean UTF-8. |
| `noRememberPassword` | Passwords are remembered. | `true`: "Remember password" was turned off. |
| `forwardAgent` | SSH agent forwarding is off. | `true`: request forwarding for interactive shells. |
| `x11Forward` | X11 forwarding is off. | `untrusted` or `trusted`: request that mode for interactive shells. |

### Safety

- Unknown fields (written by a newer version) are kept as they are, and are
  not lost on save.
- Writes go to a temporary file first, which is then renamed, so a half
  written file is never left behind.
- If `hosts.json` cannot be read (for example after a bad hand edit), the
  app first copies it as is to `hosts.json.broken-<YYYYMMDD-HHMMSS>` in the
  same folder, then starts with an empty list. The next save therefore
  cannot destroy the original content.

## settings.json

Every option is saved here as soon as it changes. Keys are written only
when they differ from the default, so a missing key always means the
default.

| Key | Meaning | Default (when missing) |
|---|---|---|
| `language` | UI language: `en` or `zh-Hans`. | Follow the system. |
| `appearance` | `light` or `dark` forces the app's appearance; `system` follows the system's. | Follow the system (also for unknown values). |
| `noAutoSelectTab` | `true` opens new sessions in the background. | New sessions are selected. |
| `minimizeToTray` | `true` hides the main window to the tray when it is closed. | Closing quits. |
| `noConfirmClose` | `true` closes and quits without asking while sessions are open. | Ask first. |
| `keepAliveSeconds` | Seconds between the SSH keepalives of each connection, 1 to 3600 (higher reads as 3600); `-1` (any negative number) turns them off, which the options show as 0. Applies to new connections. | 30 (also for `0`) |
| `tunnelManager.noAutostart` | When `true`, skips background tunnel autostart on this device. Manual starts still work. Not synced. | `false` |
| `sshAgent` | The SSH agent of the hosts with `"auth": "agent"`: a Unix socket's path (`~` for the home folder), such as `~/.bitwarden-ssh-agent.sock`, or on Windows a named pipe, `\\.\pipe\<name>`. This computer's own: not synced. Applies to new connections. | `SSH_AUTH_SOCK`; `\\.\pipe\openssh-ssh-agent` on Windows. |
| `x11Display` | Override the local X display for X11 forwarding (for example `localhost:1`). This computer's own: not synced. | `$DISPLAY`, or `localhost:0` on Windows when unset. |
| `downloadDir` | Folder downloads go into. | The system's Downloads folder. |
| `askDownloadDir` | `true` asks where to save each download. | Use `downloadDir`. |
| `copyOnSelect` | `true` copies the mouse selection to the clipboard. | Off. |
| `rightClick` | `paste`, `copy`, `copy-or-paste` or `ignore`. | The context menu (also for unknown values). |
| `noLinkClick` | `true` keeps Ctrl+click (Cmd+click) from opening links. | Links open. |
| `wheelLines` | Lines per mouse wheel notch, 1 to 20 (higher reads as 20). | 3 |
| `shortcuts` | Changed shortcuts only, such as `{"find": "Ctrl+Alt+F", "closeTab": "none"}`. | Platform defaults. |
| `termEncoding` | The server's character encoding for hosts set to "Default": `gbk`, `gb18030`, `big5`, `shift_jis`, `euc-jp`, `euc-kr`, `iso-8859-1` or `windows-1252`. | `utf-8`, no conversion (also for unknown values). |
| `termBackspace` | `ctrl-h`: Backspace sends `^H`, for hosts set to "Default". | `^?` |
| `termDelete` | `del` or `ctrl-h`: what Delete sends, for hosts set to "Default". | `ESC [3~` |
| `localShell` | The program a local terminal runs, with its arguments, as a command line split into words as a shell would (quotes allowed), such as `pwsh -NoLogo` or `/opt/homebrew/bin/fish -l`. This computer's own: not synced. | The login shell; PowerShell on Windows. |
| `optionAsAlt` | `true` makes Option the Alt (Meta) key on macOS. | Off. |
| `termFontFamily` | Terminal font, a comma-separated list. | JetBrains Mono, Cascadia Mono, Consolas, Menlo, DejaVu Sans Mono, monospace |
| `termFontSize` | Font size, 6 to 48. | 13.5 |
| `termLineHeight` | Line height as a multiple of the font's, 0.8 to 2. | 1 |
| `termFontWeight` | Font weight: 300, 400, 500, 600 or 700. | 400 |
| `termNoLigatures` | `true` turns ligatures off. | Ligatures on. |
| `termTheme` | Ghostty color scheme name. | Tomorrow Night |
| `termBackground` | File name of the background image copy. | No image. |
| `termBackgroundFit` | `contain` or `tile`. | Cover. |
| `termBackgroundOpacity` | Image opacity in percent, 5 to 100. | 30 |
| `keepManagerOpen` | `true` keeps the connection manager open after connecting. | It closes. |
| `fileIconTheme` | `minimal`, `bundled:seti`, or `<publisher>.<name>/<theme ID>`. | Material Icon Theme |
| `noFollowTerminalDir` | `true` keeps the file panel where it is when the shell's folder changes. | It follows. |
| `noAgentIcons` | `true` hides the logo of the coding agent running in a session tab, and stops reading the program in the shell's foreground for it. | Shown. |
| `noCommandBar` | `true` hides the command bar under the terminal, and all it does. | Shown. |
| `noCommandBarPaths` | `true` turns the command bar's path picker off. | On. |
| `noCommandBarHistory` | `true` turns the history picker off, and keeping the commands sent. | On. |
| `noCommandBarCommands` | `true` turns command completion off. | On. |
| `noServerHistory` | `true` keeps the server's `~/.bash_history` and `~/.zsh_history` out of the history picker. | Read. |
| `geoLocations` | `on` shows where IP addresses are, from the downloaded ip2region data; `off` hides it (also "Don't Ask Again"). | Not asked yet: the Network tab asks. |
| `geoLocationsV6` | The same for the IPv6 data. | Asked the first time an IPv6 peer shows. |
| `geoOnline` | `true` looks up online (ip-api.com) the addresses the data has no city or ISP for. | Off. |
| `sidebarWidth`, `treeWidth` | Widths of the sidebar and the file panel's folder tree, as last dragged. | The app's defaults. |
| `monitorPercent`, `terminalPercent` | The server monitor's share of the sidebar and the terminal's share of the main area, in percent. | The app's defaults. |
| `monitorHidden` | The parts of the server monitor hidden from its context menu, for every host: any of `uptime`, `cpu`, `processes`, `network`, `latency`, `disks`, such as `["processes", "disks"]`. A part added in a later version shows until hidden. | Every part shows. |
| `procListPercent`, `procFieldsPercent` | In the Processes tab, the process list's share above the details, and the details' fields' share beside the environment variables, in percent. | The app's defaults. |
| `netListPercent` | In the Network tab, the table's share above the connections and trace tabs, in percent. | The app's defaults. |
| `bottomPanelHidden` | `true` collapses the bottom panel under the terminal to its row of tabs, or, on the right, hides its column. | Open. |
| `bottomPanelSide` | `right` puts the bottom panel in a column on the terminal's right. | `""`, under the terminal. |
| `rightPanelWidth` | Width of the column on the terminal's right, in DIPs, as last dragged. | The app's default (460). |
| `monitorSide` | `right` puts the server monitor at the top of the column on the terminal's right; the session list then fills the left column. | `""`, at the top left. |
| `rightMonitorPercent` | The server monitor's share of the right column's height, above the bottom panel, when both are there, in percent. | The app's default (45). |
| `commandEditorShown` | `true` shows the command editor in the Commands tab. | Hidden. |
| `commandEditorWidth` | The command editor's width beside the command buttons, in DIPs, as last dragged. | The app's default (460). |
| `commandEditorPercent` | The command buttons' share of the Commands tab's height above the command editor, where the tab is too narrow to have it beside them, in percent. | The app's default (40). |
| `commandEditorNoEnter` | `true` turns off the command editor's "Press Enter after each line": the last line it sends goes without a carriage return. | Every line runs. |
| `commandEditorClear` | `true` turns on the command editor's "Clear after sending". | Off. |
| `leftPanelPage` | `connections` shows the Connections page in the bottom-left panel. | The session tabs. |
| `connectionTreeExpanded` | IDs of the folders open on the Connections page, such as `["f1730000000000000000"]`; `[]` for none. By ID, so a renamed or moved folder stays open. | Every folder open. |
| `sync` | The Sync page's settings (see [Sync](#sync)). | Sync is off. |

Details of each option are in [features.md](features.md): see
[General](features.md#general), [Transfers](features.md#transfers),
[Terminal](features.md#terminal-defaults),
[Terminal › Mouse](features.md#terminal--mouse),
[Terminal › Shortcuts](features.md#terminal--shortcuts),
[Terminal › Command bar](features.md#terminal--command-bar),
[Network monitor](features.md#network-monitor),
[Appearance](features.md#appearance-font-color-scheme-background-image),
[File icon themes](features.md#file-icon-themes),
[Connection manager](features.md#connection-manager),
[Connections page](features.md#connections-page),
[Command editor](features.md#command-editor) and
[Layout and split dividers](features.md#layout-and-split-dividers).

### Terminal stream settings

- `terminalHighlight`: this device's optional object with `disabled`, `useCustom` and `rules`. Without `useCustom`, built-in rules apply. A custom rule has `pattern`, `regex`, `caseSensitive`, `color`, `bold`, `underline` and an optional `host` ID; an empty host applies to all hosts.
- `terminalRecording`: this device's optional object with `automatic`, `text`, `folder` and `limitMiB`. Automatic recording and text logs default to off. The default folder is `recordings/` under the config directory, and the default rotation limit is 64 MiB per file (range 1–512 MiB).
- `.cast` files and optional `.txt` companions are created in a host-specific subfolder. Their names start with the local date and time (`yyyy-mm-dd_HH-MM-SS`); extra parts get a numbered suffix. New parts never replace existing files. Terminal stream settings save a `settings.json.bak-<yyyymmddHHMMSS>` copy before replacing existing settings.

### App lock settings

- `lockEnabled`: enables the password gate on startup; absent means off.
- `lockIdleMinutes`: `1`, `5`, `15`, `30`, or `60`; absent or `0` disables idle locking.
- `lockOnMinimize`: locks when minimizing or hiding to the tray; absent means off.

These settings stay on this device. Before changing them the app backs up
an existing `settings.json` as `settings.json.bak-<yyyymmddHHMMSS>`.

### Alert rules

- `alertRules`: optional object keyed by SSH host ID. The empty key `""` holds defaults for all SSH hosts. A host's object replaces the defaults; `{}` disables all alerts for that host. In a rule object `unreachable` and `services` are booleans, `cpu`, `memory` and `disk` are percentage thresholds (0 disables the threshold), `cpuMinutes` is 1–60 (defaults to 1), and `loadPerCore` is a multiplier of the online CPU core count (0 disables it). This setting is local to the device and alert history is not written to disk.

### Website and certificate files

The Websites section adds no host or application settings. It reads Nginx configuration on the remote server, creates site files under `/etc/nginx/sites-available/` or `/etc/nginx/conf.d/`, and manages corresponding Debian links under `/etc/nginx/sites-enabled/`. Backups use the sibling name `<file>.bak-<YYYYMMDDHHMMSS>`. The app reads public certificates configured by Nginx and `/etc/letsencrypt/live/*/fullchain.pem`; it does not download private keys or cache configuration locally.

## db.json

This optional file maps SSH host IDs to arrays of Database tab connection
profiles. Each profile stores `id`, `name`, `engine` (`mysql`, `postgres`
or `redis`), `host`, `port`, `user`, `database` (or Redis index), `tls`
(`disable`, `require` or `verify-full`), `remember` and `readOnly`. Missing
boolean fields are false. Profiles are not synced and do not change
`hosts.json` or `settings.json`. Unreadable data is not overwritten.
Updates back up the previous file as `db.json.bak-<YYYYMMDDHHMMSS>` (UTC).
Remembered passwords are system credentials under
`database/<SSH host ID>/<profile ID>/password`.

## Sync

The Sync page of the options (see [Sync](features.md#sync)) is the `sync`
object of `settings.json`, missing while sync has never been set up:

| Key | Meaning | Default (when missing) |
|---|---|---|
| `provider` | `gist`, `webdav` or `s3`. | Sync is off. |
| `gistId` | The gist holding the file, found or created by the first sync. | Find or create one. |
| `webdavUrl`, `webdavUser` | The WebDAV folder and user. | None. |
| `s3Endpoint`, `s3Region`, `s3Bucket`, `s3Key`, `s3AccessKeyId` | The S3-compatible bucket, the file's key in it and the access key's ID. | Region `us-east-1`, key `finallyshell-sync.json`. |
| `passwords` | `true` includes saved passwords. | Not included. |
| `auto` | `true` syncs automatically. | Off. |
| `interval` | Minutes between automatic syncs: 5, 15, 30 or 60. | 15 |
| `historyAllDays`, `historyDailyDays`, `historyWeeklyWeeks`, `historyMax` | WebDAV and S3 version retention windows and target limit. | 7 days, 30 days, 26 weeks, 200 versions. |
| `localBackupCount`, `localBackupDays` | Count and age limits for local backups. | 50 versions, 90 days. |

Each settings key is either synced or kept on each computer; the lists are
`syncedSettings` and `localSettings` in `internal/config/sync.go`. Kept
local: `sidebarWidth`, `monitorPercent`, `terminalPercent`, `treeWidth`,
`procListPercent`, `procFieldsPercent`, `netListPercent`,
`monitorHidden`, `bottomPanelHidden`, `bottomPanelSide`, `rightPanelWidth`,
`monitorSide`, `rightMonitorPercent`, `commandEditorShown`,
`commandEditorWidth`, `commandEditorPercent`, `commandEditorNoEnter`,
`commandEditorClear`, `termBackground`, `localShell`, `sshAgent`, `x11Display`, `downloadDir`,
`askDownloadDir`, `sync`,
`geoLocations`, `geoLocationsV6`, `geoOnline`, `leftPanelPage` and
`connectionTreeExpanded` (the left panel's page and the folders open are
where this computer's window was left, as the pane sizes are), and a
`fileIconTheme` installed from a VSIX or found in an editor's extensions.

Sync and backup data next to `hosts.json`:

- `sync-state.json`: where the last sync synced (`remote`), when (`at`),
  the computer that wrote the file it read (`device`), and the file as it
  was after that sync (`base`), still encrypted with the sync password: the
  next sync compares both sides with it; and whether that sync included
  this computer's saved passwords (`secrets`): when it did not, the next
  sync does not compare passwords with the base, so turning "Include
  saved passwords" on later receives the passwords rather than deleting
  them. Deleting the file makes the next sync keep what either side has,
  as a first sync does. `restorePaused` blocks automatic sync after an
  incomplete restore.
- `hosts.before-sync.json`: `hosts.json` as it was before the last sync
  that changed it.
- `backups/`: timestamped directories with `hosts.json`, `settings.json`,
  and when present `quickcommands.json` and `commandeditor.txt`. A
  `backup.json` describes each version; a `pinned` marker exempts it from
  retention. These are local plaintext files, not credential-store exports.
- `sync-log.json`: the last 100 sync results, without passwords or item names.
- `backup-deleted.json`: IDs of known permanent deletions, which a backup
  merge must not add back.

The file on the service, `finallyshell-sync.json`, is JSON:
`{"app": "finallyshell", "format": 1, "kdf": "argon2id", "salt": ..., "t": 3, "m": 65536, "p": 4, "cipher": "aes-256-gcm", "nonce": ..., "data": ...}`,
the binary fields in base64. New writes have an optional plaintext
`snapshot` header; older clients can still open the main file. Decrypted,
`data` is `{"schema": 1, "device": ..., "updatedAt": ..., "folders": [...], "hosts": [...], "quickCommands": {...}, "settings": {...}, "secrets": {...}}`,
where folders and hosts are those of `hosts.json`, settings the synced
keys of `settings.json`, and secrets the saved passwords by their key in
the credential store.

WebDAV stores snapshots in `finallyshell-sync-history/`, beside the main
file. S3 uses the same prefix and isolates custom main keys in their own
subprefix. Names are `<UTC time>-<device>-<sha8>.json`; pin markers add
`.pin` to that name. Gist uses native revisions of the same file.

## Command history

The [command bar](features.md#command-bar) keeps the commands sent from
it in `history/<host ID>.txt`, a file per host named by the host's `id`
in `hosts.json`: plain UTF-8 text, one command per line, the oldest first,
each command once (one sent again moves to the end). There is no limit on
the number of commands; once a file passes 10 MB, the oldest are dropped.
Blank commands, and those with a control character other than tab (a
second line, an escape sequence), are not kept, nor read from a file.
A host's file is deleted with the host (when it is deleted for good, not when it goes to
"Show deleted"), and Options › Terminal › Command bar › "Clear history of
all hosts…" deletes the folder. You can edit or delete the files while
the app is closed; one is read the first time the history picker needs
it in a run. [Local terminals](features.md#local-terminal) share one
history, `history/local.txt`, which no host's ID can name.

Sync does not carry the history yet; the format is kept this simple so
that it could.

## quickcommands.json

The [Commands tab](features.md#commands-tab)'s quick commands, the same
for every host:

```json
{
  "version": 1,
  "groups": [{"id": "g1", "name": "Common"}],
  "commands": [
    {"id": "c1", "group": "g1", "name": "Update system", "text": "apt update\napt -y upgrade", "enter": true},
    {"id": "c2", "name": "", "text": "tail -f /var/log/syslog", "enter": false}
  ]
}
```

- `groups` and `commands` are in the order the tab shows them. A
  command's `group` is a group's `id`; a command without one, or whose
  group is gone, is in no group and shows under "All" only.
- `name` is the button's text; an empty one shows the first line of
  `text`. `text` is the command, its lines separated by `\n`; empty
  lines at its end are not sent.
- `enter` sends a carriage return after each line; `false` leaves it off
  after the last line. A command without the key has it on.
- Keys the app does not know are kept as they are when it saves, and a
  newer `version` too.

The file is written as it changes, through a temporary file renamed over
it. If it cannot be parsed, the app copies it as is to
`quickcommands.json.broken-<YYYYMMDD-HHMMSS>` first, shows the tab empty
with a note naming the copy, and the next change replaces the file. If it
cannot be read at all (or the copy fails), the tab says so and nothing
is saved to it until the app starts again.

## commandeditor.txt

The text of the Commands tab's
[command editor](features.md#command-editor), as typed, in UTF-8 with
`\n` line ends: the same for every session, and not synced. It is
written a second after typing stops, and as the app quits, through a
temporary file renamed over it; missing, the editor starts empty. If it
cannot be read, the editor says so and nothing is saved to it until the
app starts again.

## updater.json

The choices of the update window (see [Updates](features.md#updates)) are
kept by MyGo's updater plugin in `updater.json`, in the app's user data
folder rather than the config directory above: `~/Library/Application
Support/Finallyshell` on macOS (on its default file system, which ignores
case, the same folder as the config directory), `%APPDATA%\Finallyshell` on Windows,
and `$XDG_CONFIG_HOME/Finallyshell` or `~/.config/Finallyshell` on Linux.
`FINALLYSHELL_DIR` does not move it.

| Key | Meaning | Default (when missing) |
|---|---|---|
| `automaticChecks` | `false` turns the checks in the background off ("Check for updates automatically"). | On. |
| `automaticDownloads` | `true` installs the updates the background checks find without asking; they run at the next launch. | Ask. |
| `skippedVersion` | The version the user skipped; the background checks do not offer it. | None. |
| `lastCheck` | When the last check succeeded. | Never. |

A damaged file is ignored, as if it were missing.

## Updates in mygo.json

`updates` in `mygo.json` is built into the app: where it reads updates and
the key they must be signed with.

```json
"updates": {
  "publicKey": "fwcqse2POLiSdWytl0BHeURrztxcDObwmos+4IlTqHQ=",
  "github": "TooonyChen/finallyshell"
}
```

- `publicKey` is the public half of the key that signs updates. Installed
  copies accept only updates signed with its secret half, which is the
  repository secret `MYGO_UPDATER_PRIVATE_KEY` and never in the
  repository (see [Signing updates](development.md#signing-updates)).
- `github` is the repository whose latest release the app reads the
  update manifests from.

## Where passwords are stored

Passwords and private key passphrases are stored with MyGo's
`mygo.Secrets` in the system's credential store, which the system encrypts
for the current user. The service name is `finallyshell`, not the app's
identifier, so that `mygo dev`, `go run` and the installed app share the
same entries, as they share `hosts.json`. The keys are:

- `host/<host ID>/password`: the password;
- `host/<host ID>/passphrase`: the private key passphrase;
- `host/<host ID>/proxy-password`: the password of this host's SOCKS5 or
  HTTP proxy;
- `host/<host ID>/totp`: the SSH two-factor secret and its algorithm,
  digits and period. Nothing is stored in `hosts.json` or plaintext
  backups. It only travels in encrypted sync when **Include passwords**
  is enabled. Duplicating a host does not duplicate its TOTP secret;
- `host/<host ID>/privilege/<endpoint hash>`: the method (`sudo` or `su`)
  and password selected in the privilege dialog. The endpoint hash scopes
  the entry to the SSH user, address and port as well as the saved host.
  It is independent of **Remember password**, is not synced or backed up,
  and can be removed in the system's credential store, including after
  deleting the saved connection.
- `sync/password`: the sync password (see [Sync](features.md#sync));
- `sync/<service>/credential`: the token, password or secret access key of
  the sync service (`gist`, `webdav` or `s3`);
- `app/lock/verifier`: a randomly salted Argon2id verifier (3 passes,
  64 MiB, 4 lanes). The lock password itself is never stored, and the
  verifier never syncs. Reset can delete indexed privilege entries and
  entries for current hosts; legacy privilege entries for removed hosts
  or old endpoints may need to be removed from the system store manually.

"Remember password" is a per-host setting for the login and proxy
credentials. The privilege dialog has its own **Remember** checkbox.

| System | Where | How to look at it |
|---|---|---|
| macOS | A generic password in the login keychain, with the name (service) `finallyshell` and the key as the account | Keychain Access |
| Windows | A generic credential in Credential Manager, with the target `finallyshell/<key>` | Control Panel → Credential Manager → Windows Credentials |
| Linux | Secret Service (GNOME Keyring, KWallet, KeePassXC and others, through libsecret), with the attribute `service` set to `finallyshell` and `account` set to the key | "Passwords and Keys" (Seahorse), or `secret-tool lookup service finallyshell account host/<host ID>/password` |

How passwords are used:

- When connecting, the app first uses a password already known in this run.
  If there is none, it reads the credential store once, and only if the
  store has none either does it show a password prompt. The same goes for
  every password along the way: the host's, its proxy's, each jump host's,
  and each jump host's proxy's. The password prompt also has "Remember
  password". The host's own private key passphrase is read when
  connecting; if it cannot be read, the key is used as if it had no
  passphrase.
- When the server rejects a saved password, the session shows
  "Authentication failed" and a password prompt opens. When a private key
  is missing its passphrase or the passphrase is wrong, a passphrase prompt
  opens the same way (with "Remember passphrase"). When a jump host rejects
  its password, or a proxy rejects authentication, that hop's password
  prompt opens. After you enter it, the same session reconnects.
- Clicking OK in the connection editor saves to the credential store at
  once, since the user explicitly saved. A password typed into a prompt is
  kept only in memory at first, and saved after a successful login,
  replacing the old one. A wrong password is never saved, and the
  previously saved one stays until one that logs in replaces it.
- Other errors from the credential store (for example the user denying
  access or cancelling an unlock) are treated as "nothing saved". Each
  distinct error is logged only once.
- Turning off "Remember password" deletes the password from the credential
  store and keeps it only in memory, so it must be entered again after the
  app restarts. The choice is saved per host (`noRememberPassword` in
  `hosts.json`), and later password prompts are unchecked by default too.
- When the system has no credential store (on Linux without libsecret,
  without a session D-Bus, or with no Secret Service on D-Bus, which is
  common on servers and minimal desktops), passwords are kept in memory
  only. "Remember password" in the editor and the prompts is disabled, with
  the reason shown below it.
- Reading or writing the credential store may make the system ask the user
  something: to unlock the keychain or keyring, or, on macOS, whether a
  rebuilt app may read an entry saved earlier, the first time it does. These
  calls can take a long time, so the app never makes them on the UI thread
  (see the comment at the top of `internal/app/secrets.go`). They run in
  order on a background goroutine, and the results go back to the UI thread
  through `App.post`.

## Caddy site and global files

The Sites and Global options sections read the detected main Caddyfile (normally `/etc/caddy/Caddyfile`) and its imports. Existing sites are written back to their original file. New sites, new snippets and global options are written to the main file. A disabled site remains in its original file as a marked comment block, so it can be enabled again. These sections do not write plaintext site passwords to local configuration or sync data and have no app settings of their own.

## Where SSH keys are stored

The key manager writes generated and imported OpenSSH key pairs to the user's `~/.ssh` directory, outside the app's configuration and sync storage. Only the path is saved in `hosts.json`. Private files and their backups are mode `0600` on Unix; on Windows their ACL grants access only to the current user. Backups end in `.bak-<yyyymmddHHMMSS>` and may still be valid private keys, so keep them protected. A saved private-key passphrase goes to the system credential store, never alongside the key.
