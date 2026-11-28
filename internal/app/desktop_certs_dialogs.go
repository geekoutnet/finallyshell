package app

import (
	"strings"

	"finallyshell/internal/certs"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The SSL certificates app's dialogs: a new certificate (and its
// issuance's progress), a download, a deletion, the DNS providers'
// credentials.

type certsDialogKind int

const (
	certsDialogNew certsDialogKind = iota + 1 // a domain to issue for; then the job's progress
	certsDialogSelfSigned
	certsDialogJob // the job's progress alone (a renewal)
	certsDialogDownload
	certsDialogDelete
	certsDialogDNS
)

type certsDialog struct {
	kind certsDialogKind
	cert certs.Cert

	// A new certificate's form.
	text     string
	rsa      bool
	staging  bool
	advanced bool
	formErr  string
	// started is set once the form started its issuance, which it then
	// shows.
	started bool

	// A download's formats and password.
	formats  map[certs.Format]bool
	password string
	saving   bool
	saveErr  string
	savedTo  string

	// The DNS providers' credentials: the provider shown, the values
	// typed.
	provider string
	values   map[string]string
	dnsNote  string
}

// certsOpenNew opens the form of a new certificate, or of a self-signed
// one.
func (a *App) certsOpenNew(s *Session, selfSigned bool) {
	kind := certsDialogNew
	if selfSigned {
		kind = certsDialogSelfSigned
	}
	d := &certsDialog{kind: kind}
	if job := a.certs.job; job != nil && !job.running() && job.stage != certsStageDone && !selfSigned {
		d.text = strings.Join(job.domains, ", ") // what failed, to try again
		d.rsa, d.staging = job.keyType == certs.KeyRSA2048, job.staging
	}
	a.certs.dialog = d
}

func (a *App) certsOpenDownload(cert certs.Cert) {
	formats := map[certs.Format]bool{}
	for _, f := range certs.Formats {
		formats[f] = true
	}
	a.certs.dialog = &certsDialog{kind: certsDialogDownload, cert: cert, formats: formats, password: cert.Password}
}

// certsOpenDNS opens the DNS providers' credentials, on provider ("" for
// the first).
func (a *App) certsOpenDNS(provider ...string) {
	id := certs.DNSProviders[0].ID
	if len(provider) > 0 && certs.FindDNSProvider(provider[0]) != nil {
		id = provider[0]
	}
	d := &certsDialog{kind: certsDialogDNS}
	a.certsDNSShow(d, id)
	a.certs.dialog = d
}

func (a *App) certsDNSShow(d *certsDialog, id string) {
	d.provider, d.values, d.dnsNote = id, map[string]string{}, ""
	for k, v := range a.certs.dns[id] {
		d.values[k] = v
	}
}

func (a *App) certsDialogView(c *ui.Context, w *desktopWindow) {
	st := &a.certs
	d := st.dialog
	if d == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		width, _ := c.Size()
		ui.Column(c).Width(min(max(width-56, 300), 560)).Gap(10).Children(func() {
			switch d.kind {
			case certsDialogNew, certsDialogSelfSigned, certsDialogJob:
				if st.job != nil && (d.kind == certsDialogJob || d.started) {
					a.certsJobView(c, w, d)
				} else {
					a.certsFormView(c, w, d)
				}
			case certsDialogDownload:
				a.certsDownloadView(c, d)
			case certsDialogDelete:
				a.certsDeleteView(c, d)
			case certsDialogDNS:
				a.certsDNSView(c, d)
			}
		})
	})
	if !open && st.dialog == d {
		st.dialog = nil // the job, if any, goes on, shown above the list
	}
}

