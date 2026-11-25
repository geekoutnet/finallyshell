package app

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (window *codeEditorWindow) view(c *ui.Context) {
	if window.review != nil {
		window.reviewView(c)
		return
	}
	window.keys(c)
	if window.closed {
		return
	}
	ui.Column(c).Fill().Background(theme.Background).Children(func() {
		window.toolbar(c)
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
			width, _ := c.Size()
			if len(window.files) > 1 && (width >= 650 || window.showFiles) {
				window.treeView(c, min(208, width*0.37))
			}
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				window.tabStrip(c)
				window.editView(c, window.active)
			})
		})
		window.outputView(c)
		window.status(c, window.active)
	})
	window.closeQuestion(c)
}

func (window *codeEditorWindow) keys(c *ui.Context) {
	if window.askClose || window.askFile != nil {
		return
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyS) {
		window.beginReview(nil, false, nil)
	} else if c.Shortcut(ui.Cmd, ui.KeyS) && window.active != nil {
		window.beginReview(window.active, false, nil)
	}
	if c.Shortcut(ui.Cmd, ui.KeyW) {
		if window.active != nil {
			window.closeTab(window.active, false)
		} else if window.closeRequested() {
			window.app.closeWindow(window.kind)
		}
	}
	if len(window.tabs) > 1 {
		index := slices.Index(window.tabs, window.active)
		if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
			window.activate(window.tabs[(index+len(window.tabs)-1)%len(window.tabs)])
		} else if c.Shortcut(ui.Ctrl, ui.KeyTab) {
			window.activate(window.tabs[(index+1)%len(window.tabs)])
		}
	}
}

func (window *codeEditorWindow) toolbar(c *ui.Context) {
	doc := window.active
	ready := doc != nil && doc.tab.code != nil
	editable := ready && !doc.limited && window.cancel == nil
	idle, online := window.cancel == nil, window.online()
	width, _ := c.Size()
	command := func(cmd editor.Command) func() {
		return func() { doc.tab.code.Run(cmd) }
	}
	button := func(icon, label string, enabled bool, action func()) ui.Element {
		item := widgets.IconButton(c.Key(label), icon, label).Disabled(!enabled)
		if enabled && item.Clicked() {
			action()
			window.refocus = label != locales.Find() && label != locales.EditorReplace() && label != locales.EditorGoToLine()
		}
		return item
	}
	ui.Row(c).Gap(4).Wrap().Padding(5, 8).FillWidth().AlignItems(ui.Center).Shrink(0).
		Background(colors.sidebar).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		if width < 650 && len(window.files) > 1 && ui.Button(c, locales.CeditFiles()).Clicked() {
			window.showFiles = !window.showFiles
		}
		if ui.PrimaryButton(c, locales.Save()).Disabled(!editable || !doc.tab.modified()).Clicked() {
			window.beginReview(doc, false, nil)
		}
		if ui.Button(c, locales.SaveAll()).Disabled(!idle || window.unsaved() == 0).Clicked() {
			window.beginReview(nil, false, nil)
		}
		if window.spec.Format != nil && ui.Button(c, locales.CeditFormat()).Disabled(!editable || !online).Clicked() {
			window.format(doc)
		}
		if window.spec.Validate != nil && ui.Button(c, locales.CeditValidate()).Disabled(!idle || !online).Clicked() {
			window.validate()
		}
		button("undo-2", locales.Undo(), editable && doc.tab.code.Buffer.CanUndo(), command(editor.Undo))
		button("redo-2", locales.Redo(), editable && doc.tab.code.Buffer.CanRedo(), command(editor.Redo))
		button("search", locales.Find(), ready, command(editor.Find))
		button("replace", locales.EditorReplace(), editable, command(editor.FindReplace))
		button("hash", locales.EditorGoToLine(), ready, command(editor.GoToLine))
		wrap := button("wrap-text", locales.EditorSoftWrap(), ready, func() { window.wrap = !window.wrap })
		if window.wrap {
			wrap.Background(theme.Accent.Alpha(0.16))
		}
		if ready && doc.tab.code.Buffer.Language() != nil && len(doc.tab.code.Buffer.Language().Brackets) != 0 {
			button("chevrons-down-up", locales.CeditFold(), true, command(editor.FoldAll))
			button("chevrons-up-down", locales.CeditUnfold(), true, command(editor.UnfoldAll))
		}
		fontButton := func(label, glyph string, step int, allowed bool) {
			if ui.Button(c.Key(label), glyph).Label(label).Tooltip(label).Disabled(!allowed).Clicked() {
				settings := window.app.settings
				settings.CodeEditorFontSize = int(window.font().Size) + step
				if err := window.app.saveSettings(settings); err != nil {
					window.setFailure(label, err.Error())
				} else {
					window.app.settings = settings
					window.app.redraw()
				}
				window.refocus = true
			}
		}
		size := window.font().Size
		fontButton(locales.CeditFontSmaller(), "A−", -1, size > 9)
		fontButton(locales.CeditFontLarger(), "A+", 1, size < 36)
	})
}

