package app

import (
	"slices"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const windowHighlightRule WindowKind = "highlight-rule"

var highlightColorChoices = []choice[string]{
	{"red", locales.HlRed}, {"yellow", locales.HlYellow}, {"green", locales.HlGreen},
	{"cyan", locales.HlCyan}, {"blue", locales.HlBlue}, {"magenta", locales.HlMagenta},
}

func (a *App) highlightSettings() config.TerminalHighlight {
	if a.settings.Highlight != nil {
		return *a.settings.Highlight
	}
	return config.TerminalHighlight{}
}

func (a *App) highlightPage(c *ui.Context) {
	cfg := a.highlightSettings()
	rules := cfg.Rules
	if !cfg.UseCustom {
		rules = defaultHighlightRules()
	}
	enabled := !cfg.Disabled
	if ui.Checkbox(c, &enabled, locales.HlEnable()).Changed() {
		cfg.Disabled = !enabled
		a.saveHighlightSettings(cfg)
	}
	widgets.Muted(c, locales.HlHelp()).FontSize(12)
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if ui.Button(c, locales.HlAdd()).Disabled(len(rules) >= 48).Clicked() {
			a.editHighlightRule(-1, config.HighlightRule{Color: "red"})
		}
		if ui.Button(c, locales.HlReset()).Disabled(!cfg.UseCustom).Clicked() {
			go func() {
				if a.plat.Confirm(Confirmation{Title: locales.HlOptions(), Message: locales.HlReset(), Detail: locales.HlResetDetail(), OK: locales.HlReset(), Cancel: locales.Cancel()}) {
					a.post(func() {
						latest := a.highlightSettings()
						latest.UseCustom, latest.Rules = false, nil
						a.saveHighlightSettings(latest)
					})
				}
			}()
		}
	})
	ui.Text(c, locales.HlPreview()).Bold()
	a.highlightPreview(c, rules)
	for index, rule := range rules {
		a.highlightRuleRow(c.Key(index), index, rule)
	}
}

func (a *App) highlightPreview(c *ui.Context, rules []config.HighlightRule) {
	var compiled []compiledHighlight
	for _, rule := range rules[:min(len(rules), 48)] {
		if style, err := compileHighlight(rule); err == nil && style.match != nil {
			compiled = append(compiled, style)
		}
	}
	line := []byte("error  warning  success  192.0.2.10  https://example.invalid")
	spans := matchingSpans(nil, compiled, line)
	ui.Row(c).Wrap().Gap(4).Padding(8).Radius(5).Background(c.Theme().Surface).Children(func() {
		at := 0
		for _, span := range spans {
			if span.start < at {
				continue
			}
			if span.start > at {
				ui.Text(c, string(line[at:span.start])).FontSize(12)
			}
			style := compiled[span.rule].sgr
			color := 0
			for index, code := range highlightSGR {
				if style[2:4] == code {
					color = index
				}
			}
			preview := ui.Text(c, string(line[span.start:span.end])).FontSize(12).TextColor(highlightColor(c, color))
			stylePreview(preview, style)
			at = span.end
		}
		if at < len(line) {
			ui.Text(c, string(line[at:])).FontSize(12)
		}
	})
}

func stylePreview(preview ui.Element, style string) {
	for _, code := range style[4 : len(style)-1] {
		switch code {
		case '1':
			preview.Bold()
		case '4':
			preview.Underline()
		}
	}
}

func highlightColor(c *ui.Context, index int) ui.Color {
	if c.Theme().Dark {
		return []ui.Color{ui.Hex("#ff8387"), ui.Hex("#f1c35f"), ui.Hex("#73d499"), ui.Hex("#66d7de"), ui.Hex("#86baff"), ui.Hex("#dda4ed")}[index]
	}
	return []ui.Color{ui.Hex("#ac2533"), ui.Hex("#825700"), ui.Hex("#21683b"), ui.Hex("#006670"), ui.Hex("#2459ac"), ui.Hex("#803399")}[index]
}

func (a *App) highlightRuleRow(c *ui.Context, index int, rule config.HighlightRule) {
	row := ui.Row(c).Padding(6, 8).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border)
	display := rule.Pattern
	if display == "" {
		display = locales.HlPattern()
	}
	row.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.Edit()).Chosen() {
			a.editHighlightRule(index, rule)
		}
		if menu.Item(locales.Delete()).Danger().Chosen() {
			a.removeHighlightRule(index, rule)
		}
	})
	row.Children(func() {
		ui.Box(c).Size(9, 9).Radius(5).Shrink(0).Background(highlightColor(c, max(0, slices.Index(highlightColors, rule.Color))))
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, display).SingleLine().Tooltip(display)
			scope := locales.HlAllHosts()
			if rule.Host != "" {
				scope = locales.HlMissingHost()
				if host := a.hosts.Host(rule.Host); host != nil && !host.Deleted() {
					scope = host.Title()
				}
			}
			kind := locales.HlWord()
			if rule.Regex {
				kind = locales.HlRegex()
			}
			widgets.Muted(c, kind+" · "+scope).FontSize(11).SingleLine()
		})
		if widgets.IconButton(c, "pencil", locales.Edit()).Shrink(0).Clicked() {
			a.editHighlightRule(index, rule)
		}
		if widgets.IconButton(c, "trash-2", locales.Delete()).Shrink(0).Clicked() {
			a.removeHighlightRule(index, rule)
		}
	})
}

