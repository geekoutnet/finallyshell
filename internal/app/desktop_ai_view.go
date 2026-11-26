package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
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

// desktopAISidebarMin is the least width of the window that shows the
// conversations beside the chat.
const desktopAISidebarMin = 620

// aiInputMinH and aiInputMaxH bound the height the user drags the
// question's field to.
const (
	aiInputMinH = 40
	aiInputMaxH = 420
)

func (a *App) desktopAIView(c *ui.Context, window *desktopWindow) {
	if window.state == nil {
		a.desktopAIOpened(window, nil)
	}
	state := window.state.(*desktopAIState)
	a.aiCheckReady()
	if state.submit {
		state.submit = false
		a.desktopAISend(window, state)
	}
	settings := a.aiSettings()
	root := ui.Column(c).Fill().MinHeight(0).Background(theme.Background)
	wide := root.Bounds().W == 0 || root.Bounds().W >= desktopAISidebarMin
	root.Children(func() {
		a.desktopAITopBar(c, window, state, settings, wide)
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
			if wide && state.sidebar && (a.ai.ready || len(a.desktopAIChats(window.session)) > 0) {
				a.desktopAISidebar(c, window, state)
			}
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if !a.ai.ready && state.chat == nil {
					a.desktopAISetup(c)
					return
				}
				ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().TrackScroll(&state.scroll).Children(func() {
					ui.Column(c).Gap(10).Padding(14).FillWidth().MinWidth(0).Children(func() {
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
		})
	})
	if state.confirm != nil {
		a.desktopAIConfirmView(c, window, state)
	}
	a.desktopAIPromptsView(c, state)
	if chat := state.deleting; chat != nil {
		open := true
		ui.Modal(c, &open, func() {
			ui.Column(c).Width(400).MaxWidthPercent(90).Gap(10).Children(func() {
				ui.Text(c, locales.DesktopAIDeleteChatTitle()).FontSize(16).Bold()
				widgets.Muted(c, locales.DesktopAIDeleteChatBody(chat.Title)).MaxWidthPercent(100)
				ui.Row(c).Gap(7).Justify(ui.End).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						open = false
					}
					if ui.PrimaryButton(c, locales.Delete()).Clicked() {
						a.desktopAIDelete(window, chat)
						open = false
					}
				})
			})
		})
		if !open {
			state.deleting = nil
		}
	}
}

// desktopAITopBar shows the server the assistant works on and the model it
// answers with, which the user switches between.
func (a *App) desktopAITopBar(c *ui.Context, window *desktopWindow, state *desktopAIState, settings config.AISettings, wide bool) {
	busy := state.cancel != nil
	ui.Row(c).FillWidth().Shrink(0).Height(44).PaddingX(10).Gap(6).AlignItems(ui.Center).
		Background(desktopAISoft()).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		if wide {
			if widgets.ToggleIconButton(c, "columns-2", locales.DesktopAIToggleSidebar(), state.sidebar).Clicked() {
				state.sidebar = !state.sidebar
			}
		}
		label := window.session.label()
		ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(1).MinWidth(0).Tooltip(label).Children(func() {
			ui.Icon(c, icons.Get("server")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
			ui.Text(c, label).Bold().SingleLine().MinWidth(0).Shrink(1)
		})
		if a.ai.ready {
			a.desktopAIModelPicker(c, state, settings, busy)
		}
		ui.Spacer(c)
		if !wide && (a.ai.ready || len(a.desktopAIChats(window.session)) > 0) {
			ui.Button(c, locales.DesktopAIHistory()).PopoverMenuButton(func(menu *ui.Menu) {
				chats := a.desktopAIChats(window.session)
				if len(chats) == 0 {
					menu.Item(locales.DesktopAIEmptyHistory()).Disabled(true)
				}
				for _, chat := range chats {
					if menu.Item(chat.Title).Checked(state.chat == chat).Chosen() {
						a.desktopAIOpenChat(window, chat)
					}
				}
			})
		}
		if a.ai.ready {
			if wide {
				if ui.Button(c, locales.DesktopAINew()).Clicked() {
					a.desktopAIOpenChat(window, nil)
				}
			} else if widgets.IconButton(c, "plus", locales.DesktopAINew()).Clicked() {
				a.desktopAIOpenChat(window, nil)
			}
		}
		if widgets.IconButton(c, "settings", locales.DesktopAISettings()).Clicked() {
			a.openAIOptions()
		}
	})
}

