package app

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type relayDialog struct {
	source, target                *Session
	sourceClient, confirmedClient *ssh.Client
	from                          string
	names                         []string
	host                          *config.Host
	canonical                     string
	dir                           string
	folders                       []string
	taken                         map[string]fs.FileInfo
	mode                          int
	policy                        int
	busy, review                  bool
	err                           error
	cancel                        context.CancelFunc
	readFolder                    func(context.Context, *ssh.Client, string, []string, bool) (string, []string, map[string]fs.FileInfo, error)
	run                           func(context.Context, func(relayProgress)) error
}

type relayTarget struct {
	host *config.Host
	sess *Session
}

func (a *App) relayTargets(source *Session) []relayTarget {
	var targets []relayTarget
	seen := map[string]bool{source.Host.ID: true}
	add := func(host *config.Host, sess *Session) {
		if host == nil || host.Kind != config.KindSSH || host.Deleted() || seen[host.ID] {
			return
		}
		seen[host.ID] = true
		targets = append(targets, relayTarget{host, sess})
	}
	for _, candidate := range a.sessions {
		if candidate.status == statusConnected && candidate.client != nil && candidate.sftp != nil && !candidate.isPanelTab() && !candidate.isLocal() {
			add(candidate.Host, candidate)
		}
	}
	for _, host := range a.folderHosts(&a.connTree, "") {
		add(host, nil)
	}
	return targets
}

func relaySendReason(source *Session, from string, names []string) string {
	if source == nil || source.Host == nil || source.Host.Kind != config.KindSSH || source.sftp == nil || source.client == nil || source.status != statusConnected {
		return locales.RelayNeedsSftp()
	}
	if len(names) == 0 {
		return locales.RelaySelectFiles()
	}
	if !relayDirectory(from) || slices.ContainsFunc(names, func(name string) bool { return !relayBasename(name) }) {
		return locales.RelayInvalidPath()
	}
	if from == source.files.cwd && slices.ContainsFunc(source.files.entries, func(entry remotefs.Entry) bool {
		return slices.Contains(names, entry.Name) && (entry.Link || !entry.IsDir() && !entry.Mode.IsRegular())
	}) {
		return locales.RelayUnsupportedType()
	}
	return ""
}

func relaySamePeer(source, target *Session) bool {
	if source.client == target.client {
		return true
	}
	if config.ProxyOf(source.Host) != nil || config.ProxyOf(target.Host) != nil || source.Host.PortOr22() != target.Host.PortOr22() {
		return false
	}
	return source.ip != "" && source.ip == target.ip || strings.EqualFold(source.Host.Addr, target.Host.Addr)
}

func (a *App) relayMenu(menu *ui.Menu, source *Session, from string, names []string) {
	reason := a.relayEntryReason(source, from, names)
	if menu.Item(locales.RelaySend()).Disabled(reason != "").Subtitle(reason).Chosen() && reason == "" {
		a.openRelay(source, from, names)
	}
}

func (a *App) relayEntryReason(source *Session, from string, names []string) string {
	if reason := relaySendReason(source, from, names); reason != "" {
		return reason
	}
	if len(a.relayTargets(source)) == 0 {
		return locales.RelayNoHosts()
	}
	return ""
}

type relayDrag struct {
	source *Session
	from   string
	names  []string
}

func (a *App) relayButton(c *ui.Context, source *Session, from string, names []string) {
	reason := a.relayEntryReason(source, from, names)
	button := widgets.IconButton(c, "arrow-up-right", locales.RelaySend()).Disabled(reason != "")
	if reason != "" {
		button.Tooltip(reason)
		return
	}
	button.Drag(relayDrag{source, from, slices.Clone(names)}).Tooltip(locales.RelayDragHint())
	if button.Clicked() {
		a.openRelay(source, from, names)
	}
}

func (a *App) relayDropZone(row ui.Element, target *Session) {
	if target.Host == nil || target.Host.Kind != config.KindSSH || target.sftp == nil || target.status != statusConnected {
		return
	}
	if payload, ok := ui.DragOver[relayDrag](row); ok && payload.source.Host.ID != target.Host.ID {
		row.Border(1, theme.Accent)
	}
	if payload, ok := ui.Drop[relayDrag](row); ok && payload.source.Host.ID != target.Host.ID && relaySendReason(payload.source, payload.from, payload.names) == "" {
		a.openRelay(payload.source, payload.from, payload.names)
		a.relayChoose(a.relay, relayTarget{target.Host, target})
	}
}

func (a *App) openRelay(source *Session, from string, names []string) {
	if a.relay != nil {
		a.closeRelay(a.relay)
	}
	a.relay = &relayDialog{source: source, sourceClient: source.client, from: from, names: slices.Clone(names)}
	a.redraw()
}

