package monitor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const privilegeHandshakeTimeout = 15 * time.Second

func SSHPrivilegeRunner(client *ssh.Client) PrivilegeRunner {
	return func(ctx context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
		if client == nil {
			return PrivilegeResult{}, errors.New("not connected")
		}
		if request.Probe {
			out, stderr, err := RunScript(ctx, client, "exec "+request.Command+"\n")
			return PrivilegeResult{Stdout: out, Stderr: stderr}, err
		}
		return runPrivilegeSSH(ctx, client, request)
	}
}

func runPrivilegeSSH(ctx context.Context, client *ssh.Client, request PrivilegeRequest) (PrivilegeResult, error) {
	session, err := client.NewSession()
	if err != nil {
		return PrivilegeResult{}, err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() {
		if request.interrupt {
			deadline := time.AfterFunc(time.Second, func() { session.Close() })
			defer deadline.Stop()
			_ = session.Signal(ssh.SIGINT)
		}
		session.Close()
	})
	defer stop()
	stream := newPrivilegeStream(request)
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
	if err := session.Start(request.Command); err != nil {
		return PrivilegeResult{}, err
	}
	inputCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputErr := make(chan error, 1)
	password := bytes.Clone(request.Password)
	go func() {
		err := stream.send(inputCtx, stdin, password, privilegeHandshakeTimeout)
		if inputCtx.Err() != nil {
			err = nil
		}
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
		if sendErr != nil && (err == nil || privilegeAuthError(sendErr)) {
			err = sendErr
		}
	default:
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return result, err
}

type privilegeStream struct {
	request  PrivilegeRequest
	mu       sync.Mutex
	started  bool
	prompted bool
	prompt   chan struct{}
	retry    chan struct{}
	rejected chan error
	ready    chan struct{}
	before   [2][]byte
	stdout   privilegeOutput
	stderr   privilegeOutput
}

func newPrivilegeStream(request PrivilegeRequest) *privilegeStream {
	stream := &privilegeStream{request: request, prompt: make(chan struct{}), retry: make(chan struct{}, 1), ready: make(chan struct{}), rejected: make(chan error, 1)}
	stream.stdout = privilegeOutput{secret: request.Password, limit: procOutputLimit, progress: request.progress}
	stream.stderr = privilegeOutput{secret: request.Password, limit: 64 << 10}
	return stream
}

type privilegeStreamWriter struct {
	stream *privilegeStream
	stdout bool
}

func (stream *privilegeStream) writer(stdout bool) privilegeStreamWriter {
	return privilegeStreamWriter{stream, stdout}
}

func (writer privilegeStreamWriter) Write(chunk []byte) (int, error) {
	writer.stream.write(writer.stdout, chunk)
	return len(chunk), nil
}

func (stream *privilegeStream) write(stdout bool, chunk []byte) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.started {
		index := 1
		if stdout {
			index = 0
		}
		prefix := append(stream.before[index], chunk...)
		if stream.request.prompt != "" && stdout == stream.request.PTY {
			prompt := []byte(stream.request.prompt)
			offset := len(stream.before[index])
			for start := max(0, offset-len(prompt)+1); ; {
				found := bytes.Index(prefix[start:], prompt)
				if found < 0 {
					break
				}
				start += found
				atLineStart := len(bytes.TrimSpace(prefix[bytes.LastIndexByte(prefix[:start], '\n')+1:start])) == 0
				atPromptEnd := len(bytes.TrimSpace(prefix[start+len(prompt):])) == 0
				if !stream.request.PTY || atLineStart && atPromptEnd {
					if !stream.prompted {
						stream.prompted = true
						close(stream.prompt)
					} else {
						select {
						case stream.retry <- struct{}{}:
						default:
						}
					}
				}
				start += len(prompt)
			}
		}
		stream.before[index] = prefix
		if stdout {
			marker := []byte(stream.request.ready)
			if found := bytes.Index(prefix, marker); found >= 0 {
				end := found + len(marker)
				if newline := bytes.IndexByte(prefix[end:], '\n'); newline >= 0 && len(bytes.TrimSpace(prefix[end:end+newline])) == 0 {
					stream.started = true
					chunk = prefix[end+newline+1:]
					stderr := stream.before[1]
					if stream.request.method == PrivilegeSudo && stream.request.prompt != "" {
						stripped := bytes.ReplaceAll(stderr, []byte(stream.request.prompt+" "), nil)
						defer clear(stripped)
						stderr = stripped
					}
					stream.stderr.write(stderr)
					close(stream.ready)
				}
			}
		}
		if !stream.started {
			if stream.request.method == PrivilegeSu && bytes.Contains(prefix, []byte("su: Authentication failure")) {
				select {
				case stream.rejected <- ErrPrivilegeWrongPassword:
				default:
				}
			}
			if len(prefix) > 64<<10 {
				remaining := copy(prefix, prefix[len(prefix)-(64<<10):])
				clear(prefix[remaining:])
				stream.before[index] = prefix[:remaining]
			}
			return
		}
	}
	if stdout {
		stream.stdout.write(chunk)
	} else {
		stream.stderr.write(chunk)
	}
}

