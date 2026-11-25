package app

import (
	"context"
	"path"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type caddyConfigPage struct {
	requested, loading bool
	failure            string
	files              []caddyFileInfo
	tree               ui.OutlineState[string]
	selected           ui.Selection[string]
}

func (a *App) refreshCaddyConfig(s *Session) { a.caddyRefresh(s) }

func (a *App) caddyConfigSection(c *ui.Context, s *Session) {
	if !a.caddyDetected(c, s, false) {
		return
	}
	page, install := &s.caddy.config, s.caddy.state.install
	if !page.requested {
		page.requested, page.loading = true, true
		caddyRead(a, s, caddyBackend.files, func(files []caddyFileInfo, err error) {
			page.loading, page.failure = false, caddyError(err)
			if err == nil {
				page.files = files
				page.tree.Open.Add(install.Config)
				for _, file := range files {
					page.tree.Open.Add("dir:" + path.Dir(file.Path))
				}
			}
		})
	}
	fileReason, formatReason, envReason, adminReason := "", "", "", ""
	if len(page.files) == 0 {
		fileReason = locales.CpanelNoFiles()
	}
	validateReason := fileReason
	if _, err := caddyServiceCommand(install, "validate"); err != nil {
		validateReason = locales.CpanelNoTarget()
	}
	formatReason = fileReason
	if !caddyIsCaddyfile(install) {
		formatReason = locales.CpanelCaddyfileOnly()
	}
	if !caddyValidPath(install.Envfile) {
		envReason = locales.CpanelNoEnv()
	}
	if install.AdminAddr == "off" || install.AdminAddr == "" {
		adminReason = locales.CpanelAdminOff()
	}
	actions := []caddyAction{
		{locales.CpanelEditor(), fileReason, func() { a.caddyOpenConfigEditor(s, page.files, "") }},
		{locales.CpanelFormatAll(), formatReason, func() { a.caddyFormatAll(s) }},
		{locales.CpanelValidate(), validateReason, func() { a.caddyValidateDialog(s) }},
		{locales.CpanelAdapt(), formatReason, func() { a.caddyShowJSON(s, false) }},
		{locales.CpanelRunningConfig(), adminReason, func() { a.caddyShowJSON(s, true) }},
		{locales.CpanelBackups(), "", func() { a.caddyBackups(s) }},
		{locales.CpanelEnvironment(), envReason, func() { a.caddyOpenConfigEditor(s, nil, install.Envfile) }},
	}
	caddyActions(c, actions...)
	if page.loading {
		ui.Spinner(c).Label(locales.CpanelReading()).Padding(6)
	}
	caddyNotice(c, page.failure)
	rows := make(map[string][]string, len(page.files))
	children := map[string][]string{}
	var roots []string
	main := install.Config
	if !slices.ContainsFunc(page.files, func(file caddyFileInfo) bool { return file.Path == main }) {
		main = ""
	}
	for _, file := range caddyOrderFiles(page.files, install.Config) {
		kind := locales.CpanelImport()
		if file.Path == install.Config {
			kind = locales.CpanelMain()
		}
		modified := ""
		if !file.Modified.IsZero() {
			modified = file.Modified.Local().Format("2006-01-02 15:04:05")
		}
		rows[file.Path] = []string{file.Path, kind, formatBytes(float64(len(file.Content))), modified}
		switch {
		case main == "" || file.Path == main:
			roots = append(roots, file.Path)
		case path.Dir(file.Path) == path.Dir(main):
			children[main] = append(children[main], file.Path)
		default:
			directory := "dir:" + path.Dir(file.Path)
			if len(children[directory]) == 0 {
				children[main] = append(children[main], directory)
			}
			children[directory] = append(children[directory], file.Path)
		}
	}
	cols := []ui.TableColumn{
		{ID: "path", Title: locales.CpanelConfigPath(), Width: 270, MinWidth: 130},
		{ID: "kind", Title: locales.CpanelKind(), Width: 105, MinWidth: 80},
		{ID: "size", Title: locales.CpanelSize(), Width: 86},
		{ID: "modified", Title: locales.CpanelModified(), Width: 170},
	}
	page.tree.List.Selection = &page.selected
	page.tree.List.Label = func(index int) string { return strings.TrimPrefix(page.tree.Item(index), "dir:") }
	page.tree.List.CellMenu = func(menu *ui.Menu, row, _ int) {
		if row < 0 || row >= page.tree.Rows() {
			return
		}
		selected := page.tree.Item(row)
		if rows[selected] == nil {
			selected = ""
		}
		caddyMenu(menu, caddyAction{locales.CpanelEditor(), fileReason, func() { a.caddyOpenConfigEditor(s, page.files, selected) }})
		menu.Separator()
		caddyMenu(menu, actions[1:]...)
	}
	table := ui.OutlineTable(c, &page.tree, cols, roots, func(node string) []string { return children[node] }, func(node string, column int) {
		value := ""
		if row := rows[node]; row != nil {
			value = row[column]
		} else if column == 0 {
			value = strings.TrimPrefix(node, "dir:")
		}
		ui.Text(c, value).SingleLine().MinWidth(0).Tooltip(value)
	}).Grow(1).MinHeight(0).FillWidth().Label(locales.CaddyConfig())
	if table.Submitted() {
		for _, file := range page.files {
			if page.selected.Has(file.Path) {
				a.caddyOpenConfigEditor(s, page.files, file.Path)
				break
			}
		}
	}
}

func caddyOrderFiles(files []caddyFileInfo, first string) []caddyFileInfo {
	ordered := slices.Clone(files)
	slices.SortStableFunc(ordered, func(left, right caddyFileInfo) int {
		switch {
		case left.Path == right.Path:
			return 0
		case left.Path == first:
			return -1
		case right.Path == first:
			return 1
		default:
			return strings.Compare(left.Path, right.Path)
		}
	})
	return ordered
}

func caddyIsCaddyfile(install monitor.CaddyInstall) bool {
	return install.Adapter == "caddyfile" || install.Adapter == "" && !strings.EqualFold(path.Ext(install.Config), ".json")
}

func (a *App) caddyOpenConfigEditor(s *Session, files []caddyFileInfo, first string) {
	install := s.caddy.state.install
	backend, run := a.caddyAccess(s)
	spec := CodeEditorSpec{Title: locales.CaddyConfig(), Client: s.client, Session: s, Root: true}
	if len(files) == 0 {
		if !caddyValidPath(first) || first != install.Envfile {
			return
		}
		spec.Title = locales.CpanelEnvironment()
		spec.Files = []CodeEditorFile{{Path: first}}
	} else {
		if first == "" {
			first = install.Config
		}
		for _, file := range caddyOrderFiles(files, first) {
			if caddyValidPath(file.Path) {
				spec.Files = append(spec.Files, CodeEditorFile{Path: file.Path, Content: file.Content, Loaded: true})
			}
		}
		if caddyIsCaddyfile(install) {
			spec.Language = "caddyfile"
			spec.Format = func(ctx context.Context, file, text string) (string, error) {
				return backend.format(ctx, run, install, text)
			}
		} else if install.Adapter == "json" || strings.EqualFold(path.Ext(install.Config), ".json") {
			spec.Language = "json"
		}
	}
	if len(spec.Files) == 0 {
		return
	}
	allowed := map[string]bool{}
	for _, file := range spec.Files {
		allowed[file.Path] = true
	}
	check := func(files []monitor.CaddyFile) error {
		for _, file := range files {
			if !allowed[file.Path] {
				return errCaddyPanelTarget
			}
		}
		return nil
	}
	connection := systemContext(s)
	spec.Validate = func(ctx context.Context, files []monitor.CaddyFile) (string, error) {
		if err := check(files); err != nil {
			return "", err
		}
		if err := connection.Err(); err != nil {
			return "", err
		}
		return backend.validate(ctx, run, install, files)
	}
	spec.Save = func(ctx context.Context, files []monitor.CaddyFile, expect map[string]string) (string, error) {
		if err := check(files); err != nil {
			return "", err
		}
		if err := connection.Err(); err != nil {
			return "", err
		}
		for _, file := range files {
			if _, ok := expect[file.Path]; !ok {
				return "", monitor.ErrCaddyChanged
			}
		}
		for file := range expect {
			if !allowed[file] {
				return "", errCaddyPanelTarget
			}
		}
		result, err := backend.apply(ctx, run, install, monitor.CaddyChange{Writes: files, Expect: expect, Reload: true})
		return caddyApplyOutput(result), err
	}
	spec.Saved = func() { a.refreshCaddyConfig(s) }
	a.openCodeEditor(spec)
}

func (a *App) caddyValidateDialog(s *Session) {
	command, err := caddyServiceCommand(s.caddy.state.install, "validate")
	if err == nil {
		a.caddyConfirm(s, locales.CpanelValidate(), command, locales.CpanelValidateNote(), func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, _ func(string)) (string, error) {
			return backend.validate(ctx, run, install, nil)
		})
	}
}