// desktopAIModelPicker is the model the conversation uses, a click
// choosing another of those the settings list.
func (a *App) desktopAIModelPicker(c *ui.Context, state *desktopAIState, settings config.AISettings, busy bool) {
	current := aiModelOf(settings, state)
	label := current
	if label == "" {
		label = locales.DesktopAINoModel()
	}
	picker := ui.Row(c.Key("model")).Gap(4).AlignItems(ui.Center).Padding(3, 8).Radius(6).Border(1, theme.Border).Background(colors.card).
		Shrink(1).MinWidth(0).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.DesktopAIModel() + ": " + label).Tooltip(locales.DesktopAIModel()).Disabled(busy)
	picker.Children(func() {
		ui.Icon(c, icons.Get("bot")).FontSize(13).TextColor(desktopAIAccent()).Shrink(0)
		ui.Text(c, label).FontSize(12).FontWeight(600).SingleLine().MinWidth(0).Shrink(1).TextColor(desktopAIAccent())
		ui.Icon(c, icons.Get("chevron-down")).FontSize(11).TextColor(theme.TextMuted).Shrink(0)
	})
	picker.PopoverMenuButton(func(menu *ui.Menu) {
		if menu.Item(locales.DesktopAIDefaultModel(settings.Model)).Checked(current == settings.Model).Chosen() {
			state.model = ""
		}
		for _, model := range settings.Models {
			if menu.Item(model).Checked(current == model).Chosen() {
				state.model = model
			}
		}
		menu.Separator()
		if menu.Item(locales.DesktopAISettings()).Chosen() {
			a.openAIOptions()
		}
	})
}

// desktopAISidebar lists the server's conversations, the chosen one
// highlighted.
func (a *App) desktopAISidebar(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	ui.Column(c).Width(196).Shrink(0).Background(colors.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Row(c).Padding(8, 8, 4, 12).Gap(4).AlignItems(ui.Center).FillWidth().Children(func() {
			widgets.Muted(c, locales.DesktopAIConversations()).FontSize(12).Bold().Grow(1).MinWidth(0)
			if a.ai.ready && widgets.IconButton(c, "plus", locales.DesktopAINew()).Clicked() {
				a.desktopAIOpenChat(window, nil)
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Gap(2).Padding(0, 6, 8, 6).FillWidth().Children(func() {
				chats := a.desktopAIChats(window.session)
				if len(chats) == 0 {
					widgets.Muted(c, locales.DesktopAIEmptyHistory()).FontSize(12).Padding(6)
				}
				for index, chat := range chats {
					selected := state.chat == chat
					row := ui.Row(c.Key(index)).Gap(6).Padding(4, 4, 4, 8).Radius(6).FillWidth().AlignItems(ui.Center).MinHeight(28).
						Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(chat.Title).Tooltip(chat.Title)
					// A conversation answering behind the one shown, or waiting
					// for the user to allow a command.
					conv := a.aiConvOf(window, chat)
					running, waiting := conv != nil && aiBusy(conv), conv != nil && conv.confirm != nil
					hovered := row.Hovered()
					switch {
					case selected:
						row.Background(theme.Accent.Alpha(0.16))
					case hovered:
						row.Background(theme.Text.Alpha(0.06))
					}
					deletable := !a.aiChatBusy(chat)
					row.ContextMenu(func(m *ui.Menu) {
						if m.Item(locales.DesktopAIDeleteChat()).Danger().Disabled(!deletable).Chosen() {
							state.deleting = chat
						}
					})
					if row.Clicked() && !selected {
						a.desktopAIOpenChat(window, chat)
					}
					row.Children(func() {
						switch {
						case waiting:
							ui.Icon(c, icons.Get("circle-alert")).FontSize(12).TextColor(theme.Warning).Shrink(0).Tooltip(locales.DesktopAIChatWaiting())
						case running:
							ui.Spinner(c).Size(12, 12).Shrink(0).Label(locales.DesktopAIChatRunning())
						default:
							ui.Icon(c, icons.Get("message-square")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
						}
						ui.Text(c, chat.Title).FontSize(12).SingleLine().MinWidth(0).Grow(1).Shrink(1)
						// Always there, to keep the titles from moving, and shown
						// over the row the pointer is on.
						del := widgets.IconButton(c.Key("delete"), "trash-2", locales.DesktopAIDeleteChat()).Disabled(!deletable)
						if !hovered && !selected {
							del.Opacity(0)
						}
						if del.Clicked() && deletable {
							state.deleting = chat
						}
					})
				}
			})
		})
	})
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
	widgets.Muted(c, locales.DesktopAIWelcomeBody()).MaxWidthPercent(100)
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
		// Every common task, by category.
		more := ui.Button(c.Key("more"), "").Label(locales.DesktopAIPromptsMore())
		more.Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("wand-sparkles")).FontSize(13).TextColor(desktopAIAccent())
				ui.Text(c, locales.DesktopAIPromptsMore()).TextColor(desktopAIAccent())
			})
		})
		if more.Clicked() {
			state.prompts, state.promptQuery = true, ""
		}
	})
}

