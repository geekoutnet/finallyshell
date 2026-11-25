package app

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"finallyshell/internal/remotefs"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
)

func (a *App) desktopFilesOpenPlace(w *desktopWindow, target string) {
	sc, f := w.session.sftp, w.explorer()
	if sc == nil || !desktopFilesPath(target) || f.job != nil {
		return
	}
	var item desktopFilesItem
	a.desktopFilesWork(w, locales.OpenFile()+": "+target, false, nil, func(ctx context.Context, _ func(string)) error {
		info, err := sc.Stat(target)
		if err == nil {
			item = desktopFilesItem{remotefs.Entry{Name: path.Base(target), Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime()}, target}
			if attr, ok := info.Sys().(*sftp.FileStat); ok {
				item.UID, item.GID = attr.UID, attr.GID
			}
		}
		return err
	}, func(err error) {
		if err == nil && w.session.sftp == sc {
			a.desktopFilesOpenItem(w, item, false)
		}
	})
}

func (a *App) desktopFilesBlankMenu(c *ui.Context, w *desktopWindow, menu *ui.Menu) {
	f := w.explorer()
	ready := w.session.sftp != nil && f.job == nil && f.conflict == nil && f.modal == nil && !f.loading
	exec := desktopFilesRunner(w.session, f) != nil
	folder := ready && desktopFilesPath(f.dir)
	menu.Submenu(locales.DesktopFilesNew(), func(sub *ui.Menu) { a.desktopFilesNewMenu(w, sub, folder) }).Disabled(!folder)
	menu.Submenu(locales.DesktopFilesUpload(), func(sub *ui.Menu) { a.desktopFilesUploadMenu(w, sub, folder) }).Disabled(!folder)
	desktopFilesMenuAction(menu, locales.DesktopFilesRemoteDownload(), folder && exec, func() { a.desktopFilesFetch(w) })
	desktopFilesMenuAction(menu, locales.DesktopFilesTerminalHere(), folder, func() { a.desktopOpenTerminalAt(w.session, f.dir) })
	menu.Separator()
	desktopFilesMenuAction(menu, locales.DesktopFilesPaste(), folder && (a.desktopFilesClipboard(c) != nil || desktopFilesHasLocalClipboard(c)), func() { a.desktopFilesPaste(c, w) })
	menu.Submenu(locales.DesktopFilesSort(), func(sub *ui.Menu) { desktopFilesSortMenu(f, sub) }).Disabled(f.dir == "")
	menu.Submenu(locales.DesktopFilesViewMode(), func(sub *ui.Menu) { desktopFilesViewMenu(f, sub) }).Disabled(f.dir == "")
	desktopFilesMenuAction(menu, locales.Refresh(), ready, func() { a.desktopFilesRefresh(w) })
	if folder {
		desktopFilesMenuAction(menu, locales.CopyPath(), true, func() { c.Services().WriteClipboard(f.dir) })
		if !slices.Contains(a.desktopFilesHost(w.session).Favorites, f.dir) {
			desktopFilesMenuAction(menu, locales.DesktopFilesAddFavorite(), true, func() { a.desktopFilesFavorite(w.session, f.dir, true) })
		}
	}
}