func caddyReadDialog[T any](a *App, s *Session, title string, read func(caddyBackend, context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (T, error), show func(*systemDialog, T)) {
	dialog := &systemDialog{client: s.client, title: title, busy: true}
	s.caddy.state.dialog = dialog
	dialog.cancel = caddyRead(a, s, read, func(result T, err error) {
		if s.caddy.state.dialog != dialog {
			return
		}
		dialog.busy, dialog.cancel = false, nil
		if err != nil {
			dialog.failure = caddyError(err)
			return
		}
		show(dialog, result)
	})
}

func (a *App) caddyShowJSON(s *Session, running bool) {
	title := locales.CpanelAdapt()
	if running {
		title = locales.CpanelRunningConfig()
	}
	caddyReadDialog(a, s, title, func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (string, error) {
		var text string
		var err error
		if running {
			text, err = backend.admin(ctx, run, install, "GET", "/config/", "")
		} else {
			text, err = backend.adapt(ctx, run, install)
		}
		if err != nil {
			return "", err
		}
		return caddyPrettyJSON(text)
	}, func(dialog *systemDialog, text string) { dialog.body = caddyReadOnlyBody("caddy.json", text) })
}

func (a *App) caddyFormatAll(s *Session) {
	files := slices.Clone(s.caddy.config.files)
	caddyReadDialog(a, s, locales.CpanelFormatAll(), func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyBackupPreview, error) {
		preview := caddyBackupPreview{Change: monitor.CaddyChange{Expect: map[string]string{}, Reload: true}}
		for _, file := range files {
			formatted, err := backend.format(ctx, run, install, file.Content)
			if err != nil {
				return preview, err
			}
			if formatted != file.Content {
				preview.Files = append(preview.Files, file.CaddyFile)
				preview.Change.Writes = append(preview.Change.Writes, monitor.CaddyFile{Path: file.Path, Content: formatted})
				preview.Change.Expect[file.Path] = file.Content
			}
		}
		return preview, nil
	}, func(dialog *systemDialog, preview caddyBackupPreview) { a.caddyReviewChange(s, dialog, preview) })
}

