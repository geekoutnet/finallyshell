package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type serverSitesPage struct {
	client              *ssh.Client
	status              monitor.NginxStatus
	statusPoll          sessionPoll
	inventory           monitor.NginxInventory
	certificates        []monitor.Certificate
	certbot             []monitor.CertbotCertificate
	certFailures        []string
	schedule, certError string
	certbotNginx        bool
	loaded, reading     bool
	readErr             string
	denied              bool
	tabs                int
	sites, certs        dockerResourceTable
	dialog              *systemDialog
	recovery            *monitor.NginxChange
	read                systemRunner
	localTLS            func(context.Context, string) (monitor.Certificate, error)
	// tuning is the Nginx settings tab's form (serverpage_site_settings.go).
	tuning nginxTuning
}

func (a *App) sitesRoot(s *Session) systemRunner {
	priv := a.sessionPrivilege(s).runner
	return func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		return priv.Run(ctx, monitor.NginxToolPath+script, monitor.PrivilegeOptions{Stdin: input, Progress: progress, ScriptStdin: true, Interrupt: progress != nil})
	}
}

func sitesReader(s *Session) systemRunner {
	if read := s.server.sites.read; read != nil {
		return read
	}
	client := s.client
	return func(ctx context.Context, script string, _ io.Reader, progress func(string)) (string, string, error) {
		return monitor.RunScript(ctx, client, script)
	}
}

func (a *App) refreshServerSites(s *Session) {
	page := &s.server.sites
	client := s.client
	if !systemReady(s, client) || page.reading || client == nil && page.read == nil {
		return
	}
	page.reading = true
	read, root := sitesReader(s), a.sitesRoot(s)
	go func() {
		ctx, cancel := context.WithTimeout(systemContext(s), 90*time.Second)
		defer cancel()
		status, statusErr := readSiteStatus(ctx, read)
		var inventory monitor.NginxInventory
		var readErr error
		if status.Installed && !status.Custom {
			inventory, readErr = monitor.ReadNginxInventory(ctx, func(ctx context.Context, script string) (string, string, error) {
				return root(ctx, script, nil, nil)
			})
		}
		if statusErr != nil {
			readErr = statusErr
		}
		var result sitesCertificateData
		if !errors.Is(readErr, monitor.ErrPrivilegeCancelled) && !errors.Is(readErr, monitor.ErrPrivilegeDenied) && !errors.Is(readErr, monitor.ErrPrivilegeWrongPassword) {
			result = readSitesCertificates(ctx, root, status, inventory)
		}
		a.post(func() {
			if !systemReady(s, client) || page.client != client {
				return
			}
			page.loaded, page.reading = true, false
			page.status = status
			if readErr == nil || page.inventory.Main == "" {
				page.inventory = inventory
			}
			page.readErr = siteError("", readErr)
			page.denied = siteRootDenied(readErr) || result.denied
			page.certificates, page.certFailures = result.certificates, result.filesFailed
			page.certbot, page.certbotNginx = result.certbot, result.plugin
			page.schedule, page.certError = result.schedule, result.failure
			page.sites.selected.Clear()
			page.certs.selected.Clear()
		})
	}()
}

func readSiteStatus(ctx context.Context, read systemRunner) (monitor.NginxStatus, error) {
	out, _, err := read(ctx, monitor.NginxStatusCommand, nil, nil)
	status := monitor.ParseNginxStatus(out)
	if err == nil && status.Installed && (status.PID > 0 || !status.Unit) {
		master, _, masterErr := read(ctx, monitor.NginxMasterCommand(status.PID), nil, nil)
		lines := strings.Fields(master)
		if masterErr == nil && len(lines) > 0 {
			fallback := monitor.ParseNginxStatus("MainPID=" + lines[0])
			status.PID, status.Custom, status.MasterVerified = fallback.PID, slices.Contains(lines[1:], "custom"), fallback.PID > 0
			if !status.Unit && status.PID > 0 {
				status.State = "active"
			}
		}
	}
	return status, err
}

