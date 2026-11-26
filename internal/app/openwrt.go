package app

import (
	"cmp"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// On OpenWrt and its derivatives (ImmortalWrt, iStoreOS, …) the System
// tab shows the sections a router needs instead of the systemd ones,
// which do not apply there: the status, the network interfaces, the DHCP
// leases, the port forwards, procd's services, the packages (opkg or
// apk) and the logs (logread), then the scheduled jobs and the disk as on
// any server. The OpenWrt app of the desktop shows the same. What they
// read and run is in internal/monitor/openwrt*.go; changes go through the
// escalation layer and ask first, as the other sections' do.

// The sections of the OpenWrt System tab, in the order of its row.
const (
	owSecStatus = iota
	owSecInterfaces
	owSecDHCP
	owSecForwards
	owSecServices
	owSecPackages
	owSecLogs
	owSecCron
	owSecDisk
)

// openWrtPage is what a session keeps of its OpenWrt sections.
type openWrtPage struct {
	// The probe: whether the server is OpenWrt, read once a connection.
	client   *ssh.Client
	probing  bool
	checked  bool
	is       bool
	info     monitor.OpenWrtInfo
	probeErr string
	poll     sessionPoll // the status, read again while it shows

	section  int
	ifaces   owIfacesPage   // openwrt_net.go
	dhcp     owDHCPPage     // openwrt_net.go
	forwards owForwardsPage // openwrt_net.go
	services owServicesPage // openwrt_sys.go
	packages owPackagesPage // openwrt_sys.go
	logs     owLogsPage     // openwrt_sys.go
	dialog   *systemDialog
	backup   string // where the last backup was saved
}

// owSectionNames are the labels of the sections' row, by section.
func owSectionNames() []string {
	return []string{
		owSecStatus:     locales.OwStatus(),
		owSecInterfaces: locales.OwInterfaces(),
		owSecDHCP:       locales.OwDHCP(),
		owSecForwards:   locales.OwForwards(),
		owSecServices:   locales.ServerServices(),
		owSecPackages:   locales.OwPackages(),
		owSecLogs:       locales.ServerLogs(),
		owSecCron:       locales.ServerCron(),
		owSecDisk:       locales.ServerDisk(),
	}
}

// owCanRead tells whether s can run the OpenWrt scripts: a connection,
// or the fake runner of the tests.
func owCanRead(s *Session) bool {
	return s.server.services.run != nil || s.client != nil && s.status == statusConnected && execAllowed(s.Host)
}

// openWrtDetect starts the probe of s's server once a connection: the
// System tab, the Files tab and the desktop show more on OpenWrt.
func (a *App) openWrtDetect(s *Session) {
	if s.isPanelTab() {
		s = s.panelOf
	}
	page := &s.server.openwrt
	if page.client != s.client {
		*page = openWrtPage{client: s.client, section: page.section}
	}
	if page.checked || page.probing || !owCanRead(s) {
		return
	}
	page.probing = true
	read, client := systemReader(s), s.client
	go func() {
		ctx, cancel := context.WithTimeout(systemContext(s), 20*time.Second)
		defer cancel()
		out, _, err := read(ctx, monitor.OpenWrtProbeScript, nil, nil)
		info, perr := monitor.ParseOpenWrtProbe(out)
		a.post(func() {
			if page.client != client {
				return
			}
			page.probing, page.checked = false, true
			page.is, page.info = perr == nil, info
			if err != nil && perr != nil && !errors.Is(perr, monitor.ErrNotOpenWrt) {
				page.probeErr = systemError("", err)
			}
		})
	}()
}

// isOpenWrt tells whether s's server is OpenWrt, as far as the probe
// knows yet.
func (s *Session) isOpenWrt() bool {
	if s == nil {
		return false
	}
	if s.isPanelTab() {
		s = s.panelOf
	}
	return s.server.openwrt.is && s.server.openwrt.client == s.client
}

// openWrtPageOf is the System tab of s on OpenWrt: the row of sections
// over the one chosen.
func (a *App) openWrtPageOf(c *ui.Context, s *Session) {
	page := &s.server.openwrt
	ui.Row(c).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.Icon(c, icons.Get("router")).FontSize(14).TextColor(theme.Accent).Shrink(0)
		ui.Text(c, s.label()).Bold().SingleLine().Shrink(1).MinWidth(40).MaxWidth(200)
		names := owSectionNames()
		if a.opsNarrow() {
			systemSelect(c, &page.section, locales.TabSystem(), names)
		} else {
			ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
				ui.Segmented(c, &page.section, names...).Label(locales.TabSystem())
			})
		}
		if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Shrink(0).Clicked() {
			a.refreshOpenWrtSection(s)
		}
	})
	ui.Column(c.Key(page.section)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch page.section {
		case owSecInterfaces:
			a.owInterfacesSection(c, s)
		case owSecDHCP:
			a.owDHCPSection(c, s)
		case owSecForwards:
			a.owForwardsSection(c, s)
		case owSecServices:
			a.owServicesSection(c, s)
		case owSecPackages:
			a.owPackagesSection(c, s)
		case owSecLogs:
			a.owLogsSection(c, s)
		case owSecCron:
			a.serverCronSection(c, s)
		case owSecDisk:
			a.serverDiskSection(c, s)
		default:
			a.owStatusSection(c, s)
		}
	})
	a.systemDialogView(c.Key("openwrt-dialog"), s, &page.dialog)
}