func (a *App) closeRelay(dialog *relayDialog) {
	if dialog.cancel != nil {
		dialog.cancel()
	}
	if a.relay == dialog {
		a.relay = nil
	}
}

func (a *App) relayChoose(dialog *relayDialog, target relayTarget) {
	if dialog.cancel != nil {
		dialog.cancel()
	}
	dialog.host, dialog.target = target.host, target.sess
	dialog.confirmedClient = nil
	dialog.canonical, dialog.dir, dialog.folders, dialog.err = "", "", nil, nil
	dialog.busy, dialog.review, dialog.mode = false, false, int(relayAuto)
	if dialog.target == nil {
		a.relayConnect(dialog)
	}
}

func (a *App) relayConnect(dialog *relayDialog) {
	host := dialog.host
	if a.relay != dialog || host == nil || host.Deleted() {
		return
	}
	if a.readChainSecrets(host, func() { a.relayConnect(dialog) }) {
		dialog.busy = true
		return
	}
	if need := a.missingSecret(host); need != nil {
		dialog.busy = true
		a.askSecret(need, func() { a.relayConnect(dialog) })
		a.pwCancel = func() { dialog.busy, dialog.host = false, nil }
		return
	}
	previous := len(a.sessions)
	a.openSessionIn(nil, host)
	if len(a.sessions) > previous {
		dialog.target = a.sessions[previous]
	}
	dialog.busy = false
}

func (a *App) relayRead(dialog *relayDialog, folder string, review bool) {
	if dialog.busy || dialog.target == nil || dialog.target.client == nil {
		return
	}
	if folder != "" && !relayDirectory(folder) {
		dialog.err = errRelayPath
		return
	}
	selected := dialog.target
	client := selected.client
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	dialog.busy, dialog.err, dialog.cancel = true, nil, cancel
	read := dialog.readFolder
	if read == nil {
		read = relayReadFolder
	}
	go func() {
		defer cancel()
		canonical, folders, taken, err := read(ctx, client, folder, dialog.names, review)
		a.post(func() {
			if a.relay != dialog || dialog.target != selected {
				return
			}
			dialog.busy, dialog.err, dialog.cancel = false, err, nil
			if selected.client != client {
				dialog.err = errConnectionLost
				return
			}
			if err == nil {
				dialog.confirmedClient = client
				dialog.canonical, dialog.dir, dialog.folders = canonical, canonical, folders
				dialog.taken, dialog.review, dialog.policy = taken, review, int(relaySkip)
			}
		})
	}()
}

func relayReadFolder(ctx context.Context, client *ssh.Client, folder string, names []string, review bool) (string, []string, map[string]fs.FileInfo, error) {
	channel, err := relayOpenSFTP(ctx, client)
	if err != nil {
		return "", nil, nil, err
	}
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() { channel.Close() })
	defer stop()
	if folder == "" {
		folder = "."
	}
	canonical, err := channel.RealPath(folder)
	if err != nil || !relayDirectory(canonical) {
		return "", nil, nil, errors.Join(errRelayPath, err)
	}
	entries, err := channel.ReadDirContext(ctx, canonical)
	if err != nil {
		return "", nil, nil, err
	}
	var folders []string
	taken := map[string]fs.FileInfo{}
	for _, entry := range entries {
		if entry.IsDir() && relayBasename(entry.Name()) {
			folders = append(folders, entry.Name())
		}
		if review {
			taken[entry.Name()] = entry
		}
	}
	if review {
		for _, name := range names {
			if !relayBasename(name) {
				return "", nil, nil, errRelayPath
			}
			info, err := channel.Lstat(path.Join(canonical, name))
			if err == nil {
				taken[name] = info
			} else if errors.Is(err, fs.ErrNotExist) {
				delete(taken, name)
			} else {
				return "", nil, nil, err
			}
		}
	}
	slices.Sort(folders)
	return canonical, folders, taken, nil
}