func (a *App) desktopFilesItemMenu(c *ui.Context, w *desktopWindow, menu *ui.Menu, picked []desktopFilesItem) {
	f, s := w.explorer(), w.session
	if len(picked) == 0 {
		a.desktopFilesBlankMenu(c, w, menu)
		return
	}
	ready := s.sftp != nil && f.job == nil && f.modal == nil && f.conflict == nil && !f.loading
	exec := desktopFilesRunner(s, f) != nil
	item := picked[0]
	one := ready && len(picked) == 1
	valid := desktopFilesPath(item.target) && item.target != "/"
	open := valid && !item.Broken && (item.IsDir() || item.Mode.IsRegular() || item.Link)
	if open && !item.IsDir() && desktopFilesArchiveKind(item.target) != "" {
		open = exec && !item.Link
	}
	desktopFilesMenuAction(menu, locales.OpenFile(), one && open, func() { a.desktopFilesOpenItem(w, item, false) })
	desktopFilesMenuAction(menu, locales.DesktopFilesOpenNew(), one && item.IsDir(), func() { a.desktopFilesOpenItem(w, item, true) })
	desktopFilesMenuAction(menu, locales.DesktopFilesTerminalHere(), one, func() {
		dir := path.Dir(item.target)
		if item.IsDir() {
			dir = item.target
		}
		a.desktopOpenTerminalAt(s, dir)
	})
	desktopFilesMenuAction(menu, locales.Download(), ready, func() { a.desktopFilesDownload(w, picked) })
	menu.Separator()
	desktopFilesMenuAction(menu, locales.Copy(), ready, func() { a.desktopFilesCopy(c, s, picked, false) })
	desktopFilesMenuAction(menu, locales.DesktopFilesCut(), ready, func() { a.desktopFilesCopy(c, s, picked, true) })
	desktopFilesMenuAction(menu, locales.DesktopFilesPaste(), ready && f.dir != "" && (a.desktopFilesClipboard(c) != nil || desktopFilesHasLocalClipboard(c)), func() { a.desktopFilesPaste(c, w) })
	desktopFilesMenuAction(menu, locales.Rename(), one && valid, func() { a.desktopFilesRename(w, item) })
	desktopFilesMenuAction(menu, locales.DesktopFilesTrash(), ready && valid, func() { a.desktopFilesDelete(w, picked, false) })
	if menu.Item(locales.DesktopFilesPermanent()).Danger().Disabled(!ready || !valid).Chosen() && ready && valid {
		a.desktopFilesDelete(w, picked, true)
	}
	menu.Separator()
	_, _, compress := desktopFilesArchiveItems(picked)
	menu.Submenu(locales.DesktopFilesCompress(), func(sub *ui.Menu) {
		for _, kind := range []string{".tar.gz", ".zip"} {
			desktopFilesMenuAction(sub, strings.TrimPrefix(kind, "."), ready && exec && compress, func() { a.desktopFilesCompress(w, picked, kind) })
		}
	}).Disabled(!ready || !exec || !compress)
	extract := one && exec && !item.Link && item.Mode.IsRegular() && desktopFilesArchiveKind(item.target) != ""
	desktopFilesMenuAction(menu, locales.DesktopFilesExtractHere(), extract, func() { a.desktopFilesAskExtract(w, item, false) })
	desktopFilesMenuAction(menu, locales.DesktopFilesExtractTo(), extract, func() { a.desktopFilesAskExtract(w, item, true) })
	menu.Separator()
	desktopFilesMenuAction(menu, locales.CopyPath(), true, func() {
		paths, _ := desktopFilesTargets(picked)
		c.Services().WriteClipboard(strings.Join(paths, "\n"))
	})
	favorite := slices.Contains(a.desktopFilesHost(s).Favorites, item.target)
	label := locales.DesktopFilesAddFavorite()
	if favorite {
		label = locales.DesktopFilesRemoveFavorite()
	}
	desktopFilesMenuAction(menu, label, one, func() { a.desktopFilesFavorite(s, item.target, !favorite) })
	if hook := desktopAddShortcutHook; hook != nil {
		desktopFilesMenuAction(menu, locales.DesktopFilesShortcut(), one && valid, func() { hook(a, s, item.target) })
	}
	desktopFilesMenuAction(menu, locales.DesktopFilesNoteEdit(), one, func() { a.desktopFilesAskNote(w, item) })
	desktopFilesMenuAction(menu, locales.DesktopFilesProperties(), one, func() { a.desktopFilesAskProperties(w, item) })
}

func (a *App) desktopFilesAskNote(w *desktopWindow, item desktopFilesItem) {
	w.explorer().modal = &desktopFilesPrompt{title: locales.DesktopFilesNoteEdit(), field: locales.DesktopFilesNote(),
		value: a.desktopFilesHost(w.session).Notes[item.target], text: item.target, action: locales.Save(), accept: func(prompt *desktopFilesPrompt) {
			a.desktopFilesNote(w.session, item.target, prompt.value)
		}}
}

func (a *App) desktopFilesInline(c *ui.Context, w *desktopWindow, prompt *desktopFilesPrompt) {
	f := w.explorer()
	ui.Row(c).FillWidth().Padding(5, 10).Gap(7).AlignItems(ui.Center).Shrink(0).Background(colors.note).Children(func() {
		ui.Text(c, prompt.title).FontSize(12).Bold().SingleLine().Shrink(0)
		field := ui.TextInput(c, &prompt.value).AutoFocus().Placeholder(prompt.field).Grow(1).MinWidth(0).Tooltip(prompt.text)
		if field.Shortcut(0, ui.KeyEscape) || ui.Button(c, locales.Cancel()).Clicked() {
			f.inline = nil
			return
		}
		valid := prompt.valid == nil || prompt.valid(prompt)
		if (ui.PrimaryButton(c, prompt.action).Disabled(!valid).Clicked() || field.Submitted()) && valid {
			f.inline = nil
			prompt.accept(prompt)
		}
	})
	desktopFilesRule(c)
}

