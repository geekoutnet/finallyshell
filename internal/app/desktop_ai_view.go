package app

import (
	"fmt"
	"strings"

	"finallyshell/internal/ai"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func desktopAIAccent() ui.Color {
	if theme.Dark {
		return ui.Hex("#c9b5ff")
	}
	return ui.Hex("#5c37a7")
}

func desktopAISoft() ui.Color {
	if theme.Dark {
		return ui.Hex("#28223d")
	}
	return ui.Hex("#f2edfc")
}

func aiStatus(c *ui.Context, text string, failed bool) {
	color, background := theme.TextMuted, desktopAISoft()
	if failed {
		color, background = colors.problemText, colors.dangerBanner
	}
	ui.Column(c).FillWidth().MinWidth(0).Padding(8, 10).Radius(7).Background(background).Children(func() {
		ui.Text(c, text).TextColor(color).FontSize(12).MaxWidthPercent(100)
	})
}

func (a *App) desktopAIView(c *ui.Context, window *desktopWindow) {
	if window.state == nil {
		a.desktopAIOpened(window, nil)
	}
	state := window.state.(*desktopAIState)
	a.aiCheckReady()
	settings := a.aiSettings()
	ui.Column(c).Fill().MinHeight(0).Background(theme.Background).Children(func() {
		ui.Row(c).FillWidth().Shrink(0).Height(48).PaddingX(12).Gap(7).AlignItems(ui.Center).
			Background(desktopAISoft()).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			ui.Icon(c, icons.Get("sparkles")).Size(17, 17).TextColor(desktopAIAccent())
			label := settings.Model
			if label == "" {
				label = locales.DesktopAINoModel()
			}
			ui.Text(c, label).Bold().SingleLine().MinWidth(0).Grow(1).TextColor(desktopAIAccent())
			if a.ai.ready || len(a.desktopAIChats(window.session)) > 0 {
				ui.Button(c, locales.DesktopAIHistory()).Disabled(state.cancel != nil).PopoverMenuButton(func(menu *ui.Menu) {
					chats := a.desktopAIChats(window.session)
					if len(chats) == 0 {
						menu.Item(locales.DesktopAIEmptyHistory()).Disabled(true)
					}
					for _, chat := range chats {
						if menu.Item(chat.Title).Checked(state.chat == chat).Chosen() {
							a.desktopAISelect(window.session, state, chat)
						}
					}
				})
			}
			if a.ai.ready && ui.Button(c, locales.DesktopAINew()).Disabled(state.cancel != nil).Clicked() {
				a.desktopAISelect(window.session, state, nil)
			}
			if widgets.IconButton(c, "settings", locales.DesktopAISettings()).Clicked() {
				a.openAIOptions()
			}
		})
		if !a.ai.ready && state.chat == nil {
			a.desktopAISetup(c)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().TrackScroll(&state.scroll).Children(func() {
			ui.Column(c).Gap(12).Padding(14).FillWidth().MinWidth(0).Children(func() {
				if state.chat == nil {
					a.desktopAIWelcome(c, window, state)
				} else {
					for index, message := range state.chat.Messages {
						a.desktopAIMessageView(c.Key(index), window, state, message)
					}
				}
				if a.ai.storeError {
					aiStatus(c, locales.DesktopAIHistoryFailed(), true)
				}
			})
		})
		if a.ai.ready {
			a.desktopAIComposer(c, window, state)
		}
	})
	if state.confirm != nil {
		a.desktopAIConfirmView(c, window, state)
	}
}

func (a *App) desktopAISetup(c *ui.Context) {
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(18).Children(func() {
			ui.Column(c).Width(420).MaxWidthPercent(100).Gap(9).Padding(16).AlignItems(ui.Start).
				Background(desktopAISoft()).Border(1, theme.Border).Radius(14).Children(func() {
				desktopAITile(c, 42)
				ui.Text(c, locales.DesktopAISetupTitle()).FontSize(20).Bold()
				text := locales.DesktopAISetupBody()
				if a.ai.checking {
					text = locales.AiBusy()
				} else if a.ai.keyError != "" {
					text = a.ai.keyError
				}
				widgets.Muted(c, text).MaxWidthPercent(100)
				if ui.PrimaryButton(c, locales.DesktopAISettings()).Clicked() {
					a.openAIOptions()
				}
			})
		})
	})
}

