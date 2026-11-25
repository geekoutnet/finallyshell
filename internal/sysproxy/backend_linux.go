package sysproxy

import (
	"os"
	"strings"
)

func NativeBackend() Backend {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("XDG_SESSION_DESKTOP"))
	return gnomeBackend{runCommand, strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity")}
}
