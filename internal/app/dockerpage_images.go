package app

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerImagesPage struct {
	rows          []monitor.ImageRow
	client        *ssh.Client
	tool          monitor.DockerCmd
	updated       time.Time
	frame         uint64
	gen           uint64
	cancel        context.CancelFunc
	err           error
	readOutput    string
	loaded        bool
	loading       bool
	danglingKnown bool

	filter                   string
	danglingOnly, unusedOnly bool
	table                    ui.ListState
	sort                     ui.SortOrder
	selected                 ui.Selection[[2]string]
	rowIndex                 int
	rowKey                   [2]string
	dialog                   *dockerImgDialog
}

func dockerImgTarget(s *Session) dockerTarget { return dockerTarget{session: s, client: s.client} }

func dockerImgRunner(s *Session) dockerRunner {
	if run := s.overview.docker.run; run != nil {
		return run
	}
	return monitor.RunImageProgress
}

func dockerImgContext(s *Session, timeout time.Duration) (context.Context, context.CancelFunc) {
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}

func (a *App) refreshDockerImages(s *Session) {
	target := dockerImgTarget(s)
	if !target.available() || s.overview.docker.d.State != monitor.DockerOK {
		return
	}
	page := &s.docker.images
	if page.cancel != nil {
		page.cancel()
	}
	tool := monitor.CmdOf(&s.overview.docker.d)
	if page.client != target.client || page.tool != tool {
		page.rows, page.loaded, page.updated = nil, false, time.Time{}
		page.selected.Clear()
	}
	page.client, page.tool = target.client, tool
	page.gen++
	gen := page.gen
	ctx, cancel := dockerImgContext(s, dockerTimeout)
	page.cancel, page.loading = cancel, true
	run := dockerImgRunner(s)
	go func() {
		defer cancel()
		output, err := run(ctx, target.client, tool.ImageList(), nil)
		var rows []monitor.ImageRow
		danglingKnown := false
		if err == nil {
			rows, err = monitor.ParseImageList(output)
		}
		if err == nil {
			danglingKnown = dockerImgMarkDangling(ctx, run, target.client, tool, rows)
			dockerImgPlatforms(ctx, run, target.client, tool, rows)
		}
		a.post(func() {
			if page.gen != gen {
				return
			}
			page.cancel, page.loading = nil, false
			if !target.available() || tool != monitor.CmdOf(&s.overview.docker.d) {
				return
			}
			page.err, page.updated = err, time.Now()
			if err == nil {
				page.rows, page.loaded, page.readOutput, page.danglingKnown = rows, true, "", danglingKnown
				keys := make(map[[2]string]bool, len(rows))
				for _, row := range rows {
					keys[dockerImgRowKey(row)] = true
				}
				for key := range page.selected.All() {
					if !keys[key] {
						page.selected.Remove(key)
					}
				}
			} else {
				page.readOutput = dockerImgTail(output)
			}
		})
	}()
}

func dockerImgMarkDangling(ctx context.Context, run dockerRunner, client *ssh.Client, tool monitor.DockerCmd, rows []monitor.ImageRow) bool {
	if !slices.ContainsFunc(rows, func(row monitor.ImageRow) bool { return row.Untagged }) {
		return true
	}
	output, err := run(ctx, client, tool.ImageDangling(), nil)
	if err != nil {
		return false
	}
	matches, err := monitor.ParseImageList(output)
	if err != nil {
		return false
	}
	ids := map[string]bool{}
	for _, match := range matches {
		ids[monitor.ImageIDKey(match.ID)] = true
	}
	for index := range rows {
		rows[index].Dangling = rows[index].Untagged && ids[monitor.ImageIDKey(rows[index].ID)]
	}
	return true
}

