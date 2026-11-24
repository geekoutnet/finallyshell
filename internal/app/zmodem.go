package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"finallyshell/internal/termenc"
	"finallyshell/internal/zmodem"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) remoteTerminalStream(s *Session, spec shellSpec, conn io.ReadWriteCloser) *terminalStream {
	raw := zmodem.Wrap(conn, func(ctx context.Context, cancel context.CancelFunc, sending bool, transfer *zmodem.Transfer) error {
		return a.zmodemSession(ctx, cancel, s, spec.hostID, sending, transfer)
	})
	return newTerminalStream(termenc.Wrap(raw, spec.enc), spec.highlight, func() {
		a.post(func() { a.notify(locales.RecFailed()) })
	})
}

type zmodemRunner interface {
	Send([]string, func(zmodem.Progress)) error
	Receive(string, func(string, int64) bool, func(zmodem.Progress)) error
}

type zmodemOffer struct {
	host, folder string
	job          *transferJob
}

func (a *App) zmodemSession(ctx context.Context, cancel context.CancelFunc, s *Session, hostID string, sending bool, transfer zmodemRunner) error {
	watch := make(chan zmodemOffer, 1)
	a.post(func() {
		if ctx.Err() != nil || s.Host == nil || s.Host.ID != hostID || s.status != statusConnected {
			watch <- zmodemOffer{}
			return
		}
		q := &a.transfers
		q.nextID++
		job := &transferJob{id: q.nextID, sess: s, zmodem: true, state: transferRunning, total: -1, cancel: cancel}
		job.src = locales.ZmTransfer()
		job.dir = download
		if sending {
			job.dir = upload
		}
		q.jobs = append(q.jobs, job)
		a.showTransfers()
		watch <- zmodemOffer{host: s.label(), folder: a.downloadDir(), job: job}
	})
	var offer zmodemOffer
	select {
	case <-ctx.Done():
		a.post(func() {
			select {
			case offer := <-watch:
				if offer.job != nil {
					offer.job.cancel, offer.job.state = nil, transferCanceled
					a.showTransfers()
				}
			default:
			}
		})
		return ctx.Err()
	case offer = <-watch:
	}
	if offer.job == nil {
		return context.Canceled
	}
	job := offer.job
	dir, paths, err := a.chooseZmodem(ctx, offer, sending)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	var progress zmodem.Progress
	var last time.Time
	if err == nil {
		a.post(func() { job.dstDir = dir })
		report := func(next zmodem.Progress) {
			changed := next.Name != progress.Name || next.Done < progress.Done
			if next.Path == "" {
				next.Path = progress.Path
			}
			progress = next
			now := time.Now()
			if !changed && now.Sub(last) < 200*time.Millisecond {
				return
			}
			last = now
			a.post(func() {
				if changed {
					job.sampleAt, job.sampleDone, job.speed = now, next.Done, 0
				}
				job.src, job.total = next.Name, next.Total
				job.progress(next.Done, now)
				a.showTransfers()
			})
		}
		if sending {
			err = transfer.Send(paths, report)
		} else {
			err = transfer.Receive(dir, func(name string, size int64) bool {
				return a.confirmZmodemFile(ctx, offer.host, dir, name, size)
			}, report)
		}
	}
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	a.post(func() {
		job.cancel = nil
		if progress.Name != "" {
			job.src, job.done, job.total, job.local = progress.Name, progress.Done, progress.Total, progress.Path
			if err == nil && job.local != "" {
				job.src = filepath.Base(job.local)
			}
		}
		switch {
		case err == nil:
			job.state = transferDone
		case errors.Is(err, context.Canceled) || errors.Is(err, zmodem.ErrCanceled):
			job.state = transferCanceled
		case errors.Is(err, zmodem.ErrTimeout):
			job.state, job.err = transferFailed, errors.New(locales.ZmTimeout())
		default:
			job.state, job.err = transferFailed, errors.New(locales.ZmFailed())
		}
		a.showTransfers()
	})
	return err
}

func (a *App) chooseZmodem(ctx context.Context, offer zmodemOffer, sending bool) (string, []string, error) {
	type choice struct {
		dir   string
		paths []string
		err   error
	}
	result := make(chan choice, 1)
	go func() {
		if sending {
			paths, err := a.plat.OpenFiles(locales.ChooseUploadFiles(), false)
			if err != nil || ctx.Err() != nil {
				result <- choice{err: err}
				return
			}
			var names []string
			for _, path := range paths {
				name, ok := zmodem.SourceName(path)
				if !ok {
					result <- choice{err: errors.New("unsafe ZMODEM file name")}
					return
				}
				names = append(names, name)
			}
			if len(paths) != 0 && a.plat.Confirm(Confirmation{
				Title: locales.ZmTransfer(), Message: locales.ZmConfirmSend(offer.host),
				Detail: locales.ZmSelectedFiles(len(paths)) + "\n" + strings.Join(names, "\n") + "\n\n" + locales.ZmSendWarning(),
				OK:     locales.UploadDirection(), Cancel: locales.Cancel(),
			}) {
				result <- choice{paths: paths}
				return
			}
			result <- choice{err: context.Canceled}
			return
		}
		var dir string
		var err error
		if picker, ok := a.plat.(interface {
			ChooseFolderAt(WindowKind, string, string) (string, error)
		}); ok {
			dir, err = picker.ChooseFolderAt(WindowMain, locales.ZmChooseFolder(), offer.folder)
		} else {
			dir, err = a.plat.ChooseFolder(WindowMain, locales.ZmChooseFolder())
		}
		if err == nil && dir != "" && !filepath.IsAbs(dir) {
			err = errors.New("invalid ZMODEM destination")
		}
		if err == nil && (dir == "" || ctx.Err() != nil) {
			err = context.Canceled
		}
		result <- choice{dir: dir, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case picked := <-result:
		return picked.dir, picked.paths, picked.err
	}
}

func (a *App) confirmZmodemFile(ctx context.Context, host, dir, name string, size int64) bool {
	if ctx.Err() != nil {
		return false
	}
	answer := make(chan bool, 1)
	go func() {
		answer <- a.plat.Confirm(Confirmation{
			Title: locales.ZmTransfer(), Message: locales.ZmConfirmReceive(name, formatSize(size), host),
			Detail: locales.ZmReceiveDefault(dir), OK: locales.DownloadDirection(), Cancel: locales.Cancel(),
		})
	}()
	select {
	case yes := <-answer:
		return yes && ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func (a *App) zmodemTransferMenu(menu *ui.Menu, job *transferJob) {
	if !job.finished() {
		if menu.Item(locales.Cancel()).Chosen() {
			a.cancelTransfer(job)
		}
		return
	}
	if job.state == transferDone && job.local != "" {
		if menu.Item(locales.OpenFile()).Chosen() {
			a.openDownload(job)
		}
		if menu.Item(locales.ShowInFolder()).Chosen() {
			a.plat.ShowInFolder(job.local)
		}
	} else if job.state != transferDone {
		menu.Item(locales.Retry()).Disabled(true).Subtitle(locales.ZmRestart())
	}
}
