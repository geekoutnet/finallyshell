package app

import (
	"context"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

type caddySectionBackend struct {
	detect   func(context.Context, monitor.CaddyRunner) (monitor.CaddyInstall, error)
	read     func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]monitor.CaddyFile, error)
	apply    func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, monitor.CaddyChange) (monitor.CaddyApplyResult, error)
	validate func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, []monitor.CaddyFile) (string, error)
	hash     func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (string, error)
	format   func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (string, error)
	runner   monitor.CaddyRunner
}

func (backend caddySectionBackend) resolved() caddySectionBackend {
	if backend.detect == nil {
		backend.detect = monitor.DetectCaddy
	}
	if backend.read == nil {
		backend.read = monitor.ReadCaddyFiles
	}
	if backend.apply == nil {
		backend.apply = monitor.ApplyCaddyChange
	}
	if backend.validate == nil {
		backend.validate = monitor.ValidateCaddyConfig
	}
	if backend.hash == nil {
		backend.hash = monitor.HashCaddyPassword
	}
	if backend.format == nil {
		backend.format = monitor.FormatCaddyfile
	}
	return backend
}

type caddySectionFiles struct {
	backend         caddySectionBackend
	client          *ssh.Client
	request         *caddySectionRead
	inst            monitor.CaddyInstall
	files           []monitor.CaddyFile
	loaded, reading bool
	failure         string
}

type caddySectionRead struct {
	cancel context.CancelFunc
}

func (data *caddySectionFiles) main() (monitor.CaddyFile, bool) {
	for _, file := range data.files {
		if file.Path == data.inst.Config {
			return file, true
		}
	}
	return monitor.CaddyFile{}, false
}

func caddySectionPath(file string) bool {
	return path.IsAbs(file) && path.Clean(file) == file && file != "/" && !strings.ContainsAny(file, "\x00\r\n")
}

func (a *App) caddySectionRunner(s *Session) monitor.CaddyRunner {
	if s.client == nil && s.overview.memory.privilegeRunner == nil {
		return func(context.Context, string, io.Reader, func(string)) (string, string, error) {
			return "", "", context.Canceled
		}
	}
	priv := a.sessionPrivilege(s).runner
	return func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		return priv.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input, Progress: progress, ScriptStdin: true, Interrupt: progress != nil})
	}
}

func (a *App) caddySectionOps(s *Session, data *caddySectionFiles) (caddySectionBackend, monitor.CaddyRunner) {
	backend := data.backend.resolved()
	run := backend.runner
	if run == nil {
		run = a.caddySectionRunner(s)
	}
	return backend, run
}

func (a *App) loadCaddySection(s *Session, data *caddySectionFiles, done func()) {
	client := s.client
	if !systemReady(s, client) || data.reading || client == nil && data.backend.detect == nil && data.backend.runner == nil {
		return
	}
	backend, run := a.caddySectionOps(s, data)
	ctx, cancel := context.WithTimeout(systemContext(s), time.Minute)
	request := &caddySectionRead{cancel}
	data.client, data.reading, data.request = client, true, request
	go func() {
		defer cancel()
		inst, err := backend.detect(ctx, run)
		var files []monitor.CaddyFile
		if err == nil && caddySectionPath(inst.Config) && (inst.Method != monitor.CaddyNotInstalled || inst.Binary != "") {
			files, err = backend.read(ctx, run, inst)
			if err == nil {
				files = slices.DeleteFunc(slices.Clone(files), func(file monitor.CaddyFile) bool { return !caddySectionPath(file.Path) })
			}
		}
		a.post(func() {
			if data.request != request {
				return
			}
			data.request, data.reading = nil, false
			if !systemReady(s, client) {
				return
			}
			data.loaded, data.failure = true, systemError("", err)
			if err == nil {
				data.inst, data.files = inst, files
			}
			if done != nil {
				done()
			}
		})
	}()
}

func (a *App) runCaddySectionApply(s *Session, data *caddySectionFiles, change monitor.CaddyChange, done func(monitor.CaddyApplyResult, error), ctx context.Context, progress func(string)) {
	backend, run := a.caddySectionOps(s, data)
	inst := data.inst
	go func() {
		result, err := backend.apply(ctx, caddySectionProgressRunner(run, progress), inst, change)
		a.post(func() { done(result, err) })
	}()
}

func (a *App) caddySectionProgress(dialog *systemDialog, visible func() bool) (func(string), func()) {
	buffer := newSystemChunks(200)
	publish := func(final bool) {
		lines := buffer.take(final)
		if len(lines) == 0 {
			return
		}
		a.post(func() {
			if !visible() {
				return
			}
			for _, line := range lines {
				if marker, ok := strings.CutPrefix(line, "CADDY_TX "); ok {
					switch {
					case strings.HasPrefix(marker, "backup "):
						line = locales.SiteBackup(strings.TrimPrefix(marker, "backup "))
					case marker == "validated":
						line = locales.CsiteValidated()
					case marker == "reloaded":
						line = locales.CsiteReloaded()
					default:
						continue
					}
				}
				dialog.output = systemTail(dialog.output+line+"\n", 65536)
			}
		})
	}
	return func(chunk string) { buffer.add(chunk); publish(false) }, func() { publish(true) }
}

func caddySectionProgressRunner(run monitor.CaddyRunner, progress func(string)) monitor.CaddyRunner {
	return func(ctx context.Context, script string, input io.Reader, backendProgress func(string)) (string, string, error) {
		if progress == nil {
			return run(ctx, script, input, backendProgress)
		}
		return run(ctx, script, input, func(line string) {
			if backendProgress != nil {
				backendProgress(line)
			}
			progress(line)
		})
	}
}

func (a *App) caddySectionEditor(s *Session, data *caddySectionFiles, file monitor.CaddyFile, saved func()) {
	if !systemReady(s, data.client) || caddyConfigWriteReason(data.inst, file.Path) != "" {
		return
	}
	backend, run := a.caddySectionOps(s, data)
	inst := data.inst
	language := "caddyfile"
	if inst.Adapter == "json" {
		language = "json"
	}
	spec := CodeEditorSpec{
		Title: locales.CaddyConfig(), Session: s, Client: s.client, Root: true, Language: language,
		Files: []CodeEditorFile{{Path: file.Path, Content: file.Content, Loaded: true}},
		Validate: func(ctx context.Context, files []monitor.CaddyFile) (string, error) {
			return backend.validate(ctx, run, inst, files)
		},
		Save: func(ctx context.Context, files []monitor.CaddyFile, expect map[string]string) (string, error) {
			result, err := backend.apply(ctx, run, inst, monitor.CaddyChange{Writes: files, Expect: expect, Reload: caddyServiceRunning(inst)})
			return strings.TrimSpace(result.Validate + "\n" + result.Backup), err
		},
		Saved: saved,
	}
	if language == "caddyfile" {
		spec.Format = func(ctx context.Context, _, text string) (string, error) { return backend.format(ctx, run, inst, text) }
	}
	a.openCodeEditor(spec)
}
