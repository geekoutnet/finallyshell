package app

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

const ctrStatsInterval = 2 * time.Second
const ctrStatsHistory = 30

type ctrStats struct {
	ids    []string
	next   time.Time
	cancel context.CancelFunc
	series map[string]*ctrStatSeries
	err    string
}

type ctrStatSeries struct {
	last                monitor.ContainerStats
	at                  time.Time
	cpu, memory, rx, tx []float64
}

func ctrStatsReason(target dockerTarget, ids []string) string {
	if !target.available() || len(ids) == 0 {
		return locales.DockerUnavailable()
	}
	for _, id := range ids {
		if ctrExecuting(dockerContainer(target.session, id)) && monitor.ValidName(id) {
			return ""
		}
	}
	return locales.DockerCtrMustRun()
}

func (a *App) openCtrStats(target dockerTarget, ids []string) {
	a.setCtrModal(&ctrModal{target: target, stats: &ctrStats{ids: slices.Clone(ids), series: map[string]*ctrStatSeries{}}})
}

func (a *App) ctrReadStats(modal *ctrModal, now time.Time) {
	stats, target := modal.stats, modal.target
	var ids []string
	var containers []monitor.Container
	for _, id := range stats.ids {
		if ct := dockerContainer(target.session, id); ctrExecuting(ct) {
			ids = append(ids, id)
			containers = append(containers, monitor.Container{ID: ct.ID, Name: ct.Name})
		} else {
			delete(stats.series, id)
		}
	}
	stats.next = now.Add(ctrStatsInterval)
	if len(ids) == 0 {
		stats.err = locales.DockerCtrMustRun()
		return
	}
	script, err := monitor.CmdOf(&target.session.overview.docker.d).CtrStatsScript(ids...)
	if err != nil {
		stats.err = ctrErrorText(err)
		return
	}
	run, ctx, cancel := ctrRunner(target, 10*time.Second)
	stats.cancel = cancel
	go func() {
		defer cancel()
		output, err := run(ctx, target.client, script, nil)
		samples := monitor.ParseCtrStats(containers, output)
		at := time.Now()
		a.post(func() {
			if a.ctrDlg == nil || a.ctrDlg.ctr != modal || !target.available() {
				return
			}
			stats.cancel, stats.err = nil, ""
			if err != nil {
				stats.err = ctrReadError(err, output)
				return
			}
			for _, id := range ids {
				sample, ok := samples[id]
				if !ok {
					delete(stats.series, id)
					continue
				}
				series := stats.series[id]
				if series == nil {
					series = &ctrStatSeries{}
					stats.series[id] = series
				}
				series.append(sample, at)
			}
		})
	}()
}

func (series *ctrStatSeries) append(sample monitor.ContainerStats, at time.Time) {
	add := func(values []float64, value float64) []float64 {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			value = 0
		}
		if len(values) == ctrStatsHistory {
			copy(values, values[1:])
			values = values[:len(values)-1]
		}
		return append(values, value)
	}
	var rx, tx float64
	if seconds := at.Sub(series.at).Seconds(); !series.at.IsZero() && seconds > 0 {
		if sample.NetRx >= series.last.NetRx {
			rx = float64(sample.NetRx-series.last.NetRx) / seconds
		}
		if sample.NetTx >= series.last.NetTx {
			tx = float64(sample.NetTx-series.last.NetTx) / seconds
		}
	}
	series.cpu, series.memory = add(series.cpu, sample.CPU), add(series.memory, float64(sample.MemUsed))
	series.rx, series.tx = add(series.rx, rx), add(series.tx, tx)
	series.last, series.at = sample, at
}

