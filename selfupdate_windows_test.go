package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The setup program runs, silent, only once the app's process has ended,
// and the app starts after it: here a process that sleeps, a setup that
// notes its arguments and the app that notes it started, with a ' in the
// folder's name.
func TestStartInstallAfter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log.txt")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("@echo off\r\n"+body+"\r\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	setup := write("setup.cmd", `>>"`+log+`" echo setup %*`)
	exe := write("app.cmd", `>>"`+log+`" echo app`)

	app := exec.Command("powershell.exe", "-NoProfile", "-Command", "Start-Sleep -Seconds 3")
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	if err := startInstallAfter(app.Process.Pid, setup, exe); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the setup ran while the app was running")
	}
	app.Wait()
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		got := strings.Fields(strings.ReplaceAll(string(data), "\r", ""))
		if strings.Join(got, " ") == "setup /S app" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log %q, want the setup with /S, then the app", data)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The app starts the install and quits at once, as InstallUpdate does:
// what it started goes on without it. The app is this test's binary run
// again, which starts it and exits.
func TestStartInstallOutlivesApp(t *testing.T) {
	if setup := os.Getenv("FINALLYSHELL_TEST_INSTALL_SETUP"); setup != "" {
		if err := startInstallAfter(os.Getpid(), setup, os.Getenv("FINALLYSHELL_TEST_INSTALL_APP")); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log.txt")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("@echo off\r\n"+body+"\r\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	app := exec.Command(os.Args[0], "-test.run=^TestStartInstallOutlivesApp$")
	app.Env = append(os.Environ(),
		"FINALLYSHELL_TEST_INSTALL_SETUP="+write("setup.cmd", `>>"`+log+`" echo setup %*`),
		"FINALLYSHELL_TEST_INSTALL_APP="+write("app.cmd", `>>"`+log+`" echo app`))
	if out, err := app.CombinedOutput(); err != nil {
		t.Fatalf("the app: %v %s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if got := strings.Join(strings.Fields(string(data)), " "); got == "setup /S app" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log %q, want the setup with /S, then the app", data)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