// refreshOpenWrtSection reads again what the section shown reads.
func (a *App) refreshOpenWrtSection(s *Session) {
	page := &s.server.openwrt
	switch page.section {
	case owSecInterfaces:
		page.ifaces.poll.pollNow()
	case owSecDHCP:
		page.dhcp.poll.pollNow()
	case owSecForwards:
		page.forwards.poll.pollNow()
	case owSecServices:
		page.services.poll.pollNow()
	case owSecPackages:
		page.packages.poll.pollNow()
	case owSecLogs:
		page.logs.poll.pollNow()
	case owSecCron:
		a.refreshServerCron(s)
	case owSecDisk:
		a.refreshServerDisk(s)
	default:
		page.poll.pollNow()
	}
}

// owPoll reads script over s every interval while the section shows, and
// hands the output to apply on the UI thread; a failure's stderr is its
// error.
func (a *App) owPoll(p *sessionPoll, s *Session, every time.Duration, script func() string, apply func(out string, err error)) {
	if !owCanRead(s) {
		return
	}
	read := systemReader(s)
	a.keepPolling(p, s, every, func() pollRead {
		text := script()
		return func(ctx context.Context, _ *ssh.Client) func() {
			ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			out, stderr, err := read(ctx, text, nil, nil)
			if err != nil {
				err = errors.New(systemError(stderr, err))
			}
			return func() { apply(out, err) }
		}
	})
}

// owRun asks before it runs script as root on s, showing its output, and
// reads the section again after.
func (a *App) owRun(s *Session, title, target, consequence, script string, after func(*Session)) {
	page := &s.server.openwrt
	dialog := &systemDialog{client: s.client, title: title, target: target, consequence: consequence}
	dialog.confirm = func() { a.systemWork(s, dialog, a.systemRoot(s), script, nil, after) }
	page.dialog = dialog
}

// owForm asks for what body edits, then runs the script make builds of
// it as root; an invalid form stays open, saying so.
func (a *App) owForm(s *Session, title string, body func(c *ui.Context), make func() (string, error), after func(*Session)) {
	page := &s.server.openwrt
	dialog := &systemDialog{client: s.client, title: title}
	dialog.body = func(c *ui.Context, _ float32) {
		ui.Column(c).FillWidth().Gap(8).Children(func() { body(c) })
		if dialog.failure != "" {
			ui.Text(c, dialog.failure).TextColor(theme.Danger)
		}
	}
	var confirm func()
	confirm = func() {
		script, err := make()
		if err != nil {
			dialog.failure, dialog.confirm = locales.OwInvalid(), confirm
			return
		}
		dialog.failure = ""
		a.systemWork(s, dialog, a.systemRoot(s), script, nil, after)
	}
	dialog.confirm = confirm
	page.dialog = dialog
}

// owField is a labeled text input of a form.
func owField(c *ui.Context, label string, value *string, hint string) {
	ui.Column(c.Key(label)).Gap(3).FillWidth().Children(func() {
		widgets.Muted(c, label).FontSize(12)
		ui.TextInput(c, value).Label(label).Placeholder(hint).FillWidth()
	})
}

