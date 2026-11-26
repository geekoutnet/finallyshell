package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const pageAI = "ai"

type aiOptionsState struct {
	draft, base   config.AISettings
	endpoint, key string
	// others are the other models, one per line.
	others          string
	models          []string
	status          string
	failed, writing bool
	cancel          context.CancelFunc
	generation      int
}

func (a *App) aiSettings() config.AISettings {
	if a.settings.AI != nil {
		return *a.settings.AI
	}
	return config.AISettings{}
}

func desktopAISecret(endpoint string) string {
	if parsed, err := ai.BaseURL(endpoint); err == nil {
		endpoint = parsed.String()
	}
	return fmt.Sprintf("ai/endpoint/%x", sha256.Sum256([]byte(endpoint)))
}

func aiValidate(settings config.AISettings, model bool) error {
	if _, err := ai.BaseURL(settings.Endpoint); err != nil {
		return errors.New(locales.AiEndpointInvalid())
	}
	if model && (strings.TrimSpace(settings.Model) == "" || len(settings.Model) > 256) {
		return errors.New(locales.AiModelRequired())
	}
	if len(settings.Models) > 32 || slices.ContainsFunc(settings.Models, func(m string) bool { return len(m) > 256 }) {
		return errors.New(locales.AiTooManyModels())
	}
	return nil
}