func (a *App) ctrStatsView(c *ui.Context, modal *ctrModal, height float32, open *bool) {
	stats := modal.stats
	now := c.Now()
	if stats.cancel == nil && !now.Before(stats.next) {
		a.ctrReadStats(modal, now)
	}
	if stats.cancel == nil {
		c.After(stats.next.Sub(now))
	}
	ui.Text(c, locales.DockerCtrStats()).FontSize(16).Bold()
	if stats.err != "" {
		ui.Text(c, stats.err).TextColor(theme.Danger).MaxLines(3).Selectable()
	}
	ui.Scroll(c).MaxHeight(max(height-180, 140)).FillWidth().Children(func() {
		ui.Column(c).Gap(10).FillWidth().Padding(0, 8, 0, 0).Children(func() {
			for _, id := range stats.ids {
				ct := dockerContainer(modal.target.session, id)
				if ct == nil {
					continue
				}
				ctrStatsCard(c.Key(id), ct, stats.series[id], stats.cancel != nil)
			}
		})
	})
	ctrCloseButton(c, open)
}

func ctrStatsCard(c *ui.Context, ct *monitor.Container, series *ctrStatSeries, busy bool) {
	ui.Column(c).Gap(8).Padding(10).Border(1, colors.line).Radius(6).FillWidth().Children(func() {
		ui.Text(c, ct.Name).Bold().SingleLine().Tooltip(ct.Name)
		if series == nil || !ctrExecuting(ct) {
			label := locales.DockerCtrNoStats()
			if !ctrExecuting(ct) {
				label = locales.DockerCtrMustRun()
			} else if busy {
				label = locales.DockerReading()
			}
			widgets.Muted(c, label)
			return
		}
		last := series.last
		percent := func(value float64) string { return fmt.Sprintf("%.1f%%", value) }
		rate := func(value float64) string { return formatBytes(value) + "/s" }
		ui.Row(c).Gap(12).Wrap().FillWidth().Children(func() {
			ctrStatsGraph(c, locales.Cpu(), percent(last.CPU), [][]float64{series.cpu}, []ui.Color{theme.Accent}, percent)
			ctrStatsGraph(c, locales.DockerMemUsage(), formatBytes(float64(last.MemUsed))+" / "+formatBytes(float64(last.MemLimit)), [][]float64{series.memory}, []ui.Color{theme.Success}, formatBytes)
			ctrStatsGraph(c, locales.DockerNetIO(), "↓ "+rate(series.rx[len(series.rx)-1])+"  ↑ "+rate(series.tx[len(series.tx)-1]), [][]float64{series.rx, series.tx}, []ui.Color{rxChart, txChart}, rate)
		})
		widgets.Muted(c, locales.DockerBlockIO()+": "+locales.DockerBlockIOValue(formatBytes(float64(last.BlockRead)), formatBytes(float64(last.BlockWrite)))+
			"  ·  "+locales.DockerCtrPIDs(last.PIDs)).FontSize(12)
	})
}

func ctrStatsGraph(c *ui.Context, label, value string, data [][]float64, palette []ui.Color, format func(float64) string) {
	ui.Column(c.Key(label)).Gap(5).Grow(1).Basis(200).MinWidth(135).Children(func() {
		widgets.Muted(c, label).FontSize(12)
		ui.Text(c, value).SingleLine().Tooltip(value).FontFeatures("tnum")
		peak := 0.0
		for _, values := range data {
			for _, value := range values {
				peak = max(peak, value)
			}
		}
		ui.Box(c).Height(50).FillWidth().Background(theme.Background).Border(1, colors.line).Draw(func(painter *ui.Painter, rect ui.Rect) {
			plot := ui.Rect{X: rect.X + 5, Y: rect.Y + 16, W: rect.W - 10, H: rect.H - 20}
			for index, values := range data {
				var xs, ys [ctrStatsHistory]float32
				for point, value := range values {
					xs[point] = float32(ctrStatsHistory-len(values)+point) / (ctrStatsHistory - 1)
					if peak != 0 {
						ys[point] = float32(value / peak)
					}
				}
				painter.StrokePath(chart.Line(plot, xs[:len(values)], ys[:len(values)], chart.CurveLinear), 1.5, palette[index])
			}
		}).Children(func() {
			widgets.Muted(c, format(peak)).FontSize(10).Padding(1, 5).SingleLine()
		})
	})
}

func ctrCloseButton(c *ui.Context, open *bool) {
	ui.Row(c).Justify(ui.End).Children(func() {
		if ui.PrimaryButton(c, locales.Close()).Clicked() {
			*open = false
		}
	})
}
