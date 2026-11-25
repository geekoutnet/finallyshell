package app

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

var taskLoadSeries = [...]string{"load1", "load5", "load15"}

type taskCard struct {
	label, icon, primary, secondary string
	percent                         string
	fraction                        float64
	ink                             ui.Color
}

func desktopTaskLoadState(load float64, cores int) (float64, string) {
	if cores <= 0 {
		return 0, "—"
	}
	fraction := load / float64(cores)
	switch {
	case fraction >= 1:
		return fraction, locales.DesktopTaskOverloaded()
	case fraction >= 0.7:
		return fraction, locales.DesktopTaskBusy()
	default:
		return fraction, locales.DesktopTaskSmooth()
	}
}

func taskValue(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func taskCount(value int) string {
	if value <= 0 {
		return "—"
	}
	return strconv.Itoa(value)
}

func taskGHz(value float64) string {
	if value <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f GHz", value)
}

func taskPercent(value float64) string { return fmt.Sprintf("%.0f%%", value) }

func taskRate(value float64) string { return locales.TunBytesPerSecond(formatBytes(value)) }

func taskSize(value uint64) string {
	if value == 0 {
		return "—"
	}
	return memorySize(value)
}

func (state *desktopTaskState) diskChoice() (monitor.TaskDisk, []string) {
	var names []string
	var selected, mounted, whole monitor.TaskDisk
	for _, disk := range state.perf.Disks {
		names = append(names, disk.Name)
		if disk.Name == state.disk {
			selected = disk
		}
		if slices.Contains(disk.Mounts, "/") || mounted.Name == "" && len(disk.Mounts) != 0 {
			mounted = disk
		}
		if whole.Name == "" && !disk.Partition && !strings.HasPrefix(disk.Name, "loop") {
			whole = disk
		}
	}
	if selected.Name == "" && len(names) > 0 {
		switch {
		case mounted.Name != "":
			selected = mounted
		case whole.Name != "":
			selected = whole
		default:
			selected = state.perf.Disks[0]
		}
		state.disk = selected.Name
	}
	return selected, names
}

func (state *desktopTaskState) ifaceChoice(stats *monitor.Stats) (monitor.Iface, []string) {
	var names []string
	var selected, busiest monitor.Iface
	for _, iface := range stats.Ifaces {
		names = append(names, iface.Name)
		if iface.Name == state.iface {
			selected = iface
		}
		if iface.Name == stats.Iface {
			busiest = iface
		}
	}
	if selected.Name == "" && len(names) > 0 {
		selected = busiest
		if selected.Name == "" {
			selected = stats.Ifaces[0]
		}
		state.iface = selected.Name
	}
	return selected, names
}

func taskColors() [5]ui.Color {
	if theme.Dark {
		return [5]ui.Color{ui.Hex("#a8a0ff"), ui.Hex("#7ab9ff"), ui.Hex("#5fd8ab"), ui.Hex("#fbbb73"), ui.Hex("#69d8e9")}
	}
	return [5]ui.Color{ui.Hex("#5f49c9"), ui.Hex("#1765bd"), ui.Hex("#077a55"), ui.Hex("#a85b16"), ui.Hex("#087b93")}
}

func (state *desktopTaskState) cards(stats *monitor.Stats, disk monitor.TaskDisk, iface monitor.Iface) []taskCard {
	p := taskColors()
	load, loadLabel := desktopTaskLoadState(state.perf.Load[0], state.perf.Logical)
	items := []taskCard{
		{label: locales.DesktopTaskLoad(), icon: "gauge", primary: loadLabel, percent: "—", fraction: load, ink: p[0]},
		{label: locales.DesktopTaskCPU(), icon: "cpu", primary: taskGHz(state.perf.GHz), percent: "—", ink: p[1]},
		{label: locales.DesktopTaskMemory(), icon: "memory-stick", primary: "—", percent: "—", ink: p[2]},
		{label: locales.DesktopTaskDisk(), icon: "hard-drive", primary: taskValue(disk.Name), percent: "—", ink: p[3]},
		{label: locales.DesktopTaskNetwork(), icon: "network", primary: taskValue(iface.Name), ink: p[4]},
	}
	if state.perf.Logical > 0 {
		items[0].percent = taskPercent(load * 100)
		items[0].secondary = fmt.Sprintf("%s · %.2f", locales.DesktopTaskOneMinute(), state.perf.Load[0])
	}
	if stats.OK {
		items[1].percent, items[1].fraction = taskPercent(stats.CPU), stats.CPU/100
	}
	if len(state.perf.CPUs) > 0 && state.perf.CPUs[0].Name == "cpu" {
		usage := state.perf.CPUs[0].Usage()
		items[1].percent, items[1].fraction = taskPercent(usage), usage/100
	}
	if threads := max(state.perf.Logical, state.perf.Hardware.Threads); threads > 0 {
		items[1].secondary = locales.DesktopTaskThreads() + " · " + strconv.Itoa(threads)
	}
	if stats.MemTotal > 0 {
		fraction := ratio(stats.MemUsed, stats.MemTotal)
		items[2].percent, items[2].fraction = taskPercent(fraction*100), fraction
		items[2].primary = memorySize(stats.MemUsed)
		items[2].secondary = "/ " + memorySize(stats.MemTotal)
	}
	if disk.Measured {
		items[3].percent, items[3].fraction = taskPercent(disk.Busy), disk.Busy/100
		items[3].secondary = "↓ " + taskRate(disk.Read) + "  ↑ " + taskRate(disk.Write)
	}
	if iface.Name != "" {
		items[4].secondary = "↓ " + taskRate(iface.RxRate) + "  ↑ " + taskRate(iface.TxRate)
	}
	return items
}

func (a *App) desktopTaskPerformance(c *ui.Context, session *Session, state *desktopTaskState) {
	stats := &session.stats
	disk, disks := state.diskChoice()
	iface, ifaces := state.ifaceChoice(stats)
	items := state.cards(stats, disk, iface)
	ui.Scroll(c.Key("performance-scroll")).Grow(1).MinHeight(0).KeepScrollbars(true).Children(func() {
		body := ui.Column(c).FillWidth().MinWidth(0).Padding(12).Gap(12)
		width := body.Bounds().W - 24
		body.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.DesktopTaskPerformance()).FontSize(20).Bold().SingleLine().Grow(1).MinWidth(0)
				if desktopTaskConnected(session, state) {
					taskLiveBadge(c, false)
				}
			})
			if state.succeeded {
				taskNotice(c, locales.DesktopTaskSuccess(), false)
			}
			taskReadStatus(c, state.perfReady, desktopTaskConnected(session, state), state.perfErr)
			columns := 5
			if width > 0 && width < 690 {
				columns = 3
			}
			ui.Grid(c).Columns(columns).Gap(8).FillWidth().Children(func() {
				for index, item := range items {
					metric := taskMetric(index)
					if taskMetricCard(c, metric, item, state.metric == metric, columns < 5) {
						state.metric = metric
					}
				}
			})
			ui.Column(c.Key(state.metric)).Padding(13).Gap(11).FillWidth().MinWidth(0).Radius(9).Background(colors.card).Border(1, colors.line).Children(func() {
				item := items[state.metric]
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.Get(item.icon)).FontSize(16).TextColor(item.ink).Shrink(0)
					ui.Text(c, item.label).FontSize(16).Bold().SingleLine().Grow(1).MinWidth(0)
					switch state.metric {
					case taskDisk:
						if len(disks) > 0 {
							ui.Select(c, &state.disk, disks).Width(115).Label(locales.DesktopTaskDisk())
						}
					case taskNetwork:
						if len(ifaces) > 0 {
							ui.Select(c, &state.iface, ifaces).Width(115).Label(locales.DesktopTaskNetwork())
						}
					}
				})
				switch state.metric {
				case taskLoad:
					taskLoadDetail(c, state, width)
				case taskCPU:
					taskCPUDetail(c, state, stats, width)
				case taskMemory:
					a.desktopTaskMemory(c, session, state, width)
				case taskDisk:
					disk, _ = state.diskChoice()
					taskDiskDetail(c, state, disk, width)
				case taskNetwork:
					iface, _ = state.ifaceChoice(stats)
					taskNetworkDetail(c, state, iface, width)
				}
			})
		})
	})
}

