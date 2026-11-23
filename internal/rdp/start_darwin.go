//go:build darwin

package rdp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// keepFile is how long the .rdp file stays after it was opened: the app
// that opens it reads it once it has started.
const keepFile = 2 * time.Minute

// Start writes the .rdp file of r's host into a private folder and opens
// it with the app that opens .rdp files, Microsoft's Windows App (or the
// older Microsoft Remote Desktop), which asks for the password itself: a
// .rdp file cannot carry it to them. wait removes the file a while
// later.
func Start(r Request) (wait func() error, err error) {
	path, dir, err := writeFile(r.Host)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	cmd := exec.Command("/usr/bin/open", path)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		msg := strings.TrimSpace(out.String())
		if strings.Contains(msg, "No application") || strings.Contains(msg, "-10814") {
			return nil, fmt.Errorf("%w: %s", ErrNoClient, msg)
		}
		if msg != "" {
			return nil, fmt.Errorf("open: %s", msg)
		}
		return nil, err
	}
	return func() error {
		time.Sleep(keepFile)
		os.RemoveAll(dir)
		return nil
	}, nil
}
