package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type dockerImgOp int

const (
	imgDetails dockerImgOp = iota
	imgHistory
	imgPull
	imgBuild
	imgLoad
	imgPrune
	imgTag
	imgPush
	imgRemove
	imgSave
)

func (op dockerImgOp) title() string {
	switch op {
	case imgDetails:
		return locales.DockerDetails()
	case imgHistory:
		return locales.DockerImgHistory()
	case imgPull:
		return locales.DockerImgPull()
	case imgBuild:
		return locales.DockerImgBuild()
	case imgLoad:
		return locales.DockerImgLoad()
	case imgPrune:
		return locales.DockerImgPrune()
	case imgTag:
		return locales.DockerImgTag()
	case imgPush:
		return locales.DockerImgPush()
	case imgRemove:
		return locales.DockerImgRemove()
	case imgSave:
		return locales.DockerImgSave()
	}
	return ""
}

func (dialog *dockerImgDialog) title() string {
	if dialog.op == imgRemove && len(dialog.rows) > 1 {
		return locales.DockerImgRemoveMany()
	}
	if dialog.op == imgLoad && dialog.importing {
		return locales.DockerImgImport()
	}
	return dialog.op.title()
}

type dockerImgDialog struct {
	target                 dockerTarget
	tool                   monitor.DockerCmd
	op                     dockerImgOp
	rows                   []monitor.ImageRow
	value, platform, local string
	importing, gzip, force bool
	forceOnly, all         bool
	pruneRows              []monitor.ImageRow
	pruneIDs               []string
	search, showEnv        bool
	showJSON, picking      bool
	searchTerm             string
	suggestions            []string
	stopSearch             context.CancelFunc
	build                  monitor.ImageBuild
	loading                bool
	inspect                monitor.ImageInspect
	layers                 []monitor.ImageLayer
	readErr                error
	readOutput             string
	readScroll             ui.ScrollState
	work                   *dockerWork
	stopWork, stopRead     context.CancelFunc
	stopping               bool
	validation             string
}

func dockerImgReadyReason(s *Session, requireList bool) string {
	state, page := &s.overview.docker, &s.docker.images
	if !dockerImgTarget(s).available() || state.d.State != monitor.DockerOK {
		return locales.DockerImgUnavailable()
	}
	if state.work != nil || page.dialog != nil && page.dialog.work != nil && !page.dialog.work.done {
		return locales.DockerImgBusy()
	}
	if requireList && (!page.loaded || page.loading || page.err != nil || page.client != s.client || page.tool != monitor.CmdOf(&state.d)) {
		return locales.DockerImgListNotReady()
	}
	return ""
}

func dockerImgRemoveReason(s *Session, rows []monitor.ImageRow, force bool) string {
	if reason := dockerImgReadyReason(s, true); reason != "" {
		return reason
	}
	if !dockerImgUsageKnown(s) || s.overview.docker.loading {
		return locales.DockerImgUsageUnknown()
	}
	if force && s.overview.docker.d.Tool == "podman" {
		return locales.DockerImgPodmanForce()
	}
	users := s.docker.images.usage(&s.overview.docker.d)
	if !force && slices.ContainsFunc(rows, func(row monitor.ImageRow) bool { return len(users[monitor.ImageIDKey(row.ID)]) > 0 }) {
		return locales.DockerImgUsedReason()
	}
	return ""
}

func dockerImgItem(menu *ui.Menu, title, reason string) bool {
	item := menu.Item(title)
	if reason != "" {
		item.Subtitle(reason).Disabled(true)
	}
	return item.Chosen()
}

func (a *App) dockerImgMenu(c *ui.Context, menu *ui.Menu, s *Session, row monitor.ImageRow, selected []monitor.ImageRow) {
	readReason := ""
	if !dockerImgTarget(s).available() || !monitor.ValidImageID(row.ID) {
		readReason = locales.DockerImgUnavailable()
	}
	for _, op := range []dockerImgOp{imgDetails, imgHistory} {
		if dockerImgItem(menu, op.title(), readReason) {
			a.openDockerImgDialog(s, op, []monitor.ImageRow{row})
		}
	}
	menu.Separator()
	if dockerImgItem(menu, locales.DockerImgRun(), readReason) {
		a.openDockerPage(s, dockerSecContainers)
	}
	for _, op := range []dockerImgOp{imgTag, imgPush, imgSave} {
		reason := dockerImgReadyReason(s, true)
		if reason == "" && op == imgPush && row.Untagged {
			reason = locales.DockerImgTagFirst()
		}
		if dockerImgItem(menu, op.title(), reason) {
			a.openDockerImgDialog(s, op, []monitor.ImageRow{row})
		}
	}
	menu.Submenu(locales.Copy(), func(sub *ui.Menu) {
		if sub.Item(locales.DockerID()).Chosen() {
			c.WriteClipboard(row.ID)
		}
		if dockerImgItem(sub, locales.DockerImgReference(), imgWhen(row.Untagged, locales.DockerImgTagFirst())) {
			c.WriteClipboard(row.Ref)
		}
	})
	menu.Separator()
	a.dockerImgRemoveMenu(menu, s, selected)
}