func (window *codeEditorWindow) font() ui.Font {
	font := codeFont
	if size := window.app.settings.CodeEditorFontSize; size != 0 {
		font.Size = float32(max(9, min(size, 36)))
	}
	return font
}

func (window *codeEditorWindow) tabStrip(c *ui.Context) {
	if len(window.tabs) == 0 {
		return
	}
	ui.ScrollHorizontal(c).MinWidth(0).FillWidth().Height(34).Shrink(0).Scrollbars(false).Background(colors.sidebar).
		BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.Row(c).Gap(2).PaddingX(4).Children(func() {
			for _, doc := range window.tabs {
				ctx := c.Key(doc.tab.path)
				row := ui.Row(ctx).Gap(4).AlignItems(ui.Center).Padding(2, 5).Shrink(0).BorderWidth(0, 0, 2, 0).
					BorderColor(colors.line)
				if doc == window.active {
					row.Background(theme.Background).BorderColor(theme.Accent)
				}
				row.Children(func() {
					label := doc.tab.name()
					if doc.tab.modified() {
						label += " ●"
					}
					if ui.Text(ctx, label).Role(ui.RoleButton).SingleLine().MaxWidth(172).Padding(4, 6).Tooltip(doc.tab.path).Clicked() {
						window.activate(doc)
					}
					if widgets.IconButton(ctx, "x", locales.ScCloseTab()).Disabled(window.cancel != nil).Clicked() {
						window.closeTab(doc, false)
					}
				})
			}
		})
	})
}

func (window *codeEditorWindow) editView(c *ui.Context, doc *codeEditorDoc) {
	if doc == nil || doc.tab.code == nil {
		ui.Column(c).Grow(1).MinHeight(0).Center().Padding(12).Gap(8).Children(func() {
			if doc == nil {
				widgets.Muted(c, locales.CeditChooseFile())
			} else if doc.tab.state == tabBinary {
				ui.Text(c, locales.CeditUTF8Only()).FillWidth().TextAlign(ui.Center)
			} else if doc.tab.err != nil {
				ui.Text(c, locales.EditorCannotOpen(doc.tab.name(), codeFormatError(doc.tab.err, window.hostLabel()))).
					FillWidth().TextAlign(ui.Center).TextColor(theme.Danger)
				if validCodePath(doc.tab.path) && ui.Button(c, locales.Retry()).Disabled(window.cancel != nil).Clicked() {
					window.load(doc)
				}
			} else if window.cancel == nil && ui.Button(c, locales.CeditReadFile()).Clicked() {
				window.load(doc)
			}
		})
		return
	}
	if doc.limited {
		ui.Text(c, locales.CeditTooLarge()).FillWidth().FontSize(12).Padding(5, 9).Background(colors.note).Shrink(0)
	}
	state := doc.tab.code
	state.SoftWrap, state.Theme, state.Font, state.ReadOnly = window.wrap, codeTheme(), window.font(), doc.limited || window.cancel != nil
	state.ContextMenu = func(menu *ui.Menu) {
		menu.Separator()
		window.saveMenu(menu, doc)
	}
	view := editor.View(c.Key(doc.tab.path), state).Grow(1).MinHeight(0).FillWidth().AutoFocus()
	if window.refocus {
		window.refocus = false
		view.Focus()
	}
}

func (window *codeEditorWindow) saveMenu(menu *ui.Menu, doc *codeEditorDoc) {
	item := menu.Item(locales.Save()).Shortcut(ui.Cmd, ui.KeyS)
	if window.cancel != nil {
		item.Disabled(true).Subtitle(locales.CeditWorking())
	} else if doc.limited {
		item.Disabled(true).Subtitle(locales.CeditReadOnly())
	} else if !doc.tab.modified() {
		item.Disabled(true).Subtitle(locales.CeditNoChanges())
	}
	if item.Chosen() {
		window.beginReview(doc, false, nil)
	}
	all := menu.Item(locales.SaveAll()).Shortcut(ui.Cmd|ui.Shift, ui.KeyS)
	if window.cancel != nil {
		all.Disabled(true).Subtitle(locales.CeditWorking())
	} else if window.unsaved() == 0 {
		all.Disabled(true).Subtitle(locales.CeditNoChanges())
	}
	if all.Chosen() {
		window.beginReview(nil, false, nil)
	}
}

