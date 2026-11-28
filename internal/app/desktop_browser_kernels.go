package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"finallyshell/internal/browserkernel"
	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The server browser's kernels: browsers downloaded on request into
// config.Dir()/browser-kernels instead of bundled with the app. One set
// serves every server's browser window; each host remembers which
// browser it uses in its DesktopHost.Browser, on this device only.

type desktopKernels struct {
	manager *browserkernel.Manager
	// client downloads the kernels: nil for http.DefaultClient, which
	// follows the system's proxy environment.
	client    *http.Client
	loaded    bool
	reading   bool
	installed map[browserkernel.Kind]browserkernel.Installed
	damaged   map[browserkernel.Kind]bool
	jobs      map[browserkernel.Kind]*desktopKernelJob
	// latest is the newest version a check found.
	latest   map[browserkernel.Kind]string
	checking map[browserkernel.Kind]bool
	issues   map[browserkernel.Kind]func() string
}

type desktopKernelJob struct {
	progress browserkernel.Progress
	cancel   context.CancelFunc
	removing bool
}

const desktopBrowserKernelKey = "kernel:"

func (a *App) kernelManager() *browserkernel.Manager {
	k := &a.browserKernels
	if k.manager == nil {
		k.manager = browserkernel.New(filepath.Join(config.Dir(), "browser-kernels"))
		k.installed = make(map[browserkernel.Kind]browserkernel.Installed)
		k.damaged = make(map[browserkernel.Kind]bool)
		k.jobs = make(map[browserkernel.Kind]*desktopKernelJob)
		k.latest = make(map[browserkernel.Kind]string)
		k.checking = make(map[browserkernel.Kind]bool)
		k.issues = make(map[browserkernel.Kind]func() string)
	}
	return k.manager
}

// desktopKernelsLoad reads the downloaded kernels once, and clears what
// interrupted downloads left.
func (a *App) desktopKernelsLoad() {
	m, k := a.kernelManager(), &a.browserKernels
	if k.loaded || k.reading {
		return
	}
	k.reading = true
	go func() {
		m.Clean()
		read := make(map[browserkernel.Kind]browserkernel.Installed)
		damaged := make(map[browserkernel.Kind]bool)
		for _, kind := range browserkernel.Kinds {
			installed, err := m.Installed(kind)
			if err == nil {
				read[kind] = installed
			} else if errors.Is(err, browserkernel.ErrDamaged) {
				damaged[kind] = true
			}
		}
		a.post(func() {
			k.reading, k.loaded = false, true
			for _, kind := range browserkernel.Kinds {
				if k.jobs[kind] == nil {
					k.installed[kind], k.damaged[kind] = read[kind], damaged[kind]
					if _, ok := read[kind]; !ok {
						delete(k.installed, kind)
					}
				}
			}
		})
	}()
}

// desktopKernelRead rereads one kernel after a change: on the caller's
// goroutine, posted.
func (a *App) desktopKernelRead(kind browserkernel.Kind) func() {
	installed, err := a.browserKernels.manager.Installed(kind)
	return func() {
		k := &a.browserKernels
		if err == nil {
			k.installed[kind] = installed
		} else {
			delete(k.installed, kind)
		}
		k.damaged[kind] = errors.Is(err, browserkernel.ErrDamaged)
	}
}

func desktopKernelIssue(err error) func() string {
	switch {
	case errors.Is(err, browserkernel.ErrInUse), errors.Is(err, browserkernel.ErrBusy):
		return locales.DesktopKernelInUse
	case errors.Is(err, browserkernel.ErrChecksum):
		return locales.DesktopKernelChecksum
	case errors.Is(err, browserkernel.ErrStalled):
		return locales.DesktopKernelStalled
	case errors.Is(err, browserkernel.ErrUnsupported):
		return locales.DesktopKernelUnsupported
	}
	return locales.DesktopKernelFailed
}