func (a *App) desktopFilesDialog(c *ui.Context, w *desktopWindow, height float32) {
	f := w.explorer()
	if a.pwOpen || a.pendingHostKey() != nil {
		return
	}
	if plan := f.conflict; plan != nil {
		open := true
		ui.Modal(c, &open, func() { a.desktopFilesConflictView(c, w, plan, &open) })
		if !open {
			f.conflict = nil
		}
		return
	}
	if prompt := f.modal; prompt != nil {
		open := true
		ui.Modal(c, &open, func() {
			if prompt.properties != nil {
				a.desktopFilesPropertiesView(c, w, prompt.properties, &open)
				return
			}
			ui.Column(c).Width(465).MaxWidth(465).Gap(10).Children(func() {
				ui.Text(c, prompt.title).Bold().FontSize(17)
				if prompt.text != "" {
					ui.Scroll(c).MaxHeight(min(150, height/3)).MinHeight(0).FillWidth().Padding(8).Radius(6).Background(colors.note).Children(func() {
						ui.Text(c, prompt.text).FontSize(12).Selectable()
					})
				}
				submit := false
				if prompt.field != "" {
					widgets.Muted(c, prompt.field)
					submit = ui.TextInput(c, &prompt.value).AutoFocus().FillWidth().Placeholder(prompt.field).Submitted()
				}
				if prompt.secondLabel != "" {
					widgets.Muted(c, prompt.secondLabel)
					field := ui.TextInput(c, &prompt.second).FillWidth().Placeholder(desktopFilesFetchName(prompt.value, ""))
					submit = field.Submitted() || submit
				}
				if prompt.err != "" {
					ui.Text(c, prompt.err).TextColor(theme.Danger)
				}
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						open = false
					}
					valid := prompt.valid == nil || prompt.valid(prompt)
					if (ui.PrimaryButton(c, prompt.action).Disabled(!valid).Clicked() || submit) && valid {
						f.modal = nil
						prompt.accept(prompt)
					}
				})
			})
		})
		if !open && f.modal == prompt {
			if prompt.properties != nil {
				prompt.properties.cancel()
			}
			f.modal = nil
		}
	}
}

func (a *App) desktopFilesConflictView(c *ui.Context, w *desktopWindow, plan *desktopFilesConflict, open *bool) {
	item := plan.items[plan.index]
	ui.Column(c).Width(490).Gap(12).Children(func() {
		ui.Text(c, locales.DesktopFilesConflict()).FontSize(17).Bold()
		ui.Text(c, locales.DesktopFilesConflictBody())
		ui.Column(c).Padding(9).Gap(5).Radius(7).Background(colors.note).Children(func() {
			widgets.Muted(c, locales.DesktopFilesSource()).FontSize(11).Bold()
			ui.Text(c, item.from).SingleLine().Tooltip(item.from).FillWidth()
			widgets.Muted(c, locales.DesktopFilesTarget()).FontSize(11).Bold()
			ui.Text(c, item.to).SingleLine().Tooltip(item.to).FillWidth()
		})
		widgets.Muted(c, locales.DesktopFilesReplaceBackup()).FontSize(12)
		ui.Checkbox(c, &plan.applyAll, locales.DesktopFilesApplyAll())
		ui.Row(c).Justify(ui.End).Gap(6).Wrap().Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				*open = false
			}
			if ui.Button(c, locales.DesktopFilesSkip()).Clicked() {
				a.desktopFilesResolve(w, plan, relaySkip)
			}
			if ui.Button(c, locales.Replace()).Disabled(plan.clip.s == w.session && item.from == item.to).Clicked() {
				a.desktopFilesResolve(w, plan, relayOverwrite)
			}
			if ui.PrimaryButton(c, locales.DesktopFilesKeepBoth()).Clicked() {
				a.desktopFilesResolve(w, plan, relayRename)
			}
		})
	})
}

