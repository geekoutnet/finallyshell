package app

import (
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type terminalRead struct {
	data []byte
	err  error
}

type terminalStream struct {
	conn   io.ReadWriteCloser
	reads  chan terminalRead
	closed chan struct{}
	once   sync.Once
	err    error

	rules     atomic.Pointer[[]compiledHighlight]
	loaded    *[]compiledHighlight
	highlight *highlighter
	pending   []byte
	readErr   error

	mu      sync.Mutex
	rec     *recording
	onError func()

	asking bool
}

func newTerminalStream(conn io.ReadWriteCloser, rules []compiledHighlight, onError func()) *terminalStream {
	stream := &terminalStream{conn: conn, reads: make(chan terminalRead, 2), closed: make(chan struct{}), onError: onError}
	stream.rules.Store(&rules)
	go stream.read()
	return stream
}

func (stream *terminalStream) read() {
	for {
		if stream.isClosed() {
			return
		}
		data := make([]byte, 32<<10)
		count, err := stream.conn.Read(data)
		if count == 0 && err == nil {
			continue
		}
		select {
		case stream.reads <- terminalRead{data[:count], err}:
		case <-stream.closed:
			return
		}
		if err != nil {
			return
		}
	}
}

func (stream *terminalStream) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	for len(stream.pending) == 0 && stream.readErr == nil {
		var idle <-chan time.Time
		var timer *time.Timer
		if stream.highlight != nil && len(stream.highlight.text) != 0 {
			timer = time.NewTimer(16 * time.Millisecond)
			idle = timer.C
		}
		select {
		case <-stream.closed:
			stream.readErr = io.EOF
		case <-idle:
			stream.pending = stream.highlight.process(nil, true)
			if len(stream.pending) == 0 && len(stream.highlight.text) != 0 {
				stream.pending = append([]byte(nil), stream.highlight.text...)
				stream.highlight.text = stream.highlight.text[:0]
			}
		case read := <-stream.reads:
			stream.consume(read)
		}
		if timer != nil {
			timer.Stop()
		}
	}
	count := copy(buf, stream.pending)
	stream.pending = stream.pending[count:]
	if count != 0 {
		return count, nil
	}
	return 0, stream.readErr
}

func (stream *terminalStream) consume(read terminalRead) {
	stream.capture(func(rec *recording, now time.Time) error { return rec.output(read.data, now) })
	stream.readErr = read.err
	if rules := stream.rules.Load(); rules != stream.loaded {
		if stream.highlight == nil {
			stream.highlight = newHighlighter(*rules)
		} else {
			stream.highlight.rules = *rules
		}
		stream.loaded = rules
	}
	stream.pending = append(stream.pending, stream.highlight.process(read.data, read.err != nil)...)
	if read.err != nil {
		stream.pending = append(stream.pending, stream.highlight.text...)
		stream.highlight.text = stream.highlight.text[:0]
	}
}

func (stream *terminalStream) Write(data []byte) (int, error) { return stream.conn.Write(data) }

func (stream *terminalStream) Resize(cols, rows int) error {
	stream.capture(func(rec *recording, now time.Time) error {
		if rec.width == cols && rec.height == rows {
			return nil
		}
		err := rec.write("r", strconv.Itoa(cols)+"x"+strconv.Itoa(rows), now)
		rec.width, rec.height = cols, rows
		return err
	})
	if sized, ok := stream.conn.(interface{ Resize(int, int) error }); ok {
		return sized.Resize(cols, rows)
	}
	return nil
}

func (stream *terminalStream) isClosed() bool {
	select {
	case <-stream.closed:
		return true
	default:
		return false
	}
}

func (stream *terminalStream) Close() error {
	stream.once.Do(func() {
		close(stream.closed)
		stream.stopRecording()
		stream.err = stream.conn.Close()
	})
	return stream.err
}

func (stream *terminalStream) recording() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.rec != nil
}

func (stream *terminalStream) setRecording(rec *recording) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.isClosed() || stream.rec != nil {
		_ = rec.Close()
		return
	}
	stream.rec = rec
}

func (stream *terminalStream) stopRecording() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.rec != nil {
		if err := stream.rec.Close(); err != nil {
			stream.reportRecordingError()
		}
		stream.rec = nil
	}
}

func (stream *terminalStream) capture(write func(*recording, time.Time) error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.rec != nil && write(stream.rec, time.Now()) != nil {
		_ = stream.rec.Close()
		stream.rec = nil
		stream.reportRecordingError()
	}
}

func (stream *terminalStream) reportRecordingError() {
	if stream.onError != nil {
		stream.onError()
	}
}
