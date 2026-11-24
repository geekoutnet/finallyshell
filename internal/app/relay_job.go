package app

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/remotefs"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type relayJob struct {
	target            *Session
	sourceName, label string
	src, dst          *ssh.Client
	srcLife, dstLife  context.Context
	task              relayTask
	files, totalFiles int
	mode              relayMode
	measured          bool
	runner            func(context.Context, func(relayProgress)) error
}

func (t *transferJob) relayProgress(progress relayProgress, now time.Time) {
	t.total, t.current = progress.total, progress.current
	t.relay.files, t.relay.totalFiles = progress.files, progress.totalFiles
	t.relay.mode, t.relay.measured = progress.mode, true
	t.progress(progress.bytes, now)
}

func relayTransferLabels(job *transferJob) (*ui.SVG, string, string) {
	return icons.Get("arrow-up-right"), job.relay.sourceName, job.relay.label + ":" + job.dstDir
}

func relayTransferDetail(c *ui.Context, job *transferJob) {
	if job.relay.measured {
		mode := relayModeOptions()[job.relay.mode]
		widgets.Muted(c, locales.RelayProgress(mode, job.relay.files, job.relay.totalFiles)).FontSize(11.5).SingleLine()
	}
	if job.state == transferCanceled && job.err != nil && job.err != context.Canceled {
		text := relayErrorText(job.err)
		widgets.Muted(c, text).FontSize(11.5).SingleLine().Tooltip(text)
	}
}

func (a *App) relayTransferMenu(row ui.Element, job *transferJob) {
	row.ContextMenu(func(menu *ui.Menu) {
		reason := ""
		if job.finished() {
			reason = locales.RelayTransferEnded()
		}
		if menu.Item(locales.Cancel()).Disabled(reason != "").Subtitle(reason).Chosen() && reason == "" {
			a.cancelTransfer(job)
		}
	})
}

func relayOpenSFTP(ctx context.Context, client *ssh.Client) (*sftp.Client, error) {
	if client == nil {
		return nil, errConnectionLost
	}
	type result struct {
		client *sftp.Client
		err    error
	}
	opened := make(chan result)
	go func() {
		channel, err := remotefs.NewClient(client)
		select {
		case opened <- result{channel, err}:
		case <-ctx.Done():
			if channel != nil {
				channel.Close()
			}
		}
	}()
	select {
	case answer := <-opened:
		return answer.client, answer.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (job *relayJob) run(ctx context.Context, report func(relayProgress)) error {
	if job.runner != nil {
		return job.runner(ctx, report)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, life := range []context.Context{job.srcLife, job.dstLife} {
		if life != nil {
			stop := context.AfterFunc(life, cancel)
			defer stop()
			if life.Err() != nil {
				return context.Canceled
			}
		}
	}
	openCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	src, err := relayOpenSFTP(openCtx, job.src)
	if err != nil {
		return err
	}
	defer src.Close()
	stopSource := context.AfterFunc(ctx, func() { src.Close() })
	defer stopSource()
	dst, err := relayOpenSFTP(openCtx, job.dst)
	if err != nil {
		return err
	}
	defer dst.Close()
	stopTarget := context.AfterFunc(ctx, func() { dst.Close() })
	defer stopTarget()
	remove := func(name string) error {
		if ctx.Err() == nil {
			return relayRemoveStage(ctx, dst, name)
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		client, err := relayOpenSFTP(cleanupCtx, job.dst)
		if err != nil {
			return err
		}
		defer client.Close()
		stopClose := context.AfterFunc(cleanupCtx, func() { client.Close() })
		defer stopClose()
		return relayRemoveStage(cleanupCtx, client, name)
	}
	err = job.task.move(ctx, relaySFTP{src}, relaySFTP{dst}, remove, report)
	if err != nil && ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		return errors.Join(ctx.Err(), err)
	}
	return err
}

func relayRemoveStage(ctx context.Context, client *sftp.Client, name string) error {
	if !relayDirectory(name) || !strings.HasPrefix(path.Base(name), relayStagePrefix) {
		return errRelayPath
	}
	real, err := client.RealPath(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || real != name {
		return errors.Join(errRelayChanged, err)
	}
	err = relayRemoveTree(ctx, client, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func relayRemoveTree(ctx context.Context, client *sftp.Client, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := client.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return client.Remove(name)
	}
	entries, err := client.ReadDirContext(ctx, name)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !relayBasename(entry.Name()) {
			return errRelayPath
		}
		if err := relayRemoveTree(ctx, client, path.Join(name, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return client.RemoveDirectory(name)
}