func (window *codeEditorWindow) outputView(c *ui.Context) {
	if window.operation == "" && window.online() {
		return
	}
	ui.Column(c).FillWidth().Padding(4, 8).Gap(3).Shrink(0).Background(colors.sidebar).
		BorderWidth(1, 0, 0, 0).BorderColor(colors.line).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			switch {
			case window.cancel != nil:
				ui.Spinner(c).Size(16, 16)
			case window.failed || !window.online():
				ui.Icon(c, icons.Get("triangle-alert")).TextColor(theme.Danger).FontSize(14)
			}
			title := window.operation
			if title == "" {
				title = locales.EditorNotConnected()
			}
			ui.Text(c, title).SingleLine().Grow(1).MinWidth(0).Bold().FontSize(12).Tooltip(title)
			if window.cancel != nil {
				if ui.Button(c, locales.Cancel()).Clicked() {
					window.cancel()
				}
			} else {
				if window.override != nil && ui.Button(c, locales.CeditSaveAnyway()).Disabled(!window.online()).Clicked() {
					window.saveReview(window.override, true)
				}
				if window.operation != "" && widgets.IconButton(c, "x", locales.Close()).Clicked() {
					window.output, window.operation, window.override = "", "", nil
				}
			}
		})
		message := window.output
		if !window.online() && window.cancel == nil && message == "" {
			message = window.offlineMessage()
		}
		if message != "" {
			_, height := c.Size()
			ui.Scroll(c).FillWidth().MaxHeight(min(125, height*0.23)).Children(func() {
				ui.Text(c, message).FillWidth().Padding(4, 6).Font(codeFont.Family).FontSize(12).Selectable()
			})
		}
	})
}

func (window *codeEditorWindow) status(c *ui.Context, doc *codeEditorDoc) {
	ui.Row(c).FillWidth().Wrap().Gap(5).Padding(3, 8).Shrink(0).AlignItems(ui.Center).
		Background(colors.sidebar).BorderWidth(1, 0, 0, 0).BorderColor(colors.line).Children(func() {
		label := locales.CeditUser()
		if window.spec.Root {
			label = locales.CeditRoot()
		}
		ui.Text(c, label).FontSize(11).Bold().Padding(2, 5).Radius(4).Background(theme.Accent.Alpha(0.15))
		if doc == nil || doc.tab.code == nil {
			return
		}
		state := doc.tab.code
		if doc.limited {
			widgets.Muted(c, locales.CeditReadOnly()).FontSize(11)
		}
		widget := func(label, tooltip string) ui.Element {
			return ui.Text(c.Key(tooltip), label).Padding(2, 4).FontSize(11).SingleLine().Role(ui.RoleButton).Tooltip(tooltip)
		}
		position := state.CursorPos()
		column := utf8.RuneCountInString(state.Buffer.Line(position.Line)[:position.Col]) + 1
		if widget(fmt.Sprintf("%d:%d", position.Line+1, column), locales.EditorGoToLine()).Clicked() {
			state.Run(editor.GoToLine)
		}
		name := locales.EditorPlainText()
		if lang := state.Buffer.Language(); lang != nil {
			name = lang.Name
		}
		widget(name, locales.EditorLanguage()).Menu(func(menu *ui.Menu) {
			choose := func(label string, lang *editor.Language) {
				if menu.Item(label).Checked(state.Buffer.Language() == lang).Chosen() {
					state.Buffer.SetLanguage(lang)
					window.refocus = true
				}
			}
			choose(locales.EditorPlainText(), nil)
			for _, lang := range configCodeLanguages {
				choose(lang.Name, lang)
			}
			menu.Separator()
			for _, lang := range editor.Languages() {
				choose(lang.Name, lang)
			}
		})
		encoding := "UTF-8"
		if doc.tab.bom {
			encoding += " BOM"
		}
		widgets.Muted(c, encoding).FontSize(11).Tooltip(locales.EditorEncoding())
		ending := codeEnding(state.Buffer.LineEnding())
		widget(ending, locales.EditorLineEndings()).Menu(func(menu *ui.Menu) {
			for _, value := range []string{"\n", "\r\n"} {
				item := menu.Item(codeEnding(value)).Checked(state.Buffer.LineEnding() == value)
				if state.ReadOnly {
					item.Disabled(true).Subtitle(locales.CeditReadOnly())
				}
				if item.Chosen() {
					state.Buffer.SetLineEnding(value)
					window.refocus = true
				}
			}
		})
		indent := locales.CeditSpaces()
		if codeIndentUnit(state) == "\t" {
			indent = locales.CeditTabs()
		}
		widget(fmt.Sprintf("%s: %d", indent, codeIndentSize(state)), locales.CeditIndent()).Menu(func(menu *ui.Menu) {
			for _, option := range []struct {
				name  string
				style editor.IndentStyle
			}{{locales.CeditTabs(), editor.IndentTabs}, {locales.CeditSpaces(), editor.IndentSpaces}} {
				for _, size := range []int{2, 4, 8} {
					if menu.Item(fmt.Sprintf("%s: %d", option.name, size)).Checked(option.name == indent && size == codeIndentSize(state)).Chosen() {
						state.Indent, state.TabSize, window.refocus = option.style, size, true
					}
				}
			}
		})
		widgets.Muted(c, doc.tab.path).FontSize(11).SingleLine().MinWidth(0).Shrink(1).Tooltip(doc.tab.path)
	})
}

