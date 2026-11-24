package app

import (
	"cmp"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const keysWindow WindowKind = "ssh-keys"

type keyRow struct {
	sshx.KeyInfo
	path string
	used int
	err  error
}

type keyManager struct {
	dir, query, selected string
	rows                 []keyRow
	row                  int
	sort                 ui.SortOrder
	table                ui.ListState
	loading              bool
	reload               bool
	deleting             bool
	message              string
	messageError         bool
	form                 *keyForm
	delete               *keyRow
	deleteAgain          bool
	details              *keyRow
	deploy               *keysDeployDialog
	backend              keysDeployBackend
	hostID               string
}

func (a *App) keysOptions(c *ui.Context) {
	widgets.Muted(c, locales.KeysOptionsHint())
	if ui.PrimaryButton(c, locales.KeysManage()).AlignSelf(ui.Start).Clicked() {
		a.openKeys(WindowOptions, "")
	}
	ui.Field(c, locales.FwdDisplay(), func() {
		display := a.settings.X11Display
		placeholder := "$DISPLAY"
		if runtime.GOOS == "windows" {
			placeholder = "localhost:0"
		}
		if ui.TextInput(c, &display).Placeholder(placeholder).Width(250).Changed() {
			a.changeSettings(func() { a.settings.X11Display = display })
		}
	}).Description(locales.FwdDisplayHint())
}

func (a *App) forwardingFields(c *ui.Context, ed *connEditor) {
	ui.Fieldset(c, locales.FwdTitle(), func() {
		ui.Field(c, "", func() { ui.Checkbox(c, &ed.forwardAgent, locales.FwdAgent()) }).Description(locales.FwdAgentHint())
		ui.Field(c, locales.FwdX11(), func() {
			values := []string{"", "untrusted", "trusted"}
			names := []string{locales.FwdOff(), locales.FwdUntrusted(), locales.FwdTrusted()}
			current := names[max(slices.Index(values, ed.x11Forward), 0)]
			if ui.Select(c, &current, names).Width(195).Changed() {
				if index := slices.Index(names, current); index >= 0 {
					ed.x11Forward = values[index]
				}
			}
		}).Description(locales.FwdX11Hint())
		if ed.id != "" {
			if ui.Button(c, locales.KeysDeploy()).AlignSelf(ui.Start).Clicked() {
				a.openKeys(WindowEditor, ed.id)
			}
		}
	})
}

func forwardWarning(feature string, err error) string {
	if feature == "agent" {
		return locales.FwdAgentFailed()
	}
	switch {
	case errors.Is(err, sshx.ErrX11Display):
		return locales.FwdDisplayFailed()
	case errors.Is(err, sshx.ErrX11Untrusted):
		return locales.FwdUntrustedFailed()
	default:
		return locales.FwdX11Failed()
	}
}

func (a *App) forwardingStatus(c *ui.Context, session *Session) {
	ui.Column(c.Key("forwarding-status")).FillWidth().Background(colors.note).Padding(5, 10).Gap(3).Children(func() {
		for _, feature := range []string{"agent", "x11"} {
			if err := session.forwardWarnings[feature]; err != nil {
				message := forwardWarning(feature, err)
				ui.Text(c.Key(feature), message).FontSize(12).TextColor(theme.Warning).SingleLine().Tooltip(message)
			}
		}
	})
}

func (a *App) openKeys(parent WindowKind, hostID string) {
	if a.windowOpen(keysWindow) {
		a.plat.FocusWindow(keysWindow)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		a.notify(locales.KeysHomeFailed())
		return
	}
	manager := &keyManager{dir: filepath.Join(home, ".ssh"), hostID: hostID, backend: keysDeployBackend{connect: connectKeysRemote}}
	spec := WindowSpec{Kind: keysWindow, Parent: parent, Title: locales.KeysTitle, Width: 960, Height: 590, MinWidth: 460, MinHeight: 300}
	spec.Closed = func() {
		if manager.deploy != nil && manager.deploy.cancel != nil {
			manager.deploy.cancel()
		}
	}
	a.openWindow(spec, func(c *ui.Context) { a.keysView(c, manager) })
	a.refreshKeys(manager)
}

func keyPathID(path string) string {
	path, _ = filepath.Abs(sshx.ExpandHome(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func (a *App) keyReferences(path string) []*config.Host {
	id := keyPathID(path)
	return slices.DeleteFunc(slices.Clone(a.hosts.Hosts), func(host *config.Host) bool {
		return host.IsRDP() || host.KeyPath == "" || keyPathID(host.KeyPath) != id
	})
}

func listKeys(dir string, refs []string) ([]keyRow, error) {
	paths := make(map[string]string)
	for _, path := range refs {
		path = sshx.ExpandHome(path)
		paths[keyPathID(path)] = path
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		base := strings.TrimSuffix(name, ".pub")
		if !entry.IsDir() && sshx.ValidKeyName(base) && (base != name || strings.HasPrefix(name, "id_") || strings.HasSuffix(strings.ToLower(name), ".pem")) {
			path := filepath.Join(dir, base)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				paths[keyPathID(path)] = path
			}
		}
	}
	rows := make([]keyRow, 0, len(paths))
	for _, path := range paths {
		key, err := sshx.InspectKey(path)
		rows = append(rows, keyRow{KeyInfo: key, path: path, err: err})
	}
	return rows, nil
}

func (a *App) refreshKeys(m *keyManager) {
	if m.loading {
		m.reload = true
		return
	}
	m.loading = true
	refs := make([]string, 0, len(a.hosts.Hosts))
	for _, host := range a.hosts.Hosts {
		if !host.IsRDP() && host.KeyPath != "" {
			refs = append(refs, host.KeyPath)
		}
	}
	go func() {
		rows, err := listKeys(m.dir, refs)
		a.post(func() {
			m.loading, m.rows = false, rows
			if err != nil {
				m.message, m.messageError = locales.KeysReadFailed(), true
			}
			if m.reload {
				m.reload = false
				a.refreshKeys(m)
			}
		})
	}()
}

func (m *keyManager) shown() []keyRow {
	query := strings.ToLower(strings.TrimSpace(m.query))
	rows := slices.DeleteFunc(slices.Clone(m.rows), func(row keyRow) bool {
		return !strings.Contains(strings.ToLower(row.path+" "+row.Comment+" "+row.Fingerprint()+" "+keyAlgorithm(row)), query)
	})
	slices.SortFunc(rows, func(left, right keyRow) int {
		order := 0
		switch m.sort.Column {
		case "type":
			order = cmp.Compare(keyAlgorithm(left), keyAlgorithm(right))
		case "bits":
			order = cmp.Compare(left.Bits(), right.Bits())
		case "hosts":
			order = cmp.Compare(left.used, right.used)
		case "comment":
			order = cmp.Compare(left.Comment, right.Comment)
		case "fingerprint":
			order = cmp.Compare(left.Fingerprint(), right.Fingerprint())
		case "encrypted":
			order = cmp.Compare(keyPassphrase(left), keyPassphrase(right))
		}
		order = cmp.Or(order, cmp.Compare(strings.ToLower(filepath.Base(left.path)), strings.ToLower(filepath.Base(right.path))), cmp.Compare(left.path, right.path))
		if m.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func keyAlgorithm(row keyRow) string {
	if row.Public == nil {
		return "—"
	}
	switch row.Public.Type() {
	case "ssh-ed25519":
		return "Ed25519"
	case "ssh-rsa":
		return "RSA"
	default:
		return row.Public.Type()
	}
}

func keyPassphrase(row keyRow) string {
	if row.err != nil {
		return "—"
	}
	if row.Encrypted {
		return locales.KeysYes()
	}
	return locales.KeysNo()
}

func (a *App) keysView(c *ui.Context, manager *keyManager) {
	if len(manager.rows) != 0 {
		count := make(map[string]int)
		for _, host := range a.hosts.Hosts {
			if !host.IsRDP() && host.KeyPath != "" {
				count[keyPathID(host.KeyPath)]++
			}
		}
		for index := range manager.rows {
			manager.rows[index].used = count[keyPathID(manager.rows[index].path)]
		}
	}
	rows := manager.shown()
	index := slices.IndexFunc(rows, func(row keyRow) bool { return row.path == manager.selected })
	var chosen *keyRow
	if index >= 0 {
		chosen = &rows[index]
	}
	ui.Column(c).Fill().Padding(14).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(7).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.KeysTitle()).FontSize(19).Bold().Grow(1).MinWidth(82)
			if ui.PrimaryButton(c, locales.KeysGenerate()).Clicked() {
				manager.form = &keyForm{kind: "ed25519", name: "id_ed25519", comment: defaultKeyComment()}
			}
			if ui.Button(c, locales.KeysImport()).Clicked() {
				a.pickKey(manager)
			}
			if ui.Button(c, locales.Refresh()).Disabled(manager.loading).Clicked() {
				a.refreshKeys(manager)
			}
		})
		if manager.hostID != "" {
			if host := a.hosts.Host(manager.hostID); host != nil {
				widgets.Muted(c, locales.KeysFromHost(host.Target())).SingleLine().FontSize(12)
			}
		}
		ui.TextInput(c, &manager.query).Placeholder(locales.KeysFilter()).FillWidth()
		a.keysTable(c, manager, rows, index)
		ui.Row(c).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
			a.keyActions(c, manager, chosen)
		})
		if manager.message != "" {
			color := theme.TextMuted
			if manager.messageError {
				color = theme.Danger
			}
			ui.Text(c, manager.message).TextColor(color).FontSize(12).SingleLine().Tooltip(manager.message)
		}
	})
	a.keyFormView(c, manager)
	a.keyDeleteView(c, manager)
	a.keyDetailsView(c, manager)
	a.keysDeployView(c, manager)
}

