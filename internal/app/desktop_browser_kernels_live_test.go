package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/browserkernel"

	"golang.org/x/crypto/ssh"
)

// TestDesktopBrowserKernelsLive downloads each kernel from its real
// source, runs it the way the server browser does, through an SSH
// connection's SOCKS5 proxy, and checks that a page the server serves
// only on its own loopback, under a name only the server resolves,
// reaches the server from that browser, in a new window and a new tab.
//
// Needs FINALLYSHELL_TEST_KERNELS=1, FINALLYSHELL_TEST_SSH
// (user:pass@host:port of the testbox) and, on that box, a web
// server on 127.0.0.1:9311 logging "<Host><path>\t<User-Agent>" lines to
// /tmp/kernel-ua.log, with kernel-internal.test in its /etc/hosts.
// FINALLYSHELL_KERNEL_DIR keeps the downloads between runs;
// FINALLYSHELL_TEST_KERNEL_KINDS picks kernels (comma separated);
// FINALLYSHELL_TEST_KERNEL_FRESH=1 downloads them again.
func TestDesktopBrowserKernelsLive(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_KERNELS") == "" {
		t.Skip("FINALLYSHELL_TEST_KERNELS not set")
	}
	spec := os.Getenv("FINALLYSHELL_TEST_SSH")
	if spec == "" {
		t.Skip("FINALLYSHELL_TEST_SSH not set")
	}
	cred, addr, _ := strings.Cut(spec, "@")
	user, pass, _ := strings.Cut(cred, ":")
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password(pass)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	remote := func(command string) string {
		t.Helper()
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		out, err := session.CombinedOutput(command)
		if err != nil {
			t.Fatalf("%s: %v: %s", command, err, out)
		}
		return string(out)
	}
	root := os.Getenv("FINALLYSHELL_KERNEL_DIR")
	if root == "" {
		root = filepath.Join(t.TempDir(), "browser-kernels")
	}
	m := browserkernel.New(root)
	kinds := browserkernel.Kinds
	if list := os.Getenv("FINALLYSHELL_TEST_KERNEL_KINDS"); list != "" {
		kinds = nil
		for _, name := range strings.Split(list, ",") {
			kinds = append(kinds, browserkernel.Kind(strings.TrimSpace(name)))
		}
	}
	profiles := filepath.Join(t.TempDir(), "browser-profiles")
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			installed, err := m.Installed(kind)
			if err != nil || os.Getenv("FINALLYSHELL_TEST_KERNEL_FRESH") != "" {
				release, err := m.Latest(t.Context(), nil, kind)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("downloading %s %s (%d bytes, sha256 %q) from %s", kind, release.Version, release.Size, release.SHA256, release.URL)
				start, lastShown := time.Now(), time.Now()
				installed, err = m.Install(t.Context(), nil, release, func(p browserkernel.Progress) {
					if time.Since(lastShown) > 15*time.Second {
						lastShown = time.Now()
						t.Logf("phase %d: %d of %d bytes", p.Phase, p.Done, p.Total)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("installed in %s", time.Since(start).Round(time.Second))
			}
			if read, err := m.Installed(kind); err != nil || read != installed {
				t.Fatalf("Installed() = %+v, %v; want %+v", read, err, installed)
			}
			t.Logf("%s %s: %s (%d bytes unpacked)", kind, installed.Version, installed.Path(), installed.Size)

			release, err := m.Acquire(kind)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			tunnel, err := desktopBrowserProxy(client)
			if err != nil {
				t.Fatal(err)
			}
			defer tunnel.Close()
			choice := desktopKernelChoice(installed)
			proxy := tunnel.Addr().String()
			profile, cleanup, err := desktopBrowserProfile(choice, profiles, "live-"+string(kind), proxy)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			run := &desktopBrowserRun{choice: choice, profile: profile, addr: proxy, ctx: ctx}
			remote(": > /tmp/kernel-ua.log")
			target := "http://kernel-internal.test:9311/" + string(kind) + "-window"
			process, err := desktopLaunchBrowser(run, target)
			if err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- process.Wait() }()
			stopped := false
			defer func() {
				if !stopped {
					desktopBrowserStopProcess(process, exited)
				}
			}()
			product := "Chrome/"
			if kind.FirefoxBased() {
				product = "Firefox/"
			}
			waitFor := func(path string) {
				t.Helper()
				deadline := time.Now().Add(90 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range strings.Split(remote("cat /tmp/kernel-ua.log"), "\n") {
						host, agent, _ := strings.Cut(line, "\t")
						if host == "kernel-internal.test:9311/"+path {
							if !strings.Contains(agent, product) {
								t.Fatalf("request from %q, want %s", agent, product)
							}
							t.Logf("server got %s from %s", path, agent)
							return
						}
					}
					select {
					case err := <-exited:
						stopped = true
						t.Fatalf("browser exited before loading %s: %v", path, err)
					case <-time.After(time.Second):
					}
				}
				t.Fatalf("server never got %s; log %q", path, remote("cat /tmp/kernel-ua.log"))
			}
			waitFor(string(kind) + "-window")
			// A second address opens a tab in the same instance.
			openCtx, openCancel := context.WithTimeout(ctx, 30*time.Second)
			err = desktopBrowserOpenTab(openCtx, run, "http://kernel-internal.test:9311/"+string(kind)+"-tab")
			openCancel()
			if err != nil {
				t.Fatalf("new tab: %v", err)
			}
			waitFor(string(kind) + "-tab")
			if !m.InUse(kind) {
				t.Error("kernel not held while running")
			}
			if err := m.Remove(kind); err == nil {
				t.Fatal("removed a running kernel")
			}
			start := time.Now()
			stopped = true
			desktopBrowserStopProcess(process, exited)
			t.Logf("browser stopped in %s", time.Since(start).Round(time.Millisecond))
			// Nothing of it is left: on Windows a running browser holds its
			// profile, which then cannot be moved.
			deadline := time.Now().Add(15 * time.Second)
			for {
				err := os.Rename(profile, profile+".moved")
				if err == nil {
					os.Rename(profile+".moved", profile)
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("browser processes left running: %v", err)
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Logf("all processes ended after %s", time.Since(start).Round(time.Millisecond))
		})
	}
}
