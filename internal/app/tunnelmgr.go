package app

import (
	"cmp"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const windowTunnelManager WindowKind = "tunnel-manager"

type tunnelViewState int

const (
	tunnelViewAll tunnelViewState = iota
	tunnelViewStopped
	tunnelViewConnecting
	tunnelViewListening
	tunnelViewRetry
	tunnelViewError
)

type tunnelAction int

const (
	tunnelActionStart tunnelAction = iota
	tunnelActionStop
	tunnelActionRestart
	tunnelActionStartBackground
	tunnelActionEnableBackground
	tunnelActionDisableBackground
)

type tunnelManagerView struct {
	query, hostID string
	kind          config.TunnelKind
	state         tunnelViewState
	selection     ui.Selection[tunnelKey]
	table         ui.ListState
	ask           *tunnelManagerQuestion
	log           *tunnelManagerRow
	chooseHost    bool
	autostart     *bool
	err           error
	sessionRates  map[tunnelKey]tunnelSessionTraffic
}

type tunnelManagerQuestion struct {
	action tunnelAction
	rows   []tunnelManagerRow
	routes map[string][]*config.Host
}

type tunnelSessionRun struct {
	session *Session
	run     *tunnelRun
}

type tunnelManagerRow struct {
	key        tunnelKey
	host       string
	hostAddr   string
	spec       config.Tunnel
	address    string
	state      tunnelViewState
	background bool
	err        error
	lastErr    error
	attempt    int
	retryAt    time.Time
	since      time.Time
	stats      sshx.TunnelStats
	traffic    []float64
	sessions   []tunnelSessionRun
}

const tunnelTrafficSamples = 24

type tunnelTraffic struct {
	at      time.Time
	bytes   int64
	samples []float64
}

type tunnelSessionTraffic struct {
	tunnelTraffic
	since   time.Time
	handles []*sshx.Tunnel
}

func (traffic *tunnelTraffic) sample(now time.Time, stats sshx.TunnelStats) {
	bytes := stats.Up + stats.Down
	elapsed := now.Sub(traffic.at)
	if !traffic.at.IsZero() {
		if elapsed < time.Second {
			return
		}
		if len(traffic.samples) == tunnelTrafficSamples {
			copy(traffic.samples, traffic.samples[1:])
			traffic.samples = traffic.samples[:tunnelTrafficSamples-1]
		}
		traffic.samples = append(traffic.samples, float64(max(0, bytes-traffic.bytes))/elapsed.Seconds())
	}
	traffic.bytes, traffic.at = bytes, now
}

func (a *App) OpenTunnelManager() { a.openTunnelManager("") }

func (a *App) openTunnelManager(hostID string) {
	if a.Locked() {
		return
	}
	view := &a.tmgr.view
	a.ensureTunnelManagerIDs()
	if view.hostID != hostID {
		view.selection.Clear()
	}
	view.hostID = hostID
	if hostID != "" {
		view.query, view.kind, view.state = "", "", tunnelViewAll
	}
	a.openWindow(WindowSpec{Kind: windowTunnelManager, Title: locales.TmgrTitle,
		Width: 1280, Height: 700, MinWidth: 680, MinHeight: 470,
		Closed: func() {
			view.ask, view.log, view.chooseHost, view.autostart = nil, nil, false, nil
		}}, a.tunnelManagerWindow)
	a.scheduleTunnelManagerTick()
	a.redraw()
}

func (a *App) tunnelManagerTabLabel(label string) string {
	listening, attention := 0, false
	for key, run := range a.tmgr.runs {
		state := a.tmgr.hosts[key.host]
		if run.handle != nil {
			listening++
		}
		attention = attention || run.err != nil || state != nil && state.err != nil
	}
	if attention {
		return locales.TmgrTabAttention(label)
	}
	if listening > 0 {
		return locales.TmgrTabLive(label)
	}
	return label
}

func (a *App) tunnelManagerCount() int {
	count := 0
	for key, run := range a.tmgr.runs {
		state := a.tmgr.hosts[key.host]
		if run.handle != nil || run.starting || !run.retryAt.IsZero() || !run.blocked && state != nil && (state.connecting || !state.retryAt.IsZero()) {
			count++
		}
	}
	return count
}

func (a *App) ensureTunnelManagerIDs() {
	for _, host := range a.hosts.Live() {
		if tunnelManagerHost(host) {
			host.EnsureTunnelIDs()
		}
	}
}

func (a *App) sessionTunnelSpecs(hostID string, specs []config.Tunnel) []config.Tunnel {
	specs = slices.Clone(specs)
	if host := a.hosts.Host(hostID); host != nil {
		host.EnsureTunnelIDs()
		for index := range specs {
			if specs[index].ID == "" && index < len(host.Tunnels) && sameTunnelDefinition(specs[index], host.Tunnels[index]) {
				specs[index].ID = host.Tunnels[index].ID
			}
		}
	}
	return slices.DeleteFunc(specs, func(spec config.Tunnel) bool {
		return spec.Background || a.tmgr.runs[tunnelKey{hostID, spec.ID}] != nil
	})
}

func sameTunnelDefinition(left, right config.Tunnel) bool {
	left.ID, left.Background = right.ID, right.Background
	return sameBackgroundTunnel(left, right)
}

func (a *App) managerSessionRuns(key tunnelKey, spec config.Tunnel) []tunnelSessionRun {
	var runs []tunnelSessionRun
	for _, session := range a.sessions {
		if session.isPanelTab() || session.Host == nil || session.Host.ID != key.host || session.client == nil || session.status != statusConnected {
			continue
		}
		for _, run := range session.tunnels.runs {
			if run.spec.ID == key.id || run.spec.ID == "" && sameTunnelDefinition(run.spec, spec) {
				runs = append(runs, tunnelSessionRun{session, run})
				break
			}
		}
	}
	return runs
}

func tunnelManagerKind(kind config.TunnelKind) string {
	switch kind {
	case config.TunnelHTTP:
		return locales.TmgrHTTP()
	case config.TunnelRemoteDynamic:
		return locales.TmgrRemoteSOCKS()
	case config.TunnelRemoteHTTP:
		return locales.TmgrRemoteHTTP()
	default:
		return tunnelKindLabel(kind)
	}
}

func (row tunnelManagerRow) name() string {
	return cmp.Or(row.spec.Name, tunnelManagerKind(row.spec.Kind))
}

func (row tunnelManagerRow) bind() string {
	if row.address != "" {
		return row.address
	}
	if row.spec.ListenPort == 0 {
		return locales.TmgrAutoPort(row.spec.ListenHostOrLoopback())
	}
	return row.spec.ListenAddr()
}

func (row tunnelManagerRow) target() string {
	spec := row.spec
	switch {
	case spec.TargetSocket != "":
		return spec.TargetSocket
	case !spec.Kind.IsProxy():
		return spec.TargetAddr()
	case !spec.Kind.IsRemote():
		return locales.TmgrServerExit()
	case spec.Upstream == "":
		return locales.TmgrLocalExit()
	default:
		if upstream, err := url.Parse(spec.Upstream); err == nil && upstream.User == nil && upstream.Host != "" && (upstream.Scheme == "http" || upstream.Scheme == "socks5") {
			return locales.TmgrUpstream(upstream.Scheme + "://" + upstream.Host)
		}
		return locales.TmgrLocalExit()
	}
}

func (row tunnelManagerRow) errorText() string {
	switch {
	case row.err == nil:
		return ""
	case errors.Is(row.err, sshx.ErrBulkHostKey):
		return locales.TmgrUntrusted()
	case errors.Is(row.err, errTunnelManagerLogin):
		return locales.TmgrLoginError()
	case errors.Is(row.err, errTunnelManagerEngine):
		return locales.TmgrEngineError()
	default:
		return describeTunnelError(row.err)
	}
}

func (a *App) tunnelManagerRows() []tunnelManagerRow {
	var rows []tunnelManagerRow
	hosts := a.hosts.Live()
	slices.SortStableFunc(hosts, func(left, right *config.Host) int {
		return cmp.Or(cmp.Compare(strings.ToLower(left.Title()), strings.ToLower(right.Title())), cmp.Compare(left.ID, right.ID))
	})
	for _, host := range hosts {
		if !tunnelManagerHost(host) {
			continue
		}
		for _, spec := range host.Tunnels {
			if spec.ID == "" {
				continue
			}
			rows = append(rows, a.tunnelManagerRow(host, spec))
		}
	}
	return rows
}

func (a *App) tunnelManagerRow(host *config.Host, spec config.Tunnel) tunnelManagerRow {
	row := tunnelManagerRow{key: tunnelKey{host.ID, spec.ID}, host: host.Title(), hostAddr: host.Addr, spec: spec,
		state: tunnelViewStopped, background: spec.Background}
	row.sessions = a.managerSessionRuns(row.key, spec)
	if run := a.tmgr.runs[row.key]; run != nil {
		row.background, row.stats, row.since, row.traffic = true, run.stats(), run.since, run.traffic.samples
		row.err, row.lastErr, row.retryAt, row.attempt = run.err, run.lastErr, run.retryAt, run.attempt
		switch state := a.tmgr.hosts[host.ID]; {
		case run.handle != nil:
			row.state, row.address = tunnelViewListening, run.handle.Addr().String()
		case run.starting:
			row.state = tunnelViewConnecting
		case run.blocked:
			row.state = tunnelViewError
		case state != nil && state.connecting:
			row.state, row.err, row.attempt = tunnelViewConnecting, state.err, state.attempt
			row.retryAt = time.Time{}
		case state != nil && state.client == nil:
			row.err, row.retryAt, row.attempt = state.err, state.retryAt, state.attempt
			if row.retryAt.Before(run.retryAt) && !state.retryAt.IsZero() {
				row.err, row.retryAt, row.attempt = run.err, run.retryAt, run.attempt
			}
			row.state = tunnelRetryView(row.retryAt)
		default:
			row.state = tunnelRetryView(row.retryAt)
		}
		return row
	}
	for _, sessionRun := range row.sessions {
		run := sessionRun.run
		switch {
		case run.fwd != nil:
			if row.address == "" {
				row.address = run.fwd.Addr().String()
			}
			row.state, row.err, row.background = tunnelViewListening, nil, false
			if err := run.fwd.DialErr(); err != nil {
				opts, _ := a.dialOptions(host, a.passwords[host.ID], nil)
				row.lastErr = tunnelForwardError(err, bulkSecrets(opts))
			}
			stats := run.fwd.Stats()
			row.stats.Active += stats.Active
			row.stats.Total += stats.Total
			row.stats.Up += stats.Up
			row.stats.Down += stats.Down
		case run.starting && row.state != tunnelViewListening:
			row.state, row.background = tunnelViewConnecting, false
		case run.err != nil && row.state == tunnelViewStopped:
			opts, _ := a.dialOptions(host, a.passwords[host.ID], nil)
			row.state, row.err, row.background = tunnelViewError, tunnelForwardError(run.err, bulkSecrets(opts)), false
		}
	}
	if rate, ok := a.tmgr.view.sessionRates[row.key]; ok && row.state == tunnelViewListening {
		row.traffic, row.since = rate.samples, rate.since
	}
	return row
}

func tunnelRetryView(when time.Time) tunnelViewState {
	if !when.IsZero() {
		return tunnelViewRetry
	}
	return tunnelViewError
}

func (view *tunnelManagerView) filtered(rows []tunnelManagerRow) []tunnelManagerRow {
	query := strings.ToLower(strings.TrimSpace(view.query))
	return slices.DeleteFunc(slices.Clone(rows), func(row tunnelManagerRow) bool {
		return view.hostID != "" && row.key.host != view.hostID || view.kind != "" && row.spec.Kind != view.kind ||
			view.state != tunnelViewAll && row.state != view.state || !strings.Contains(strings.ToLower(strings.Join([]string{row.name(), row.host, row.hostAddr, tunnelManagerKind(row.spec.Kind)}, " ")), query)
	})
}

func (action tunnelAction) label() string {
	switch action {
	case tunnelActionStart:
		return locales.TunnelStart()
	case tunnelActionStop:
		return locales.TunnelStop()
	case tunnelActionRestart:
		return locales.TmgrRestart()
	case tunnelActionStartBackground:
		return locales.TmgrStartWithoutTerminal()
	case tunnelActionEnableBackground:
		return locales.TmgrEnableBackground()
	default:
		return locales.TmgrDisableBackground()
	}
}

func (row tunnelManagerRow) available(action tunnelAction) string {
	active := row.state == tunnelViewListening || row.state == tunnelViewConnecting || row.state == tunnelViewRetry
	switch action {
	case tunnelActionStart, tunnelActionStartBackground:
		if active {
			return locales.TmgrAlreadyActive()
		}
	case tunnelActionStop, tunnelActionRestart:
		if row.state == tunnelViewStopped {
			return locales.TmgrNotRunning()
		}
	}
	return ""
}

func (a *App) askTunnelManager(action tunnelAction, rows []tunnelManagerRow) {
	rows = slices.DeleteFunc(slices.Clone(rows), func(row tunnelManagerRow) bool { return row.available(action) != "" })
	if len(rows) > 0 && !a.Locked() {
		routes := make(map[string][]*config.Host)
		for _, row := range rows {
			if routes[row.key.host] == nil {
				if host := a.hosts.Host(row.key.host); tunnelManagerHost(host) {
					routes[row.key.host] = a.tunnelRoute(host)
				}
			}
		}
		a.tmgr.view.ask = &tunnelManagerQuestion{action, rows, routes}
	}
}

func (a *App) applyTunnelManager(question *tunnelManagerQuestion) {
	if a.Locked() {
		return
	}
	view := &a.tmgr.view
	view.err = nil
	for _, old := range question.rows {
		host, spec := a.tunnelSpec(old.key)
		if spec == nil || !sameBackgroundTunnel(old.spec, *spec) || !a.tunnelRouteMatches(old.key.host, question.routes[old.key.host]) {
			view.err = errBulkTargetChanged
			continue
		}
		row := a.tunnelManagerRow(host, *spec)
		if row.available(question.action) != "" {
			continue
		}
		switch question.action {
		case tunnelActionStart, tunnelActionStartBackground:
			a.startManagerRow(row, question.action == tunnelActionStartBackground)
		case tunnelActionStop:
			a.stopManagerRow(row)
		case tunnelActionRestart:
			a.restartManagerRow(row)
		case tunnelActionEnableBackground, tunnelActionDisableBackground:
			a.setTunnelBackground(host, spec, question.action == tunnelActionEnableBackground)
		}
	}
	a.redraw()
}

func (a *App) startManagerRow(row tunnelManagerRow, background bool) {
	if state := a.tmgr.hosts[row.key.host]; (row.background || background) && state != nil && !state.connecting && state.client == nil && state.retryAt.IsZero() {
		a.retryBackgroundHost(row.key.host, state)
		if a.tmgr.runs[row.key] != nil {
			return
		}
	}
	if background || row.spec.Background || len(row.sessions) == 0 || a.tmgr.runs[row.key] != nil || !sameTunnelDefinition(row.sessions[0].run.spec, row.spec) || !tunnelManagerEngineSupports(row.spec) {
		a.stopBackgroundTunnel(row.key)
		a.startBackgroundTunnel(row.key)
		return
	}
	value := row.sessions[0]
	a.startTunnel(value.session, value.run)
}

func (a *App) stopManagerRow(row tunnelManagerRow) {
	a.stopBackgroundTunnel(row.key)
	for _, value := range row.sessions {
		value.run.stop()
	}
}

func (a *App) restartManagerRow(row tunnelManagerRow) {
	if row.background || len(row.sessions) == 0 {
		if state := a.tmgr.hosts[row.key.host]; state != nil && state.client == nil && !state.connecting {
			a.retryBackgroundHost(row.key.host, state)
			if run := a.tmgr.runs[row.key]; run != nil && !run.blocked {
				return
			}
		}
		for _, value := range row.sessions {
			value.run.stop()
		}
		a.restartBackgroundTunnel(row.key)
		return
	}
	value := row.sessions[0]
	client, handle := value.session.client, value.run.fwd
	a.stopManagerRow(row)
	if handle == nil {
		a.startManagerRow(row, false)
		return
	}
	value.run.starting = true
	generation := value.run.gen
	go func() {
		handle.Wait()
		a.post(func() {
			if value.run.gen != generation || value.session.client != client {
				return
			}
			value.run.starting = false
			_, spec := a.tunnelSpec(row.key)
			if spec != nil && sameBackgroundTunnel(*spec, row.spec) {
				a.startManagerRow(row, false)
			}
		})
	}()
}

func (a *App) setTunnelBackground(host *config.Host, spec *config.Tunnel, enabled bool) {
	if spec.Background == enabled {
		return
	}
	if a.tunnelEditorOpen(host.ID) {
		a.tmgr.view.err = errBulkTargetChanged
		return
	}
	spec.Background = enabled
	if err := a.saveHosts(a.hosts); err != nil {
		spec.Background = !enabled
		a.tmgr.view.err = err
		return
	}
	if run := a.tmgr.runs[tunnelKey{host.ID, spec.ID}]; run != nil {
		run.spec.Background = enabled
	}
}

func (a *App) tunnelEditorOpen(hostID string) bool {
	return a.windowOpen(WindowEditor) && a.editor != nil && a.editor.id == hostID
}

func (a *App) setTunnelAutostart(enabled bool) {
	if a.Locked() {
		return
	}
	settings := a.settings
	settings.TunnelManager = nil
	if !enabled {
		settings.TunnelManager = &config.TunnelManagerSettings{NoAutostart: true}
	}
	if err := a.saveSettings(settings); err != nil {
		a.tmgr.view.err = err
		return
	}
	a.tmgr.view.err = nil
	a.settings = settings
}

func (a *App) editTunnelFromManager(hostID, tunnelID string) {
	host := a.hosts.Host(hostID)
	if !tunnelManagerHost(host) {
		return
	}
	opening := !a.windowOpen(WindowEditor)
	a.openEditor(host, windowTunnelManager)
	if !opening || a.editor == nil {
		return
	}
	a.editor.nav.Page = editorTunnels
	editor := &a.editor.tunnels
	index := -1
	if tunnelID != "" {
		index = slices.IndexFunc(editor.list, func(spec config.Tunnel) bool { return spec.ID == tunnelID })
		if index < 0 {
			return
		}
	}
	editor.row = index
	editor.open(index)
}

func (a *App) sampleManagerSessions(now time.Time) {
	if !a.hasSessionTunnels() {
		a.tmgr.view.sessionRates = nil
		return
	}
	previous := a.tmgr.view.sessionRates
	type sessionSample struct {
		stats   sshx.TunnelStats
		handles []*sshx.Tunnel
	}
	samples := make(map[tunnelKey]sessionSample)
	for _, session := range a.sessions {
		if session.Host == nil || session.client == nil || session.status != statusConnected {
			continue
		}
		for _, run := range session.tunnels.runs {
			if run.fwd == nil || run.spec.ID == "" {
				continue
			}
			key := tunnelKey{session.Host.ID, run.spec.ID}
			sample := samples[key]
			stats := run.fwd.Stats()
			sample.stats.Up += stats.Up
			sample.stats.Down += stats.Down
			sample.handles = append(sample.handles, run.fwd)
			samples[key] = sample
		}
	}
	rates := make(map[tunnelKey]tunnelSessionTraffic, len(samples))
	for key, sample := range samples {
		rate := previous[key]
		if !slices.Equal(rate.handles, sample.handles) {
			rate = tunnelSessionTraffic{since: now, handles: sample.handles}
		}
		rate.sample(now, sample.stats)
		rates[key] = rate
	}
	a.tmgr.view.sessionRates = rates
}
