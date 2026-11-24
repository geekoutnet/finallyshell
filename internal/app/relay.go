package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type relayMode int

const (
	relayAuto relayMode = iota
	relayDirect
	relayViaApp
)

type relayPolicy int

const (
	relaySkip relayPolicy = iota
	relayRename
	relayOverwrite
)

type relayItem struct {
	source, dest string
	backup       bool
}

type relayEndpoint struct {
	address, user string
	port          int
}

type relayRunner func(context.Context, string) (string, string, error)

var (
	relayHost    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
	relayUser    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	errRelayPath = errors.New("invalid transfer path")
)

func relayQuote(args ...string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
	}
	return strings.Join(quoted, " ")
}

func (target relayEndpoint) command(command string) (string, error) {
	ip, ipErr := netip.ParseAddr(target.address)
	if !relayHost.MatchString(target.address) && (ipErr != nil || ip.Zone() != "") ||
		!relayUser.MatchString(target.user) || target.port < 1 || target.port > 65535 {
		return "", errRelayPath
	}
	return relayQuote("ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=yes", "-o", "PreferredAuthentications=publickey",
		"-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "UpdateHostKeys=no",
		"-p", strconv.Itoa(target.port), "-l", target.user, "--", target.address, command), nil
}

func relayBasename(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\:`) && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ") &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func relayDirectory(dir string) bool {
	return path.IsAbs(dir) && dir == path.Clean(dir) && !strings.ContainsRune(dir, '\\') && strings.IndexFunc(dir, unicode.IsControl) < 0
}

func relayTar(dir string, names []string, exclude string) (string, error) {
	if !relayDirectory(dir) || len(names) == 0 {
		return "", errRelayPath
	}
	args := []string{"tar"}
	if exclude != "" {
		args = append(args, "--exclude="+exclude)
	}
	args = append(args, "-C", dir, "-cf", "-")
	for _, name := range names {
		if !relayBasename(name) {
			return "", errRelayPath
		}
		args = append(args, "./"+name)
	}
	return relayQuote(args...), nil
}

func relayCanDirect(ctx context.Context, run relayRunner, target relayEndpoint) bool {
	if run == nil {
		return false
	}
	command, err := target.command("true")
	if err != nil {
		return false
	}
	probe, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := run(probe, command+"\n")
		result <- err
	}()
	select {
	case err := <-result:
		return err == nil && probe.Err() == nil
	case <-probe.Done():
		return false
	}
}

const (
	relayTarFailed   = "__finallyshell_source_tar_failed__"
	relayStagePrefix = ".finallyshell-transfer-"
)

func relayPipeline(from string, names []string, stage string, target relayEndpoint) (string, error) {
	if !relayDirectory(stage) || !strings.HasPrefix(path.Base(stage), relayStagePrefix) {
		return "", errRelayPath
	}
	pack, err := relayTar(from, names, path.Base(stage))
	if err != nil {
		return "", errRelayPath
	}
	unpack := relayQuote("test", "-d", stage) + " && " + relayQuote("test", "!", "-L", stage) +
		" && umask '077' && exec " + relayQuote("tar", "--no-same-owner", "--no-same-permissions", "-C", stage, "-xf", "-")
	remote, err := target.command(relayQuote("sh", "-c", unpack))
	if err != nil {
		return "", err
	}
	return "{ " + pack + " || " + relayQuote("printf", "%s\\n", relayTarFailed) + " >&2; } | " + remote + "\n", nil
}

func relayPlan(names []string, existing map[string]bool, policy relayPolicy) []relayItem {
	reserved := make(map[string]bool, len(names))
	for _, name := range names {
		reserved[name] = true
	}
	var items []relayItem
	for _, name := range names {
		item := relayItem{source: name, dest: name}
		if existing[name] {
			switch policy {
			case relaySkip:
				continue
			case relayRename:
				ext := path.Ext(name)
				if ext == name {
					ext = ""
				}
				for number := 2; ; number++ {
					candidate := fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), number, ext)
					if !existing[candidate] && !reserved[candidate] {
						item.dest, reserved[candidate] = candidate, true
						break
					}
				}
			case relayOverwrite:
				item.backup = true
			}
		}
		items = append(items, item)
	}
	return items
}

func relayStream(ctx context.Context, dst io.Writer, src io.Reader, size int64, progress func(int64)) error {
	buffer := make([]byte, 64*1024)
	var copied int64
	for ctx.Err() == nil {
		read, err := src.Read(buffer)
		if read > 0 {
			if int64(read) > size-copied {
				return errRelaySize
			}
			written, writeErr := dst.Write(buffer[:read])
			copied += int64(written)
			progress(int64(written))
			if writeErr != nil {
				return writeErr
			}
			if written != read {
				return io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if copied != size {
				return errRelaySize
			}
			return nil
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
	return ctx.Err()
}