func dockerImgPlatforms(ctx context.Context, run dockerRunner, client *ssh.Client, tool monitor.DockerCmd, rows []monitor.ImageRow) {
	platforms := map[string]string{}
	var ids []string
	for _, row := range rows {
		if row.Platform != "" {
			platforms[row.ID] = row.Platform
		} else if _, seen := platforms[row.ID]; !seen {
			platforms[row.ID] = ""
			ids = append(ids, row.ID)
		}
	}
	for group := range slices.Chunk(ids, 64) {
		if ctx.Err() != nil {
			break
		}
		script, err := tool.ImagePlatforms(group)
		if err != nil {
			continue
		}
		output, err := run(ctx, client, script, nil)
		lines := strings.Fields(output)
		if err == nil && len(lines) == len(group) {
			for index, line := range lines {
				if strings.Count(line, "/") == 1 && !strings.ContainsAny(line, "<>") && line != "/" {
					platforms[group[index]] = line
				}
			}
		}
	}
	for index := range rows {
		rows[index].Platform = platforms[rows[index].ID]
	}
}

func dockerImgRowKey(row monitor.ImageRow) [2]string { return [2]string{row.ID, row.Ref} }

func (*dockerImagesPage) usage(engine *monitor.Docker) map[string][]string {
	result := map[string][]string{}
	for _, container := range engine.Containers {
		if container.ImageID != "" {
			id := monitor.ImageIDKey(container.ImageID)
			result[id] = append(result[id], container.Name)
		}
	}
	for _, names := range result {
		slices.Sort(names)
	}
	return result
}

func dockerImgUsageKnown(s *Session) bool {
	state := &s.overview.docker
	return state.read && state.err == nil && state.dataClient == s.client && state.d.NumContainers <= len(state.d.Containers) &&
		!slices.ContainsFunc(state.d.Containers, func(row monitor.Container) bool { return row.ImageID == "" })
}

func (page *dockerImagesPage) shown(users map[string][]string) []monitor.ImageRow {
	query := strings.ToLower(strings.TrimSpace(page.filter))
	rows := make([]monitor.ImageRow, 0, len(page.rows))
	for _, row := range page.rows {
		names := users[monitor.ImageIDKey(row.ID)]
		if page.danglingOnly && page.danglingKnown && !row.Dangling || page.unusedOnly && len(names) > 0 {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Ref+" "+row.ID+" "+row.Platform+" "+strings.Join(names, " ")), query) {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(left, right monitor.ImageRow) int {
		order := 0
		switch page.sort.Column {
		case "id":
			order = cmp.Compare(left.ID, right.ID)
		case "size":
			order = cmp.Compare(left.Size, right.Size)
		case "created":
			order = left.Created.Compare(right.Created)
		case "used":
			order = cmp.Compare(len(users[monitor.ImageIDKey(left.ID)]), len(users[monitor.ImageIDKey(right.ID)]))
		case "platform":
			order = cmp.Compare(left.Platform, right.Platform)
		default:
			order = cmp.Compare(left.Ref, right.Ref)
		}
		if page.sort.Descending {
			order = -order
		}
		return order
	})
	return rows
}

func dockerImgSize(rows []monitor.ImageRow) string {
	byID := map[string]uint64{}
	for _, row := range rows {
		byID[row.ID] = max(byID[row.ID], row.Size)
	}
	var size uint64
	for value := range maps.Values(byID) {
		size += value
	}
	return formatSize(int64(size))
}

func dockerImgSelected(page *dockerImagesPage, rows []monitor.ImageRow) []monitor.ImageRow {
	var selected []monitor.ImageRow
	for _, row := range rows {
		if page.selected.Has(dockerImgRowKey(row)) {
			selected = append(selected, row)
		}
	}
	return selected
}

