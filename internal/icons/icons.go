// Package icons holds the app's SVG icons, from Lucide (ISC license, see
// svg/LICENSE), and the logos of coding agents, from LobeHub Icons (MIT
// license, see agents/LICENSE), which are their owners' trademarks.
package icons

import (
	"embed"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg agents/*.svg
var files embed.FS

var (
	mu     sync.Mutex
	parsed = map[string]*ui.SVG{}
)

// Get returns the icon named after its file in svg/, without ".svg". A
// name with no file is a bug in the app: tests panic on it, while the
// app draws a blank icon rather than crash over a decoration.
func Get(name string) *ui.SVG { return get("svg/" + name + ".svg") }

// Agent returns the logo of a coding agent, named after its file in
// agents/ as monitor.Agent's Icon names it, single-colored, to draw as
// an icon. It panics on a name that has no file.
func Agent(name string) *ui.SVG { return get("agents/" + name + ".svg") }

func get(file string) *ui.SVG {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := parsed[file]; ok {
		return s
	}
	data, err := files.ReadFile(file)
	if err != nil {
		if testing.Testing() {
			panic("icons: no icon " + file)
		}
		data = blank
	}
	s := ui.MustParseSVG(data)
	parsed[file] = s
	return s
}

// blank is the icon drawn for a name with no file, outside tests.
var blank = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"></svg>`)
