package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"finallyshell/internal/certs"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// certsConfirmBlock asks for the go-ahead of a change to the server: what
// it does, and that it is undone afterwards.
func (a *App) certsConfirmBlock(c *ui.Context, job *certsJob) {
	conf := job.confirm
	ui.Column(c).Gap(6).Padding(8, 10).Radius(6).Background(colors.warningBanner).FillWidth().Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("triangle-alert")).TextColor(theme.Warning)
			ui.Text(c, locales.CertsConfirmTitle()).Bold()
		})
		switch conf.kind {
		case certConfirmSplit:
			ui.Text(c, locales.CertsConfirmSplit(conf.target)).FillWidth()
			if len(job.servers) > 0 {
				widgets.Muted(c, locales.CertsConfirmPort80By(strings.Join(job.servers, ", "))).FontSize(12).FillWidth()
			}
		case certConfirmFirewall:
			ui.Text(c, locales.CertsConfirmFirewall(conf.target)).FillWidth()
		default:
			ui.Text(c, locales.CertsConfirmOther(conf.target)).FillWidth()
		}
		widgets.Muted(c, locales.CertsConfirmUndo()).FontSize(12).FillWidth()
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.PrimaryButton(c, locales.CertsConfirmApply()).Clicked() {
				a.certsAnswerConfirm(certsAnswerApply)
			}
			if ui.Button(c, locales.CertsConfirmUseDNS()).Clicked() {
				a.certsAnswerConfirm(certsAnswerManual)
			}
		})
	})
}

// certsRecordsView is the TXT records to add by hand, each copied in a
// click, and that the issuance goes on once they are there.
func (a *App) certsRecordsView(c *ui.Context, job *certsJob) {
	ui.Column(c).Gap(6).Padding(8, 10).Radius(6).Background(colors.card).Border(1, colors.line).FillWidth().Children(func() {
		ui.Text(c, locales.CertsRecordsTitle()).Bold()
		widgets.Muted(c, locales.CertsRecordsNote()).FontSize(12).FillWidth()
		for i, r := range job.records {
			ui.Column(c.Key(i)).Gap(3).FillWidth().Children(func() {
				certsCopyLine(c, locales.CertsRecordType(), "TXT")
				certsCopyLine(c, locales.CertsRecordName(), r.name)
				certsCopyLine(c, locales.CertsRecordValue(), r.value)
			})
			if i < len(job.records)-1 {
				ui.Divider(c)
			}
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(locales.CertsRecordsWaiting())
			widgets.Muted(c, locales.CertsRecordsWaiting()).FontSize(12)
		})
	})
}

// certsCopyLine is a label, a value and a button that copies it.
func certsCopyLine(c *ui.Context, label, value string) {
	ui.Row(c.Key(label)).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		widgets.Muted(c, label).FontSize(12).Width(64).Shrink(0)
		ui.Text(c, value).Font("monospace").FontSize(12).Selectable().Grow(1).MinWidth(0)
		if widgets.IconButton(c, "copy", locales.CertsCopy(label)).Clicked() {
			c.WriteClipboard(value)
		}
	})
}

// certsDownloadView is a download: the formats, the password of the PFX
// and JKS files, and where to save the ZIP.
func (a *App) certsDownloadView(c *ui.Context, d *certsDialog) {
	st := &a.certs
	ui.Text(c, locales.CertsDownloadTitle(certsDomainsText(d.cert.Domains))).FontSize(17).Bold().FillWidth()
	if d.cert.Method == certs.MethodSelfSigned {
		widgets.Muted(c, locales.CertsSelfSignedWarning()).FontSize(12).FillWidth()
	}
	if d.cert.Staging {
		ui.Text(c, locales.CertsStagingNote()).FontSize(12).TextColor(theme.Warning).FillWidth()
	}
	ui.Column(c).Gap(4).Children(func() {
		for _, f := range certs.Formats {
			on := d.formats[f]
			if ui.Checkbox(c.Key(string(f)), &on, certsFormatLabel(f)).Changed() {
				d.formats[f] = on
			}
			widgets.Muted(c, certsFormatNote(f)).FontSize(12).MarginStart(26)
		}
	})
	if d.formats[certs.FormatIIS] || d.formats[certs.FormatTomcat] {
		certsCopyLine(c, locales.CertsPassword(), d.password)
		widgets.Muted(c, locales.CertsPasswordNote()).FontSize(12).FillWidth()
	}
	if d.saveErr != "" {
		ui.Text(c, d.saveErr).TextColor(theme.Danger).FillWidth().Selectable()
	}
	if d.savedTo != "" {
		ui.Text(c, locales.CertsSaved(d.savedTo)).TextColor(theme.Success).FillWidth().Selectable()
	}
	chosen := 0
	for _, on := range d.formats {
		if on {
			chosen++
		}
	}
	ui.Row(c).Gap(8).AlignSelf(ui.End).Children(func() {
		if d.saving {
			ui.Spinner(c).Label(locales.CertsSaving())
		}
		if ui.Button(c, locales.Close()).Clicked() {
			st.dialog = nil
		}
		if ui.PrimaryButton(c, locales.CertsSaveZip()).Disabled(chosen == 0 || d.saving).Clicked() {
			a.certsSaveZip(d)
		}
	})
}

