package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"

	"github.com/pkg/sftp"
)

const codeFileLimit = 5 << 20

var (
	errCodePath    = errors.New("invalid remote file path")
	errCodeRegular = errors.New("not a regular file")
	errCodeAtomic  = errors.New("the SFTP server does not support atomic replacement")
	errCodeBackup  = errors.New("a backup already exists at this timestamp")
	errCodeRead    = errors.New("incomplete remote read")
	errCodeResult  = errors.New("the server did not report a backup")
)

func validCodePath(file string) bool {
	return path.IsAbs(file) && file != "/" && path.Clean(file) == file && !strings.ContainsAny(file, "\x00\r\n")
}

func codeHasMarker(output, marker string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSuffix(line, "\r") == marker {
			return true
		}
	}
	return false
}

func readRootCode(ctx context.Context, run monitor.CaddyRunner, file string) ([]byte, error) {
	if !validCodePath(file) {
		return nil, errCodePath
	}
	quote := monitor.SystemCommand
	script := "file=" + quote(file) + `
if ! test '-f' "$file"; then printf '%s\n' 'cedit:regular' >&2; exit 42; fi
( 'head' '-c' ` + quote(strconv.Itoa(codeFileLimit+1)) + ` '--' "$file" && printf '\377\000\376\377' ) | 'base64'
`
	stdout, stderr, err := run(ctx, script, nil, nil)
	if codeHasMarker(stderr, "cedit:regular") || codeHasMarker(stdout, "cedit:regular") {
		return nil, errCodeRegular
	}
	if err != nil {
		return nil, codeRunError(stderr, err)
	}
	encoded := strings.NewReplacer("\r", "", "\n", "").Replace(stdout)
	const end = "\xff\x00\xfe\xff"
	if len(encoded) > base64.StdEncoding.EncodedLen(codeFileLimit+1+len(end)) {
		return nil, errCodeRead
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) < len(end) || string(data[len(data)-len(end):]) != end {
		return nil, codeRunError(stderr, errCodeRead)
	}
	return data[:len(data)-len(end)], nil
}

func writeRootCode(ctx context.Context, run monitor.CaddyRunner, file, before, after string, progress func(string)) (string, error) {
	if !validCodePath(file) {
		return "", errCodePath
	}
	quote := monitor.SystemCommand
	script := "file=" + quote("./"+path.Base(file)) + "\nfolder=" + quote(path.Dir(file)) +
		"\nexpected=" + quote(fmt.Sprintf("%x", sha256.Sum256([]byte(before)))) +
		"\nreceived=" + quote(fmt.Sprintf("%x", sha256.Sum256([]byte(after)))) + `
set -e
umask '077'
'cd' '-P' '--' "$folder"
unchanged() {
    if test '-f' "$file" && ! test '-L' "$file"; then
        sum=$('sha256sum' < "$file") && test "${sum%% *}" '=' "$expected" && return '0'
    fi
    printf '%s\n' 'cedit:changed' >&2
    exit 43
}
unchanged
stage=$('mktemp' '-d' '--' './.finallyshell-edit.XXXXXXXX')
temp=$stage'/new'
snapshot=$stage'/original'
trap '"rm" "-f" "--" "$temp" "$snapshot"; "rmdir" "--" "$stage"' '0'
trap 'exit 1' 'HUP' 'INT' 'TERM'
'cat' > "$temp"
sum=$('sha256sum' < "$temp")
test "${sum%% *}" '=' "$received"
'cp' '-p' '--' "$file" "$snapshot"
sum=$('sha256sum' < "$snapshot")
if ! test "${sum%% *}" '=' "$expected"; then printf '%s\n' 'cedit:changed' >&2; exit 43; fi
'chown' "--reference=$snapshot" '--' "$temp"
'chmod' "--reference=$snapshot" '--' "$temp"
stamp=$('date' '+%Y%m%d%H%M%S')
backup=$file'.bak-'$stamp
unchanged
if ! 'ln' '-T' '--' "$snapshot" "$backup"; then
    if test '-e' "$backup" || test '-L' "$backup"; then printf '%s\n' 'cedit:backup' >&2; fi
    exit 44
fi
unchanged
'mv' '-fT' '--' "$temp" "$file"
printf '%s\n' ` + quote(file) + `'.bak-'"$stamp"
`
	stdout, stderr, err := run(ctx, script, strings.NewReader(after), progress)
	backup := strings.TrimRight(stdout, "\r\n")
	if err != nil {
		if codeHasMarker(stderr, "cedit:changed") || codeHasMarker(stdout, "cedit:changed") {
			return "", monitor.ErrCaddyChanged
		}
		if codeHasMarker(stderr, "cedit:backup") || codeHasMarker(stdout, "cedit:backup") {
			return "", errCodeBackup
		}
		return "", codeRunError(stderr, err)
	}
	if stamp, ok := strings.CutPrefix(backup, file+".bak-"); !ok || len(stamp) != 14 || strings.Trim(stamp, "0123456789") != "" {
		return "", errCodeResult
	}
	return backup, nil
}

