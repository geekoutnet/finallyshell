package zmodem

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFrames(t *testing.T) {
	for _, fixture := range []struct {
		wire string
		want header
	}{
		{"**\x18B00000000000000\r\x8a\x11", header{kind: zrqinit, format: hexHeader}},
		{"**\x18B0100000023be50\r\x8a\x11", header{kind: zrinit, format: hexHeader, value: 0x23000000}},
		{"*\x18A\x18@\x18@\x18@\x18@\x18@\x18@\x18@", header{kind: zrqinit, format: bin16}},
	} {
		got, err := readHeader(bufio.NewReader(strings.NewReader(fixture.wire)))
		if err != nil || got != fixture.want {
			t.Fatalf("%q: got %+v, %v; want %+v", fixture.wire, got, err, fixture.want)
		}
	}
	for _, format := range []byte{hexHeader, bin16, bin32} {
		want := header{kind: zdata, value: 0x7f181310, format: format}
		wire := encodeHeader(want)
		got, err := readHeader(bufio.NewReader(bytes.NewReader(wire)))
		if err != nil || got != want {
			t.Fatalf("%q: got %+v, %v; want %+v", wire, got, err, want)
		}
	}
}

func TestDataPacket(t *testing.T) {
	payload := make([]byte, 256)
	for index := range payload {
		payload[index] = byte(index)
	}
	for _, format := range []byte{bin16, bin32} {
		wire := encodeData(payload, zcrcw, format)
		got, end, err := readData(bufio.NewReader(bytes.NewReader(wire)), format)
		if err != nil || end != zcrcw || !bytes.Equal(got, payload) {
			t.Fatalf("end %x, data %x, err %v", end, got, err)
		}
		wire[1] ^= 1
		if _, _, err := readData(bufio.NewReader(bytes.NewReader(wire)), format); err == nil {
			t.Fatal("accepted corrupt packet")
		}
	}
}