func taskMetricCard(c *ui.Context, metric taskMetric, item taskCard, selected, compact bool) bool {
	height, pad := float32(90), float32(8)
	if compact {
		height, pad = 72, 5
	}
	card := ui.Column(c.Key(metric)).FillWidth().Height(height).MinWidth(0).Padding(pad, 8).Gap(2).Radius(8).
		Background(colors.card).Border(1, colors.line).Role(ui.RoleButton).Focusable().Label(item.label).AccessSelected(selected).
		Tooltip(strings.TrimSpace(item.primary + " " + item.secondary + " " + item.percent))
	if selected {
		card.Background(item.ink.Alpha(0.10)).Border(1, item.ink.Alpha(0.58))
	} else if card.Hovered() {
		card.Background(item.ink.Alpha(0.06)).Border(1, item.ink.Alpha(0.30))
	}
	card.Children(func() {
		ui.Row(c).Height(28).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Box(c).Size(23, 23).Center().Shrink(0).Radius(5).Background(item.ink.Alpha(0.13)).Children(func() {
				ui.Icon(c, icons.Get(item.icon)).FontSize(14).TextColor(item.ink)
			})
			ui.Text(c, item.label).SingleLine().MinWidth(0).Shrink(1).FontSize(12).Bold()
			if item.percent != "" {
				ui.Spacer(c)
				taskRing(c, item)
			}
		})
		ui.Text(c, item.primary).FontSize(12).Bold().SingleLine().MinWidth(0)
		if item.secondary != "" {
			widgets.Muted(c, item.secondary).FontSize(10).SingleLine().MinWidth(0)
		}
	})
	return card.Clicked()
}

