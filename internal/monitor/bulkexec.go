package monitor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const BulkOutputLimit = 1 << 20

const BulkRootStderrLimit = 64 << 10

const BulkShell = "'sh' '-s'"

type bulkCapture struct {
	mu        sync.Mutex
	streams   [2]bytes.Buffer
	left      int
	truncated bool
}

type bulkStream struct {
	capture *bulkCapture
	index   int
}

func (stream bulkStream) Write(chunk []byte) (int, error) {
	capture := stream.capture
	capture.mu.Lock()
	defer capture.mu.Unlock()
	size := min(len(chunk), capture.left)
	capture.streams[stream.index].Write(chunk[:size])
	capture.left -= size
	capture.truncated = capture.truncated || size < len(chunk)
	return len(chunk), nil
}

func (capture *bulkCapture) result() (string, string, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.streams[0].String(), capture.streams[1].String(), capture.truncated
}

func RunBulkScript(ctx context.Context, client *ssh.Client, script string) (string, string, bool, error) {
	if client == nil {
		return "", "", false, errors.New("not connected")
	}
	capture := &bulkCapture{left: BulkOutputLimit}
	done := make(chan error, 1)
	go func() {
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		session, err := client.NewSession()
		if err == nil {
			defer session.Close()
			stop := context.AfterFunc(ctx, func() { session.Close() })
			defer stop()
			if err = ctx.Err(); err == nil {
				session.Stdin = strings.NewReader(script)
				session.Stdout = bulkStream{capture, 0}
				session.Stderr = bulkStream{capture, 1}
				err = session.Run(BulkShell)
			}
		}
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	stdout, stderr, truncated := capture.result()
	return stdout, stderr, truncated, err
}

func BulkPrivilegeRunner(client *ssh.Client) PrivilegeRunner {
	return func(ctx context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
		if client == nil {
			return PrivilegeResult{}, errors.New("not connected")
		}
		done := make(chan struct {
			result PrivilegeResult
			err    error
		}, 1)
		request.Password = bytes.Clone(request.Password)
		go func() {
			defer clear(request.Password)
			var result PrivilegeResult
			var err error
			if request.Probe {
				result.Stdout, result.Stderr, _, err = RunBulkScript(ctx, client, "exec "+request.Command+"\n")
			} else {
				result, err = runBulkPrivilegeSSH(ctx, client, request)
			}
			done <- struct {
				result PrivilegeResult
				err    error
			}{result, err}
		}()
		select {
		case reply := <-done:
			return reply.result, reply.err
		case <-ctx.Done():
			return PrivilegeResult{}, ctx.Err()
		}
	}
}

func runBulkPrivilegeSSH(ctx context.Context, client *ssh.Client, request PrivilegeRequest) (PrivilegeResult, error) {
	if err := ctx.Err(); err != nil {
		return PrivilegeResult{}, err
	}
	session, err := client.NewSession()
	if err != nil {
		return PrivilegeResult{}, err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return PrivilegeResult{}, err
	}
	stream := newPrivilegeStream(request)
	stream.stdout.limit = BulkOutputLimit - BulkRootStderrLimit
	stream.stderr.limit = BulkRootStderrLimit
	session.Stdout, session.Stderr = stream.writer(true), stream.writer(false)
	stdin, err := session.StdinPipe()
	if err != nil {
		return PrivilegeResult{}, err
	}
	if request.PTY {
		err = session.RequestPty("dumb", 24, 80, ssh.TerminalModes{
			ssh.ECHO: 0, ssh.ECHONL: 0, ssh.ISIG: 0, ssh.IEXTEN: 0, ssh.IXON: 0,
			ssh.IGNCR: 0, ssh.ICRNL: 0, ssh.INLCR: 0, ssh.ICANON: 1,
		})
		if err != nil {
			return PrivilegeResult{}, ErrPrivilegeDenied
		}
	}
	if err := ctx.Err(); err != nil {
		return PrivilegeResult{}, err
	}
	if err := session.Start(request.Command); err != nil {
		return PrivilegeResult{}, err
	}
	inputCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputErr := make(chan error, 1)
	password := bytes.Clone(request.Password)
	go func() {
		err := stream.send(inputCtx, stdin, password, privilegeHandshakeTimeout)
		inputErr <- err
		if err != nil {
			session.Close()
		}
	}()
	err = session.Wait()
	cancel()
	result := stream.result()
	select {
	case sendErr := <-inputErr:
		if sendErr != nil && !errors.Is(sendErr, context.Canceled) && (err == nil || privilegeAuthError(sendErr)) {
			err = sendErr
		}
	default:
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return result, err
}
