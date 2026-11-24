package monitor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

func RunImageProgress(ctx context.Context, client *ssh.Client, script string, progress func(string)) (string, error) {
	if progress == nil {
		return RunDocker(ctx, client, script)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if client == nil {
		return "", errors.New("not connected")
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	output := &imageStream{progress: progress}
	session.Stdin = strings.NewReader(script)
	session.Stdout, session.Stderr = output, output
	stop := context.AfterFunc(ctx, func() {
		timer := time.AfterFunc(time.Second, func() { session.Close() })
		defer timer.Stop()
		session.Signal(ssh.SIGINT)
		session.Close()
	})
	defer stop()
	err = session.Run("sh -s")
	output.lock.Lock()
	text := string(output.tail)
	output.lock.Unlock()
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return text, &DockerExit{Status: exit.ExitStatus()}
	}
	return text, err
}

type imageStream struct {
	lock     sync.Mutex
	tail     []byte
	progress func(string)
}

func (stream *imageStream) Write(chunk []byte) (int, error) {
	stream.lock.Lock()
	defer stream.lock.Unlock()
	const limit = 64 << 10
	if len(chunk) >= limit {
		stream.tail = append(stream.tail[:0], chunk[len(chunk)-limit:]...)
	} else {
		if overflow := len(stream.tail) + len(chunk) - limit; overflow > 0 {
			copy(stream.tail, stream.tail[overflow:])
			stream.tail = stream.tail[:len(stream.tail)-overflow]
		}
		stream.tail = append(stream.tail, chunk...)
	}
	stream.progress(string(chunk))
	return len(chunk), nil
}