func (a *App) desktopFilesPropertiesView(c *ui.Context, w *desktopWindow, props *desktopFilesProperties, open *bool) {
	item := props.item
	ui.Column(c).Width(484).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
			a.desktopFilesIcon(c, item.Name, item.IsDir(), 30)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, locales.DesktopFilesProperties()).Bold().FontSize(16)
				ui.Text(c, item.Name).SingleLine().Tooltip(item.Name).TextColor(theme.TextMuted)
			})
		})
		ui.Scroll(c).MaxHeight(435).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(9).Children(func() {
				ui.Column(c).FillWidth().Gap(4).Padding(9).Radius(7).Background(colors.note).Children(func() {
					desktopFilesPropertyLine(c, locales.DesktopFilesAddress(), item.target)
					desktopFilesPropertyLine(c, locales.DesktopFilesType(), entryKind(item.Entry))
					if props.reading {
						widgets.Muted(c, locales.DesktopFilesLoading())
					} else if props.info != nil {
						size := formatSize(props.bytes)
						if props.measuring {
							size = locales.DesktopFilesCalculating()
						} else if props.unknownSize {
							size = "—"
						}
						desktopFilesPropertyLine(c, locales.ColSize(), size)
						desktopFilesPropertyLine(c, locales.ColModified(), props.info.ModTime().Format("2006-01-02 15:04:05"))
						if !props.accessed.IsZero() {
							desktopFilesPropertyLine(c, locales.DesktopFilesAccessed(), props.accessed.Format("2006-01-02 15:04:05"))
						}
					}
				})
				if props.info != nil {
					a.desktopFilesPermissions(c, w, props)
				}
				ui.Text(c, locales.DesktopFilesNote()).Bold().FontSize(12)
				ui.TextInput(c, &props.note).FillWidth().Placeholder(locales.DesktopFilesNote())
				if props.problem != "" {
					ui.Text(c, props.problem).TextColor(theme.Danger).FontSize(12)
				}
			})
		})
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				*open = false
			}
			if ui.PrimaryButton(c, locales.DesktopFilesApply()).Disabled(props.info == nil).Clicked() {
				a.desktopFilesApplyProperties(w, props)
			}
		})
	})
}

func desktopFilesPropertyLine(c *ui.Context, key, value string) {
	ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
		widgets.Muted(c, key).Width(109).FontSize(11.5).SingleLine()
		ui.Text(c, value).FontSize(12).SingleLine().Grow(1).MinWidth(0).Selectable().Tooltip(value)
	})
}

func (a *App) desktopFilesPermissions(c *ui.Context, w *desktopWindow, props *desktopFilesProperties) {
	editable := desktopFilesRunner(w.session, w.explorer()) != nil && props.item.target != "/"
	modeEditable := editable && props.info.Mode()&fs.ModeSymlink == 0
	ui.Row(c).Gap(8).FillWidth().Children(func() {
		for _, field := range []struct {
			value *string
			label string
		}{{&props.owner, locales.DesktopFilesOwner()}, {&props.group, locales.DesktopFilesGroup()}} {
			ui.Column(c.Key(field.label)).Grow(1).MinWidth(0).Gap(3).Children(func() {
				widgets.Muted(c, field.label).FontSize(11.5)
				ui.TextInput(c, field.value).FillWidth().Disabled(!editable).Placeholder(field.label)
			})
		}
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(9).Children(func() {
		ui.Text(c, locales.ColPermissions()).Bold().FontSize(12)
		ui.TextInput(c, &props.octal).Width(75).Disabled(!modeEditable).Tooltip(locales.DesktopFilesOctal())
	})
	for index, label := range []string{locales.DesktopFilesOwner(), locales.DesktopFilesGroup(), locales.DesktopFilesOthers()} {
		ui.Row(c.Key(index)).FillWidth().AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Text(c, label).Width(74).FontSize(11.5).SingleLine()
			for bit, flag := range []string{locales.DesktopFilesRead(), locales.DesktopFilesWrite(), locales.DesktopFilesExecute()} {
				desktopFilesModeCheck(c, props, flag, uint32(0o400)>>(index*3+bit), modeEditable)
			}
		})
	}
	ui.Row(c).Gap(10).FillWidth().Children(func() {
		for index, label := range []string{locales.DesktopFilesSetuid(), locales.DesktopFilesSetgid(), locales.DesktopFilesSticky()} {
			desktopFilesModeCheck(c, props, label, uint32(0o4000)>>index, modeEditable)
		}
	})
	if props.info.IsDir() {
		ui.Checkbox(c, &props.recursive, locales.DesktopFilesRecursive()).Disabled(!editable)
	}
}

func desktopFilesModeCheck(c *ui.Context, props *desktopFilesProperties, label string, mask uint32, editable bool) {
	mode, valid := props.mode()
	on := mode&mask != 0
	if ui.Checkbox(c, &on, label).Disabled(!editable || !valid).Changed() {
		if on {
			mode |= mask
		} else {
			mode &^= mask
		}
		props.octal = fmt.Sprintf("%04o", mode)
	}
}
