package app

import (
	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

var desktopIconSizes = []struct {
	id   string
	name func() string
}{
	{"small", locales.DesktopShellSmall}, {"", locales.DesktopShellMedium}, {"large", locales.DesktopShellLarge},
}

func (a *App) desktopPersonalizePage(c *ui.Context) {
	prefs := a.desktopPreferences()
	ui.Scroll(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(14).Gap(13).Children(func() {
			ui.Text(c, locales.DesktopShellPersonalize()).FontSize(20).Bold()
			ui.Text(c, locales.DesktopShellPersonalizeHint()).TextColor(theme.TextMuted)
			ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, locales.DesktopWallpaperChoose()).Disabled(a.desktopWall.choosing).Clicked() {
					a.desktopChooseWallpaper()
				}
				if ui.Button(c, locales.DesktopWallpaperReset()).Disabled(prefs.Wallpaper == "" || a.desktopWall.choosing).Clicked() {
					a.desktopSetWallpaper("", nil)
				}
			})
			if a.desktopWall.err != nil {
				ui.Text(c, describeImageError(a.desktopWall.err)).TextColor(ui.Hex("#df6478"))
			}
			desktopPersonalizeSection(c, locales.DesktopWallpaperBuiltins(), func() {
				ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
					for idx, design := range desktopWallpapers {
						key := "builtin:" + design.id
						if idx == 0 {
							key = ""
						}
						a.desktopWallpaperChoice(c, key, design.name(), 91, 54, prefs.Wallpaper == key || idx == 0 && prefs.Wallpaper == "builtin:"+design.id)
					}
					if _, err := config.DesktopWallpaperPath(prefs.Wallpaper); err == nil {
						a.desktopWallpaperChoice(c, prefs.Wallpaper, locales.DesktopWallpaperCustom(), 91, 54, true)
					}
				})
			})
			desktopPersonalizeSection(c, locales.DesktopWallpaperColors(), func() {
				ui.Row(c).FillWidth().Wrap().Gap(7).Children(func() {
					for _, color := range desktopWallpaperColors {
						key := "color:" + color.hex
						a.desktopWallpaperChoice(c, key, color.name(), 36, 27, prefs.Wallpaper == key)
					}
				})
			})
			desktopPersonalizeSection(c, locales.DesktopShellBehavior(), func() {
				desktopPersonalizeToggle(c, locales.DesktopShellOpenIcons(), locales.DesktopShellDoubleClick(), locales.DesktopShellSingleClick(), prefs.SingleClick, func(value bool) {
					a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.SingleClick = value })
				})
				desktopPersonalizeToggle(c, locales.DesktopShellNewWindows(), locales.DesktopShellCascade(), locales.DesktopShellStack(), prefs.StackWindows, func(value bool) {
					a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.StackWindows = value })
				})
				desktopPersonalizeToggle(c, locales.DesktopShellMaximized(), locales.DesktopShellKeepDock(), locales.DesktopShellCoverDock(), prefs.CoverDock, func(value bool) { a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.CoverDock = value }) })
				ui.Text(c, locales.DesktopShellIconSize()).Bold().Margin(4, 0, 0, 0)
				ui.Row(c).Wrap().Gap(8).Children(func() {
					value := prefs.IconSize
					for _, size := range desktopIconSizes {
						ui.Radio(c.Key(size.id), &value, size.id, size.name())
					}
					if value != prefs.IconSize {
						a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.IconSize = value })
					}
				})
				if ui.Button(c, locales.DesktopDockReset()).Disabled(prefs.Pinned == nil).AlignSelf(ui.Start).Clicked() {
					a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.Pinned = nil })
				}
			})
		})
	})
}

func desktopPersonalizeSection(c *ui.Context, label string, content func()) {
	ui.Column(c.Key(label)).FillWidth().Padding(10).Gap(8).Radius(10).Border(1, theme.Border).Background(theme.Surface).Children(func() {
		ui.Text(c, label).Bold().FontSize(13)
		content()
	})
}

func desktopPersonalizeToggle(c *ui.Context, label, off, on string, checked bool, update func(bool)) {
	ui.Text(c, label).Bold()
	value := 0
	if checked {
		value = 1
	}
	ui.Row(c.Key(label)).Wrap().Gap(8).Children(func() { ui.Radio(c, &value, 0, off); ui.Radio(c, &value, 1, on) })
	if (value == 1) != checked {
		update(value == 1)
	}
}

func (a *App) desktopWallpaperChoice(c *ui.Context, key, label string, width, height float32, selected bool) {
	button := ui.Column(c.Key(key)).Width(width).Shrink(0).Gap(4).AlignItems(ui.Center).Role(ui.RoleButton).Label(label).Tooltip(label)
	if button.Clicked() && !selected && !a.desktopWall.choosing {
		a.desktopSetWallpaper(key, nil)
	}
	button.Children(func() {
		thumb := ui.Box(c).Size(width, height).Radius(7).Clip().Border(1, theme.Border)
		a.desktopWallpaper(thumb, key, 7)
		if selected {
			thumb.Border(2, theme.Accent).Children(func() {
				ui.Box(c).Absolute().Right(4).Bottom(4).Size(18, 18).Radius(9).Background(theme.Accent).Center().Children(func() { ui.Icon(c, icons.Get("check")).FontSize(13).TextColor(ui.Hex("#ffffff")) })
			})
		}
		if width > 40 {
			ui.Text(c, label).FontSize(11).SingleLine().MaxWidth(width)
		}
	})
}