func (a *App) desktopAIMessageView(c *ui.Context, window *desktopWindow, state *desktopAIState, message *desktopAIMessage) {
	switch message.Role {
	case "command":
		a.desktopAICommandView(c, state, message)
		return
	case "notice":
		// The earlier part summarized: a quiet line where it ends.
		ui.Row(c).FillWidth().Justify(ui.Center).Gap(6).AlignItems(ui.Center).Padding(2, 0).Children(func() {
			ui.Icon(c, icons.Get("archive")).FontSize(12).TextColor(theme.TextMuted)
			widgets.Muted(c, message.Text).FontSize(11).Tooltip(locales.AiContextCompactedNote())
		})
		return
	case "user":
		// The user's question, on the right, with copying it and editing it
		// under it.
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			bubble := ui.Column(c).MaxWidthPercent(85).MinWidth(0).Gap(4).Padding(8, 12).Radius(10).Background(desktopAISoft())
			if state.editing == message {
				bubble.Border(1, desktopAIAccent())
			}
			bubble.Children(func() {
				ui.Text(c, message.Text).MaxWidthPercent(100).Selectable()
				if message.preview != "" {
					ui.Text(c, locales.DesktopAIContextSent()).TextColor(desktopAIAccent()).FontSize(11).Tooltip(message.preview)
				}
			})
		})
		ui.Row(c).FillWidth().Justify(ui.End).Gap(2).Margin(-6, 0, 0, 0).Children(func() {
			a.desktopAICopyButton(c, state, message)
			if widgets.IconButton(c, "pencil", locales.DesktopAIEditQuestion()).Disabled(state.cancel != nil).Clicked() {
				state.draft, state.editing, state.focusInput = message.Text, message, true
			}
		})
		return
	}
	streaming := state.activeReply == message
	ui.Column(c).FillWidth().MinWidth(0).Gap(6).Padding(10, 12).Radius(10).Border(1, theme.Border).Background(colors.card).Children(func() {
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("sparkles")).FontSize(12).TextColor(desktopAIAccent())
			ui.Text(c, locales.DesktopAppAI()).Bold().TextColor(desktopAIAccent()).FontSize(12)
			if message.Model != "" {
				widgets.Muted(c, "· "+message.Model).FontSize(11).SingleLine().MinWidth(0).Shrink(1)
			}
		})
		if message.Text == "" {
			if state.activeReply == message {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12)
					widgets.Muted(c, locales.DesktopAIReceiving()).FontSize(12)
				})
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
		if streaming {
			return
		}
		// Copying the reply, and, for the chat's last, asking its question
		// again for another.
		ui.Row(c).Gap(2).Margin(0, -4, -4, -4).Children(func() {
			a.desktopAICopyButton(c, state, message)
			if question := aiLastQuestion(state.chat, message); question != nil {
				if widgets.IconButton(c, "refresh-cw", locales.DesktopAIRegenerate()).Disabled(state.cancel != nil).Clicked() {
					a.desktopAIRedo(window, state, question)
				}
			}
		})
	})
}

// aiLastQuestion is the question reply answers when reply is chat's last
// message from the model: nil for any other.
func aiLastQuestion(chat *desktopAIChat, reply *desktopAIMessage) *desktopAIMessage {
	if chat == nil {
		return nil
	}
	var question, last *desktopAIMessage
	for _, m := range chat.Messages {
		switch m.Role {
		case "user":
			question, last = m, nil
		case "assistant":
			last = m
		}
	}
	if last != reply {
		return nil
	}
	return question
}