// desktopKernelInstall downloads the kernel's newest release, in place of
// the one there is, if any.
func (a *App) desktopKernelInstall(kind browserkernel.Kind) {
	m, k := a.kernelManager(), &a.browserKernels
	if k.jobs[kind] != nil {
		return
	}
	if m.InUse(kind) {
		k.issues[kind] = locales.DesktopKernelInUse
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &desktopKernelJob{cancel: cancel}
	k.jobs[kind], k.issues[kind] = job, nil
	client := k.client
	go func() {
		defer cancel()
		release, err := m.Latest(ctx, client, kind)
		version := ""
		if err == nil {
			version = release.Version
			_, err = m.Install(ctx, client, release, func(progress browserkernel.Progress) {
				a.post(func() { job.progress = progress })
			})
		}
		reread := a.desktopKernelRead(kind)
		a.post(func() {
			if k.jobs[kind] == job {
				delete(k.jobs, kind)
			}
			reread()
			switch {
			case err == nil:
				k.latest[kind] = version
			case !errors.Is(err, context.Canceled):
				k.issues[kind] = desktopKernelIssue(err)
			}
		})
	}()
}

func (a *App) desktopKernelCheck(kind browserkernel.Kind) {
	m, k := a.kernelManager(), &a.browserKernels
	if k.checking[kind] || k.jobs[kind] != nil {
		return
	}
	k.checking[kind], k.issues[kind] = true, nil
	client := k.client
	go func() {
		release, err := m.Latest(context.Background(), client, kind)
		a.post(func() {
			k.checking[kind] = false
			if err != nil {
				k.issues[kind] = locales.DesktopKernelCheckFailed
			} else {
				k.latest[kind] = release.Version
			}
		})
	}()
}

func (a *App) desktopKernelRemove(kind browserkernel.Kind) {
	m, k := a.kernelManager(), &a.browserKernels
	if k.jobs[kind] != nil {
		return
	}
	job := &desktopKernelJob{removing: true, cancel: func() {}}
	k.jobs[kind], k.issues[kind] = job, nil
	go func() {
		err := m.Remove(kind)
		reread := a.desktopKernelRead(kind)
		a.post(func() {
			if k.jobs[kind] == job {
				delete(k.jobs, kind)
			}
			reread()
			if err == nil {
				delete(k.latest, kind)
			} else if errors.Is(err, browserkernel.ErrInUse) || errors.Is(err, browserkernel.ErrBusy) {
				k.issues[kind] = locales.DesktopKernelInUse
			} else {
				k.issues[kind] = locales.DesktopKernelRemoveFailed
			}
		})
	}()
}

func desktopKernelChoice(installed browserkernel.Installed) desktopBrowserChoice {
	return desktopBrowserChoice{name: installed.Kind.Name(), path: installed.Path(), firefox: installed.Kind.FirefoxBased(), kernel: installed.Kind}
}

// desktopBrowserChoices are the browsers the window can run: the
// downloaded kernels, then the browsers found on this computer.
func (a *App) desktopBrowserChoices(state *desktopBrowserState) []desktopBrowserChoice {
	var choices []desktopBrowserChoice
	k := &a.browserKernels
	for _, kind := range browserkernel.Kinds {
		if installed, ok := k.installed[kind]; ok && k.jobs[kind] == nil {
			choices = append(choices, desktopKernelChoice(installed))
		}
	}
	return append(choices, state.choices...)
}

func desktopBrowserKey(choice desktopBrowserChoice) string {
	if choice.kernel != "" {
		return desktopBrowserKernelKey + string(choice.kernel)
	}
	return choice.path
}

// desktopBrowserSync keeps the window's browser the host's own while it
// is available, another one when the chosen one is gone.
func (a *App) desktopBrowserSync(w *desktopWindow, state *desktopBrowserState) {
	if state.discovering || !a.browserKernels.loaded || state.starting || state.run != nil || state.stop != nil {
		return
	}
	choices := a.desktopBrowserChoices(state)
	saved := a.desktopHost(w.session).Browser
	if saved != "" {
		for _, choice := range choices {
			if desktopBrowserKey(choice) == saved {
				state.choice = choice
				return
			}
		}
	}
	if !slices.Contains(choices, state.choice) {
		state.choice = desktopBrowserChoice{}
		if len(choices) != 0 {
			state.choice = choices[0]
		}
	}
}

// desktopBrowserPick makes choice the host's browser.
func (a *App) desktopBrowserPick(w *desktopWindow, state *desktopBrowserState, choice desktopBrowserChoice) {
	state.choice, state.issue = choice, nil
	if key := desktopBrowserKey(choice); a.desktopHost(w.session).Browser != key {
		a.changeDesktopHost(w.session, func(host *config.DesktopHost) { host.Browser = key })
	}
}

// desktopBrowserSavedChoice is the host's saved browser when it is an
// executable on this computer the search does not find.
func desktopBrowserSavedChoice(saved string, found []desktopBrowserChoice) (desktopBrowserChoice, bool) {
	if saved == "" || strings.HasPrefix(saved, desktopBrowserKernelKey) || !desktopBrowserExecutable(saved) {
		return desktopBrowserChoice{}, false
	}
	for _, choice := range found {
		if choice.path == saved {
			return desktopBrowserChoice{}, false
		}
	}
	return desktopBrowserChoice{filepath.Base(saved), saved, strings.Contains(strings.ToLower(filepath.Base(saved)), "firefox"), ""}, true
}

func desktopKernelNote(kind browserkernel.Kind) string {
	switch kind {
	case browserkernel.Chrome:
		return locales.DesktopKernelChromeNote()
	case browserkernel.Firefox:
		return locales.DesktopKernelFirefoxNote()
	}
	return locales.DesktopKernelCamoufoxNote()
}

func (a *App) desktopBrowserKernelsView(c *ui.Context, w *desktopWindow, state *desktopBrowserState) {
	m := a.kernelManager()
	ui.Column(c).FillWidth().Padding(12).Gap(10).Radius(10).Background(theme.Background).Border(1, theme.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.DesktopBrowserKernels()).Bold().Grow(1).MinWidth(0)
			if widgets.IconButton(c, "folder", locales.ShowInFolder()).Clicked() {
				root := m.Root
				go func() {
					_ = os.MkdirAll(root, 0o700)
					a.post(func() { a.plat.ShowInFolder(root) })
				}()
			}
		})
		ui.Text(c, locales.DesktopBrowserKernelsNote()).TextColor(theme.TextMuted).FontSize(12)
		for _, kind := range browserkernel.Kinds {
			a.desktopBrowserKernelRow(c.Key(string(kind)), state, kind)
		}
	})
}

