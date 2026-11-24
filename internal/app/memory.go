package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type memoryState struct {
	client        *ssh.Client
	confirm       monitor.MemoryAction
	busy          bool
	before, after monitor.Memory
	output        string
	err           error
}

func memoryActionName(action monitor.MemoryAction) string {
	switch action {
	case monitor.MemoryCompact:
		return locales.MemoryCompact()
	default:
		return locales.MemoryDropCaches()
	}
}

func memoryActionWarning(action monitor.MemoryAction) string {
	switch action {
	case monitor.MemoryCompact:
		return locales.MemoryCompactWarning()
	default:
		return locales.MemoryDropWarning()
	}
}

func memoryErrorText(err error) string {
	switch {
	case errors.Is(err, monitor.ErrMemoryUnavailable):
		return locales.MemoryUnavailable()
	case errors.Is(err, monitor.ErrMemoryNoCompact):
		return locales.MemoryNoCompact()
	case errors.Is(err, monitor.ErrMemoryPrivilege):
		return locales.MemoryNeedsRoot()
	case errors.Is(err, monitor.ErrMemoryRestricted):
		return locales.MemoryRestricted()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.SysInfoTimeout()
	default:
		return err.Error()
	}
}

func memoryActionBlock(mem monitor.Memory, action monitor.MemoryAction) error {
	if total, _ := mem.Value("MemTotal"); total == 0 {
		return monitor.ErrMemoryUnavailable
	}
	switch action {
	case monitor.MemoryCompact:
		if _, ok := mem.Value("compact_memory"); !ok {
			return monitor.ErrMemoryNoCompact
		}
	}
	return nil
}

func (a *App) runMemoryAction(s *Session, action monitor.MemoryAction) {
	state, client := &s.overview.memory, s.client
	run := s.overview.memoryRunner
	if state.busy || state.client != client || s.status != statusConnected || !execAllowed(s.Host) || client == nil && run == nil {
		return
	}
	if run == nil {
		run = func(ctx context.Context, script string) (string, string, error) {
			return monitor.RunScript(ctx, client, script)
		}
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	state.busy, state.confirm, state.err, state.output = true, 0, nil, ""
	state.before = monitor.Memory{}
	go func() {
		read := func() (monitor.Memory, error) {
			ctx, cancel := context.WithTimeout(parent, sysInfoTimeout)
			defer cancel()
			return monitor.ReadMemory(ctx, run)
		}
		before, err := read()
		if err == nil {
			err = memoryActionBlock(before, action)
		}
		var after monitor.Memory
		var output string
		if err == nil && parent.Err() == nil {
			ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
			output, err = monitor.RunMemoryAction(ctx, run, action)
			cancel()
			after, _ = read()
			after.Rates = after.SwapRates(before)
		}
		a.post(func() {
			if s.client != client || state.client != client || parent.Err() != nil {
				return
			}
			if err != nil {
				before = monitor.Memory{}
			}
			state.busy, state.before, state.after, state.output, state.err = false, before, after, output, err
		})
	}()
}

func memorySize(value uint64) string {
	switch {
	case value >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(value)/(1<<30))
	case value >= 10<<20:
		return fmt.Sprintf("%.0f MB", float64(value)/(1<<20))
	default:
		return formatSize(int64(value))
	}
}

func memoryValue(mem monitor.Memory, key string) string {
	if value, ok := mem.Value(key); ok {
		return memorySize(value)
	}
	return "—"
}

func memoryCommit(mem monitor.Memory) string {
	text := memoryValue(mem, "Committed_AS") + " / " + memoryValue(mem, "CommitLimit")
	used, hasUsed := mem.Value("Committed_AS")
	limit, hasLimit := mem.Value("CommitLimit")
	if hasUsed && hasLimit && limit > 0 {
		text += fmt.Sprintf(" (%.0f%%)", 100*float64(used)/float64(limit))
	}
	return text
}

func memorySwapUsed(mem monitor.Memory) string {
	if used, ok := mem.SwapUsed(); ok {
		return memorySize(used)
	}
	return "—"
}

func memoryCache(mem monitor.Memory) string {
	if _, hasCache := mem.Value("Cached"); !hasCache {
		if _, hasBuffers := mem.Value("Buffers"); !hasBuffers {
			if _, hasSlab := mem.Value("SReclaimable"); !hasSlab {
				return "—"
			}
		}
	}
	return memorySize(mem.PhysicalParts()[1])
}

