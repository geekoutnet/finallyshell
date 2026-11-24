package app

import (
	"context"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type ctrFiles struct {
	dir, input string
	selected   string
	loaded     bool
	entries    []monitor.CtrFile
	err        string
	cancel     context.CancelFunc
	scroll     ui.ScrollState
}

func ctrReadError(err error, output string) string {
	if output = strings.TrimSpace(output); output == "" {
		return err.Error()
	}
	return output
}

func (a *App) ctrReadFiles(modal *ctrModal, dir string) {
	files, target := modal.files, modal.target
	if files.cancel != nil {
		return
	}
	script, err := monitor.CmdOf(&target.session.overview.docker.d).CtrListScript(target.id, dir)
	files.loaded, files.err = true, ""
	if err != nil {
		files.err = ctrErrorText(err)
		return
	}
	run, ctx, cancel := ctrRunner(target, dockerTimeout)
	files.cancel = cancel
	go func() {
		defer cancel()
		output, err := run(ctx, target.client, script, nil)
		var entries []monitor.CtrFile
		parseFailed := false
		if err == nil {
			entries, err = monitor.ParseCtrFiles(output)
			parseFailed = err != nil
		}
		slices.SortFunc(entries, func(left, right monitor.CtrFile) int {
			if left.Mode[0] == 'd' && right.Mode[0] != 'd' {
				return -1
			}
			if right.Mode[0] == 'd' && left.Mode[0] != 'd' {
				return 1
			}
			return strings.Compare(left.Name, right.Name)
		})
		a.post(func() {
			if a.ctrDlg == nil || a.ctrDlg.ctr != modal || !target.available() {
				return
			}
			files.cancel = nil
			if parseFailed {
				files.err = locales.DockerCtrFileParse()
			} else if err != nil {
				files.err = ctrReadError(err, output)
			} else {
				files.dir, files.input, files.entries = path.Clean(dir), path.Clean(dir), entries
				files.selected, files.scroll.Y = "", 0
			}
		})
	}()
}

func (a *App) ctrFilesView(c *ui.Context, modal *ctrModal, height float32, open *bool) {
	files, target := modal.files, modal.target
	ct := dockerContainer(target.session, target.id)
	running := ctrExecuting(ct)
	if running && !files.loaded {
		a.ctrReadFiles(modal, files.dir)
	}
	ui.Text(c, locales.DockerCtrFiles()+" · "+target.name).FontSize(16).Bold().SingleLine()
	ready := running && files.cancel == nil
	ui.Row(c).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
		if widgets.IconButton(c, "arrow-up", locales.ParentFolder()).Disabled(!ready || files.dir == "/").Clicked() {
			a.ctrReadFiles(modal, path.Dir(files.dir))
		}
		field := ui.TextInput(c, &files.input).Label(locales.DockerCtrPath()).Grow(1).MinWidth(0)
		if ui.Button(c, locales.DockerCtrGo()).Disabled(!ready).Clicked() || ready && field.Submitted() {
			a.ctrReadFiles(modal, files.input)
		}
		if widgets.IconButton(c, "refresh-cw", locales.Refresh()).Disabled(!ready).Clicked() {
			a.ctrReadFiles(modal, files.dir)
		}
	})
	ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).FillWidth().Children(func() {
		busy := target.session.overview.docker.work != nil
		if ui.Button(c, locales.NewFolder()).Disabled(!ready || busy).Clicked() {
			ctrOpenFileTask(modal, monitor.CtrFileMkdir, "")
		}
		if ui.Button(c, locales.DockerCtrCopyFromServer()).Disabled(!ready || busy).Clicked() {
			ctrOpenFileTask(modal, monitor.CtrFileFromServer, "")
		}
		if ui.Button(c, locales.DockerCtrCopyToServer()).Disabled(!ready || busy || files.selected == "").Clicked() {
			ctrOpenFileTask(modal, monitor.CtrFileToServer, files.selected)
		}
		if files.cancel != nil {
			ui.Spinner(c).Label(locales.DockerReading())
		}
	})
	if !running {
		widgets.Muted(c, locales.DockerCtrMustRun())
	} else if files.err != "" {
		ui.Text(c, files.err).TextColor(theme.Danger).MaxLines(3).Selectable()
	}
	ui.Row(c).Gap(8).FillWidth().Padding(3, 14, 3, 6).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ctrFileCells(c, locales.FieldName(), locales.ColSize(), locales.ColPermissions(), locales.ColModified(), true)
		ui.Box(c).Width(ctrMenuW).Shrink(0)
	})
	ui.Scroll(c).Height(min(max(height-270, 115), min(400, max(100, float32(len(files.entries))*27)))).FillWidth().TrackScroll(&files.scroll).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 8, 0, 0).Children(func() {
			if len(files.entries) == 0 && ready && files.err == "" {
				widgets.Muted(c, locales.DockerCtrEmptyFolder()).Padding(6)
			}
			for _, entry := range files.entries {
				a.ctrFileRow(c.Key(entry.Name), modal, entry, ready)
			}
		})
	})
	ctrCloseButton(c, open)
}