func (a *App) desktopBrowserKernelRow(c *ui.Context, state *desktopBrowserState, kind browserkernel.Kind) {
	m, k := a.kernelManager(), &a.browserKernels
	installed, has := k.installed[kind]
	job, supported, inUse := k.jobs[kind], m.Supported(kind), m.InUse(kind)
	ui.Column(c).FillWidth().Gap(6).Padding(8, 0).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(180).Gap(2).Children(func() {
				title := kind.Name()
				if has {
					title += "  " + installed.Version
				}
				ui.Text(c, title).Bold()
				status := desktopKernelNote(kind)
				switch {
				case !supported:
					status = locales.DesktopKernelUnsupported()
				case job != nil || !k.loaded:
				case has:
					status = formatSize(installed.Size) + " · " + status
					if k.latest[kind] == installed.Version {
						status = locales.DesktopKernelUpToDate() + " · " + status
					}
				case k.damaged[kind]:
					status = locales.DesktopKernelDamaged()
				default:
					status = locales.DesktopKernelNotInstalled() + " · " + status
				}
				ui.Text(c, status).TextColor(theme.TextMuted).FontSize(12)
			})
			switch {
			case !supported || !k.loaded:
			case job != nil && job.removing:
				ui.Spinner(c).FontSize(14).Label(locales.Delete())
			case job != nil:
				if ui.Button(c, locales.Cancel()).Clicked() {
					job.cancel()
				}
			case state.confirmRemove == kind && has:
			case !has:
				label := locales.Download()
				if k.damaged[kind] {
					label = locales.DesktopKernelReinstall()
				}
				if ui.PrimaryButton(c, label).Clicked() {
					a.desktopKernelInstall(kind)
				}
				if k.damaged[kind] && ui.Button(c, locales.Delete()).TextColor(theme.Danger).Clicked() {
					a.desktopKernelRemove(kind)
				}
			default:
				latest := k.latest[kind]
				if latest != "" && latest != installed.Version {
					if ui.PrimaryButton(c, locales.DesktopKernelUpdate(latest)).Disabled(inUse).Clicked() {
						a.desktopKernelInstall(kind)
					}
				} else if k.checking[kind] {
					ui.Spinner(c).FontSize(14).Label(locales.DesktopKernelCheckUpdate())
				} else if ui.Button(c, locales.DesktopKernelCheckUpdate()).Clicked() {
					a.desktopKernelCheck(kind)
				}
				if ui.Button(c, locales.DesktopKernelReinstall()).Disabled(inUse).Clicked() {
					a.desktopKernelInstall(kind)
				}
				if ui.Button(c, locales.Delete()).Disabled(inUse).TextColor(theme.Danger).Clicked() {
					state.confirmRemove = kind
				}
			}
		})
		if job != nil && !job.removing {
			fraction, label := 0.0, locales.DesktopKernelResolving()
			switch job.progress.Phase {
			case browserkernel.Downloading:
				progress := formatSize(job.progress.Done)
				if job.progress.Total > 0 {
					fraction = float64(job.progress.Done) / float64(job.progress.Total)
					progress += " / " + formatSize(job.progress.Total)
				}
				label = locales.DesktopKernelDownloading(progress)
			case browserkernel.Extracting:
				label = locales.DesktopKernelExtracting()
				if job.progress.Total > 0 {
					fraction = float64(job.progress.Done) / float64(job.progress.Total)
				}
			}
			ui.Progress(c, min(fraction, 1)).FillWidth().Label(label)
			ui.Text(c, label).TextColor(theme.TextMuted).FontSize(12)
		}
		if state.confirmRemove == kind && has && job == nil {
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.DesktopKernelRemoveConfirm(kind.Name(), formatSize(installed.Size))).Grow(1).MinWidth(0)
				if ui.PrimaryButton(c, locales.Delete()).Clicked() {
					state.confirmRemove = ""
					a.desktopKernelRemove(kind)
				}
				if ui.Button(c, locales.Cancel()).Clicked() {
					state.confirmRemove = ""
				}
			})
		}
		if issue := k.issues[kind]; issue != nil {
			ui.Text(c, issue()).TextColor(theme.Danger).FontSize(12)
		}
	})
}

