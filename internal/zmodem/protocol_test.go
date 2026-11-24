package zmodem

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type damagedLink struct {
	io.Writer
	damageNext bool
	damaged    bool
}

func (link *damagedLink) Write(payload []byte) (int, error) {
	if !link.damaged && link.damageNext {
		link.damaged = true
		payload = bytes.Clone(payload)
		payload[0] = 'x'
	}
	link.damageNext = bytes.HasPrefix(payload, []byte{'*', zdle, bin32, zdle, zdata ^ 0x40})
	return link.Writer.Write(payload)
}

func TestZmodemLoopback(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	name := "sample-传输.bin"
	files := []string{filepath.Join(source, name), filepath.Join(source, "empty.txt")}
	payload := bytes.Repeat([]byte("payload\x00\x18\xff\x11\x13\r\n"), 350)
	for index, file := range files {
		if err := os.WriteFile(file, [][]byte{payload, nil}[index], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dest, name), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendSide, receiveSide := net.Pipe()
	defer sendSide.Close()
	defer receiveSide.Close()
	for _, conn := range []net.Conn{sendSide, receiveSide} {
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	result := make(chan error, 1)
	go func() {
		defer receiveSide.Close()
		recv := Transfer{reader: bufio.NewReader(receiveSide), writer: receiveSide, start: header{kind: zrqinit}}
		result <- recv.Receive(dest, nil, nil)
	}()
	reader := bufio.NewReader(sendSide)
	ready, err := readHeader(reader)
	if err != nil {
		t.Fatal(err)
	}
	link := &damagedLink{Writer: sendSide}
	sender := Transfer{reader: reader, writer: link, start: ready}
	if err := sender.Send(files, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !link.damaged {
		t.Fatal("test did not inject a data error")
	}
	for file, want := range map[string][]byte{name: []byte("original"), "sample-传输 (1).bin": payload, "empty.txt": nil} {
		got, err := os.ReadFile(filepath.Join(dest, file))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %d bytes, %v", file, len(got), err)
		}
	}
	if temps, _ := filepath.Glob(filepath.Join(dest, "*.part")); len(temps) != 0 {
		t.Fatalf("left partial files: %v", temps)
	}
}

func TestZmodemNames(t *testing.T) {
	for _, name := range []string{"../tmp", "a/b", `..\temp`, "file:stream", "AUX.txt", "LPT²", "x\n", "x.", " x", ".", "..", "\xff"} {
		if _, _, _, err := parseMetadata([]byte(name + "\x001 2\x00")); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	name, size, stamp, err := parseMetadata([]byte("sample 文件.txt\x0014 1234 0\x00"))
	if name != "sample 文件.txt" || size != 14 || stamp.Unix() != 668 || err != nil {
		t.Fatalf("%q %d %v %v", name, size, stamp, err)
	}
}

type fakeConn struct {
	io.Reader
	bytes.Buffer
}

func (conn *fakeConn) Read(buf []byte) (int, error) { return conn.Reader.Read(buf) }
func (conn *fakeConn) Close() error                 { return nil }

type singleBytes struct{ io.Reader }

func (reader singleBytes) Read(buf []byte) (int, error) { return reader.Reader.Read(buf[:1]) }

func TestZmodemDetection(t *testing.T) {
	plain := "mark**\x18B00000000000001\r\n; **\x18B00not a handshake; ** end."
	valid := "**\x18B0100000023be50\r\x8a\x11"
	conn := &fakeConn{Reader: singleBytes{strings.NewReader(plain + valid + "prompt$ ")}}
	var offers int
	stream := Wrap(conn, func(_ context.Context, _ context.CancelFunc, sending bool, transfer *Transfer) error {
		offers++
		if !sending || transfer.start.value != 0x23000000 {
			t.Errorf("wrong offer: %t %+v", sending, transfer.start)
		}
		return nil
	})
	defer stream.Close()
	out, err := io.ReadAll(stream)
	if err != nil || offers != 1 || string(out) != plain+"prompt$ " {
		t.Fatalf("%d offers, %q, %v", offers, out, err)
	}
}

func TestZmodemCancel(t *testing.T) {
	conn := &fakeConn{Reader: strings.NewReader("**\x18B00000000000000\r\n" + CancelSequence)}
	stream := Wrap(conn, func(ctx context.Context, cancel context.CancelFunc, _ bool, transfer *Transfer) error {
		if _, err := readHeader(transfer.reader); !errors.Is(err, ErrCanceled) {
			t.Errorf("peer cancellation: %v", err)
		}
		cancel()
		if _, err := transfer.reader.ReadByte(); err == nil {
			t.Log("buffered bytes remain for discard")
		}
		return ctx.Err()
	})
	defer stream.Close()
	out, err := io.ReadAll(stream)
	if err != nil || len(out) != 0 || conn.String() != CancelSequence {
		t.Fatalf("read %q, wrote %q, err %v", out, conn.String(), err)
	}
}

func TestZmodemCancelUnblocksPeerRead(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := make(chan context.CancelFunc, 1)
	stream := Wrap(local, func(_ context.Context, cancel context.CancelFunc, _ bool, transfer *Transfer) error {
		started <- cancel
		_, err := readHeader(transfer.reader)
		return err
	})
	defer stream.Close()
	terminalOutput := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		count, _ := stream.Read(buf)
		terminalOutput <- string(buf[:count])
	}()
	if _, err := io.WriteString(peer, "**\x18B00000000000000\r\x8a\x11"); err != nil {
		t.Fatal(err)
	}
	select {
	case cancel := <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("transfer did not start")
	}
	buf := make([]byte, len(CancelSequence))
	if _, err := io.ReadFull(peer, buf); err != nil || string(buf) != CancelSequence {
		t.Fatalf("cancel bytes %q: %v", buf, err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := io.WriteString(peer, "prompt$ "); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-terminalOutput:
		if out != "prompt$ " {
			t.Fatalf("protocol leaked into terminal: %q", out)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal read did not resume")
	}
}