func (stream *privilegeStream) result() PrivilegeResult {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	defer func() {
		for _, data := range stream.before {
			clear(data[:cap(data)])
		}
	}()
	if !stream.started {
		message := append(bytes.Clone(stream.before[1]), '\n')
		message = append(message, stream.before[0]...)
		defer clear(message)
		if stream.request.prompt != "" {
			stripped := bytes.ReplaceAll(message, []byte(stream.request.prompt), []byte("\n"))
			defer clear(stripped)
			message = stripped
		}
		rejection := privilegeRejection(stream.request.method, string(message))
		if rejection == nil && stream.prompted && stream.request.method == PrivilegeSudo {
			rejection = ErrPrivilegeDenied
		}
		return PrivilegeResult{Rejected: rejection}
	}
	return PrivilegeResult{Started: true, Authenticated: stream.prompted, Stdout: stream.stdout.finish(), Stderr: stream.stderr.finish()}
}

func (stream *privilegeStream) send(ctx context.Context, input io.WriteCloser, password []byte, timeout time.Duration) error {
	defer clear(password)
	defer input.Close()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-stream.ready:
	case err := <-stream.rejected:
		return err
	case <-stream.prompt:
		if len(password) == 0 {
			return ErrPrivilegeDenied
		}
		line := append(password[:len(password):len(password)], '\n')
		_, err := input.Write(line)
		clear(line)
		if err != nil {
			return err
		}
		select {
		case <-stream.ready:
		case err := <-stream.rejected:
			return err
		case <-stream.retry:
			return ErrPrivilegeWrongPassword
		case <-timer.C:
			return ErrPrivilegeDenied
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-timer.C:
		return ErrPrivilegeDenied
	case <-ctx.Done():
		return ctx.Err()
	}
	clear(password)
	source := stream.request.Stdin
	if stream.request.script != nil {
		source = stream.request.script
	}
	if source == nil {
		return nil
	}
	if stream.request.frameInput {
		return writePrivilegeFrames(ctx, input, source)
	}
	_, err := io.Copy(input, source)
	return err
}

func writePrivilegeFrames(ctx context.Context, dst io.Writer, src io.Reader) error {
	// Use short canonical TTY lines so payload bytes cannot be interpreted
	// as terminal controls. POSIX printf decodes them into the command's stdin.
	var input [128]byte
	var frame [5*len(input) + 1]byte
	defer clear(input[:])
	defer clear(frame[:])
	for ctx.Err() == nil {
		count, err := src.Read(input[:])
		if count > 0 {
			for index, value := range input[:count] {
				copy(frame[index*5:], []byte{'\\', '0', '0' + value>>6, '0' + (value>>3)&7, '0' + value&7})
			}
			frame[count*5] = '\n'
			if _, err := dst.Write(frame[:count*5+1]); err != nil {
				return err
			}
		}
		if err == io.EOF {
			_, err = io.WriteString(dst, ".\n")
			return err
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

type privilegeOutput struct {
	secret, pending []byte
	saved           bytes.Buffer
	limit           int
	progress        func(string)
}

func (output *privilegeOutput) emit(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if output.progress != nil {
		output.progress(string(chunk))
	}
	if remaining := output.limit - output.saved.Len(); remaining > 0 {
		output.saved.Write(chunk[:min(remaining, len(chunk))])
	}
}

func (output *privilegeOutput) write(chunk []byte) {
	if len(output.secret) == 0 {
		output.emit(chunk)
		return
	}
	data := append(output.pending, chunk...)
	defer clear(data)
	for {
		found := bytes.Index(data, output.secret)
		if found < 0 {
			break
		}
		output.emit(data[:found])
		output.emit(privilegeReplacement(output.secret))
		data = data[found+len(output.secret):]
	}
	keep := min(len(data), len(output.secret)-1)
	for keep > 0 && !bytes.HasSuffix(data, output.secret[:keep]) {
		keep--
	}
	output.emit(data[:len(data)-keep])
	output.pending = bytes.Clone(data[len(data)-keep:])
}

func (output *privilegeOutput) finish() string {
	output.emit(output.pending)
	clear(output.pending)
	output.pending = nil
	return output.saved.String()
}
