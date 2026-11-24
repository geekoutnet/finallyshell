package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerResourcesPage struct {
	client   *ssh.Client
	tool     monitor.DockerCmd
	networks dockerNetworksPage
	volumes  dockerVolumesPage
	system   dockerSystemPage
	dialog   *dockerResourceDialog
	lastWork *dockerResourceDialog
	work     *dockerWork
	input    func(context.Context, *ssh.Client, monitor.DockerInput, func(string)) (string, error)
	events   dockerRunner
}

func (a *App) dockerResources(s *Session) *dockerResourcesPage {
	page := &s.docker.resources
	tool := monitor.CmdOf(&s.overview.docker.d)
	if page.client != s.client || page.tool != tool {
		a.stopPoll(&page.system.events.poll)
		if page.dialog != nil && page.dialog.cancel != nil {
			page.dialog.cancel()
		}
		page.closeDialog()
		*page = dockerResourcesPage{client: s.client, tool: tool, input: page.input, events: page.events}
	}
	return page
}

func (a *App) refreshDockerResources(s *Session) {
	page := a.dockerResources(s)
	switch s.docker.section {
	case dockerSecNetworks:
		a.loadDockerNetworks(s, page, true)
	case dockerSecVolumes:
		a.loadDockerVolumes(s, page, true)
		if page.volumes.usage.read {
			a.loadDockerVolumeUsage(s, page, true)
		}
	case dockerSecSystem:
		a.loadDockerSystem(s, page, true)
	}
}

type dockerResourceLoad[T any] struct {
	rows          T
	read, loading bool
	err           error
	request       *dockerResourceRequest
}

type dockerResourceRequest struct{ cancel context.CancelFunc }

func resourceRunner(s *Session) dockerRunner {
	if s.overview.docker.run != nil {
		return s.overview.docker.run
	}
	return monitor.RunDockerProgress
}

func resourceTarget(s *Session) dockerTarget { return dockerTarget{session: s, client: s.client} }

func resourceContext(s *Session) context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func readDockerResource[T any](a *App, s *Session, load *dockerResourceLoad[T], force bool, read func(context.Context, *ssh.Client, dockerRunner) (T, error)) {
	target := resourceTarget(s)
	if !target.available() || (!force && (load.read || load.loading || load.err != nil)) {
		return
	}
	if load.request != nil {
		load.request.cancel()
	}
	run, tool := resourceRunner(s), monitor.CmdOf(&s.overview.docker.d)
	ctx, cancel := context.WithTimeout(resourceContext(s), dockerTimeout)
	request := &dockerResourceRequest{cancel}
	load.loading, load.err, load.request = true, nil, request
	go func() {
		defer cancel()
		rows, err := read(ctx, target.client, run)
		a.post(func() {
			if !slices.Contains(a.sessions, s) || !target.available() || tool != monitor.CmdOf(&s.overview.docker.d) || load.request != request {
				return
			}
			load.loading, load.err, load.request = false, err, nil
			if err == nil {
				load.rows, load.read = rows, true
			}
		})
	}()
}

func resourceOnce[T any](script string, parse func(string) (T, error)) func(context.Context, *ssh.Client, dockerRunner) (T, error) {
	return func(ctx context.Context, client *ssh.Client, run dockerRunner) (T, error) {
		var zero T
		out, err := run(ctx, client, script, nil)
		if err != nil {
			return zero, err
		}
		return parse(out)
	}
}

func resourceInspect[T any](cmd monitor.DockerCmd, kind, list string, parse func(string) ([]T, error)) func(context.Context, *ssh.Client, dockerRunner) ([]T, error) {
	return func(ctx context.Context, client *ssh.Client, run dockerRunner) ([]T, error) {
		out, err := run(ctx, client, list, nil)
		if err != nil {
			return nil, err
		}
		refs, err := monitor.DockerResourceRefs(out, kind == "network")
		if err != nil {
			return nil, err
		}
		var rows []T
		for start := 0; start < len(refs); start += 64 {
			command, err := cmd.ResourceInspect(kind, refs[start:min(start+64, len(refs))])
			if err != nil {
				return nil, err
			}
			out, err := run(ctx, client, command, nil)
			if err != nil {
				return nil, err
			}
			batch, err := parse(out)
			if err != nil {
				return nil, err
			}
			rows = append(rows, batch...)
		}
		return rows, nil
	}
}

