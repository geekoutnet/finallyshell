package monitor

import (
	"context"
	"errors"
	"io"
	"strings"
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

	OS                      string // ID from /etc/os-release
	PackageLatest           string // the newest version the package manager offers, "" when unknown
	OSVersion               string
	EnvironmentFiles        []string // a leading minus denotes an optional EnvironmentFile
	WorkingDir              string
	CaddyEnvironment        map[string]string // non-secret HOME, XDG and CADDY_ADMIN settings
	ContainerConfig         string            // config path inside the container
	ContainerConfigUnmapped bool              // the config has no known host mount
	ContainerDataUnmapped   bool              // certificate paths are inside the container
	ContainerImage          string
	RestartPolicy           string
	Mounts                  []CaddyMount
	Ports                   string
	Resume                  bool // the unit starts from the autosaved JSON config
}

// CaddyMount maps a container filesystem destination to its host source.
type CaddyMount struct {
	Type, Source, Destination, Name string
	RW                              bool
}

// CaddyFile is a config file and its content.
type CaddyFile struct {
	Path    string
	Content string
	Size    int64
	Adapter string
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
	// ExpectMissing lists paths which must still be absent when the lock is taken.
	ExpectMissing     []string
	Reload            bool
	requireEmptySites bool
	importPatterns    []string
}

// CaddyApplyResult tells what ApplyCaddyChange did.
type CaddyApplyResult struct {
	Backup     string // the folder holding the previous files
	Validate   string // what caddy validate printed
	Reloaded   bool
	RolledBack bool
}

var (
	// ErrCaddyNotImplemented is reserved for unavailable optional panel actions.
	ErrCaddyNotImplemented = errors.New("not implemented yet")
	// ErrCaddyChanged is a file changed on the server since it was read.
	ErrCaddyChanged = errors.New("the file changed on the server")
	// ErrCaddyInvalid is a config caddy validate refused; the error's
	// text has its output.
	ErrCaddyInvalid = errors.New("caddy refused the configuration")
)

// DetectCaddy probes the distribution, binaries, package database, services,
// containers and listening sockets. No install is reported as a zero Method,
// not an error. A runner failure is returned as an error.
func DetectCaddy(ctx context.Context, run CaddyRunner) (CaddyInstall, error) {
	return detectCaddy(ctx, run)
}

// ReadCaddyFiles reads the main config and recursively expands Caddyfile imports
// through root read-only scripts. It returns an error on missing literal paths,
// malformed records or exceeded limits. A syntactically unfinished file remains
// readable so the editor can repair it.
func ReadCaddyFiles(ctx context.Context, run CaddyRunner, inst CaddyInstall) ([]CaddyFile, error) {
	return readCaddyFiles(ctx, run, inst)
}

// ApplyCaddyChange runs one root script to lock, verify expectations, back up,
// write, validate and optionally reload. It returns ErrCaddyChanged for stale
// edits, ErrCaddyInvalid on failed validation, and ErrCaddyRollback when recovery
// fails. Backup and rollback status are returned even when the command fails.
func ApplyCaddyChange(ctx context.Context, run CaddyRunner, inst CaddyInstall, change CaddyChange) (CaddyApplyResult, error) {
	return applyCaddyChange(ctx, run, inst, change)
}

// ValidateCaddyConfig reads the import graph with the provided overlays and
// writes it to a private temporary directory, inside the container for Docker.
// Both absolute and relative file imports are rebased there. It adapts the
// Caddyfile to JSON before validating, returning diagnostic
// output and ErrCaddyInvalid if Caddy refuses the preview.
func ValidateCaddyConfig(ctx context.Context, run CaddyRunner, inst CaddyInstall, files []CaddyFile) (string, error) {
	return validateCaddyPreview(ctx, run, inst, files)
}

// FormatCaddyfile runs `caddy fmt -` with content on stdin, returning stdout
// verbatim. Caddy's nonzero "not formatted" status still carries valid output.
func FormatCaddyfile(ctx context.Context, run CaddyRunner, inst CaddyInstall, content string) (string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || len(content) > caddyFileLimit {
		return "", ErrCaddyUnsafe
	}
	stdout, stderr, err := run(ctx, caddyCLI(inst, "fmt", "-"), strings.NewReader(content), nil)
	if err != nil && !(ctx.Err() == nil && (stdout != "" || content == "") && strings.Contains(strings.ToLower(stderr), "not formatted")) {
		return "", caddyCommandError(err, caddyOutput(stdout, stderr))
	}
	return stdout, nil
}

// HashCaddyPassword returns a bcrypt hash for the default basic_auth policy.
// The password is sent only on stdin; failures never include remote output.
func HashCaddyPassword(ctx context.Context, run CaddyRunner, inst CaddyInstall, plain string) (string, error) {
	hash, _, err := CaddyPasswordHash(ctx, run, inst, plain)
	return hash, err
}
