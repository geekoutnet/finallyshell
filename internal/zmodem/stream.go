package zmodem

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"sync"
	"time"
)

type Handle func(ctx context.Context, cancel context.CancelFunc, sending bool, transfer *Transfer) error

type chunk struct {
	data []byte
	err  error
}

type Stream struct {
	conn   io.ReadWriteCloser
	handle Handle
	life   context.Context
	stop   context.CancelFunc
	chunks chan chunk
	reader *bufio.Reader
	once   sync.Once
	err    error
	mu     sync.Mutex
	active bool
	feed   feed
	stripX bool
}

type feed struct {
	stream *Stream
	ctx    context.Context
	wait   time.Duration
	tail   []byte
	err    error
}

func Wrap(conn io.ReadWriteCloser, handle Handle) *Stream {
	life, stop := context.WithCancel(context.Background())
	stream := &Stream{conn: conn, handle: handle, life: life, stop: stop, chunks: make(chan chunk, 2)}
	stream.feed = feed{stream: stream, ctx: life}
	stream.reader = bufio.NewReaderSize(&stream.feed, 32<<10)
	go stream.readRaw()
	return stream
}

func (stream *Stream) readRaw() {
	for {
		if stream.life.Err() != nil {
			return
		}
		buf := make([]byte, 32<<10)
		count, err := stream.conn.Read(buf)
		if count == 0 && err == nil {
			continue
		}
		select {
		case stream.chunks <- chunk{buf[:count], err}:
		case <-stream.life.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (source *feed) Read(buf []byte) (int, error) {
	if err := source.ctx.Err(); err != nil {
		return 0, err
	}
	if len(source.tail) == 0 && source.err == nil {
		var alarm <-chan time.Time
		if source.wait > 0 {
			timer := time.NewTimer(source.wait)
			defer timer.Stop()
			alarm = timer.C
		}
		select {
		case next := <-source.stream.chunks:
			source.tail, source.err = next.data, next.err
		case <-source.ctx.Done():
			return 0, source.ctx.Err()
		case <-alarm:
			return 0, ErrTimeout
		}
	}
	if len(source.tail) == 0 {
		return 0, source.err
	}
	count := copy(buf, source.tail)
	source.tail = source.tail[count:]
	return count, nil
}

var marker = []byte("**\x18B0")

func (stream *Stream) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	reader := stream.reader
	for {
		first, err := reader.Peek(1)
		if err != nil {
			return 0, err
		}
		if stream.stripX {
			stream.stripX = false
			if first[0]&0x7f == 0x11 {
				_, _ = reader.Discard(1)
				continue
			}
		}
		available, _ := reader.Peek(reader.Buffered())
		index := bytes.IndexByte(available, '*')
		if index < 0 {
			return reader.Read(buf[:min(len(buf), len(available))])
		}
		if index > 0 {
			return reader.Read(buf[:min(index, len(buf))])
		}
		if start, ok := stream.detect(); ok {
			stream.run(start)
			continue
		}
		return reader.Read(buf[:1])
	}
}

func (stream *Stream) detect() (header, bool) {
	reader := stream.reader
	stream.feed.wait = 80 * time.Millisecond
	defer func() { stream.feed.wait = 0 }()
	for size := 2; size <= len(marker); size++ {
		prefix, err := reader.Peek(size)
		if err != nil || !bytes.Equal(prefix, marker[:size]) {
			return header{}, false
		}
	}
	stream.feed.wait = 2 * time.Second
	for size := len(marker) + 1; size <= 18; size++ {
		prefix, err := reader.Peek(size)
		if err != nil || hexDigit(prefix[size-1]) > 15 || size == 6 && prefix[size-1] != '0' && prefix[size-1] != '1' {
			return header{}, false
		}
	}
	frame, err := reader.Peek(20)
	if err != nil {
		return header{}, false
	}
	start, ok := startValue(frame)
	if ok {
		_, _ = reader.Discard(20)
	}
	return start, ok
}

func (stream *Stream) run(start header) {
	stream.stripX = true
	stream.mu.Lock()
	stream.active = true
	stream.mu.Unlock()
	defer func() {
		stream.mu.Lock()
		stream.active = false
		stream.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(stream.life)
	defer cancel()
	stream.feed.ctx = ctx
	stream.feed.wait = 15 * time.Second
	err := stream.handle(ctx, cancel, start.kind == zrinit, &Transfer{stream.reader, stream.conn, start, ctx})
	stream.feed.ctx, stream.feed.wait = stream.life, 0
	if err != nil && stream.life.Err() == nil && err != errSkipped {
		_, _ = io.WriteString(stream.conn, CancelSequence)
		stream.discardCancellation()
	}
}

func (stream *Stream) discardCancellation() {
	ctx, cancel := context.WithTimeout(stream.life, time.Second)
	defer cancel()
	stream.feed.ctx, stream.feed.wait = ctx, 150*time.Millisecond
	defer func() { stream.feed.ctx, stream.feed.wait = stream.life, 0 }()
	for ctx.Err() == nil {
		if _, err := stream.reader.ReadByte(); err != nil {
			break
		}
	}
}

func (stream *Stream) Write(buf []byte) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.active {
		return len(buf), nil
	}
	return stream.conn.Write(buf)
}

func (stream *Stream) Resize(cols, rows int) error {
	if conn, ok := stream.conn.(interface{ Resize(int, int) error }); ok {
		return conn.Resize(cols, rows)
	}
	return nil
}

func (stream *Stream) Close() error {
	stream.once.Do(func() {
		stream.stop()
		stream.err = stream.conn.Close()
	})
	return stream.err
}
