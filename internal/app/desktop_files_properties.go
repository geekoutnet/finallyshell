package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/pkg/sftp"
)

type desktopFilesProperties struct {
	item                   desktopFilesItem
	info                   fs.FileInfo
	accessed               time.Time
	owner, group, octal    string
	oldOwner, oldGroup     string
	note, oldNote          string
	bytes                  int64
	reading, measuring     bool
	unknownSize, recursive bool
	problem                string
	cancel                 context.CancelFunc
}

func desktopFilesGoMode(mode uint32) fs.FileMode {
	result := fs.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		result |= fs.ModeSetuid
	}
	if mode&0o2000 != 0 {
		result |= fs.ModeSetgid
	}
	if mode&0o1000 != 0 {
		result |= fs.ModeSticky
	}
	return result
}

func desktopFilesUnixMode(mode fs.FileMode) uint32 {
	result := uint32(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		result |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		result |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		result |= 0o1000
	}
	return result
}

func desktopFilesPermissionText(mode fs.FileMode) string {
	text := []byte(mode.Perm().String()[1:])
	for index, flag := range []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky} {
		if mode&flag != 0 {
			position := 3*index + 2
			symbol := byte('s')
			if index == 2 {
				symbol = 't'
			}
			if text[position] != 'x' {
				symbol -= 'a' - 'A'
			}
			text[position] = symbol
		}
	}
	return string(text)
}

func (props *desktopFilesProperties) mode() (uint32, bool) {
	if len(props.octal) < 3 || len(props.octal) > 4 || strings.IndexFunc(props.octal, func(character rune) bool { return character < '0' || character > '7' }) >= 0 {
		return 0, false
	}
	mode, err := strconv.ParseUint(props.octal, 8, 12)
	return uint32(mode), err == nil
}

func desktopFilesOwnerValid(name string) bool {
	if _, err := strconv.ParseUint(name, 10, 32); err == nil {
		return true
	}
	return relayUser.MatchString(name)
}

func (a *App) desktopFilesAskProperties(w *desktopWindow, item desktopFilesItem) {
	f, s := w.explorer(), w.session
	sc, run := s.sftp, desktopFilesRunner(s, f)
	if sc == nil || !desktopFilesPath(item.target) {
		return
	}
	ctx, cancel := context.WithTimeout(desktopFilesLife(s), time.Minute)
	note := a.desktopFilesHost(s).Notes[item.target]
	props := &desktopFilesProperties{item: item, note: note, oldNote: note, reading: true, cancel: cancel}
	prompt := &desktopFilesPrompt{title: locales.DesktopFilesProperties(), properties: props}
	f.modal = prompt
	go func() {
		defer cancel()
		info, err := sc.Lstat(item.target)
		a.post(func() {
			props.reading = false
			if err != nil {
				props.problem = err.Error()
				return
			}
			props.info, props.bytes, props.measuring = info, info.Size(), info.IsDir() && run != nil
			props.unknownSize = info.IsDir()
			props.owner = strconv.FormatUint(uint64(item.UID), 10)
			props.group = strconv.FormatUint(uint64(item.GID), 10)
			if attr, ok := info.Sys().(*sftp.FileStat); ok {
				props.owner, props.group = fmt.Sprint(attr.UID), fmt.Sprint(attr.GID)
				props.accessed = time.Unix(int64(attr.Atime), 0)
			}
			props.oldOwner, props.oldGroup = props.owner, props.group
			props.octal = fmt.Sprintf("%04o", desktopFilesUnixMode(info.Mode()))
		})
		if err != nil || !info.IsDir() || run == nil {
			return
		}
		// Its own error: the post above still reads err.
		out, stderr, duErr := run(ctx, relayQuote("du", "-sb", "--", item.target), nil)
		size, _, found := strings.Cut(out, "\t")
		amount, parseErr := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		a.post(func() {
			props.measuring = false
			if duErr == nil && found && parseErr == nil && amount >= 0 {
				props.bytes, props.unknownSize = amount, false
			} else if duErr != nil && !errors.Is(duErr, context.Canceled) {
				props.problem = desktopFilesCommandError(stderr, duErr).Error()
			}
		})
	}()
}

func (a *App) desktopFilesApplyProperties(w *desktopWindow, p *desktopFilesProperties) {
	f, s := w.explorer(), w.session
	sc := s.sftp
	if p.info == nil || sc == nil {
		return
	}
	mode, ok := p.mode()
	chmod := mode != desktopFilesUnixMode(p.info.Mode())
	chown := p.owner != p.oldOwner || p.group != p.oldGroup
	if !ok || !desktopFilesOwnerValid(p.owner) || !desktopFilesOwnerValid(p.group) {
		p.problem = locales.DesktopFilesBadPermissions()
		return
	}
	if !chmod && !chown && !p.recursive {
		a.desktopFilesNote(s, p.item.target, p.note)
		p.cancel()
		f.modal = nil
		return
	}
	target, owner := p.item.target, p.owner+":"+p.group
	run := desktopFilesRunner(s, f)
	if target == "/" || run == nil || chmod && p.info.Mode()&fs.ModeSymlink != 0 {
		p.problem = locales.DesktopFilesBadPermissions()
		return
	}
	var commands, changes []string
	if chown {
		commands = append(commands, desktopFilesOwnerCommand(target, owner, p.recursive))
		changes = append(changes, locales.DesktopFilesOwnerGroup()+": "+owner)
	}
	if chmod || !chown || p.info.Mode()&fs.ModeSymlink == 0 && mode&0o7000 != 0 {
		formatted := fmt.Sprintf("%04o", mode)
		commands = append(commands, desktopFilesModeCommand(target, formatted, p.recursive))
		changes = append(changes, locales.ColPermissions()+": "+formatted)
	}
	if p.recursive {
		changes = append(changes, locales.DesktopFilesRecursiveWarning())
	}
	p.cancel()
	title := locales.DesktopFilesApplyPermissions()
	a.desktopFilesConfirm(w, title, target+"\n"+strings.Join(changes, "\n"), locales.DesktopFilesApply(), func() {
		if s.sftp != sc {
			return
		}
		if chown {
			priv := a.sessionPrivilege(s).runner
			run = func(ctx context.Context, script string, report func(string)) (string, string, error) {
				return priv.Run(ctx, script, monitor.PrivilegeOptions{Progress: report, ScriptStdin: true, Interrupt: true})
			}
		}
		a.desktopFilesWork(w, title+": "+target, true, []string{path.Dir(target)}, func(ctx context.Context, report func(string)) error {
			info, err := sc.Lstat(target)
			if err != nil || info.Mode() != p.info.Mode() || !info.ModTime().Equal(p.info.ModTime()) {
				return errors.Join(errRelayChanged, err)
			}
			return desktopFilesRun(ctx, run, strings.Join(commands, " &&\n"), report)
		}, func(err error) {
			if err == nil {
				a.desktopFilesNote(s, target, p.note)
			}
		})
	})
}