type dockerResourceTable struct {
	list     ui.ListState
	selected ui.Selection[string]
	sort     ui.SortOrder
	query    string
}

type dockerResourceRow struct {
	key, name string
	values    []string
	order     map[string]int64
}

func (table *dockerResourceTable) shown(rows []dockerResourceRow, cols []ui.TableColumn) []dockerResourceRow {
	query := strings.ToLower(strings.TrimSpace(table.query))
	shown := slices.DeleteFunc(slices.Clone(rows), func(row dockerResourceRow) bool {
		return query != "" && !strings.Contains(strings.ToLower(row.name+" "+strings.Join(row.values, " ")), query)
	})
	col := slices.IndexFunc(cols, func(col ui.TableColumn) bool { return col.ID == table.sort.Column })
	slices.SortStableFunc(shown, func(left, right dockerResourceRow) int {
		order := 0
		if col >= 0 {
			id := cols[col].ID
			if _, present := left.order[id]; present {
				order = cmp.Compare(left.order[id], right.order[id])
			} else {
				order = strings.Compare(strings.ToLower(left.values[col]), strings.ToLower(right.values[col]))
			}
			if table.sort.Descending {
				order = -order
			}
		}
		if order != 0 {
			return order
		}
		return cmp.Or(strings.Compare(strings.ToLower(left.name), strings.ToLower(right.name)), strings.Compare(left.key, right.key))
	})
	return shown
}

func resourceColumn(id, label string, width float32) ui.TableColumn {
	return ui.TableColumn{ID: id, Title: label, Width: width, MinWidth: 65, Sortable: true}
}

func (table *dockerResourceTable) draw(c *ui.Context, title string, cols []ui.TableColumn, rows []dockerResourceRow, menu func(*ui.Menu, string), details func(string)) {
	table.list.Sort, table.list.KeepOffset = &table.sort, true
	table.list.Key = func(row int) any { return rows[row].key }
	table.list.Label = func(row int) string { return rows[row].name }
	if menu != nil {
		table.list.Selection = &table.selected
		table.list.CellMenu = func(m *ui.Menu, row, col int) {
			if row >= 0 && row < len(rows) {
				menu(m, rows[row].key)
			}
		}
	}
	element := ui.Table(c, &table.list, cols, len(rows), func(row, col int) {
		value := resourceValue(rows[row].values[col])
		ui.Text(c, value).SingleLine().MinWidth(0).Tooltip(value)
	}).Grow(1).MinHeight(0).FillWidth().Label(title)
	if details != nil && element.Submitted() && rows != nil {
		for _, row := range rows {
			if table.selected.Has(row.key) {
				details(row.key)
				break
			}
		}
	}
	if len(rows) == 0 {
		widgets.Muted(c, locales.DockerResNothing()).FontSize(12).Padding(3, 10)
	}
}

func resourceToolbar(c *ui.Context, table *dockerResourceTable, count int, actions func()) {
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		if table != nil {
			ui.SearchField(c, &table.query).MinWidth(92).Grow(1).Placeholder(locales.DockerResFilter()).Label(locales.DockerResFilter())
		}
		actions()
		widgets.Muted(c, locales.DockerResCount(count)).FontSize(11).SingleLine().Shrink(0)
	})
}

func resourceStatus[T any](c *ui.Context, load *dockerResourceLoad[T], retry func()) {
	if load.loading || load.err != nil {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Padding(2, 8).Children(func() {
			if load.loading {
				ui.Spinner(c).Label(locales.DockerReading())
				return
			}
			ui.Text(c, locales.DockerResReadFailed()+" "+load.err.Error()).TextColor(theme.Danger).SingleLine().Grow(1).MinWidth(0).Tooltip(load.err.Error())
			if ui.Button(c, locales.Refresh()).Clicked() {
				retry()
			}
		})
	}
}

func resourceTitle(c *ui.Context, label string) {
	ui.Text(c, label).Bold().Padding(2, 8)
}

func resourceValue(value string) string { return cmp.Or(value, "—") }

func resourcePairText(pairs map[string]string) string {
	var lines []string
	for _, key := range sortedKeys(pairs) {
		lines = append(lines, key+"="+pairs[key])
	}
	return strings.Join(lines, "\n")
}

func resourceCopy(c *ui.Context, menu *ui.Menu, rows ...labelValue) {
	menu.Submenu(locales.Copy(), func(m *ui.Menu) {
		for _, row := range rows {
			if m.Item(row.label).Disabled(row.value == "").Chosen() {
				c.WriteClipboard(row.value)
				c.Toast(locales.CopiedText(row.label))
			}
		}
	})
}