func memoryRate(rate monitor.SwapRate) string {
	if !rate.Known {
		return "—"
	}
	return locales.MemoryPagesRate(fmt.Sprintf("%.1f", rate.PagesPerSecond))
}

func (a *App) memoryCard(c *ui.Context, s *Session, width float32) {
	state := &s.overview.memory
	if state.client != s.client {
		*state = memoryState{client: s.client}
	}
	mem := s.stats.Memory
	if state.after.SampledAt.After(mem.SampledAt) {
		mem = state.after
	}
	total, _ := mem.Value("MemTotal")
	ready := total > 0
	overviewCard(c.Key("memory"), "cpu", locales.SysMemory(), func() {
		if state.busy {
			ui.Spinner(c).Size(14, 14).Label(locales.MemoryWorking())
		}
		if ui.PrimaryButton(c, locales.MemoryFree()).FontSize(12).Padding(2, 8).Shrink(0).Disabled(!ready || state.busy).Clicked() {
			state.confirm = monitor.MemoryDropCaches
		}
		ui.Button(c, "⋯").Size(25, 24).Padding(0).Shrink(0).Label(locales.MemoryActions()).Tooltip(locales.MemoryActions()).Disabled(!ready || state.busy).
			PopoverMenuButton(func(menu *ui.Menu) {
				for _, action := range []monitor.MemoryAction{monitor.MemoryDropCaches, monitor.MemoryCompact} {
					problem := memoryActionBlock(mem, action)
					item := menu.Item(memoryActionName(action)).Disabled(problem != nil)
					if problem != nil {
						item.Subtitle(memoryErrorText(problem))
					}
					if item.Chosen() && problem == nil && !state.busy {
						state.confirm = action
					}
				}
			})
	}, func() {
		if !ready {
			message := locales.MemoryWaiting()
			if !mem.SampledAt.IsZero() {
				message = locales.MemoryUnavailable()
			}
			widgets.Muted(c, message)
			return
		}
		columns := 1
		if width >= 780 {
			columns = 2
		}
		ui.Grid(c).Columns(columns).FillWidth().Gap(14).Children(func() {
			memoryPhysical(c, mem)
			memorySwap(c, mem)
		})
		if state.busy {
			widgets.Muted(c, locales.MemoryWorking())
		} else if !state.before.SampledAt.IsZero() && state.err == nil {
			memoryResult(c, state.before, state.after)
		}
		if iconTextButton(c, "arrow-up-right", locales.OverviewTopProcs()).AlignSelf(ui.Start).Clicked() {
			s.procs.sort = ui.SortOrder{Column: "mem", Descending: true}
			a.openProcesses()
		}
	})
}

func memoryPhysical(c *ui.Context, mem monitor.Memory) {
	parts := mem.PhysicalParts()
	used := "—"
	if _, ok := mem.Value("MemFree"); ok {
		used = memorySize(parts[0])
	} else {
		parts = [3]uint64{}
	}
	memorySection(c, locales.MemoryPhysical(), parts,
		[3]string{locales.MemoryApps(), locales.MemoryCache(), locales.MemoryFreeSpace()}, func() {
			memoryRow(c, locales.MemoryTotal(), memoryValue(mem, "MemTotal"))
			memoryRow(c, locales.MemoryApps(), used)
			memoryRow(c, locales.MemoryAvailable(), memoryValue(mem, "MemAvailable")).Tooltip(locales.MemoryAvailableHint())
			memoryRow(c, locales.MemoryCache(), memoryCache(mem))
			memoryRow(c, locales.MemoryShared(), memoryValue(mem, "Shmem"))
			memoryRow(c, locales.MemoryDirty(), memoryValue(mem, "Dirty"))
			memoryRow(c, locales.MemoryCommitted(), memoryCommit(mem))
		})
}

