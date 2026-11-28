package browserkernel

import (
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Chromium's sandbox must be able to read and run the unpacked files.
func TestInstallGrantsTheSandbox(t *testing.T) {
	m := testManager(t, runtime.GOOS, "amd64")
	archive, _ := chromeZip(t, "1")
	server := serve(t, map[string][]byte{"/c.zip": archive})
	installed, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "1", URL: server.URL + "/c.zip", Format: "zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(installed.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	sddl := descriptor.String()
	// SDDL writes ALL APPLICATION PACKAGES as AC.
	for _, trustee := range []string{";AC)", ";S-1-15-2-2)"} {
		if !strings.Contains(sddl, trustee) {
			t.Errorf("%s missing from the executable's ACL %s", trustee, sddl)
		}
	}
}
