package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

var systemLogHistoryMu sync.Mutex

func systemLogHistory(s *Session) string {
	key := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", s.Host.ID, s.Host.Addr, s.Host.PortOr22(), systemUser(s))))
	return filepath.Join(config.Dir(), "log-paths", fmt.Sprintf("%x.json", key))
}

func loadSystemLogHistory(file string) []string {
	info, err := os.Stat(file)
	if err != nil || info.Size() > 64<<10 {
		return nil
	}
	data, err := os.ReadFile(file)
	var paths []string
	if err == nil {
		_ = json.Unmarshal(data, &paths)
	}
	return slices.DeleteFunc(paths, func(path string) bool { return !monitor.ValidLogPath(path) })
}

func (a *App) rememberSystemLog(s *Session, path string) {
	if strings.Contains(path, "[redacted]") || systemRedact(path) != path {
		return
	}
	page := &s.server.logs
	page.recent = append([]string{path}, slices.DeleteFunc(page.recent, func(item string) bool { return item == path })...)
	page.recent = page.recent[:min(len(page.recent), 12)]
	file := systemLogHistory(s)
	go func() {
		systemLogHistoryMu.Lock()
		defer systemLogHistoryMu.Unlock()
		previous := loadSystemLogHistory(file)
		paths := append([]string{path}, slices.DeleteFunc(previous, func(item string) bool { return item == path })...)
		data, _ := json.Marshal(paths[:min(len(paths), 12)])
		dir := filepath.Dir(file)
		if os.MkdirAll(dir, 0700) != nil {
			return
		}
		pending, err := os.CreateTemp(dir, "paths-*")
		if err != nil {
			return
		}
		defer os.Remove(pending.Name())
		_, err = pending.Write(data)
		closeErr := pending.Close()
		if err == nil && closeErr == nil {
			if _, statErr := os.Stat(file); statErr == nil {
				if backupErr := os.Link(file, file+".bak-"+time.Now().Format("20060102150405")); backupErr != nil {
					return
				}
			}
			_ = os.Rename(pending.Name(), file)
		}
	}()
}

func (a *App) logFilePicker(s *Session) {
	page := &s.server.logs
	dialog := &systemDialog{client: s.client, title: locales.LogvCommon(), busy: true}
	page.dialog = dialog
	run := systemReader(s)
	file := systemLogHistory(s)
	ctx, cancel := context.WithTimeout(systemContext(s), 15*time.Second)
	dialog.cancel = cancel
	go func() {
		defer cancel()
		recent := loadSystemLogHistory(file)
		out, stderr, err := run(ctx, monitor.CommonLogFiles, nil, nil)
		common := slices.DeleteFunc(strings.Split(strings.TrimSpace(out), "\n"), func(file string) bool { return !monitor.ValidLogPath(file) })
		a.post(func() {
			if page.dialog != dialog {
				return
			}
			page.common, page.recent, dialog.busy = common, recent, false
			dialog.failure = systemError(stderr, err)
			dialog.body = func(c *ui.Context, height float32) {
				ui.Scroll(c).FillWidth().MaxHeight(height / 2).Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						if ui.Button(c, locales.LogvDocker()).AlignSelf(ui.Start).Clicked() {
							page.dialog = nil
							a.openServiceJournal(s, "docker.service")
						}
						files := append(slices.Clone(page.recent), page.common...)
						seen := map[string]bool{}
						for _, file := range files {
							if seen[file] {
								continue
							}
							seen[file] = true
							if ui.Button(c.Key(file), file).AlignSelf(ui.Start).MaxWidth(580).Tooltip(file).Clicked() {
								page.dialog, page.sourceMode, page.path = nil, 2, file
								a.setSystemLog(s, monitor.JournalSource{File: file, Lines: 300})
							}
						}
					})
				})
			}
		})
	}()
}
