package app

import (
	"bytes"
	"encoding/json"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type caddyAction struct {
	label, reason string
	invoke        func()
}

func caddyActions(c *ui.Context, actions ...caddyAction) {
	bar := ui.Row(c).FillWidth().Shrink(0).Gap(5).AlignItems(ui.Center)
	narrow := bar.LayoutBounds().W > 0 && bar.LayoutBounds().W < 640
	bar.ContextMenu(func(menu *ui.Menu) { caddyMenu(menu, actions...) })
	bar.Children(func() {
		visible := actions
		if narrow && len(actions) > 1 {
			ui.MenuButton(c, locales.SvcActions(), func(menu *ui.Menu) { caddyMenu(menu, actions...) }).MarginStart(8).Shrink(0)
			visible = actions[:1]
			for index, action := range actions {
				if action.reason == "" {
					visible = actions[index : index+1]
					break
				}
			}
		}
		row := ui.Row(c).Padding(4, 8).Gap(6)
		row.Children(func() {
			for _, action := range visible {
				button := ui.Button(c, action.label).Shrink(0).Disabled(action.reason != "")
				if action.reason != "" {
					button.Tooltip(action.reason)
				}
				if button.Clicked() && action.reason == "" {
					action.invoke()
				}
			}
		})
	})
}

func caddyCompactSections(c *ui.Context, section *int) {
	names := caddySectionNames()
	ui.Button(c, names[*section]+" ▾").Label(locales.TabCaddy()).Grow(1).MinWidth(0).PopoverMenuButton(func(menu *ui.Menu) {
		for index, name := range names {
			if menu.Item(name).Radio(index == *section).Chosen() {
				*section = index
			}
		}
	})
}

func (a *App) caddyDialogView(c *ui.Context, s *Session, slot **systemDialog) {
	dialog := *slot
	if dialog == nil {
		return
	}
	open := systemReady(s, dialog.client)
	if open {
		ui.Modal(c, &open, func() {
			width, height := c.Size()
			ui.Column(c).Width(min(max(width-56, 260), 720)).Gap(9).Children(func() {
				ui.Text(c, dialog.title).FontSize(17).Bold()
				ui.Scroll(c).FillWidth().MaxHeight(max(80, height-150)).Children(func() {
					ui.Column(c).Gap(8).FillWidth().Children(func() {
						if dialog.target != "" && !dialog.busy {
							ui.Text(c, dialog.target).Selectable().FillWidth()
						}
						if dialog.consequence != "" && !dialog.done && !dialog.busy {
							ui.Text(c, dialog.consequence).FillWidth()
						}
						if dialog.body != nil && !dialog.done {
							dialog.body(c, height)
						}
						if dialog.busy {
							ui.Spinner(c).Label(locales.SvcWorking())
						}
						if dialog.output != "" || dialog.failure != "" {
							output := strings.TrimSpace(strings.Join([]string{dialog.output, dialog.failure}, "\n"))
							ui.TextArea(c, &output).ReadOnly(true).Font("monospace").FillWidth().Height(min(260, height/3)).Label(locales.SvcOutput())
						}
						if dialog.rootDenied {
							privilegeHelp(c, systemUser(s))
						}
					})
				})
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					label := locales.Cancel()
					if dialog.done || !dialog.busy && dialog.confirm == nil && dialog.consequence == "" {
						label = locales.Close()
					}
					if ui.Button(c, label).Clicked() {
						if dialog.busy && dialog.cancel != nil {
							dialog.cancel()
						} else {
							open = false
						}
					}
					if dialog.confirm != nil && !dialog.busy && ui.PrimaryButton(c, locales.SvcConfirm()).Clicked() {
						confirm := dialog.confirm
						dialog.confirm = nil
						confirm()
					}
				})
			})
		})
	}
	if !open && *slot == dialog {
		if dialog.cancel != nil {
			dialog.cancel()
		}
		*slot = nil
	}
}

func caddyMenu(menu *ui.Menu, actions ...caddyAction) {
	for _, action := range actions {
		if menu.Item(action.label).Disabled(action.reason != "").Subtitle(action.reason).Chosen() && action.reason == "" {
			action.invoke()
		}
	}
}

func caddyNotice(c *ui.Context, message string) {
	if message != "" {
		ui.Text(c, message).Padding(4, 8).FillWidth().SingleLine().Tooltip(message).TextColor(theme.Warning)
	}
}

func (a *App) caddyRefresh(s *Session) {
	if dialog := s.caddy.state.dialog; dialog != nil && (dialog.busy || dialog.confirm != nil) {
		return
	}
	a.caddyConnection(s)
	a.invalidateCaddy(s)
	a.detectCaddyPanel(s)
}

func caddyPrettyJSON(text string) (string, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(strings.TrimSpace(text)), "", "  "); err != nil {
		return "", err
	}
	return pretty.String(), nil
}

func caddyInstallName(method string) string {
	switch method {
	case monitor.CaddyAPT:
		return locales.CpanelMethodApt()
	case monitor.CaddyDNF:
		return locales.CpanelMethodDnf()
	case monitor.CaddyPacman:
		return locales.CpanelMethodPacman()
	case monitor.CaddyAPK:
		return locales.CpanelMethodApk()
	case monitor.CaddyBinary:
		return locales.CpanelMethodBinary()
	case monitor.CaddyDocker:
		return "Docker"
	}
	return locales.CpanelUnknown()
}

func caddyReadOnlyBody(file, text string) func(*ui.Context, float32) {
	code := editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true}
	code.Buffer.SetLanguage(editor.LanguageFor(file))
	return func(c *ui.Context, height float32) {
		editor.View(c, &code).Height(min(440, height/2)).FillWidth()
	}
}