// desktopAICopyButton copies message's text, showing a check for a moment
// once it has.
func (a *App) desktopAICopyButton(c *ui.Context, state *desktopAIState, message *desktopAIMessage) {
	icon, label := "copy", locales.DesktopAICopyMessage()
	if state.copied == message {
		icon, label = "check", locales.DesktopAICopied()
	}
	if widgets.IconButton(c, icon, label).Clicked() {
		c.WriteClipboard(message.Text)
		state.copied = message
		time.AfterFunc(1500*time.Millisecond, func() {
			a.post(func() {
				if state.copied == message {
					state.copied = nil
				}
			})
		})
	}
}

// desktopAICommandView is a command the model ran on the server: its
// state, its output, and, while it waits, the user's answer.
func (a *App) desktopAICommandView(c *ui.Context, state *desktopAIState, card *desktopAIMessage) {
	icon, color, status := "x", theme.TextMuted, ""
	switch card.Status {
	case aiCmdWaiting:
		icon, color, status = "shield-alert", theme.Warning, locales.DesktopAICmdWaiting()
	case aiCmdQueued, aiCmdRunning:
		icon, color, status = "", desktopAIAccent(), locales.DesktopAICmdRunning()
		if d := card.deadline; d != nil && card.Status == aiCmdRunning {
			status = locales.DesktopAICmdRunningFor(aiDuration(time.Since(d.start)), aiDuration(d.limit()))
		}
	case aiCmdDone:
		icon, color = "circle-check", theme.Success
		exit := 0
		if card.Exit != nil {
			exit = *card.Exit
		}
		if exit != 0 {
			icon, color = "circle-x", theme.Danger
		}
		status = locales.DesktopAICmdExit(strconv.Itoa(exit), aiElapsed(card.Millis))
	case aiCmdTimedOut:
		icon, color, status = "timer", theme.Warning, locales.DesktopAICmdTimedOut(aiDuration(time.Duration(card.Millis)*time.Millisecond))
	case aiCmdRefused:
		status = locales.DesktopAICmdRefused()
	case aiCmdCancelled:
		status = locales.DesktopAICmdCancelled()
	case aiCmdFailed:
		icon, color, status = "triangle-alert", theme.Danger, locales.DesktopAICmdFailed(card.err)
	}
	border := theme.Border
	if card.Status == aiCmdWaiting {
		border = theme.Warning
	}
	ui.Column(c).FillWidth().MinWidth(0).Radius(8).Clip().Border(1, border).Background(colors.sidebar).Children(func() {
		ui.Row(c).Gap(7).Padding(6, 6, 6, 9).AlignItems(ui.Center).FillWidth().Children(func() {
			if icon == "" {
				ui.Spinner(c).Size(14, 14).Shrink(0)
			} else {
				ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(color).Shrink(0)
			}
			ui.Text(c, "$ "+card.Text).Font("monospace").FontSize(12).SingleLine().Grow(1).MinWidth(0).Tooltip(card.Text)
			widgets.Muted(c, status).FontSize(11).SingleLine().Shrink(1).MinWidth(0).MaxWidth(220).Tooltip(status).TextColor(color)
			if card.output != "" || card.Status == aiCmdDone || card.Status == aiCmdTimedOut {
				if card.Status.finished() && (card.output != "" || card.Status != aiCmdFailed) {
					toggle, label := "chevron-down", locales.DesktopAICmdShowOutput()
					if card.showOutput {
						toggle, label = "chevron-up", locales.DesktopAICmdHideOutput()
					}
					if widgets.IconButton(c, toggle, label).Clicked() {
						card.showOutput = !card.showOutput
					}
				}
			}
			// A long scan the user wants to see the end of goes on.
			if d := card.deadline; d != nil && card.Status == aiCmdRunning && d.limit() < aiMaxRunTime {
				if ui.Button(c, locales.DesktopAICmdWaitLonger()).Shrink(0).Tooltip(locales.DesktopAICmdWaitLongerNote()).Clicked() {
					d.extend(aiWaitLonger)
				}
			}
			if widgets.IconButton(c, "copy", locales.Copy()).Clicked() {
				c.WriteClipboard(card.Text)
			}
		})
		var notes []string
		if card.Reason != "" {
			notes = append(notes, card.Reason)
		}
		if card.Auto && card.Status.finished() {
			notes = append(notes, locales.DesktopAICmdAuto())
		} else if card.Status == aiCmdDone || card.Status == aiCmdTimedOut {
			notes = append(notes, locales.DesktopAICmdApproved())
		}
		if len(notes) > 0 {
			widgets.Muted(c, strings.Join(notes, " · ")).FontSize(11).Padding(0, 9, 6, 30).MaxWidthPercent(100)
		}
		if card.Status == aiCmdWaiting && state.run != nil {
			ui.Column(c).Gap(6).Padding(0, 9, 9, 30).FillWidth().MinWidth(0).Children(func() {
				switch {
				case card.cut != ai.CutNone:
					aiStatus(c, locales.AiGuardCutConfirm(), true)
				case card.denyRule != "":
					aiStatus(c, locales.AiGuardRuleConfirm(card.denyRule), false)
				}
				if card.highRisk {
					if card.cut == ai.CutNone {
						aiStatus(c, locales.DesktopAIHighRisk(), true)
					}
					ui.TextInput(c, &card.confirm).Label(locales.DesktopAITypeRun()).Placeholder(locales.DesktopAITypeRun()).Width(200)
				}
				ui.Row(c).Gap(6).Children(func() {
					if ui.PrimaryButton(c, locales.DesktopAICmdAllow()).Disabled(card.highRisk && card.confirm != "RUN").Clicked() {
						a.aiAnswer(state, card, true)
					}
					if ui.Button(c, locales.DesktopAICmdRefuse()).Clicked() {
						a.aiAnswer(state, card, false)
					}
				})
			})
		}
		if card.showOutput {
			ui.Scroll(c).MaxHeight(260).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Background(theme.Background).Children(func() {
				text := card.output
				if strings.TrimSpace(text) == "" {
					text = locales.DesktopAICmdNoOutput()
				}
				ui.Text(c, text).Font("monospace").FontSize(11).Padding(7, 10).MaxWidthPercent(100).Selectable()
			})
		}
	})
}

