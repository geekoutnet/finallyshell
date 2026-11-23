package app

// The connection editor's pages for a Remote Desktop host
// (config.KindRDP): its address and login, then mstsc's options in
// groups as mstsc has them, Display, Local resources, Connection (with
// the gateway) and Advanced (more .rdp lines). The type choice on the
// first page switches between these and the SSH pages; the fields of the
// other kind are kept while the editor is open.

import (
	"runtime"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The Remote Desktop pages, after the first (editorSSH, which shows the
// RDP fields for an RDP host).
const (
	editorRDPDisplay    = "rdp-display"
	editorRDPResources  = "rdp-resources"
	editorRDPConnection = "rdp-connection"
	editorRDPAdvanced   = "rdp-advanced"
)

// The Remote Desktop pages' fields that can be wrong.
const (
	fieldRDPWidth editorField = 200 + iota
	fieldRDPHeight
	fieldRDPGateway
)

// rdpFieldOrder are those fields, in the order they show.
var rdpFieldOrder = []editorField{fieldRDPWidth, fieldRDPHeight, fieldRDPGateway}

// The display size choices.
const (
	rdpSizeFull   = ""       // full screen
	rdpSizeWindow = "window" // a window of the client's size
	rdpSizeCustom = "custom" // a window Width by Height
)

// The drive choices.
const (
	rdpDrivesNone = ""
	rdpDrivesAll  = "all"
	rdpDrivesSome = "some"
)

// rdpFields are the Remote Desktop pages' fields, as edited: the options
// themselves, but for those typed as text.
type rdpFields struct {
	cfg           config.RDPConfig // the saved ones' unknown keys kept
	size          string
	width, height string
	drives        string
	letters       string // the drives, for rdpDrivesSome
	// gatewayPassword is the gateway's, saved as the host's password is;
	// readingGateway is set while it is read from the store.
	gatewayPassword string
	readingGateway  bool
}

// rdpFieldsOf are the fields of h's options, the defaults for a new host
// or one without.
func (a *App) rdpFieldsOf(h *config.Host) rdpFields {
	var rf rdpFields
	if h != nil && h.RDP != nil {
		rf.cfg = *h.RDP
	}
	r := &rf.cfg
	switch {
	case !r.Windowed:
		rf.size = rdpSizeFull
	case r.Width > 0 && r.Height > 0:
		rf.size = rdpSizeCustom
	default:
		rf.size = rdpSizeWindow
	}
	if r.Width > 0 {
		rf.width = strconv.Itoa(r.Width)
	}
	if r.Height > 0 {
		rf.height = strconv.Itoa(r.Height)
	}
	switch d := config.NormalizeDrives(r.Drives); d {
	case "":
	case "*":
		rf.drives = rdpDrivesAll
	default:
		rf.drives, rf.letters = rdpDrivesSome, strings.Join(r.DriveLetters(), ", ")
	}
	if h != nil {
		rf.gatewayPassword = a.passwords[gatewaySecretKey(h.ID)]
	}
	return rf
}

// rdpSizeProblem says what is wrong with a width or height typed.
func rdpSizeProblem(s string) string {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 200 || n > 8192 {
		return locales.ErrRDPSize()
	}
	return ""
}

// rdpProblems adds to errs what is wrong with the Remote Desktop pages.
func (ed *connEditor) rdpProblems(errs map[editorField]string) {
	rf := &ed.rdp
	if rf.size == rdpSizeCustom {
		if p := rdpSizeProblem(rf.width); p != "" {
			errs[fieldRDPWidth] = p
		}
		if p := rdpSizeProblem(rf.height); p != "" {
			errs[fieldRDPHeight] = p
		}
	}
	if rf.cfg.GatewayUsage != config.GatewayOff && strings.TrimSpace(rf.cfg.GatewayHost) == "" {
		errs[fieldRDPGateway] = locales.ErrHostRequired()
	}
}

// result are the options as the fields have them, for saving.
func (rf *rdpFields) result() *config.RDPConfig {
	r := rf.cfg
	r.Windowed = rf.size != rdpSizeFull
	r.Width, r.Height = 0, 0
	if rf.size == rdpSizeCustom {
		r.Width, _ = strconv.Atoi(strings.TrimSpace(rf.width))
		r.Height, _ = strconv.Atoi(strings.TrimSpace(rf.height))
	}
	switch rf.drives {
	case rdpDrivesAll:
		r.Drives = "*"
	case rdpDrivesSome:
		r.Drives = config.NormalizeDrives(rf.letters)
		if r.Drives == "*" {
			r.Drives = ""
		}
	default:
		r.Drives = ""
	}
	r.Domain = strings.TrimSpace(r.Domain)
	r.Monitors = strings.TrimSpace(r.Monitors)
	r.Program, r.ProgramFolder = strings.TrimSpace(r.Program), strings.TrimSpace(r.ProgramFolder)
	r.GatewayHost, r.GatewayUser = strings.TrimSpace(r.GatewayHost), strings.TrimSpace(r.GatewayUser)
	r.Custom = strings.TrimSpace(r.Custom)
	return &r
}

// saveRDP saves the Remote Desktop pages into h: its login, kept as an
// SSH host's password is, its options and its gateway's password. The
// system's client connects, so h keeps no proxy, tunnels or key; its
// terminal options are left as they were.
func (a *App) saveRDP(h *config.Host, ed *connEditor) {
	h.User, h.Auth, h.KeyPath = strings.TrimSpace(ed.user), config.AuthPassword, ""
	a.keepLogin(h, ed.password, ed.remember, ed.readingSecret)
	h.RDP = ed.rdp.result()
	delete(a.proxySecrets(), proxySecretKey(h.ID))
	h.Proxy, h.Tunnels = nil, nil
	a.keepProxySecret(h, ed.remember, false)
	a.keepGatewaySecret(h, ed.rdp.gatewayPassword, ed.remember, ed.rdp.readingGateway)
}

// keepGatewaySecret keeps the gateway's password as the editor saved it,
// as keepProxySecret keeps a proxy's: for this run, and in the store
// with remember; deleted when h takes none. With unread, the saved one
// not read yet, an empty one leaves what the store keeps.
func (a *App) keepGatewaySecret(h *config.Host, pw string, remember, unread bool) {
	key, store := gatewaySecretKey(h.ID), hostSecretKey(h.ID, secretGatewayPassword)
	delete(a.secrets.untried, key)
	switch {
	case !usesGatewayPassword(h):
		delete(a.passwords, key)
		a.deleteSecret(store)
	case pw != "":
		a.passwords[key] = pw
		if remember {
			a.setSecret(store, pw)
		} else {
			a.deleteSecret(store)
		}
	case unread && remember:
	default:
		delete(a.passwords, key)
		a.deleteSecret(store)
	}
}

// hostKinds are the kinds the editor offers, with their labels.
func hostKinds() ([]config.HostKind, []string) {
	return []config.HostKind{config.KindSSH, config.KindRDP}, []string{locales.KindSSH(), locales.KindRDP()}
}

// setKind switches the editor to the pages of kind k, with its default
// port while the port is empty or the other kind's.
func (ed *connEditor) setKind(k config.HostKind) {
	old := (&config.Host{Kind: ed.kind}).DefaultPort()
	if p := strings.TrimSpace(ed.port); p == "" || p == strconv.Itoa(old) {
		ed.port = strconv.Itoa((&config.Host{Kind: k}).DefaultPort())
	}
	ed.kind = k
}

// kindField is the type choice at the top of the first page.
func kindField(c *ui.Context, ed *connEditor) {
	ui.Field(c, locales.FieldHostKind(), func() {
		kinds, labels := hostKinds()
		chosen := labels[max(slices.Index(kinds, ed.kind), 0)]
		if ui.Select(c, &chosen, labels).Width(220).Label(locales.FieldHostKind()).Changed() {
			ed.setKind(kinds[slices.Index(labels, chosen)])
		}
	}).Description(hostKindHint(ed.kind))
}

// hostKindHint says how a host of kind k is opened.
func hostKindHint(k config.HostKind) string {
	if k == config.KindRDP {
		return locales.KindRDPHint(rdpClientName())
	}
	return ""
}

// rdpPages are the editor's pages for a Remote Desktop host.
func (a *App) rdpPages(ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) []widgets.NavPage {
	return []widgets.NavPage{
		{ID: editorSSH, Title: locales.EditorPageRDP, View: func(c *ui.Context) { a.rdpGeneralPage(c, ed, errs, input) }},
		{ID: editorRDPDisplay, Title: locales.EditorPageRDPDisplay, View: func(c *ui.Context) { rdpDisplayPage(c, ed, errs, input) }},
		{ID: editorRDPResources, Title: locales.EditorPageRDPResources, View: func(c *ui.Context) { rdpResourcesPage(c, ed, input) }},
		{ID: editorRDPConnection, Title: locales.EditorPageRDPConnection, View: func(c *ui.Context) { a.rdpConnectionPage(c, ed, errs, input) }},
		{ID: editorRDPAdvanced, Title: locales.SectionAdvanced, View: func(c *ui.Context) { rdpAdvancedPage(c, ed) }},
	}
}

// rdpGeneralPage is the first page of a Remote Desktop host: the type,
// the address, and who logs in.
func (a *App) rdpGeneralPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	r := &ed.rdp.cfg
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionGeneral(), func() {
			kindField(c, ed)
			ui.Field(c, locales.FieldName(), func() {
				input(ui.TextInput(c, &ed.name).Placeholder(locales.FieldNamePlaceholder()))
			})
			ui.Field(c, locales.FieldHost(), func() {
				input(ui.TextInput(c, &ed.addr).Placeholder(locales.FieldHostPlaceholder()).AutoFocus())
			}).Error(errs[fieldAddr])
			ui.Field(c, locales.FieldPort(), func() {
				input(ui.TextInput(c, &ed.port).Width(90))
			}).Error(errs[fieldPort])
			ui.Field(c, locales.FieldNotes(), func() {
				ui.TextArea(c, &ed.notes).Lines(2, 4)
			})
		})
		ui.Fieldset(c, locales.SectionAuth(), func() {
			ui.Field(c, locales.FieldUser(), func() {
				input(ui.TextInput(c, &ed.user).Placeholder("Administrator"))
			}).Description(locales.RdpUserHint())
			ui.Field(c, locales.FieldDomain(), func() {
				input(ui.TextInput(c, &r.Domain).Placeholder(locales.FieldDomainPlaceholder()))
			})
			ui.Field(c, locales.FieldPassword(), func() {
				input(ui.TextInput(c, &ed.password).Password())
			}).Description(rdpPasswordNote())
			a.rememberField(c, &ed.remember, false)
		})
	})
}