func taskRing(c *ui.Context, item taskCard) {
	track := colors.gaugeTrack
	ui.Box(c).Size(30, 30).Center().Shrink(0).Draw(func(painter *ui.Painter, bounds ui.Rect) {
		var ring ui.Path
		ringPath(&ring, bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 13.5)
		painter.StrokePath(&ring, 2.4, track)
		if item.fraction > 0 {
			painter.StrokePath(ring.Trim(0, float32(min(item.fraction, 1))), 2.4, item.ink)
		}
	}).Children(func() {
		ui.Text(c, item.percent).TextColor(item.ink).SingleLine().FontSize(8).Bold().FontFeatures("tnum")
	})
}

func taskLiveBadge(c *ui.Context, paused bool) {
	ink, text := taskColors()[2], locales.DesktopTaskLive()
	if paused {
		ink, text = theme.TextMuted, locales.DesktopTaskPaused()
	}
	ui.Row(c).Padding(4, 7).Gap(5).AlignItems(ui.Center).Radius(12).Shrink(0).Background(ink.Alpha(0.10)).Children(func() {
		widgets.StatusDot(c, ink)
		ui.Text(c, text).TextColor(ink).FontSize(10).SingleLine()
	})
}

type taskSeries struct {
	label  string
	ink    ui.Color
	values []float64
}

