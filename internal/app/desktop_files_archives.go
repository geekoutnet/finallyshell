package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"

	"finallyshell/locales"

	"github.com/pkg/sftp"
)

func desktopFilesStage(sc *sftp.Client, dir string, work func(string) error) (err error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	stage := path.Join(dir, fmt.Sprintf(".finallyshell-explorer-%x", token))
	if err := sc.Mkdir(stage); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, relayRemoveTree(context.Background(), sc, stage)) }()
	if err := sc.Chmod(stage, 0o700); err != nil {
		return err
	}
	return work(stage)
}

func desktopFilesAbsent(sc *sftp.Client, target string) error {
	if _, err := sc.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New(locales.DesktopFilesTaken(target))
	}
	return nil
}

func desktopFilesPublish(sc *sftp.Client, staged, target string) error {
	if err := desktopFilesAbsent(sc, target); err != nil {
		return err
	}
	return sc.Rename(staged, target)
}

func (a *App) desktopFilesFetch(w *desktopWindow) {
	f, s := w.explorer(), w.session
	dir, sc, run := f.dir, s.sftp, desktopFilesRunner(s, f)
	if sc == nil || run == nil || !desktopFilesPath(dir) || f.job != nil {
		return
	}
	title := locales.DesktopFilesRemoteDownload()
	f.modal = &desktopFilesPrompt{title: title, text: dir, field: locales.DesktopFilesURL(), secondLabel: locales.ColName(), action: locales.Download(),
		valid: func(p *desktopFilesPrompt) bool {
			return desktopFilesURL(strings.TrimSpace(p.value)) && desktopFilesName(desktopFilesFetchName(p.value, p.second))
		}, accept: func(p *desktopFilesPrompt) {
			if s.sftp != sc {
				return
			}
			raw, name := strings.TrimSpace(p.value), desktopFilesFetchName(p.value, p.second)
			target := path.Join(dir, name)
			a.desktopFilesWork(w, title+": "+target, true, []string{dir}, func(ctx context.Context, report func(string)) error {
				if err := desktopFilesAbsent(sc, target); err != nil {
					return err
				}
				return desktopFilesStage(sc, dir, func(stage string) error {
					staged := path.Join(stage, name)
					if err := desktopFilesRun(ctx, run, desktopFilesFetchCommand(raw, staged), func(chunk string) {
						for line := range strings.SplitSeq(strings.ReplaceAll(chunk, "\r", "\n"), "\n") {
							if before, _, found := strings.Cut(line, "%"); found {
								fields := strings.Fields(before)
								if len(fields) > 0 && strings.Trim(fields[len(fields)-1], "0123456789.") == "" {
									report(fields[len(fields)-1] + "%")
								}
							}
						}
					}); err != nil {
						if strings.Contains(err.Error(), raw) {
							return errors.New(strings.ReplaceAll(err.Error(), raw, "…"))
						}
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					return desktopFilesPublish(sc, staged, target)
				})
			}, nil)
		},
	}
}

func desktopFilesFetchName(raw, name string) string {
	if name != "" {
		return name
	}
	if parsed, err := url.ParseRequestURI(strings.TrimSpace(raw)); err == nil && !strings.HasSuffix(parsed.Path, "/") && parsed.Path != "" {
		return path.Base(parsed.Path)
	}
	return ""
}

func desktopFilesArchiveItems(items []desktopFilesItem) (string, []string, bool) {
	if len(items) == 0 {
		return "", nil, false
	}
	dir := path.Dir(items[0].target)
	names := make([]string, 0, len(items))
	for _, item := range items {
		if !desktopFilesPath(item.target) || item.target == "/" || path.Dir(item.target) != dir || !desktopFilesName(item.Name) {
			return "", nil, false
		}
		names = append(names, item.Name)
	}
	return dir, names, true
}

func (a *App) desktopFilesCompress(w *desktopWindow, items []desktopFilesItem, kind string) {
	f, s := w.explorer(), w.session
	dir, names, ok := desktopFilesArchiveItems(items)
	sc, run := s.sftp, desktopFilesRunner(s, f)
	if !ok || sc == nil || run == nil || f.job != nil {
		return
	}
	title := locales.DesktopFilesCompress()
	f.modal = &desktopFilesPrompt{title: title, text: dir + "\n" + strings.Join(names, "\n"), field: locales.ColName(), value: names[0] + kind, action: title,
		valid: func(p *desktopFilesPrompt) bool { return desktopFilesName(p.value) && !slices.Contains(names, p.value) },
		accept: func(p *desktopFilesPrompt) {
			if s.sftp != sc {
				return
			}
			name := p.value
			if !strings.HasSuffix(strings.ToLower(name), kind) {
				name += kind
			}
			target := path.Join(dir, name)
			a.desktopFilesWork(w, title+": "+target, true, []string{dir}, func(ctx context.Context, report func(string)) error {
				if err := desktopFilesAbsent(sc, target); err != nil {
					return err
				}
				return desktopFilesStage(sc, dir, func(stage string) error {
					staged := path.Join(stage, name)
					if err := desktopFilesRun(ctx, run, desktopFilesCompressCommand(kind, dir, staged, names), report); err != nil {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					return desktopFilesPublish(sc, staged, target)
				})
			}, nil)
		},
	}
}

func (a *App) desktopFilesAskExtract(w *desktopWindow, item desktopFilesItem, folder bool) {
	f, s := w.explorer(), w.session
	sc, run := s.sftp, desktopFilesRunner(s, f)
	target := item.target
	kind := desktopFilesArchiveKind(target)
	if sc == nil || run == nil || f.job != nil || !desktopFilesPath(target) || kind == "" || !item.Mode.IsRegular() || item.Link {
		return
	}
	start := func(out string) { a.desktopFilesExtract(w, sc, run, target, out, folder) }
	parent := path.Dir(target)
	if !folder {
		title := locales.DesktopFilesExtractHere()
		a.desktopFilesConfirm(w, title, target+" → "+parent, title, func() { start(parent) })
		return
	}
	title := locales.DesktopFilesExtractTo()
	name := item.Name[:len(item.Name)-len(kind)]
	f.modal = &desktopFilesPrompt{title: title, text: target + " → " + parent, field: locales.NewFolder(), value: name, action: locales.DesktopFilesExtract(),
		valid:  func(p *desktopFilesPrompt) bool { return desktopFilesName(p.value) },
		accept: func(p *desktopFilesPrompt) { start(path.Join(parent, p.value)) },
	}
}

func (a *App) desktopFilesExtract(w *desktopWindow, sc *sftp.Client, run desktopFilesExec, archive, dest string, folder bool) {
	if w.session.sftp != sc {
		return
	}
	dir := path.Dir(archive)
	a.desktopFilesWork(w, locales.DesktopFilesExtract()+": "+archive+" → "+dest, true, []string{dir}, func(ctx context.Context, report func(string)) error {
		if folder {
			if err := desktopFilesAbsent(sc, dest); err != nil {
				return err
			}
		}
		return desktopFilesStage(sc, dir, func(stage string) error {
			data := path.Join(stage, "data")
			if err := sc.Mkdir(data); err != nil {
				return err
			}
			backup := path.Join(stage, path.Base(archive))
			if backup == data {
				return errRelayPath
			}
			if err := desktopFilesRun(ctx, run, desktopFilesTool("cp")+relayQuote("cp", "--", archive, backup), nil); err != nil {
				return err
			}
			if err := desktopFilesValidateArchive(ctx, run, backup); err != nil {
				return err
			}
			if err := desktopFilesRun(ctx, run, desktopFilesExtractCommand(backup, data), report); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if folder {
				return desktopFilesPublish(sc, data, dest)
			}
			infos, err := sc.ReadDir(data)
			if err != nil {
				return err
			}
			for _, info := range infos {
				if !desktopFilesName(info.Name()) {
					return errRelayPath
				}
				if err := desktopFilesAbsent(sc, path.Join(dest, info.Name())); err != nil {
					return err
				}
			}
			for _, info := range infos {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := desktopFilesPublish(sc, path.Join(data, info.Name()), path.Join(dest, info.Name())); err != nil {
					return err
				}
			}
			return nil
		})
	}, nil)
}

func desktopFilesValidateArchive(ctx context.Context, run desktopFilesExec, archive string) error {
	var list string
	zip := desktopFilesArchiveKind(archive) == ".zip"
	switch desktopFilesArchiveKind(archive) {
	case ".zip":
		list = desktopFilesTool("unzip") + "unset UNZIP UNZIPOPT ZIPINFO ZIPINFOOPT\n" + relayQuote("unzip", "-Z", "-l", archive)
	case ".gz":
		if !desktopFilesName(path.Base(archive)[:len(path.Base(archive))-3]) {
			return errRelayPath
		}
		return nil
	default:
		list = desktopFilesTool("tar") + "unset TAR_OPTIONS\n" + relayQuote("tar", "--quoting-style=escape", "-tvf", archive)
	}
	unsafe := false
	part := ""
	_, stderr, err := run(ctx, list, func(chunk string) {
		if unsafe {
			return
		}
		part += chunk
		for {
			line, rest, ok := strings.Cut(part, "\n")
			if !ok {
				unsafe = len(part) > 64<<10
				break
			}
			part = rest
			if line == "" || zip && (strings.HasPrefix(line, "Archive: ") || strings.HasPrefix(line, "Zip file size:") || line[0] >= '0' && line[0] <= '9' && (strings.Contains(line, " files, ") || strings.Contains(line, " file, "))) {
				continue
			}
			if line[0] != '-' && line[0] != 'd' {
				unsafe = true
				return
			}
		}
	})
	if unsafe || part != "" {
		return errors.New(locales.DesktopFilesUnsafeArchive())
	}
	if tool, ok := strings.CutPrefix(strings.TrimSpace(stderr), "desktop-files-missing:"); err != nil && ok {
		return errors.New(locales.DesktopFilesMissingTool(tool))
	}
	return desktopFilesCommandError(stderr, err)
}