// rdpPasswordNote says what happens to the password on this system's
// client.
func rdpPasswordNote() string {
	if runtime.GOOS == "darwin" {
		return locales.RdpPasswordNoteMac()
	}
	return locales.RdpPasswordNote()
}

// rdpCheck is a check box of an option that is on when *off is false,
// as the options kept as their inverse are (NoClipboard...).
func rdpCheck(c *ui.Context, off *bool, label string) {
	on := !*off
	if ui.Checkbox(c, &on, label).Changed() {
		*off = !on
	}
}

// rdpSelect is a select of the values of an option, with their labels,
// as choiceSelect is without a default.
func rdpSelect[K comparable](c *ui.Context, label string, value *K, values []K, labels []string, width float32) {
	chosen := labels[max(slices.Index(values, *value), 0)]
	if ui.Select(c, &chosen, labels).Width(width).Label(label).Changed() {
		*value = values[slices.Index(labels, chosen)]
	}
}

// rdpDisplayPage is the Display page: size, monitors, colors, scaling.
func rdpDisplayPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	rf := &ed.rdp
	r := &rf.cfg
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionRDPDisplay(), func() {
			ui.Field(c, locales.FieldRDPSize(), func() {
				rdpSelect(c, locales.FieldRDPSize(), &rf.size,
					[]string{rdpSizeFull, rdpSizeWindow, rdpSizeCustom},
					[]string{locales.RdpSizeFull(), locales.RdpSizeWindow(), locales.RdpSizeCustom()}, 220)
			})
			if rf.size == rdpSizeCustom {
				ui.Field(c, locales.FieldRDPWidth(), func() {
					input(ui.TextInput(c, &rf.width).Width(90).Placeholder("1920"))
				}).Error(errs[fieldRDPWidth])
				ui.Field(c, locales.FieldRDPHeight(), func() {
					input(ui.TextInput(c, &rf.height).Width(90).Placeholder("1080"))
				}).Error(errs[fieldRDPHeight])
			}
			ui.Field(c, "", func() {
				ui.Checkbox(c, &r.AllMonitors, locales.RdpAllMonitors())
			})
			if r.AllMonitors {
				ui.Field(c, locales.FieldRDPMonitors(), func() {
					input(ui.TextInput(c, &r.Monitors).Width(160).Placeholder("0,1"))
				}).Description(locales.RdpMonitorsHint())
			}
			ui.Field(c, locales.FieldRDPColors(), func() {
				depth := r.ColorDepthOrDefault()
				rdpSelect(c, locales.FieldRDPColors(), &depth, []int{32, 24, 16, 15},
					[]string{locales.RdpColors32(), locales.RdpColors24(), locales.RdpColors16(), locales.RdpColors15()}, 220)
				r.ColorDepth = depth
				if depth == 32 {
					r.ColorDepth = 0
				}
			})
			ui.Field(c, "", func() {
				ui.Column(c).Gap(8).Children(func() {
					ui.Checkbox(c, &r.SmartSizing, locales.RdpSmartSizing())
					rdpCheck(c, &r.NoDynamicResolution, locales.RdpDynamicResolution())
					rdpCheck(c, &r.NoConnectionBar, locales.RdpConnectionBar())
				})
			})
		})
		ui.Fieldset(c, locales.SectionRDPExperience(), func() {
			ui.Field(c, locales.FieldRDPNetwork(), func() {
				rdpSelect(c, locales.FieldRDPNetwork(), &r.Network,
					[]config.RDPNetwork{config.NetworkAuto, config.NetworkLAN, config.NetworkWAN, config.NetworkHighBroadband,
						config.NetworkSatellite, config.NetworkLowBroadband, config.NetworkModem},
					[]string{locales.RdpNetworkAuto(), locales.RdpNetworkLAN(), locales.RdpNetworkWAN(), locales.RdpNetworkHighBroadband(),
						locales.RdpNetworkSatellite(), locales.RdpNetworkLowBroadband(), locales.RdpNetworkModem()}, 300)
			})
			ui.Field(c, "", func() {
				ui.Column(c).Gap(8).Children(func() {
					rdpCheck(c, &r.NoBitmapCache, locales.RdpBitmapCache())
					rdpCheck(c, &r.NoAutoReconnect, locales.RdpAutoReconnect())
				})
			})
		})
	})
}