func codeRunError(stderr string, err error) error {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

func readSFTPCode(ctx context.Context, client *sftp.Client, file string) ([]byte, error) {
	if !validCodePath(file) {
		return nil, errCodePath
	}
	info, err := client.Stat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errCodeRegular
	}
	remote, err := client.Open(file)
	if err != nil {
		return nil, err
	}
	defer remote.Close()
	return io.ReadAll(io.LimitReader(codeContextReader{ctx, remote}, codeFileLimit+1))
}

type codeContextReader struct {
	ctx context.Context
	io.Reader
}

func (reader codeContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.Reader.Read(data)
}

func writeSFTPCode(ctx context.Context, client *sftp.Client, file, before, after string) (string, error) {
	if !validCodePath(file) {
		return "", errCodePath
	}
	if _, ok := client.HasExtension("posix-rename@openssh.com"); !ok {
		return "", errCodeAtomic
	}
	check := func() (fs.FileInfo, error) {
		info, err := client.Lstat(file)
		if errors.Is(err, fs.ErrNotExist) || err == nil && !info.Mode().IsRegular() {
			return nil, monitor.ErrCaddyChanged
		}
		if err != nil {
			return nil, err
		}
		data, err := readSFTPCode(ctx, client, file)
		if err != nil {
			return nil, err
		}
		if string(data) != before {
			return nil, monitor.ErrCaddyChanged
		}
		return info, nil
	}
	info, err := check()
	if err != nil {
		return "", err
	}
	temp := path.Join(path.Dir(file), ".finallyshell-edit-"+rand.Text())
	if err := stageSFTPCode(ctx, client, temp, info, after); err != nil {
		return "", err
	}
	defer client.Remove(temp)
	backup := file + ".bak-" + time.Now().UTC().Format("20060102150405")
	if err := stageSFTPCode(ctx, client, backup, info, before); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", errCodeBackup
		}
		return "", err
	}
	if _, err := check(); err != nil {
		return backup, err
	}
	return backup, client.PosixRename(temp, file)
}

func stageSFTPCode(ctx context.Context, client *sftp.Client, file string, info fs.FileInfo, content string) (err error) {
	remote, err := client.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := remote.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			client.Remove(file)
		}
	}()
	if err := remote.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.Copy(remote, codeContextReader{ctx, strings.NewReader(content)}); err != nil {
		return err
	}
	if desired, ok := info.Sys().(*sftp.FileStat); ok {
		current, err := remote.Stat()
		if err != nil {
			return err
		}
		if actual, ok := current.Sys().(*sftp.FileStat); ok && (actual.UID != desired.UID || actual.GID != desired.GID) {
			if err := remote.Chown(int(desired.UID), int(desired.GID)); err != nil {
				return err
			}
		}
	}
	return remote.Chmod(info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
}
