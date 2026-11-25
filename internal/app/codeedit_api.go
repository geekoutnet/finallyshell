package app

import (
	"context"

	"finallyshell/internal/monitor"
	"golang.org/x/crypto/ssh"
)

// This file is the contract of the code editor window: a strong editor
// for files on a server (the MyGo code editor: highlighting, find and
// replace, go to line, folding, several cursors, snippets), which reads
// and writes as root when the user can't, shows the changes before
// saving, and runs the caller's checks. The Caddy panel's Config section
// opens it, as can the file panel. codeedit*.go builds it.

// CodeEditorFile is a file the editor shows: its path on the server and,
// when the caller already read it, its content.
type CodeEditorFile struct {
	Path    string
	Content string
	Loaded  bool // Content is the file's, no need to read it
}

// CodeEditorSpec is what the editor edits and how.
type CodeEditorSpec struct {
	Title   string
	Session *Session
	Client  *ssh.Client
	// Files are shown in a tree on the left, the first open; one file
	// hides the tree.
	Files []CodeEditorFile
	// Language names the highlighting, as the MyGo editor's (or
	// "caddyfile", "nginx", "systemd"); "" guesses from the file name.
	Language string
	// Root reads and writes through the escalation layer.
	Root bool
	// Validate, when set, checks the files as they would be saved
	// (every open file's text), before saving; its error stops the save
	// and its text shows.
	Validate func(ctx context.Context, files []monitor.CaddyFile) (string, error)
	// Format, when set, formats one file's text (a toolbar button).
	Format func(ctx context.Context, path, text string) (string, error)
	// Save, when set, saves instead of the editor's own write: the
	// changed files at once (the Caddy panel applies them as one
	// transaction with a reload). The editor's own write backs each file
	// up as <file>.bak-<yyyymmddHHMMSS> and refuses a file changed on
	// the server since it was read.
	Save func(ctx context.Context, files []monitor.CaddyFile, expect map[string]string) (string, error)
	// AllowSaveAnyway lets the user explicitly bypass a failed Validate when Save is nil.
	AllowSaveAnyway bool
	// Saved is called on the UI thread after a save.
	Saved func()
}

// openCodeEditor opens the code editor window as spec says.
func (a *App) openCodeEditor(spec CodeEditorSpec) {
	openCodeEditorImpl(a, spec)
}

// openCodeEditorImpl is set by codeedit*.go.
var openCodeEditorImpl = func(a *App, spec CodeEditorSpec) {}