// certsFormView is a new certificate's form: the domains, and options
// folded away.
func (a *App) certsFormView(c *ui.Context, w *desktopWindow, d *certsDialog) {
	st := &a.certs
	self := d.kind == certsDialogSelfSigned
	title, note := locales.CertsNewTitle(), locales.CertsNewNote()
	if self {
		title, note = locales.CertsSelfSignedTitle(), locales.CertsSelfSignedNote()
	}
	ui.Text(c, title).FontSize(17).Bold()
	widgets.Muted(c, note).FillWidth()
	if self {
		ui.Row(c).Gap(6).AlignItems(ui.Start).Padding(6, 8).Radius(4).Background(colors.warningBanner).FillWidth().Children(func() {
			ui.Icon(c, icons.Get("triangle-alert")).TextColor(theme.Warning).Shrink(0)
			ui.Text(c, locales.CertsSelfSignedWarning()).FillWidth()
		})
	}
	placeholder := locales.CertsDomainsPlaceholder()
	if self {
		placeholder = locales.CertsSelfSignedPlaceholder()
	}
	submitted := ui.TextInput(c, &d.text).Label(locales.CertsDomains()).Placeholder(placeholder).FillWidth().AutoFocus().Submitted()
	if d.formErr != "" {
		ui.Text(c, d.formErr).TextColor(theme.Danger).FillWidth()
	}
	ui.Collapsible(c, locales.CertsAdvanced(), &d.advanced, func() {
		ui.Column(c).Gap(6).Padding(4, 0).Children(func() {
			ui.Checkbox(c, &d.rsa, locales.CertsKeyRSA())
			widgets.Muted(c, locales.CertsKeyNote()).FontSize(12)
			if !self {
				ui.Checkbox(c, &d.staging, locales.CertsStaging())
				widgets.Muted(c, locales.CertsStagingNote()).FontSize(12)
			}
		})
	})
	ui.Row(c).Gap(8).AlignSelf(ui.End).Children(func() {
		if ui.Button(c, locales.Cancel()).Clicked() {
			st.dialog = nil
		}
		label := locales.CertsGenerate()
		if ui.PrimaryButton(c, label).Disabled(strings.TrimSpace(d.text) == "").Clicked() || submitted && strings.TrimSpace(d.text) != "" {
			keyType := certs.KeyEC256
			if d.rsa {
				keyType = certs.KeyRSA2048
			}
			domains, problem := certsParseDomains(d.text, self)
			if problem != "" {
				d.formErr = problem
				return
			}
			d.formErr = ""
			if self {
				a.certsMakeSelfSigned(d, domains, keyType)
				return
			}
			d.started = true
			a.certsStart(w.session, domains, keyType, d.staging, "")
		}
	})
}

// certsMakeSelfSigned makes and keeps a self-signed certificate, then
// offers its download.
func (a *App) certsMakeSelfSigned(d *certsDialog, domains []string, keyType certs.KeyType) {
	st := &a.certs
	b, err := certs.SelfSigned(domains, keyType, st.clock(), certs.SelfSignedValidity)
	var saved certs.Cert
	if err == nil {
		saved, err = certsStore().Save(certs.Cert{Domains: domains, Method: certs.MethodSelfSigned, KeyType: keyType}, b, st.clock())
	}
	if err != nil {
		d.formErr = err.Error()
		return
	}
	a.certsReload()
	st.selected = saved.ID
	a.certsOpenDownload(saved)
}

// certsSteps are the steps an issuance shows, with their stages.
var certsSteps = []struct {
	stage certsStage
	label func() string
}{
	{certsStageDetect, locales.CertsStepDetect},
	{certsStageProve, locales.CertsStepProve},
	{certsStageWait, locales.CertsStepWait},
	{certsStageIssue, locales.CertsStepIssue},
	{certsStageDone, locales.CertsStepDone},
}