// aiModelList reads the other models, one per line: trimmed, without
// blank lines, repeats or the default model.
func aiModelList(text, def string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != def && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func aiLocalOllama(settings config.AISettings) bool {
	base, err := ai.BaseURL(settings.Endpoint)
	return err == nil && settings.Provider == "ollama" && ai.Loopback(base)
}

func (a *App) aiCredential(ctx context.Context, settings config.AISettings, draftKey string, allowDraftLocal bool, then func(ai.Client, error)) {
	client := ai.Client{Endpoint: settings.Endpoint, Model: strings.TrimSpace(settings.Model)}
	secretID := desktopAISecret(settings.Endpoint)
	local := aiLocalOllama(settings)
	if draftKey != "" || local && allowDraftLocal {
		client.Key = draftKey
		then(client, ctx.Err())
		return
	}
	a.secrets.queue.do(func() {
		key, found, err := a.plat.SecretGet(secretID)
		if err == nil && (!found || key == "") && local {
			var enabled string
			enabled, found, err = a.plat.SecretGet(secretID + "/local")
			found = found && enabled == "yes"
		}
		a.post(func() {
			if ctx.Err() != nil {
				then(client, ctx.Err())
				return
			}
			if err != nil {
				then(client, errors.New(locales.AiKeyStoreError()))
				return
			}
			if !found || key == "" && !local {
				then(client, errors.New(locales.AiKeyRequired()))
				return
			}
			client.Key = key
			then(client, nil)
		})
	})
}

func (a *App) openAIOptions() {
	if a.optionsNav.Page != pageAI || !a.windowOpen(WindowOptions) {
		a.aiCloseOptions()
	}
	a.optionsNav.Page = pageAI
	a.openOptions()
	a.redraw()
}

func (a *App) aiCloseOptions() {
	if state := a.ai.options; state != nil {
		state.key = ""
		state.generation++
		if state.cancel != nil {
			state.cancel()
		}
	}
	a.ai.options = nil
}

func (a *App) aiOptionsPage(c *ui.Context) {
	state := a.ai.options
	if state == nil {
		settings := a.aiSettings()
		state = &aiOptionsState{base: settings, draft: settings, endpoint: settings.Endpoint, others: strings.Join(settings.Models, "\n")}
		a.ai.options = state
	}
	busy := state.cancel != nil || state.writing
	ui.Column(c).Gap(9).FillWidth().MaxWidth(640).Children(func() {
		ui.Column(c).Gap(4).Padding(12).Radius(10).Background(desktopAISoft()).Children(func() {
			ui.Text(c, locales.AiConnection()).Bold()
			widgets.Muted(c, locales.AiPrivacyNote()).FontSize(12).MaxWidthPercent(100)
		})
		aiField(c, locales.AiProvider(), func() {
			presets := ai.Presets()
			labels := make([]string, len(presets))
			current := locales.AiCustom()
			for index, preset := range presets {
				labels[index] = preset.Label
				if labels[index] == "" {
					labels[index] = locales.AiCustom()
				}
				if state.draft.Provider == preset.ID {
					current = labels[index]
				}
			}
			if ui.Select(c, &current, labels).FillWidth().Label(locales.AiProvider()).Disabled(busy).Changed() {
				for index, preset := range presets {
					if labels[index] == current {
						state.draft.Provider, state.endpoint = preset.ID, preset.Endpoint
						state.draft.Model, state.key, state.status, state.models, state.others = "", "", "", nil, ""
						break
					}
				}
			}
		})
		aiField(c, locales.AiEndpoint(), func() {
			if ui.TextInput(c, &state.endpoint).FillWidth().MinWidth(0).Label(locales.AiEndpoint()).Placeholder("https://api.openai.com/v1").Disabled(busy).Changed() {
				state.key, state.models, state.status = "", nil, ""
			}
		})
		aiField(c, locales.AiKey(), func() {
			ui.TextInput(c, &state.key).Password().FillWidth().MinWidth(0).Label(locales.AiKey()).Placeholder(locales.AiKeyHint()).Disabled(busy)
		})
		aiField(c, locales.AiModel(), func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &state.draft.Model).Grow(1).MinWidth(0).Label(locales.AiModel()).Placeholder(locales.AiModelHint()).Disabled(busy)
				if len(state.models) > 0 {
					widgets.IconButton(c, "chevron-down", locales.AiChooseModel()).Disabled(busy).PopoverMenuButton(func(menu *ui.Menu) {
						for _, model := range state.models {
							if menu.Item(model).Chosen() {
								state.draft.Model = model
							}
						}
					})
				}
				if ui.Button(c, locales.AiFetchModels()).Disabled(busy).Clicked() {
					a.aiOptionsRequest(state, true)
				}
			})
		})
		aiField(c, locales.AiOtherModels(), func() {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				ui.TextArea(c, &state.others).Lines(2, 6).Grow(1).MinWidth(0).Label(locales.AiOtherModels()).Disabled(busy)
				if len(state.models) > 0 {
					widgets.IconButton(c, "plus", locales.AiAddModel()).Disabled(busy).PopoverMenuButton(func(menu *ui.Menu) {
						listed := aiModelList(state.others, state.draft.Model)
						for _, model := range state.models {
							if menu.Item(model).Checked(slices.Contains(listed, model)).Disabled(model == state.draft.Model).Chosen() && !slices.Contains(listed, model) {
								state.others = strings.Join(append(listed, model), "\n")
							}
						}
					})
				}
			})
			widgets.Muted(c, locales.AiOtherModelsHint()).FontSize(12)
		})
		aiField(c, locales.AiSystemPrompt(), func() {
			ui.TextArea(c, &state.draft.SystemPrompt).FillWidth().Height(70).Label(locales.AiSystemPrompt()).Placeholder(locales.AiSystemHint()).Disabled(busy)
		})
		ui.Checkbox(c, &state.draft.SendContext, locales.AiSendContext()).Disabled(busy)
		sendProfile := !state.draft.NoServerProfile
		if ui.Checkbox(c, &sendProfile, locales.AiProfileSetting()).Disabled(busy).Changed() {
			state.draft.NoServerProfile = !sendProfile
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.PrimaryButton(c, locales.AiSave()).Disabled(busy).Clicked() {
				a.aiSaveOptions(state)
			}
			if ui.Button(c, locales.AiTest()).Disabled(busy).Clicked() {
				a.aiOptionsRequest(state, false)
			}
			if busy && state.cancel != nil && ui.Button(c, locales.Cancel()).Clicked() {
				state.cancel()
				state.cancel = nil
				state.generation++
				state.status = locales.DesktopAIStopped()
			}
			if ui.Button(c, locales.AiTurnOff()).Disabled(busy || state.base.Endpoint == "").Clicked() {
				a.changeSettings(func() { a.settings.AI = nil })
				a.aiCheckReady()
				a.ai.options = nil
			}
		})
		if state.status != "" {
			aiStatus(c, state.status, state.failed)
		}
	})
}

func aiField(c *ui.Context, label string, field func()) {
	ui.Column(c).Gap(4).FillWidth().MinWidth(0).Children(func() {
		ui.Text(c, label).Bold()
		field()
	})
}

