package app

import (
	"strings"

	"finallyshell/internal/config"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type desktopWallpaperState struct {
	name                string
	image               *ui.Bitmap
	err                 error
	gen                 int
	requested, choosing bool
}

type desktopWallpaperDesign struct {
	id                           string
	name                         func() string
	base, mid, end, glow, ribbon string
	phase                        float32
}

var desktopWallpapers = []desktopWallpaperDesign{
	{"prism", locales.DesktopWallpaperPrism, "#121638", "#433987", "#27678b", "#c398ff", "#7adfff", -.10},
	{"sunset", locales.DesktopWallpaperSunset, "#261644", "#762a62", "#b25758", "#ffb386", "#ffbb95", .16},
	{"aurora", locales.DesktopWallpaperAurora, "#101534", "#1c425b", "#124947", "#60efba", "#8cbbff", .01},
	{"ocean", locales.DesktopWallpaperOcean, "#0d1b3a", "#10466e", "#11798a", "#7bcee3", "#47d6d4", -.21},
	{"forest", locales.DesktopWallpaperForest, "#12251f", "#224c39", "#416a4f", "#aade9c", "#b7d99a", .22},
	{"graphite", locales.DesktopWallpaperGraphite, "#10151e", "#273243", "#38465b", "#a0b7dd", "#99aaca", -.08},
	{"nebula", locales.DesktopWallpaperNebula, "#17132e", "#38205c", "#612d79", "#df86de", "#8e8cfb", .07},
	{"dusk", locales.DesktopWallpaperDusk, "#13192b", "#302f50", "#575267", "#f2b7a5", "#d0b2da", -.16},
}

var desktopWallpaperColors = []struct {
	hex  string
	name func() string
}{
	{"#172437", locales.DesktopWallpaperNavy}, {"#293d78", locales.DesktopWallpaperBlue}, {"#42336b", locales.DesktopWallpaperViolet},
	{"#673452", locales.DesktopWallpaperPlum}, {"#633938", locales.DesktopWallpaperClay}, {"#184e51", locales.DesktopWallpaperTeal},
	{"#315244", locales.DesktopWallpaperPine}, {"#393c48", locales.DesktopWallpaperSlate},
}

func desktopWallpaperColor(key string) (ui.Color, bool) {
	hex, ok := strings.CutPrefix(key, "color:")
	if !ok || len(hex) != 7 || hex[0] != '#' {
		return ui.Color{}, false
	}
	for _, ch := range hex[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return ui.Color{}, false
		}
	}
	return ui.Hex(hex), true
}

func (a *App) desktopWallpaper(e ui.Element, key string, radius float32) {
	if color, ok := desktopWallpaperColor(key); ok {
		e.Background(color)
		return
	}
	design := desktopWallpapers[0]
	if id, ok := strings.CutPrefix(key, "builtin:"); ok {
		for _, candidate := range desktopWallpapers {
			if candidate.id == id {
				design = candidate
				break
			}
		}
	}
	var image *ui.Bitmap
	if _, err := config.DesktopWallpaperPath(key); err == nil {
		image = a.desktopWallpaperImage(key)
	}
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Clip(r, radius, func() {
			if image != nil {
				p.Image(image, r, ui.Cover)
			} else {
				drawDesktopWallpaper(p, r, design)
			}
		})
	})
}