func imgWhen(on bool, message string) string {
	if on {
		return message
	}
	return ""
}

func (a *App) dockerImgRemoveMenu(menu *ui.Menu, s *Session, rows []monitor.ImageRow) {
	label := locales.DockerImgRemove()
	if len(rows) > 1 {
		label = locales.DockerImgRemoveMany()
	}
	if dockerImgItem(menu, label, dockerImgRemoveReason(s, rows, false)) {
		a.openDockerImgDialog(s, imgRemove, rows)
	}
	if dockerImgItem(menu, locales.DockerForceRemove(), dockerImgRemoveReason(s, rows, true)) {
		a.openDockerImgDialog(s, imgRemove, rows)
		s.docker.images.dialog.forceOnly = true
	}
}

func (a *App) openDockerImgDialog(s *Session, op dockerImgOp, rows []monitor.ImageRow) {
	page := &s.docker.images
	if page.dialog != nil && page.dialog.work != nil && !page.dialog.work.done {
		return
	}
	if page.dialog != nil {
		page.dialog.close()
	}
	dialog := &dockerImgDialog{target: dockerImgTarget(s), tool: monitor.CmdOf(&s.overview.docker.d), op: op, rows: slices.Clone(rows)}
	page.dialog = dialog
	if (op == imgDetails || op == imgHistory) && len(rows) == 1 {
		a.readDockerImgDialog(dialog, rows[0].ID)
	}
	if op == imgPrune {
		dialog.snapshotPrune(s)
	}
}

func (dialog *dockerImgDialog) close() {
	if dialog.stopSearch != nil {
		dialog.stopSearch()
	}
	if dialog.stopRead != nil {
		dialog.stopRead()
	}
}

func (dialog *dockerImgDialog) current() bool {
	s := dialog.target.session
	return s.docker.images.dialog == dialog && dialog.target.available() && dialog.tool == monitor.CmdOf(&s.overview.docker.d)
}

func (a *App) readDockerImgDialog(dialog *dockerImgDialog, id string) {
	read := dialog.tool.ImageInspect
	if dialog.op == imgHistory {
		read = dialog.tool.ImageHistory
	}
	script, err := read(id)
	if err != nil {
		dialog.readErr = err
		return
	}
	ctx, cancel := dockerImgContext(dialog.target.session, dockerTimeout)
	dialog.loading, dialog.stopRead = true, cancel
	run := a.dockerExec(dialog.target.session, monitor.RunImageProgress, nil, false).command
	go func() {
		defer cancel()
		output, err := run(ctx, dialog.target.client, script, nil)
		var inspect monitor.ImageInspect
		var layers []monitor.ImageLayer
		if err == nil {
			if dialog.op == imgHistory {
				layers, err = monitor.ParseImageHistory(output)
			} else {
				var results []monitor.ImageInspect
				results, err = monitor.ParseImageInspect(output)
				if err == nil {
					if len(results) == 1 && monitor.ImageIDKey(results[0].ID) == monitor.ImageIDKey(id) {
						inspect = results[0]
					} else {
						err = monitor.ErrImageReference
					}
				}
			}
		}
		a.post(func() {
			if dialog.current() {
				dialog.loading, dialog.inspect, dialog.layers = false, inspect, layers
				dialog.readErr = err
				if err != nil {
					dialog.readOutput = dockerImgTail(output)
				}
			}
		})
	}()
}

func (a *App) requestDockerImgSearch(dialog *dockerImgDialog) {
	term := strings.TrimSpace(dialog.value)
	if !dialog.search || len(term) < 3 {
		term = ""
	}
	if term == dialog.searchTerm {
		return
	}
	if dialog.stopSearch != nil {
		dialog.stopSearch()
	}
	dialog.searchTerm, dialog.suggestions = term, nil
	script, err := dialog.tool.ImageSearch(term)
	if err != nil {
		return
	}
	ctx, cancel := dockerImgContext(dialog.target.session, 10*time.Second)
	dialog.stopSearch = cancel
	run := a.dockerExec(dialog.target.session, monitor.RunImageProgress, nil, false).command
	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(350 * time.Millisecond):
		}
		output, err := run(ctx, dialog.target.client, script, nil)
		if err == nil && ctx.Err() == nil {
			found := monitor.ParseImageSearch(output)
			a.post(func() {
				if dialog.current() && dialog.searchTerm == term && dialog.work == nil {
					dialog.suggestions = found[:min(len(found), 5)]
				}
			})
		}
	}()
}