func resourceMenuItem(m *ui.Menu, label, reason string, danger bool) bool {
	item := m.Item(label).Disabled(reason != "").Subtitle(reason)
	if danger {
		item.Danger()
	}
	return item.Chosen() && reason == ""
}

func (page *dockerResourcesPage) blocked(s *Session) string {
	if page.work != nil || s.overview.docker.work != nil {
		return locales.DockerResBusy()
	}
	if !resourceTarget(s).available() || page.tool != monitor.CmdOf(&s.overview.docker.d) || s.overview.docker.d.State != monitor.DockerOK || s.overview.docker.err != nil {
		return locales.DockerResStale()
	}
	return ""
}

func resourceListReason[T any](load *dockerResourceLoad[T]) string {
	if !load.read || load.err != nil || load.loading {
		return locales.DockerResStale()
	}
	return ""
}

type dockerResourceDialog struct {
	title   string
	fields  func(*ui.Context, *dockerResourceDialog, *bool)
	work    *dockerWork
	cancel  context.CancelFunc
	stopped bool
	after   func()
	clear   func()
}

func (page *dockerResourcesPage) show(title string, fields func(*ui.Context, *dockerResourceDialog, *bool)) {
	if page.dialog != nil && page.dialog.clear != nil {
		page.dialog.clear()
	}
	page.dialog = &dockerResourceDialog{title: title, fields: fields}
}

func (page *dockerResourcesPage) closeDialog() {
	if page.dialog != nil && page.dialog.clear != nil {
		page.dialog.clear()
	}
	page.dialog = nil
}

func (page *dockerResourcesPage) drawDialog(c *ui.Context) {
	dialog := page.dialog
	if dialog == nil {
		return
	}
	open := true
	ui.Modal(c.Key(dialog), &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-64, 250), 620)).Gap(8).Children(func() {
			ui.Text(c, dialog.title).FontSize(17).Bold()
			if work := dialog.work; work != nil {
				drawResourceWork(c, dialog, height, &open)
				return
			}
			ui.Scroll(c).MaxHeight(max(height-155, 130)).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Padding(0, 8, 0, 0).Gap(8).Children(func() { dialog.fields(c, dialog, &open) })
			})
		})
	})
	if !open && page.dialog == dialog {
		page.closeDialog()
	}
}

func (page *dockerResourcesPage) outputMenu(m *ui.Menu) {
	if page.lastWork != nil && m.Item(locales.DockerResOpenResult()).Chosen() {
		page.closeDialog()
		page.dialog = page.lastWork
	}
}