func drawDesktopWallpaper(p *ui.Painter, r ui.Rect, design desktopWallpaperDesign) {
	base, mid, end := ui.Hex(design.base), ui.Hex(design.mid), ui.Hex(design.end)
	glow, ribbon := ui.Hex(design.glow), ui.Hex(design.ribbon)
	p.FillGradient(r, ui.LinearGradient{Angle: 118, Stops: []ui.GradientStop{{Color: base}, {Color: mid, At: .55}, {Color: end, At: 1}}}, 0)
	p.FillRadialGradient(r, ui.RadialGradient{Center: ui.Point{X: .12, Y: .38}, Stops: []ui.GradientStop{
		{Color: glow.Alpha(.45)}, {Color: glow.Alpha(.12), At: .41}, {Color: glow.Alpha(0), At: .78},
	}}, 0)
	p.FillRadialGradient(r, ui.RadialGradient{Center: ui.Point{X: .87, Y: .18}, Stops: []ui.GradientStop{
		{Color: ribbon.Alpha(.43)}, {Color: ribbon.Alpha(.11), At: .35}, {Color: ribbon.Alpha(0), At: .75},
	}}, 0)
	x := func(at float32) float32 { return r.X + at*r.W }
	y := func(at float32) float32 { return r.Y + (at+design.phase)*r.H }
	for idx := range 4 {
		offset := float32(idx) * .108
		var wave ui.Path
		wave.MoveTo(x(-.07), y(.38+offset)).CubeTo(x(.35), y(-.16+offset), x(.39), y(1.17+offset), x(.76), y(.64+offset)).
			CubeTo(x(.92), y(.40+offset), x(.96), y(.25+offset), x(1.09), y(.31+offset)).
			LineTo(x(1.09), y(1.31)).LineTo(x(-.07), y(1.34)).Close()
		p.FillPathGradient(&wave, ui.LinearGradient{Angle: 151, Stops: []ui.GradientStop{
			{Color: glow.Alpha(.27)}, {Color: base.Alpha(.03), At: .43}, {Color: ribbon.Alpha(.24), At: .71}, {Color: base.Alpha(.55), At: 1},
		}})
		var rim ui.Path
		rim.MoveTo(x(-.07), y(.38+offset)).CubeTo(x(.35), y(-.16+offset), x(.39), y(1.17+offset), x(.76), y(.64+offset)).
			CubeTo(x(.92), y(.40+offset), x(.96), y(.25+offset), x(1.09), y(.31+offset))
		p.StrokePathGradient(&rim, max(1, r.W*.0018), ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
			{Color: glow.Alpha(.12)}, {Color: glow.Alpha(.60), At: .30}, {Color: ribbon.Alpha(.09), At: .6}, {Color: ribbon.Alpha(.63), At: 1},
		}})
	}
}

func (a *App) desktopWallpaperImage(name string) *ui.Bitmap {
	st := &a.desktopWall
	if st.name != name {
		st.name, st.image, st.err, st.requested = name, nil, nil, false
	}
	if !st.requested {
		st.requested = true
		st.gen++
		gen := st.gen
		go func() {
			file, err := config.DesktopWallpaperPath(name)
			var img *ui.Bitmap
			if err == nil {
				img, err = readImage(file)
			}
			a.post(func() {
				if st.gen == gen && st.name == name {
					st.image, st.err = img, err
				}
			})
		}()
	}
	return st.image
}

func (a *App) desktopChooseWallpaper() {
	st := &a.desktopWall
	if st.choosing {
		return
	}
	st.choosing, st.err = true, nil
	title := locales.DesktopWallpaperChoose()
	go func() {
		file, err := a.plat.OpenImage(WindowMain, title)
		var name string
		var img *ui.Bitmap
		if err == nil && file != "" {
			name, img, err = desktopImportWallpaper(file)
		}
		a.post(func() {
			st.choosing = false
			if err != nil {
				st.err = err
				return
			}
			if name != "" {
				a.desktopSetWallpaper(name, img)
			}
		})
	}()
}

func desktopImportWallpaper(file string) (string, *ui.Bitmap, error) {
	data, err := readImageFile(file)
	if err != nil {
		return "", nil, err
	}
	img, err := decodeBackground(data, maxBackgroundSide)
	if err != nil {
		return "", nil, err
	}
	name, err := config.SaveDesktopWallpaper(data, file)
	if err != nil {
		return "", nil, &saveImageError{err}
	}
	return name, img, nil
}

func (a *App) desktopSetWallpaper(key string, img *ui.Bitmap) {
	old := a.desktopPreferences().Wallpaper
	st := &a.desktopWall
	st.gen++
	st.name, st.image, st.err, st.requested = key, img, nil, img != nil
	a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.Wallpaper = key })
	if old != key {
		if _, err := config.DesktopWallpaperPath(old); err == nil {
			go config.RemoveDesktopWallpaper(old)
		}
	}
}