func (a *App) confirmRelay(dialog *relayDialog) {
	if !dialog.review || dialog.target == nil || dialog.target.client != dialog.confirmedClient || dialog.target.client == nil ||
		dialog.target.status != statusConnected || dialog.source.client != dialog.sourceClient || relaySendReason(dialog.source, dialog.from, dialog.names) != "" {
		return
	}
	items := dialog.plan()
	if len(items) == 0 {
		return
	}
	source, target := dialog.source, dialog.target
	job := &relayJob{target: target, sourceName: source.label(), label: target.label(), src: source.client, dst: target.client, runner: dialog.run,
		srcLife: source.ctx, dstLife: target.ctx, task: relayTask{from: dialog.from, to: dialog.canonical, mode: relayMode(dialog.mode),
			items: items, existing: dialog.taken, samePeer: relaySamePeer(source, target),
			endpoint: relayEndpoint{target.Host.Addr, target.client.User(), target.Host.PortOr22()}}}
	if execAllowed(source.Host) && execAllowed(target.Host) && config.ProxyOf(target.Host) == nil {
		job.task.run = func(ctx context.Context, script string) (string, string, error) {
			return monitor.RunScript(ctx, job.src, script)
		}
	}
	a.closeRelay(dialog)
	names := make([]string, len(items))
	for index, item := range items {
		names[index] = item.source
	}
	a.enqueue(&transferJob{sess: source, dir: serverRelay, src: names[0], srcs: names, dstDir: dialog.canonical, relay: job})
	a.openBottomTab(tabTransfers)
}

func (dialog *relayDialog) plan() []relayItem {
	existing := make(map[string]bool, len(dialog.taken))
	for name := range dialog.taken {
		existing[name] = true
	}
	return relayPlan(dialog.names, existing, relayPolicy(dialog.policy))
}

func (a *App) relayView(c *ui.Context) {
	dialog := a.relay
	if dialog == nil || a.pwOpen || a.pendingHostKey() != nil {
		return
	}
	if relaySendReason(dialog.source, dialog.from, dialog.names) != "" || dialog.sourceClient != dialog.source.client {
		a.closeRelay(dialog)
		return
	}
	if dialog.target != nil && dialog.target.sftp != nil && dialog.target.status == statusConnected && dialog.canonical == "" && dialog.err == nil && !dialog.busy {
		a.relayRead(dialog, dialog.target.files.cwd, false)
	}
	open := true
	ui.Modal(c, &open, func() { a.relayForm(c, dialog, &open) })
	if !open {
		a.closeRelay(dialog)
	}
}

func (a *App) relayForm(c *ui.Context, dialog *relayDialog, open *bool) {
	width, height := c.Size()
	ui.Scroll(c).Width(min(410, max(240, width-40))).MaxHeight(max(170, height-46)).Children(func() {
		ui.Column(c).FillWidth().Gap(9).Children(func() {
			ui.Text(c, locales.RelaySend()).FontSize(16).Bold()
			widgets.Muted(c, locales.RelaySource(dialog.source.label(), dialog.from, len(dialog.names))).MaxLines(2)
			if dialog.review {
				a.relayReview(c, dialog, open)
				return
			}
			a.relayDestination(c, dialog)
			if dialog.err != nil {
				ui.Text(c, relayErrorText(dialog.err)).TextColor(theme.Danger).MaxLines(2)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					*open = false
				}
				if ui.PrimaryButton(c, locales.RelayReview()).Disabled(dialog.busy || dialog.target == nil || dialog.target.sftp == nil || dialog.target.status != statusConnected || !relayDirectory(dialog.dir)).Clicked() {
					a.relayRead(dialog, dialog.dir, true)
				}
			})
		})
	})
}

func (a *App) relayDestination(c *ui.Context, dialog *relayDialog) {
	title := locales.RelayChooseHost()
	if dialog.host != nil {
		title = dialog.host.Title()
	}
	ui.MenuButton(c, title, func(menu *ui.Menu) {
		a.relayTargetMenu(menu, dialog)
	}).Label(locales.RelayChooseHost()).FillWidth()
	if dialog.target == nil || dialog.target.status != statusConnected || dialog.target.sftp == nil {
		if dialog.busy || dialog.target != nil && dialog.target.status == statusConnecting {
			ui.Spinner(c).Label(locales.StatusConnecting())
		} else if dialog.target != nil {
			if dialog.target.status == statusConnected {
				if err := dialog.target.files.err; err != nil {
					widgets.Muted(c, fileErrorText(err)).MaxLines(2)
				} else {
					ui.Spinner(c).Label(locales.RelayFolder())
				}
			} else {
				widgets.Muted(c, dialog.target.statusText()).MaxLines(2)
				if smallButton(c, locales.Reconnect()).Clicked() {
					a.reconnect(dialog.target)
				}
			}
		}
		return
	}
	ui.Row(c).Gap(6).Children(func() {
		if ui.TextInput(c, &dialog.dir).Grow(1).MinWidth(0).Label(locales.RelayFolder()).Placeholder(locales.RelayFolder()).Submitted() {
			a.relayRead(dialog, dialog.dir, false)
		}
		if widgets.IconButton(c, "arrow-up", locales.ParentFolder()).Disabled(dialog.busy || dialog.canonical == "").Clicked() {
			a.relayRead(dialog, path.Dir(dialog.canonical), false)
		}
		if widgets.IconButton(c, "refresh-cw", locales.Refresh()).Disabled(dialog.busy).Clicked() {
			a.relayRead(dialog, dialog.dir, false)
		}
	})
	ui.Scroll(c.Key(dialog.canonical)).Height(110).FillWidth().Border(1, theme.Border).Radius(4).Children(func() {
		ui.Column(c).FillWidth().Padding(4).Gap(3).Children(func() {
			if dialog.busy {
				ui.Spinner(c).Label(locales.RelayFolder())
			} else if len(dialog.folders) == 0 {
				widgets.Muted(c, locales.RelayNoSubfolders()).Padding(6)
			}
			for _, folder := range dialog.folders {
				if ui.Button(c.Key(folder), folder).FillWidth().Justify(ui.Start).Tooltip(folder).Disabled(dialog.busy).Clicked() {
					a.relayRead(dialog, path.Join(dialog.canonical, folder), false)
				}
			}
		})
	})
	relaySelect(c, &dialog.mode, locales.RelayMode(), relayModeOptions())
}