func taskChart(c *ui.Context, height float32, scale float64, format func(float64) string, series ...taskSeries) {
	peak := scale
	if peak <= 0 {
		for _, curve := range series {
			for _, value := range curve.values {
				peak = max(peak, value)
			}
		}
		if peak > 0 {
			magnitude := math.Pow(10, math.Floor(math.Log10(peak)))
			peak = math.Ceil(peak/magnitude) * magnitude
		} else {
			peak = 1
		}
	}
	var ink = theme.TextMuted
	grid, bg := colors.line, colors.overviewPage
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Box(c).Height(height).FillWidth().Radius(6).Clip().Background(bg).Border(1, grid).
			Draw(func(painter *ui.Painter, bounds ui.Rect) {
				left, right := bounds.X+8, bounds.X+bounds.W-8
				top, bottom := bounds.Y+16, bounds.Y+bounds.H-12
				for row := range 5 {
					y := top + float32(row)*(bottom-top)/4
					painter.Line(left, y, right, y, 1, grid)
				}
				for col := range 7 {
					x := left + float32(col)*(right-left)/6
					painter.Line(x, top, x, bottom, 1, grid.Alpha(0.58))
				}
				for _, curve := range series {
					var points [][2]float32
					for index, value := range curve.values {
						slot := monitor.HistoryLen - len(curve.values) + index
						x := left + float32(slot)*(right-left)/float32(monitor.HistoryLen-1)
						y := bottom - float32(min(max(value, 0), peak)/peak)*(bottom-top)
						points = append(points, [2]float32{x, y})
					}
					if len(points) == 1 {
						painter.Fill(ui.Rect{X: points[0][0] - 2, Y: points[0][1] - 2, W: 4, H: 4}, curve.ink, 2)
					}
					if len(points) > 1 {
						painter.StrokePolyline(points, 1.7, curve.ink)
					}
				}
			}).Children(func() {
			ui.Text(c, format(peak)).Absolute().Top(2).Left(7).FontSize(9).TextColor(ink).Background(bg).PaddingX(2).ZIndex(1)
		})
		if height > 70 {
			ui.Row(c).Wrap().Gap(9).AlignItems(ui.Center).Children(func() {
				for _, curve := range series {
					ui.Row(c.Key(curve.label)).AlignItems(ui.Center).Gap(4).Children(func() {
						ui.Box(c).Size(9, 3).Radius(1.5).Background(curve.ink)
						widgets.Muted(c, curve.label).FontSize(10)
					})
				}
				ui.Spacer(c)
				widgets.Muted(c, locales.DesktopTaskHistory()).FontSize(10)
			})
		}
	})
}

type taskFact struct{ label, value string }

func taskFacts(c *ui.Context, width float32, facts ...taskFact) {
	columns := 3
	if width >= 700 {
		columns = 4
	}
	if width > 0 && width < 535 {
		columns = 2
	}
	ui.Grid(c).Columns(columns).Gap(6).FillWidth().Children(func() {
		for _, fact := range facts {
			ui.Column(c.Key(fact.label)).FillWidth().MinWidth(0).Gap(2).Padding(5, 7).Radius(5).Background(colors.overviewPage).Children(func() {
				widgets.Muted(c, fact.label).FontSize(10).SingleLine().MinWidth(0).Tooltip(fact.label)
				ui.Text(c, fact.value).FontSize(12).Bold().SingleLine().MinWidth(0).Tooltip(fact.value).FontFeatures("tnum")
			})
		}
	})
}

func taskLoadDetail(c *ui.Context, state *desktopTaskState, width float32) {
	read := state.perf
	palette := taskColors()
	labels := [...]string{locales.DesktopTaskOneMinute(), locales.DesktopTaskFiveMinutes(), locales.DesktopTaskFifteenMinutes()}
	fraction, title := desktopTaskLoadState(read.Load[0], read.Logical)
	if read.Logical > 0 {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ink := palette[2]
			if fraction >= 1 {
				ink = theme.Danger
			} else if fraction >= 0.7 {
				ink = theme.Warning
			}
			ui.Text(c, title).FontSize(13).Bold().TextColor(ink)
			widgets.Muted(c, locales.DesktopTaskLoadPerCore()).FontSize(11).MinWidth(0).Shrink(1)
		})
	}
	chart := make([]taskSeries, 0, len(labels))
	facts := make([]taskFact, 0, 7)
	decimal := func(value float64) string { return fmt.Sprintf("%.2f", value) }
	for index, label := range labels {
		chart = append(chart, taskSeries{label, palette[index], state.history[taskLoadSeries[index]]})
		value := "—"
		if state.perfReady {
			value = decimal(read.Load[index])
		}
		facts = append(facts, taskFact{label, value})
	}
	for _, fact := range []struct {
		label string
		value int
	}{
		{locales.DesktopTaskProcesses(), read.Processes}, {locales.DesktopTaskThreads(), read.Threads},
		{locales.DesktopTaskRunning(), read.Running}, {locales.DesktopTaskBlocked(), read.Blocked},
	} {
		value := "—"
		if state.perfReady {
			value = strconv.Itoa(fact.value)
		}
		facts = append(facts, taskFact{fact.label, value})
	}
	taskChart(c, taskChartHeight(width, 163), 0, decimal, chart...)
	taskFacts(c, width, facts...)
}