func memorySwap(c *ui.Context, mem monitor.Memory) {
	swappiness := "—"
	if value, ok := mem.Value("swappiness"); ok {
		swappiness = fmt.Sprint(value)
	}
	memorySection(c, locales.Swap(), mem.SwapParts(),
		[3]string{locales.MemorySwapOccupied(), locales.MemorySwapCached(), locales.MemoryFreeSpace()}, func() {
			memoryRow(c, locales.MemorySwapUsed(), memorySwapUsed(mem)+" / "+memoryValue(mem, "SwapTotal"))
			memoryRow(c, locales.MemorySwapCached(), memoryValue(mem, "SwapCached"))
			memoryRow(c, locales.MemoryFreeSpace(), memoryValue(mem, "SwapFree"))
			memoryRow(c, locales.MemorySwapIn(), memoryRate(mem.Rates[0]))
			memoryRow(c, locales.MemorySwapOut(), memoryRate(mem.Rates[1]))
			memoryRow(c, locales.MemorySwappiness(), swappiness)
		})
}

func memorySection(c *ui.Context, title string, parts [3]uint64, labels [3]string, rows func()) {
	palette := [3]ui.Color{theme.Accent, theme.Success, colors.gaugeTrack}
	total := parts[0] + parts[1] + parts[2]
	ui.Column(c.Key(title)).FillWidth().MinWidth(0).Gap(5).Children(func() {
		ui.Text(c, title).FontSize(13).Bold()
		ui.Box(c).FillWidth().Height(8).Radius(4).Background(palette[2]).Clip().Children(func() {
			if total > 0 {
				width := func(value uint64) float32 { return float32(100 * float64(value) / float64(total)) }
				ui.Box(c).Absolute().Top(0).Left(0).Bottom(0).WidthPercent(width(parts[0] + parts[1])).Background(palette[1])
				ui.Box(c).Absolute().Top(0).Left(0).Bottom(0).WidthPercent(width(parts[0])).Background(palette[0])
			}
		})
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Margin(0, 0, 4, 0).Children(func() {
			for index, label := range labels {
				ui.Row(c.Key(index)).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(2).Background(palette[index]).Shrink(0)
					widgets.Muted(c, label).FontSize(11)
				})
			}
		})
		rows()
	})
}

func memoryRow(c *ui.Context, label, value string) ui.Element {
	return ui.Row(c.Key(label)).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
		widgets.Muted(c, label).FontSize(12).Grow(1).MinWidth(0)
		ui.Text(c, value).FontSize(12).FontFeatures("tnum").Shrink(0)
	})
}

func memoryResult(c *ui.Context, before, after monitor.Memory) {
	if after.SampledAt.IsZero() {
		ui.Text(c, locales.MemoryRefreshFailed()).FontSize(12).TextColor(theme.Warning)
		return
	}
	old, oldKnown := before.Value("MemAvailable")
	now, nowKnown := after.Value("MemAvailable")
	summary := locales.MemoryActionComplete()
	if oldKnown && nowKnown {
		summary = locales.MemoryAvailableChange(memorySize(old), memorySize(now))
	}
	ui.Column(c).FillWidth().Padding(7, 9).Gap(3).Radius(5).Background(theme.Accent.Alpha(0.09)).Children(func() {
		ui.Text(c, summary).FontSize(13).Bold()
		widgets.Muted(c, locales.MemoryResultChange(memoryCache(before), memoryCache(after), memorySwapUsed(before), memorySwapUsed(after))).FontSize(12)
	})
}

func (a *App) memoryDialog(c *ui.Context, s *Session) {
	state := &s.overview.memory
	if state.confirm == 0 && state.err == nil {
		return
	}
	open := true
	ui.Modal(c.Key("memory-dialog"), &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-64, 240), 480)).Gap(12).Children(func() {
			if state.err != nil {
				ui.Text(c, locales.MemoryFailed()).FontSize(16).Bold()
				ui.Text(c, memoryErrorText(state.err))
				if state.output != "" {
					ui.Scroll(c).FillWidth().MaxHeight(height / 3).Children(func() {
						ui.Text(c, state.output).Selectable().FontSize(12)
					})
				}
				if ui.Button(c, locales.Close()).AlignSelf(ui.End).Clicked() {
					open = false
				}
				return
			}
			action := state.confirm
			ui.Text(c, memoryActionName(action)).FontSize(16).Bold()
			widgets.Muted(c, s.Host.Title())
			ui.Text(c, memoryActionWarning(action))
			ui.Row(c).Justify(ui.End).Wrap().Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).AutoFocus().Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, memoryActionName(action)).Clicked() {
					a.runMemoryAction(s, action)
					open = false
				}
			})
		})
	})
	if !open {
		state.confirm, state.err, state.output = 0, nil, ""
	}
}
