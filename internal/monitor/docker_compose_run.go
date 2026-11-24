package monitor

import (
	"context"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

func RunComposeStream(ctx context.Context, client *ssh.Client, script string, progress func(string)) (string, error) {
	if client == nil {
		return "", errors.New("not connected")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	session.Stdin = strings.NewReader(script)
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	session.Stderr = &limitWriter{w: &stderr, n: 64 << 10}
	if err := session.Start("sh -s"); err != nil {
		return "", err
	}
	const outputLimit = 64 << 10
	tail := make([]byte, 0, outputLimit)
	chunk := make([]byte, 16<<10)
	var readErr error
	for {
		count, err := stdout.Read(chunk)
		if count > 0 {
			if progress != nil {
				progress(string(chunk[:count]))
			}
			tail = appendComposeTail(tail, chunk[:count], outputLimit)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	err = session.Wait()
	output := string(tail) + stderr.String()
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return output, &DockerExit{Status: exit.ExitStatus()}
	}
	return output, errors.Join(err, readErr)
}

func appendComposeTail(tail, chunk []byte, limit int) []byte {
	if len(chunk) >= limit {
		return append(tail[:0], chunk[len(chunk)-limit:]...)
	}
	if overflow := len(tail) + len(chunk) - limit; overflow > 0 {
		tail = tail[:copy(tail, tail[overflow:])]
	}
	return append(tail, chunk...)
}
