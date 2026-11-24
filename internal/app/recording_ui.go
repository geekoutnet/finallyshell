package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) recordingSettings() config.TerminalRecording {
	if a.settings.Recording != nil {
		return *a.settings.Recording
	}
	return config.TerminalRecording{}
}

func (a *App) autoRecord(s *Session, p *pane) {
	if a.recordingSettings().Automatic {
		a.setPaneRecording(s, p, false)
	}
}

func (a *App) setPaneRecording(s *Session, p *pane, manual bool) {
	if p == nil || p.term == nil || s.status != statusConnected && manual {
		return
	}
	stream := p.stream.Load()
	if stream == nil || stream.isClosed() || stream.asking {
		return
	}
	if stream.recording() {
		if manual {
			stream.stopRecording()
			a.redraw()
		}
		return
	}
	width, height := p.term.Size()
	title, hostID := s.label(), "local"
	if s.Host != nil {
		title, hostID = s.Host.Title(), s.Host.ID
	}
	cfg := a.recordingSettings()
	dir := filepath.Join(recordingDir(cfg), recordingHost(title, hostID))
	limit := cfg.LimitMiB
	if limit == 0 {
		limit = recordingDefaultMiB
	}
	limit = min(max(limit, 1), 512)
	stream.asking = true
	go func() {
		if manual && !a.plat.Confirm(Confirmation{
			Title: locales.RecOptions(), Message: locales.RecConfirm(title), Detail: locales.RecConfirmDetail(dir),
			OK: locales.RecStart(), Cancel: locales.Cancel(),
		}) {
			a.post(func() { stream.asking = false })
			return
		}
		if stream.isClosed() {
			return
		}
		rec, err := newRecording(dir, title, max(width, 1), max(height, 1), limit<<20, cfg.Text, time.Now())
		if err == nil {
			stream.setRecording(rec)
		}
		a.post(func() {
			stream.asking = false
			if err != nil && !stream.isClosed() {
				a.notify(locales.RecFailed())
			}
		})
	}()
}

func (a *App) recordingMenu(m *ui.Menu, s *Session) {
	p := s.pane()
	var stream *terminalStream
	if p != nil {
		stream = p.stream.Load()
	}
	label, reason := locales.RecStart(), ""
	if s.isLocal() {
		reason = locales.RecSSHOnly()
	} else if s.status != statusConnected || p == nil || p.term == nil || stream == nil || stream.isClosed() {
		reason = locales.NotConnected()
	} else if stream.asking {
		reason = locales.RecPreparing()
	} else if stream.recording() {
		label = locales.RecStop()
	}
	if m.Item(label).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.setPaneRecording(s, p, true)
	}
	if m.Item(locales.RecOpenFolder()).Chosen() {
		a.openRecordingFolder()
	}
}

func (a *App) recordingBadge(c *ui.Context, s *Session, p *pane) {
	if p == nil || p.term == nil {
		return
	}
	stream := p.stream.Load()
	if s.status != statusConnected || stream == nil || stream.isClosed() || p.find.open {
		return
	}
	active := stream.recording()
	label, tint := locales.RecStart(), c.Theme().TextMuted
	if active {
		label, tint = locales.RecStop(), c.Theme().Danger
	}
	if p == s.pane() || active {
		ui.Row(c.Key("recording-badge")).Absolute().Top(5).Right(7).ZIndex(1).Children(func() {
			button := ui.Box(c.Key(label)).Size(23, 23).Role(ui.RoleButton).Label(label).Tooltip(label).
				Background(c.Theme().Surface).Radius(5).Disabled(stream.asking)
			button.DrawOver(func(painter *ui.Painter, bounds ui.Rect) {
				dot := ui.Rect{X: bounds.X + (bounds.W-8)/2, Y: bounds.Y + (bounds.H-8)/2, W: 8, H: 8}
				if active {
					painter.Fill(dot, tint, 4)
				} else {
					painter.Stroke(dot, tint, 4, 1.5)
				}
			})
			if button.Clicked() {
				a.setPaneRecording(s, p, true)
			}
		})
	}
}