func (a *App) aiSaveOptions(state *aiOptionsState) {
	settings, key := state.draft, strings.TrimSpace(state.key)
	settings.Endpoint = strings.TrimSpace(state.endpoint)
	settings.Model = strings.TrimSpace(settings.Model)
	settings.Models = aiModelList(state.others, settings.Model)
	if err := aiValidate(settings, true); err != nil {
		state.status, state.failed = err.Error(), true
		return
	}
	base, _ := ai.BaseURL(settings.Endpoint)
	settings.Endpoint = base.String()
	models := strings.Join(settings.Models, "\n")
	if key != "" && (strings.Contains(settings.Endpoint, key) || strings.Contains(settings.Model, key) || strings.Contains(models, key) || strings.Contains(settings.SystemPrompt, key)) {
		state.status, state.failed = locales.AiKeyFieldOnly(), true
		return
	}
	secretID := desktopAISecret(settings.Endpoint)
	local := aiLocalOllama(settings)
	state.writing, state.failed, state.status = true, false, locales.AiBusy()
	state.key = ""
	generation := state.generation
	a.secrets.queue.do(func() {
		var err error
		var stored string
		var found bool
		switch {
		case key != "":
			stored = key
		case local:
			stored, found, err = a.plat.SecretGet(secretID)
			if err == nil && (!found || stored == "") {
				err = a.plat.SecretSet(secretID+"/local", "yes")
			}
		default:
			stored, found, err = a.plat.SecretGet(secretID)
		}
		if err == nil && stored != "" && (strings.Contains(settings.Endpoint, stored) || strings.Contains(settings.Model, stored) || strings.Contains(models, stored) || strings.Contains(settings.SystemPrompt, stored)) {
			err = errAIKeyFieldOnly
		}
		if err == nil && key != "" {
			err = a.plat.SecretSet(secretID, key)
			found = true
		}
		a.post(func() {
			state.writing = false
			if state.generation != generation || a.Locked() {
				return
			}
			switch {
			case err == errAIKeyFieldOnly:
				state.status, state.failed = locales.AiKeyFieldOnly(), true
			case err != nil:
				state.status, state.failed = locales.AiKeyStoreError(), true
			case !local && (!found || stored == ""):
				state.status, state.failed = locales.AiKeyRequired(), true
			default:
				state.base, state.draft, state.endpoint, state.others = settings, settings, settings.Endpoint, strings.Join(settings.Models, "\n")
				state.status, state.failed = locales.AiSaved(), false
				a.changeSettings(func() { a.settings.AI = &settings })
				a.ai.readyFor, a.ai.ready, a.ai.checking, a.ai.keyError = settings, true, false, ""
			}
		})
	})
}

var errAIKeyFieldOnly = errors.New("key outside credential field")

func (a *App) aiOptionsRequest(state *aiOptionsState, models bool) {
	settings := state.draft
	settings.Endpoint = state.endpoint
	if err := aiValidate(settings, !models); err != nil {
		state.status, state.failed = err.Error(), true
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	state.generation++
	generation := state.generation
	state.cancel, state.failed, state.status = cancel, false, locales.AiBusy()
	finish := func(list []string, result string, err error) {
		if generation != state.generation {
			return
		}
		cancel()
		state.cancel, state.failed = nil, err != nil
		if err != nil {
			state.status = locales.AiRequestError() + "\n" + err.Error()
			return
		}
		if models {
			state.models = list
			state.status = locales.AiModelsFound(len(list))
		} else {
			state.status = result
		}
	}
	a.aiCredential(ctx, settings, strings.TrimSpace(state.key), true, func(client ai.Client, err error) {
		if err != nil {
			finish(nil, "", err)
			return
		}
		go func() {
			if models {
				list, err := client.Models(ctx)
				a.post(func() { finish(list, "", err) })
				return
			}
			start := time.Now()
			var reply string
			err := client.Stream(ctx, []ai.Message{{Role: "user", Content: "Reply with only OK."}}, func(delta string) {
				reply = ai.Limit(reply+delta, 1024)
			})
			millis, safe := int(time.Since(start).Milliseconds()), client.SafeText(reply)
			a.post(func() {
				if err == nil && strings.TrimSpace(safe) == "" {
					err = errors.New(locales.AiEmptyReply())
				}
				finish(nil, fmt.Sprintf("%s · %d ms\n%s", locales.AiTestOK(), millis, safe), err)
			})
		}()
	})
}