// aiDuration is a command's time to the second: 45s, 2m05s, 1h00m.
func aiDuration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
}

// aiElapsed is how long a command ran.
func aiElapsed(millis int64) string {
	if millis < 1000 {
		return fmt.Sprintf("%d ms", millis)
	}
	return fmt.Sprintf("%.1f s", float64(millis)/1000)
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

// desktopAIComposer is where the user asks: the context to attach, folded
// to its first line, the question, which Enter sends and Shift+Enter
// breaks, and the conversation's options.
func (a *App) desktopAIComposer(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	busy := state.cancel != nil
	settings := a.aiSettings()
	noTools := a.ai.noTools[aiToolsKey(ai.Client{Endpoint: settings.Endpoint, Model: aiModelOf(settings, state)})]
	agent := aiServer(window.session) != nil && !noTools
	ui.Column(c).Shrink(0).FillWidth().MinWidth(0).Gap(6).Padding(8, 12, 10, 12).Background(colors.card).
		BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		if state.chat == nil || !slices.Contains(state.chat.Messages, state.retry) {
			state.retry = nil
		}
		if state.chat == nil || !slices.Contains(state.chat.Messages, state.editing) {
			state.editing = nil
		}
		if state.status != "" {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
				ui.Scroll(c).MaxHeight(46).Grow(1).MinWidth(0).Children(func() { aiStatus(c, state.status, state.failed) })
				if state.failed && state.retry != nil && !busy && ui.Button(c, locales.Retry()).Shrink(0).Clicked() {
					a.desktopAIRedo(window, state, state.retry)
				}
			})
		}
		if state.editing != nil {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("pencil")).FontSize(12).TextColor(desktopAIAccent())
				widgets.Muted(c, locales.DesktopAIEditing()).FontSize(12).Grow(1).MinWidth(0).Shrink(1)
				if widgets.IconButton(c, "x", locales.Cancel()).Clicked() {
					state.editing, state.draft = nil, ""
				}
			})
		}
		switch {
		case aiServer(window.session) == nil:
			widgets.Muted(c, locales.DesktopAIAgentOff()).FontSize(11).MaxWidthPercent(100)
		case noTools:
			widgets.Muted(c, locales.DesktopAINoTools()).FontSize(11).MaxWidthPercent(100)
		}
		if state.attach && !busy {
			desktopAIContextChip(c, state)
		}
		// The bar above the question: dragging it makes the field taller or
		// shorter, a double click lets it grow with its text again.
		grip := ui.Box(c.Key("input-grip")).FillWidth().Height(8).Margin(-4, 0, -2, 0).Cursor(ui.CursorResizeNS).
			Role(ui.RoleSplitter).Label(locales.DesktopAIResizeInput()).Tooltip(locales.DesktopAIResizeInput())
		input := ui.TextArea(c, &state.draft).FillWidth().MinWidth(0).Label(locales.DesktopAIQuestion()).
			Placeholder(locales.DesktopAIComposerHint()).Disabled(busy).OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods != 0 {
				return false // Shift+Enter breaks the line
			}
			if strings.TrimSpace(k.Text()) != "" {
				state.submit = true
			}
			return true
		})
		if state.inputH > 0 {
			input.Height(state.inputH)
		} else {
			input.Lines(2, 8)
		}
		if state.focusInput {
			state.focusInput = false
			input.Focus()
		}
		if grip.DoubleClicked() {
			state.inputH = 0
		} else if _, dy, held := grip.Dragged(); held && dy != 0 {
			h := state.inputH
			if h <= 0 {
				h = input.Bounds().H
			}
			state.inputH = desktopClamp(h-dy, aiInputMinH, aiInputMaxH)
		}
		grip.Draw(func(p *ui.Painter, r ui.Rect) {
			color := theme.Border
			if grip.Hovered() || grip.Pressed() {
				color = theme.Accent
			}
			p.Fill(ui.Rect{X: r.X + r.W/2 - 16, Y: r.Y + r.H/2 - 1, W: 32, H: 2}, color, 1)
		})
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
			if agent {
				ui.Checkbox(c, &state.autoRun, locales.DesktopAIAutoRun()).Tooltip(locales.DesktopAIAutoRunNote())
			}
			ui.Spacer(c)
			desktopAIPromptsButton(c, state, busy)
			if busy {
				if ui.Button(c, locales.DesktopAIStop()).Clicked() {
					a.desktopAIStop(state)
				}
			} else {
				ready := !state.previewBusy && !a.ai.loading && strings.TrimSpace(state.draft) != ""
				if ui.PrimaryButton(c, locales.DesktopAISend()).Disabled(!ready).Clicked() {
					a.desktopAISend(window, state)
				}
			}
		})
	})
}