func codeEnding(ending string) string {
	if ending == "\r\n" {
		return "CRLF"
	}
	return "LF"
}

func (window *codeEditorWindow) reviewView(c *ui.Context) {
	review := window.review
	if c.Shortcut(0, ui.KeyEscape) {
		window.review, window.refocus = nil, true
		return
	}
	ui.Column(c).Fill().Background(theme.Background).Children(func() {
		ui.Column(c).Padding(8, 10).FillWidth().Gap(3).Shrink(0).Background(colors.sidebar).Children(func() {
			ui.Text(c, locales.CeditReviewTitle(len(review.items))).Bold().FontSize(16)
			note := locales.CeditReviewNote()
			if window.spec.Save != nil {
				note = locales.CeditReviewTransaction()
			}
			widgets.Muted(c, note).FillWidth().FontSize(12)
		})
		ui.ScrollHorizontal(c).FillWidth().MinWidth(0).Height(32).Shrink(0).Children(func() {
			labels := make([]string, len(review.items))
			for index, item := range review.items {
				labels[index] = item.doc.tab.name()
			}
			ui.Segmented(c, &review.selected, labels...)
		})
		item := review.items[review.selected]
		ui.Row(c).FillWidth().Padding(4, 10).Gap(6).Wrap().Shrink(0).Children(func() {
			ui.Text(c, item.after.Path).SingleLine().MinWidth(0).Shrink(1).Tooltip(item.after.Path).FontSize(12)
			before, after := decodeText([]byte(item.before.Content), encUTF8), decodeText([]byte(item.after.Content), encUTF8)
			if before.ending != after.ending {
				widgets.Muted(c, codeEnding(before.ending)+" → "+codeEnding(after.ending)).FontSize(12)
			} else if before.text == after.text && item.before.Content != item.after.Content {
				widgets.Muted(c, locales.CeditNormalizeEndings()).FontSize(12)
			}
		})
		item.diff.Layout, item.diff.Font, item.diff.Theme = review.layout, window.font(), codeTheme()
		editor.DiffView(c.Key(item.after.Path), &item.diff).Grow(1).MinHeight(0).FillWidth()
		ui.Row(c).FillWidth().Padding(7, 10).Gap(6).Wrap().AlignItems(ui.Center).Shrink(0).Background(colors.sidebar).Children(func() {
			layout := int(review.layout)
			ui.Segmented(c, &layout, locales.CeditSplit(), locales.CeditUnified())
			review.layout = editor.DiffLayout(layout)
			ui.Box(c).Grow(1)
			if ui.Button(c, locales.CeditBack()).Clicked() {
				window.review, window.refocus = nil, true
			}
			if ui.PrimaryButton(c, locales.Save()).Clicked() {
				window.saveReview(review, false)
			}
		})
	})
}

