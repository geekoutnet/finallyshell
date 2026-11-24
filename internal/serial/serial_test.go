package serial

import (
	"bytes"
	"reflect"
	"testing"

	"finallyshell/internal/config"
)

func TestSerialPortNames(t *testing.T) {
	for _, tc := range []struct {
		os       string
		in, want []string
	}{
		{"windows", []string{"COM9", "com2", "COM2", "LPT1", "COM0"}, []string{"COM2", "COM9"}},
		{"linux", []string{"ttyUSB0", "ttyS1", "ttyACM2", "tty", "ttySnot"}, []string{"/dev/ttyACM2", "/dev/ttyS1", "/dev/ttyUSB0"}},
		{"darwin", []string{"tty.test", "cu.test", "cu.modem"}, []string{"/dev/cu.modem", "/dev/cu.test"}},
	} {
		if got := portNames(tc.os, tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.os, got, tc.want)
		}
	}
}

func TestSerialSettings(t *testing.T) {
	if got, err := normalize(config.SerialConfig{}); err != nil || got.Baud != 9600 || got.DataBits != 8 || got.StopBits != 1 {
		t.Fatalf("defaults: %+v, %v", got, err)
	}
	for _, invalid := range []config.SerialConfig{{Baud: -1}, {DataBits: 9}, {Parity: "other"}, {StopBits: 3}, {Flow: "other"}, {LineEnding: "other"}} {
		if _, err := normalize(invalid); err == nil {
			t.Errorf("accepted invalid settings: %+v", invalid)
		}
	}
	for ending, want := range map[string]string{"cr": "a\rb\rc\r", "lf": "a\nb\nc\n", "crlf": "a\r\nb\r\nc\r\n"} {
		stream := &serialStream{port: &testPort{}, ending: lineEnding(ending)}
		if _, err := stream.Write([]byte("a\rb\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte("c\r\n")); err != nil || stream.port.(*testPort).String() != want {
			t.Errorf("line ending %s: got %q (%v)", ending, stream.port.(*testPort).String(), err)
		}
	}
}

type testPort struct{ bytes.Buffer }

func (*testPort) Close() error { return nil }
