package monitor

import (
	"context"
	"errors"
	"io"
)

// This file is the contract of the Caddy panel's backend: what the app
// asks of a server's Caddy. The functions run POSIX sh scripts through a
// CaddyRunner, which runs them as root (through the escalation layer)
// or, in tests, on a fake. Every argument is single-quoted, every change
// backs up what it replaces and checks the result before it takes
// effect.

// CaddyRunner runs script as root on the server, with stdin when not
// nil, reporting output lines to progress when not nil.
type CaddyRunner func(ctx context.Context, script string, stdin io.Reader, progress func(string)) (stdout, stderr string, err error)

// How Caddy was installed.
const (
	CaddyNotInstalled = ""
	CaddyAPT          = "apt"    // the official Debian/Ubuntu repository (or the distribution's)
	CaddyDNF          = "dnf"    // COPR @caddy/caddy or the distribution's
	CaddyPacman       = "pacman" // Arch
	CaddyAPK          = "apk"    // Alpine
	CaddyBinary       = "binary" // a static binary put in place by hand or by the app
	CaddyDocker       = "docker" // a container from the caddy image
)

// CaddyInstall is what DetectCaddy found of a server's Caddy.
type CaddyInstall struct {
	Method    string // one of the Caddy* methods
	Binary    string // path of the caddy binary, "" for docker
	Version   string // as `caddy version` prints it
	Container string // the container's name, for CaddyDocker

	// The service that runs it: a systemd unit, its state, and whether
	// it starts at boot; or the container's restart policy.
	Unit           string // caddy.service, caddy-api.service, or a custom unit
	UnitFile       string
	Active         string // active, inactive, failed, activating…
	Enabled        bool
	ServiceManager string // systemd, openrc, none

	Config    string // the main config file, from the unit's ExecStart (--config), default /etc/caddy/Caddyfile
	Adapter   string // caddyfile or json ("" means caddyfile)
	AdminAddr string // from the config's admin option or CADDY_ADMIN, default localhost:2019; "off" when disabled
	DataDir   string // where certificates are kept (XDG_DATA_HOME of the service's user)
	User      string // the user the service runs as
	Envfile   string // an EnvironmentFile of the unit, if any

	Modules   []string // non-standard modules (caddy list-modules --skip-standard)
	Listening []string // addresses Caddy listens on (ss)
	// Conflicts are other programs listening on 80 or 443 (nginx, apache…).
	Conflicts []string

	// Managed tells whether the config uses the app's layout: the main
	// Caddyfile importing one file per site from SitesDir.
	Managed  bool
	SitesDir string // /etc/caddy/sites by default

	OS            string // ID from /etc/os-release
	PackageLatest string // the newest version the package manager offers, "" when unknown
}

// CaddyFile is a config file and its content.
type CaddyFile struct {
	Path    string
	Content string
}

// CaddyChange is a set of config edits applied as one: written, checked
// with `caddy validate` against the main config, then reloaded; any
// failure restores every file as it was.
type CaddyChange struct {
	Writes  []CaddyFile
	Deletes []string
	// Expect holds, by path, the content each file had when the user
	// started editing; a file changed on the server since is not
	// overwritten (ErrCaddyChanged).
	Expect map[string]string
	Reload bool
}

// CaddyApplyResult tells what ApplyCaddyChange did.
type CaddyApplyResult struct {
	Backup     string // the folder holding the previous files
	Validate   string // what caddy validate printed
	Reloaded   bool
	RolledBack bool
}

var (
	// ErrCaddyNotImplemented is what the stubs return until the backend
	// is built.
	ErrCaddyNotImplemented = errors.New("not implemented yet")
	// ErrCaddyChanged is a file changed on the server since it was read.
	ErrCaddyChanged = errors.New("the file changed on the server")
	// ErrCaddyInvalid is a config caddy validate refused; the error's
	// text has its output.
	ErrCaddyInvalid = errors.New("caddy refused the configuration")
)

// DetectCaddy finds Caddy on the server however it was installed.
func DetectCaddy(ctx context.Context, run CaddyRunner) (CaddyInstall, error) {
	return CaddyInstall{}, ErrCaddyNotImplemented
}

// ReadCaddyFiles reads the main config and every file it imports.
func ReadCaddyFiles(ctx context.Context, run CaddyRunner, inst CaddyInstall) ([]CaddyFile, error) {
	return nil, ErrCaddyNotImplemented
}

// ApplyCaddyChange applies change as one transaction (see CaddyChange).
func ApplyCaddyChange(ctx context.Context, run CaddyRunner, inst CaddyInstall, change CaddyChange) (CaddyApplyResult, error) {
	return CaddyApplyResult{}, ErrCaddyNotImplemented
}

// ValidateCaddyConfig checks the config with files in place of the ones
// on disk, without changing anything, and returns caddy's output.
func ValidateCaddyConfig(ctx context.Context, run CaddyRunner, inst CaddyInstall, files []CaddyFile) (string, error) {
	return "", ErrCaddyNotImplemented
}

// FormatCaddyfile returns content as `caddy fmt` formats it.
func FormatCaddyfile(ctx context.Context, run CaddyRunner, inst CaddyInstall, content string) (string, error) {
	return "", ErrCaddyNotImplemented
}

// HashCaddyPassword returns the hash `caddy hash-password` makes of
// plain, which goes on stdin, never in the command line.
func HashCaddyPassword(ctx context.Context, run CaddyRunner, inst CaddyInstall, plain string) (string, error) {
	return "", ErrCaddyNotImplemented
}