// desktopAIContextChip is the context a question sends: its first line,
// a click showing all of it.
func desktopAIContextChip(c *ui.Context, state *desktopAIState) {
	preview := state.preview
	if state.previewBusy {
		preview = locales.AiBusy()
	} else if preview == "" {
		preview = locales.DesktopAINoContext()
	}
	first := ""
	for _, line := range strings.Split(preview, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			first = line
			break
		}
	}
	ui.Column(c).FillWidth().MinWidth(0).Radius(7).Background(desktopAISoft()).Children(func() {
		label, chevron := locales.DesktopAIContextShow(), "chevron-right"
		if state.showContext {
			label, chevron = locales.DesktopAIContextHide(), "chevron-down"
		}
		row := ui.Row(c).Gap(6).Padding(4, 8).AlignItems(ui.Center).FillWidth().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
		if !state.showContext {
			row.Tooltip(preview)
		}
		if row.Clicked() {
			state.showContext = !state.showContext
		}
		row.Children(func() {
			ui.Icon(c, icons.Get(chevron)).FontSize(11).TextColor(theme.TextMuted).Shrink(0)
			ui.Text(c, locales.DesktopAIContextHeading()).Bold().FontSize(11).SingleLine().Shrink(0)
			if !state.showContext {
				widgets.Muted(c, first).FontSize(11).SingleLine().MinWidth(0).Shrink(1)
			}
		})
		if state.showContext {
			ui.Scroll(c).MaxHeight(120).FillWidth().Children(func() {
				ui.Text(c, preview).FontSize(11).Padding(0, 8, 6, 25).MaxWidthPercent(100)
			})
		}
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