// owStatusSection is the system's identity and use, and what to do with
// it as a whole: back up its configuration, reboot it.
func (a *App) owStatusSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt
	a.owPoll(&page.poll, s, 15*time.Second, func() string { return monitor.OpenWrtProbeScript }, func(out string, err error) {
		if info, perr := monitor.ParseOpenWrtProbe(out); perr == nil {
			page.info = info
		}
	})
	info := page.info
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(12).Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("router")).FontSize(28).TextColor(theme.Accent)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, strings.TrimSpace(info.Distribution+" "+info.Version)).FontSize(18).Bold()
					widgets.Muted(c, orDash(strings.TrimSpace(info.Model+"  "+info.Target+"  "+info.Revision))).SingleLine()
				})
				if ui.Button(c, locales.OwBackup()).Clicked() {
					a.owBackup(s)
				}
				if ui.Button(c, locales.OwReboot()).Clicked() {
					a.owRun(s, locales.OwReboot(), info.Hostname, locales.OwRebootNote(), monitor.OpenWrtRebootCommand, nil)
				}
			})
			if page.backup != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.OwBackupSaved(page.backup)).TextColor(theme.Success).Shrink(1).MinWidth(0)
					if ui.Button(c, locales.ShowInFolder()).Clicked() {
						a.plat.ShowInFolder(page.backup)
					}
				})
			}
			used := func(total, free uint64) string {
				if total == 0 {
					return "-"
				}
				return locales.OwUsedOf(formatSize(int64(total-min(free, total))), formatSize(int64(total)))
			}
			load := "-"
			if info.Uptime > 0 {
				load = strings.Join([]string{owLoad(info.Load[0]), owLoad(info.Load[1]), owLoad(info.Load[2])}, " / ")
			}
			firewall := map[string]string{"fw4": "fw4 (nftables)", "fw3": "fw3 (iptables)"}[info.Firewall]
			sftp := info.SFTP
			if sftp == "" {
				sftp = locales.OwSFTPMissing()
			}
			rows := [][2]string{
				{locales.SysHostname(), orDash(info.Hostname)},
				{locales.OwRelease(), orDash(info.Description)},
				{locales.SysKernel(), orDash(info.Kernel)},
				{locales.SysUptime(), uptimeText(time.Duration(info.Uptime) * time.Second)},
				{locales.OwLoad(), load},
				{locales.OwMemory(), used(info.MemTotal, info.MemAvailable)},
				{locales.OwRootFS(), used(info.RootTotal, info.RootFree)},
				{locales.OwTmpFS(), used(info.TmpTotal, info.TmpFree)},
				{locales.OwPackageManager(), orDash(info.PackageManager)},
				{locales.ServerFirewall(), orDash(firewall)},
				{"SFTP", sftp},
			}
			ui.Column(c).FillWidth().Padding(4, 0).Children(func() {
				for _, row := range rows {
					ui.Row(c.Key(row[0])).FillWidth().Gap(12).Padding(5, 0).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
						widgets.Muted(c, row[0]).Width(170).Shrink(0)
						ui.Text(c, row[1]).Selectable().Grow(1).MinWidth(0)
					})
				}
			})
			if info.SFTP == "" && info.PackageManager != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					widgets.Muted(c, locales.OwSFTPNote()).Shrink(1).MinWidth(0)
					if ui.Button(c, locales.OwInstallSFTP()).Clicked() {
						a.owInstallSFTP(s)
					}
				})
			}
		})
	})
}

func owLoad(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// owBackup saves a backup of the configuration, as sysupgrade -b makes
// it, into the download folder.
func (a *App) owBackup(s *Session) {
	page := &s.server.openwrt
	host := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < ' ' {
			return '_'
		}
		return r
	}, cmp.Or(page.info.Hostname, "openwrt"))
	name := "backup-" + host + "-" + time.Now().Format("2006-01-02-150405") + ".tar.gz"
	dir := a.downloadDir()
	dialog := &systemDialog{client: s.client, title: locales.OwBackup(), target: filepath.Join(dir, name), consequence: locales.OwBackupNote()}
	run := a.systemRoot(s)
	dialog.confirm = func() {
		dialog.busy = true
		go func() {
			ctx, cancel := context.WithTimeout(systemContext(s), 2*time.Minute)
			defer cancel()
			out, stderr, err := run(ctx, monitor.OpenWrtBackupCommand, nil, nil)
			path := filepath.Join(dir, name)
			if err == nil && len(out) < 2 {
				err = errors.New(locales.OwBackupEmpty())
			}
			if err == nil {
				err = os.WriteFile(path, []byte(out), 0o600)
			}
			a.post(func() {
				dialog.busy, dialog.done = false, true
				if err != nil {
					dialog.failure = systemError(stderr, err)
					return
				}
				page.backup = path
				dialog.output = locales.OwBackupSaved(path)
			})
		}()
	}
	page.dialog = dialog
}