func drawResourceWork(c *ui.Context, dialog *dockerResourceDialog, height float32, open *bool) {
	work := dialog.work
	switch {
	case dialog.stopped:
		widgets.Muted(c, locales.DockerResStoppedWaiting())
	case !work.done:
		widgets.Muted(c, locales.DockerActionRunning(dialog.title))
	case work.err != nil:
		ui.Text(c, locales.DockerActionFailed()).Bold().TextColor(theme.Danger)
		ui.Text(c, work.err.Error()).Selectable()
	default:
		widgets.Muted(c, locales.DockerActionDone())
	}
	if work.output != "" {
		dockerOutput(c, work.output, true, min(280, max(height-220, 100)), &work.scroll)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Wrap().Children(func() {
		if work.output != "" && ui.Button(c, locales.CopyAll()).Clicked() {
			c.WriteClipboard(work.output)
		}
		if !work.done && dialog.cancel != nil && !dialog.stopped && ui.Button(c, locales.DockerResStopWaiting()).Clicked() {
			dialog.stopped = true
			dialog.cancel()
		}
		closeButton := ui.PrimaryButton(c, locales.Close())
		if !work.done {
			closeButton.Tooltip(locales.DockerResCloseToCancel())
		}
		if closeButton.Clicked() {
			*open = false
		}
	})
}

func resourceButtons(c *ui.Context, open *bool, label, reason string) bool {
	clicked := false
	ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(func() {
		if ui.Button(c, locales.Cancel()).Clicked() {
			*open = false
		}
		clicked = ui.PrimaryButton(c, label).Disabled(reason != "").Tooltip(reason).Clicked() && reason == ""
	})
	return clicked
}

func resourceText(c *ui.Context, label string, value *string) {
	ui.Column(c.Key(label)).FillWidth().Gap(2).Children(func() {
		ui.Text(c, label).FontSize(12)
		ui.TextInput(c, value).Label(label).FillWidth()
	})
}

func resourceArea(c *ui.Context, label string, value *string, height float32) {
	ui.Column(c.Key(label)).FillWidth().Gap(2).Children(func() {
		ui.Text(c, label).FontSize(12)
		ui.TextArea(c, value).Label(label).Height(height).FillWidth()
	})
}

type dockerResourceStep struct {
	label, script string
	input         *monitor.DockerInput
	private       bool
}

func (a *App) runDockerResource(s *Session, page *dockerResourcesPage, dialog *dockerResourceDialog, steps ...dockerResourceStep) {
	if len(steps) == 0 || page.blocked(s) != "" || !slices.Contains(a.sessions, s) || page.dialog != dialog {
		return
	}
	target := resourceTarget(s)
	work := &dockerWork{target: target, action: dockerRemove}
	page.work, dialog.work, page.lastWork = work, work, dialog
	s.overview.docker.work = work
	ctx, cancel := context.WithTimeout(resourceContext(s), 5*time.Minute)
	dialog.cancel = cancel
	if dialog.clear != nil {
		dialog.clear()
	}
	run, runInput, report := resourceRunner(s), page.input, a.dockerProgress(work)
	if runInput == nil {
		runInput = func(ctx context.Context, client *ssh.Client, input monitor.DockerInput, progress func(string)) (string, error) {
			return input.Run(ctx, client, progress)
		}
	}
	tool := page.tool
	privateFailure := locales.DockerSysLoginHint()
	section := s.docker.section
	go func() {
		defer cancel()
		var output string
		var err error
		var reclaimed uint64
		var haveReclaimed bool
		for _, step := range steps {
			if err = ctx.Err(); err != nil {
				break
			}
			if step.label != "" {
				line := "› " + step.label + "\n"
				output += line
				report(line)
			}
			var text string
			if step.input != nil {
				text, err = runInput(ctx, target.client, *step.input, nil)
				if step.private {
					text = ""
					if err != nil {
						err = dockerPrivateCommandError{message: privateFailure}
					}
				}
			} else {
				text, err = run(ctx, target.client, step.script, report)
			}
			if text != "" {
				if amount, ok := monitor.DockerReclaimed(text); ok {
					reclaimed += amount
					haveReclaimed = true
				}
				output += strings.TrimRight(text, "\n") + "\n"
			}
			if len(output) > 64<<10 {
				output = output[len(output)-(64<<10):]
			}
			if err != nil {
				break
			}
		}
		a.post(func() {
			if s.overview.docker.work == work {
				s.overview.docker.work = nil
			}
			if page.work != work {
				return
			}
			page.work = nil
			work.done, work.err = true, err
			if output != "" {
				work.output = output
			}
			if haveReclaimed {
				work.output += "\n" + locales.DockerSysReclaimedTotal() + ": " + formatSize(int64(reclaimed))
			}
			if !slices.Contains(a.sessions, s) || !target.available() || tool != monitor.CmdOf(&s.overview.docker.d) {
				return
			}
			if err == nil && page.dialog == dialog && dialog.after != nil {
				dialog.after()
				return
			}
			a.refreshResourceAfterAction(s, page, section)
		})
	}()
}

type dockerPrivateCommandError struct{ message string }

func (err dockerPrivateCommandError) Error() string { return err.message }

func (a *App) refreshResourceAfterAction(s *Session, page *dockerResourcesPage, section int) {
	a.readDockerOf(s)
	if page.networks.read || page.networks.loading || section == dockerSecNetworks {
		a.loadDockerNetworks(s, page, true)
	}
	if page.volumes.read || page.volumes.loading || section == dockerSecVolumes {
		a.loadDockerVolumes(s, page, true)
	}
	if page.volumes.usage.read || page.volumes.usage.loading {
		a.loadDockerVolumeUsage(s, page, true)
	}
	systemRefreshed := page.system.disk.read || page.system.disk.loading || section == dockerSecSystem
	if systemRefreshed {
		a.loadDockerSystem(s, page, true)
	}
	if page.system.registries.read && (!systemRefreshed || page.system.mode != dockerSystemRegistries) {
		a.loadDockerRegistries(s, page, true)
	}
}