type sitesCertificateData struct {
	certificates      []monitor.Certificate
	filesFailed       []string
	certbot           []monitor.CertbotCertificate
	schedule, failure string
	plugin, denied    bool
}

func readSitesCertificates(ctx context.Context, root systemRunner, status monitor.NginxStatus, inventory monitor.NginxInventory) (result sitesCertificateData) {
	discovery, stderr, err := root(ctx, monitor.CertificateDiscoveryCommand, nil, nil)
	if err != nil {
		result.failure = siteError(stderr, err)
		result.denied = siteRootDenied(err)
		return result
	}
	files := monitor.CertificateFiles(discovery, inventory.Certificates())
	for batch := range slices.Chunk(files, 25) {
		out, stderr, err := root(ctx, monitor.CertificateReadCommand(batch), nil, nil)
		if err != nil {
			result.failure = siteError(stderr, err)
			result.denied = siteRootDenied(err)
			break
		}
		certs, failed := monitor.ParseCertificateFiles(out, batch)
		result.certificates = append(result.certificates, certs...)
		result.filesFailed = append(result.filesFailed, failed...)
	}
	if status.Certbot {
		out, stderr, err := root(ctx, monitor.CertbotListCommand, nil, nil)
		if err != nil {
			result.failure = siteError(stderr, err)
			result.denied = result.denied || siteRootDenied(err)
		} else {
			result.certbot = monitor.ParseCertbot(out)
		}
		out, _, err = root(ctx, monitor.CertbotPluginsCommand, nil, nil)
		result.plugin = err == nil && monitor.CertbotHasNginx(out)
	}
	schedule, stderr, err := root(ctx, monitor.CertbotScheduleCommand, nil, nil)
	if err != nil {
		result.denied = result.denied || siteRootDenied(err)
		if result.failure == "" {
			result.failure = siteError(stderr, err)
		}
	}
	result.schedule = strings.TrimSpace(siteClean(schedule))
	return result
}

func (a *App) serverSitesSection(c *ui.Context, s *Session) {
	page := &s.server.sites
	if page.client != s.client {
		if page.dialog != nil && page.dialog.cancel != nil {
			page.dialog.cancel()
		}
		a.stopPoll(&page.statusPoll)
		*page = serverSitesPage{client: s.client, tabs: page.tabs, read: page.read, localTLS: page.localTLS}
	}
	if !page.loaded && !page.reading {
		a.refreshServerSites(s)
	}
	if page.loaded && s.client != nil {
		a.keepPolling(&page.statusPoll, s, 25*time.Second, func() pollRead {
			read := sitesReader(s)
			return func(ctx context.Context, _ *ssh.Client) func() {
				ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				status, err := readSiteStatus(ctx, read)
				return func() {
					if !page.reading && err == nil {
						if page.status.Installed != status.Installed || page.status.Custom != status.Custom || page.status.Certbot != status.Certbot {
							a.refreshServerSites(s)
						} else {
							page.status = status
						}
					}
				}
			}
		})
	}
	box := ui.Column(c.Key("sites-layout")).Grow(1).MinHeight(0).FillWidth()
	width := box.LayoutBounds().W
	box.Children(func() {
		a.sitesHeader(c, s)
		switch page.tabs {
		case 0:
			a.siteList(c.Key("site-list"), s, width > 0 && width < 825)
		case 1:
			a.certList(c.Key("cert-list"), s, width > 0 && width < 760)
		default:
			a.nginxSettingsView(c.Key("nginx-settings"), s)
		}
		if page.denied && page.dialog == nil {
			if ui.Button(c, locales.PrivNeedsRoot()).Margin(2, 8).AlignSelf(ui.Start).Clicked() {
				page.dialog = &systemDialog{client: s.client, title: locales.SiteAccess(), rootDenied: true}
			}
		}
		a.systemDialogView(c.Key("sites-dialog"), s, &page.dialog)
	})
}
