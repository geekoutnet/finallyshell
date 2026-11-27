# Features in detail

English · [简体中文](../zh-CN/features.md)

This page describes what Finallyshell does, screen by screen, including
the `settings.json` keys behind each option and the limits of what is
supported. [configuration.md](configuration.md) covers the files on disk;
[development.md](development.md) covers building and the code.

- [Window layout](#window-layout)
- [Sessions and tabs](#sessions-and-tabs)
  - [Bottom panel in a tab of its own](#bottom-panel-in-a-tab-of-its-own)
  - [Coding agents in tabs](#coding-agents-in-tabs)
- [Connections page](#connections-page)
- [New tab and quick connect](#new-tab-and-quick-connect)
- [Local terminal](#local-terminal)
- [Connection manager](#connection-manager)
- [SSH keys](#ssh-keys)
- [Import connections](#import-connections)
  - [From FinalShell](#from-finalshell)
  - [From Xshell](#from-xshell)
  - [From SecureCRT](#from-securecrt)
  - [From MobaXterm](#from-mobaxterm)
  - [From an SSH config](#from-an-ssh-config)
  - [From a CSV or Excel file](#from-a-csv-or-excel-file)
- [Connection editor](#connection-editor)
  - [SSH connection](#ssh-connection)
  - [Terminal](#terminal-page)
  - [Proxy and jump hosts](#proxy-and-jump-hosts)
  - [Tunnels](#tunnels-page)
  - [Keys and other behavior](#keys-and-other-behavior)
- [Server monitor and system info](#server-monitor-and-system-info)
- [Terminal](#terminal-section)
  - [Find](#find)
  - [Full screen](#full-screen)
  - [Splits](#splits)
  - [Command bar](#command-bar)
  - [Appearance: font, color scheme, background image](#appearance-font-color-scheme-background-image)
- [File panel](#file-panel)
  - [Following the terminal's folder](#following-the-terminals-folder)
  - [File icon themes](#file-icon-themes)
  - [Symbolic links](#symbolic-links)
  - [File operations](#file-operations)
  - [Upload and download](#upload-and-download)
  - [Archive transfer](#archive-transfer)
  - [Transfers tab](#transfers-tab)
  - [Downloads popover](#downloads-popover)
  - [Progress on the app icon](#progress-on-the-app-icon)
- [Remote file editor](#remote-file-editor)
- [Desktop mode](#desktop-mode)
- [Tunnels tab](#tunnels-tab)
- [Processes tab](#processes-tab)
- [Network tab](#network-tab)
  - [IP locations](#ip-locations)
  - [Trace route](#trace-route)
- [Run on many hosts](#run-on-many-hosts)
- [Database tab](#database-tab)
- [Commands tab](#commands-tab)
  - [Command editor](#command-editor)
- [Options window](#options-window)
  - [General](#general)
  - [Transfers](#transfers)
  - [Terminal](#terminal-defaults)
  - [Terminal › Mouse](#terminal--mouse)
  - [Terminal › Shortcuts](#terminal--shortcuts)
  - [Terminal › Command bar](#terminal--command-bar)
  - [Network monitor](#network-monitor)
  - [Sync](#sync)
- [Account](#account)
- [App lock](#app-lock)
- [Keyboard shortcuts](#keyboard-shortcuts)
- [Layout and split dividers](#layout-and-split-dividers)
- [Language](#language)
- [Updates](#updates)

## Window layout

The main window follows FinalShell's layout, except that the session tabs
are vertical:

- Top left: the server monitor of the current session.
- Bottom left: the vertical session list, or, on its other page, the
  saved connections in their folders (see
  [Connections page](#connections-page)).
- Top right: the terminal.
- Bottom right: the Files, Transfers, Tunnels, Processes, Network and
  Commands tabs, which can also open in a tab of their own (see below).

The bottom panel can collapse to its row of tabs, which stays at the
bottom, and the terminal takes the rest of the height. The chevron at the
right end of the tab row, before "Open in its own tab", hides the panel
(chevron down, "Hide panel") and shows it again (chevron up, "Show
panel"); so do "Hide panel" and "Show panel" in the tab row's context menu
and the shortcut "Show or hide the bottom panel" (Cmd+J on macOS,
Ctrl+Shift+J on Windows and Linux). While the panel is collapsed, a click
on a tab's label opens it on that tab, at the height it had before, and
so does anything that opens one of its tabs, such as a click on the
server monitor's process list, which opens Processes. The choice is kept
in `settings.json` and restored at the next start. A panel opened in a
tab of its own is the panel itself and is not affected, and full screen
shows the terminal alone either way.

The bottom panel can also go on the right of the terminal: "Move panel to
right side" in the tab row's context menu puts it in a column on the
right, full height, with the terminal and its command bar on the left;
"Move panel to bottom" puts it back. The column is 460 DIPs wide by
default and keeps its width when the window is resized, the terminal
taking the difference. Its tabs scroll sideways where they do not fit
(with the mouse wheel over them, a touchpad, or the keyboard), and so do
the Files table's columns. Hidden while on the right, the column goes away
and a thin strip at the terminal's right edge keeps the "Show panel"
button (chevron left; the hide button in the tab row is a chevron right);
the shortcut, the strip's context menu and anything that opens one of the
panel's tabs show it again, as at the bottom. The side is kept in
`settings.json`. A panel opened in a tab of its own ignores it, and full
screen is unchanged.

The server monitor can go on the right too: "Move to right side" at the
top of its context menu puts it in the same column, and "Move to left
side" puts it back. The session list then fills the left column, and the
monitor's old height there comes back with it. With the bottom panel
under the terminal, the monitor has the column alone, full height, next
to the terminal and the panel. With the panel on the right as well, the
monitor is on top and the panel below, with a divider between them.
Hiding the panel there collapses it to its row of tabs at the column's
bottom, with the "Show panel" button (chevron up) at its end, and the
monitor takes the height; a click on a tab opens the panel on it. The
monitor follows the current session wherever it is, and a panel opened
in a tab of its own has it beside it. The side is kept in
`settings.json`; full screen shows the terminal alone.

Every table's columns can be resized: drag the thin line at the right
edge of a column's header, and double-click it to fit the column to its
cells (or, in the Docker containers list, the Trash window and the quick
connect list, to give the column its default width back). This works
everywhere, including the Docker containers list and the desktop's Files
and Trash windows, whose columns used to be fixed. The widths last until
the app quits.

## Sessions and tabs

The bottom left of the window is a vertical list of sessions, on the
"Sessions (N)" page of the switch at its top. Each has a status dot:
green for connected, yellow for
connecting, red for failed. The list scrolls, and sessions can be
reordered by dragging. The context menu of a session has Reconnect,
Duplicate session, Disconnect, Rename and Close.

Over the main area, a row of tabs shows the same sessions, one tab per
server, in the list's order, as a browser's tabs: a server's tab stands
for its row and the rows under it (its panel row and its desktop). A click
on a tab goes back to the row of that server the user was on last, so the
terminal, the bottom panel, the desktop and the monitor switch with it;
choosing a row in the list moves the tabs along. A tab's context menu has
what the list's has for the server (Switch to desktop mode or Switch to
terminal mode, whichever it does not show, Reconnect, Duplicate session,
Disconnect and its connection info), Split right and Split
down, Rename, which renames it on the tab itself, and Close, then Close
other tabs, Close tabs to the right, Close tabs to the left, Close
disconnected tabs and Close all tabs. "+", tinted at the row's end, opens
a new tab. A server's tab starts with an icon of what it shows, in about
the room of a status dot: `>_` for its terminal, a monitor for its desktop,
or the icon of a panel row's page, with the connection's status as a dot
on the icon's upper corner, as a badge; its tooltip names it too. The tabs share the row's width, down to 120 DIPs each; more
scroll sideways with the wheel, without a scrollbar, and a menu at the
row's end lists them all, scrolling the one chosen into view. Each session
keeps the tab of the bottom panel it shows: switching servers does not
change another's. In the list, dashed lines join a server's panel row and
desktop to it, and a little room sets one server apart from the next.

Closing a server that is connected or connecting, from its tab, the list's
close button or context menu, or the shortcut, asks first: closing ends
the connection, its desktop and panel, and its transfers. A disconnected
server, a panel row and a new tab close at once. Closing several tabs from
the menu asks once, saying how many of them are still connected.

The first button of the tab row folds the whole left column (the monitor
and the session list) away, and the main area takes the window's width;
the button, now pointing right, and the shortcut "Show or hide the left
column" (Cmd+B on macOS, Ctrl+Shift+B on Windows and Linux) show it again.
The choice is kept in `settings.json`.

A session tab can have a name of its own for this run, to tell apart
several tabs open to the same server. "Rename" in its context menu, or F2
while the session list has the keyboard focus, turns the tab's name into
a text field in place, with the name selected. Enter keeps the new name;
Esc, clicking elsewhere or moving the focus away keeps the old one.
Names are trimmed and kept to one line, and an empty name, or the host's
own title, goes back to the host's name, as "Reset name" in the context
menu does. A renamed tab's tooltip shows the host and its address. The
name is shown wherever the tab is named: the session list and finding a
row by typing in it, the transfers, and the file editor's tabs opened
after the rename. It is never saved: it stays through Disconnect and
Reconnect, and goes when the tab closes. "Duplicate session" opens a tab
with the host's name. New tabs, which have no host yet, cannot be
renamed. In the terminal, F2 goes to the program running there.

The "+" button opens a new tab (see below), and the terminal button to
its left a [local terminal](#local-terminal). New tabs can be selected,
dragged and closed like sessions, but the count in the title does not
include them. When every tab is closed, the right side still shows quick
connect, and connecting there opens a new tab.

When a connection succeeds, its time is recorded for the "Recent" list of
quick connect. When a host's `noMonitor` is `true`, the server status is
not polled, so no extra exec channel is opened (some jump hosts do not
allow one).

Each connection sends an SSH keepalive (`keepalive@openssh.com`) every
30 seconds, as OpenSSH's `ServerAliveInterval` does, so that routers,
firewalls and proxies don't drop a session that is quiet, or whose
program prints nothing for a while. It does not need the server monitor
or its exec channel, and the connection to each jump host on the way
sends its own; through a SOCKS5 or HTTP proxy, the host's keepalive
crosses the proxy. Any answer counts. When three in a row get no answer
(15 seconds each, or the interval when shorter), the connection is
closed and the session shows "Connection lost", ready to reconnect, rather
than looking connected until something is typed. The interval is set in
Options › General; 0 turns keepalives off.

On the first connection to a host, a dialog shows the host key
fingerprint. Once trusted, it is written to `finallyshell/known_hosts` in
the config directory. A connection whose host key has changed is refused.

### Bottom panel in a tab of its own

The button at the right end of the bottom panel's tabs ("Open in its own
tab", two arrows pointing out) opens a panel row: a row in the session
list, indented right below its session, that shows the bottom panel in
the whole right side, without the terminal, for the tables that want
height, such as Processes and Network. The row shows the icon and the
name of the tab it shows, such as "Network". Its tooltip, and the text
the list finds as you type, is "server1 | Network", with the session's
current name; a panel row is not renamed itself. The session's own row
keeps its terminal and its bottom panel as before.

- The panel row is another view of the same session, not a second
  connection: it uses the session's connection, files, tunnels,
  processes and network lists. A folder opened in the panel row is the
  one the session's row shows too.
- It has its own selected tab, which starts at the one selected when it
  opened; the row's icon and name follow it. A click on the side panel's
  process list shows its Processes tab.
- A session has at most one panel row: the button selects it when it is
  already open.
- In the panel row the button is "Back to terminal" (two arrows pointing
  in), which selects the session's row. The panel row stays until it is
  closed with its × or its context menu (Back to terminal, Close).
- Dragging the session moves its panel row with it; the panel row cannot
  be dragged alone. Closing the session closes its panel row too, and
  reconnecting keeps it, showing the new connection. Closing the panel
  row ends nothing.
- A panel row is not a session: the count in "Sessions (N)" and the
  question when quitting leave it out.
- The terminal's actions do nothing in a panel row: full screen, find,
  the command bar.
- A new tab has no button. A session that failed or was disconnected
  still opens its panel row, whose pages show the session's state.

### Coding agents in tabs

While a coding agent runs in a session's shell, the session's row shows
the agent's logo after its name, on the name's line, so that several tabs
to the same server, each running an agent, can be told apart. The logo's
tooltip is the agent's name ("Claude Code"). It is drawn in one color,
the row's text color, as the other icons are. A long name is cut before
the logo. The name itself doesn't change: a renamed tab shows its own
name with the logo, and the transfers, finding a row by typing and the
file editor's tabs read the name alone. A panel row shows no logo; its
session's row does.

| Agent | Its program |
|---|---|
| Claude Code | `claude`, `@anthropic-ai/claude-code` |
| Codex | `codex`, `@openai/codex` |
| Gemini CLI | `gemini`, `@google/gemini-cli` |
| pi | `pi`, `@mariozechner/pi-coding-agent` |
| opencode | `opencode`, `opencode-ai` |
| Amp | `amp`, `@sourcegraph/amp` |
| Cursor CLI | `cursor-agent` |
| GitHub Copilot CLI | `copilot`, `@github/copilot` |
| Qwen Code | `qwen`, `@qwen-code/qwen-code` |
| Goose | `goose` |
| Kimi CLI | `kimi` |
| Kiro CLI | `kiro-cli` |
| Aider | `aider`, `aider-chat` |
| Crush | `crush` |
| Droid (Factory) | `droid` |
| Auggie | `auggie`, `@augmentcode/auggie` |

Aider, Crush, Droid and Auggie show a generic robot, having no logo in
the set the app uses.

How it is told:

- The program is the one in the foreground of the shell's terminal, read
  from the server's `/proc` over an exec channel of the session's own
  connection, with the shell's folder (see [Following the terminal's
  folder](#following-the-terminals-folder)), so two tabs to the same
  server are told apart. It is an agent when its name, or its first
  argument's, is one of those above, whole: `pip` is not `pi`, and
  `claude-monitor` is not `claude`. When the program is a runtime or a
  launcher (`node`, `bun`, `deno`, `python`, `npx`, `bunx`, `pnpm`,
  `uvx` and the like, and `sudo`), the script or package it runs counts,
  so `node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js` and
  `npx @openai/codex` are agents.
- It is read after Enter in the terminal and after the command bar sends
  a command, about 250 ms and 1.5 s later, and after Ctrl+C, Ctrl+D and
  Ctrl+Z, which end or stop an agent without an Enter. While a tab shows
  an agent, it is also read every 10 seconds, so that the logo goes when
  the agent ends by itself. Nothing is read otherwise, and a tab never
  has more than one read in flight. A read that fails, as one that times
  out, changes nothing: the logo stays, and so do the reads every 10
  seconds, until a read that answers.
- Not seen: an agent inside `tmux` or `screen`, or in an `ssh` to another
  host; servers without `/proc` (macOS, BSD); hosts whose exec channel is
  off (`noMonitor`). An agent run with `sudo` is seen, unless `/proc` is
  mounted with `hidepid`.
- Options › Terminal › "Show the coding agent running in each tab", on by
  default, turns it off: no logo, and no reads for it (the reads for the
  file panel still happen while it follows the terminal).

The logos are from [LobeHub Icons](https://github.com/lobehub/lobe-icons)
(MIT license, credited in the third-party licenses). They are their
owners' trademarks, shown only to name the program that is running.

## Connections page

The switch at the top of the bottom-left panel, "Sessions (N) |
Connections", shows the session tabs or the saved connections. The
Connections page is the connection manager's tree of folders and
connections, kept in the main window as MobaXterm's session tree and
Xshell's Sessions pane are, without taking width from the terminal. It
shows names and type badges, in the order the connection manager shows them
unsorted, and never the deleted connections. A connection's tooltip
shows `user@host:port` and the first line of its notes. A connection
with a session open has a dot after its name, in the color of that
session's dot in the session list: green when connected, yellow while
connecting.

- Double-click a connection, or select it and press Enter, to open a
  session, in the selected tab if it is a new tab, as quick connect does.
  The session shows on the right, and the panel stays on Connections, so
  several connections can be opened in a row; "Sessions (N)" counts the
  new ones. Double-click or Enter on a folder opens or closes it, as do
  its arrow and the Left and Right keys.
- The search box at the top filters as the connection manager's does:
  every word must match the name, address, user, notes or connection type,
  ignoring case. The type switch below the search box combines with the
  search and keeps its choice until the app quits.
  The folders holding the results open and the first result is
  selected, so Enter connects to it; Down moves to the tree; Esc clears
  the search and closes again the folders it opened. Its search is its
  own, apart from the connection manager's.
- The context menus are the connection manager's: Connect, Edit,
  Duplicate, Rename (F2), Move to folder, Copy host and Delete on a
  connection; Connect all, New connection, New subfolder, Rename, Move
  to folder and Delete on a folder; New connection and New folder on the empty space
  under the rows. F2 and Delete work on the selected row. Rows are
  dragged onto a folder, or onto a connection for its folder, to move
  them, as in the connection manager.
- Deleting asks first, for a connection too: it moves to the deleted
  connections, which the page does not list, and which "Show deleted" in
  the connection manager lists to restore.
- The "+" in the header is New connection while the page is shown: in
  the selected folder, or the folder of the selected connection. The
  editor it opens, and Edit, belong to the main window (not modal); once
  saved, the page selects the connection.
- Both places read the same connections, so a rename, move or delete in
  either shows in the other at once.
- The key "Switch sessions / connections" (Cmd+Shift+E on macOS,
  Ctrl+Shift+E elsewhere, changed on the Shortcuts page) switches the
  pages: to Connections with the keyboard in the search, leaving full
  screen first, and back to the sessions with the keyboard in the
  terminal.

The page shown is `leftPanelPage` in `settings.json`, and the folders
open are `connectionTreeExpanded`, by folder ID, so a folder keeps being
open as it is renamed or moved. Until a folder is opened or closed on the
page, every folder is open. Neither is synced: they are where this
computer's window was left, as the pane sizes are.

## New tab and quick connect

One new tab is opened at startup. Its "Quick connect" list shows every
host that is not deleted (name, host, user):

- First the hosts connected recently (at most 30), the last one connected
  at the top.
- Then the other hosts, the last one added at the top. The time a host was
  added (`createdAt`) is recorded when it is created, duplicated or
  imported. Older hosts without that time come last, in their saved
  order.

When both parts are present they are headed "Recent" and "Other
connections". A host that was just added therefore appears here right
away, without having to be connected once first.

- SSH and Remote Desktop hosts use distinct icons and type badges. Clicking
  an SSH host or pressing Enter opens a session in this tab; Remote Desktop
  opens in the system client and leaves the tab in place.
- The search box filters as you type, matching name, address, user, notes
  and type. Results keep the same order as without a search. The up and down
  arrow keys move the selection and skip the "Recent" and "Other
  connections" rows.
- "Clear" clears the list of recent connections. It does not delete
  hosts: they are all still listed under "Other connections".
- The "Connection manager" button opens the connection manager. While a
  new tab is selected, connecting from the connection manager also
  connects in that tab.
- When there are no hosts at all, the page shows a "New connection"
  button and, under it, a "Connection manager" button, so a new user can
  import their connections from another client (see below) before adding
  any.
- The "Local terminal" button, on both pages, opens a
  [local terminal](#local-terminal) in this tab.

## Local terminal

A local terminal is a tab whose terminal runs a shell on this computer
instead of connecting to a server, to try something without leaving the
app. The terminal button in the session list's header, left of "+", opens
one at the end of the list and selects it (unless Options › General says
not to select new tabs); the "Local terminal" button of a new tab's page
opens one in that tab. There is no default shortcut.

- It runs the Local terminal program of [Options › Terminal](#terminal-defaults)
  in the home folder, in a pseudo-terminal of MyGo's terminal plugin (a
  Unix pseudo-terminal on macOS and Linux, ConPTY on Windows). Without
  one, it runs the user's login shell (`$SHELL`, else `/bin/zsh` on macOS
  and `/bin/sh` elsewhere), or PowerShell on Windows (`pwsh.exe`, else
  `powershell.exe`, else `%COMSPEC%` or `cmd.exe`). Text is UTF-8: a host's encoding
  and Backspace and Delete settings are for servers.
- Its row looks like a session's, with a computer in place of the status
  dot, in the dot's color: green while the program runs, gray once it
  ended, red when it could not start. Its name is "Local terminal", and
  the line under it "This computer", or the title the program sets (OSC 0
  and 2), as a terminal app shows it. Rename, Duplicate session (another
  local terminal), Disconnect (ends the program), Reconnect and Close
  work as for a session. It is counted in "Sessions (N)".
- The terminal, its font, colors, mouse, keys, find bar and full screen
  are a session's. The command bar sends to it, and keeps its commands in
  a history of its own (`history/local.txt`, see
  [configuration.md](configuration.md)); Tab completes nothing, as paths
  and command names are read from a server, and the history picker lists
  no server history.
- The Commands tab and its command editor send to it. The Transfers tab
  shows the transfers of every session, as on any tab: the queue is the
  app's, and each transfer's Retry and Open act on its own session. The
  Files, Tunnels, Processes and Network tabs stay, so the layout doesn't
  move when switching tabs, and each says "Not available in a local
  terminal". The bottom panel has no "Open in its own tab" button for it.
- The server monitor, on either side, shows a line "Local terminal" in
  place of the server's stats, and nothing is polled. The coding agent's
  icon on the tab is not shown.
- Send to: a local terminal is not in "All sessions", so a command meant
  for every server does not also run on this computer, even when sent
  from the local terminal's own Commands tab: then it goes to the servers
  only. "Choose sessions…" lists it with its computer
  icon, to check it like any session. "Current session" sends to it
  while it is the tab shown.
- When the program exits the tab says "Exited", and Reconnect, a
  double click on its row or Enter in the terminal area (the Reconnect
  shortcut) starts it again in the same tab. A program that cannot be
  found shows "Could not start …" with the error; one that is found but
  cannot run writes its error in the terminal and exits.
- Quitting asks first while a local terminal's program runs, as with
  connected sessions, in a line of its own ("1 local terminal is open.
  Quitting ends its program.").
- Nothing of a local terminal is saved or synced: local terminals do not
  come back after a restart.

There are no split panes in the terminal area, so there is nothing to
split for a local terminal either. Monitoring this computer, a local file
browser and saving local terminals across restarts are not done.

## Connection manager

The connection manager opens with Cmd+O on macOS and Ctrl+Shift+O on
other systems (the key can be changed on the Shortcuts page). It is a
separate window titled "Connections". As in FinalShell, it is a tree of
folders and connections, with the columns Name, Type, Host, Port, User and
Notes (only the first line of the notes is shown). Clicking a column
header sorts by it, and dragging the edge of a header resizes the column.

The host list is stored in `<user config dir>/finallyshell/hosts.json`.
Passwords are never written to it; they are kept in the system's
credential store (see
[Where passwords are stored](configuration.md#where-passwords-are-stored)).

Toolbar:

- New connection.
- New folder: created in the selected folder, or, when a connection is
  selected, in the folder that connection is in.
- Collapse all, Expand all.
- Import: import connections from another client (see below).
- Show deleted.
- A folder filter: "All", or only one folder.
- A type filter: "All types", "SSH" or "Remote Desktop".
- A search box.

The manager opens with the folders open as you left them last time, after
a restart too (`managerTreeExpanded`), and with every folder collapsed the
first time; use Expand all, Collapse all or the arrows to change them.

Below the tree is "Close window after connecting", checked by default. It
is stored as `keepManagerOpen` in `settings.json`; without that key the
window closes.

Search filters as you type, matching name, address, user, notes and type.
Every word must match, ignoring case. Search and both filters apply together.
Folders that contain results are expanded and the first result is selected,
so Enter connects to it. Clearing the search and type filter restores the
folders that were expanded before.

Double-clicking a connection, or pressing Enter, opens a session in the
main window (in the selected tab if the main window has a new tab
selected), brings the main window to the front, and closes the connection
manager if "Close window after connecting" is checked.

The context menu of a connection has:

- Connect.
- Edit.
- Duplicate: the copy is named "<name> (copy)". Proxy or jump host
  settings, tunnels, and the remembered password and proxy password are
  copied too; the entries in the credential store are copied to the new
  connection as well.
- Rename (F2).
- Move to folder.
- Copy host: copies the host address.
- Delete (the Delete key).

The context menu of a folder has Connect all, New connection, New
subfolder, Rename, Move to folder and Delete. Deleting a folder asks first. Its connections
and subfolders move to its parent folder (or to the top level for a
top-level folder), and the question says where they go.

Connect all opens a session to every connection in the folder and in its
subfolders, in the order the tree shows them, each as a double-click
opens it: the first in the selected tab if it is a new tab, the others in
tabs of their own, and the connection manager closes if "Close window
after connecting" is checked. Deleted connections are left out, and on a folder with no
connection the item is disabled. With more than 10 connections it asks
first: "Connect to N servers?", naming the folder. A connection whose
password is not known asks for it, and the next ones wait, so the
prompts come one at a time; Cancel skips that connection and goes on
with the next. The Connections page has the same item.

Dragging a connection or folder onto a folder row moves it into that
folder. Dropping it on a connection row moves it into the folder of that
connection, and dropping it on empty space moves it to the top level. A
folder cannot be dragged into itself or into one of its own subfolders.

Deleting a connection only moves it to the deleted items (`deletedAt`).
With "Show deleted" checked, deleted connections are shown in grey in
their original folders, and their context menu has Restore and Delete
permanently (which asks first). Every permanent delete goes through
`deleteHostForever`, which also clears the password and proxy password
remembered for the host, both in memory and in the credential store.

Deleting (or permanently deleting) a connection that other connections use
as their jump host asks first, and the question lists the connections that
will no longer be able to connect. The jump host drop-down only lists
connections that are not deleted.

A connection editor opened from the connection manager is a modal window
of the connection manager (see
[Child windows](development.md#child-windows)). After it saves, the
connection manager shows and selects the connection right away.

The main window's [Connections page](#connections-page) shows the same
tree, and what changes in one shows in the other at once.

## SSH keys

Open the key manager from **Options → SSH keys**, or from **Deploy public key** on a saved connection's Terminal page. The manager lists keys in `~/.ssh` and key paths referenced by saved connections, including keys outside that directory. Filter or sort by file name, type, size, encryption, comment, SHA256 fingerprint or host count. Key details show the full fingerprint and randomart; **Copy public key** copies the OpenSSH public line, never the private key.

Generate Ed25519 (the default) or RSA 4096 with an optional passphrase, or import an existing OpenSSH-compatible private key. The new pair goes into `~/.ssh`. Replacing an unused pair takes a separate confirmation and backs up both existing files. Deleting a pair referenced by a connection requires a second confirmation; it never removes public keys from servers. Keys outside `~/.ssh` are not deleted by the manager.

**Deploy public key** can target multiple saved SSH connections. It uses their existing credentials and adds the public key to each login user's `~/.ssh/authorized_keys` without root. It keeps a timestamped backup before appending and does not add a duplicate or bypass restrictions on an existing copy. Results are shown per host. Verification opens a new connection that must authenticate with the selected key, without a password fallback. Only verified connections can be switched to that key; existing passwords in the credential store are retained unless you explicitly choose to remove them. If the existing credentials are unavailable, connect normally first and retry.

**Disable password authentication** is a separate, optional server-wide change. It requires a verified key login in this deployment and a second key login that can run the change through the shared root escalation flow. It uses an OpenSSH drop-in when the standard directory is included early enough, otherwise backs up and edits the main config. Configurations with custom Includes or Match blocks are left for manual review. Validation with `sshd -t` and an effective configuration check must pass before reload; on failure the change is rolled back and backups are kept. Keep an independent recovery session open: disabling password authentication can lock out other users and services, and does not disable keyboard-interactive authentication.

## Import connections

Import, in the connection manager's toolbar, opens the "Import connections"
window, a modal window of the connection manager. "Import from" at its top
chooses where the connections come from: the sources below, each in
its own section. The window opens
on the source chosen last since Finallyshell started (FinalShell the first
time).

Choosing a source looks for its file or folder in the places that source
keeps it (the files are read in a goroutine and shown through `App.post`
when done). If none is found, or the source has no place of its own, a
file or folder chooser opens (showing hidden files and folders). Canceling
it says what to choose, with a button to choose it. Once a file or folder
is read, its path is shown at the top (the home folder as `~`, except on
Windows), next to "Choose file…" or "Choose folder…", which reads
another at any time.

The window lists every SSH connection read (Name, Host, User, Folder,
Note), each with a checkbox. All are checked by default. "Select all" and
"Select none" change them all, and Enter, double-click or Space toggles
the selected row.

- A connection with the same host address (ignoring case), port and user
  as an existing host is shown in grey as "Already present as ...". It
  cannot be checked and is not imported. Of two identical connections in
  the source, only the first is imported.
- Files, or lines of a file, that cannot be read (not in the source's
  format, too large, a read error, no host address) count as failed.
  Connections of other kinds (RDP, VNC and such) and connections deleted
  in the other client count as skipped. Both are listed below the table,
  each with its file, or its file and line (`file:line`).
- The Note column says what is not imported of a connection, such as a
  proxy, or that its key file must be chosen after importing, and the jump
  host it will connect through.

"Import N" puts the connections into folders that follow the source's
folder structure. Folders are matched by name level by level from the top:
an existing one is reused, a missing one is created, and only folders that
receive imported connections are created. A source without folders
imports at the top level. The window then closes, the connection manager
expands those folders and selects the first imported connection, and the
bottom of the connection manager shows how many were imported, skipped and
failed, and how many need a key file chosen.

For every source:

- The time added (`createdAt`) is the time of the import, so imported
  connections come first under "Other connections" in quick connect.
- Passwords that other clients store are not imported: they encrypt them
  with keys of their own, and Finallyshell does not decrypt them. Each
  password is asked for at the first connection (remembered by default,
  in the system keychain). A list of servers that has passwords in it
  puts each in the system keychain, as "Remember password" does, never in
  `hosts.json`.
- A private key file the source names becomes the host's key file. A key
  the other client keeps in a store of its own is not imported: the host
  has "Private key" authentication without a key file, and the Note
  column and the result say to choose it.

### From FinalShell

FinalShell keeps its connections in the `conn` folder of its data folder,
which Finallyshell looks for in order:

- Windows: `%LOCALAPPDATA%\finalshell`, `%APPDATA%\finalshell`,
  `%ProgramFiles%\FinalShell`, `%ProgramFiles(x86)%\FinalShell`,
  `C:\finalshell`. If FinalShell is installed somewhere else and keeps its
  data in its install folder, choose the folder yourself.
- macOS: `~/Library/FinalShell`, `~/Library/Application Support/finalshell`.
- Linux: `~/.finalshell`, `~/.local/share/finalshell`.

You can choose the data folder, its `conn` folder, or a folder exported
from FinalShell.

Fields that are imported: name, host, port, user, notes (`description`),
folder, authentication method, and whether the exec channel is enabled (a
connection with it turned off is imported with the server monitor turned
off). The time added is not FinalShell's `create_time`.

- Passwords are not imported. FinalShell stores them encrypted in its
  files; Finallyshell does not decrypt them and does not read that field.
- A connection that uses a private key is imported with "Private key"
  authentication but no key file. FinalShell stores private keys in its
  own `config.json` and the connection only records the key's ID; that
  file is not read, so edit the connection and choose the key file.
- Proxies (`proxy_id` points into FinalShell's own proxy list), port
  forwarding, terminal encoding and backspace settings are not imported
  (the format FinalShell stores those values in is not known yet). A
  connection that used a proxy says so in the Note column.

FinalShell's file format has no official documentation. The import follows
the FinalShell import code of other open source clients (Hatoba,
LiteShell, MeatShell, OpenFinalShell), summarized in the comments of
`internal/importer/finalshell.go`. Parsing is lenient: unknown fields are
ignored, missing ones get defaults (port 22, password authentication), and
a port written as a string is accepted.

Telnet and serial entries are recognized when the file explicitly names
the protocol in `protocol` or `conection_type`. Unknown numeric type codes
are not inferred from the port. Serial settings are used when present.

### From Xshell

Xshell keeps each session in a file of its own, `<name>.xsh`, in its
`Sessions` folder; subfolders are its folders. On Windows, Finallyshell
looks for that folder in order:

- `Documents\NetSarang Computer\<version>\Xshell\Sessions` (Xshell 6 and
  later), the newest version first, in the profile's Documents folder and
  in OneDrive's.
- `Documents\NetSarang\Xshell\Sessions` (Xshell 5).
- `%APPDATA%\NetSarang\Xshell\Sessions` (older versions).

On macOS and Linux, and when Xshell keeps its sessions somewhere else (set
in its options), choose the folder yourself. You can choose the
`Sessions` folder, the `Xshell` folder it is in, or any folder with
`.xsh` files or an `.xts` export (File, Export in Xshell) in it: an
export is read as it is, without extracting it, and its folders are
imported under the folder it is in. The chooser picks folders only, so to
import an `.xts` file, choose the folder that holds it.

Fields that are imported: the name (the file's, without `.xsh`), host,
port, user, notes (the session's Description), folder and authentication
method. SSH, Telnet and serial sessions are imported; Rlogin and other
unsupported sessions are skipped. Telnet also carries over the terminal
type and literal login prompts. For serial sessions, verify the device
name and settings after import; unrecognized settings use defaults.

- Passwords are not imported. Xshell stores them encrypted in the session
  file; Finallyshell does not decrypt them and does not read that field.
- A session that uses a public key is imported with "Private key"
  authentication but no key file: Xshell keeps its keys in its own key
  store, and the session only names the key. Edit the connection and
  choose the key file (exported from Xshell's User Keys in OpenSSH format
  if needed).
- Proxies (a name from Xshell's own proxy list), port forwarding and
  terminal settings are not imported. A session that used a proxy says so
  in the Note column.

Xshell's session files have no official documentation. The import follows
session files published by their users and other tools that read them,
summarized in the comments of `internal/importer/xshell.go`. Files written
by Xshell 6 and later (UTF-16) and by older versions (the ANSI code page,
GBK on Chinese Windows) are both read. Parsing is lenient: unknown
sections and keys are ignored, a missing protocol is SSH, and a missing
port is 22.

### From SecureCRT

SecureCRT keeps one `.ini` file per session in the `Sessions` folder of
its config folder, with a subfolder for each of its folders. Finallyshell
looks for the config folder at:

- Windows: `%APPDATA%\VanDyke\Config`.
- macOS: `~/Library/Application Support/VanDyke/SecureCRT/Config`.
- Linux: `~/.vandyke/SecureCRT/Config`.

SecureCRT can move its config folder (Options › Global Options › General ›
Configuration Paths shows where it is). Choose the config folder, its
`Sessions` folder, or a folder in it (its sessions are imported with the
folders under it). `Default.ini` at the top of `Sessions` holds
SecureCRT's default settings and `__FolderData__.ini` the order of a
folder's sessions: neither is a session. At most 10,000 session files
are read; the first one past that is listed as failed.

Fields that are imported: name (the file name without `.ini`), host,
port, user, notes (the session's description) and folder. SSH2, SSH1,
Telnet and serial sessions are imported; RLogin, RDP and other sessions
count as skipped. Serial imports carry over the COM port or device name
and recognized line settings.

- Passwords are not imported. SecureCRT stores them encrypted in the
  session files; Finallyshell does not decrypt them and does not read
  those lines.
- A session with a key file of its own (public key options) is imported
  with "Private key" authentication and that key file: the private key,
  when SecureCRT names its `.pub` file. `${VDS_CONFIG_PATH}` in the path
  is the config folder. A session that uses SecureCRT's global key gets
  the global key file, from `SSH2.ini` in the config folder, when its
  authentication methods try a public key before a password. A key path
  that cannot be resolved (another SecureCRT variable, a Windows path on
  macOS or Linux, or the global key when no `SSH2.ini` is found next to
  the `Sessions` folder) leaves the key file to choose, as the Note column
  says.
- Firewalls are not imported: a session through a proxy says "Proxy not
  imported" in the Note column, and one through another session, a jump
  host, says "Jump host not imported". Port forwarding, logon actions,
  terminal and appearance settings are not imported either.

SecureCRT's file format has no official documentation. The import follows
VanDyke's scripting examples and the SecureCRT readers of other open
source projects, summarized in the comments of
`internal/importer/securecrt.go`. Parsing is lenient: lines in other
formats and options it does not use are ignored, missing ones get
defaults (SSH2, port 22, password authentication), and multi-line values
(binary values, and lists other than the description) are skipped.

### From MobaXterm

MobaXterm keeps its sessions in `MobaXterm.ini`. On Windows, Finallyshell
looks for it in `%APPDATA%\MobaXterm` (the installed version), then in
`Documents\MobaXterm`. The portable version keeps it next to
`MobaXterm.exe`: choose it there. You can also choose a file of sessions
exported from MobaXterm: "Export all sessions to file" or "Export
sessions from this folder" write a `.mxtsessions` file, and "Save session
to file" a `.moba` file of one session. Files saved on Chinese Windows
(GBK) are read too.

SSH, Telnet and serial sessions are imported; RDP, VNC, SFTP and other
unsupported sessions are skipped. Serial imports include the COM port
and recognized line settings. Fields that are imported: name, host, port, user (none for
MobaXterm's `<default>`), the comment as the notes, the folder (the
session folders, nested as in MobaXterm; a folder exported on its own is
a folder of its name), and the private key file, as the host's key file
(`_CurrentDrive_` in the path is the drive letter `C`). On macOS and
Linux, a key path of Windows is not a file there: the host has "Private key" authentication
without a key file, to choose after importing.

- Passwords are not imported. MobaXterm stores them encrypted (behind its
  master password), apart from the sessions; Finallyshell does not read
  them.
- SSH gateways (jump hosts) and proxies are not imported: the host
  connects directly, and the Note column says so. Its jump host can be
  set in the connection editor afterwards.
- Terminal settings, colors and fonts, the command run at login, X11
  forwarding and MobaXterm's SSH tunnels are not imported.

MobaXterm's file format has no official documentation. The import follows
the description of the format reverse-engineered for MobaXterm 26.3 in
the gist ".mxtsessions file format" by Ruzgfpegk, and the readers of
other open source tools (mobaConverterGo, mobaxterm2ssh, mobaxterm-mcp),
summarized in the comments of `internal/importer/mobaxterm.go`. It has
not been checked against users' files yet, so parsing is lenient:
unknown sections and fields are ignored, missing fields get defaults
(port 22, password authentication), and a line that cannot be read is
listed as failed with its line number.

### From an SSH config

OpenSSH's client configuration, `~/.ssh/config` (on Windows,
`%USERPROFILE%\.ssh\config`), is read when it exists; otherwise, or with
"Choose file…", any file can be chosen (of any name). It is read as `ssh`
reads it:

- Each name of a `Host` line is a connection named after it: `Host web db`
  gives two. Patterns (names with `*`, `?` or `!`) are not connections, but
  their settings go to the names they match, and lines before the first
  `Host` go to every name. Each setting comes from the first line that
  applies, in file order, as in `ssh`: `Host *` with `User deploy` at the
  end gives every host that user unless it has one of its own.
- Fields that are imported: `HostName` (`%h` is the name; without it, the
  host is the name), `Port`, `User` (the local user's name when there is
  none, as `ssh` uses), the first `IdentityFile` as the private key file,
  and `ProxyJump`. Keywords are read in any case, as `Key value` or
  `Key=value`, with values in quotes and `#` comments.
- In the key file's path, `~` stays as the home folder, and `%d` (the home
  folder), `%u` (the local user), `%h` (the host), `%r` (the user), `%n`
  (the name), `%p` (the port) and `%%` are expanded. Other tokens are left
  as they are, and the Note column says so.
- A `ProxyJump` to another name of the file makes that connection the jump
  host, once both are imported (or the jump host is present already); the
  Note column shows it. A chain (`a,b`) uses its last hop. A jump host that
  is not a name of the file (`user@host:port`, or a name with no `Host`
  line), or not imported, is not set: the host connects directly, and the
  Note column says so. The note follows the checkboxes: unchecking the
  jump host changes it at once on the hosts that go through it.
  `ProxyCommand` is not imported either, and says so as a proxy.
- `Include` reads other files in place, with globs; a relative path is in
  the folder of the file chosen (`~/.ssh`, as `ssh` has it). At most 16
  levels and 256 files are read.
- Connections are imported at the top level, with no notes. A host with a
  key file uses "Private key" authentication with that file; the others
  use a password, asked for at the first connection (the file has none).

Listed below the table: a `Host` line with patterns only and a `Match`
block (not supported) as skipped; a `Port` that is not a number from 1 to
65535 (ignored), a line that does not parse (a quote not closed), and an
`Include` that cannot be read or goes past the limits as failed. Other
keywords (`LocalForward`, `CertificateFile` and such) are ignored.

### From a CSV or Excel file

"CSV or Excel file" imports a list of servers kept in a spreadsheet: a
`.csv`, `.tsv` or `.txt` file, or the first visible sheet of an Excel
workbook (`.xlsx`). It has no place of its own, so the file chooser opens
right away. "Save template…" at the bottom of the window saves
`servers.csv` (the downloads folder is suggested): the headers in the
app's language and one example row, to fill in and import.

The first row is the header (a title row or a few above it are skipped:
the first of the first ten rows with a host column is the header).
Columns are found by their header, ignoring case, spaces, `_` and `-`;
other columns are ignored:

| Field | Headers |
| --- | --- |
| Name | name, label, title, 名称, 名字, 别名 |
| Host (required) | host, hostname, address, ip, ip address, 主机, 主机名, 主机地址, 地址, ip地址 |
| Port | port, 端口 |
| User | user, username, login, 用户, 用户名, 账号 |
| Password | password, 密码 |
| Private key | key, private key, key file, identity file, 私钥, 私钥文件 |
| Folder | folder, group, 文件夹, 分组 |
| Notes | notes, note, description, comment, comments, 备注, 说明 |

- A file without a host column is not imported: the window lists the
  headers it found.
- Rows are listed in the file's order. Empty rows are skipped. A row
  without a host, or with a port that is not 1 to 65535, is listed below
  the table with its row number, as failed.
- A host written `host:port` (`[IPv6]:port` for an IPv6 address) is
  imported as the host alone; its port is used when the Port cell is
  empty (a filled Port cell wins). A bare IPv6 address is not split.
  `user@host` gives the user when the User cell is empty.
- Spaces around a cell's value are ignored, except in the Password cell:
  a password is imported exactly as written. A Password cell of only
  spaces counts as empty.
- A folder `Production/Databases` (or with `\`) is a nested folder. A
  list without a Folder column imports at the top level.
- A private key file makes the host log in with that key; its Password
  cell is then not used. The others log in with a password.
- Passwords go into the system keychain, as "Remember password" does,
  never into `hosts.json`. The table has a Password column that shows
  `••••` for the rows with one, never the password.

Text files: the encoding is the one Excel saved them in: "CSV UTF-8"
(with a byte order mark), plain "CSV" (the system's code page, GBK on
Chinese Windows) or "Unicode text" (UTF-16 with tabs). The delimiter is
the one of tab, `;` (Excel's CSV in many European languages) and `,`
under which one of the first ten rows is a header with a host column, so
a title row above the header does not decide it (with several, the one
whose header has the most known columns). A file without a host column
uses its first line's: a tab, `;` when there are more of them than of
`,`, else `,`. A first line `sep=;` says it instead. Quotes are read as
Excel writes them.

Excel files: cells are read as text, numbers without a trailing `.0`, and
a formula's cell as the value Excel or LibreOffice saved with it. Number
formats (dates, currency) are not applied. Excel 97-2003 workbooks
(`.xls`), workbooks protected with a password, and OpenDocument
spreadsheets are not read: the window asks to save the file as `.xlsx`
or CSV. A text file named `.xls`, as some systems export, is read as
text.

Limits: the file is at most 10 MB, and rows after the first 10,000 are
not read (the first one not read is listed). A workbook's parts are
unzipped to at most 32 MB each, and its sheet to at most about a million
cells.

## Connection editor

New connection and Edit connection open a separate window, with its pages
on the left and OK and Cancel at the bottom.

### SSH connection

- Name, Host, Port, Notes (several lines).
- Method (Password, Private key or SSH agent), User, then either
  Password, or the Private key file ("Browse…" chooses it) and its
  Passphrase.
- "Remember password" ("Remember passphrase" for a private key), on by
  default.
- **Two-factor (TOTP)**: paste a Base32 secret or an `otpauth://totp/` URI.
  Plain secrets use six digits, a 30-second period and SHA-1. URIs can
  also specify eight digits, a different period, and SHA-256 or SHA-512.
  Save or remove the secret with confirmation; leave the field blank to
  keep an existing secret. It is stored separately from the login password.
- "Enable exec channel (server monitor)".

Errors are shown under the field they belong to.

When an SSH keyboard-interactive challenge asks for a verification code,
the client uses the host's saved TOTP, waiting for the next code if the
current one has three seconds or less left. Password questions still take
the login password. Without a saved TOTP, interactive connections show the
server's prompt and let you enter the code manually. In the Connections
page or Connection Manager, right-click an SSH host and choose **Copy
current code**. The menu or copy notice shows the remaining validity;
the code itself goes only to the clipboard. Clipboard history is managed
by your operating system.

With **SSH agent**, the host logs in with the keys an SSH agent holds
(Bitwarden, 1Password, KeePassXC, OpenSSH's `ssh-agent`, the Windows
OpenSSH agent service), so no key file is needed. Only the user is asked:
the key file, passphrase, password and "Remember" fields are hidden. The
optional TOTP field is still available. The line "SSH agent: Uses the
address in Options › General" has a **Check** button, which lists the
agent's keys (type, SHA256 fingerprint, comment) without logging in;
listing asks the agent for no approval. Connecting tries the agent's keys
one after the other, as OpenSSH does, until the server accepts one. An
agent such as Bitwarden asks the user to approve each signature, and the
connection waits for that answer however long it takes. The agent's
connection stays open while logging in and is closed once logged in. A
jump host can log in with the agent too; each hop logs in its own way.

When it fails, the bar over the terminal says why, with an "SSH agent
settings" button that opens Options › General: no agent at the address
(naming it, or saying that `SSH_AUTH_SOCK` is not set), the agent has no
keys, the server refused every key in the agent, or the agent did not
sign (the user denied it). No password prompt opens. Agent forwarding
(`ssh -A`) is not supported.

### Remote Desktop

Choose **Remote Desktop** to open the saved connection in the system's
RDP client: mstsc on Windows, FreeRDP on Linux, or Windows App on macOS.
The editor offers display, local resources, connection and gateway settings.

#### Remote Desktop through SSH

In **Connection**, choose **Connect through SSH host** and select a saved
SSH host. **Target as seen from the SSH host** can be a private name or IP,
with an optional port. Leave it empty to use the saved RDP address and port
(3389 by default). Do not combine this mode with a Remote Desktop Gateway.

Finallyshell uses the SSH host's usual sign-in and host-key verification,
or reuses an open SSH session. It checks TCP reachability from that host
before opening a local tunnel on `127.0.0.1` and handing its address to the
RDP client. The RDP client's authentication and certificate checks still
apply; it may warn that the certificate does not match the loopback address.
On Windows and Linux, the tunnel stops when the client process exits. On
macOS, stop it in **Tunnels** when done; Windows App asks for the password.

### Telnet connection

Choose **Telnet** in the connection editor, enter a host and port (23 by
default), and optionally turn on the 30-second keepalive. To connect
without saving a host, enter `telnet://host[:port]` in Quick Connect and
press Enter; wrap IPv6 addresses in brackets. Do not put credentials in
the URL. The Terminal page sets the advertised terminal type, character
encoding and Backspace/Delete behavior; resizing updates the server when
it supports window-size negotiation.

Automatic login is off by default, including for imported hosts. The
Authentication page uses case-insensitive `login:` or `username:` and
`password:` prompts by default; you can supply regular expressions. A
password is sent only after the username prompt has matched and you have
confirmed the clear-text warning. Remembered passwords go only to the
system credential store and are not synced. Leave the password blank to
enter it manually. **Telnet is not encrypted**: use it only on a trusted
network, and do not reuse your SSH password. Telnet connects directly;
it has no proxy or SSH management features.

### Serial connection

Choose **Serial** in the connection editor. Select or refresh detected
ports, or enter a local device manually (`COM4`, `/dev/ttyUSB0`, or
`/dev/cu.usbserial-…`). On Windows, Linux and macOS the app opens the port
directly with your account's permissions; it does not elevate access.

Choose a common baud rate or enter a custom one, and set data bits,
parity, stop bits and flow control (none, RTS/CTS or XON/XOFF). Defaults
are 9600 baud, 8 data bits, no parity, one stop bit and no flow control.
Set CR, LF or CRLF for outgoing line endings. Local echo is off by
default; leave it off when entering passwords on a device. The Terminal
page sets character encoding and Backspace/Delete behavior. If the device
is unplugged, plug it back in and choose **Reconnect**. Serial sessions
have no SSH management features.

<a id="terminal-page"></a>

### Terminal

- Terminal type: the `TERM` sent to the server, `xterm-256color` by
  default.
- Backspace sends, Delete sends and Character encoding: the first choice
  of each is "Default: ...", which follows the
  [Terminal page of Options](#terminal-defaults); each can also be set for
  this host alone. The choices are the same as on that page.

In `hosts.json` these are `backspace` (`del`, `ctrl-h`), `delete`
(`vt220`, `del`, `ctrl-h`) and `encoding` (the names are listed under
[Terminal](#terminal-defaults)); a missing field means "Default". Hosts
saved by older versions have no `backspace`, so they follow the global
setting, whose default is still `^?`: their behavior does not change.

### Proxy and jump hosts

Type is one of None (connect directly), Proxy library, SOCKS5, HTTP,
HTTPS, SOCKS4 / 4a or Jump host.

- Proxy library picks a proxy saved in the proxy manager. The host keeps a
  full copy of it, so editing the proxy there changes every host that uses
  it, and builds without the proxy library connect through the copy. Test
  shows its last test; New proxy adds one and chooses it. A proxy typed
  on this page goes into the library with Save to the proxy library.
- HTTPS is an HTTP proxy reached over TLS, its certificate checked for its
  name. SOCKS4 has a user ID and no password, sends a host name as SOCKS4a
  for the proxy to resolve, and reaches IPv4 only.

- SOCKS5 and HTTP (`CONNECT`) take the proxy's host and port (1080 and 8080
  by default), and a user and password when the proxy needs
  authentication (HTTP uses Basic authentication). With SOCKS5 the proxy
  resolves the host name, so the host can be a name only the proxy's side
  knows.
- Jump host picks another saved connection (not the connection itself).
  It works like `ssh -J`: log in to the jump host first, then have its SSH
  server forward the connection to this host (the jump host must allow TCP
  forwarding). A jump host can have its own proxy or jump host, and the
  hops are connected one after another. If they loop back, both the editor
  and connecting report "The jump hosts form a loop: a → b → a".
- Every hop is treated as if it were connected directly: its host key is
  confirmed on the first connection, and it uses its own login method and
  password. When connecting, passwords that are not known yet are asked
  for one at a time, in order: the host's own, then its proxy's, then each
  jump host's. The prompt is titled "Jump host password" or "Proxy
  password" and says which host it is for and which connection needs it.
  After a host is changed to use a jump host, reconnecting asks for the new
  jump host's password first.
- Proxy passwords are kept in the system's credential store like host
  passwords (following "Remember password" on the SSH connection page), not
  in `hosts.json`.
- Errors name the hop that failed: "Proxy 1.2.3.4:1080: Connection
  refused: no proxy on that port", "Jump host web-1: Authentication
  failed: ...", "Jump host web-1: Cannot connect to 10.0.0.5:22: ...".
- Closing or disconnecting a session also closes its connections to the

#### Proxy manager

The proxy manager (Proxies in the connection manager's toolbar, Manage
proxies… on the Proxy page, or the Proxies page of Options, which shows
the same library) keeps proxies by name: SOCKS5, HTTP, HTTPS
and SOCKS4, with a user and a password. The table lists each with its
address, how many hosts use it and its last test; New proxy, Edit,
Duplicate and Delete change the library, and a search finds proxies by
name, address, user or note. Editing a proxy changes the hosts that use
it; deleting one leaves them their copies, and they connect as before. A
host whose copy was changed on its own, as on a build without the
library, keeps it as its own proxy.

Test, on a row, in the form, or Test all, goes through the proxy to
ip-api.com (in the app's language), then ip.sb, then Cloudflare's trace,
and shows how long the proxy took to open the connection (green under
300 ms, amber under 1 s, red beyond), the address the internet sees and
where it is (country, region, city, network). A proxy that fails says
why: refused, timed out, wrong password, or the proxy refusing the
target.

The library and its passwords sync with the hosts (see
[Sync](#sync)). Builds without the library keep syncing, and change
nothing of it.
  jump hosts.

<a id="tunnels-page"></a>

### Tunnels

The connection editor lists each tunnel's name, type, listener, target
or exit, restrictions, and whether it starts with the session. Double-click
a row or use its menu to edit it. Changes take effect on the next connection.

| Type | Listener | Exit or destination |
|---|---|---|
| Local (`-L`) | This computer | The SSH server connects to a host and port, or to an absolute Unix socket path such as `/var/run/docker.sock`. |
| Remote (`-R`) | SSH server | This computer connects to a fixed host and port. |
| Dynamic (`-D`) | This computer | SOCKS5 CONNECT from the SSH server. UDP and BIND are not supported. |
| HTTP proxy | This computer | HTTP requests and CONNECT from the SSH server. |
| Reverse SOCKS5 | SSH server | SOCKS5 CONNECT from this computer. |
| Reverse HTTP | SSH server | HTTP requests and CONNECT from this computer. |

Leave the listen port blank or use `0` to allocate a free port at start.
Fixed ports are checked for conflicting listeners on the same side, including
wildcard addresses. For remote tunnels, use an IP address or `localhost`.
The default is `127.0.0.1` on the listening machine. Listening beyond
loopback requires explicit acknowledgment; only use it for intended clients.
For public access on the server, check the SSH server's `GatewayPorts` and
firewall policy. The port reported by SSH is not proof of public reachability.

Local SOCKS5 and HTTP proxies can serve both protocols on the same port.
Set a username and password to require SOCKS5 username/password or HTTP
Basic proxy authentication. These credentials are not encrypted between a
proxy client and the listener itself; use loopback or a trusted network.
Never expose an unauthenticated proxy or a Docker socket to an untrusted
network. Remote tunnels can exit directly or through a SOCKS5 or HTTP CONNECT
upstream reachable from this computer, with separate optional credentials.

**Advanced** accepts IPv4 and IPv6 addresses or CIDRs in an IP allowlist,
a concurrent connection cap, a shared rate limit in KiB/s per direction,
and a per-connection idle timeout. Blank or zero means no numeric limit.
Remote allowlists use the origin address reported by the SSH server, not
an HTTP header. Disabling **Start with the session** leaves the tunnel
available for manual start.

### Tunnel recipes

In the connection editor, open **Tunnels → Tunnel recipes…**. In the command palette, the same action is available for a single SSH connection. Pick a goal, edit the suggested addresses and ports, and add the tunnel to the draft. Save the connection to keep it. Local port suggestions skip ports already used by app tunnels or listening on this computer; availability is checked again when the tunnel starts. Remote port suggestions do not probe the server.

- **Server services and intranet:** reach MySQL, PostgreSQL, Redis, MongoDB, Elasticsearch, a web panel, or another machine’s SSH or Remote Desktop. The review shows where to connect your client. The Docker and MySQL recipes can target remote Unix sockets; Docker socket access can be equivalent to root.
- **Forward or reverse proxies:** use SOCKS5, HTTP or a mixed local listener. Reverse proxies let programs on the server use this computer’s network. The wizard can suggest common local proxy ports; a listener is not proof that the suggested protocol works. Shell exports and HTTP settings for APT and Docker are copyable. Only shell exports can be sent to an active terminal, as unsubmitted input after confirmation; no server files are changed.
- **Publishing and LAN sharing:** publish a local service through the server, or share a listener with selected LAN subnets. Review who can connect before accepting a non-loopback address. Use authentication before sharing a proxy and check GatewayPorts and the firewall with Public access before publishing. The recipe does not change the server configuration.

**Import from ssh command…** previews `-L`, `-R`, `-D`, or their OpenSSH config directives without executing anything. The SSH config importer also imports forwards; non-loopback listeners from a config file start disabled until you review them. The gallery can copy an OpenSSH command or directives. Tunnels that OpenSSH cannot reproduce safely (such as HTTP or authenticated proxies, or tunnels with allowlists or limits) are listed as comments rather than emitted as active forwards. `ExitOnForwardFailure` is recognized but does not change Finallyshell’s independent tunnel startup.

### System proxy

Enable **Use as system proxy when running** on a local loopback SOCKS5, HTTP or mixed tunnel without proxy authentication or an allowlist. Blank PAC rules select a manual proxy; `all` routes globally through PAC. Otherwise enter exact domains, `*.example.org` patterns or CIDRs such as `10.0.0.0/8` separated by commas. Nonmatching traffic goes directly. IPv6 CIDRs depend on the PAC client’s `isInNetEx` support. Proxy and PAC support varies by application; some applications ignore system settings.

Supported desktop settings are Windows per-user Internet Settings, macOS enabled network services and GNOME on Linux. The most recently applied tunnel replaces the previous system proxy; the original settings return when the active tunnel stops, on quit, or at the next launch after an unexpected exit. The command palette’s **Restore system proxy** action restores the backup without stopping tunnels.

### Public access and LAN sharing

Right-click a remote tunnel in the editor or in **Tunnels** and choose
**Public access…** (or **Make it public…** for a loopback-only bind).
Choose the server address explicitly. A wildcard bind can let anyone with
network access to the server reach the port. Do not expose an unauthenticated
proxy; use authentication and restrict its sources.

- **Run checks** inspects `GatewayPorts`, available relay tools and the
  server firewall. Changing `GatewayPorts` to `clientspecified` requires
  confirmation and elevated access. The app backs up the configuration,
  validates it, reloads sshd and rolls back on failure. Custom `Match` or
  `Include` rules require manual review. Reconnect SSH after a successful
  change; restarting a tunnel on the old connection is not enough.
- A **server relay** uses a loopback-only remote SSH forward plus an SSH
  command running `socat`, `ncat` or Python 3.8+ on the server. It needs
  `setsid`, a fixed public port and a server that honors loopback binds.
  It does not support source allowlists because it hides client IPs from
  the tunnel. Server ports below 1024 use the app's privilege prompt.
  Enabling a relay turns off automatic and background starts. Use the
  assistant to start it; stopping the tunnel or losing its SSH connection
  stops the relay as well.
- The assistant can allow a TCP port in active UFW or an unambiguous
  firewalld zone. It remembers rules it adds on this computer. Remove
  unused rules from **Managed rules…**, including rules whose tunnel was
  deleted. The assistant cannot inspect your cloud security groups;
  check them separately and restrict them to the intended sources.
- Test TCP or make an HTTP `GET /` from this computer. An HTTP response
  (even an error code) proves only that something answered from here.
  Copy the public address or open it in a browser. To verify access from
  other networks, test from one of those networks too.

For a local tunnel bound to a non-loopback address, right-click and choose
**Share with the LAN…**. Copy the addresses of active, matching adapters
and use the displayed subnet for the source allowlist. Prefer a trusted LAN
and require proxy authentication. On Windows the dialog can check for an
enabled program-specific inbound rule, copy a `netsh` command, or request
UAC to add the rule. The check is best effort: profiles, scope and localized
command output can make it inconclusive. The rule you add applies to all
programs and profiles and remains after the tunnel stops; remove or narrow
it in Windows Defender Firewall when no longer needed.

### Keys and other behavior

- Enter is OK (except in Notes, where it starts a new line), and Esc is
  Cancel.
- Only one editor is open at a time. Clicking New connection or Edit while
  one is open brings the open one to the front, without losing what is
  unsaved in it.
- Changes to the host of a session that is already open take effect at the
  next connection.
- **Forward the SSH agent** is off by default. It asks for agent forwarding on each shell, independently of how the host logs in, using the local agent configured in Options. A remote user with access to the forwarded socket can request signatures while the shell is open; enable it only on servers you trust.
- **X11 forwarding** is off by default. Untrusted mode uses the local X server's SECURITY extension to issue a restricted cookie and refuses to fall back to trusted access. Trusted mode lets remote X clients interact with local windows and input, so use it only on trusted hosts. The local display defaults to `$DISPLAY`, or `localhost:0` on Windows when unset; change it in **Options → SSH keys**. The X server must already be running (for example XQuartz, VcXsrv or Xming). If the display is unreachable, local authorization fails, or the server refuses forwarding, the terminal shows the reason and the shell still opens.
- If the host is deleted in the connection manager while it is being
  edited, OK only says "This connection was deleted" and does not save it
  back.

## Server monitor and system info

The top left, or the top of the column on the terminal's right (see
[Window layout](#window-layout)), shows the current session's server: IP, uptime, load, CPU,
memory and swap, the processes using the most memory, network speed and
latency graphs, and disks. It reads `/proc` over SSH every 2 seconds, so
only Linux servers are supported. A reading that takes more than 10
seconds, as when `df` hangs on a stale network mount, is given up, and
the next one starts on time. When not connected it shows an empty
placeholder like FinalShell's ("-", 0%). A click on the process list opens
the [Processes tab](#processes-tab).

Right-click anywhere on the monitor to move it to the other side ("Move
to right side" or "Move to left side") or to choose which parts show: a
check box each for "Uptime and load", "CPU, memory, swap", "Processes",
"Network", "Latency" and "Disks". The IP line and the System info button
always show. The choice is the same for every host, and the placeholder
hides the same parts. When every part is hidden, the panel shows the IP
line and "Right-click to choose what shows". The stats keep being read
while a part is hidden, so showing it again shows its graph's history at
once. The interface name above the network graph keeps its own menu (see
below). The choice is kept in `monitorHidden` in `settings.json`, on this
computer only.

The network graph shows the busiest interface by default: the one with
the most bytes since the server started, never `lo`. The interface name
at the graph's top right opens a menu of "Automatic (busiest)" and every
interface of `/proc/net/dev` with its current speeds: the others first,
then Docker's `veth*`, then `lo`. Choosing an interface checks or
unchecks it, and with several checked the graph shows their sum and the
label says "eth0 + wg0". Each interface keeps its own history, so a
choice shows the last two minutes at once, and an interface that comes
up later (a VPN) is in the menu from the next reading. The choice is kept
per host (`monitorIfaces` in `hosts.json`, synced with the host). A
chosen interface the server no longer has is left out; when none of the
chosen is left, the graph shows the busiest again and its label says
"eth0 (auto)", with the missing names in its tooltip. Those stay checked
in the menu, marked "not found", until unchecked.

The "System info" button below the IP opens a popover with the host name,
OS (`/etc/os-release`), kernel, architecture, CPU model and cores, memory,
uptime, IP addresses and disks. "Copy" copies them as plain text. They are
read once each time the popover opens, over an exec channel, by a POSIX sh
script that also runs on BusyBox. When tools such as `lscpu`, `nproc` or
`ip` are missing, it skips them or uses something else.

### Overview memory

The **Overview › Memory** card separates non-cache use, buffers and
reclaimable cache, and free RAM. Available RAM is the kernel’s estimate
of what can be used without swapping; virtual memory committed is a
separate measure, not swap usage. The card also shows swap usage,
page-in and page-out rates, and swappiness when available.

**Free memory** drops kernel caches after confirmation; the menu also
offers memory compaction on supported kernels. Both run as root and can
temporarily increase load. Dropping caches
does not free memory held by running apps. The result compares observed
before and after readings, not a guaranteed amount freed. Swap is
read-only in this card.

### Running as root

The app runs a privileged action directly if the SSH login is root, or
tries non-interactive `sudo`. If `sudo` needs a password, the app tries the
SSH login password once when it has one in memory. It can then reuse a
saved privilege password or ask for the `sudo` password. If `sudo` is not
available to the account, the dialog offers the root password (`su`).

The successful method is reused until the connection ends. The password
is masked; selecting **Remember (system credential store)** saves it only
after authentication succeeds. A wrong entry can be corrected, with at
most three dialog submissions per action. If elevation is denied or you
cancel, the app shows commands your administrator can copy to grant sudo
access. Operating-system restrictions such as locked root accounts or
read-only memory controls still apply.

Docker and Podman first try the SSH user and passwordless `sudo`. If the
engine still needs root, select **Use root / sudo** in the Docker tab or its
Overview card. Refreshing does not open a password dialog; it can reuse a
working method already authorized in this connection. Canceling the prompt
leaves the button available. If authorization is refused or still cannot
reach the engine, the app shows the administrator’s sudo commands. For
Docker it also shows `usermod -aG docker <user>`; reconnect after a group
change. Membership of the `docker` group grants root-equivalent access.

### Docker tab › Containers

Filter and sort containers, group by Compose project, and show or hide stopped containers. Select rows using checkboxes, Ctrl/Command-click, or Shift-click, then use the selection menu for lifecycle actions or live stats. Double-click a row for details, or use its menu for logs, a shell, published ports, and more. Removal and other destructive operations show what they affect before proceeding.

Once root access is authorized, all Docker sections and logs use that
account. For an interactive container shell, the terminal sends the
command but never types a stored password: enter it in the terminal if
`sudo` or `su` prompts. Direct access and passwordless `sudo` do not prompt.

**New container** configures the image, command, ports, mounts, environment, network, and resource limits. The run-command preview masks environment and log-option values. **Duplicate container** copies settings without reusing published ports or a static IP; mounts may still share data. **Edit container** replaces a standalone Docker container, retaining settings not shown by the form and attempting to restore the original if the replacement fails. It requires the Docker Unix socket and `curl`; edit Compose services in their Compose file instead. Replacement does not keep changes in the writable layer.

**Live stats** samples CPU, memory, network rates, disk I/O, and PIDs every two seconds while open. **Container files** requires a running, unpaused container with suitable shell utilities. Browse, rename, create directories, and delete entries there. Copy files or folders between the container and an absolute path on the SSH server. The app checks that the destination is free before copying; use the SSH file panel for transfers between the server and your computer. The row menu can also commit a container to an image or export its filesystem as a tar on the server, excluding mounted volumes. Both can include sensitive data.

Directory listings also work in Alpine containers with BusyBox. If the listing cannot represent a filename unambiguously, the app rejects it rather than showing a different name. Renaming reports a conflict instead of overwriting an existing entry.

### Docker tab › Images

The Images section lists local Docker or Podman images, including untagged images, with one row per tag. Sort any column, search references, IDs, platforms or container names, or filter to dangling or unused images. Hover over the creation time for the exact date and over the usage count for the container names. The displayed total counts each image once, not each tag; images can share layers. Select multiple rows with Ctrl/Cmd or Shift to remove them together.

Use the row menu or the selected-image actions for details (including copyable fields and raw JSON), layer history, tagging, pushing, removal or saving a TAR or gzip archive on the server. Saving never overwrites an existing path and requires `mktemp` and `ln -T` on the server, plus `gzip` for compression. The run action takes you to Containers. Normal removal is disabled for images held by containers; on Docker, force removal requires a separate warning and checkbox. Podman force removal is unavailable because it can delete dependent containers. Removing an image does not also prune its parent images; use Prune explicitly for cleanup.

Pull accepts a platform and optionally all tags. Public Docker Hub suggestions are opt-in and unavailable with Podman. Build uses a server-side context and Dockerfile, supports multiple tags and build arguments, and offers a target stage, platform, cache bypass and updated base images. Only build trusted Dockerfiles and never put credentials in build arguments. Load restores an image archive and Import creates a new image from a root filesystem archive. Use a server path or upload a local TAR/TAR.GZ over SFTP into a private temporary directory, cleaned up after the operation when the connection allows it.

Long operations stream engine output and can be stopped; stopping does not undo completed work. If the registry denies a push, check permissions and sign in under System. Prune defaults to dangling images and shows a list and size estimate before you confirm. Including all unused images can also remove tagged images. The client checks that the candidates and container list still match just before cleanup. This check and cleanup are not atomic: the engine decides what is eligible at execution time and reports the actual reclaimed space.

### Docker tab › Compose

Select **Compose** in the Docker tab to inspect projects and their service
containers. The project list shows how many containers are running. The
service table supports filtering, sorting, and selecting multiple rows to
act on their services. The app prefers Docker Compose v2 and uses container
labels when enumeration is unavailable, including with Docker Compose v1
and `podman-compose`. Unsupported options explain why they are disabled.

Deploy, start, stop, restart, pull, or pull and recreate from the project or
service menu. Validate saved configuration before deploying and view or
follow logs per project or service. Operations stream output; canceling
closes the SSH command channel, but work already started on the server may
continue. The app refreshes Compose and Overview after changes.

Open the Compose files or `.env` in the remote SFTP editor. After saving,
return to the project to validate or deploy. **New project** writes
`compose.yaml` and an optional `.env`, never overwriting existing files.
It asks before using a nonempty directory, uses shared root escalation when
SFTP cannot write, and can deploy after validation. The app remembers its
projects locally so even undeployed projects remain listed.

**Take down** removes the project's containers and networks. Volumes and
images are kept unless explicitly selected; the confirmation lists volumes
found by project label when available. **Delete project** can also
remove an app-created directory after you type its project name; everything
under that path, including bind-mounted data stored there, will be lost.

### Docker tab › Networks, volumes and system

- **Networks:** Filter or sort the list, open details to see IP ranges and connected containers, and create networks with IPv4/IPv6, driver options and labels. From the row menu you can connect or disconnect a container, copy network values, or remove selected networks. Pruning in this section removes only the listed unused local networks; built-in, ingress and attached networks are protected.
- **Volumes:** Show only unused volumes, inspect their labels, options and referencing containers, or measure sizes on demand. Browse local-engine mountpoints in Files if the SSH user has SFTP access. Removal and pruning permanently delete data; the app rechecks the volume’s identity before removal. On Docker 23 or later (API 1.42+), pruning in this section targets the displayed unused anonymous volumes by default; explicitly opt in to include named volumes.
- **System:** Inspect engine information and disk usage, stream and filter recent events, or choose cleanup categories with estimates. Pausing events or leaving the Events view stops the remote event stream. Cleanup runs each confirmed step in order and streams the output. Network and volume removal is limited to the named resources. Container, image and build cache pruning applies to all resources the engine considers eligible at execution time. Stopping the wait does not undo changes.
- **Registries and daemon:** Registry management uses the authorized root account’s Docker configuration in root mode; otherwise it uses the SSH user’s configuration. It shows only registry keys and helper names, not credentials. Login requires a configured credential helper; the password goes over standard input. The daemon editor is for a recognized Linux Docker system service using the default socket and configuration path. It uses shared root escalation even when the SSH user can already access the socket. Saving requires Python 3 and `dockerd --validate` to check the running service’s flags before writing; Docker 20.10 can read the configuration, but cannot save it. Saving backs up existing configuration as `daemon.json.bak-<yyyymmddHHMMSS>` (UTC). Restarting needs separate confirmation. Podman does not expose Docker-specific operations such as daemon editing, registry management, or volume pruning.

<a id="terminal-section"></a>

## Terminal

The top right is the terminal. When a session disconnects, the last screen
stays, and a "Reconnect" bar appears at the top.

### Find

Ctrl+Shift+F (Cmd+F on macOS) opens a find bar at the top right of the
terminal (MyGo's `ui.FindBar`).

- As you type, the plugin's `Search` searches the screen and scrollback,
  highlights every match, and shows which match is current out of how
  many.
- Enter goes to the next match (upwards, to earlier output), Shift+Enter
  to the previous one. F3 and Shift+F3 (Cmd+G and Shift+Cmd+G on macOS)
  work too.
- Esc or "Done" closes the bar, ends the search and returns the keyboard to
  the terminal.
- Pressing the shortcut again selects all the text in the find box.
- Each session has its own find bar, and each [pane](#splits) of a split
  session its own; the key opens the focused pane's.
- Matching ignores the case of ASCII letters (that is how the plugin
  works).

### Full screen

Ctrl+Shift+Enter (Cmd+Enter on macOS) hides the left side bar and the file
panel below, leaving only the terminal, and puts the window into the
system's full screen. Pressing it again leaves full screen.

- Esc leaves full screen only when there is no shell to receive it (a new
  tab, a disconnected session). In a connected terminal, Esc goes to the
  shell as usual (vi needs it).
- If the window was already in the system's full screen, it stays there
  when the app leaves its full screen.
- Leaving the system's full screen by the system's means (the green button
  on macOS) also leaves the app's full screen.
- On a [panel row](#bottom-panel-in-a-tab-of-its-own), which has no
  terminal, the key does nothing. Going to a panel row while in full
  screen shows its panel alone.
- A [split](#splits) session shows all its panes in full screen.

### Splits

A session's terminal splits into panes, as a tab of Ghostty, iTerm or
Windows Terminal does, to have a deploy, a log and `htop` side by side on
the same server. Each pane is a shell of its own on the session's SSH
connection, a new channel on it: no new login. A pane of a
[local terminal](#local-terminal) runs the local terminal program again.

- **Splitting**: Split right (Cmd+D on macOS, Alt+Shift+= elsewhere)
  opens a pane right of the focused one, Split down (Cmd+Shift+D,
  Alt+Shift+-) one under it, and the new pane takes the focus. The new
  shell has the same terminal type, keys and encoding as the first. Panes
  split again, in either direction. The session row's context menu and
  the Split button at the end of the [command bar](#command-bar) have
  the same items, with Zoom this pane, Equalize panes, Move pane and
  Close this pane, which act on the focused pane; with one pane, all but
  the two splits are disabled. An item chosen from the button leaves the
  keyboard in the focused pane's terminal. A right-click in the terminal
  opens the terminal's own menu (Copy, Paste, Select All, Clear): MyGo's
  terminal builds it, and the app cannot add to it.
- **Focus**: the focused pane has a blue border, the others are dimmed a
  little. A click in a pane focuses it (the click also goes to its
  terminal, so selecting and programs that take the mouse work as
  usual). Cmd+Option+arrows (Alt+arrows elsewhere) move the focus to the
  pane on that side.
- **Resizing**: the dividers between panes drag, down to 120 DIPs wide or
  60 high a pane, and a double click on a divider shares its room
  equally. Equalize panes in the row's menu gives every pane the same
  room.
- **Moving**: with two panes or more, hovering near the middle of a
  pane's top edge shows a small grip. Drag it onto another pane: that
  pane shows four drop zones (left, right, top, bottom), the side nearest
  the pointer lit up, and dropping splits it on that side with the pane
  dragged, whose sibling takes the room it leaves. The shell, its output
  and the folder the file panel follows stay with the pane: only the
  layout changes. A card of the pane (its number, title and last line)
  follows the pointer; Esc gives the drag up, and a drop on the pane
  itself, or on the side of its neighbor it is already on, changes
  nothing. Only the grip takes the press: a drag anywhere else in a pane
  selects text, and a program that takes the mouse gets clicks beside the
  grip. There is no grip while a pane is zoomed or its find bar is open.
  The row's menu has Move pane, whose items swap the focused pane with
  the one on the left, right, above or below; the Shortcuts page has the
  same four actions, with no keys by default.
- **Zoom**: Zoom this pane (Cmd+Shift+Enter, Ctrl+Shift+X elsewhere)
  shows the focused pane alone in the terminal area; the key again, or
  moving the focus, shows them all.
- **Closing**: Close tab (Cmd+W, Ctrl+Shift+W) closes the focused pane
  while the session has several, and the session with the last one, as
  in Ghostty. A pane whose shell exits (`exit`, Ctrl+D) closes; its
  neighbor takes its room, and the focus.
- **Connection lost**: when the connection drops, every pane ends, and
  the session shows "Connection lost" as with one terminal. Reconnect
  opens a shell in every pane again, in the same layout.
- **A server that refuses**: a server allows a number of sessions on a
  connection (OpenSSH's `MaxSessions`, 10 by default; the monitor and
  SFTP take one each). A split it refuses adds no pane, and a toast says
  why.
- **What follows the focused pane**: the command bar (its placeholder says
  "Sends to the focused pane"), the [Commands tab](#commands-tab) and the
  [command editor](#command-editor) send to the focused pane's shell;
  "All sessions", in the Commands tab, sends once to each session, to its
  focused pane. The file
  panel follows the focused pane's folder, and goes to it as the focus
  moves (see [Following the terminal's folder](#following-the-terminals-folder));
  the row shows the coding agent of the focused pane's shell; the find bar
  works on the focused pane.
- **The session row** shows how many panes a split session has after its
  name (▦ 3).
- Nothing about panes is saved: a session opens with one pane.

### Command bar

Under the terminal is a one-line command bar, as FinalShell's command
input box: a terminal icon, the input, without placeholder text, a
"History" button and a Split button at its end. It is on by default;
Options › Terminal › Command bar turns it off, or any of its popups,
and lists its keys (see [Terminal › Command bar](#terminal--command-bar)).

- Enter sends what the bar holds to the shell, followed by a carriage
  return, as if it had been typed in the terminal (through the session's
  encoding), and clears the bar. The terminal keeps all its keys: the bar
  only has the keyboard while you are typing in it.
- Tab completes the word at the caret:
  - a word that looks like a path (it has a `/`, or starts with `~` or
    `.`) opens the path picker, the server's folder in columns: folders
    first, ending in `/`, then files, with the icons of the file panel's
    icon theme, hidden ones last. Typing filters it (ignoring case),
    Backspace in an empty filter goes up a folder, the arrow keys move,
    and Enter or Tab inserts the entry. A folder inserted lists its own
    entries; a file ends the word with a space. Names with blanks or
    other characters the shell would read are escaped with `\`. At most
    2000 entries are listed, with "and N more" after them. A name with a
    control character (a newline, an escape sequence) is shown dimmed,
    with the character escaped, and cannot be inserted: sent, a newline
    would run what follows it as a command of its own. Command names and
    lines of the server's history with one are left out.
  - the first word otherwise opens command completion: the commands on
    the server's PATH that start with it. They are read once per
    connection, over one exec channel, with bash's `compgen -c` (which
    includes builtins), or a listing of each `$PATH` folder on servers
    without bash. A host whose "Enable exec channel" is off lists
    `/usr/local/sbin`, `/usr/local/bin`, `/usr/sbin`, `/usr/bin`, `/sbin`
    and `/bin` over SFTP instead.
  - any other word opens the path picker for the current folder.
- Relative paths are read from the shell's working directory when the
  shell reports it (OSC 7) or the app read it from the server (see
  [Following the terminal's folder](#following-the-terminals-folder)),
  else from the file panel's folder; `~` is the
  home folder. Folders are listed over the session's SFTP, so a server
  without SFTP shows the error in the popup, while sending and the history
  still work.
- Up, Alt+H (Cmd+Shift+H on macOS) or the "History" button opens the history picker (the
  button keeps the keyboard in the bar, and closes the picker when it is
  open; it is hidden while the history picker is off): the commands sent
  before that
  start with what is typed, the newest at the bottom, next to the bar.
  Up and Down move; Left and Right highlight one field (word) of the
  highlighted command, so `epel-release` can be taken out of
  `yum -y install epel-release` alone. Enter inserts the field in place of
  the word at the caret, or, with no field highlighted, puts the whole
  command in the bar. "Clear list" deletes the host's history.
- The history is the app's own, per host: every command sent from the
  bar, each once, newest last, with no limit on how many (the file is cut
  from the oldest past 10 MB; see
  [configuration.md](configuration.md#command-history)). Below it come
  the commands of the server's `~/.bash_history` and `~/.zsh_history`,
  read over SFTP once per connection, so commands typed in the terminal
  show up too (bash writes its history file when the shell exits).
- Enter sends to this session only, to its focused pane while it is
  split, whatever the Commands tab's "Send to" is set to, and never asks
  first. A command for several sessions is sent from the
  [Commands tab](#commands-tab) or its [command editor](#command-editor).
- The Split button, after "History" (an icon of a square split in two),
  opens the panes' menu above the bar: Split right, Split down, Zoom
  this pane, Equalize panes, Move pane and Close this pane, with their
  keys, the same items as the session row's menu (see [Splits](#splits)).
  They act on the focused pane. It is disabled while the session is
  connecting or disconnected.
- Esc closes the popup; Esc again gives the keyboard back to the
  terminal. "Focus command bar" (Ctrl+Shift+L, Cmd+Shift+L on macOS)
  moves the keyboard to the bar and back.
- New tabs show no bar. In a session that is connecting or disconnected,
  the bar is shown disabled.

Commands you send often can be kept as buttons in the
[Commands tab](#commands-tab).

### ZMODEM (rz/sz)

Run `rz` in an SSH terminal to select one or more local files and confirm the upload. Use plain `rz` without an overwrite option; the app asks the receiver to protect existing files, but the remote program controls its own write policy. Run `sz -- 'file'` to choose a local destination, starting in the configured Downloads folder, and confirm each incoming file. No extra SSH channel, SFTP or privilege escalation is needed.

The Transfers tab shows the current file, speed and cancellation. Verified downloads are published under their original name or with a ` (1)`-style suffix; unsafe names, directories and files of 4 GiB or more are not supported. Retransmissions within a transfer are automatic. After canceling or disconnecting, run the command again to start a new transfer. ZMODEM is not used in local terminals.

### Keyword highlighting

SSH terminals color common errors red, warnings yellow, success indicators green, IP addresses cyan and HTTP(S) URLs blue with an underline. In Options › Terminal › Keyword highlighting, turn highlighting off or edit, preview and scope rules to all hosts or a saved host. Rules can match whole words or regular expressions, with case and style controls. The rules apply to new output; escape sequences, full-screen programs using the alternate screen and ZMODEM payloads are left alone.

### Session recording

Use the recording button in an SSH pane or the session menu to start or stop recording the focused pane. A red dot identifies a recording pane. In Options › Terminal › Session recording, choose a folder, add an ANSI-stripped `.txt` log, set the per-file rotation limit or opt into automatic recording of newly opened SSH panes. Automatic recording is off by default.

Recordings use asciicast v2 (`.cast`) and can be replayed with an external player. Keyboard input is not captured directly, but echoed commands and other visible output are recorded. Stop recording before displaying credentials, private keys or confidential output. Use Open Recordings Folder to find the files.

### Appearance: font, color scheme, background image

The Options pages Terminal › Font, Terminal › Color scheme and Terminal ›
Background image set how terminals look. A change applies at once to
every open terminal (`SetFont`, `SetTheme`), and new terminals use it too.
A change made while a session is connecting is applied once it connects.
Each page has "Restore defaults" at the bottom and a preview at the top: a
sample prompt, a colored `ls` and code with ligatures, drawn with UI
widgets in the same font, colors and background.

Font:

- Font family: as you type, the monospaced fonts installed on the system
  are suggested (from `ui.FontFamilies`). It can be a comma-separated list,
  of which the first installed font is used. Empty means the default:
  JetBrains Mono, Cascadia Mono, Consolas, Menlo, DejaVu Sans Mono,
  monospace.
- Size: 6 to 48, 13.5 by default.
- Line height: 0.8 to 2 times the font's own line height, 1 by default.
- Weight: Light 300, Regular 400, Medium 500, Semibold 600, Bold 700. The
  plugin always draws bold 300 heavier than this, and at least 700; there
  is no separate weight for bold.
- Ligatures: on by default. Turning them off sets `-calt` and `-liga`.

Changing the font ends a Ctrl+= zoom in the terminal; changing only the
color scheme does not.

Color scheme:

- The several hundred themes that Ghostty ships
  (`terminal.GhosttyThemes()`), including the user's own in Ghostty's
  themes folder.
- The search box filters by name. Each row shows the background color and
  a few colors as a sample, and the 16 ANSI colors are shown under the
  preview.
- The default is Tomorrow Night. If the theme file goes missing, the
  default is used.
- The same scheme shows in the light and the dark appearance of the app
  (Options › General › Appearance): there is a single choice, set as both
  the plugin's `Theme` and its `DarkTheme`.

Background image:

- "Choose…" opens the system's open dialog for a PNG, JPEG, GIF, WebP or
  BMP image (a file of at most 64 MB and at most 100 million pixels). The
  image is decoded first to make sure it can be shown, then copied into
  the config directory (`finallyshell/background-<time>.<extension>`), so
  moving or deleting the original does not matter. Choosing another image,
  or "Remove", deletes the old copy.
- Fit: Cover (cropped), Contain or Tile. Opacity: 5% to 100%, 30% by
  default.
- A file that is not an image, or cannot be read, only shows an error. It
  is not copied and does not replace the current image.
- If the copy goes missing (or the name in `settings.json` is not one of
  the app's copies), the page shows "The background image is missing:
  choose it again, or remove it." You can choose an image again or remove
  it.
- Reading and decoding the image never happen on the UI thread. Nothing is
  read at startup. The first time the image is needed, a goroutine reads
  it and shows it through `App.post`; until then the terminal shows only
  the color scheme's background color. If the image is changed or removed
  while it is being read, the old result is dropped.
- An image larger than 3840 pixels on its long side is scaled down before
  it is kept in memory (the copy on disk is still the original file).
  JPEG images are rotated according to their EXIF orientation.

How it is drawn: every terminal is created with `Transparent: true`, and
the app draws the background under it. First the color scheme's
background color, then the image, then a layer of the background color at
(1 − opacity), so text always sits on the color scheme's background.
Background colors set by programs (OSC 11, cell backgrounds) are drawn as
usual.

In `settings.json` these are `termFontFamily`, `termFontSize`,
`termLineHeight`, `termFontWeight`, `termNoLigatures`, `termTheme`,
`termBackground` (the file name of the copy), `termBackgroundFit`
(`contain` or `tile`; missing means cover) and `termBackgroundOpacity`
(percent). A missing key means the default.

## File panel

The "Files" tab at the bottom right browses the server over SFTP: a folder
tree on the left, a file table on the right (with multiple selection), and
a path bar where a path can be typed directly.

### Following the terminal's folder

When the shell's working directory changes, as after `cd /var/log`, the
file panel goes there too. The toggle button after the home button in the
panel's toolbar turns this off and on, for every session; turned back on,
the panel goes to the shell's folder at once. It is on by default.

- The panel moves only when the shell's folder changes. A folder the user
  browsed in the panel stays after a command such as `ls`, until the next
  `cd`.
- A shell that reports its folder (OSC 7: fish, and zsh or bash set up to)
  tells it. Otherwise, on a Linux server, the app reads it from `/proc`
  over a channel of its own on the same connection: the working directory
  of the program in the foreground of the shell's terminal, else the
  shell's. This needs the host's server monitor to be on (it uses the
  same exec channels), no root and no tools beyond the shell.
- It reads as the session connects, for where the shell starts, without
  moving the panel; then after Enter in the terminal, or a line the
  command bar sends: about a quarter of a second later and again after a
  second and a half. Nothing else is read while nothing is typed. A shell
  whose login scripts `cd` elsewhere leaves the panel at the home folder
  until the next `cd`.
- Not followed: macOS and other servers without `/proc` whose shell
  doesn't report OSC 7, root shells from `sudo -i` or `su` (the user can't
  read root's processes), and shells inside tmux or screen. A folder the
  panel can't open, such as one on another host the shell went to with
  ssh, leaves the panel where it was.
- A [split](#splits) session's panel follows its focused pane. Each pane's
  shell is read on its own: of the shells of the connection, the server
  tells them apart by the order they started in, which is the order the
  panes opened them in. While a pane opens or closes their numbers do not
  match, and the read tells nothing until they do.

### File icon themes

The file panel shows icons by file name: `docker-compose.yml` gets the
Docker icon, `main.go` the Go icon, `src/` the src folder icon, and an
expanded folder switches to its open icon. The icons come from VS Code's
file icon theme format, so any VS Code icon theme works. The theme is
chosen on the Options page File manager › File icon theme, which has a
preview at the top, and takes effect at once.

- Built in: Material Icon Theme (the default), Seti (the one VS Code ships
  with), and Minimal (one icon for files and one for folders, as the app
  used to look).
- "Install from VSIX…" takes a `.vsix` file (a VS Code extension package,
  for example one downloaded from [Open VSX](https://open-vsx.org)). After
  checking that it contains an icon theme that can be read, it copies the
  file as is to `icon-themes/<publisher>.<name>.vsix` in the config
  directory. A newer version of the same extension replaces the older one.
  The installed theme is selected automatically. The limit is 50 MB. The
  remove button in the list deletes the copy.
- Icon themes already installed in VS Code, VS Code Insiders, VSCodium,
  Cursor and Windsurf are listed automatically (`~/.vscode/extensions` and
  so on; on Windows, under `%USERPROFILE%`). They are read in place, not
  copied. Uninstalled ones (`.obsolete`) are skipped, and only the newest
  version of an extension is listed. "Refresh" looks again.
- If the selected theme later disappears or cannot be read, the panel
  shows Material Icon Theme and the options page says why.

Only the theme's JSON and the SVG, PNG and font files it refers to are
read; no code from the extension runs. Paths that point outside the
extension are ignored. Themes that use a font for their icons (Seti uses
WOFF, file-icons uses WOFF2) are converted to TrueType before they are
registered with the UI. Icons are matched with VS Code's rules: file name
first, then extension (the longest first, so `a.test.ts` looks for
`test.ts` first), then language (the file name to language table comes
from the language extensions that VS Code ships), and finally the default
file icon. As in VS Code, the theme's `light` section is used only while
the app is light; in the dark appearance the icons are the theme's own.
Switching the appearance switches the icons at once.

In `settings.json` this is `fileIconTheme`: missing means Material,
`minimal` is Minimal, `bundled:seti` is Seti, and anything else is
`<publisher>.<name>/<theme ID>`.

Both built-in themes are MIT licensed; their licenses are inside their
VSIX files (`internal/fileicons/bundled/`). To update them and the language
table, change the versions in `internal/fileicons/internal/gen` and run
`go generate ./internal/fileicons` (this needs git and network access).

### Symbolic links

In the file table, the icon of each symbolic link has a small arrow in its
bottom left corner, and the name is followed, in light text, by where it
points (`bin → usr/bin`). As with `ls -l`, this is the link's text as
written, relative or absolute, not resolved.

- A link to a folder is shown as a folder, sorted with the folders, and
  double-clicking it opens it.
- A link to a file has the type "Link", the size of the file it points to,
  and the link's own permissions.
- A link whose target does not exist (or that loops) is a broken link: its
  name is red, its type is "Broken link", it has no size, and
  double-clicking does not open it. A link whose target cannot be examined
  for lack of permission is not considered broken.
- When listing a folder, up to 16 links are resolved at the same time
  (each needs two round trips, `readlink` and `stat`), so folders with many
  links (such as `/usr/lib64`) do not wait for them one by one.
- The folder tree on the left shows no arrows.

### File operations

Files and folders can be renamed, deleted and created from the context
menus of the file table and of the folder tree:

- The file table's menu has New folder, New file, Rename (one item
  chosen) and Delete (one or more chosen). With the table focused, F2
  renames the chosen item and Delete (or Cmd+Backspace on macOS) deletes
  the chosen items.
- The folder tree's menu acts on the folder right-clicked, without
  opening it: Open, Upload files, Upload folder (into that folder), New
  folder, New file, Rename, Delete, Refresh and Copy path. `/` has no
  Rename or Delete.

Rename, New folder and New file ask for a name. Rename starts with the
name chosen up to its extension, as Finder and Explorer do. A name that is
empty, `.`, `..` or has a `/` in it is refused, and so is a name already
taken in the folder: a rename never replaces another file, and a new file
never empties one that is there. Errors from the server (such as
permission denied) show in the dialog, which stays open.

Delete always asks first. A folder is deleted with everything in it. Links
are never followed: deleting a link to a folder removes the link, not the
folder it points to. Up to 16 files of a folder are removed at the same
time, so large folders don't wait for each round trip. Canceling the
dialog stops a delete that is still running; what was already removed
stays removed.

After a change the folder is listed again and a renamed or new item is
chosen in the table. When the current folder, or one it is in, is
renamed, the panel follows it to its new path; when it is deleted, the
panel goes to the folder it was in.

### Upload and download

Upload: drag files or folders from Explorer or Finder onto the file table
(they go to the current folder) or onto a folder in the folder tree. The
toolbar's upload button and the context menu can also choose files or a
folder.

Download: select files or folders and click the download button. They are
saved in the download folder, which is the system's Downloads folder
(`~/Downloads`) by default and can be changed on the Transfers page of the
options. "Download to…" in the context menu chooses a location instead.
With "Ask where to save each download" checked on the Transfers page, the
download button always asks for the location first with the system's
folder chooser.

### Archive transfer

A folder of many small files is slow over SFTP, which takes several round
trips for every file. "Download as archive" and "Upload as archive…" send
a selection packed with tar and gzip instead, as FinalShell's packed
transfer does:

- "Download as archive" is in the file table's context menu (for a folder
  or several items, not for a single file), in the folder tree's context
  menu (for any folder but `/`) and in the context menu of the toolbar's
  download button (right-click it; a click still downloads). The server runs `tar -czf -` in the items' folder; the app reads
  the stream and unpacks it into the download folder file by file.
- "Upload as archive…" is in the file table's and the folder tree's
  context menus and in the context menus of the toolbar's two upload
  buttons (right-click them; a click still chooses files or a folder). It chooses
  folders; the app packs them into a stream that the server unpacks with
  `tar -xzf -` in the folder, which it makes first if needed. The files
  belong to the user who logs in, whatever the owners on this computer.

Both directions stream: there is no temporary archive on either side.
Each is one transfer in the queue, like the others: the Transfers tab
shows "Download as archive" or "Upload as archive", the progress of the
files' bytes (counted while the transfer already runs), and the file
being packed or unpacked. Cancel stops tar on the server and closes its
channel; a file half written on this computer is removed. Existing items
of the same name are asked about once before starting, then replaced.
Folders, empty ones too, symbolic links, modes and modification times are
kept; owners, devices and sockets are not.

When `tar` is missing on the server, or fails, the transfer fails with
what it printed, and "Retry over SFTP" sends the same items the usual
way, one transfer each.

To pack by default, turn on "Archive transfer" with the package button in
the file panel's toolbar, next to "Follow the terminal's folder", or
with "Upload and download folders as archives" on the Transfers page of
the options; they are one setting, and the desktop's Files app has the same
button. With it on, items dropped from the system's file manager, the
upload and download buttons, Download and Download to… in the menus,
pasting files, and uploads and downloads in the desktop's Files app send
what they transfer as one archive transfer, a single file too, so that
it travels compressed: text and logs shrink a lot, while files that are
already compressed (zip, gz, images, video) hardly do. A symbolic link
on its own still goes over SFTP, which sends the file it points to, and
so does everything on a connection where SSH command execution is
disabled. The desktop's downloads of items from several folders at
once, as in search results, also go over SFTP.

Downloads are unpacked safely: names that are absolute, have `..` or are
outside the items asked for stop the transfer; nothing is written
through a symbolic link (a link where a folder goes is replaced by the
folder, as tar does); a hard link may only point to a file the same
download wrote; setuid, setgid and sticky bits are dropped. The gzip
stream is read to its end, and when its checksum or size does not match,
the transfer fails and says that the files unpacked from it may be
wrong. On Windows, names that Windows does not allow (such as `a:b` or `CON`) fail
the transfer with the name; they are not renamed.

Some symbolic links are not created, and the rest of the download is:

- Links that point outside the download folder: by their target, or
  through other links once everything is unpacked (`a -> b/../..` where
  `b -> ..`), on the disk as it is.
- On Windows, all links when Windows does not allow the user to create
  them (it does in Developer Mode or for administrators).

The transfer then ends as done, with a warning line such as "2 links were
not created: they point outside the folder"; its tooltip lists the links.
"Retry over SFTP" is not offered for it.

### Send to another server

Select files or folders in the file panel and choose **Send to another
server…**. The toolbar button also accepts a drag onto a connected SSH
session. Connected servers appear first; selecting a saved server opens
it using the usual credential and host-key prompts. Browse the destination
with SFTP or enter an absolute path, then review the files and the destination
before starting.

**Automatic** first tries non-interactive public-key SSH from the source to
the destination, requiring a host key already trusted on the source. When
that probe fails, the app streams between the two SFTP connections without
writing to local disk. You can force either mode. Direct transfer streams
tar; the app never sends the destination password or key to the source.

Choose once whether to skip conflicts, rename incoming items, or replace whole
items after making a `.bak-YYYYMMDDhhmmss` backup (UTC). Folders are not merged.
Files and directories are staged on the destination and checked by size
before publishing. Permissions and modification times are preserved, but
ownership, symbolic links, special files and privileged mode bits are not
copied. Names that can be interpreted differently across SFTP servers,
such as names containing backslashes or colons, are rejected. Transfers
run with the existing SSH accounts, without elevation.

The Transfers tab shows a single source → destination job, with bytes,
file counts, speed, estimated time and a cancel button. A canceled or
failed job does not resume. Canceling during publication does not undo items
already published; a lost connection may leave a staging folder, whose path
is reported if cleanup fails.

### Transfers tab

The "Transfers" tab at the bottom right shows progress, speed, time left
and a cancel button. At most 3 transfers run at the same time; the rest
wait in a queue.

- When the destination already has an item with the same name, the app
  asks whether to replace it.
- A failed or canceled transfer can be retried ("Retry"). It is queued
  again on the same session with its current connection; a session that is
  closed or not connected cannot retry.
- A finished download can be opened ("Open", with the system's default
  app; if no app can open it, it is shown in its folder) or shown in its
  folder ("Show in folder").

### Downloads popover

The "Downloads" button at the far right of the file panel's toolbar works
like FinalShell's. While transfers are running, the button shows their
count. It opens a popover:

- The first row is the download folder (with the middle left out when it is
  too long), "Change…", and "Show in Finder" ("Show in Explorer" on
  Windows, "Show in folder" on Linux; the folder is created first if it
  does not exist).
- Below are the recent transfers, uploads and downloads, newest at the top,
  each row the same as on the Transfers tab.
- At the bottom is "Clear finished".

The popover and the Transfers tab show the same queue: the Transfers tab in
the order transfers started, the popover the other way round.

### Progress on the app icon

While transfers are running:

- On macOS, the Dock icon shows their count and the overall progress.
- On Windows, the taskbar button shows the overall progress (MyGo has no
  icon badge on Windows).
- Linux docks that support the Unity launcher API show both.

The overall progress counts the bytes of the transfers that are running
and have a known size. Everything is cleared when all transfers end.
Progress is updated at most 4 times a second, and the count is updated as
soon as it changes.

## Remote file editor

As in FinalShell, double-clicking a file in the file table (or selecting
it and pressing Enter), or choosing "Edit" in the context menu, opens it
in the editor window. Double-clicking a folder still opens the folder.

The editor is MyGo's code editor (`ui/editor`): syntax highlighting for
more than 30 languages, find and replace, go to line, undo, multiple
cursors and folding. The language is chosen by file name; a script without
an extension is recognized by its `#!` line. Languages it does not have,
such as nginx or systemd files, are shown as plain text, and `.conf` is
treated as TOML by MyGo.

### Window

There is one editor window (a child window of the main window, 1000×700,
resizable), with a tab for each file. Each tab remembers the session it
came from. The window title is `host:path` of the selected tab, and a tab
with unsaved changes shows a dot. Opening a file that is already open
switches to its tab.

The toolbar has Save, Undo, Redo, Find, Replace, Go to line, Soft wrap and
Reload from the server. The status bar at the bottom shows "Saved" or the
error from saving, the language, line and column, the encoding and the
line endings (click the encoding or line endings to change them).

### Keys

- Ctrl+S (Cmd+S on macOS) saves.
- Ctrl+W (Cmd+W on macOS, or the key "Close tab" was changed to) closes the
  tab. Closing the last tab closes the window.
- Ctrl+Tab and Ctrl+Shift+Tab switch tabs.
- Esc only goes to the editor (it closes the find bar, the go to line bar
  and completion, and returns multiple cursors to one). Unlike other child
  windows, Esc never closes the editor window: Esc is pressed often while
  editing, and one stray press should not close the window. Close the
  window with its close button, or by closing the last tab.
- The editor's own keys (Ctrl+F find, Ctrl+H replace, Ctrl+G go to line,
  Ctrl+D select next, and so on; Zed's keys on macOS) are described in
  MyGo's documentation.
- The main window's shortcuts do not work in the editor window.

### Reading

The file is read entirely into memory over SFTP in a goroutine (no
temporary file), with progress shown for large files, and shown through
`App.post` when done.

- A file over 10 MB asks first ("Open Anyway").
- A file with a NUL byte in its first 8000 bytes is treated as binary and is
  not opened. "Open with the Default App" downloads it to the app's cache
  folder (`<cache dir>/finallyshell/opened/<time>/`; folders older than a
  day are deleted the next time one is opened) and opens it with the
  system's default app. Changes to that copy are not sent back to the
  server.

### Saving

The editor first writes a hidden temporary file in the same folder
(`.<file name>.finallyshell-<random>.tmp`), gives it the original file's
owner (when the owner differs) and permissions (including setuid and
similar bits), then replaces the original with `posix-rename@openssh.com`.
If the server does not have that extension (or it fails), the original is
deleted first and the temporary file renamed (renaming in SFTP version 3
cannot overwrite an existing file, so for a short moment the file does not
exist). This way programs that read the file only ever see the old or the
new version, and the original is safe if the connection drops midway.

Replacing creates a new file, which loses the original's extended
attributes, ACLs, SELinux label and hard links, and only root can give a
file to another user. So in these cases the editor writes in place instead
(truncate, then write):

- the file is a symbolic link (the target is written, and the link stays a
  link);
- the folder is not writable;
- the owner or permissions cannot be set;
- the rename is refused (for example in a sticky folder such as `/tmp`).

Writing in place keeps everything about the file but is not atomic: if it
fails halfway, the file is left truncated (the text is still in the editor,
so it can be saved again).

If creating the temporary file fails for another reason (for example a full
disk), the editor reports the error and leaves the original alone. If the
file has disappeared by the time it is saved, it is created again.

### Conflicts

The editor records the file's modification time and size on the server
when it is opened (or last saved), and checks them again before saving. If
they changed (or the file was deleted), it asks whether to "Overwrite" or
"Reload". Reloading counts as an edit, so Undo brings your text back.

### Line endings and encoding

- The file's line endings are kept (CRLF or LF; a file with both is saved
  with whichever it has more of).
- UTF-8 is the default, and a leading BOM is kept.
- For a file that is not valid UTF-8, a notice at the top says it may be in
  GB18030 (or GBK) and offers "Reopen as GB18030". Saving it unchanged
  writes back the bytes that are not UTF-8 as they were.
- The status bar can switch to GBK or GB18030 (`golang.org/x/text`). Before
  any change, the original bytes are decoded again with the new encoding;
  after a change, only the encoding used for saving changes. A character
  that GBK does not have (an emoji, for example) makes saving fail with the
  line it is on.

### Unsaved changes

- Closing a tab, or closing the window with its close button, asks "Don't
  Save / Cancel / Save" first. For the window it is "Save All": the tabs
  that are saved close, and the window closes once all of them are saved.
- When quitting the app, the confirmation also says how many files have
  unsaved changes. This question is asked whether or not "Confirm before
  closing the window" is on.
- When the main window is hidden to the tray, an editor window with
  unsaved changes is not closed.

### Disconnected sessions

When the session disconnects, the text in the editor is kept, a notice says
there is no connection, and Save is disabled. After the session reconnects,
saving uses its new SFTP connection. If the session's tab was closed, the
file is saved through another connected session to the same host.

A file without write permission fails to save in the regular editor. Use
the code editor to edit with elevated permissions.

### Code editor

- In the file list, choose **Open in code editor** or **Edit as root in code editor**. Each invocation opens a separate window. When a tool opens several files (such as a Caddyfile and its imports), a collapsible tree lets you open them in tabs. Modified files have a dot.
- The editor highlights Caddyfile, Nginx, systemd units, dotenv and INI files, as well as MyGo's built-in languages. For Caddyfile, use `Ctrl+Space` for directives, global options and matchers, `Tab` to move between snippet fields, and `Ctrl+/` (`Cmd+/` on macOS) to toggle comments. The toolbar provides search, replace, line navigation, undo/redo, wrapping, folding and font sizing. The status bar lets you choose the language, indentation and LF or CRLF; UTF-8 BOMs are preserved.
- **Save** (`Ctrl+S` / `Cmd+S`) and **Save All** show the exact paths and a split or unified diff before writing. The editor shows pending operations and their results, including validation output and backups. The Caddy save hook receives all modified files as one batch, with their original bytes for conflict detection. A rejected validation leaves edits unsaved; Caddy does not offer a bypass.
- Without a save hook, the editor refuses changed files and symlinks, backs up each original as `<file>.bak-<yyyymmddHHMMSS>` and replaces it atomically, preserving owner and permissions. SSH-user saves require the SFTP server's atomic rename extension; root saves go through the shared escalation layer. Ordinary multi-file saves are atomic per file, not a single transaction. Files larger than 5 MiB open as a read-only preview of the first 256 KiB; non-UTF-8 text and binary files are not editable.
- Disconnecting keeps edits in memory. Reconnect to the same host and retry; for an editor opened by a tool with its own checks, reconnect the original SSH session. A conflicting file is never overwritten. Closing a modified tab or window asks whether to save, discard or cancel.

## Desktop mode

Open **Open desktop** from an SSH session's menu, the bottom-panel corner,
or an SSH host's menu in the connection manager or Connections page. From a
saved host it reuses a connected session or starts a connection. The desktop
has its own session row; it uses the existing SSH connection and installs
nothing on the server. The app's full-screen shortcut works on this row too.

A server shows its terminal or its desktop, as its terminal mode and its
desktop mode, and the app knows which from the row the user is on or was
on last. Its tab's menu and its row's in the session list say Switch to
desktop mode or Switch to terminal mode, whichever it does not show; so
does the desktop's own menu on its wallpaper, and the host button of the
dock. The button by the bottom panel's tabs switches to the desktop, and
the shortcut "Switch between terminal and desktop" (Cmd+Shift+B on macOS,
Ctrl+Shift+D on Windows and Linux) switches both ways, from the terminal
too. The terminal's own context menu (Copy, Paste, Select All, Clear) is
built by MyGo's terminal and takes no items of the app's.

### Desktop windows

- Drag a title bar to move a window; drag any edge or corner to resize it.
  Double-click the title bar to maximize or restore. Drag to the top, left,
  or right edge to preview and apply a snap. A narrow desktop may allocate
  more than half its width to respect an app's minimum width. A window can
  be moved partly past the desktop's left, right and bottom edges; 120 DIPs
  of its title bar stay on the desktop to drag it back. New and restored
  windows still open wholly on the desktop.
- Press anywhere in a window to focus it. The title-bar menu restores,
  minimizes, maximizes, keeps a window on top, or closes it. The shell
  remembers each app's last resized size in memory, not its position.

### Desktop icons and folders

- Click to select; use Ctrl (Command on macOS) to toggle items, Shift to
  select a range, or drag a rectangle from empty space. Double-click or
  press Enter to open; single-click opening is optional.
- Drag an icon onto another grid cell or onto a folder. **New › New folder**
  creates a folder with an inline name field. Open it to see its apps,
  drag icons back out, rename it, or delete it and return its icons to
  the desktop. This never changes files on the server.
- The desktop menu contains icon sizes, auto-arrange, name/type sorting,
  refresh, Files, Terminal and Personalization. Removing an icon does not
  uninstall the app; the launcher can add it back. Path shortcuts sent
  from Files open the file manager at that path.

### Dock, launcher and personalization

The translucent dock groups the launcher and assistant, pinned and running
apps, and desktop controls. A dot indicates a running app; the focused app
has a wider marker. Click to open, focus, minimize or restore. For multiple
windows, pick one from the list. Right-click for window and pin actions, or
drag pinned apps to reorder. **Show desktop** hides the visible windows; use
it again to restore them. The clock uses local time; the host popover shows
the connection status and lets you return to the terminal or switch to
another connected SSH session's desktop.

The launcher groups available apps and searches names in the current
language. Right-click an app to pin it or add it to the desktop. Escape or
a press outside closes the launcher.

In **Settings › Personalization**, choose one of eight illustrated wallpapers,
eight solid colors, or a local PNG, JPEG, GIF (still frame), or WebP image.
The same page sets icon size, single/double click, cascade/stack placement,
and whether maximized windows cover the dock. In cover mode, move the
pointer to the bottom edge to show the dock. You can reset the dock pins and
wallpaper independently. Icon layouts and folders are saved per host; other
personalization stays on this device.

### Desktop mode › This PC and Files

**This PC** shows quick access to the remote home folder, common Linux folders and favorites, plus mount points with their filesystem and available space. Double-click a location to open it in **Files**. Each Files window has its own folder, navigation history, selection, sort order and view. Use the address bar to jump to an absolute path, type to filter the current folder, or press Enter in the search field to search subfolders (up to 300 results). The sidebar stores the last 20 locations and allows reordering favorites by drag. Notes are stored per host on this device and appear in the Details view and icon tooltips. When reading a folder, searching or opening a file takes more than a moment, a card with a spinner and Cancel says so over the list's bottom right; the list itself does not move, so a double click lands where it was aimed.

Create and rename items inline, or use the command bar and context menus to open a terminal, transfer files, compress, extract, or edit properties. Opening text uses the remote file editor; other files can be downloaded and opened with the system app. Upload files or folders from the picker, by drag and drop, or by pasting a file list from the system clipboard. Remote download supports HTTP, HTTPS and FTP, and runs on the server. Operations that need SSH command execution are unavailable on connections where it is disabled.

Cut and copy work between Files windows. On one server Paste copies or moves; between servers it copies using the transfer queue, directly when the servers support it and otherwise through the client without writing the data to disk. Replace preserves the original as a backup; Keep both gives the copy a numbered name. Upload and archive extraction refuse existing destinations. Archives are extracted on the server into a temporary folder and only regular files and directories are supported. Properties can change octal permissions and ownership, including recursive changes after confirmation. Delete moves items to Trash; Shift+Delete explicitly confirms permanent deletion.

### Desktop mode › Trash

Deleting files from the desktop file manager moves them to the remote trash
after confirmation. The Trash window shows the original path, deletion time,
size, permissions, owner and file note. Search by name, path or note, sort the
list, and select one or more items to restore or permanently delete. The context
menu also copies the original path or opens its parent in Files.

Restore recreates missing folders. If a destination already exists, keep both,
skip, or replace it; Replace first moves the existing item to the trash, so it
remains recoverable. Notes follow restored files, including renamed copies.
Restore all and Empty trash apply to the listed trash,
not just search matches. Permanent deletion and emptying show the item count,
size and affected paths before proceeding. You can cancel a batch; items
already processed stay changed.

The app uses the freedesktop Trash layout on Linux without installing a service.
It keeps files on their original filesystem, in the user's trash or a partition
trash. If moving requires elevation, the notification and operation log identify
root's trash. Use the shield button to authorize viewing root's trash if it is
not already available. Elevated changes refuse folders other users can redirect.
Items without readable metadata can be permanently
deleted but cannot be restored to an unknown location. Stale metadata without a
matching item is cleaned up on refresh.

### Desktop mode › Task Manager

Task Manager monitors the connected Linux server over SSH; it installs
nothing on the server. **Performance** shows load, CPU, memory and swap,
disk activity, and network traffic. Select a disk or partition to see
throughput, IOPS, capacity and mount points, or a network interface to
see traffic, addresses and link speed when reported. Charts retain up to
60 samples. CPU details include time distribution and per-logical-CPU
charts. Load is busy from 70% per logical CPU and overloaded from 100%.
Dropping caches or compacting memory asks for confirmation and uses the
shared root-access flow.

**Processes** refreshes every two seconds after each read. Pause it,
search, sort any column, or group processes by parent. Double-click a row
for basic details, mappings, file descriptors, environment variables or
TCP/UDP connections. Sensitive environment values are masked unless
revealed explicitly; protected fields can be retried as root. Process
actions distinguish graceful exit, force kill, stop, resume and priority
changes. Each confirms the specific process and checks its start time
before acting. Confirmed changes are recorded in the desktop operation log.

**Users** shows login sessions reported by `who`; **Services** is a
read-only list with service and startup status when available. Task
Manager stops its reads when minimized, closed, or no longer visible.
The session's normal monitor is independent. GPU metrics and a separate
startup page are not included.

### Desktop mode › Settings

Settings shows the Linux distribution, hostname, SSH user, kernel,
virtualization, uptime, addresses, CPU, memory and mounted storage. The
connection alias can be edited here. Addresses are hidden by default
and omitted when you copy the overview while they are hidden. Public IP
detection contacts `api.ipify.org` from the server only when requested.
Personalization shows the desktop shell’s appearance and window options.

The operation log belongs to the open desktop. Filter it by app or text,
export the matching entries, or confirm to clear the entire local log.
Server logins are separate: the desktop reads `last` and can request
administrator privileges to read failed attempts with `lastb`.

DNS shows effective resolvers and supports presets, custom IPv4/IPv6
nameservers and search domains. It writes through systemd-resolved, the
NetworkManager profile for the default route, or a regular
`/etc/resolv.conf`. An unknown manager, managed symbolic link or resolver
file mounted by the container runtime is never overwritten. The first
backup of an editable configuration is kept and can be restored. The dialog
names the files or commands; NetworkManager reactivation can interrupt
SSH. After a change, the desktop tests `example.com`. Global
systemd-resolved settings do not change per-interface DNS.

Time zone & time shows the live server clock, estimated difference from
this computer, time zone, NTP status and hardware clock. It searches the
server’s time zones and can change the zone, toggle NTP or request a
manual synchronization when supported. Server changes require
confirmation, use shared privilege escalation and appear in the desktop
operation log. Security links to the App lock, SSH key manager and the
System tab’s security tools.

### Desktop mode › Terminal, Browser and App Center

- Each Terminal window opens its own shell channel on the existing SSH connection. Minimizing it keeps the shell running; closing a live shell asks first. The toolbar opens its current folder in Files, starts another shell after exit, changes font size, copies visible terminal text, pastes and finds text. Use the terminal's right-click menu or copy shortcut to copy a selection. Folder tracking uses the shell's reports or Linux process information when available.
- Server Browser opens a local Chrome, Edge, Brave, Chromium or Firefox window through a loopback-only SOCKS5 proxy on this SSH connection, without changing the system proxy. Use the URL field or the suggested server-local ports to open pages. **Check exit IP** explicitly contacts `api.ipify.org` through the proxy. Stopping the browser, closing its desktop window, or losing the SSH connection stops the proxy. The listener is local to this computer; other local programs can use it while it is running.
- App Center lists the client's built-in apps, not an installation store. Search or filter by category, reopen recently used apps, or use an app's menu to add it to this host's desktop or pin it to the dock. The server tools reuse the existing management pages and their confirmations, including Docker, databases, website and certificate management, system tools, networking and transfers. Every app appears on the desktop by default.

### Desktop mode › AI Assistant

Configure your own OpenAI-compatible service in **Options › AI Assistant**. Pick a provider preset, enter the base URL and the default model, list any other model IDs under **Other models**, one per line (**Add a fetched model** adds those **Fetch models** found), and test the connection. Only loopback Ollama can be enabled without a key. Requests use the same system or environment proxy as the updater.

Open **AI Assistant** on a server desktop. Its top bar shows the server it works on and the model it answers with: the model menu switches the current conversation to the default model or another listed one, and each reply shows the model that wrote it. The server's conversations are listed on the left (the first button of the top bar folds them away; a narrow window lists them under **History** instead). Enter sends the question and Shift+Enter starts a new line. The first question of a conversation sends the question and the assistant's instructions only; later ones also send the recent conversation, which **Include chat history** turns off. Opt in to a previewed attachment: the detected OS, working directory, up to 20 terminal lines and selected log lines, when available, folded to its first line above the question until you open it. The performance prompt can also attach current load, CPU, memory and top processes. Recognizable secrets are masked, but always check the preview before sending. Attachments and replies based on them are not written to chat history.

**Common tasks**, under the question and on the welcome screen, lists 77
prompts for what is done most often on a server, in sixteen categories:
system checks, performance, disks, networks and ports, security, users,
services, logs, Docker, web servers, databases, backups and cron jobs,
software updates, certificates, the firewall, and commands and scripts.
A search finds them across the categories; a click puts one in the
question, to fill in what [ ] marks and send. The conversations on the
left delete from the button on their row or their context menu, after
asking, even while another conversation answers. Several conversations
answer at once: **New chat**, or a click on another conversation, leaves
the one answering going on behind; its row in the list spins while it
answers and shows a warning sign while a command waits for you, and a
click on it shows it as it is. Only a conversation answering cannot be
deleted; the bar above the question drags it taller or shorter, and a
double click on it gives the default height back.

**Server profile:** on a server connected over SSH, the assistant reads, once per connection and with read-only commands, what the server is: system and version, init system, package manager, whether `sudo` works without a password (asked only of members of an admin group), CPU, memory and disk, time zone, firewall, SELinux or AppArmor, container tools, the services running and failed, language runtimes and listening ports. It goes with the questions, as untrusted data dated to the day, so that the commands fit the server: `apt` or `dnf`, `systemctl` or OpenWrt's `init.d`, `ufw` or `firewall-cmd`. Each conversation keeps the profile it started with. **Server profile** in the window's top bar shows it, and **Refresh** reads it again. A slow or failed read never holds a question back; it goes without one. Options › AI › **Send the server profile with questions** turns it off.

**Context:** a conversation's history is budgeted in tokens, set in Options › AI › **Context length** (32K, 64K by default, 128K or 200K). A question sends the newest history that fits six tenths of it, with what the commands printed while the app runs. Past three quarters of it, the model first summarizes the earlier part (goal, server facts, commands and changes, current state, open problems, decisions, next steps), keeping the last three questions as they are; the summary stands for that part from then on, and a line in the chat marks where it ends. A summary that fails leaves the question to go on with the history that fits. Within one question, the oldest command results are shortened first when many rounds run long.

**Guard rails:** a command that may cut the SSH connection the assistant works over (stopping or restarting the SSH server, taking the network down, turning on a firewall that does not let SSH in, killing every process of a user) never runs on its own, not even with **Run everything automatically**: its card warns that it may cut the connection and waits for `RUN` typed. **Commands to confirm** in Options › AI holds regular expressions, one per line: a command matching any of them waits for you to allow it, read-only or not, and the card names the rule; lines that are not regular expressions are flagged and ignored. One question runs at most 30 rounds of commands.

Under each question are Copy message and Edit and resend: editing puts
the question back in the field, with a note above it, and sending
replaces it and the conversation after it. Under each reply is Copy
message, and under the conversation's last reply Regenerate, which asks
its question again for another reply in its place. When a request fails,
Retry beside the error asks the question again.

On a server connected over SSH, the assistant runs commands itself to investigate: the model calls a tool that runs one command at a time on an SSH channel of its own, apart from the terminal, as the login user, with no terminal and no input. The model gives each command its time limit (`timeout_seconds`, 10 to 1800 seconds, 120 when it does not), more for a long scan. Each command runs as a job on the server, detached from the SSH channel, in a folder of the user's own (`/tmp/finallyshell-ai-<uid>/`, mode 700) that holds its output and exit code; one still running after 20 seconds goes on in the background, its card saying so, while the app looks at it every few seconds on a short channel, and the model gets its result when it ends. A running card counts its time against its limit, and **Wait 5 more min** puts the limit off as it runs, up to an hour in all; at the limit, or when the question is stopped, the command is stopped with its process group. When the output is longer than the model gets, the model is told where the whole of it is kept on the server, to read the part it needs. Folders older than a day are removed as a new command starts. It works with POSIX `sh` alone, BusyBox and OpenWrt included. A command stopped at its limit hands the model what it printed so far, with how to get it done: narrow or split it, run it at low priority with `nice -n 19 ionice -c3`, or run it again with a longer limit, which it does on its own. Each command shows as a card with its state, exit code and time; its output (the first and last 16 KB, secrets masked) opens below it. Read-only commands run at once: an allowlist of programs and options that change nothing, such as `df`, `ps`, `journalctl -n`, `systemctl status` or `docker ps`, also behind `nice`, `ionice` or `timeout`, with no redirection to a file, command substitution, variables or `sudo`, and not reading key or password files. Any other command waits on its card for **Allow** or **Refuse**; refusing tells the model to ask instead. Commands that look destructive also need `RUN` typed. **Run everything automatically** lets one conversation run the others without asking, but not the destructive ones; it is off in every new conversation. A question runs at most 30 rounds of commands. Commands that change the system go to the desktop's operation log. Command output, logs and attachments reach the model as untrusted data. Conversations keep the commands and their exit codes, not their output. Without SSH (local terminals, Telnet, serial), the assistant only suggests commands, as it does with a model or provider that refuses tools: the question is asked again without them, and the window says why.

Copy a shell snippet or insert a single line without pressing Enter. **Run** shows the exact line and asks for confirmation; obvious disk-destructive commands also require typing `RUN`. Use an empty shell prompt: the assistant cannot verify what program or input is currently in the terminal. **Ctrl+Shift+I** (**Cmd+I** on macOS) opens a terminal popover that suggests one command and never runs it. To explain a terminal selection, copy it and use **Explain copied text with AI** from the command bar’s context menu, then review and send it in the assistant. The command popover only suggests; the assistant has no managed built-in service or knowledge base.

## System tab

The System tab of the bottom panel manages the connected server itself: a row at its top switches between Services, Logs, Scheduled jobs, Disk, Updates, Security, Firewall and Websites. Each section reads the server when it shows, over SSH; what needs root goes through the same escalation as the other features, and every change asks first. Not available for local terminals, nor when the host's monitor is off. On OpenWrt it shows the sections of [System tab › OpenWrt](#system-tab--openwrt) instead.

### System tab › Services, logs and scheduled jobs

- **Services:** Filter and sort units by state or startup policy. Select one or more rows and use **Actions** or the context menu. Details show status, the unit file and properties. Editing a drop-in backs up the existing `override.conf`, saves the replacement and reloads systemd; it does not restart the service. OpenRC and SysV service lists, when available, are read-only.
- **Logs:** Follow the system journal, a unit journal or an absolute remote file path. Filter the journal by priority and time, or filter received lines by literal text or a regular expression. Pause freezes the display while up to 20,000 recent lines (and at most 24 MiB of text) buffer. A hidden viewer closes its SSH stream. Copy or save the filtered view; local export refuses to overwrite an existing file. Recently entered paths stay on this computer, not in the connection sync.
- **Scheduled jobs:** Switch between your crontab, another user's crontab, system cron files and systemd timers. Personal crontab edits preserve unrelated lines and back up the existing crontab in the writing user's home (root's home when elevated). Disabling a job comments it out without deleting it. The next three times are estimates using the server's current UTC offset, not future daylight-saving changes or `CRON_TZ` settings. System cron files are edited separately with a backup; timers are read-only. **Run once now** shows command output and does not reproduce the cron environment or support `%` input syntax.

### System tab › Disk, updates, security and firewall

- **Disk** lists mount usage and scans one filesystem at a time. Enter an absolute path or open a mount; double-click a directory to scan it, or switch to **Largest files** for up to 100 files over 50 MiB. Scans have a timeout, can be canceled and are cached until refresh or disconnect. Right-click to open Files, copy a path or delete selected items that are not protected. Turn off **Scan as root** to operate as the login user inside directories that ordinary users can modify. An emptied `.log` file is backed up next to the original. Cleanup offers APT and DNF caches, archived system journals with a size limit, and matching rotated logs; visible sizes are estimates, not guaranteed savings.
- **Updates** shows the OS, kernel, latest available package versions, security updates and reboot requirements when supported. Refresh indexes or upgrade all, security or selected packages on APT, DNF and YUM. Zypper, APK and Pacman show the locally available index read-only. Package operations stream output and cannot be canceled from the app once confirmed; keep SSH connected until they finish. Confirming **Reboot server** schedules a restart in one minute.
- **Security** groups recent SSH failures by source IP, username or day, and lists successful logins, present and past sessions, recent sudo users, and selected effective SSH settings. IP locations use the existing lookup settings. Fail2ban lists active jails and their banned IPs, supports confirmed bans and unbans, and does not ban the current SSH source.
- **Firewall** shows UFW numbered rules, Firewalld zones and listening ports. It protects the configured SSH port, the actual connection port and other SSH ports it can verify. UFW adds a source-specific SSH allow rule before enabling. Unmodified packaged pre-rules are recognized; potentially unsafe custom pre-rules still prevent enabling. Firewalld changes only the chosen port or service in the live and persistent configurations, without reloading unrelated runtime rules. Rich rules, nftables and iptables are read-only. Existing firewall configuration files are backed up beside the originals before changes.

### System tab › Sites and certificates

- **Websites:** Inspect Nginx version, service state and master PID. The sortable list comes from `nginx -T`, including HTTP virtual hosts, listen options, TLS expiry, upstream or document root, and the configuration file and line. Apache and Caddy are detected but not managed. Use the row menu or **Site actions** for details, editing, and access or error logs under `/var/log`. On Debian-style layouts, you can enable or disable multiple sites with matching `sites-enabled` links without deleting their configurations.
- **Configuration:** Create a static site or a reverse proxy to a port on `127.0.0.1`, optionally using existing TLS certificate and private-key paths. A new site goes in `sites-available` with a matching link or in `conf.d`. Changes require confirmation. An edit saves a sibling `.bak-YYYYMMDDHHMMSS` backup, refuses stale files, and reloads a running Nginx only after `nginx -t` succeeds. When the test fails, the previous file is put back at once and the editor shows your edits again, so the next start of Nginx never meets a broken file; if putting it back fails too, **Restore backup** does it by hand. Link and creation changes are rolled back on a failed test. Custom layouts, unsafe file ownership or permissions, and include patterns that would pick up a backup can prevent changes.
- **Nginx settings:** The third tab, as a hosting panel has it. Service shows whether nginx runs, its version and master PID, with Start, Stop, Restart, Reload and Test; Start and Restart test the configuration first, and Stop and Restart ask first. Configuration and logs edits the main `nginx.conf` as a site's file is edited, and opens the error and access logs on the Logs page. Performance tunes `worker_processes`, `worker_connections`, `keepalive_timeout`, `client_max_body_size`, `client_header_buffer_size`, `client_body_buffer_size`, `server_names_hash_bucket_size`, `gzip`, `gzip_min_length`, `gzip_comp_level` and `server_tokens`, each with what it does and nginx's default. Saving lists the changes and asks first; each directive is changed where nginx reads it (main, `events` or `http`, never inside a `server`), or added at the start of its block, and every other line, comments included, stays as it was. The file is saved as an edited one is: backed up, tested with `nginx -t`, reloaded, and restored at once when nginx refuses it.
- **Certificates:** View public certificates used by Nginx and Let's Encrypt `fullchain.pem` files, including the subject, SANs, issuer, public-key type and validity dates. Expiry turns yellow under 30 days and red under 7 days, including expired certificates. **Check domain** compares the certificate presented on port 443 from the SSH server with the certificate and trust result from this computer; the server-side probe does not verify trust.
- **ACME:** Inspect Certbot lineages and common renewal timers or cron entries. `acme.sh` is detected only. Certbot renewal and its staging dry run both ask first because existing hooks may run. Issuing a certificate requires the Certbot Nginx plugin and asks before publishing names to an ACME CA and letting the plugin change Nginx. Supported Nginx configurations are backed up before running these tools. A canceled stream does not guarantee that the remote operation has been undone; refresh and inspect its state before retrying.

### System tab › OpenWrt

On OpenWrt and its derivatives (ImmortalWrt, iStoreOS and others with `/etc/openwrt_release`), from 21.02 to 25.12, the System tab shows the sections a router needs instead: Status, Interfaces, DHCP, Port forwards, Services, Packages, Logs, then Scheduled jobs and Disk as on other servers. The desktop has an **OpenWrt** app with the same sections. Changes run as root and ask first; they go through `uci` and reload only the service concerned, as LuCI does.

- **Status:** the release, model, kernel, uptime, load, memory, the root (overlay) and `/tmp` space, the package manager (opkg, or apk from 25.12), the firewall (fw4 with nftables, or fw3 with iptables) and whether there is an SFTP server. **Back up configuration** saves a `sysupgrade -b` archive, as LuCI's backup, to the download folder. **Reboot** restarts the router.
- **Interfaces:** netifd's interfaces with their state, protocol, device, addresses, gateway, uptime and traffic. **Restart interface** (`ifup`) and **Stop interface** (`ifdown`) warn when the connection goes through the interface.
- **DHCP:** dnsmasq's leases with their remaining time, and the static leases. **Make static** gives the selected device its address for good; static leases can be added and deleted.
- **Port forwards:** the firewall's redirects. Add one (name, TCP and/or UDP, external port or range, internal IP and port, zones, `wan` to `lan` by default), enable or disable it, or delete it; the firewall reloads its rules without dropping connections.
- **Services:** the scripts of `/etc/init.d`, whether procd runs them and whether they start at boot. Start, stop, restart, reload, enable or disable one; stopping or restarting `network`, `dropbear`, `sshd` or `firewall` warns that the connection may drop.
- **Packages:** the installed packages and the upgrades in the downloaded lists. **Update lists** downloads them (OpenWrt keeps them in memory, so a reboot drops them). **Install…** searches the lists by name; **Upgrade** and **Remove** act on the selected packages. Upgrading core packages one by one is warned against, as OpenWrt does.
- **Logs:** the last 200, 500 or 2000 lines of the system log (`logread`) or the kernel log (`dmesg`), filtered as you type.

OpenWrt has no SFTP server until `openssh-sftp-server` is installed. The Files tab of such a router says so under its error, with **Install SFTP server**, which downloads the lists, installs the package and connects the file manager again.

The Processes tab, the Task Manager and the monitor's process list read `/proc` where BusyBox's `ps` has no `-o`, as on OpenWrt; the disk scan uses `readlink -f` where there is no `realpath`.

## Caddy tab

The Caddy tab manages the web server of an SSH connection. It includes Overview, Sites, Global options, Config files, Certificates, Logs, and Install & migrate. Open it as a full-size tab for more room. Server operations that need root use the connection's privilege escalation. Configuration changes are backed up, written atomically, and validated against the complete configuration before reloading a running service. A stopped native service stays stopped. If validation or reload fails, the previous files are restored.

### Caddy tab › Overview and service

The Overview detects an existing installation automatically and shows its service or container, version and available update, config and data directories, custom modules, admin API reachability, listening addresses and port conflicts. When the data is available it also shows service resource usage, websites, expiring certificates and reverse proxy upstream failures. Start, stop, restart, graceful reload, validate and boot settings each show a confirmation before running. Stopping Caddy takes its websites offline; port conflicts link to the System tab's Websites section.

### Caddy tab › Sites

- Filter sites by address, type, target or file. The full table shows TLS, the configuration file and line, and indicators for basic authentication, IP rules, headers and access logging. In a narrow panel, hover over an address for these details.
- Create or duplicate a site with a live Caddyfile preview. Managed layouts create a separate file for each new site; other layouts append to the main Caddyfile. Templates cover static files, reverse proxies, Node.js and Go applications, PHP/WordPress, SPAs, www redirects, authenticated file browsing, API gateways, Docker ports, WebSocket and load balancing. PHP-FPM can look up sockets on the server; the Docker template uses published TCP ports from the connection's Docker overview.
- The form covers TLS, client certificates, IP and authentication rules, compression, headers and CORS, request size, access logs, path routes and proxy health checks. Caddy hashes new basic-auth passwords on the server; plaintext never appears in the preview or saved configuration. Use the code editor for sites with settings or comments the form cannot preserve.
- Use the row menu to disable, enable or delete a site, open an HTTP(S) address, or test response headers from this computer or the server. Validate checks the full candidate configuration without saving. Changes name the addresses and files, then back up, validate and hot-reload the configuration; a failure restores the previous files.

### Caddy tab › Global options

- Browse settings grouped into general, binding and ports, admin API, logging, TLS/ACME, on-demand TLS, servers, local CA and metrics. Compound settings accept Caddyfile syntax with examples, and server settings can be edited individually in the draft. Unknown and sensitive entries are not rewritten by the forms.
- For trusted proxies, use Caddy's private ranges or download Cloudflare's official IPv4 and IPv6 ranges from this computer. Review the draft before saving and only trust proxies that actually forward your requests.
- Add, edit or remove named snippets. Use the code editor to change an existing snippet's name and its imports together. Saving asks for confirmation, checks for intervening edits, validates the entire configuration and hot-reloads; a failure restores the previous files.

### Caddy tab › Config files

Browse the main config and its imports, and open them together in the code editor. Formatting and backup restoration show a diff before applying. A file that changed after it was read is not overwritten. Adapt a Caddyfile to JSON or view the running configuration from the admin API; both previews are read-only. The service's environment file can also be opened if detected. Keep private values out of shared screenshots.

### Caddy tab › Certificates and logs

The certificate list includes the issuer, key type, location and time remaining; less than seven days is red, and less than 30 days is amber. Caddy renews its managed certificates automatically. Copy the public local CA certificate or download it when its file is available via SFTP. The log viewer follows the service journal, container logs, or a detected access log file. Pause, clear, filter with text or a regular expression, or inspect JSON access logs in columns and view individual entries in full; a log without access entries, as the service journal, stays text across the whole width, with a note why.

### Caddy tab › Install, update and migrate

The wizard recommends an installation method and previews startup, autostart and optional firewall changes (80/TCP, 443/TCP and 443/UDP in active UFW or the default firewalld zone). If another web service holds ports 80/443, leave Caddy stopped or explicitly confirm stopping and disabling recognized services. Failed installation attempts restore the affected firewall rules and service state. Updates use the detected installation method; Compose-managed containers should be updated through Compose. Uninstall keeps configuration and data by default. Removing them from a standard native installation requires typing `caddy`; the files are moved to a private recovery directory.

Adopt an existing installation without editing it, review a diff for managed-layout conversion, or move a supported standalone binary to an official package. For Nginx, select and review editable drafts; complex rules are flagged for manual review. Optional cutover validates the reviewed config, stops and disables Nginx, enables and starts Caddy, and verifies that its service process owns both TCP ports 80 and 443. Failure restores the previous config and service states; Nginx files are never removed. Port checks do not verify individual websites. Migration between Docker and a host service is not supported.

## Tunnels tab

The session's **Tunnels** tab shows each listener (including its assigned
port), target or exit, status, last error, open, total and rejected
connections, and traffic in both directions. The chart shows recent
one-second traffic samples. The tab count is the number of live listeners.

Right-click a row or open **Tunnel actions** to start or stop it after
confirmation, copy its address or an example command, view its connection
log, or edit its definition. Copied proxy addresses and commands omit
credentials. For a reverse proxy, use them on the server, not on this computer.
The in-memory log shows the last 200 completed or rejected connections for
that run and can be filtered or copied; it does not record URLs, headers or
request bodies. With an HTTP keep-alive client the log lists its last target.

Enabled tunnels start when the SSH session connects. A failed listener
does not fail the session. Closing, disconnecting, or losing the session
stops its tunnels; reconnecting uses the host's current settings. When the
host uses a jump host, forwarding uses the connection to the final host.

## Tunnel manager

Open **Tunnel manager** from the command palette, the Tunnels tab's network
button, or the tray menu. **Tunnels…** in an SSH host's context menu opens
the manager filtered to that host.

- Tunnels are grouped by host. Search by tunnel or host name, or filter by
  host, type and state. The table shows the actual address once listening
  (including a dynamically assigned port), target or egress, operating mode,
  active and total connections, traffic history and uptime. On narrow windows,
  scroll the table horizontally for the remaining columns.
- Select rows to start, stop or restart them together. **Start all background**
  applies to tunnels marked for background autostart; **Stop all** stops
  managed listeners and pending retries across hosts without disconnecting any
  terminal. The context menu copies the active address, shows the latest error
  and next retry, or opens the host's terminal. Add and edit open the host's
  connection editor. Editing or deleting an active background tunnel stops it;
  start it again to apply the new configuration.
- Background tunnels share a separate SSH connection per host, without a
  shell or monitoring. They reuse the host's saved credentials, jump hosts,
  proxy and SSH keepalive settings. An unknown or changed host key never opens
  a prompt or gets accepted automatically: connect once in a terminal, verify
  the key, then retry. Network failures and listener refusals retry with
  jittered exponential backoff, from about two seconds to at most one minute.
  Waking the computer or regaining network access checks the connection early.
- Background autostart runs marked tunnels at app launch, even if they are
  not set to start with the terminal. The manager's checkbox controls autostart
  on this device; its row menu changes which tunnels are marked. Neither change
  starts or stops a running tunnel immediately. **Start without a terminal**
  starts the selected tunnel in the background without changing its saved flag.
- Closing the main window to the tray keeps background tunnels alive. Quitting
  closes them and cancels retries. A dot on the main **Tunnels** tab indicates
  background listeners; an exclamation mark signals a failure or a pending retry.

## Processes tab

The "Processes" tab at the bottom right is the process manager of the
current session, as FinalShell's "advanced process management". A click
on the side panel's process list opens it too.

- The table lists every process with its PID, user, memory (RSS), CPU %,
  name, command line and executable path. A click on a header sorts by
  that column, a second click reverses it; CPU, highest first, is the
  default. The filter above it matches the name, the command line and
  the user, ignoring case, and the count beside it says how many are
  shown.
- The list is read again every 2 seconds while the tab is shown, and not
  at all while another tab or session is shown. The chosen row stays on
  its process as the rows move. Refresh reads it at once.
- CPU % is the share of one CPU the process used since the last reading,
  from the CPU times in `/proc/<pid>/stat`, as `top` shows it: a busy
  process with several threads can pass 100%. The first reading shows
  `ps`'s average over the process's life (0 on BusyBox, whose `ps` has
  none).
- The path of another user's process needs root to read and shows "-".
- A double click, Enter or "Open Details" in the context menu opens the
  process in a tab below the list, named like `7267-java`, with its PID,
  name, user, full command line, executable path and working directory
  on the left and its environment variables on the right. The tab's
  buttons copy all of it as text and read it again; the environment's
  context menu copies a value, a `NAME=value` line, or all of them.
  Fields that need root show "-" with "Needs root". A process gone since
  says so. Tabs close with their ×; the divider between the list and the
  tabs, and the one between the fields and the environment, can be
  dragged.
- The context menu of a row also has Copy PID, Copy Command Line, Send
  Signal (SIGTERM, SIGKILL, SIGHUP, SIGINT, SIGSTOP, SIGCONT) and Kill….
  Every signal asks first. Kill sends SIGTERM; when the process is still
  running 3 seconds later, the dialog offers "Force Kill" with SIGKILL.
  When the server refuses, the dialog shows its words, such as "The
  server refused: Operation not permitted". After that refusal, **Run as root**
  retries through the SSH privilege prompt (when needed) and checks the
  process start time again before sending the signal. A process that ended while
  the dialog was open gets no signal, even when another process has its
  PID by then: the dialog says "The process has already ended. No signal
  was sent."
- When the session disconnects, the last list stays, greyed, under the
  same reconnect bar as the terminal's. Servers that are not Linux show
  "Only Linux servers are supported", and hosts whose server monitor is
  off show that it is off: the tab uses the same exec channels.

Each reading is one POSIX sh script over an exec channel, as the server
monitor's: `ps` for the users, memory and command lines, `cat` of every
`/proc/<pid>/stat` for the names and CPU times, and one `ls -l` of the
`/proc/<pid>/exe` links for the paths. It runs with BusyBox's `ps` and
`ls`, and leaves out its own processes. Details read
`/proc/<pid>/cmdline`, `exe`, `cwd` and `environ` once when the tab
opens. Signals are sent with `kill`, by a script that first checks the
PID's start time in `/proc/<pid>/stat` against the one the list read.

## Network tab

The "Network" tab at the bottom right is the network monitor of the
current session, as FinalShell's "advanced network monitor": which
processes listen on which ports, who is connected to them, and how much
each moves.

- The table has a row for each listening socket (TCP in LISTEN, and UDP
  sockets waiting for packets) and a row for each process with other
  connections, those it opened: PID, name, listen IP, port, protocol,
  remote IPs, connections, upload and download per second, and the bytes
  uploaded and downloaded. The connections to a listening socket count
  under it, whichever process holds them (a forked `sshd`, nginx's
  workers); a socket several processes share is the lowest PID's. A
  click on a header sorts by that column. The filter matches the name,
  the PID, the listen address and port, and the remote addresses of a
  row's connections.
- The table is read again every 2 seconds while the tab is shown, and not
  at all while another tab or session is shown; Refresh reads it at once.
  The chosen row stays on its socket or process as the rows move.
- Upload is what the server sent, download what it received. The rates
  are the difference between two readings of each TCP connection's byte
  counts (`bytes_acked` and `bytes_received` of `ss -i`): a new connection
  shows 0 until the next reading. UDP has no connections in the TCP
  sense: its sockets show "-" for the counts.
- Without root, the processes of other users' sockets can't be read:
  those connections are under a row "-", and the count says "Needs root
  for other users' processes". Connections in TIME-WAIT belong to no
  process.
- A double click, Enter or "Open Connections" in the context menu opens
  the row in a tab below the table, named like `0.0.0.0:80` or
  `7267-java`, with each connection: location (only with the location
  data, below), IP, port, state, upload and download per second, and the
  totals. A busy server's tab shows the first 5000 connections, and says
  so. The context menu of a connection copies its IP or address, traces
  the route to it, and, when online lookups are on, looks it up online.
- When the session disconnects, the last reading stays, greyed, under the
  reconnect bar. Servers that are not Linux, and hosts whose server
  monitor is off, show it, as the Processes tab does.

Each reading is one POSIX sh script over an exec channel: `ss -tunapiH`
(iproute2), with `awk` keeping only the byte counts of its TCP
information. Without `ss`, as on BusyBox, it reads `/proc/net/tcp`,
`tcp6`, `udp` and `udp6`, finds each socket's process by the socket links
in `/proc/<pid>/fd`, and its name in `/proc/<pid>/stat`; `/proc` has no
byte counts, so rates and totals show "-".

### IP locations

Nothing for IP locations is in the app, and without the data nothing is
looked up anywhere: the tables show IP addresses only, with no Location
column.

- The first time the Network tab shows, a bar above the table asks "Show
  where IP addresses are? This downloads about 11 MB of location data
  (ip2region)." with Download, Not Now and Don't Ask Again.
  - Download fetches `ip2region_v4.xdb` of [ip2region](https://github.com/lionsoul2014/ip2region)'s
    release v3.18.0 into `geo/` in the config directory, with its
    progress in the bar and Cancel. The file is written under a
    temporary name and kept only when its SHA-256 is the one the app
    keeps for it; a failed or canceled download leaves nothing behind,
    and the bar offers Retry. The data is read into memory off the UI
    thread, its checksum checked again each time.
  - The file comes from GitHub (`raw.githubusercontent.com`), and, when
    GitHub cannot be reached, sends nothing within about 15 seconds,
    answers with an error or serves a file of the wrong size or
    checksum, from jsDelivr (`cdn.jsdelivr.net`), which mirrors the same
    release and is often reachable where GitHub is not, as in mainland
    China. Both serve the same file: the checksum is the same whichever
    serves it. The progress starts from 0 again when the download moves
    to jsDelivr; Cancel stops it without trying jsDelivr. When both fail,
    the bar names each host and what it answered. The IPv6 file is too
    large for jsDelivr (it serves files of up to 20 MB) and comes from
    GitHub only.
  - Not Now hides the bar until the app starts again.
  - Don't Ask Again turns locations off (`geoLocations` `"off"`) until
    they are turned on in the options.
- With the data, the connections show the country, region, city and ISP
  of each address, from the data, which has Chinese names in China and
  English ones elsewhere. Private, loopback, link-local, CGNAT
  (100.64.0.0/10) and multicast addresses show "LAN" and are never looked
  up.
- IPv6 addresses need a data file of their own, about 37 MB more: the bar
  asks for it the first time an IPv6 peer shows (`geoLocationsV6`).
- Looking up online is off by default and turned on in the options (see
  [Network monitor](#network-monitor)). Then the addresses of the rows
  shown for which the data has no city or ISP are sent to ip-api.com,
  100 in a request, in the app's language (English or Chinese names),
  keeping to its limit of 15 requests a minute with the counts it sends
  back; while it answers 429, ip.sb is asked instead, one address at a
  time. The answers fill in what the data lacks, and are kept for 30
  days in `geo/cache.json`. "Look Up Online" in a connection's menu asks
  again, and prefers the online answer.

The data is ip2region's (Apache-2.0 or MIT). ip-api.com's free service is
unencrypted (HTTP) and for non-commercial use only.

### Trace route

"Trace Route" in a connection's menu, or the route button of the
toolbar for an address or host name typed in, traces the route from the
server in a tab below the table named like "Trace 8.8.8.8": hop, IP,
round trip (the average of the probes that answered), location, ASN and
host name.

- The tab runs `nexttrace --traceroute --raw -M -g <en|cn> <target>` on
  the server over an exec channel:
  [NextTrace](https://github.com/nxtrace/NTrace-core) runs as its own
  program, and locates the hops with its own service, as it is made to;
  `-M` keeps it from uploading the route for a map, and `-g` asks for the
  locations in the app's language (`cn` for Simplified Chinese, `en`
  otherwise). The app never calls NextTrace's service itself. Its RAW
  output is a line per probe as it goes, so each hop shows as it
  arrives; a hop that answers from more than one router shows the first.
  `--traceroute` keeps NextTrace in its traditional mode once its default
  becomes MTR (no earlier than 2027, its README says); a NextTrace too
  old to know the flag, as its `--help` shows, runs without it.
- When `nexttrace` is not on the server's PATH (with `/usr/local/bin`,
  `/usr/sbin` and `/sbin` added), the tab says so, with the install
  command of NextTrace's README, `curl -sL https://nxtrace.org/nt | bash`,
  and a Copy button: the app never installs anything on the server. It
  traces with `traceroute -n -w 2` or `tracepath -n` instead when the
  server has one, the hops showing as they arrive, located with the
  ip2region data when it is downloaded; with neither, it says so.
- NextTrace and traceroute need root, or the capability to send raw
  packets, on many servers: their error shows in the tab.
- The tab's buttons copy the route as text and trace it again.

## Run on many hosts

Open **Run on hosts** from the connection manager's selection or context
menu, or from the Commands tab, next to **Send to**. In either place,
**Ctrl+Shift+P** (**Cmd+Shift+P** on macOS) opens the command palette.
The separate window can stay open beside the terminals.

- Search the connection tree and check individual SSH hosts or whole folders
  (including subfolders). Remote Desktop hosts and SSH hosts with exec
  disabled cannot be selected. Type a POSIX Shell script or load a saved
  quick command. Unlike **Send to**, this does not type into the terminals.
- Each run and retry asks for confirmation and shows the actual targets.
  Default limits are 10 simultaneous hosts and 60 seconds per host, including
  connection and elevation. **Stop scheduling on first failure** skips hosts
  that have not started; those already running finish independently.
- Open sessions are reused. Other hosts use saved SSH authentication and
  proxy/jump-host settings. Unknown or changed keys, including jump-host
  keys, are never accepted here: connect interactively and verify them first.
  Use interactive connection setup to provide any missing SSH login secret.
- **Run as root** uses shared escalation. If a sudo or root password is needed,
  the dialog can reuse it in memory for other hosts in this run. **Remember**
  stores an authenticated password for that host in the system credential store.
- Results can be sorted, filtered by status, or selected for stdout/stderr
  and batch retries. **Retry failed** includes timeouts. Results keep up to
  1 MiB per host and can be copied or exported as CSV or plain text; replacing
  an existing export requires confirmation and makes a timestamped backup.
- The last 20 runs keep the target IDs, options and summary locally, not
  output. Enable **Save script in local history** to reopen the script too.
  Do not put secrets in scripts or print them to output. Back up any files
  that a script will overwrite first. Cancelling or timing out closes the
  execution channel; it cannot undo changes or guarantee remote jobs stop.

## Database tab

Use **Database** on a connected SSH session to browse MySQL/MariaDB,
PostgreSQL or Redis. The panel can be opened as a full-size tab. Each
database connection uses the existing SSH session; it opens no local
port and installs nothing remotely. **Find databases** suggests addresses
from listening TCP sockets and Docker-published ports. Docker discovery
can be retried through the shared root prompt when access is restricted.

- Connections are saved per SSH host. The address is relative to the SSH
  server (default `127.0.0.1`). If the database is on a different machine,
  prefer TLS with certificate and name verification: SSH alone does not
  encrypt the leg from the SSH server to the database. The password is
  stored only in the system credential store when **Remember password**
  is selected and authentication succeeds.
- Choose a database, schema, table or view in the tree. Data loads 200
  rows at a time. Sort by a column for repeatable pages, enter a WHERE
  expression without the `WHERE` keyword, or right-click a cell to view
  its full value. NULL is distinct from an empty string and binary values
  display in hex. Structure shows columns, constraints and indexes, with
  the CREATE statement (a reference reconstruction on PostgreSQL, not a
  restorable dump). Table cells are not edited directly.
- The SQL editor runs the selection or all statements, with a result for
  each executed statement, elapsed time, and affected rows where available.
  Query history holds the last 100 runs per open connection in memory only.
  Query results are capped; export the displayed result or page to CSV or
  JSON. JSON preserves NULL, while CSV uses an empty field. An existing
  export requires confirmation and is backed up before replacement.
- Redis uses cursor-based `SCAN`, not `KEYS`. Browse strings, hashes,
  lists, sets, sorted sets and streams, and inspect TTL. Edit text strings
  and hash fields without clearing expiration, or select multiple keys to
  delete. The console runs one command per line; quote arguments with
  spaces. Credential and connection-control commands are not supported.
- **Read-only** is off by default. When on, only a conservative set of
  read commands is accepted; SQL reads run in a read-only transaction.
  Server-side routines may have side effects despite using read syntax.
  Use a database account with read-only permissions for an independently
  enforced boundary. Writes require confirmation, run in order, and stop
  at the first error. Cancellation does not undo changes, and completed
  statements may already be committed. Back up important data first and
  never put passwords or private keys in the editor or console.

## Commands tab

The "Commands" tab at the bottom right holds quick commands, as
FinalShell's quick command panel: commands you keep, as buttons, to send
to the current session with a click. They are the same for every host,
and kept in `quickcommands.json` (see
[configuration.md](configuration.md#quickcommandsjson)).

- The groups are on the left: "All", which shows every command, then
  your groups in their order, each with how many commands it has, and
  "New group", which names a new group in place (Enter keeps the name,
  Esc gives it up). A group's context menu renames it or deletes it,
  after asking; its commands are kept, in no group, under "All".
- The commands of the group chosen are buttons on the right, in rows
  that wrap. A button shows the command's name and, in gray, the command
  when it is one line, or how many lines it has; its tooltip is the
  whole command. The search box above them filters by name and by
  command, ignoring case.
- A click sends the command to the session, through the same path as
  the command bar's Enter: in the session's encoding, the file panel
  following the shell's folder as it does after Enter, and each line that
  runs kept in the host's command bar history. Each line is followed by a
  carriage return, so a command of several lines runs line by line. With
  "Press Enter after each line" off, the last line is sent without one:
  it waits at the prompt for you to check it and press Enter; the lines
  before it still run. In the session's row, the terminal then has the
  keyboard.
- "Send to", in the toolbar before the search box, chooses where the
  buttons send (while the [command editor](#command-editor) shows, the
  one beside its Send, which sets the same target, takes its place): "Current session" (the default), "All sessions", every
  session that is connected, or "Choose sessions…", a popover with a
  check box for each session, with its status dot and name. While it is
  not the current session, it is in the warning color. The command bar
  does not follow it: it always sends to its own session.
  - Each session gets the command once (a panel row is its session, not
    another one). New tabs and sessions that are not connected are
    skipped; the others still get it.
  - The first send after the target changes to anything but the current
    session asks "Send to N sessions?", listing them, with Send and
    Cancel. Later sends to the same target don't ask again.
  - The target is not saved: every start sends to the current session.
    A session closed drops out of the sessions chosen; with none left,
    the target is the current session again.
  - The terminal of the current session keeps the keyboard after a
    send, and "Put in the command bar" still fills the current session's
    bar only.
- The buttons are dimmed and send nothing while the session is not
  connected, in a new tab, and in a panel row whose session is not
  connected (with another target: while none of its sessions is
  connected); the commands can still be added, edited and arranged.
- Right-click a button for Send, Put in the command bar (a one-line
  command only: it fills the bar without sending it, and gives the bar
  the keyboard), Edit…, Duplicate (right after it, as "name (copy)"),
  Move to group, and Delete, which asks first.
- Drag a button onto another to move it there, onto the empty space
  after the buttons to move it to the end, or onto a group to move it
  into that group.
- "New command" opens the editor in a window of its own: the name, the
  group (the one shown, for a new command), the command, which can have
  several lines, and "Press Enter after each line" (on by default). An
  empty name shows the command's first line instead. Save keeps it; Esc
  or Cancel gives up the changes.
- The Commands tab can open in a tab of its own (see
  [above](#bottom-panel-in-a-tab-of-its-own)), for a board of commands
  as high as the window; its buttons send to its session.

Not supported: commands with parameters to fill in, commands of one host only, syncing the
commands, and a shortcut per command.

### Command editor

FinalShell's command editor: a box beside the buttons to write a few
lines in and send them, without saving them as a quick command first.

- "Command editor", in the toolbar after "New command", shows it or hides
  it, and is pressed while it shows. It is on the right of the buttons,
  the groups keeping the tab's left; the divider between them sets its
  width, from 280 DIPs up to what leaves the buttons 220, and a double
  click puts it back at 460. Where the tab is narrower than 670 DIPs, as
  in the right column, it is under the groups and the buttons, the tab's
  whole width, the divider setting the buttons' share of the height (40%
  by default). Whether it shows and both sizes are saved with the other
  pane sizes (see [Layout and split dividers](#layout-and-split-dividers)).
  While it shows, the toolbar's search box says just "Search", which fits
  the room left to it.
- At the top: "Command editor", "Save as command" and "Clear", which
  empties it (Cmd+Z, Ctrl+Z elsewhere, brings the text back). The text has
  line numbers and a monospaced font, and no highlighting: what is typed
  is what is sent. Enter is a new line, Tab indents, and Esc gives the
  keyboard back to the terminal, as in the command bar.
- At the bottom: "Options", "Send to" and "Send".
  - "Send", or Cmd+Enter (Ctrl+Enter on Linux and Windows) in the text,
    sends the lines the selection touches, each whole, or all of the
    text with none selected, so that a sloppy drag sends no half a
    command. A selection that ends at the start of a line, as a drag to
    the start of the next line does, leaves that line out. The button
    says "Send selection" while there is a selection. The lines are sent
    as a quick command of them is: line by line, each through the
    command bar's path (the session's encoding, the folder follower, the
    host's history), each followed by a carriage return.
    Lines are sent as they are, control characters included, as a quick
    command's are. Empty lines at the end are left out.
  - "Send to" is the same target as the toolbar's (see above): its
    warning color, its question before the first send
    to a new target, and each connected session getting the text once.
  - "Options" has "Press Enter after each line" (on by default; off, the
    last line waits at the prompt, and the terminal takes the keyboard
    for you to press Enter there) and "Clear after sending" (off by
    default), which takes what was sent out of the editor: the lines
    selected, whole, or all of it. Otherwise the text stays, and keeps
    the keyboard. Both are saved in `settings.json`
    (`commandEditorNoEnter`, `commandEditorClear`).
  - "Send" is disabled, and Cmd+Enter sends nothing, while the editor is
    empty or no session of the target is connected, as the buttons are.
- "Save as command" opens the quick command editor on a new command of
  the lines selected, whole as Send takes them, or all of the text, with
  the editor's "Press Enter after each line".
- The text is the same in every session and in a panel row's Commands
  tab, and is kept across restarts: it is saved in `commandeditor.txt` in
  the settings folder (see
  [configuration.md](configuration.md#commandeditortxt)) a second after
  typing stops, and as the app quits. It is not synced. A file that
  cannot be read is said in the editor, and is not saved over.
- There is no limit to the text beyond the file's: the editor lays out
  the lines in view only, and the text is split into lines only to send
  it.

Not supported: syntax highlighting, several editor tabs, variables in
commands, and opening the editor in a window of its own.

## Alerts

In **Options › Alerts**, configure defaults for all SSH hosts or override
them for individual hosts. Rules are off by default. They can alert on
unreachable SSH endpoints during a connection attempt, unexpected SSH
disconnects, CPU usage above a threshold for a continuous number of
minutes, memory or monitored disk usage above a threshold, 1-minute load
relative to the number of online CPU cores, and failed systemd services.
Intentional disconnects do not raise alerts. Resource and service checks
require a connected session with monitoring enabled. The systemd check runs
separately, about once a minute, and is skipped on non-systemd hosts.

An ongoing incident is reported once; even after recovery, a host and rule
will not notify again within 15 minutes. Desktop notifications use the
system notification service. If delivery fails, the app shows an in-app
notice. The bell opens recent alerts with time, host, rule, observed value
and acknowledgment state. Filter, sort or select several to acknowledge
them together. Alerts and their history only run while the app is open.

## Options window

The gear button next to the "Sessions" title at the bottom left opens the
Options window. As in FinalShell, it has a tree of pages on the left and
the page on the right. Every option is saved to `settings.json` as soon as
it changes, and takes effect at once, except for the two Mouse options
noted below and the keepalive interval and SSH agent address, which
apply to connections made after the change.

The terminal appearance pages are described under
[Appearance](#appearance-font-color-scheme-background-image), and the
file icon theme page under [File icon themes](#file-icon-themes).

### General

- Language: System language, English or 简体中文.
- Appearance: System (the default), Light or Dark. System follows the
  system's light or dark appearance, also when it changes while the app
  runs. Light and Dark force it. Every window switches at once, native
  title bars included, and the app starts in the saved appearance. The
  terminal keeps its color scheme in both. Saved as `appearance`.
- Select new tabs automatically (on by default): switch to a new
  connection once it opens. When off, new sessions open in the background
  and the current session stays selected; if no session is selected, the
  new one is still selected.
- Minimize to tray when closed (off by default): the main window's close
  button hides the window to a tray icon (the menu bar on macOS). The
  icon's menu has "Show Finallyshell", "Check for Updates…" and "Quit". On Windows, left-clicking
  the icon also shows the window; on macOS, so does clicking the Dock icon.
  When the system has no tray (Linux without
  `libayatana-appindicator3`), the option behaves as if it were off.
- Confirm before closing the window (on by default): while a session is
  connected or connecting, closing the main window or quitting (the tray
  menu, Cmd+Q on macOS) asks once first, with the system's message box.
  With the tray option also on, the close button hides the window without
  asking, and only "Quit" asks.
- Connections: "Send a keepalive every N seconds", 30 by default, 0 to
  turn keepalives off, up to 3600. See
  [Sessions and tabs](#sessions-and-tabs). Saved as `keepAliveSeconds`.
- Connections: "SSH agent address", the agent that hosts logging in with
  [SSH agent](#ssh-connection) use, with a **Check** button that lists its
  keys. Empty uses the default: `SSH_AUTH_SOCK` on macOS and Linux,
  `\\.\pipe\openssh-ssh-agent` on Windows. A table under it lists common
  addresses: Bitwarden `~/.bitwarden-ssh-agent.sock`, 1Password
  `~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock`,
  Windows `\\.\pipe\openssh-ssh-agent` (Bitwarden's Windows agent uses it
  too). A Unix socket's path may start with `~`. An app opened from the
  macOS Dock does not get variables set in `.zshrc`, so an agent set up
  there must be entered here. Saved as `sshAgent`, for this computer only
  (not synced).
- Updates: the version running, "Check for Updates…", "Check for updates
  automatically" (on by default) and the newer version a check found, or
  why this copy cannot update itself; see [Updates](#updates).

See [Closing and quitting](development.md#closing-and-quitting) for the
full behavior.

### Transfers

- Download folder, with "Change…" and "Use default".
- "Ask where to save each download" (off by default).

In `settings.json` these are `downloadDir` (missing means the system's
Downloads folder; choosing the system's Downloads folder again is also
stored as missing) and `askDownloadDir`. If the download folder has been
deleted, it is created again when downloading.

<a id="terminal-defaults"></a>

### Terminal

The Terminal page holds the global defaults for the character encoding
and the key sequences. Hosts set to "Default" in the connection editor use
them.

- Character encoding: UTF-8 (the default), GBK, GB18030, Big5, Shift_JIS,
  EUC-JP, EUC-KR, ISO-8859-1 or Windows-1252. It has to match the server's
  locale: older Chinese servers use a GBK locale, and with UTF-8 their
  Chinese text is garbled.
  - libghostty-vt decodes UTF-8 only, so the app converts between the SSH
    connection and the terminal (`internal/termenc`, with
    `golang.org/x/text`). The server's output is decoded into UTF-8 before
    it reaches the terminal, and a character split across two packets is
    put back together; bytes that cannot be decoded show as U+FFFD.
    Keyboard input, pastes and the terminal's replies are encoded before
    they are sent to the server; a character the encoding lacks is sent as
    `?` (not x/text's default 0x1A, which is Ctrl+Z in a shell). With UTF-8
    nothing is converted.
  - File names in the file panel and the built-in editor are still handled
    as UTF-8, and the server monitor reads ASCII output only, so neither is
    affected.
  - In `settings.json` this is `termEncoding`, in `hosts.json` `encoding`,
    with the values `utf-8`, `gbk`, `gb18030`, `big5`, `shift_jis`,
    `euc-jp`, `euc-kr`, `iso-8859-1` and `windows-1252`. Unknown values
    mean UTF-8.
- Backspace sends: `^?` (ASCII DEL, the default) or `^H` (Control-H, which
  some old systems and serial consoles need). On connecting, the erase
  character of the server's terminal is set to match. Alt+Backspace sends
  Esc followed by the same character. FinalShell's third choice, "VT220 -
  Delete" (`ESC [3~`), deletes forward in a shell, so it is not offered.
- Delete sends: `ESC [3~` (VT220, the default), `^?` or `^H`. Only Delete
  without modifiers changes; Ctrl+Delete and the like still send the
  plugin's `ESC [3;5~`, which programs read as Delete with modifiers.
- Changes apply the next time a session connects, since the conversion and
  the erase character are set when connecting. The page has "Restore
  defaults". In `settings.json` these are `termBackspace` (`ctrl-h`;
  missing means `^?`) and `termDelete` (`del`, `ctrl-h`; missing means
  `ESC [3~`). Unknown values mean the default.

Under "Local terminal", the "Local terminal program" field is the program
a [local terminal](#local-terminal) runs, with its arguments, for example
`wsl.exe`, `cmd.exe`, `pwsh -NoLogo` or `/opt/homebrew/bin/fish -l`.
Empty, the placeholder "Default shell", runs the user's login shell, or
PowerShell on Windows. The line is split into words as a shell would,
without expanding variables or wildcards: blanks separate words, single
and double quotes keep them together (`sh -c 'echo hi; exec bash'`), and
a backslash escapes the next character, except on Windows, where it
stays itself so that paths need no doubling
(`"C:\Program Files\PowerShell\7\pwsh.exe" -NoLogo`). A quote that is not
closed shows an error in the tab. The program is looked up on `PATH`.
It applies to local terminals opened, or reconnected, after the change.
In `settings.json` it is `localShell`; it is this computer's own and not
synced, as programs and their paths differ between computers.

### Terminal › Mouse

All of these use features of the terminal plugin itself.

- Copy on select (off by default): text selected with the mouse (and with
  Select All) is copied to the clipboard.
- Right click: Show the context menu (the default), Paste, Copy the
  selection, Copy if text is selected, else paste, or Do nothing. When a
  program on the server takes over the mouse (vim with `mouse=a`, for
  example), the right click goes to the program, and this setting applies
  only with Shift held.
- Ctrl+click opens links (Cmd+click on macOS, on by default): web addresses
  and OSC 8 hyperlinks shown in the terminal open in the system's browser.
  When off, clicking a link does nothing. The plugin has no switch to turn
  off link detection, so links (and file paths) are still underlined while
  Ctrl is held; they just do not open.
- Lines per mouse wheel notch (1 to 20, 3 by default): affects the mouse
  wheel only, not trackpads. On Windows, 3 means the "lines to scroll at a
  time" from the system's mouse settings, and other values scale from it.

Copy on select and the scroll lines take effect at once in open terminals
(the plugin has `SetCopyOnSelect` and `SetScrollMultiplier`). The plugin
reads the right click and link options only when a terminal is created and
has no setter for them, so open sessions pick them up after they
reconnect. Recreating a terminal in use would lose its screen and
scrollback, so the app does not do that.

In `settings.json` these are `copyOnSelect`, `rightClick` (`paste`,
`copy`, `copy-or-paste` or `ignore`; a missing or unknown value means the
context menu), `noLinkClick` and `wheelLines` (missing means 3, more than 20
means 20).

There is no middle-click paste. The plugin passes the middle button only
to programs that take over the mouse, does not handle it itself, and does
not expose whether a program has taken over the mouse; if the app handled
the middle button on its own, such a program would receive both the
middle click and the pasted text. The plugin also has no Linux PRIMARY
selection. Supporting it would need the plugin to add a middle-click
option like `RightClick`.

### Terminal › Shortcuts

A table lists each action with its current key and its default key (see
[Keyboard shortcuts](#keyboard-shortcuts)). Select a row and click
"Change…", press the new key combination in the dialog, and click OK to
save it. "Use default" fills in the default key, "Reset" restores the
selected row, and "Reset all" restores every row.

Keys the shell needs cannot be used, and the dialog says why in red:

- keys without modifiers (or with only Shift);
- letters, digits, punctuation, Space, the arrow keys and similar keys with
  only Ctrl (Ctrl+C, Ctrl+R, Ctrl+W, Ctrl+←, ...);
- the same keys with only Alt (Alt+B and Alt+F are Meta in the shell and
  move by words);
- on macOS, the same keys with only Option (they type characters);
- on Windows, Ctrl+Alt with a letter (that is AltGr, which many keyboard
  layouts use to type characters);
- keys the system uses (Cmd+Q, Cmd+H, Cmd+M, Cmd+Tab, Cmd+Space and others
  on macOS; Alt+Tab and Alt+F4 on Windows and Linux; Win key combinations
  on Windows).

Exceptions: "Switch to tab 1–9" may use Alt with the digits (readline's
numeric arguments are rarely used, and GNOME Terminal does the same),
"Terminal: reconnect" may use any key (it only acts while the session is
disconnected and there is no shell), and the panes' actions (split, focus, move)
may use Alt with any key, as Windows Terminal's do, with a warning in
yellow that the shell no longer gets the key while the action takes it.

A key that another action already uses is shown with a warning in yellow;
after OK, that other action has no key (shown as "None"). A key the
terminal itself uses (Ctrl+Shift+A select all, Ctrl+= zoom, Shift+PageUp
scrolling, Ctrl+Insert and Shift+Insert copy and paste) also shows a
warning, and after OK the key belongs to the shortcut.

Window shortcuts take effect at once. The terminal's copy and paste keys
are the terminal plugin's `Keybinds`, which the plugin reads only when a
terminal is created, so a change applies to sessions connected (or
reconnected) afterwards.

On macOS this page also has "Use Option as Alt (Meta)" (off by default,
the plugin's `OptionAsAlt`). When on, Option+B sends Esc b instead of
typing ∫. It also applies to sessions connected afterwards.

In `settings.json`, only changed keys are stored:
`"shortcuts": {"find": "Ctrl+Alt+F", "closeTab": "none"}`. The key names
are `copy`, `paste`, `find`, `fullScreen`, `reconnect`, `previousTab`,
`nextTab`, `closeTab`, `connectionManager`, `newTab`, `switchTab` (whose
value looks like `"Alt+1-9"`), `focusCommandBar`, `switchLeftPanel`,
`toggleBottomPanel`, `splitRight`, `splitDown`, `zoomPane`,
`focusPaneLeft`, `focusPaneRight`, `focusPaneUp`, `focusPaneDown`,
`movePaneLeft`, `movePaneRight`, `movePaneUp` and `movePaneDown`. `Super` is Cmd on macOS, and `none` means no
key. A key that is missing or cannot be read has its default. Option as Alt
is `optionAsAlt`.

### Terminal › Command bar

Under "Show the command bar under the terminal", a "Keys" section lists
the bar's keys: Enter, Tab, Up or the history shortcut, Left and Right in the history
picker, Esc, and the Focus command bar shortcut as it is bound on
Terminal › Shortcuts. The bar itself shows no key help.

The switches of the [command bar](#command-bar), each on by default and
applied at once, to every session:

| Option | Key | |
|---|---|---|
| Show the command bar under the terminal | `noCommandBar` | Off hides the bar in every session: no popups, no history kept or read, no exec channel opened for it. The other switches can't change while it is off. |
| Path picker | `noCommandBarPaths` | Tab on a path, or on a word that isn't the first. |
| History picker | `noCommandBarHistory` | Up, Alt+H (Cmd+Shift+H on macOS) and the History button, which is hidden while it is off. Off also stops keeping the commands sent. |
| Command completion | `noCommandBarCommands` | Tab on the first word; off, Tab there opens the path picker. |
| Read the server's shell history | `noServerHistory` | `~/.bash_history` and `~/.zsh_history` in the history picker. |

The keys are named for turning each part off, so `true` means off.
"Clear history of all hosts…" asks first, then deletes the history the
app keeps of every host (the `history` folder); the servers' own history
files are not changed.

### Network monitor

The page of the [Network tab](#network-tab)'s IP locations and trace
route. The explanations are here, so that the tab stays quiet.

| Option | Key | |
|---|---|---|
| Show IP locations (downloads ip2region data, about 11 MB) | `geoLocations` | `"on"` shows the Location column; turning it on downloads the data when it is not there. `"off"` hides the column and keeps the files; it is also "Don't Ask Again". Missing until the user answers the bar. |
| Include IPv6 addresses (about 37 MB more) | `geoLocationsV6` | The same for the IPv6 data. |
| Look up IP locations online (ip-api.com) | `geoOnline` | `true` looks up online the addresses the data has no city or ISP for. Off by default, and only while locations are shown. |

The page shows how much the downloaded data takes on disk, and "Delete
Downloaded Location Data" asks first, then deletes `geo/` (the data and
the online lookups' cache) and turns locations and online lookups off.
The note under the online switch says that the addresses go to a third
party, and that ip-api.com's free service is unencrypted and for
non-commercial use. A last section says what Trace Route runs on the
server. None of these keys is synced: agreeing to a download, or to
sending the server's peers to a service, is each computer's own.

### Sync

Options › Sync keeps connections and settings the same on several
computers, through storage you already have. Finallyshell runs no server
for it, and it is off until you choose a service:

| Service | What you enter | Free for personal use |
|---|---|---|
| GitHub Gist | A personal access token with the `gist` scope ("Create a token on GitHub" opens the page with the scope chosen). The Gist ID can stay empty: the first sync finds a gist of yours holding `finallyshell-sync.json`, or creates a secret one, and keeps its ID. | Any GitHub account. |
| WebDAV | The folder's URL, a user and a password (an app password where the service has them). A missing folder is created. | Jianguoyun (坚果云), Nextcloud, Synology, Box and others. |
| S3-compatible storage | The endpoint, region (`auto` for Cloudflare R2; `us-east-1` when empty), bucket, file name (`finallyshell-sync.json` when empty), access key ID and secret access key. Requests are path-style and signed with AWS Signature Version 4. | Cloudflare R2 and Backblaze B2 have free tiers; MinIO runs anywhere. |

The service keeps a main file and a version history. Before it leaves the computer, its content is
encrypted with AES-256-GCM, with a key derived by Argon2id (3 passes,
64 MiB, 4 lanes, a random salt) from the **sync password**, which you
choose and enter on each computer. Host names, addresses, passwords and
manual backup notes are inside the ciphertext. Version metadata (time,
device name, app version, item counts and reason) is not encrypted. The
sync password cannot be recovered. With a wrong one, sync says so and
changes nothing.

What is synced:

- Folders and connections, deleted ones included (so a delete reaches the
  other computers), but not each host's last connection time, which keeps
  the recent list of each computer its own.
- Saved quick commands and their groups. The command editor draft and
  command history stay on this device.
- Settings that mean the same on another computer: language, appearance,
  the General, Mouse, Shortcuts and Terminal pages, the font, the color
  scheme, the background's fit and opacity, and the file icon theme when
  it is one every copy has (Material, Seti or minimal). Pane sizes, the
  download folder, the background image itself and icon themes installed
  from a VSIX stay on each computer.
- With "Include saved passwords" checked, off by default, the passwords and
  key passphrases of the hosts that remember theirs. A computer that has it
  off keeps the passwords already in the file as they are. Private key
  files are not synced; hosts keep their key's path.

Each sync merges three ways: this computer's data, the file's, and the
data both had after the last sync, kept in `sync-state.json`, still
encrypted. Each folder, host, setting and password is merged on its own.
What changed on one side only is kept; what was deleted on one side and
not changed on the other is deleted; what changed on both sides keeps this
computer's version, and the status says how many there were. The first
sync with a service (or after the sync password changed) deletes nothing:
it keeps what either side has. When another computer writes the file
during a sync, the sync reads it again and merges again (WebDAV and S3 send
`If-Match`, for the servers that support it; a gist is checked just before
writing). Before a sync applies received changes, it saves a local backup.
Changes made while a sync is under way are merged on top of what it
brought, and synced next; a password saved meanwhile is kept, not
replaced by the one the sync brought. When the service or its fields
change during a sync, what it brought is dropped.

A computer that may hold an old copy asks before it merges: one syncing
with the service for the first time, one whose last sync's file no longer
opens (another computer changed the sync password), and one that has not
synced for 30 days. When its connections differ from the file's (some
only here, or set otherwise), the sync stops before anything changes on
either side, the status says **Choose how to sync**, and **Choose How to
Sync** shows both sides, with how many connections are only here, only
in the file, different and alike, and three choices: **Merge both**
(recommended) keeps every connection of both sides and, where one
differs, the file's version, likely the newer; **Use the cloud's on this
computer** replaces this computer's, backed up first; **Use this
computer's in the cloud** replaces the file's, other computers following
at their next sync, its old content kept in the service's versions.
Automatic sync waits for the choice; **Later** leaves it for now. Such a
computer with nothing of its own merges without asking, keeping the
file's version of what differs.

"Sync Now" syncs at once. "Sync automatically" syncs at start, a minute
after changes here (they are looked for once a minute) and every 5, 15
(the default), 30 or 60 minutes for changes made elsewhere. After an error
it waits for the interval. The status line shows the last sync's time and
what it did, or why it failed.

"Replace the Remote with This Computer's Data…" asks first, then writes
this computer's data to the file without merging, encrypted with the sync
password entered: the way to start over, or to change the sync password
(enter the new one, replace, then enter the new one on the other
computers). If the previous file can still be opened with the current
password, turning off "Include saved passwords" preserves its passwords.

The sync password and the service's token, password or secret key are kept
in the system's credential store (see
[Where passwords are stored](configuration.md#where-passwords-are-stored));
in memory only when the system has none. The other fields are the `sync`
key of `settings.json` (see [Sync](configuration.md#sync)).

Not supported yet: Google Drive, OneDrive or signing in to GitHub in a
browser instead of a token.

#### Backups & versions

Open **Backups & versions** from **Options › Sync** or the connection
manager beside **Show deleted**. The list combines local backups and server
versions, with source, time, device, counts, reason, size and pin status.
The **Sync log** tab shows the latest 100 sync events.

- WebDAV and S3 save encrypted versions before overwriting the main file.
  By default the app keeps all snapshots for 7 days, one per day up to 30
  days, one per week up to 26 weeks and one per month afterward, with a
  target of 200. Pinned snapshots and the newest 10 are exempt. Gist uses
  native GitHub revisions: the app cannot pin, delete or prune them.
- Local backups work with sync off. The app saves them before applying
  received changes, restoring, importing and bulk or folder deletion.
  Unpinned backups expire at 90 days or when more than 50 are kept.
  **Back up now** saves a local snapshot and can also make a server version.
- **Preview** lists added, removed and changed items by name. **Restore**
  saves the current state, then replaces local data with the selected
  version, including folders and their assignments. If sync is on it also
  uploads a new version. If the server update fails, automatic sync stays
  paused. **Add missing items** leaves existing items and known deletions
  alone. **Export JSON** writes plaintext and may include passwords.
  Snapshots made with another password ask for it when opened.
- Sync pauses before removing more than the greater of 10 or 20% of the
  live connections on either side. It also checks permanent removal from
  Deleted and folder subtrees containing more than 10 live connections.
  Review versions, restore from the server copy, or approve the deletion.
  Automatic sync never confirms the deletion for you.

## Account

The bar at the bottom of the left panel is the account's. Signed out, it
reads **Sign in with GitHub to sync** and opens **Sign in with GitHub**,
which uses GitHub's device flow: the window shows the address to open
(`https://github.com/login/device`) and an eight-character code, each
with **Open** and **Copy**, and **Copy Code and Open GitHub** does both.
Enter the code on GitHub and authorize Finallyshell; the window waits,
showing the code's time left (15 minutes), and closes once you have. No
token is typed or pasted, and Finallyshell never sees the GitHub
password. It asks only for gists (for the account's sync) and to read
the email addresses. **Get a New Code** starts again after a declined or
expired code.

Signed in, the bar shows the account (its initial, with a dot: green
synced, blue syncing, orange waiting for you, red failed or the sign-in
expired), its display name (the user name when it has none), GitHub's
mark and how the sync stands (**Synced · 2 minutes
ago**). Clicking it opens the account's card: the name, `@user`, the
primary email address with most of its name hidden (**Show** shows it
whole), **GitHub · Signed in**, the sync's state and settings, **Sync
Now**, **Account & Sync Settings…**, **Activity** and **Sign Out** (which
asks first, and keeps the connections here and the gist on GitHub).

Signing in is all it takes. The account syncs on its own, completely
apart from [Sync](#sync): a secret gist of its own
(`finallyshell-account.json`), its own sync password, state and log; both
can be on, and never run at the same moment. The merge is the same, the
question of [a computer that may hold an old copy](#sync) included.

**Account & Sync Settings…** opens the **Account** window; while an
account is signed in, **Options › Account** shows the same, kept in step
with the window:

- **Sync password.** By default it is the account's GitHub email address,
  so that every computer signing in to the account syncs with nothing to
  type. An email address is no secret, though: with saved passwords
  included, the window says so, as anyone with the gist's link who knows
  the address could read them. **Sync password in use** shows it, hidden
  until **Show**, and **Copy** copies it. **Change Sync Password…**
  encrypts the gist's current data again with a new password (its older
  versions on GitHub keep the old one), keeping the last sync's data as
  the base of the next merge; **Use My GitHub Email** goes back.
- **Computers with different passwords.** The gist's header says which
  kind of password it is sealed with, never the password. A computer
  whose password no longer opens it tries the account's verified email
  addresses, and uses the one that does, as when another computer went
  back to the email address. When none does, it asks: after a password
  of your own set on another computer, it names that computer and when;
  typed once, it is remembered.
- **A changed email address.** A computer that has synced keeps the
  password it has. Signed in, the app reads the profile at start and
  every 6 hours; when the primary address changed and the account syncs
  with its email address, it encrypts the gist again with the new one in
  the background, and logs it. Computers signing in then use the new
  address; others follow by themselves, the new address being among
  those they try.
- **Use the default sync settings** (on): automatic, saved passwords
  included, checking every 15 minutes: changes here go up a minute after
  they are made, and each check brings the others' down, merged item by
  item. Turned off, **Include saved passwords**, **Sync automatically**
  and its interval are the account's own, apart from the Sync page's.
- **Sync Now**, **Replace the Remote with This Computer's Data…**, and
  **Gist on GitHub** and **Versions on GitHub**, which open the gist and
  its revisions.
- **Activity**: the account's log, newest first: each sign-in, sign-out,
  renewal, expired sign-in, password change (yours, one followed from
  another computer, or a new email address), choice, and each sync,
  automatic, manual or chosen, with its result. Click a sync to see its
  steps (renew the sign-in, read the gist, create it on the first sync,
  merge, upload, back up this computer's data, apply the changes), what
  it added, removed and changed here and on the server, the conflicts,
  the computer, and the error. Automatic syncs that find nothing to do
  are one entry, counting them (**12 checks, nothing to do, since
  19:10**), so that they don't crowd out the rest. The entries are grouped
  by day, the latest 30 shown first and more on request, and a filter
  keeps **Changes**, **Problems** (failed or paused) or the **Account**'s
  own events. **Clear** empties it. The Sync page's log does not list the
  account's syncs, nor this one the Sync page's.

GitHub's sign-in lasts 8 hours and renews itself with a refresh token,
which lasts six months and is replaced at each renewal: the app renews it
before a sync when it is about to expire. Another copy of the app may
have renewed it first; the app then uses that one. If GitHub refuses the
token, the app renews or checks it once and tries again. A sign-in that
GitHub no longer accepts (revoked, or not used for six months) shows
**Sign-in expired**; **Sign In Again** with the same account keeps its gist
and settings. Signing in with another account starts that account's own
gist.

## App lock

App lock is off by default. In **Options › Security**, set a password
to require it at startup. Use your current password to change or remove
it. Choose **Off**, **1**, **5**, **15**, **30** or **60 minutes** of
inactivity, and optionally lock whenever the main window is minimized
or hidden to the tray. **Lock now** is available here, in the command
palette and in the tray menu. The shortcut is `Ctrl+Alt+L` on Windows and
Linux and `Command+Option+Shift+L` on macOS.

While locked, the app replaces its window content with the lock screen.
Connections and transfers already in progress keep running. Failed
password attempts delay the next try by 1, 2, 4, 8, 16, then at most 30
seconds. This is a privacy lock for the app, not a replacement for the
operating system's screen lock.

If you forget the password, **Reset lock…** asks you to confirm deleting
the app's remembered credentials from this device. It removes the lock
and quits, ending open connections and transfers and discarding unsaved
edits. Saved connections, private key files and cloud backups are not
changed. Reopen the app after reset to work without the lock.

## Keyboard shortcuts

Defaults, the same as FinalShell's. All of them can be changed on the
Terminal › Shortcuts page.

| Action | macOS | Windows / Linux |
|---|---|---|
| Terminal: copy | Cmd+C | Ctrl+Shift+C |
| Terminal: paste | Cmd+V | Ctrl+Shift+V |
| Terminal: find | Cmd+F | Ctrl+Shift+F |
| Terminal: full screen | Cmd+Enter | Ctrl+Shift+Enter |
| Terminal: reconnect (while the session is disconnected and the terminal area has focus) | Enter | Enter |
| Previous tab | Cmd+[ | Ctrl+Shift+Tab |
| Next tab | Cmd+] | Ctrl+Tab |
| Close tab | Cmd+W | Ctrl+Shift+W |
| Connection manager | Cmd+O | Ctrl+Shift+O |
| New tab | Cmd+T | Ctrl+Shift+T |
| Switch to tab 1–9 | Cmd+1…9 | Alt+1…9 |
| Focus command bar (and back to the terminal) | Cmd+Shift+L | Ctrl+Shift+L |
| Switch sessions / connections (the bottom-left panel's pages) | Cmd+Shift+E | Ctrl+Shift+E |
| Show or hide the bottom panel | Cmd+J | Ctrl+Shift+J |
| Show or hide the left column | Cmd+B | Ctrl+Shift+B |
| Split the pane right | Cmd+D | Alt+Shift+= |
| Split the pane down | Cmd+Shift+D | Alt+Shift+- |
| Zoom the focused pane, or show all panes | Cmd+Shift+Enter | Ctrl+Shift+X |
| Focus the pane on the left / right / above / below | Cmd+Option+arrows | Alt+arrows |
| Switch between terminal and desktop | Cmd+Shift+B | Ctrl+Shift+D |
| Desktop: close window | Cmd+Shift+F4 | Ctrl+Shift+F4 |
| Desktop: minimize / maximize or restore | Cmd+Shift+↓ / ↑ | Ctrl+Shift+↓ / ↑ |
| Desktop: snap left / right | Cmd+Shift+← / → | Ctrl+Shift+← / → |
| Desktop: cycle windows | Cmd+Shift+F7 | Ctrl+Shift+F7 |
| Desktop: show desktop or restore windows | Cmd+Shift+M | Ctrl+Shift+M |
| Desktop: open app launcher | Cmd+Shift+Space | Ctrl+Shift+Space |

Close tab closes the focused pane of a [split](#splits) session first.

Notes:

- Ctrl+Shift+T used to open the connection manager. It now opens a new
  tab, as in other terminal apps; the new tab has the recent connections
  and a "Connection manager" button. The connection manager moved to
  Ctrl+Shift+O.
- On macOS, Close tab is Cmd+W, not FinalShell's Cmd+Q (which quits). So
  that Cmd+W reaches the window, the macOS menu bar drops MyGo's default
  File menu (its Close Window item holds Cmd+W). In the main window, Cmd+W
  closes the tab. In child windows (Options, the connection editor), Cmd+W
  closes the child window, as in other Mac apps (if "Close tab" was changed
  to another key, that key also closes child windows), and Esc closes them
  too. On Windows and Linux, child windows close with Esc and the system's
  close button. The file editor is the exception: Cmd+W (Ctrl+W) closes a
  tab, and Esc does not close the window (see
  [Remote file editor](#remote-file-editor)).
- On macOS, switching tabs uses only Cmd with a digit: Option with a digit
  types a character (¡, ™, £, ...).
- On Windows, the terminal plugin by default also binds Ctrl+C (copy when
  there is a selection) and Ctrl+V (paste when the clipboard has
  something). Finallyshell removes those, leaving them to the shell as on
  Linux (interrupt, and insert the next key literally). Copy and paste with
  Ctrl+Shift+C and Ctrl+Shift+V, or Ctrl+Insert and Shift+Insert.
- Focus command bar is not Ctrl+Shift+K: on Windows that is the
  terminal's clear screen (as in Windows Terminal). FinalShell switches
  with a lone Ctrl press, which would clash with every Ctrl key the shell
  uses, so it isn't copied. With the command bar off, the key goes to the
  terminal.
- Switch sessions / connections is E as in VS Code's Explorer, which
  shows its side bar's tree; no terminal binds Ctrl+Shift+E. On macOS it
  takes Shift too, as Cmd+E is "Use Selection for Find" in Mac apps.
  Older versions of IBus on Linux use Ctrl+Shift+E for emoji input and
  take it before the app; change the key on the Shortcuts page, or in
  IBus's preferences, if it does nothing.
- Show or hide the bottom panel is J as in VS Code. Ctrl+J alone is a
  newline in the shell, so on Windows and Linux it takes Shift too. It
  does nothing in full screen and on a panel opened in a tab of its own.
- The panes' keys are Ghostty's on macOS and Windows Terminal's on
  Windows and Linux (Alt+Shift+= is its Alt+Shift+Plus on a US keyboard).
  Windows Terminal has no key for zoom; Ctrl+Shift+X is Terminator's, and
  no terminal's or text field's (Ctrl+Shift+Z is Redo in text fields).
  Alt with the arrows is also what some shells and editors read (Alt+Left
  moves by a word in fish and in some readline setups), and
  Alt+Shift+- is bash's Alt+_ (insert the last argument, as Alt+. does):
  the focus keys and Zoom are taken only while the session shown is split,
  so a session with one pane keeps them, and the split keys only where a
  pane can split. The Shortcuts page accepts these Alt keys for the
  panes' actions, with a warning, and any of them can be changed there.
- Letters with only Ctrl (Ctrl+A to Ctrl+Z and so on) all belong to the
  shell and are not used as shortcuts on Windows and Linux. In MyGo's
  legacy keyboard mode, Ctrl+[, Ctrl+I and Ctrl+M send nothing; that is an
  upstream issue (#21), and the app does not work around it.

## Layout and split dividers

The dividers between the areas of the window can be dragged to resize them
(or moved with the arrow keys once they have focus), and double-clicking a
divider restores the default size. Every area has a minimum size, so it
cannot be dragged out of sight. The sizes are saved in
`finallyshell/settings.json` and restored at the next start. This includes
the dividers of the Processes and Network tabs, which every session
shares, in the bottom panel and in a panel opened in a tab of its own.

The bottom panel can also collapse to its row of tabs (see
[Window layout](#window-layout)); the divider's position is kept for
when it opens again, and whether it is collapsed is saved with the sizes.

When the bottom panel is on the right, the divider between the terminal
and the column sets the column's width, from 410 DIPs (what the panes
inside the panel need, the Processes details' two halves the widest)
up to what leaves the terminal 320 DIPs; a double click puts it back at
460. The width is saved with the other sizes, and the terminal's
divider under it keeps its position for when the panel goes back to the
bottom.

With the monitor on the right as well as the bottom panel, the divider
between them sets the monitor's share of the column's height, 45% by
default, leaving the monitor its IP line and the panel its tabs and a row
of its table; a double click puts it back at 45%. It is saved with the
other sizes. The column's width is the same whichever of them is in it.

The Commands tab's command editor has a divider of its own, beside the
buttons or under them; it is saved with the other sizes (see
[Command editor](#command-editor)).

## Language

The UI is in English and Simplified Chinese. By default it follows the
system language. Change it on the General page of the Options window
(the gear button next to the "Sessions" title at the bottom left); the
choice is saved.

## Updates

Finallyshell updates itself from the releases of this project,
`geekoutnet/finallyshell`, on GitHub. Its builds are not signed, so it
does not use MyGo's signed updater: the app checks, downloads and installs
updates itself (`updates.go`).

- Checking: at every launch, however recent the last check, so that a
  newer version shows on the update card each time the app starts, and
  once a day while the app runs (24 hours after the last check that
  worked). It asks every download mirror of this
  project at once (`updateMirrors` in `updates.go`) and takes the first
  answer; only when none answers does it ask the GitHub API. "Check for
  updates automatically" in Options › General turns the checks in the
  background off; it is on by default.
- When a newer version is out, a card at the bottom right of the main
  window shows it: the new version, the one running, "What's new" (the
  version's release page), and **Later** and **Update now**. Later hides
  the card for that version for this run; it comes back at the next launch
  or for a newer version.
- Update now downloads the version's setup program from a mirror, checks it
  against the release's `SHA256SUMS`, with the progress on the card, then
  quits, runs the setup program silently and starts the app again. A
  download or check that fails says so on the card, with **Try again**.
- "Check for Updates…" in Options › General checks at once, and says when
  the app is up to date or the check failed. The page also shows the
  version running and the newer one found, and installs it.

Where a copy installs updates itself:

| System | Install | Installs updates |
|---|---|---|
| Windows | The setup program's install (with `Uninstall.exe` beside the app) | Yes |
| Windows | The portable zip | No: the card offers **Download** |
| macOS, Linux | Any | No: the card offers **Download** |
| Any | A development build (`go run`, `mygo dev`) | Does not check |

A copy that cannot install updates still checks and shows the card;
Download opens the version's
[release page](https://github.com/geekoutnet/finallyshell/releases) to
install it by hand.

Notes:

- The macOS app is signed ad hoc and not notarized: the release notes say
  how to open it the first time. The Windows setup program has no
  Authenticode signature, so SmartScreen may warn.
- Whether the app checks in the background and when it last checked are
  kept in `update-check.json` in the config directory; see
  [configuration.md](configuration.md#update-checkjson).
