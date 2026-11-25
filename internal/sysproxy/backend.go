package sysproxy

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

func safeBackupURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.User == nil && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

type commandRunner func(name string, args ...string) (string, error)

func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C", "LANG=C")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(output), nil
}
