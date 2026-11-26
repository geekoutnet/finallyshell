package app

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// The AI assistant's common tasks: prompts for what is done most often on
// a server, by category, which the user picks to fill the question in, and
// edits before sending. Their text is in desktop_ai_prompts.json, in both
// languages, as a catalog of its own.

//go:embed desktop_ai_prompts.json
var aiPromptsJSON []byte

// aiPromptCategory is a category of common tasks; each name and prompt is
// in English and in Chinese, a prompt as its title and its text.
type aiPromptCategory struct {
	ID      string `json:"id"`
	Icon    string `json:"icon"`
	En      string `json:"en"`
	Zh      string `json:"zh"`
	Prompts []struct {
		En [2]string `json:"en"`
		Zh [2]string `json:"zh"`
	} `json:"prompts"`
}

// aiPromptCatalog is the categories, read once.
var aiPromptCatalog = sync.OnceValue(func() []aiPromptCategory {
	var cats []aiPromptCategory
	if err := json.Unmarshal(aiPromptsJSON, &cats); err != nil {
		panic("desktop_ai_prompts.json: " + err.Error())
	}
	return cats
})

// aiPrompt is a common task in the language shown.
type aiPrompt struct {
	category, title, text string
}

// aiChinese reports whether the app shows Chinese.
func aiChinese() bool { return strings.HasPrefix(i18n.Locale(), "zh") }

func (cat *aiPromptCategory) name() string {
	if aiChinese() {
		return cat.Zh
	}
	return cat.En
}

// aiPrompts are the common tasks of the category with the ID, every one
// for "", that the search finds.
func aiPrompts(category, query string) []aiPrompt {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []aiPrompt
	for i := range aiPromptCatalog() {
		cat := &aiPromptCatalog()[i]
		if category != "" && cat.ID != category {
			continue
		}
		for _, p := range cat.Prompts {
			pair := p.En
			if aiChinese() {
				pair = p.Zh
			}
			if q != "" && !strings.Contains(strings.ToLower(pair[0]+" "+pair[1]), q) {
				continue
			}
			out = append(out, aiPrompt{category: cat.ID, title: pair[0], text: pair[1]})
		}
	}
	return out
}

// desktopAIPromptsButton opens the common tasks.
func desktopAIPromptsButton(c *ui.Context, state *desktopAIState, disabled bool) {
	b := ui.Button(c, "").Label(locales.DesktopAIPrompts()).Tooltip(locales.DesktopAIPrompts()).Disabled(disabled)
	b.Children(func() {
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("wand-sparkles")).FontSize(13).TextColor(desktopAIAccent())
			ui.Text(c, locales.DesktopAIPrompts())
		})
	})
	if b.Clicked() {
		state.prompts, state.promptQuery = true, ""
	}
}

// desktopAIPromptsView is the common tasks, by category, with a search;
// a click on one puts it in the question, to edit and send.
func (a *App) desktopAIPromptsView(c *ui.Context, state *desktopAIState) {
	if !state.prompts {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(780).MaxWidthPercent(94).Height(520).MaxHeightPercent(90).Gap(10).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Icon(c, icons.Get("wand-sparkles")).FontSize(17).TextColor(desktopAIAccent())
				ui.Text(c, locales.DesktopAIPrompts()).FontSize(17).Bold()
				ui.Spacer(c)
				ui.SearchField(c, &state.promptQuery).Placeholder(locales.DesktopAIPromptsSearch()).
					Label(locales.DesktopAIPromptsSearch()).Width(260).Shrink(1).MinWidth(120)
			})
			widgets.Muted(c, locales.DesktopAIPromptsNote()).FontSize(12)
			ui.Row(c).Grow(1).MinHeight(0).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
				a.desktopAIPromptCategories(c, state)
				prompts := aiPrompts(state.promptCat, state.promptQuery)
				ui.Scroll(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
					ui.Column(c).Gap(8).FillWidth().Padding(0, 6, 0, 0).Children(func() {
						if len(prompts) == 0 {
							widgets.Muted(c, locales.DesktopAIPromptsEmpty()).Padding(12)
						}
						for i, p := range prompts {
							if desktopAIPromptCard(c.Key(i), p) {
								state.draft, state.prompts = p.text, false
							}
						}
					})
				})
			})
		})
	})
	if !open {
		state.prompts = false
	}
}

// desktopAIPromptCategories are the categories, every one first, each
// with how many tasks it has.
func (a *App) desktopAIPromptCategories(c *ui.Context, state *desktopAIState) {
	ui.Scroll(c).Width(190).Shrink(0).MinHeight(0).Children(func() {
		ui.Column(c).Gap(2).FillWidth().Padding(0, 12, 0, 0).Children(func() {
			item := func(id, icon, name string, n int) {
				selected := state.promptCat == id
				row := ui.Row(c.Key("cat:"+id)).Gap(8).Padding(6, 8).Radius(6).FillWidth().AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Role(ui.RoleTab).Label(name)
				switch {
				case selected:
					row.Background(theme.Accent.Alpha(0.14)).Checked(true)
				case row.Hovered():
					row.Background(theme.Text.Alpha(0.06))
				}
				if row.Clicked() {
					state.promptCat = id
				}
				row.Children(func() {
					color := theme.TextMuted
					if selected {
						color = theme.Accent
					}
					ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(color).Shrink(0)
					label := ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
					if selected {
						label.FontWeight(600)
					}
					widgets.Muted(c, strconv.Itoa(n)).FontSize(11).Shrink(0)
				})
			}
			item("", "layout-grid", locales.DesktopAIPromptsAll(), len(aiPrompts("", state.promptQuery)))
			for i := range aiPromptCatalog() {
				cat := &aiPromptCatalog()[i]
				item(cat.ID, cat.Icon, cat.name(), len(aiPrompts(cat.ID, state.promptQuery)))
			}
		})
	})
}

// desktopAIPromptCard is a common task: its title over its text. It
// reports whether the user chose it.
func desktopAIPromptCard(c *ui.Context, p aiPrompt) bool {
	card := ui.Column(c).Gap(4).Padding(10, 12).Radius(8).FillWidth().Border(1, theme.Border).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(p.title).Tooltip(p.text)
	if card.Hovered() {
		card.Background(desktopAISoft()).BorderColor(desktopAIAccent().Alpha(0.5))
	} else {
		card.Background(colors.card)
	}
	card.Children(func() {
		ui.Text(c, p.title).FontSize(13).Bold().MaxWidthPercent(100)
		widgets.Muted(c, p.text).FontSize(12).MaxWidthPercent(100)
	})
	return card.Clicked()
}