func (a *App) keysTable(c *ui.Context, m *keyManager, rows []keyRow, index int) {
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.KeysName(), MinWidth: 75, Sortable: true},
		{ID: "type", Title: locales.KeysType(), Width: 75, Sortable: true},
		{ID: "bits", Title: locales.KeysBits(), Width: 56, Sortable: true},
		{ID: "hosts", Title: locales.KeysHosts(), Width: 70, Sortable: true},
		{ID: "encrypted", Title: locales.KeysEncrypted(), Width: 86, Sortable: true},
	}
	width, _ := c.Size()
	if width >= 700 {
		fingerprintWidth := min(450, width*0.45)
		columns = append(columns, ui.TableColumn{ID: "comment", Title: locales.KeysComment(), MinWidth: 95, Sortable: true}, ui.TableColumn{ID: "fingerprint", Title: locales.KeysFingerprint(), Width: fingerprintWidth, MinWidth: 140, Sortable: true})
	}
	m.row = index
	m.table.Selected, m.table.Sort, m.table.KeepOffset = &m.row, &m.sort, true
	m.table.Key = func(index int) any { return rows[index].path }
	m.table.Label = func(index int) string { return filepath.Base(rows[index].path) }
	m.table.CellMenu = func(menu *ui.Menu, index, _ int) {
		row := rows[index]
		m.selected = row.path
		a.keyMenu(c, m, &row, menu)
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		if len(rows) == 0 {
			message := locales.KeysEmpty()
			if m.loading {
				message = locales.KeysLoading()
			}
			widgets.Muted(c, message).FontSize(12).Padding(8, 4)
			return
		}
		table := ui.Table(c, &m.table, columns, len(rows), func(index, column int) {
			row := rows[index]
			values := []string{filepath.Base(row.path), keyAlgorithm(row), strconv.Itoa(row.Bits()), strconv.Itoa(row.used), keyPassphrase(row), row.Comment, row.Fingerprint()}
			label := values[column]
			tip := label
			if column == 0 {
				tip = row.path
			}
			ui.Text(c, label).SingleLine().Tooltip(tip)
		}).Grow(1).MinHeight(0).FillWidth().Label(locales.KeysTitle())
		if m.row >= 0 && m.row < len(rows) {
			m.selected = rows[m.row].path
			if table.Submitted() {
				row := rows[m.row]
				m.details = &row
			}
		}
	})
}