func taskCPUDetail(c *ui.Context, state *desktopTaskState, stats *monitor.Stats, width float32) {
	read := state.perf
	palette := taskColors()
	taskChart(c, taskChartHeight(width, 134), 100, taskPercent, taskSeries{locales.DesktopTaskCPUUsage(), palette[1], state.history["cpu"]})
	info := read.Hardware
	if info.Model != "" {
		ui.Text(c, info.Model).FontSize(14).Bold().SingleLine().MinWidth(0).Tooltip(info.Model)
	}
	uptime := "—"
	if stats.OK {
		uptime = uptimeText(stats.Uptime)
	}
	taskFacts(c, width, taskFact{locales.DesktopTaskCurrentSpeed(), taskGHz(read.GHz)}, taskFact{locales.DesktopTaskBaseSpeed(), taskGHz(info.BaseGHz)},
		taskFact{locales.DesktopTaskSockets(), taskCount(info.Sockets)}, taskFact{locales.DesktopTaskCores(), taskCount(info.Cores)},
		taskFact{locales.DesktopTaskThreads(), taskCount(max(read.Logical, info.Threads))}, taskFact{locales.DesktopTaskVirtualization(), taskValue(info.Virtualization)},
		taskFact{locales.DesktopTaskUptime(), uptime}, taskFact{"L1d / L1i", taskValue(info.Cache[0]) + " / " + taskValue(info.Cache[1])},
		taskFact{"L2 / L3", taskValue(info.Cache[2]) + " / " + taskValue(info.Cache[3])})
	if len(read.CPUs) == 0 {
		return
	}
	if read.CPUs[0].Name == "cpu" {
		ui.Text(c, locales.DesktopTaskCPUTime()).Bold()
		var facts []taskFact
		for index, label := range strings.Split(locales.DesktopTaskCPUTimeNames(), "|") {
			if index < 10 {
				facts = append(facts, taskFact{label, fmt.Sprintf("%.2f%%", read.CPUs[0].Time[index])})
			}
		}
		taskFacts(c, width, facts...)
	}
	if len(read.CPUs) <= 1 {
		return
	}
	ui.Text(c, locales.DesktopTaskPerCPU()).Bold()
	columns := 3
	if width > 690 {
		columns = 5
	}
	ui.Grid(c).Columns(columns).Gap(8).FillWidth().Children(func() {
		for _, cpu := range read.CPUs {
			if cpu.Name == "cpu" {
				continue
			}
			ui.Column(c.Key(cpu.Name)).MinWidth(0).Gap(2).Children(func() {
				widgets.Muted(c, "CPU "+strings.TrimPrefix(cpu.Name, "cpu")).FontSize(10).SingleLine()
				taskChart(c, 48, 100, taskPercent, taskSeries{ink: palette[1], values: state.history[cpu.Name]})
			})
		}
	})
}