func desktopAITile(c *ui.Context, size float32) {
	ui.Box(c).Size(size, size).Radius(11).Center().Gradient(ui.Hex("#6d3cbc"), ui.Hex("#4365c9"), 135).Children(func() {
		ui.Icon(c, icons.Get("sparkles")).FontSize(size * 0.47).TextColor(ui.RGB(255, 255, 255))
	})
}

func (a *App) desktopAIWelcome(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	desktopAITile(c, 42)
	ui.Text(c, locales.DesktopAIWelcome()).FontSize(21).Bold()
	widgets.Muted(c, locales.DesktopAIWelcomeBody())
	ui.Row(c).Gap(7).Wrap().Children(func() {
		for index, title := range []string{locales.DesktopAIQuickError(), locales.DesktopAIQuickSlow(), locales.DesktopAIQuickCommand(), locales.DesktopAIQuickLog()} {
			if ui.Button(c.Key(index), title).Clicked() {
				state.draft, state.performance = title, index == 1
				if index != 2 {
					state.attach = true
					a.desktopAIPreview(window.session, state)
				}
			}
		}
	})
}

func (a *App) desktopAIMessageView(c *ui.Context, window *desktopWindow, state *desktopAIState, message *desktopAIMessage) {
	user := message.Role == "user"
	label, panel := locales.DesktopAppAI(), colors.card
	if user {
		label, panel = locales.DesktopAIYou(), desktopAISoft()
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(7).Padding(10, 12).Radius(10).Border(1, theme.Border).Background(panel).Children(func() {
		ui.Text(c, label).Bold().TextColor(desktopAIAccent()).FontSize(12)
		if user {
			ui.Text(c, message.Text).MaxWidthPercent(100)
			if message.preview != "" {
				ui.Text(c, locales.DesktopAIContextSent()).TextColor(desktopAIAccent()).FontSize(12).Tooltip(message.preview)
			}
			return
		}
		if message.Text == "" {
			if state.activeReply == message {
				widgets.Muted(c, locales.DesktopAIReceiving())
			}
			return
		}
		options := &markdown.Options{
			WrapCode: true,
			OnLink:   func(string) {},
			CodeBlock: func(c *ui.Context, block *markdown.CodeBlockView) {
				a.desktopAICode(c, window, state, block, state.activeReply == message || message.partial)
			},
		}
		markdown.View(c, message.Text, options).FillWidth().MinWidth(0)
	})
}

func (a *App) desktopAICode(c *ui.Context, window *desktopWindow, state *desktopAIState, block *markdown.CodeBlockView, streaming bool) {
	command := strings.TrimSpace(block.Code())
	canSend := ai.ShellLanguage(block.Language) && ai.CommandLine(command) && !streaming && !block.Open
	ui.Column(c).FillWidth().MinWidth(0).Radius(8).Clip().Border(1, theme.Border).Background(colors.sidebar).Children(func() {
		ui.Row(c).Gap(5).Wrap().AlignItems(ui.Center).Padding(4, 7).Children(func() {
			lang := block.Language
			if lang == "" {
				lang = locales.DesktopAICode()
			}
			ui.Text(c, ai.Limit(lang, 32)).FontSize(11).TextColor(theme.TextMuted).SingleLine().MaxWidth(88)
			ui.Spacer(c)
			target := a.desktopAITarget(window.session)
			if ui.Button(c, locales.Copy()).Disabled(streaming || block.Open).Clicked() {
				c.WriteClipboard(block.Code())
			}
			if ai.ShellLanguage(block.Language) {
				if ui.Button(c, locales.DesktopAIInsert()).Disabled(!canSend || target.term == nil).Clicked() {
					if !a.aiDeliver(window.session, target, command, false) {
						state.status, state.failed = locales.DesktopAITargetChanged(), true
					}
				}
				if ui.Button(c, locales.DesktopAIRun()).Disabled(!canSend || target.term == nil).Clicked() {
					state.confirm = &aiRunConfirmation{target: target, command: command, highRisk: ai.HighRisk(command)}
				}
			}
		})
		block.Body(c)
	})
}

func (a *App) desktopAIComposer(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	ui.Column(c).Shrink(0).FillWidth().MinWidth(0).Gap(6).Padding(8, 12).Background(colors.card).
		BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		busy := state.cancel != nil
		if state.status != "" {
			ui.Scroll(c).MaxHeight(46).FillWidth().Children(func() { aiStatus(c, state.status, state.failed) })
		}
		if state.attach && !busy {
			preview := state.preview
			if state.previewBusy {
				preview = locales.AiBusy()
			} else if preview == "" {
				preview = locales.DesktopAINoContext()
			}
			ui.Scroll(c).MaxHeight(68).FillWidth().Background(desktopAISoft()).Radius(7).Children(func() {
				ui.Column(c).Padding(6, 8).Gap(3).FillWidth().Children(func() {
					ui.Text(c, locales.DesktopAIContextHeading()).Bold().FontSize(11)
					ui.Text(c, preview).FontSize(11).MaxWidthPercent(100)
				})
			})
		}
		input := ui.TextArea(c, &state.draft).FillWidth().MinWidth(0).Height(55).Label(locales.DesktopAIQuestion()).Placeholder(locales.DesktopAIQuestion()).Disabled(busy)
		ui.Row(c).Gap(5).Wrap().AlignItems(ui.Center).Children(func() {
			if ui.Checkbox(c, &state.attach, locales.DesktopAIAttach()).Disabled(busy).Changed() {
				a.desktopAIPreview(window.session, state)
			}
			if state.attach && widgets.IconButton(c, "refresh-cw", locales.Refresh()).Disabled(busy).Clicked() {
				a.desktopAIPreview(window.session, state)
			}
			if state.chat != nil && len(state.chat.Messages) != 0 {
				ui.Checkbox(c, &state.useHistory, locales.DesktopAIUseHistory()).Disabled(busy).Tooltip(locales.DesktopAIUseHistoryNote())
			}
			ui.Spacer(c)
			if busy {
				if ui.Button(c, locales.DesktopAIStop()).Clicked() {
					a.desktopAIStop(state)
				}
			} else {
				ready := !state.previewBusy && !a.ai.loading && strings.TrimSpace(state.draft) != ""
				if ui.PrimaryButton(c, locales.DesktopAISend()).Disabled(!ready).Clicked() || ready && input.Shortcut(ui.Ctrl, ui.KeyEnter) {
					a.desktopAISend(window, state)
				}
			}
		})
	})
}