type keyAction struct {
	name, blocked string
	run           func()
}

func (a *App) keyCommands(c *ui.Context, m *keyManager, row *keyRow) []keyAction {
	selected, public, deletion, deployment := "", "", "", ""
	if row == nil {
		selected = locales.KeysSelect()
		public, deletion = selected, selected
	} else {
		if row.Public == nil || row.err != nil {
			public = locales.KeysNoPublic()
		}
		if row.Unverified {
			deployment = locales.KeysUnverifiedPublic()
		}
		if filepath.Clean(keyPathID(filepath.Dir(row.path))) != filepath.Clean(keyPathID(m.dir)) || !sshx.ValidKeyName(filepath.Base(row.path)) || row.err != nil {
			deletion = locales.KeysDeleteBlocked()
		}
	}
	if m.deleting {
		deletion = locales.KeysWorking()
	}
	return []keyAction{
		{locales.KeysDetails(), selected, func() { m.details = row }},
		{locales.KeysCopyPublic(), public, func() { c.WriteClipboard(row.Authorized()) }},
		{locales.KeysDeploy(), cmp.Or(public, deployment), func() { a.beginKeysDeploy(m, row) }},
		{locales.Delete(), deletion, func() { m.delete, m.deleteAgain = row, false }},
	}
}

func (a *App) keyActions(c *ui.Context, m *keyManager, row *keyRow) {
	for _, command := range a.keyCommands(c, m, row) {
		button := ui.Button(c, command.name).Disabled(command.blocked != "")
		if command.blocked != "" {
			button.Tooltip(command.blocked)
		}
		if button.Clicked() && command.blocked == "" {
			command.run()
		}
	}
}