func (a *App) openRecordingFolder() {
	dir := recordingDir(a.recordingSettings())
	go func() {
		if err := os.MkdirAll(dir, 0o700); err == nil && a.plat.OpenPath(dir) == nil {
			return
		}
		a.post(func() { a.notify(locales.RecFailed()) })
	}()
}

func (a *App) recordingPage(c *ui.Context) {
	cfg := a.recordingSettings()
	widgets.Muted(c, locales.RecPrivacy()).FontSize(12)
	automatic := cfg.Automatic
	if ui.Checkbox(c, &automatic, locales.RecAutomatic()).Changed() {
		if automatic {
			go func() {
				if a.plat.Confirm(Confirmation{
					Title: locales.RecOptions(), Message: locales.RecAutomatic(), Detail: locales.RecPrivacy(),
					OK: locales.RecEnable(), Cancel: locales.Cancel(),
				}) {
					a.post(func() {
						latest := a.recordingSettings()
						latest.Automatic = true
						a.saveRecordingSettings(latest)
					})
				}
			}()
		} else {
			cfg.Automatic = false
			a.saveRecordingSettings(cfg)
		}
	}
	if ui.Checkbox(c, &cfg.Text, locales.RecPlain()).Changed() {
		a.saveRecordingSettings(cfg)
	}
	ui.Field(c, locales.RecLimit(), func() {
		limit := float64(cfg.LimitMiB)
		if limit == 0 {
			limit = recordingDefaultMiB
		}
		if ui.NumberInput(c, &limit, 1, 512, 1).Label(locales.RecLimit()).Width(100).Changed() {
			cfg.LimitMiB = int(limit)
			a.saveRecordingSettings(cfg)
		}
	}).Description(locales.RecRotation())
	ui.Text(c, locales.RecFolder()).Bold()
	ui.Text(c, recordingDir(cfg)).SingleLine().Tooltip(recordingDir(cfg)).MinWidth(0)
	ui.Row(c).Wrap().Gap(8).Children(func() {
		if ui.Button(c, locales.ChooseFolder()).Clicked() {
			go func() {
				if dir, err := a.plat.ChooseFolder(WindowOptions, locales.RecFolder()); err == nil && dir != "" {
					a.post(func() {
						latest := a.recordingSettings()
						latest.Folder = filepath.Clean(dir)
						a.saveRecordingSettings(latest)
					})
				}
			}()
		}
		if ui.Button(c, locales.DownloadFolderDefault()).Disabled(cfg.Folder == "").Clicked() {
			cfg.Folder = ""
			a.saveRecordingSettings(cfg)
		}
		if ui.Button(c, locales.RecOpenFolder()).Clicked() {
			a.openRecordingFolder()
		}
	})
}

func (a *App) saveRecordingSettings(cfg config.TerminalRecording) {
	settings := a.settings
	settings.Recording = &cfg
	if cfg == (config.TerminalRecording{}) {
		settings.Recording = nil
	}
	a.saveTerminalStreamSettings(settings)
}

func (a *App) saveTerminalStreamSettings(settings config.Settings) bool {
	if err := backupTerminalSettings(filepath.Join(config.Dir(), "settings.json"), time.Now()); err != nil || a.saveSettings(settings) != nil {
		a.notify(locales.RecSettingsFailed())
		return false
	}
	a.settings = settings
	a.redraw()
	return true
}

func backupTerminalSettings(path string, now time.Time) error {
	source, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer source.Close()
	for range 10000 {
		backup := path + ".bak-" + now.Format("20060102150405")
		dest, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			now = now.Add(time.Second)
			continue
		}
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dest, source)
		if err := errors.Join(copyErr, dest.Close()); err != nil {
			os.Remove(backup)
			return err
		}
		return nil
	}
	return os.ErrExist
}
