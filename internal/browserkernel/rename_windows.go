package browserkernel

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// rename is os.Rename, retried for a while on Windows when the folder is
// held open: antivirus and the search indexer open the files just
// unpacked, and Windows refuses to move a folder with an open file.
func rename(from, to string) error {
	start := time.Now()
	for delay := 10 * time.Millisecond; ; delay = min(2*delay, 500*time.Millisecond) {
		err := os.Rename(from, to)
		if err == nil || !retryable(err) || time.Since(start) > renameTimeout {
			return err
		}
		time.Sleep(delay)
	}
}

var renameTimeout = 30 * time.Second

func retryable(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION.
	return errno == 5 || errno == 32 || errno == 33
}

// removeAll is os.RemoveAll, retried the same way.
func removeAll(path string) error {
	start := time.Now()
	for delay := 10 * time.Millisecond; ; delay = min(2*delay, 500*time.Millisecond) {
		err := os.RemoveAll(path)
		if err == nil || !retryable(err) || time.Since(start) > renameTimeout {
			return err
		}
		time.Sleep(delay)
	}
}

// grantSandbox lets Chromium's sandboxed processes read and run what is
// unpacked into dir: they run as AppContainers ("ALL APPLICATION
// PACKAGES", S-1-15-2-1) and less privileged ones ("ALL RESTRICTED
// APPLICATION PACKAGES", S-1-15-2-2), which Program Files lets in and the
// user's folders do not. Without it Chrome for Testing stops with
// "Sandbox cannot access executable" and its network service crashes.
// The entries are inherited, so dir gets them before anything is in it.
func grantSandbox(dir string) error {
	var entries []windows.EXPLICIT_ACCESS
	for _, name := range []string{"S-1-15-2-1", "S-1-15-2-2"} {
		sid, err := windows.StringToSid(name)
		if err != nil {
			return err
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_READ | windows.GENERIC_EXECUTE,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	descriptor, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	current, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries(entries, current)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