// owInstallSFTP installs OpenWrt's SFTP server, then connects the Files
// tab's SFTP again.
func (a *App) owInstallSFTP(s *Session) {
	if s.isPanelTab() {
		s = s.panelOf
	}
	a.owRun(s, locales.OwInstallSFTP(), monitor.OpenWrtSFTPPackage, locales.OwInstallSFTPNote(), monitor.OpenWrtInstallSFTPCommand, func(s *Session) {
		page := &s.server.openwrt
		page.checked = false // read the SFTP server's path again
		a.openWrtDetect(s)
		a.retrySFTP(s)
	})
}

// retrySFTP starts SFTP again on s's connection, after it failed, as
// connecting does.
func (a *App) retrySFTP(s *Session) {
	client, ctx := s.client, s.ctx
	if client == nil || s.sftp != nil {
		return
	}
	go func() {
		sc, err := remotefs.NewClient(client)
		a.post(func() {
			if s.client != client || ctx != nil && ctx.Err() != nil {
				if sc != nil {
					sc.Close()
				}
				return
			}
			if err != nil {
				s.files.err = &sftpError{err}
				return
			}
			s.files.err = nil
			home, _ := sc.Getwd()
			if home == "" {
				home = "/"
			}
			s.sftp = sc
			a.openDir(s, home)
		})
	}()
}

// owSFTPHint is what the Files tab shows under its error on an OpenWrt
// server without SFTP: why, and the button to install it.
func (a *App) owSFTPHint(c *ui.Context, s *Session) {
	if _, ok := s.files.err.(*sftpError); !ok || !s.isOpenWrt() || s.server.openwrt.info.SFTP != "" {
		return
	}
	ui.Row(c).Padding(0, 10, 6, 10).Gap(8).AlignItems(ui.Center).Children(func() {
		widgets.Muted(c, locales.OwSFTPNote()).Shrink(1).MinWidth(0)
		if ui.PrimaryButton(c, locales.OwInstallSFTP()).Disabled(s.server.openwrt.info.PackageManager == "").Clicked() {
			a.owInstallSFTP(s)
		}
	})
	a.systemDialogView(c.Key("openwrt-sftp-dialog"), s, &s.server.openwrt.dialog)
}

// owConnectedVia tells whether the address of s's connection is one of
// addrs (as 192.168.1.1/24): the interface the connection goes through.
func owConnectedVia(s *Session, addrs []string) bool {
	if s.client == nil {
		return false
	}
	host, _, err := net.SplitHostPort(s.client.RemoteAddr().String())
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ip, _, _ := strings.Cut(a, "/"); ip == host {
			return true
		}
	}
	return false
}

const appOpenWrt desktopAppID = "openwrt"

func init() {
	registerDesktopApp(desktopApp{
		id: appOpenWrt, icon: "router", tint: ui.Hex("#00a3e0"), name: locales.OwTitle,
		category: desktopCatServer, order: 95, onDesktop: true, single: true,
		size: desktopSize{W: 960, H: 600}, minSize: desktopSize{W: 520, H: 360},
		view:      (*App).desktopOpenWrtView,
		available: func(s *Session) bool { return s.isOpenWrt() },
	})
}

// desktopOpenWrtView is the OpenWrt app's window: the System tab's
// OpenWrt sections.
func (a *App) desktopOpenWrtView(c *ui.Context, w *desktopWindow) {
	s := w.session
	ui.Column(c).Fill().MinHeight(0).Background(colors.overviewPage).Children(func() {
		switch {
		case s.status != statusConnected:
			overviewMessage(c, "unplug", s.statusText(), nil)
		case !execAllowed(s.Host):
			overviewMessage(c, "server", locales.SysInfoOff(), nil)
		default:
			a.openWrtPageOf(c, s)
		}
	})
}
