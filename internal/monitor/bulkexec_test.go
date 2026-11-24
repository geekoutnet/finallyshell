package monitor

import (
	"strings"
	"testing"
)

func TestBulkCapture(t *testing.T) {
	if BulkShell != "'sh' '-s'" || strings.Contains(BulkShell, "-c") {
		t.Fatalf("unsafe script command: %q", BulkShell)
	}
	capture := &bulkCapture{left: BulkOutputLimit}
	stdout, stderr := bulkStream{capture, 0}, bulkStream{capture, 1}
	for _, stream := range []bulkStream{stderr, stdout} {
		data := strings.Repeat("x", (BulkOutputLimit/2)+10)
		if written, err := stream.Write([]byte(data)); written != len(data) || err != nil {
			t.Fatalf("write = %d, %v", written, err)
		}
	}
	out, errOut, truncated := capture.result()
	if len(out)+len(errOut) != BulkOutputLimit || len(errOut) != (BulkOutputLimit/2)+10 || !truncated {
		t.Fatalf("stdout %d, stderr %d, truncated %v", len(out), len(errOut), truncated)
	}
}