func ctrFileCells(c *ui.Context, name, size, mode, modified string, heading bool) {
	if heading {
		widgets.Muted(c, name).FontSize(12).SingleLine().Grow(2).Basis(0).MinWidth(0)
	} else {
		ui.Text(c, name).FontSize(12).SingleLine().Tooltip(name).Grow(2).Basis(0).MinWidth(0)
	}
	widgets.Muted(c, size).FontSize(12).SingleLine().Tooltip(size).Grow(1).Basis(56).MinWidth(0)
	widgets.Muted(c, mode).Font("monospace").FontSize(12).SingleLine().Tooltip(mode).Grow(1).Basis(80).MinWidth(0)
	widgets.Muted(c, modified).FontSize(12).SingleLine().Tooltip(modified).Grow(1).Basis(110).MinWidth(0)
}

func (a *App) ctrFileRow(c *ui.Context, modal *ctrModal, entry monitor.CtrFile, ready bool) {
	files := modal.files
	directory := entry.Mode[0] == 'd'
	menu := func(menu *ui.Menu) {
		busy := !ready || modal.target.session.overview.docker.work != nil
		if directory && menu.Item(locales.OpenFolder()).Disabled(!ready).Chosen() {
			a.ctrReadFiles(modal, path.Join(files.dir, entry.Name))
		}
		for _, op := range []monitor.CtrFileOp{monitor.CtrFileToServer, monitor.CtrFileRename, monitor.CtrFileDelete} {
			if menu.Item(ctrFileTitle(op)).Disabled(busy).Chosen() {
				ctrOpenFileTask(modal, op, entry.Name)
			}
		}
	}
	row := ui.Row(c).Gap(8).FillWidth().Radius(4).AlignItems(ui.Center).Padding(2, 6).MinHeight(27).
		Role(ui.RoleButton).Label(entry.Name).ContextMenu(menu)
	if files.selected == entry.Name {
		row.Background(theme.Accent.Alpha(0.15))
	} else if row.Hovered() {
		row.Background(theme.Text.Alpha(0.06))
	}
	if ready && row.DoubleClicked() && directory {
		a.ctrReadFiles(modal, path.Join(files.dir, entry.Name))
	} else if ready && row.Clicked() {
		files.selected = entry.Name
	}
	modified := entry.Modified
	if seconds, err := strconv.ParseInt(modified, 10, 64); err == nil {
		modified = time.Unix(seconds, 0).Local().Format("2006-01-02 15:04:05")
	}
	row.Children(func() {
		ctrFileCells(c, entry.Name, formatBytes(float64(entry.Size)), entry.Mode, modified, false)
		dockerMoreButton(c, entry.Name, menu)
	})
}

type ctrFileTask struct {
	op               monitor.CtrFileOp
	dir, name, value string
}

func (task *ctrFileTask) copying() bool {
	return task.op == monitor.CtrFileToServer || task.op == monitor.CtrFileFromServer
}

func ctrFileTitle(op monitor.CtrFileOp) string {
	switch op {
	case monitor.CtrFileToServer:
		return locales.DockerCtrCopyToServer()
	case monitor.CtrFileFromServer:
		return locales.DockerCtrCopyFromServer()
	case monitor.CtrFileMkdir:
		return locales.NewFolder()
	case monitor.CtrFileRename:
		return locales.Rename()
	default:
		return locales.Delete()
	}
}

func ctrOpenFileTask(modal *ctrModal, op monitor.CtrFileOp, name string) {
	file := &ctrFileTask{op: op, dir: modal.files.dir, name: name}
	if op == monitor.CtrFileRename {
		file.value = name
	}
	modal.task = &ctrTask{target: modal.target, ids: []string{modal.target.id}, names: []string{modal.target.name}, kind: "files", title: ctrFileTitle(op), file: file}
}

func (task *ctrFileTask) fields(c *ui.Context) string {
	if task.name != "" {
		ui.Text(c, path.Join(task.dir, task.name)).Selectable()
	}
	switch task.op {
	case monitor.CtrFileToServer, monitor.CtrFileFromServer:
		ctrField(c, locales.DockerCtrServerPath(), &task.value, "/tmp/archive", nil)
		return locales.DockerCtrCopyWarning(task.dir)
	case monitor.CtrFileMkdir, monitor.CtrFileRename:
		ctrField(c, locales.FieldName(), &task.value, "", nil)
		return locales.DockerCtrFolderWarning(task.dir)
	default:
		return locales.DockerCtrFileDeleteWarning(path.Join(task.dir, task.name))
	}
}

func (task *ctrFileTask) script(cmd monitor.DockerCmd, id string) (string, error) {
	first, second := path.Join(task.dir, task.name), task.value
	switch task.op {
	case monitor.CtrFileMkdir, monitor.CtrFileRename:
		if task.value == "" || task.value == "." || task.value == ".." || strings.ContainsAny(task.value, "/\x00\r\n") {
			return "", monitor.CtrConfigError("path")
		}
		second = path.Join(task.dir, task.value)
		if task.op == monitor.CtrFileMkdir {
			first = second
		}
	case monitor.CtrFileFromServer:
		first = path.Join(task.dir, path.Base(task.value))
	}
	return cmd.CtrFileScript(task.op, id, first, second)
}