func (a *App) desktopTaskMemory(c *ui.Context, session *Session, state *desktopTaskState, width float32) {
	stats, palette := &session.stats, taskColors()
	taskChart(c, taskChartHeight(width, 140), 100, taskPercent, taskSeries{locales.DesktopTaskMemory(), palette[2], state.history["mem"]},
		taskSeries{locales.DesktopTaskSwap(), palette[1], state.history["swap"]})
	mem := stats.Memory
	used, swap := "—", "—"
	if stats.MemTotal > 0 {
		used = memorySize(stats.MemUsed) + " / " + memorySize(stats.MemTotal)
	}
	if stats.SwapTotal > 0 {
		swap = memorySize(stats.SwapUsed) + " / " + memorySize(stats.SwapTotal)
	}
	memorySection(c, locales.MemoryPhysical(), mem.PhysicalParts(), [3]string{locales.MemoryApps(), locales.MemoryCache(), locales.MemoryFreeSpace()}, func() {})
	taskFacts(c, width, taskFact{locales.DesktopTaskUsed(), used}, taskFact{locales.DesktopTaskAvailable(), memoryValue(mem, "MemAvailable")},
		taskFact{locales.DesktopTaskCached(), memoryValue(mem, "Cached")}, taskFact{locales.DesktopTaskBuffers(), memoryValue(mem, "Buffers")},
		taskFact{locales.DesktopTaskShared(), memoryValue(mem, "Shmem")}, taskFact{locales.DesktopTaskSlab(), memoryValue(mem, "Slab")},
		taskFact{locales.DesktopTaskSwap(), swap})
	ui.Row(c).Wrap().Gap(7).AlignItems(ui.Center).Children(func() {
		ready := desktopTaskConnected(session, state)
		if ui.Button(c, locales.DesktopTaskRelease()).Disabled(!ready || memoryActionBlock(mem, monitor.MemoryDropCaches) != nil).Clicked() {
			a.desktopTaskAskMemory(state, monitor.MemoryDropCaches)
		}
		if ui.Button(c, locales.MemoryCompact()).Disabled(!ready || memoryActionBlock(mem, monitor.MemoryCompact) != nil).Clicked() {
			a.desktopTaskAskMemory(state, monitor.MemoryCompact)
		}
	})
}

func taskDiskDetail(c *ui.Context, state *desktopTaskState, disk monitor.TaskDisk, width float32) {
	if disk.Name == "" {
		taskNotice(c, locales.DesktopTaskUnavailable(), false)
		return
	}
	palette := taskColors()
	taskChart(c, taskChartHeight(width, 155), 0, taskRate, taskSeries{locales.DesktopTaskRead(), palette[1], state.history["read/"+disk.Name]},
		taskSeries{locales.DesktopTaskWrite(), palette[3], state.history["write/"+disk.Name]})
	read, write, busy, iops := "—", "—", "—", "—"
	if disk.Measured {
		read, write, busy, iops = taskRate(disk.Read), taskRate(disk.Write), taskPercent(disk.Busy), fmt.Sprintf("%.1f", disk.IOPS)
	}
	taskFacts(c, width, taskFact{locales.DesktopTaskRead(), read}, taskFact{locales.DesktopTaskWrite(), write},
		taskFact{locales.DesktopTaskDiskBusy(), busy}, taskFact{"IOPS", iops}, taskFact{locales.DesktopTaskCapacity(), taskSize(disk.Capacity)},
		taskFact{locales.DesktopTaskMounts(), taskValue(strings.Join(disk.Mounts, ", "))})
}

func taskNetworkDetail(c *ui.Context, state *desktopTaskState, iface monitor.Iface, width float32) {
	if iface.Name == "" {
		taskNotice(c, locales.DesktopTaskUnavailable(), false)
		return
	}
	palette := taskColors()
	taskChart(c, taskChartHeight(width, 155), 0, taskRate, taskSeries{locales.DesktopTaskReceive(), palette[4], state.history["rx/"+iface.Name]}, taskSeries{locales.DesktopTaskSend(), palette[3], state.history["tx/"+iface.Name]})
	recv, sent := monitor.TaskIfaceTotals(iface)
	link := "—"
	if speed := state.perf.Hardware.LinkMbps[iface.Name]; speed > 0 {
		link = fmt.Sprintf("%d Mbit/s", speed)
	}
	taskFacts(c, width, taskFact{locales.DesktopTaskReceive(), taskRate(iface.RxRate)}, taskFact{locales.DesktopTaskSend(), taskRate(iface.TxRate)},
		taskFact{locales.DesktopTaskReceived(), memorySize(recv)}, taskFact{locales.DesktopTaskSent(), memorySize(sent)}, taskFact{locales.DesktopTaskLink(), link})
	widgets.Muted(c, locales.DesktopTaskAddresses()).FontSize(11)
	ui.Text(c, taskValue(strings.Join(state.perf.Hardware.Addresses[iface.Name], "   "))).FontSize(12).Selectable()
}

func taskChartHeight(width, standard float32) float32 {
	if width > 0 && width < 540 {
		return 91
	}
	return standard
}