func (a *App) dockerImagesSection(c *ui.Context, s *Session) {
	page := &s.docker.images
	again := page.frame+1 < a.frame
	page.frame = a.frame
	if !page.loading && (page.client != s.client || page.tool != monitor.CmdOf(&s.overview.docker.d) || page.updated.IsZero() || again && time.Since(page.updated) > dockerStale) {
		a.refreshDockerImages(s)
	}
	engine := &s.overview.docker.d
	users := page.usage(engine)
	rows := page.shown(users)
	ui.Column(c.Key(s.ID)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		a.dockerImgToolbar(c, s, rows)
		switch {
		case !page.loaded && page.loading:
			overviewMessage(c, "container", locales.DockerImgReading(), func() { ui.Spinner(c).Label(locales.DockerImgReading()) })
		case !page.loaded && page.err == nil:
			overviewMessage(c, "container", locales.DockerImgReading(), nil)
		case len(rows) == 0:
			message := locales.DockerImgEmpty()
			if page.err != nil {
				message = locales.DockerImgListError(page.err.Error())
			} else if len(page.rows) > 0 {
				message = locales.DockerImgNoMatch()
			}
			overviewMessage(c, "container", message, nil)
		default:
			a.dockerImgTable(c, s, rows, users)
		}
		a.dockerImgDialogView(c, s)
	})
}

func (a *App) dockerImgToolbar(c *ui.Context, s *Session, rows []monitor.ImageRow) {
	page := &s.docker.images
	selected := dockerImgSelected(page, rows)
	ui.Row(c.Key("images-tools")).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.SearchField(c, &page.filter).Placeholder(locales.DockerImgFilter()).Label(locales.DockerImgFilter()).Width(190).MinWidth(108).Shrink(1)
		ui.Row(c.Key("image-filters")).Gap(4).Shrink(0).AlignItems(ui.Center).Children(func() {
			dangling := ui.Checkbox(c, &page.danglingOnly, locales.DockerImgDanglingOnly()).Disabled(!page.danglingKnown)
			if !page.danglingKnown {
				dangling.Tooltip(locales.DockerImgDanglingUnknown())
			}
			unused := ui.Checkbox(c, &page.unusedOnly, locales.DockerImgUnusedOnly()).Disabled(!dockerImgUsageKnown(s))
			if !dockerImgUsageKnown(s) {
				unused.Tooltip(locales.DockerImgUsageUnknown())
			}
		})
		ui.Row(c.Key("image-buttons")).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
			reason := dockerImgReadyReason(s, false)
			for _, op := range []dockerImgOp{imgPull, imgBuild, imgLoad, imgPrune} {
				label := op.title()
				if op == imgLoad {
					label = locales.DockerImgLoadImport()
				}
				itemReason := reason
				if op == imgPrune {
					itemReason = dockerImgReadyReason(s, true)
				}
				button := ui.Button(c.Key(op), label).Disabled(itemReason != "")
				if itemReason != "" {
					button.Tooltip(itemReason)
				}
				if button.Clicked() {
					a.openDockerImgDialog(s, op, nil)
				}
			}
			if len(selected) > 0 {
				dockerMoreButton(c, locales.DockerImgSelected(len(selected)), func(menu *ui.Menu) {
					if len(selected) == 1 {
						a.dockerImgMenu(c, menu, s, selected[0], selected)
						return
					}
					a.dockerImgRemoveMenu(menu, s, selected)
				})
			}
		})
		if page.err != nil {
			message := locales.DockerImgListError(page.err.Error())
			if page.readOutput != "" {
				message += "\n" + page.readOutput
			}
			widgets.Muted(c, "!").TextColor(theme.Danger).Tooltip(message)
		}
		if page.loaded {
			text := locales.DockerImgCount(len(rows), len(page.rows), dockerImgSize(rows))
			widgets.Muted(c, text).FontSize(12).SingleLine().Tooltip(text)
		}
	})
}