func (a *App) relayTargetMenu(menu *ui.Menu, dialog *relayDialog) {
	targets := a.relayTargets(dialog.source)
	if len(targets) == 0 {
		menu.Item(locales.RelayNoHosts()).Disabled(true)
	}
	for _, target := range targets {
		status := locales.RelayConnectHost()
		if target.sess != nil {
			status = locales.StatusConnected()
		}
		if menu.Item(target.host.Title()).Subtitle(status + " · " + target.host.Target()).Disabled(dialog.busy).Chosen() {
			a.relayChoose(dialog, target)
		}
	}
}

func relaySelect(c *ui.Context, picked *int, label string, choices []string) {
	selected := choices[*picked]
	if ui.Select(c, &selected, choices).Label(label).FillWidth().Changed() {
		if index := slices.Index(choices, selected); index >= 0 {
			*picked = index
		}
	}
}

func relayModeOptions() []string {
	return []string{locales.RelayAutomatic(), locales.RelayDirect(), locales.RelayViaApp()}
}

func (a *App) relayReview(c *ui.Context, dialog *relayDialog, open *bool) {
	ui.Text(c, locales.RelayConfirmBody(dialog.target.label(), dialog.canonical)).MaxLines(3)
	if slices.ContainsFunc(dialog.names, func(name string) bool { return dialog.taken[name] != nil }) {
		ui.Text(c, locales.RelayConflicts()).FontSize(12)
		relaySelect(c, &dialog.policy, locales.RelayConflicts(), []string{locales.RelaySkip(), locales.RelayRename(), locales.RelayOverwrite()})
		widgets.Muted(c, locales.RelayBackupNote()).FontSize(12)
	}
	items := dialog.plan()
	overlaps := relaySamePeer(dialog.source, dialog.target) && relayOverlaps(dialog.from, dialog.canonical, items)
	if overlaps {
		ui.Text(c, locales.RelayOverlap()).TextColor(theme.Danger).FontSize(12)
	}
	ui.Scroll(c).Height(86).FillWidth().Border(1, theme.Border).Radius(4).Children(func() {
		ui.Column(c).FillWidth().Padding(6, 9).Children(func() {
			if len(items) == 0 {
				widgets.Muted(c, locales.RelayAllSkipped())
			}
			for _, item := range items {
				name := item.source
				if item.source != item.dest {
					name += " → " + item.dest
				}
				if item.backup {
					name = locales.RelayWillReplace(name)
				}
				ui.Text(c, name).SingleLine().Tooltip(name)
			}
		})
	})
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if ui.Button(c, locales.RelayBack()).Clicked() {
			dialog.review = false
		}
		if ui.Button(c, locales.Cancel()).Clicked() {
			*open = false
		}
		canStart := !overlaps && len(items) > 0 && dialog.target.status == statusConnected && dialog.target.client == dialog.confirmedClient && dialog.source.client == dialog.sourceClient
		if ui.PrimaryButton(c, locales.RelayStart()).Disabled(!canStart).Clicked() && canStart {
			a.confirmRelay(dialog)
		}
	})
}

func relayErrorText(err error) string {
	message := err.Error()
	for _, entry := range []struct {
		err   error
		label func() string
	}{
		{errRelayPath, locales.RelayInvalidPath}, {errRelaySize, locales.RelaySizeMismatch}, {errRelayType, locales.RelayUnsupportedType},
		{errRelayChanged, locales.RelayDestinationChanged}, {errRelayDirect, locales.RelayDirectUnavailable}, {errRelayTar, locales.RelayTarFailed},
		{errRelayOverlap, locales.RelayOverlap},
	} {
		if errors.Is(err, entry.err) {
			message = strings.ReplaceAll(message, entry.err.Error(), entry.label())
		}
	}
	return message
}