func dockerImgPruneCandidates(images []monitor.ImageRow, users map[string][]string, all bool) []monitor.ImageRow {
	tagged := map[string]bool{}
	for _, row := range images {
		if !row.Untagged {
			tagged[monitor.ImageIDKey(row.ID)] = true
		}
	}
	var rows []monitor.ImageRow
	for _, row := range images {
		id := monitor.ImageIDKey(row.ID)
		if (all || row.Dangling && !tagged[id]) && len(users[id]) == 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

func dockerImgPruneRows(s *Session, all bool) []monitor.ImageRow {
	page := &s.docker.images
	return dockerImgPruneCandidates(page.rows, page.usage(&s.overview.docker.d), all)
}

func (dialog *dockerImgDialog) snapshotPrune(s *Session) {
	dialog.pruneRows = dockerImgPruneRows(s, dialog.all)
	dialog.pruneIDs = dockerImgContainerIDs(s)
}

func dockerImgContainerIDs(s *Session) []string {
	ids := make([]string, 0, len(s.overview.docker.d.Containers))
	for _, container := range s.overview.docker.d.Containers {
		ids = append(ids, container.ID)
	}
	slices.Sort(ids)
	return ids
}

func dockerImgSortedKeys(rows []monitor.ImageRow) [][2]string {
	result := make([][2]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, dockerImgRowKey(row))
	}
	slices.SortFunc(result, func(left, right [2]string) int {
		if order := strings.Compare(left[0], right[0]); order != 0 {
			return order
		}
		return strings.Compare(left[1], right[1])
	})
	return result
}

func dockerImgPruneCheck(s *Session, dialog *dockerImgDialog, run dockerRunner) func(context.Context) (string, func() error, error) {
	tool, client, all := dialog.tool, dialog.target.client, dialog.all
	users := s.docker.images.usage(&s.overview.docker.d)
	approved := dockerImgSortedKeys(dialog.pruneRows)
	approvedIDs := slices.Clone(dialog.pruneIDs)
	notCurrent := errors.New(locales.DockerImgPruneChanged())
	return func(ctx context.Context) (string, func() error, error) {
		data, err := run(ctx, client, tool.ImagePruneContainerIDs(), nil)
		ids := strings.Fields(data)
		slices.Sort(ids)
		if err != nil || !slices.Equal(ids, approvedIDs) {
			return "", nil, errors.Join(notCurrent, err)
		}
		data, err = run(ctx, client, tool.ImageList(), nil)
		if err != nil {
			return "", nil, errors.Join(notCurrent, err)
		}
		rows, err := monitor.ParseImageList(data)
		if err != nil {
			return "", nil, errors.Join(notCurrent, err)
		}
		if !all && !dockerImgMarkDangling(ctx, run, client, tool, rows) || !slices.Equal(dockerImgSortedKeys(dockerImgPruneCandidates(rows, users, all)), approved) {
			return "", nil, notCurrent
		}
		return tool.ImagePrune(all), nil, nil
	}
}

func (dialog *dockerImgDialog) script() (string, error) {
	value := strings.TrimSpace(dialog.value)
	platform := strings.TrimSpace(dialog.platform)
	var ref, id string
	if len(dialog.rows) > 0 {
		ref, id = dialog.rows[0].Ref, dialog.rows[0].ID
	}
	switch dialog.op {
	case imgPull:
		return dialog.tool.ImagePull(value, platform, dialog.all)
	case imgBuild:
		build := dialog.build
		build.Context, build.Platform = value, platform
		build.Dockerfile, build.Target = strings.TrimSpace(build.Dockerfile), strings.TrimSpace(build.Target)
		var tags, args []string
		for _, tag := range build.Tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, tag)
			}
		}
		for _, arg := range build.Args {
			if arg != "" {
				args = append(args, arg)
			}
		}
		build.Tags, build.Args = tags, args
		return dialog.tool.ImageBuild(build)
	case imgLoad:
		if dialog.local != "" {
			value = "/tmp/image-archive.tar"
		}
		return dialog.loadScript(value)
	case imgPrune:
		return dialog.tool.ImagePrune(dialog.all), nil
	case imgTag:
		return dialog.tool.ImageTag(id, value)
	case imgPush:
		return dialog.tool.ImagePush(ref)
	case imgRemove:
		refs := make([]string, 0, len(dialog.rows))
		for _, row := range dialog.rows {
			refs = append(refs, row.Ref)
		}
		return dialog.tool.ImageRemove(dialog.force, refs...)
	case imgSave:
		return dialog.tool.ImageSave(value, dialog.gzip, ref)
	default:
		return "", monitor.ErrImageReference
	}
}