func (window *codeEditorWindow) closeQuestion(c *ui.Context) {
	if !window.askClose && window.askFile == nil {
		return
	}
	open := true
	var names []string
	for _, doc := range window.files {
		if doc.tab.modified() && (window.askFile == nil || window.askFile == doc) {
			names = append(names, doc.tab.path)
		}
	}
	if len(names) == 0 {
		window.askClose, window.askFile = false, nil
		return
	}
	button := locales.SaveAll()
	if window.askFile != nil {
		button = locales.Save()
	}
	switch ui.AlertDialog(c, &open, locales.EditorUnsavedFiles(len(names)), strings.Join(names, "\n")+"\n\n"+locales.EditorUnsavedBody(), locales.DontSave(), locales.Cancel(), button) {
	case 0:
		if window.askFile != nil {
			window.closeTab(window.askFile, true)
		} else {
			window.app.closeWindow(window.kind)
		}
	case 2:
		window.beginReview(window.askFile, window.askClose, window.askFile)
	}
	if !open {
		window.askClose, window.askFile, window.refocus = false, nil, true
	}
}

type codeEditorDir struct {
	path string
	dirs []*codeEditorDir
	docs []*codeEditorDoc
}

func makeCodeEditorTree(files []*codeEditorDoc) *codeEditorDir {
	root := &codeEditorDir{path: path.Dir(files[0].tab.path)}
	for _, doc := range files[1:] {
		for root.path != "/" && !strings.HasPrefix(doc.tab.path, root.path+"/") {
			next := path.Dir(root.path)
			if next == root.path {
				root.path = "/"
				break
			}
			root.path = next
		}
	}
	for _, doc := range files {
		dir := root
		if validCodePath(doc.tab.path) {
			relative := strings.TrimPrefix(path.Dir(doc.tab.path), root.path)
			for _, part := range strings.Split(strings.Trim(relative, "/"), "/") {
				if part == "" {
					continue
				}
				full := path.Join(dir.path, part)
				index := slices.IndexFunc(dir.dirs, func(child *codeEditorDir) bool { return child.path == full })
				if index < 0 {
					dir.dirs = append(dir.dirs, &codeEditorDir{path: full})
					index = len(dir.dirs) - 1
				}
				dir = dir.dirs[index]
			}
		}
		dir.docs = append(dir.docs, doc)
	}
	return root
}

func (window *codeEditorWindow) treeView(c *ui.Context, width float32) {
	ui.Column(c.Key("file-tree")).Width(width).Shrink(0).MinHeight(0).Background(colors.sidebar).
		BorderWidth(0, 1, 0, 0).BorderColor(colors.line).Children(func() {
		ui.Column(c).FillWidth().Padding(8, 9).Gap(3).Children(func() {
			ui.Text(c, locales.CeditFiles()).FontSize(12).Bold()
			widgets.Muted(c, window.tree.path).FontSize(11).SingleLine().FillWidth().Tooltip(window.tree.path)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(2, 4).Children(func() {
			window.treeRows(c, window.tree, 0)
		})
	})
}

func (window *codeEditorWindow) treeRows(c *ui.Context, dir *codeEditorDir, depth int) {
	row := func(key, label, hint, icon string, level int, selected bool) bool {
		ctx := c.Key(key)
		item := ui.Row(ctx).Role(ui.RoleButton).Label(label).Tooltip(hint).FillWidth().Gap(5).Padding(4, 4, 4, 5+float32(min(level, 8))*11).Radius(4).AlignItems(ui.Center)
		if selected {
			item.Background(theme.Accent.Alpha(0.15))
		} else if item.Hovered() {
			item.Background(theme.Text.Alpha(0.07))
		}
		item.Children(func() {
			ui.Icon(ctx, icons.Get(icon)).FontSize(13).TextColor(theme.TextMuted).Shrink(0)
			ui.Text(ctx, label).SingleLine().MinWidth(0).Shrink(1).FontSize(12)
		})
		return item.Clicked()
	}
	for _, doc := range dir.docs {
		name := doc.tab.name()
		if doc.tab.modified() {
			name += " ●"
		}
		if row(doc.tab.path, name, doc.tab.path, "file-text", depth, doc == window.active) {
			window.activate(doc)
			window.showFiles = false
		}
	}
	for _, child := range dir.dirs {
		icon := "chevron-down"
		if window.folded[child.path] {
			icon = "chevron-right"
		}
		if row(child.path, path.Base(child.path), child.path, icon, depth, false) {
			window.folded[child.path] = !window.folded[child.path]
		}
		if !window.folded[child.path] {
			window.treeRows(c, child, depth+1)
		}
	}
}