func (a *App) keyMenu(c *ui.Context, m *keyManager, row *keyRow, menu *ui.Menu) {
	for _, command := range a.keyCommands(c, m, row) {
		item := menu.Item(command.name).Disabled(command.blocked != "")
		if command.blocked != "" {
			item.Subtitle(command.blocked)
		}
		if item.Chosen() && command.blocked == "" {
			command.run()
		}
	}
}

func defaultKeyComment() string {
	login := "user"
	if current, err := user.Current(); err == nil {
		login = current.Username
		if _, name, ok := strings.Cut(login, `\`); ok {
			login = name
		}
	}
	computer, _ := os.Hostname()
	comment := login + "@" + cmp.Or(computer, "computer")
	if !sshx.ValidKeyComment(comment) {
		return ""
	}
	return comment
}

type keyForm struct {
	kind, name, comment, pass, again, source string
	busy, confirmed                          bool
	err                                      string
}

func (a *App) pickKey(m *keyManager) {
	title := locales.ChooseKeyFile()
	go func() {
		path, err := a.plat.OpenFile(keysWindow, title, m.dir)
		a.post(func() {
			if err == nil && path != "" {
				name := filepath.Base(path)
				if !sshx.ValidKeyName(name) {
					name = "id_imported"
				}
				m.form = &keyForm{source: path, name: name}
			}
		})
	}()
}

func keyModalWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	return min(max(width-62, 300), 640)
}

func (a *App) keyFormView(c *ui.Context, m *keyManager) {
	form := m.form
	if form == nil {
		return
	}
	open := true
	ui.Modal(c.Key("key-form"), &open, func() {
		_, height := c.Size()
		ui.Scroll(c).Width(keyModalWidth(c)).MaxHeight(max(180, height-64)).Children(func() {
			ui.Column(c).FillWidth().Gap(9).Padding(0, 8, 0, 0).Children(func() {
				title := locales.KeysGenerate()
				if form.source != "" {
					title = locales.KeysImport()
				}
				ui.Text(c, title).FontSize(17).Bold()
				if form.source != "" {
					widgets.Muted(c, form.source).SingleLine().Tooltip(form.source)
				} else {
					name := "Ed25519"
					if form.kind == "rsa" {
						name = "RSA 4096"
					}
					ui.Field(c, locales.KeysType(), func() {
						if ui.Select(c, &name, []string{"Ed25519", "RSA 4096"}).Width(155).Disabled(form.busy || form.confirmed).Changed() {
							old := form.kind
							form.kind = "ed25519"
							if name == "RSA 4096" {
								form.kind = "rsa"
							}
							if form.name == "id_"+old {
								form.name = "id_" + form.kind
							}
						}
					})
					ui.Field(c, locales.KeysComment(), func() { ui.TextInput(c, &form.comment).FillWidth().Disabled(form.busy || form.confirmed) })
				}
				ui.Field(c, locales.KeysName(), func() { ui.TextInput(c, &form.name).FillWidth().Disabled(form.busy || form.confirmed) })
				ui.Field(c, locales.KeysPassphrase(), func() { ui.TextInput(c, &form.pass).Password().FillWidth().Disabled(form.busy || form.confirmed) })
				if form.source == "" {
					ui.Field(c, locales.KeysRepeatPassphrase(), func() { ui.TextInput(c, &form.again).Password().FillWidth().Disabled(form.busy || form.confirmed) })
				}
				if form.confirmed {
					ui.Text(c, locales.KeysOverwrite(form.name)).TextColor(theme.Warning)
				} else {
					widgets.Muted(c, locales.KeysWriteHint()).FontSize(12)
				}
				if form.err != "" {
					ui.Text(c, form.err).TextColor(theme.Danger)
				}
				if form.busy {
					ui.Spinner(c).Size(18, 18).Label(locales.KeysWorking())
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Wrap().Children(func() {
					if ui.Button(c, locales.Cancel()).Disabled(form.busy).Clicked() {
						open = false
					}
					if form.confirmed {
						title = locales.KeysReplace()
					}
					if ui.PrimaryButton(c, title).Disabled(form.busy).Clicked() {
						a.saveKeyForm(m, form)
					}
				})
			})
		})
	})
	if !open && !form.busy {
		form.pass, form.again, m.form = "", "", nil
	}
}

func (a *App) saveKeyForm(m *keyManager, form *keyForm) {
	name := strings.TrimSpace(form.name)
	if !sshx.ValidKeyName(name) || !sshx.ValidKeyComment(form.comment) {
		form.err = locales.KeysInvalidName()
		return
	}
	if form.source == "" && form.pass != form.again {
		form.err = locales.KeysPassMismatch()
		return
	}
	path := filepath.Join(m.dir, name)
	if form.source != "" && keyPathID(form.source) == keyPathID(path) {
		form.err = locales.KeysSameFile()
		return
	}
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		if len(a.keyReferences(path)) > 0 {
			form.err = locales.KeysOverwriteUsed()
			return
		}
	}
	form.err, form.busy = "", true
	copy := *form
	go func() {
		var private, public []byte
		var err error
		if !copy.confirmed {
			for _, file := range []string{path, path + ".pub"} {
				if _, statErr := os.Lstat(file); statErr == nil {
					err = os.ErrExist
					break
				} else if !errors.Is(statErr, os.ErrNotExist) {
					err = statErr
					break
				}
			}
		}
		if err == nil {
			if copy.source == "" {
				private, public, err = sshx.GenerateKey(copy.kind, copy.comment, copy.pass)
			} else {
				private, public, err = sshx.ImportKey(copy.source, copy.pass)
			}
		}
		if err == nil {
			err = sshx.WriteKeyPair(path, private, public, copy.confirmed, time.Now())
		}
		clear(private)
		a.post(func() {
			form.busy = false
			switch {
			case err == nil:
				form.pass, form.again, m.form = "", "", nil
				m.selected, m.query, m.message, m.messageError = path, "", locales.KeysSaved(), false
				a.refreshKeys(m)
			case errors.Is(err, os.ErrExist) && !copy.confirmed:
				form.confirmed = true
			case errors.Is(err, sshx.ErrNeedPassphrase):
				form.confirmed, form.err = false, locales.KeysNeedsPassphrase()
			default:
				form.confirmed, form.err = false, locales.KeysWriteFailed()
			}
		})
	}()
}

func (a *App) keyDeleteView(c *ui.Context, m *keyManager) {
	row := m.delete
	if row == nil {
		return
	}
	open := true
	ui.Modal(c.Key("key-delete"), &open, func() {
		ui.Column(c).Width(keyModalWidth(c)).Gap(10).Children(func() {
			ui.Text(c, locales.KeysDeleteConfirm(filepath.Base(row.path))).Bold().FontSize(16)
			ui.Text(c, locales.KeysDeleteHint())
			references := a.keyReferences(row.path)
			if m.deleteAgain {
				ui.Text(c, locales.KeysDeleteUsed(len(references))).TextColor(theme.Danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Wrap().Children(func() {
				if ui.Button(c, locales.Cancel()).AutoFocus().Clicked() {
					open = false
				}
				if ui.Button(c, locales.Delete()).Clicked() {
					if len(references) > 0 && !m.deleteAgain {
						m.deleteAgain = true
						return
					}
					a.deleteKey(m, *row)
					open = false
				}
			})
		})
	})
	if !open {
		m.delete, m.deleteAgain = nil, false
	}
}

func (a *App) deleteKey(m *keyManager, row keyRow) {
	if m.deleting {
		return
	}
	m.deleting = true
	go func() {
		var err error
		var files []string
		for _, path := range []string{row.path, row.path + ".pub"} {
			info, statErr := os.Lstat(path)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil || !info.Mode().IsRegular() {
				err = os.ErrInvalid
				break
			}
			files = append(files, path)
		}
		if err == nil {
			for _, path := range files {
				err = errors.Join(err, os.Remove(path))
			}
		}
		a.post(func() {
			m.deleting = false
			m.message, m.messageError = locales.KeysDeleted(), err != nil
			if err != nil {
				m.message = locales.KeysDeleteFailed()
			}
			a.refreshKeys(m)
		})
	}()
}

func (a *App) keyDetailsView(c *ui.Context, m *keyManager) {
	row := m.details
	if row == nil {
		return
	}
	open := true
	ui.Modal(c.Key("key-details"), &open, func() {
		_, height := c.Size()
		ui.Scroll(c).Width(keyModalWidth(c)).MaxHeight(max(height-64, 180)).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Padding(0, 8, 0, 0).Children(func() {
				ui.Text(c, filepath.Base(row.path)).FontSize(17).Bold()
				ui.Text(c, row.path).Selectable()
				widgets.Muted(c, locales.KeysUsed(len(a.keyReferences(row.path))))
				if row.err != nil {
					ui.Text(c, locales.KeysReadFailed()).TextColor(theme.Danger)
				} else if row.Public == nil {
					ui.Text(c, locales.KeysNoPublic())
				} else {
					ui.Text(c, keyAlgorithm(*row)+" · "+strconv.Itoa(row.Bits())+" · "+row.Comment)
					ui.Text(c, row.Fingerprint()).Font("monospace").Selectable()
					ui.Text(c, row.RandomArt()).Font("monospace").FontSize(13).Selectable()
				}
				if ui.Button(c, locales.Close()).AlignSelf(ui.End).Clicked() {
					open = false
				}
			})
		})
	})
	if !open {
		m.details = nil
	}
}