// certsJobView is an issuance's progress: its steps, why it proves the
// domains as it does, what it needs from the user, how it ended.
func (a *App) certsJobView(c *ui.Context, w *desktopWindow, d *certsDialog) {
	st := &a.certs
	job := st.job
	ui.Text(c, locales.CertsJobTitle(certsDomainsText(job.domains))).FontSize(17).Bold().FillWidth()
	if job.staging {
		ui.Text(c, locales.CertsStagingNote()).FontSize(12).TextColor(theme.Warning).FillWidth()
	}
	current := job.stage
	if current == certsStageFailed || current == certsStageCanceled {
		current = job.failedAt
	}
	if current == certsStageConfirm {
		current = certsStageDetect
	}
	ui.Column(c).Gap(5).FillWidth().Children(func() {
		for _, step := range certsSteps {
			ui.Row(c.Key(int(step.stage))).Gap(8).AlignItems(ui.Center).Children(func() {
				icon, color := "minus", theme.TextMuted
				switch {
				case job.stage == certsStageDone || step.stage < current:
					icon, color = "circle-check", theme.Success
				case step.stage == current && job.stage == certsStageFailed:
					icon, color = "circle-x", theme.Danger
				case step.stage == current && job.running():
					ui.Spinner(c).Label(step.label())
					icon = ""
				}
				if icon != "" {
					ui.Icon(c, icons.Get(icon)).TextColor(color).FontSize(15)
				}
				text := ui.Text(c, step.label())
				if step.stage > current && job.stage != certsStageDone {
					text.TextColor(theme.TextMuted)
				}
			})
		}
	})
	if job.reason != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Start).FillWidth().Children(func() {
			ui.Icon(c, icons.Get("info")).TextColor(theme.Accent).Shrink(0)
			ui.Text(c, job.reason).FillWidth().Selectable()
		})
	}
	if job.hint != "" && job.running() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().FillWidth().Children(func() {
			widgets.Muted(c, job.hint).Grow(1).MinWidth(0)
			if ui.Button(c, locales.CertsAddKey()).Clicked() {
				a.certsOpenDNS(job.hintProvider.ID)
			}
		})
	}
	if job.stage == certsStageConfirm && job.confirm != nil {
		a.certsConfirmBlock(c, job)
	}
	if len(job.records) > 0 && job.running() {
		a.certsRecordsView(c, job)
	}
	switch job.stage {
	case certsStageFailed:
		ui.Column(c).Gap(4).Padding(8, 10).Radius(6).Background(colors.dangerBanner).FillWidth().Children(func() {
			ui.Text(c, locales.CertsFailedAt(certsStageLabel(job.failedAt))).Bold().TextColor(theme.Danger)
			ui.Text(c, certsErrorText(job.err)).FillWidth().Selectable().FontSize(12)
		})
	case certsStageCanceled:
		widgets.Muted(c, locales.CertsCanceled())
	case certsStageDone:
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("circle-check")).TextColor(theme.Success)
			ui.Text(c, locales.CertsIssued(job.saved.NotAfter.Local().Format("2006-01-02"))).Bold()
		})
	}
	ui.Row(c).Gap(8).AlignSelf(ui.End).Wrap().Children(func() {
		switch {
		case job.running():
			if ui.Button(c, locales.Cancel()).Clicked() {
				job.cancel()
			}
		case job.stage == certsStageDone:
			if ui.Button(c, locales.Done()).Clicked() {
				st.dialog, st.job = nil, nil
			}
			if ui.PrimaryButton(c, locales.CertsDownload()).Clicked() {
				st.job = nil
				a.certsOpenDownload(job.saved)
			}
		default:
			if ui.Button(c, locales.Close()).Clicked() {
				st.dialog, st.job = nil, nil
			}
			if ui.PrimaryButton(c, locales.CertsRetry()).Clicked() {
				st.job = nil
				if job.renew != "" {
					st.dialog = &certsDialog{kind: certsDialogJob}
					a.certsStart(w.session, job.domains, job.keyType, job.staging, job.renew)
					return
				}
				a.certsOpenNew(w.session, false)
				st.dialog.text = strings.Join(job.domains, ", ")
				st.dialog.rsa, st.dialog.staging = job.keyType == certs.KeyRSA2048, job.staging
			}
		}
	})
}