// desktopBrowserDataView resets the host's browser data: its Chromium
// profile, which keeps logins between runs.
func (a *App) desktopBrowserDataView(c *ui.Context, w *desktopWindow, state *desktopBrowserState) {
	ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		running := state.stop != nil || state.run != nil || state.starting
		if !state.confirmReset {
			if ui.Button(c, locales.DesktopBrowserResetProfile()).Disabled(running).Clicked() {
				state.confirmReset, state.notice = true, nil
			}
		} else {
			ui.Text(c, locales.DesktopBrowserResetConfirm()).Grow(1).MinWidth(0)
			if ui.PrimaryButton(c, locales.DesktopBrowserResetProfile()).Disabled(running).Clicked() {
				state.confirmReset = false
				root, hostID := filepath.Join(config.Dir(), "browser-profiles"), w.session.Host.ID
				go func() {
					err := desktopBrowserResetProfile(root, hostID)
					a.post(func() {
						if err != nil {
							state.notice = locales.DesktopBrowserResetFailed
						} else {
							state.notice = locales.DesktopBrowserResetDone
						}
					})
				}()
			}
			if ui.Button(c, locales.Cancel()).Clicked() {
				state.confirmReset = false
			}
		}
		if state.notice != nil {
			ui.Text(c, state.notice()).TextColor(theme.TextMuted).FontSize(12)
		}
	})
}
