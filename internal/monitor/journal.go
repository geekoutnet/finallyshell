package monitor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

type JournalSource struct {
	Unit, File, Priority, Since string
	Lines                       int
}

func ValidLogPath(file string) bool {
	if len(file) <= 1 || len(file) > 4096 || !strings.HasPrefix(file, "/") || path.Clean(file) != file || strings.ContainsAny(file, "\x00\r\n") {
		return false
	}
	for part := range strings.SplitSeq(strings.ToLower(file), "/") {
		switch part {
		case ".ssh", ".gnupg", "shadow", "gshadow", "master.passwd", "privkey.pem", ".netrc", ".env", "secrets":
			return false
		}
		if strings.HasSuffix(part, ".key") || strings.HasSuffix(part, "_private") || strings.HasPrefix(part, "id_") {
			return false
		}
	}
	return true
}

func JournalCommand(source JournalSource) (string, error) {
	if source.Lines < 1 || source.Lines > 20000 {
		return "", errors.New("invalid line count")
	}
	lines := strconv.Itoa(source.Lines)
	if source.File != "" {
		if source.Unit != "" || !ValidLogPath(source.File) {
			return "", errors.New("invalid log path")
		}
		return SystemCommand("tail", "-n", lines, "-F", "--", source.File), nil
	}
	args := []string{"env", "LC_ALL=C", "journalctl", "-o", "short-iso", "--no-pager", "-n", lines, "-f"}
	if source.Unit != "" {
		if !ValidService(source.Unit) {
			return "", errors.New("invalid journal unit")
		}
		args = append(args, "-u", source.Unit)
	}
	switch source.Priority {
	case "err", "warning", "info":
		args = append(args, "-p", source.Priority)
	case "":
	default:
		return "", errors.New("invalid journal priority")
	}
	switch source.Since {
	case "-10min", "-1h", "today":
		args = append(args, "--since", source.Since)
	case "boot":
		args = append(args, "--boot")
	case "":
	default:
		return "", errors.New("invalid journal time range")
	}
	return SystemCommand(args...), nil
}

const CommonLogFiles = `for file in /var/log/syslog /var/log/messages /var/log/auth.log /var/log/secure /var/log/kern.log /var/log/nginx/*.log /var/log/apache2/*.log /var/log/httpd/*log /var/log/mysql/*log /var/log/mysqld.log; do
    if [ -f "$file" ]; then 'printf' '%s\n' "$file"; fi
done`

func StreamSystem(ctx context.Context, client *ssh.Client, script string, report func(string)) (string, string, error) {
	if client == nil {
		return "", "", errors.New("not connected")
	}
	session, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	session.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	session.Stderr = &limitWriter{w: &stderr, n: 64 << 10}
	session.Stdout = scriptProgress(report)
	err = session.Run("sh -s")
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return "", stderr.String(), err
}

func RunSystemInput(ctx context.Context, client *ssh.Client, script string, input io.Reader, report func(string)) (string, string, error) {
	if client == nil {
		return "", "", errors.New("not connected")
	}
	session, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	session.Stdin = input
	var stdout, stderr bytes.Buffer
	session.Stdout = &limitWriter{w: &stdout, n: 256 << 10}
	if report != nil {
		session.Stdout = io.MultiWriter(session.Stdout, scriptProgress(report))
	}
	session.Stderr = &limitWriter{w: &stderr, n: 64 << 10}
	err = session.Run(SystemCommand("sh", "-c", script))
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}