// certsSaveZip asks where to save the download, and writes it there.
func (a *App) certsSaveZip(d *certsDialog) {
	_, b, err := certsStore().Get(d.cert.ID)
	if err != nil {
		d.saveErr = locales.ErrSaveFailed(err.Error())
		return
	}
	var formats []certs.Format
	for _, f := range certs.Formats {
		if d.formats[f] {
			formats = append(formats, f)
		}
	}
	name := certsFileName(d.cert.Domains)
	var buf bytes.Buffer
	export := certs.Export{Name: name, Bundle: b, Formats: formats, Password: d.password, Readme: certsReadme(d.cert, formats), Time: a.certs.clock()}
	if err := export.WriteZip(&buf); err != nil {
		d.saveErr = locales.ErrSaveFailed(err.Error())
		return
	}
	d.saving, d.saveErr, d.savedTo = true, "", ""
	title := locales.CertsSaveZip()
	data := buf.Bytes()
	go func() {
		path, err := a.plat.SaveFile("", title, name+".zip", "ZIP", []string{"zip"})
		if err == nil && path != "" {
			// The dialog asked before replacing a file.
			err = os.WriteFile(path, data, 0o600)
			if err != nil {
				os.Remove(path)
			}
		}
		a.post(func() {
			d.saving = false
			switch {
			case err != nil:
				d.saveErr = locales.ErrSaveFailed(err.Error())
			case path != "":
				d.savedTo = path
			}
			a.redraw()
		})
	}()
}

// certsFileName is the files' base name: the first domain, a wildcard's
// "*" spelled out.
func certsFileName(domains []string) string {
	if len(domains) == 0 {
		return "certificate"
	}
	return strings.Replace(domains[0], "*.", "wildcard.", 1)
}

func certsFormatLabel(f certs.Format) string {
	switch f {
	case certs.FormatNginx:
		return locales.CertsFormatNginx()
	case certs.FormatApache:
		return locales.CertsFormatApache()
	case certs.FormatIIS:
		return locales.CertsFormatIIS()
	case certs.FormatTomcat:
		return locales.CertsFormatTomcat()
	}
	return string(f)
}

func certsFormatNote(f certs.Format) string {
	switch f {
	case certs.FormatNginx:
		return locales.CertsFormatNginxNote()
	case certs.FormatApache:
		return locales.CertsFormatApacheNote()
	case certs.FormatIIS:
		return locales.CertsFormatIISNote()
	case certs.FormatTomcat:
		return locales.CertsFormatTomcatNote()
	}
	return ""
}

// certsReadme is the ZIP's README.txt, in the UI's language: what each
// folder holds and what for; never the password.
func certsReadme(cert certs.Cert, formats []certs.Format) string {
	var b strings.Builder
	b.WriteString(locales.CertsReadmeTitle(certsDomainsText(cert.Domains)) + "\n\n")
	b.WriteString(locales.CertsReadmeExpires(cert.NotAfter.Local().Format("2006-01-02")) + "\n")
	if cert.Method == certs.MethodSelfSigned {
		b.WriteString(locales.CertsSelfSignedWarning() + "\n")
	}
	if cert.Staging {
		b.WriteString(locales.CertsStagingNote() + "\n")
	}
	b.WriteString("\n")
	for _, f := range formats {
		b.WriteString("- " + string(f) + "/: " + certsFormatLabel(f) + " — " + certsFormatNote(f) + "\n")
	}
	for _, f := range formats {
		if f == certs.FormatIIS || f == certs.FormatTomcat {
			b.WriteString("\n" + locales.CertsReadmePassword() + "\n")
			break
		}
	}
	b.WriteString("\n" + locales.CertsReadmeKey() + "\n")
	return strings.ReplaceAll(b.String(), "\n", "\r\n")
}

// certsDeleteView asks before deleting a certificate and its key.
func (a *App) certsDeleteView(c *ui.Context, d *certsDialog) {
	st := &a.certs
	ui.Text(c, locales.CertsDeleteTitle()).FontSize(17).Bold()
	ui.Text(c, locales.CertsDeleteNote(certsDomainsText(d.cert.Domains))).FillWidth()
	if d.saveErr != "" {
		ui.Text(c, d.saveErr).TextColor(theme.Danger).FillWidth()
	}
	ui.Row(c).Gap(8).AlignSelf(ui.End).Children(func() {
		if ui.Button(c, locales.Cancel()).Clicked() {
			st.dialog = nil
		}
		if ui.PrimaryButton(c, locales.Delete()).Label(locales.CertsDeleteTitle()).Clicked() {
			if err := certsStore().Delete(d.cert.ID); err != nil {
				d.saveErr = err.Error()
				return
			}
			st.dialog = nil
			a.certsReload()
		}
	})
}