func (a *App) dockerImgTable(c *ui.Context, s *Session, rows []monitor.ImageRow, users map[string][]string) {
	page := &s.docker.images
	columns := []ui.TableColumn{
		{ID: "ref", Title: locales.DockerImgReference(), MinWidth: 125, Sortable: true},
		{ID: "id", Title: locales.DockerID(), Width: 118, MinWidth: 55, Sortable: true},
		{ID: "size", Title: locales.DockerSize(), Width: 84, Align: ui.End, Sortable: true},
		{ID: "created", Title: locales.DockerCreatedAt(), Width: 110, Sortable: true},
		{ID: "used", Title: locales.DockerImgUsed(), Width: 96, Align: ui.End, Sortable: true},
		{ID: "platform", Title: locales.DockerImgPlatform(), Width: 108, Sortable: true},
	}
	width, _ := c.Size()
	if !a.current().isPanelTab() && a.panelOnRight() {
		width = a.rightWidth
	} else {
		width -= a.sideWidth
		if a.monitorOnRight() {
			width -= a.rightWidth
		}
	}
	compact := width < 600
	if compact {
		columns[0].Title, columns[0].MinWidth = locales.DockerImgReferenceShort(), 96
		columns[1].Width, columns[2].Width, columns[3].Width = 82, 67, 78
		columns[4].Title, columns[4].Width, columns[5].Width = locales.DockerImgUsedShort(), 52, 82
	}
	usageKnown := dockerImgUsageKnown(s)
	page.rowIndex = slices.IndexFunc(rows, func(row monitor.ImageRow) bool { return dockerImgRowKey(row) == page.rowKey })
	table := &page.table
	table.Selected, table.Selection, table.Sort, table.KeepOffset = &page.rowIndex, &page.selected, &page.sort, true
	table.Key = func(index int) any { return dockerImgRowKey(rows[index]) }
	table.Label = func(index int) string { return rows[index].Ref }
	table.CellMenu = func(menu *ui.Menu, row, _ int) {
		if row >= 0 && row < len(rows) {
			selected := dockerImgSelected(page, rows)
			if len(selected) == 0 {
				selected = []monitor.ImageRow{rows[row]}
			}
			a.dockerImgMenu(c, menu, s, rows[row], selected)
		}
	}
	list := ui.Table(c, table, columns, len(rows), func(index, col int) {
		row := rows[index]
		switch col {
		case 0:
			if row.Untagged {
				label := locales.DockerImgUntagged()
				if row.Dangling {
					label = locales.DockerImgDangling()
				}
				widgets.Muted(c, label).SingleLine().Tooltip(row.ID)
			} else {
				name := row.Ref
				if compact {
					if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
						name = name[slash+1:]
					}
				}
				ui.Text(c, name).SingleLine().Tooltip(row.Ref)
			}
		case 1:
			id := monitor.ImageIDKey(row.ID)
			length := 12
			if compact {
				length = 8
			}
			ui.Text(c, id[:min(len(id), length)]).SingleLine().Tooltip(row.ID)
		case 2:
			text := formatSize(int64(row.Size))
			if compact {
				ui.Text(c, formatBytes(float64(row.Size))).SingleLine().Tooltip(text)
			} else {
				ui.Text(c, text).SingleLine()
			}
		case 3:
			if row.Created.IsZero() {
				widgets.Muted(c, orDash("")).SingleLine()
			} else {
				age := humanDuration(time.Since(row.Created))
				if !compact {
					age = locales.DockerImgAgo(age)
				}
				ui.Text(c, age).SingleLine().Tooltip(localTime(row.Created))
			}
		case 4:
			names := users[monitor.ImageIDKey(row.ID)]
			text := strconv.Itoa(len(names))
			if !usageKnown {
				text += "+?"
			}
			tip := strings.Join(names, "\n")
			if tip == "" && usageKnown {
				tip = locales.DockerImgNoUsers()
			} else if !usageKnown {
				tip = strings.TrimSpace(tip + "\n" + locales.DockerImgUsageUnknown())
			}
			ui.Text(c, text).SingleLine().Tooltip(tip)
		case 5:
			platform := row.Platform
			if compact {
				platform = strings.TrimPrefix(platform, "linux/")
			}
			ui.Text(c, orDash(platform)).SingleLine().Tooltip(row.Platform)
		}
	}).Grow(1).MinHeight(0).KeepScrollbars(true).Label(locales.DockerImages())
	if page.rowIndex >= 0 && page.rowIndex < len(rows) {
		row := rows[page.rowIndex]
		page.rowKey = dockerImgRowKey(row)
		if list.Submitted() {
			a.openDockerImgDialog(s, imgDetails, []monitor.ImageRow{row})
		}
	}
}