func (dialog *dockerImgDialog) loadScript(archive string) (string, error) {
	if dialog.importing {
		return dialog.tool.ImageImport(archive, strings.TrimSpace(dialog.platform))
	}
	return dialog.tool.ImageLoad(archive)
}

func (dialog *dockerImgDialog) blocked() string {
	s := dialog.target.session
	if !dialog.current() {
		return locales.DockerImgUnavailable()
	}
	requireList := dialog.op != imgPull && dialog.op != imgBuild && dialog.op != imgLoad
	if reason := dockerImgReadyReason(s, requireList); reason != "" {
		return reason
	}
	if len(dialog.rows) > 0 && requireList {
		for _, row := range dialog.rows {
			if !slices.ContainsFunc(s.docker.images.rows, func(now monitor.ImageRow) bool { return dockerImgRowKey(row) == dockerImgRowKey(now) }) {
				return locales.DockerImgUnavailable()
			}
		}
	}
	switch dialog.op {
	case imgRemove:
		if dialog.forceOnly && !dialog.force {
			return locales.DockerImgForceRequired()
		}
		return dockerImgRemoveReason(s, dialog.rows, dialog.force)
	case imgPrune:
		if !dockerImgUsageKnown(s) || s.overview.docker.loading {
			return locales.DockerImgUsageUnknown()
		}
		if !dialog.all && !s.docker.images.danglingKnown {
			return locales.DockerImgDanglingUnknown()
		}
		if !slices.Equal(dockerImgContainerIDs(s), dialog.pruneIDs) || !slices.Equal(dockerImgSortedKeys(dockerImgPruneRows(s, dialog.all)), dockerImgSortedKeys(dialog.pruneRows)) {
			return locales.DockerImgPruneChanged()
		}
		if len(dialog.pruneRows) == 0 {
			return locales.DockerImgPruneEmpty()
		}
	case imgLoad:
		if dialog.local != "" && s.sftp == nil {
			return locales.DockerImgNoSFTP()
		}
	}
	return ""
}

func dockerImgTail(output string) string {
	const limit = 64 << 10
	return strings.ToValidUTF8(output[max(0, len(output)-limit):], "�")
}

func (a *App) runDockerImgDialog(dialog *dockerImgDialog) {
	if reason := dialog.blocked(); reason != "" {
		dialog.validation = reason
		return
	}
	script, err := dialog.script()
	if err != nil {
		dialog.validation = dockerImgError(err)
		return
	}
	if dialog.op == imgRemove || dialog.op == imgPush || dialog.op == imgSave {
		guard, err := dialog.tool.ImageGuard(dialog.rows)
		if err != nil {
			dialog.validation = dockerImgError(err)
			return
		}
		script = guard + script
	}
	if dialog.stopSearch != nil {
		dialog.stopSearch()
	}
	s := dialog.target.session
	ctx, cancel := dockerImgContext(s, 60*time.Minute)
	work := &dockerWork{}
	dialog.work, dialog.stopWork, dialog.validation = work, cancel, ""
	run, report := a.dockerExec(s, monitor.RunImageProgress, nil, false).command, a.dockerProgress(work)
	var prepare func(context.Context) (string, func() error, error)
	if dialog.op == imgPrune {
		prepare = dockerImgPruneCheck(s, dialog, run)
	} else if dialog.op == imgLoad && dialog.local != "" {
		local, mode, tag := dialog.local, dialog.importing, strings.TrimSpace(dialog.platform)
		tool, client := dialog.tool, dialog.target.client
		uploaded := locales.DockerImgUploaded()
		staging := monitor.RunDockerProgress
		if s.overview.docker.run != nil {
			staging = s.overview.docker.run
		}
		prepare = func(ctx context.Context) (string, func() error, error) {
			return dockerImgUpload(ctx, client, tool, staging, local, mode, tag, report, uploaded)
		}
	}
	go func() {
		defer cancel()
		var output string
		if prepare != nil {
			var cleanup func() error
			script, cleanup, err = prepare(ctx)
			if err == nil {
				output, err = run(ctx, dialog.target.client, script, report)
			}
			if cleanup != nil {
				if cleanupErr := cleanup(); cleanupErr != nil {
					err = errors.Join(err, cleanupErr)
				}
			}
		} else {
			output, err = run(ctx, dialog.target.client, script, report)
		}
		a.post(func() {
			work.done, work.err = true, err
			if output != "" {
				work.output = dockerImgTail(output)
			}
			if !dialog.current() || !slices.Contains(a.sessions, s) {
				return
			}
			if dialog.op != imgSave {
				a.refreshDockerImages(s)
				a.readDockerOf(s)
			}
		})
	}()
}
