package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/locales"
	"golang.org/x/crypto/ssh"
)

func dockerImgArchiveName(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")
}

func (a *App) pickDockerImgArchive(dialog *dockerImgDialog) {
	dialog.picking = true
	title := locales.DockerImgChooseArchive()
	go func() {
		file, err := a.plat.OpenFileOfType(WindowMain, title, "TAR / TAR.GZ", []string{"tar", "tar.gz", "tgz"})
		a.post(func() {
			if !dialog.current() {
				return
			}
			dialog.picking = false
			switch {
			case err != nil:
				dialog.validation = err.Error()
			case file != "" && !dockerImgArchiveName(file):
				dialog.validation = locales.DockerImgInvalidArchive()
			case file != "":
				dialog.local, dialog.validation = file, ""
			}
		})
	}()
}

func dockerImgUpload(ctx context.Context, client *ssh.Client, tool monitor.DockerCmd, run dockerRunner, local string, importing bool, tag string, progress func(string), uploaded string) (string, func() error, error) {
	if client == nil || !dockerImgArchiveName(local) {
		return "", nil, monitor.ErrImagePath
	}
	info, err := os.Stat(local)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, monitor.ErrImagePath
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	dir := fmt.Sprintf("/tmp/finallyshell-img-%x", nonce)
	archive := path.Join(dir, filepath.Base(local))
	script, err := tool.ImageLoad(archive)
	if importing {
		script, err = tool.ImageImport(archive, tag)
	}
	if err != nil {
		return "", nil, err
	}
	if out, err := run(ctx, client, "umask 077\nmkdir -- '"+dir+"'\n", nil); err != nil {
		return "", nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	cleanup := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		quotedArchive := "'" + strings.ReplaceAll(archive, "'", `'\''`) + "'"
		out, err := run(ctx, client, "set -e\nrm -f -- "+quotedArchive+"\nrmdir -- '"+dir+"'\n", nil)
		if err != nil {
			return fmt.Errorf("%s: %w: %s", dir, err, strings.TrimSpace(out))
		}
		return nil
	}
	sftp, err := remotefs.NewClient(client)
	if err != nil {
		return "", cleanup, err
	}
	defer sftp.Close()
	stop := context.AfterFunc(ctx, func() { sftp.Close() })
	defer stop()
	var last time.Time
	err = remotefs.Upload(ctx, sftp, local, dir, func(done int64) {
		if time.Since(last) >= 400*time.Millisecond || done >= info.Size() {
			last = time.Now()
			progress(fmt.Sprintf("%s %s / %s\n", uploaded, formatSize(done), formatSize(info.Size())))
		}
	})
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return script, cleanup, err
}
