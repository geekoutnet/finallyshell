package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const dockerEventLimit = 500

type dockerSystemEventsPage struct {
	poll          sessionPoll
	table         dockerResourceTable
	rows          []monitor.DockerEvent
	last, cleared time.Time
	paused, ended bool
	kind          string
	streamID      *dockerEventStream
}

type dockerEventStream struct{ anchor time.Time }

func (a *App) keepDockerEvents(s *Session, page *dockerResourcesPage) {
	state := &page.system.events
	if state.paused || !resourceTarget(s).available() {
		a.stopPoll(&state.poll)
		return
	}
	a.keepPolling(&state.poll, s, 3*time.Second, func() pollRead {
		stream := a.dockerExec(s, monitor.StreamDockerEvents, nil, false).command
		if page.events != nil && !page.tool.Root {
			stream = page.events
		}
		state.ended = false
		streamID := &dockerEventStream{anchor: state.last}
		state.streamID = streamID
		tool, initial := page.tool, streamID.anchor
		sessionCtx := resourceContext(s)
		return func(ctx context.Context, client *ssh.Client) func() {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(sessionCtx, cancel)
			defer stop()
			since := "30m"
			watermark := initial
			if !watermark.IsZero() {
				since = watermark.UTC().Format(time.RFC3339Nano)
			}
			var mutex sync.Mutex
			var pending []monitor.DockerEvent
			queued := false
			report, flush := monitor.DockerEventLines(func(event monitor.DockerEvent) {
				if event.Time.After(initial) {
					initial = event.Time
				}
				if !event.Time.IsZero() && event.Time.Before(watermark) {
					return
				}
				mutex.Lock()
				if len(pending) == dockerEventLimit {
					pending = slices.Delete(pending, 0, 1)
				}
				pending = append(pending, event)
				if queued {
					mutex.Unlock()
					return
				}
				queued = true
				mutex.Unlock()
				a.post(func() {
					mutex.Lock()
					batch := pending
					pending, queued = nil, false
					mutex.Unlock()
					if state.poll.run == nil || state.streamID != streamID || page.tool != tool || s.client != client || state.paused || sessionCtx.Err() != nil {
						return
					}
					state.ended = false
					for _, item := range batch {
						state.add(item)
					}
				})
			})
			_, _ = stream(ctx, client, tool.Events(since), report)
			flush()
			return func() { state.ended = true }
		}
	})
}

func (state *dockerSystemEventsPage) add(event monitor.DockerEvent) {
	if !state.cleared.IsZero() && !event.Time.After(state.cleared) {
		return
	}
	if event.Time.After(state.last) {
		state.last = event.Time
	}
	if len(state.rows) > 0 {
		for index := len(state.rows) - 1; index >= 0 && !state.rows[index].Time.Before(event.Time); index-- {
			old := state.rows[index]
			if event.Time.Equal(old.Time) && event.Type == old.Type && event.Action == old.Action && event.ID == old.ID {
				return
			}
		}
	}
	if len(state.rows) == dockerEventLimit {
		state.rows = slices.Delete(state.rows, 0, 1)
	}
	state.rows = append(state.rows, event)
}

func (a *App) dockerEventsView(c *ui.Context, s *Session, page *dockerResourcesPage) {
	state := &page.system.events
	allTypes := locales.DockerSysAllTypes()
	types := []string{allTypes}
	for _, event := range state.rows {
		if !slices.Contains(types, event.Type) {
			types = append(types, event.Type)
		}
	}
	slices.Sort(types[1:])
	selected := state.kind
	if !slices.Contains(types, selected) {
		selected = allTypes
	}
	columns := []ui.TableColumn{
		resourceColumn("time", locales.BackupTime(), 158), resourceColumn("type", locales.DockerSysType(), 95),
		resourceColumn("action", locales.DockerSysEventAction(), 105), resourceColumn("actor", locales.DockerSysEventActor(), 145),
		resourceColumn("attributes", locales.DockerSysEventAttrs(), 265),
	}
	var rows []dockerResourceRow
	keys := make(map[string]monitor.DockerEvent)
	for index := len(state.rows) - 1; index >= 0; index-- {
		event := state.rows[index]
		if selected != allTypes && selected != event.Type {
			continue
		}
		key := strings.Join([]string{event.Time.Format(time.RFC3339Nano), event.ID, event.Type, event.Action}, ":")
		keys[key] = event
		name := cmp.Or(event.Name, event.ID)
		rows = append(rows, dockerResourceRow{key, name, []string{localTime(event.Time), event.Type, event.Action, name, strings.ReplaceAll(resourcePairText(event.Attributes), "\n", ", ")}, map[string]int64{"time": event.Time.UnixNano()}})
	}
	if state.table.sort.Column == "" {
		state.table.sort = ui.SortOrder{Column: "time", Descending: true}
	}
	rows = state.table.shown(rows, columns)
	resourceToolbar(c, &state.table, len(rows), func() {
		if ui.Select(c, &selected, types).Label(locales.DockerSysType()).MaxWidth(115).Shrink(0).Changed() {
			state.kind = selected
		}
		ui.Checkbox(c, &state.paused, locales.DockerSysPause()).Shrink(0)
		if ui.Button(c, locales.DockerSysClear()).Shrink(0).Clicked() {
			state.rows = nil
			state.cleared = state.last
		}
	})
	if state.ended && !state.paused {
		widgets.Muted(c, locales.DockerSysEventRetry()).FontSize(11).Padding(1, 8)
	}
	details := func(key string) {
		event := keys[key]
		page.show(locales.DockerDetails(), func(c *ui.Context, _ *dockerResourceDialog, open *bool) {
			ctrRows(c, []labelValue{{locales.BackupTime(), localTime(event.Time)}, {locales.DockerSysType(), event.Type},
				{locales.DockerSysEventAction(), event.Action}, {locales.DockerSysEventActor(), event.Name}, {locales.DockerID(), event.ID},
				{locales.DockerSysEventAttrs(), resourcePairText(event.Attributes)}}, 130)
			if ui.Button(c, locales.Close()).Clicked() {
				*open = false
			}
		})
	}
	state.table.draw(c, locales.DockerSysEvents(), columns, rows, func(m *ui.Menu, key string) {
		if m.Item(locales.DockerDetails()).Chosen() {
			details(key)
		}
		event := keys[key]
		resourceCopy(c, m, labelValue{locales.DockerSysEventActor(), cmp.Or(event.Name, event.ID)}, labelValue{locales.DockerSysEventAttrs(), resourcePairText(event.Attributes)})
	}, details)
}
