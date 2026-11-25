package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"finallyshell/internal/remotefs"
	"golang.org/x/crypto/ssh"
)

const desktopFilesMountCommand = `LC_ALL=C 'df' '-kPT'`
const desktopFilesSearchLimit = 300

type desktopFilesDrive struct {
	device, kind, mount string
	total, free         int64
	used                int
}

func desktopFilesParseMounts(text string) []desktopFilesDrive {
	var drives []desktopFilesDrive
	seen := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		kind := fields[1]
		switch kind {
		case "tmpfs", "devtmpfs", "ramfs", "overlay", "proc", "squashfs", "sysfs", "cgroup", "cgroup2", "devpts", "autofs", "tracefs", "debugfs", "mqueue", "securityfs", "pstore", "fusectl":
			continue
		}
		capacity := fields[5]
		remaining := strings.TrimSpace(line)
		for _, field := range fields[:6] {
			remaining = strings.TrimLeftFunc(strings.TrimPrefix(remaining, field), unicode.IsSpace)
		}
		mount := strings.TrimSuffix(remaining, "\r")
		mount = strings.NewReplacer(`\040`, " ", `\134`, `\`).Replace(mount)
		total, totalErr := strconv.ParseInt(fields[2], 10, 64)
		usedBlocks, usedErr := strconv.ParseInt(fields[3], 10, 64)
		free, freeErr := strconv.ParseInt(fields[4], 10, 64)
		used, percentErr := strconv.Atoi(strings.TrimSuffix(capacity, "%"))
		if !strings.HasSuffix(capacity, "%") || totalErr != nil || usedErr != nil || freeErr != nil || percentErr != nil || total <= 0 ||
			total > math.MaxInt64/1024 || free < 0 || free > total || usedBlocks < 0 || used < 0 || !desktopFilesPath(mount) || seen[mount] {
			continue
		}
		seen[mount] = true
		drives = append(drives, desktopFilesDrive{fields[0], kind, mount, total * 1024, free * 1024, min(used, 100)})
	}
	slices.SortFunc(drives, func(first, second desktopFilesDrive) int { return strings.Compare(first.mount, second.mount) })
	return drives
}

func desktopFilesPath(target string) bool {
	return path.IsAbs(target) && path.Clean(target) == target && !strings.ContainsFunc(target, unicode.IsControl)
}

func desktopFilesName(name string) bool {
	return remotefs.ValidName(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

func desktopFilesURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsFunc(raw, unicode.IsControl) || strings.Contains(raw, "\\") {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "ftp"
}

func desktopFilesCopyCommand(from, to string, cut, replace bool) string {
	flags := "-nT"
	if replace {
		flags = "-T"
	}
	if cut {
		return relayQuote("mv", flags, "--", from, to)
	}
	return relayQuote("cp", "-a", flags, "--", from, to)
}

func desktopFilesModeCommand(target, mode string, recursive bool) string {
	args := []string{"chmod", "--", mode}
	if recursive {
		return relayQuote("find", "-P", target, "!", "-type", "l", "-exec", args[0], args[1], args[2], "{}", "+")
	}
	return relayQuote(append(args, target)...)
}

func desktopFilesOwnerCommand(target, owner string, recursive bool) string {
	args := []string{"chown", "-h"}
	if recursive {
		args = append(args, "-R", "-P")
	}
	return relayQuote(append(args, "--", owner, target)...)
}

func desktopFilesSearchCommand(dir, query string) string {
	escape := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`)
	return desktopFilesTool("find") + desktopFilesTool("head") +
		relayQuote("find", "-H", dir, "-mindepth", "1", "-iname", "*"+escape.Replace(query)+"*", "-printf", `%y\0%s\0%T@\0%m\0%U\0%G\0%p\0`) +
		" | " + relayQuote("head", "-z", "-n", strconv.Itoa(7*(desktopFilesSearchLimit+1)))
}

func desktopFilesTool(tool string) string {
	return "command -v " + relayQuote(tool) + " >/dev/null 2>&1 || { printf '%s\\n' " + relayQuote("desktop-files-missing:"+tool) + " >&2; exit 127; }\n"
}

func desktopFilesFetchCommand(raw, staged string) string {
	return "if command -v 'curl' >/dev/null 2>&1; then\nexec " +
		relayQuote("curl", "-q", "--fail", "--show-error", "--location", "--max-redirs", "10", "--proto", "=http,https,ftp", "--proto-redir", "=http,https,ftp", "--globoff", "--progress-bar", "--output", staged, "--", raw) + " 2>&1" +
		"\nelse\n" + desktopFilesTool("wget") + "exec " + relayQuote("wget", "--no-config", "--max-redirect=10", "--progress=dot:mega", "-O", staged, "--", raw) + " 2>&1\nfi"
}

func desktopFilesArchiveKind(target string) string {
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tar", ".zip", ".gz"} {
		if strings.HasSuffix(strings.ToLower(target), suffix) {
			return suffix
		}
	}
	return ""
}

func desktopFilesCompressCommand(kind, dir, target string, names []string) string {
	if kind == ".zip" {
		return desktopFilesTool("zip") + "unset ZIPOPT\ncd " + relayQuote(dir) + " || exit\nexec " +
			relayQuote(append([]string{"zip", "-ry", target, "--"}, names...)...)
	}
	return desktopFilesTool("tar") + "unset TAR_OPTIONS\nexec " + relayQuote(append([]string{"tar", "-czvf", target, "-C", dir, "--"}, names...)...)
}

func desktopFilesExtractCommand(target, stage string) string {
	switch desktopFilesArchiveKind(target) {
	case ".zip":
		return desktopFilesTool("unzip") + "unset UNZIP UNZIPOPT\nexec " + relayQuote("unzip", "-n", target, "-d", stage)
	case ".gz":
		out := path.Join(stage, path.Base(target)[:len(path.Base(target))-len(".gz")])
		return desktopFilesTool("gzip") + "exec " + relayQuote("gzip", "-dc", "--", target) + " > " + relayQuote(out)
	default:
		return desktopFilesTool("tar") + "unset TAR_OPTIONS\nexec " + relayQuote("tar", "--no-same-owner", "--no-same-permissions", "--keep-old-files", "-xvf", target, "-C", stage)
	}
}

func desktopFilesCommandError(stderr string, err error) error {
	if stderr = strings.TrimSpace(stderr); err != nil && stderr != "" {
		return fmt.Errorf("%s: %w", stderr, err)
	}
	return err
}

type desktopFilesOutput struct {
	bytes.Buffer
	limit int
	emit  func(string)
}

func (output *desktopFilesOutput) Write(chunk []byte) (int, error) {
	if output.emit != nil {
		output.emit(string(chunk))
	}
	if remaining := output.limit - output.Len(); remaining > 0 {
		_, _ = output.Buffer.Write(chunk[:min(remaining, len(chunk))])
	}
	return len(chunk), nil
}

func desktopFilesSSHExec(ctx context.Context, client *ssh.Client, script string, report func(string)) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	channel, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() {
		deadline := time.AfterFunc(time.Second, func() { channel.Close() })
		defer deadline.Stop()
		_ = channel.Signal(ssh.SIGTERM)
		channel.Close()
	})
	defer stop()
	stdout := &desktopFilesOutput{limit: 2 << 20, emit: report}
	stderr := &desktopFilesOutput{limit: 64 << 10}
	channel.Stdin = strings.NewReader(script)
	channel.Stdout, channel.Stderr = io.Writer(stdout), io.Writer(stderr)
	err = channel.Run("sh -s")
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}
