package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type composeRegistration struct {
	Project string `json:"project"`
	Dir     string `json:"dir"`
	Marker  string `json:"marker,omitempty"`
}

func (entry composeRegistration) compose() monitor.Compose {
	return monitor.Compose{Project: entry.Project, Dir: entry.Dir, Files: []string{"compose.yaml"}}
}

func composeRegistryPath(s *Session) string {
	host := s.Host
	key := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", host.ID, host.Addr, host.Port, host.User)))
	return filepath.Join(config.Dir(), "compose-projects", hex.EncodeToString(key[:])+".json")
}

var composeRegistryLock sync.Mutex

func readComposeRegistry(file string) ([]composeRegistration, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []composeRegistration
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !monitor.ValidComposeProject(entry.Project) || !monitor.ValidComposeDirectory(entry.Dir) || entry.Marker != "" && !monitor.ValidName(entry.Marker) {
			return nil, errors.New("invalid Compose project registration")
		}
	}
	return entries, nil
}

func updateComposeRegistry(file string, entry composeRegistration, remove bool) error {
	composeRegistryLock.Lock()
	defer composeRegistryLock.Unlock()
	entries, err := readComposeRegistry(file)
	if err != nil {
		return err
	}
	entries = slices.DeleteFunc(entries, func(old composeRegistration) bool { return old.Project == entry.Project })
	if !remove {
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".compose-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

type composeDirectory struct {
	exists, nonEmpty, privileged bool
	occupied                     bool
}

var errComposeDirectoryChanged = errors.New("Compose project directory changed")
var errComposeFileExists = errors.New("Compose configuration already exists")

type composeFileStore interface {
	inspect(context.Context, string) (composeDirectory, error)
	mkdir(context.Context, string, bool) (bool, error)
	create(context.Context, string, []byte, bool) (bool, error)
	remove(context.Context, string, string) error
}

type composeSSHFiles struct {
	sftp   *sftp.Client
	client *ssh.Client
	root   monitor.DockerRootRunner
	user   string
}

func (a *App) composeFilesOf(s *Session) composeFileStore {
	if files := s.docker.compose.files; files != nil {
		return files
	}
	privilege := a.sessionPrivilege(s)
	return composeSSHFiles{sftp: s.sftp, client: s.client, root: privilege.runner.Run, user: privilege.user}
}

func composeShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (remote composeSSHFiles) inspect(ctx context.Context, dir string) (composeDirectory, error) {
	if !monitor.ValidComposeDirectory(dir) {
		return composeDirectory{}, errComposeDirectoryChanged
	}
	if remote.sftp != nil {
		info, err := remote.sftp.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			for parent := path.Dir(dir); ; parent = path.Dir(parent) {
				real, checkErr := remote.sftp.RealPath(parent)
				if checkErr == nil {
					if real != parent {
						return composeDirectory{}, errComposeDirectoryChanged
					}
					return composeDirectory{}, nil
				}
				if !errors.Is(checkErr, fs.ErrNotExist) || parent == "/" {
					break
				}
			}
		case err == nil:
			if !info.IsDir() {
				return composeDirectory{}, errComposeDirectoryChanged
			}
			real, err := remote.sftp.RealPath(dir)
			if err == nil && real != dir {
				return composeDirectory{}, errComposeDirectoryChanged
			}
			if err == nil {
				files, err := remote.sftp.ReadDirContext(ctx, dir)
				if err == nil {
					occupied := slices.ContainsFunc(files, func(file fs.FileInfo) bool { return file.Name() == "compose.yaml" || file.Name() == ".env" })
					return composeDirectory{exists: true, nonEmpty: len(files) != 0, occupied: occupied}, nil
				}
			}
		}
	}
	script := `dir=$1
if [ -e "$dir" ] || [ -L "$dir" ]; then
  [ ! -L "$dir" ] && [ -d "$dir" ] || exit 40
  [ "$(cd "$dir" && pwd -P)" = "$dir" ] || exit 40
  for file in "$dir/compose.yaml" "$dir/.env"; do
    if [ -e "$file" ] || [ -L "$file" ]; then printf occupied; exit 0; fi
  done
  item=$(find "$dir" -mindepth 1 -maxdepth 1 -print -quit) || exit 1
  if [ -n "$item" ]; then printf nonempty; else printf empty; fi
else
  parent=${dir%/*}
  while [ -n "$parent" ] && [ ! -e "$parent" ] && [ ! -L "$parent" ]; do parent=${parent%/*}; done
  parent=${parent:-/}
  [ "$(cd "$parent" && pwd -P)" = "$parent" ] || exit 40
  printf missing
fi`
	output, err := remote.exec(ctx, false, script, dir, nil)
	privileged := false
	if err != nil && !errors.Is(err, errComposeDirectoryChanged) && ctx.Err() == nil {
		output, err = remote.exec(ctx, true, script, dir, nil)
		privileged = true
	}
	if err != nil {
		return composeDirectory{}, err
	}
	switch output {
	case "missing":
		return composeDirectory{privileged: privileged}, nil
	case "empty", "nonempty":
		return composeDirectory{exists: true, nonEmpty: output == "nonempty", privileged: privileged}, nil
	case "occupied":
		return composeDirectory{exists: true, nonEmpty: true, occupied: true, privileged: privileged}, nil
	}
	return composeDirectory{}, errComposeDirectoryChanged
}

func (remote composeSSHFiles) mkdir(ctx context.Context, dir string, privileged bool) (bool, error) {
	if !privileged && remote.sftp != nil {
		if err := remote.sftp.MkdirAll(path.Dir(dir)); err == nil {
			if err := remote.sftp.Mkdir(dir); err == nil {
				return false, nil
			}
		}
	}
	script := `parent=${1%/*}
ancestor=$parent
while [ -n "$ancestor" ] && [ ! -e "$ancestor" ] && [ ! -L "$ancestor" ]; do ancestor=${ancestor%/*}; done
ancestor=${ancestor:-/}
[ "$(cd "$ancestor" && pwd -P)" = "$ancestor" ] || exit 40
mkdir -p -- "$parent" && mkdir -- "$1" || exit 1
if [ -n "$SUDO_UID" ] && [ -n "$SUDO_GID" ]; then chown "$SUDO_UID:$SUDO_GID" -- "$1"; fi`
	if !privileged && remote.sftp == nil {
		if _, err := remote.exec(ctx, false, script, dir, nil); err == nil || errors.Is(err, errComposeDirectoryChanged) || ctx.Err() != nil {
			return false, err
		}
	}
	_, err := remote.exec(ctx, true, script, dir, nil)
	return true, err
}

func (remote composeSSHFiles) create(ctx context.Context, file string, data []byte, privileged bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !privileged && remote.sftp != nil {
		if _, link := remote.sftp.HasExtension("hardlink@openssh.com"); link {
			tmp := file + ".tmp-" + config.NewHostID()
			handle, err := remote.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
			if err == nil {
				defer remote.sftp.Remove(tmp)
				modeErr := handle.Chmod(0o600)
				var writeErr error
				if modeErr == nil {
					_, writeErr = io.Copy(handle, bytes.NewReader(data))
				}
				if err := errors.Join(modeErr, writeErr, handle.Close()); err != nil {
					return false, err
				}
				return false, remote.sftp.Link(tmp, file)
			}
			if !errors.Is(err, fs.ErrPermission) {
				return false, err
			}
		} else {
			handle, err := remote.sftp.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
			if err == nil {
				modeErr := handle.Chmod(0o600)
				var writeErr error
				if modeErr == nil {
					_, writeErr = io.Copy(handle, bytes.NewReader(data))
				}
				return false, errors.Join(modeErr, writeErr, handle.Close())
			}
			if !errors.Is(err, fs.ErrPermission) {
				return false, err
			}
		}
	}
	script := `dir=${1%/*}
[ "$(cd "$dir" && pwd -P)" = "$dir" ] || exit 40
umask 077
temp=$(mktemp -- "$dir/.compose-XXXXXXXX") || exit 1
trap 'rm -f -- "$temp"' EXIT
tee -- "$temp" >/dev/null || exit 1
if [ -n "$SUDO_UID" ] && [ -n "$SUDO_GID" ]; then chown "$SUDO_UID:$SUDO_GID" -- "$temp" || exit 1; fi
ln -T -- "$temp" "$1"`
	if !privileged && remote.sftp == nil {
		if _, err := remote.exec(ctx, false, script, file, data); err == nil || errors.Is(err, errComposeDirectoryChanged) || ctx.Err() != nil {
			return false, err
		}
	}
	_, err := remote.exec(ctx, true, script, file, data)
	return true, err
}

func (remote composeSSHFiles) remove(ctx context.Context, dir, marker string) error {
	if !monitor.ValidComposeDirectory(dir) || !monitor.ValidName(marker) {
		return errComposeDirectoryChanged
	}
	script := `[ ! -L "$1" ] && [ "$(cd "$1" && pwd -P)" = "$1" ] || exit 40
[ -f "$1/compose.yaml" ] && [ ! -L "$1/compose.yaml" ] || exit 40
[ -f "$1/"` + composeShellArg(composeMarker(marker)) + ` ] && [ ! -L "$1/"` + composeShellArg(composeMarker(marker)) + ` ] || exit 40
rm -rf --one-file-system -- "$1"`
	_, err := remote.exec(ctx, false, script, dir, nil)
	return err
}

func composeMarker(marker string) string { return ".finallyshell-compose-" + marker }

func (remote composeSSHFiles) exec(ctx context.Context, privileged bool, script, name string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	command := "sh -c " + composeShellArg(script) + " 'compose-files' " + composeShellArg(name)
	var output string
	var err error
	if privileged {
		if remote.root == nil {
			return "", errNotConnected
		}
		if remote.user != "" && remote.user != "root" {
			user := composeShellArg(remote.user)
			command = "SUDO_UID=$(id '-u' '--' " + user + ") && SUDO_GID=$(id '-g' '--' " + user + ") || exit 1\nexport SUDO_UID SUDO_GID\n" + command
		}
		options := monitor.PrivilegeOptions{ScriptStdin: true}
		if data != nil {
			options.Stdin = bytes.NewReader(data)
		}
		output, _, err = remote.root(ctx, command, options)
	} else {
		if remote.client == nil {
			return "", errNotConnected
		}
		var session *ssh.Session
		session, err = remote.client.NewSession()
		if err != nil {
			return "", err
		}
		defer session.Close()
		stop := context.AfterFunc(ctx, func() { session.Close() })
		defer stop()
		session.Stdin = bytes.NewReader(data)
		var stdout []byte
		stdout, err = session.Output(command)
		output = string(stdout)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) && exit.ExitStatus() == 40 {
		return "", errComposeDirectoryChanged
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(output), nil
}

func createComposeFiles(ctx context.Context, remote composeFileStore, dir, marker string, expected composeDirectory, yaml, env []byte) (bool, bool, error) {
	current, err := remote.inspect(ctx, dir)
	if err != nil {
		return false, false, err
	}
	if current.exists != expected.exists || current.nonEmpty != expected.nonEmpty {
		return false, false, errComposeDirectoryChanged
	}
	if current.occupied {
		return false, false, errComposeFileExists
	}
	privileged := current.privileged
	if !current.exists {
		used, err := remote.mkdir(ctx, dir, privileged)
		if err != nil {
			return used, false, err
		}
		privileged = privileged || used
	}
	used, err := remote.create(ctx, path.Join(dir, "compose.yaml"), yaml, privileged)
	privileged = privileged || used
	if err != nil {
		return privileged, false, err
	}
	if len(env) != 0 {
		used, err = remote.create(ctx, path.Join(dir, ".env"), env, privileged)
		privileged = privileged || used
	}
	if err == nil && marker != "" {
		used, err = remote.create(ctx, path.Join(dir, composeMarker(marker)), nil, privileged)
		privileged = privileged || used
	}
	return privileged, true, err
}
