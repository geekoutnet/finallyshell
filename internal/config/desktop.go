package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const desktopWallpaperPrefix = "desktop-wallpaper-"

func DesktopWallpaperPath(name string) (string, error) {
	if !strings.HasPrefix(name, desktopWallpaperPrefix) || filepath.Base(name) != name || strings.ContainsAny(name, "/\\:\x00\r\n") {
		return "", ErrNoBackground
	}
	return filepath.Join(Dir(), name), nil
}

func SaveDesktopWallpaper(data []byte, src string) (string, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if !slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".webp"}, ext) {
		ext = ".img"
	}
	name := fmt.Sprintf("%s%d%s", desktopWallpaperPrefix, time.Now().UnixNano(), ext)
	return name, writeFileAtomic(filepath.Join(Dir(), name), data)
}

func RemoveDesktopWallpaper(name string) {
	if file, err := DesktopWallpaperPath(name); err == nil {
		_ = os.Remove(file)
	}
}

// DesktopSettings is the desktop mode's settings (internal/app/desktop*.go):
// how the desktop looks and behaves, and each host's own layout. It is
// this device's own (localSettings): the wallpaper is a file in this
// device's config folder. Settings keeps a pointer to it, replaced and
// never changed through, so that Settings stays comparable.
type DesktopSettings struct {
	// Wallpaper is the desktop's background: "" for the default built-in
	// one, "builtin:<id>" for another built-in one, "color:#rrggbb" for a
	// plain color, or the name of an image copied into the config folder
	// (png, jpg, gif or webp).
	Wallpaper string `json:"wallpaper,omitempty"`
	// SingleClick opens desktop icons with one click instead of two.
	SingleClick bool `json:"singleClick,omitempty"`
	// StackWindows opens an app's new windows on top of each other
	// instead of cascading them.
	StackWindows bool `json:"stackWindows,omitempty"`
	// CoverDock lets maximized windows cover the dock (full-screen mode)
	// instead of leaving it visible.
	CoverDock bool `json:"coverDock,omitempty"`
	// HideServerInfo folds the server's details on the right of the
	// desktop to the server's name.
	HideServerInfo bool `json:"hideServerInfo,omitempty"`
	// IconSize is the desktop icons' size: "" for medium, "small" or
	// "large".
	IconSize string `json:"iconSize,omitempty"`
	// Pinned are the IDs of the apps pinned to the dock, in order; nil
	// for the default ones.
	Pinned *[]string `json:"pinned,omitempty"`
	// Hosts is each host's own desktop, by Host.ID.
	Hosts map[string]DesktopHost `json:"hosts,omitempty"`
}

// DesktopHost is one host's desktop: where its icons are, its icon
// folders, and the file manager's favorites, recent places and notes.
type DesktopHost struct {
	// Icons are the desktop's icons the user moved or added, in order; nil
	// for the default layout.
	Icons           []DesktopIcon `json:"icons,omitempty"`
	IconsConfigured bool          `json:"iconsConfigured,omitempty"`
	AutoArrange     bool          `json:"autoArrange,omitempty"`
	// Folders are the desktop's icon folders.
	Folders []DesktopFolder `json:"folders,omitempty"`
	// Favorites are the file manager's favorite folders (absolute paths).
	Favorites []string `json:"favorites,omitempty"`
	// Recent are the files and folders opened last, newest first.
	Recent []string `json:"recent,omitempty"`
	// Notes are the notes the user wrote on files, by absolute path.
	Notes map[string]string `json:"notes,omitempty"`
}

// DesktopIcon is an icon on the desktop: an app, a folder of icons, or a
// shortcut to a remote path, at a cell of the desktop's grid.
type DesktopIcon struct {
	// App is the app's ID, "folder:<id>" for an icon folder, or
	// "path:<absolute path>" for a shortcut to a remote file or folder.
	App string `json:"app"`
	// Col and Row are the cell of the grid, from the top left; -1 places
	// it in the first free cell.
	Col int `json:"col"`
	Row int `json:"row"`
	// Folder is the ID of the icon folder it is in, "" on the desktop.
	Folder string `json:"folder,omitempty"`
}

// DesktopFolder is an icon folder on the desktop.
type DesktopFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AISettings is the AI assistant's settings (internal/app/desktop_ai*.go).
// The API key is in the system's credential store, never here.
type AISettings struct {
	Provider string `json:"provider,omitempty"`
	// Endpoint is the base URL of an OpenAI-compatible API, such as
	// https://api.openai.com/v1; "" turns the assistant off.
	Endpoint string `json:"endpoint,omitempty"`
	// Model is the default model's name, which new conversations use.
	Model string `json:"model,omitempty"`
	// Models are the other models a conversation can switch to.
	Models []string `json:"models,omitempty"`
	// SystemPrompt is added before the assistant's own instructions.
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// SendContext lets the assistant send the server's OS, the current
	// folder and the last lines of the terminal with a question.
	SendContext bool `json:"sendContext,omitempty"`
	// ContextTokens is how much of a conversation the model gets, in
	// tokens: 0 for the default (ai.DefaultContextTokens). Past it, the
	// earlier part of the conversation is summarized.
	ContextTokens int `json:"contextTokens,omitempty"`
}

// Equal reports whether s and o are the same settings.
func (s AISettings) Equal(o AISettings) bool {
	return s.Provider == o.Provider && s.Endpoint == o.Endpoint && s.Model == o.Model && slices.Equal(s.Models, o.Models) &&
		s.SystemPrompt == o.SystemPrompt && s.SendContext == o.SendContext && s.ContextTokens == o.ContextTokens
}

// HasModel reports whether model is the default model or one of the
// others.
func (s AISettings) HasModel(model string) bool {
	return model != "" && (model == s.Model || slices.Contains(s.Models, model))
}