func (a *App) desktopAIConfirmView(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	confirm := state.confirm
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(460).MaxWidthPercent(90).Gap(9).Children(func() {
			ui.Text(c, locales.DesktopAIRunTitle()).FontSize(17).Bold()
			label := window.session.label()
			if confirm.target.window != nil {
				label += " · " + confirm.target.window.displayTitle()
			}
			ui.Text(c, fmt.Sprintf("%s: %s", locales.DesktopAIRunTarget(), label)).MaxWidthPercent(100)
			widgets.Muted(c, locales.DesktopAIRunWarning()).MaxWidthPercent(100)
			ui.Scroll(c).MaxHeight(120).FillWidth().Radius(6).Background(colors.sidebar).Children(func() {
				ui.Text(c, confirm.command).Font("monospace").Padding(8).MaxWidthPercent(100)
			})
			if confirm.highRisk {
				aiStatus(c, locales.DesktopAIHighRisk(), true)
				aiField(c, locales.DesktopAITypeRun(), func() {
					ui.TextInput(c, &confirm.explicit).Label(locales.DesktopAITypeRun()).FillWidth()
				})
			}
			ui.Row(c).Gap(7).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.DesktopAIRunConfirm()).Disabled(confirm.highRisk && confirm.explicit != "RUN").Clicked() {
					if !a.aiDeliver(window.session, confirm.target, confirm.command, true) {
						state.status, state.failed = locales.DesktopAITargetChanged(), true
					}
					open = false
				}
			})
		})
	})
	if !open {
		state.confirm = nil
	}
}
