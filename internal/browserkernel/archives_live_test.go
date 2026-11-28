package browserkernel

import (
	"os"
	"strings"
	"testing"
)

// TestRealArchivesLive installs the real downloads of other systems'
// kernels, to check that they unpack with their links and modes and that
// their executables are found: FINALLYSHELL_TEST_KERNEL_ARCHIVES lists
// goos/goarch/kind triples, such as darwin/arm64/chrome. Run it where
// symbolic links work (Linux).
func TestRealArchivesLive(t *testing.T) {
	list := os.Getenv("FINALLYSHELL_TEST_KERNEL_ARCHIVES")
	if list == "" {
		t.Skip("FINALLYSHELL_TEST_KERNEL_ARCHIVES not set")
	}
	testHTTP = false
	defer func() { testHTTP = true }()
	for _, triple := range strings.Split(list, ",") {
		parts := strings.Split(strings.TrimSpace(triple), "/")
		if len(parts) != 3 {
			t.Fatalf("bad triple %q", triple)
		}
		t.Run(triple, func(t *testing.T) {
			m := New(t.TempDir())
			m.GOOS, m.GOARCH = parts[0], parts[1]
			kind := Kind(parts[2])
			release, err := m.Latest(t.Context(), nil, kind)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s from %s", release.Version, release.URL)
			installed, err := m.Install(t.Context(), nil, release, nil)
			if err != nil {
				t.Fatal(err)
			}
			if read, err := m.Installed(kind); err != nil || read != installed {
				t.Fatalf("Installed() = %+v, %v", read, err)
			}
			links := 0
			if err := walkLinks(installed.Dir+"/app", &links); err != nil {
				t.Fatalf("broken link: %v", err)
			}
			t.Logf("executable %s, %d bytes, %d symbolic links", installed.Exe, installed.Size, links)
		})
	}
}

func walkLinks(dir string, links *int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := dir + "/" + entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			*links++
			if _, err := os.Stat(path); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			if err := walkLinks(path, links); err != nil {
				return err
			}
		}
	}
	return nil
}