func (a *App) caddyReviewChange(s *Session, dialog *systemDialog, preview caddyBackupPreview) {
	change := preview.Change
	if len(change.Writes)+len(change.Deletes) == 0 {
		dialog.output = locales.CpanelNoChanges()
		return
	}
	old := map[string]string{}
	for _, file := range preview.Files {
		old[file.Path] = file.Content
	}
	valid := func(file string) bool {
		if !caddyValidPath(file) {
			return false
		}
		if previous, exists := old[file]; exists {
			expected, provided := change.Expect[file]
			return provided && expected == previous
		}
		return true
	}
	if slices.ContainsFunc(change.Writes, func(file monitor.CaddyFile) bool { return !valid(file.Path) }) ||
		slices.ContainsFunc(change.Deletes, func(file string) bool { _, exists := old[file]; return !exists || !valid(file) }) {
		dialog.failure = locales.CpanelChanged()
		return
	}
	names := make([]string, 0, len(change.Writes)+len(change.Deletes))
	states := make([]*editor.DiffState, 0, cap(names))
	add := func(file, text string) {
		names = append(names, file)
		states = append(states, &editor.DiffState{Old: editor.NewBuffer(old[file]), New: editor.NewBuffer(text), Layout: editor.DiffUnified, Wrap: true})
	}
	for _, file := range change.Writes {
		add(file.Path, file.Content)
	}
	for _, file := range change.Deletes {
		add(file, "")
	}
	index := 0
	dialog.consequence = locales.CpanelApplyNote()
	dialog.body = func(c *ui.Context, height float32) {
		systemSelect(c, &index, locales.CpanelConfigPath(), names)
		editor.DiffView(c.Key(index), states[index]).Height(min(360, height/2)).FillWidth()
	}
	dialog.confirm = func() {
		a.caddyWork(s, dialog, func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
			result, err := backend.apply(ctx, caddyProgressRunner(run, progress), install, change)
			return caddyApplyOutput(result), err
		})
	}
}

func (a *App) caddyBackups(s *Session) {
	caddyReadDialog(a, s, locales.CpanelBackups(), caddyBackend.backups, func(dialog *systemDialog, backups []caddyBackup) {
		names := make([]string, 0, len(backups))
		for _, backup := range backups {
			if caddyValidPath(backup.Path) && path.Base(path.Dir(backup.Path)) == ".backups" {
				names = append(names, backup.Path)
			}
		}
		if len(names) == 0 {
			dialog.output = locales.CpanelNoBackups()
			return
		}
		index := 0
		dialog.body = func(c *ui.Context, height float32) {
			systemSelect(c, &index, locales.CpanelBackups(), names)
			caddyActions(c, caddyAction{locales.CpanelRestore(), "", func() {
				selected := names[index]
				caddyReadDialog(a, s, locales.CpanelRestore(), func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyBackupPreview, error) {
					return backend.backupPreview(ctx, run, install, selected)
				}, func(dialog *systemDialog, preview caddyBackupPreview) {
					dialog.target = selected
					preview.Change.Reload = true
					a.caddyReviewChange(s, dialog, preview)
				})
			}}, caddyAction{locales.CpanelDeleteBackup(), "", func() {
				selected := names[index]
				a.caddyConfirm(s, locales.CpanelDeleteBackup(), selected, locales.CpanelDeleteBackupNote(), func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, _ func(string)) (string, error) {
					return backend.deleteBackup(ctx, run, install, selected)
				})
			}})
		}
	})
}