// certsDNSView is the DNS providers' credentials: with them, a domain
// hosted there is proved with no step of the user's, wildcards too.
func (a *App) certsDNSView(c *ui.Context, d *certsDialog) {
	st := &a.certs
	ui.Text(c, locales.CertsDNSSettings()).FontSize(17).Bold()
	widgets.Muted(c, locales.CertsDNSNote()).FillWidth()
	names := make([]string, len(certs.DNSProviders))
	current := ""
	for i, p := range certs.DNSProviders {
		names[i] = p.Name
		if st.certsDNSReady(&certs.DNSProviders[i]) {
			names[i] += " ✓"
		}
		if p.ID == d.provider {
			current = names[i]
		}
	}
	if ui.Select(c, &current, names).Label(locales.CertsDNSProvider()).FillWidth().Changed() {
		for i := range names {
			if names[i] == current {
				a.certsDNSShow(d, certs.DNSProviders[i].ID)
			}
		}
	}
	p := certs.FindDNSProvider(d.provider)
	if p == nil {
		return
	}
	for _, f := range p.Fields {
		v := d.values[f.Key]
		ui.Field(c.Key(p.ID+f.Key), f.Label, func() {
			in := ui.TextInput(c, &v).Label(f.Label).FillWidth()
			if f.Secret {
				in.Password()
			}
			if f.Default != "" {
				in.Placeholder(f.Default)
			}
		})
		d.values[f.Key] = v
	}
	if p.Help != "" {
		ui.Link(c, locales.CertsDNSGetKey(p.Name), p.Help)
	}
	if d.dnsNote != "" {
		ui.Text(c, d.dnsNote).TextColor(theme.Success)
	}
	_, saved := st.dns[p.ID]
	ui.Row(c).Gap(8).AlignSelf(ui.End).Children(func() {
		if saved && ui.Button(c, locales.CertsDNSClear()).Clicked() {
			delete(st.dns, p.ID)
			a.deleteSecret(certsDNSKey(p.ID))
			a.certsDNSShow(d, p.ID)
			d.dnsNote = locales.CertsDNSCleared()
		}
		if ui.Button(c, locales.Close()).Clicked() {
			st.dialog = nil
		}
		if ui.PrimaryButton(c, locales.Save()).Clicked() {
			values := map[string]string{}
			for _, f := range p.Fields {
				if v := strings.TrimSpace(d.values[f.Key]); v != "" {
					values[f.Key] = v
				}
			}
			if _, err := p.Provider(values); err != nil {
				var missing *certs.MissingFieldError
				if errors.As(err, &missing) {
					d.dnsNote = ""
					d.values["_err"] = locales.CertsDNSMissing(missing.Field)
				}
				return
			}
			data, _ := json.Marshal(values)
			a.setSecret(certsDNSKey(p.ID), string(data))
			if st.dns == nil {
				st.dns = map[string]map[string]string{}
			}
			st.dns[p.ID] = values
			delete(d.values, "_err")
			d.dnsNote = locales.CertsDNSSaved(p.Name)
		}
	})
	if e := d.values["_err"]; e != "" {
		ui.Text(c, e).TextColor(theme.Danger)
	}
}

// certsDNSParse reads a provider's credentials as kept.
func certsDNSParse(v string) map[string]string {
	var values map[string]string
	if json.Unmarshal([]byte(v), &values) != nil {
		return nil
	}
	return values
}

// certsJobBanner is an issuance going on, or just ended, while its dialog
// is closed: open it again.
func (a *App) certsJobBanner(c *ui.Context) {
	st := &a.certs
	job := st.job
	bg := colors.card
	switch job.stage {
	case certsStageFailed:
		bg = colors.dangerBanner
	case certsStageConfirm:
		bg = colors.warningBanner
	}
	ui.Row(c).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		if job.running() {
			ui.Spinner(c).Label(certsStageLabel(job.stage))
		}
		text := locales.CertsBannerRunning(certsDomainsText(job.domains), certsStageLabel(job.stage))
		switch job.stage {
		case certsStageDone:
			text = locales.CertsBannerDone(certsDomainsText(job.domains))
		case certsStageFailed:
			text = locales.CertsBannerFailed(certsDomainsText(job.domains))
		case certsStageCanceled:
			text = locales.CertsCanceled()
		}
		ui.Text(c, text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if ui.Button(c, locales.CertsShow()).Clicked() {
			st.dialog = &certsDialog{kind: certsDialogJob}
		}
		if !job.running() && widgets.IconButton(c, "x", locales.Close()).Clicked() {
			st.job = nil
		}
	})
}

func certsStageLabel(s certsStage) string {
	for _, step := range certsSteps {
		if step.stage == s {
			return step.label()
		}
	}
	if s == certsStageConfirm {
		return locales.CertsStepDetect()
	}
	return ""
}

// certsErrorText is an issuance's error for the user: what the CA said,
// on as many lines as it took.
func certsErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