// rdpResourcesPage is the Local resources page: what of this computer
// the session uses.
func rdpResourcesPage(c *ui.Context, ed *connEditor, input func(ui.Element) ui.Element) {
	rf := &ed.rdp
	r := &rf.cfg
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionRDPAudio(), func() {
			ui.Field(c, locales.FieldRDPAudio(), func() {
				rdpSelect(c, locales.FieldRDPAudio(), &r.Audio,
					[]config.RDPAudio{config.AudioLocal, config.AudioRemote, config.AudioOff},
					[]string{locales.RdpAudioLocal(), locales.RdpAudioRemote(), locales.RdpAudioOff()}, 260)
			})
			ui.Field(c, "", func() {
				ui.Checkbox(c, &r.Microphone, locales.RdpMicrophone())
			})
		})
		ui.Fieldset(c, locales.SectionRDPKeyboard(), func() {
			ui.Field(c, locales.FieldRDPKeyboard(), func() {
				rdpSelect(c, locales.FieldRDPKeyboard(), &r.Keyboard,
					[]config.RDPKeyboard{config.KeyboardFullScreen, config.KeyboardLocal, config.KeyboardRemote},
					[]string{locales.RdpKeyboardFullScreen(), locales.RdpKeyboardLocal(), locales.RdpKeyboardRemote()}, 300)
			})
		})
		ui.Fieldset(c, locales.SectionRDPDevices(), func() {
			ui.Field(c, "", func() {
				ui.Column(c).Gap(8).Children(func() {
					rdpCheck(c, &r.NoClipboard, locales.RdpClipboard())
					rdpCheck(c, &r.NoPrinters, locales.RdpPrinters())
					rdpCheck(c, &r.NoSmartCards, locales.RdpSmartCards())
					ui.Checkbox(c, &r.Ports, locales.RdpPorts())
					rdpCheck(c, &r.NoWebAuthn, locales.RdpWebAuthn())
					ui.Checkbox(c, &r.Cameras, locales.RdpCameras())
				})
			})
			ui.Field(c, locales.FieldRDPDrives(), func() {
				rdpSelect(c, locales.FieldRDPDrives(), &rf.drives,
					[]string{rdpDrivesNone, rdpDrivesAll, rdpDrivesSome},
					[]string{locales.RdpDrivesNone(), locales.RdpDrivesAll(), locales.RdpDrivesSome()}, 220)
			})
			if rf.drives == rdpDrivesSome {
				ui.Field(c, locales.FieldRDPDriveLetters(), func() {
					input(ui.TextInput(c, &rf.letters).Width(160).Placeholder("C, D"))
				}).Description(locales.RdpDriveLettersHint())
			}
		})
		ui.Fieldset(c, locales.SectionRDPProgram(), func() {
			ui.Field(c, locales.FieldRDPProgram(), func() {
				input(ui.TextInput(c, &r.Program).Placeholder(`C:\Windows\System32\notepad.exe`))
			}).Description(locales.RdpProgramHint())
			if strings.TrimSpace(r.Program) != "" {
				ui.Field(c, locales.FieldRDPProgramFolder(), func() {
					input(ui.TextInput(c, &r.ProgramFolder).Placeholder(`C:\`))
				})
			}
		})
	})
}

// rdpConnectionPage is the Connection page: the session, security and
// the gateway.
func (a *App) rdpConnectionPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	rf := &ed.rdp
	r := &rf.cfg
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionRDPSecurity(), func() {
			ui.Field(c, "", func() {
				ui.Checkbox(c, &r.Admin, locales.RdpAdmin())
			}).Description(locales.RdpAdminHint())
			ui.Field(c, locales.FieldRDPServerAuth(), func() {
				rdpSelect(c, locales.FieldRDPServerAuth(), &r.ServerAuth,
					[]config.RDPServerAuth{config.ServerAuthWarn, config.ServerAuthConnect, config.ServerAuthRefuse, config.ServerAuthNone},
					[]string{locales.RdpServerAuthWarn(), locales.RdpServerAuthConnect(), locales.RdpServerAuthRefuse(), locales.RdpServerAuthNone()}, 300)
			})
			ui.Field(c, "", func() {
				rdpCheck(c, &r.NoNLA, locales.RdpNLA())
			}).Description(locales.RdpNLAHint())
		})
		ui.Fieldset(c, locales.SectionRDPGateway(), func() {
			ui.Field(c, locales.FieldRDPGatewayUsage(), func() {
				rdpSelect(c, locales.FieldRDPGatewayUsage(), &r.GatewayUsage,
					[]config.RDPGatewayUsage{config.GatewayOff, config.GatewayAlways, config.GatewayFallback, config.GatewayDetect},
					[]string{locales.RdpGatewayOff(), locales.RdpGatewayAlways(), locales.RdpGatewayFallback(), locales.RdpGatewayDetect()}, 300)
			})
			if r.GatewayUsage == config.GatewayOff {
				return
			}
			ui.Field(c, locales.RdpGatewayHost(), func() {
				input(ui.TextInput(c, &r.GatewayHost).Placeholder("gateway.example.com"))
			}).Error(errs[fieldRDPGateway])
			ui.Field(c, locales.FieldRDPGatewayAuth(), func() {
				rdpSelect(c, locales.FieldRDPGatewayAuth(), &r.GatewayAuth,
					[]config.RDPGatewayAuth{config.GatewayAuthAny, config.GatewayAuthPassword, config.GatewayAuthSmartCard},
					[]string{locales.RdpGatewayAuthAny(), locales.RdpGatewayAuthPassword(), locales.RdpGatewayAuthSmartCard()}, 260)
			})
			ui.Field(c, "", func() {
				ui.Checkbox(c, &r.GatewaySameLogin, locales.RdpGatewaySameLogin())
			})
			if r.GatewaySameLogin || r.GatewayAuth == config.GatewayAuthSmartCard {
				return
			}
			ui.Field(c, locales.RdpGatewayUser(), func() {
				input(ui.TextInput(c, &r.GatewayUser).Placeholder(`DOMAIN\user`))
			})
			ui.Field(c, locales.RdpGatewayPasswordTitle(), func() {
				input(ui.TextInput(c, &rf.gatewayPassword).Password())
			}).Description(a.rememberNote(ed.remember))
		})
	})
}

// rdpAdvancedPage is the Advanced page: more lines of the .rdp file.
func rdpAdvancedPage(c *ui.Context, ed *connEditor) {
	ui.Form(c, func() {
		ui.Field(c, locales.FieldRDPCustom(), func() {
			ui.TextArea(c, &ed.rdp.cfg.Custom).Lines(8, 16).Font("monospace").Placeholder("redirectlocation:i:1\nvideoplaybackmode:i:1")
		}).Description(locales.RdpCustomHint())
	})
}

// rdpClientName is the Remote Desktop client of this system, as the
// editor names it.
func rdpClientName() string {
	switch runtime.GOOS {
	case "windows":
		return "mstsc"
	case "darwin":
		return "Windows App"
	}
	return "FreeRDP"
}