func (a *App) editHighlightRule(index int, rule config.HighlightRule) {
	original := rule
	spec := WindowSpec{Kind: windowHighlightRule, Title: locales.HlRule, Width: 460, Height: 410, MinWidth: 380, MinHeight: 350, Parent: WindowOptions, Modal: true}
	a.openWindow(spec, func(c *ui.Context) {
		compiled, err := compileHighlight(rule)
		valid := err == nil && compiled.match != nil
		ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
			ui.Field(c, locales.HlPattern(), func() { ui.TextInput(c, &rule.Pattern).FillWidth().Label(locales.HlPattern()) })
			if rule.Pattern != "" && !valid {
				ui.Text(c, locales.HlInvalid()).TextColor(c.Theme().Danger).FontSize(12)
			}
			if valid {
				preview := ui.Text(c, rule.Pattern).SingleLine().FontSize(12).
					TextColor(highlightColor(c, max(0, slices.Index(highlightColors, rule.Color))))
				stylePreview(preview, compiled.sgr)
			}
			ui.Row(c).Wrap().Gap(10).Children(func() {
				ui.Checkbox(c, &rule.Regex, locales.HlRegex())
				ui.Checkbox(c, &rule.CaseSensitive, locales.HlCase())
			})
			ui.Field(c, locales.HlColor(), func() { choiceSelect(c, locales.HlColor(), &rule.Color, highlightColorChoices, nil) })
			ui.Row(c).Wrap().Gap(10).Children(func() {
				ui.Checkbox(c, &rule.Bold, locales.HlBold())
				ui.Checkbox(c, &rule.Underline, locales.HlUnderline())
			})
			ui.Field(c, locales.HlScope(), func() {
				hosts := []choice[string]{{"", locales.HlAllHosts}}
				found := rule.Host == ""
				for _, host := range a.hosts.Live() {
					if !host.IsRDP() {
						hosts = append(hosts, choice[string]{host.ID, func() string { return host.Title() + " · " + host.Target() }})
						found = found || rule.Host == host.ID
					}
				}
				if !found {
					hosts = append(hosts, choice[string]{rule.Host, locales.HlMissingHost})
				}
				choiceSelect(c, locales.HlScope(), &rule.Host, hosts, nil)
			})
			ui.Spacer(c)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.closeWindow(windowHighlightRule)
				}
				if ui.PrimaryButton(c, locales.Save()).Disabled(!valid).Clicked() {
					if a.changeHighlightRule(index, original, &rule) {
						a.closeWindow(windowHighlightRule)
					}
				}
			})
		})
	})
}

func (a *App) removeHighlightRule(index int, rule config.HighlightRule) {
	go func() {
		if a.plat.Confirm(Confirmation{Title: locales.HlOptions(), Message: locales.Delete(), Detail: locales.HlDelete(rule.Pattern), OK: locales.Delete(), Cancel: locales.Cancel()}) {
			a.post(func() { a.changeHighlightRule(index, rule, nil) })
		}
	}()
}

func (a *App) changeHighlightRule(index int, original config.HighlightRule, rule *config.HighlightRule) bool {
	cfg := a.highlightSettings()
	rules := cfg.Rules
	if !cfg.UseCustom {
		rules = defaultHighlightRules()
	}
	rules = slices.Clone(rules)
	switch {
	case index < 0 && rule != nil && len(rules) < 48:
		rules = append(rules, *rule)
	case index < 0 || index >= len(rules) || rules[index] != original:
		return false
	case rule == nil:
		rules = slices.Delete(rules, index, index+1)
	default:
		rules[index] = *rule
	}
	cfg.UseCustom, cfg.Rules = true, rules
	return a.saveHighlightSettings(cfg)
}

func (a *App) saveHighlightSettings(cfg config.TerminalHighlight) bool {
	settings := a.settings
	settings.Highlight = &cfg
	if !cfg.Disabled && !cfg.UseCustom {
		settings.Highlight = nil
	}
	if !a.saveTerminalStreamSettings(settings) {
		return false
	}
	for _, s := range a.sessions {
		if s.Host == nil {
			continue
		}
		rules := highlightRules(settings, s.Host.ID)
		for _, pane := range s.panes.panes() {
			if stream := pane.stream.Load(); stream != nil {
				stream.rules.Store(&rules)
			}
		}
	}
	return true
}
