package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopBrowserState struct {
	choices                                  []desktopBrowserChoice
	choice                                   desktopBrowserChoice
	url, exitIP                              string
	discovering, starting, checking, opening bool
	// visit opens url once the browsers are found: a site or container
	// asked to be opened (desktopBrowserVisit) while they were looked for.
	visit   bool
	hosts   *desktopBrowserHosts
	issue   func() string
	stop    context.CancelFunc
	run     *desktopBrowserRun
	launch  desktopBrowserLauncher
	openTab func(context.Context, *desktopBrowserRun, string) error
}

type desktopBrowserProcess interface {
	Wait() error
	Quit()
	Kill()
}

type desktopBrowserLauncher func(*desktopBrowserRun, string) (desktopBrowserProcess, error)

// desktopBrowserVisit, given to the browser window as it opens, opens url
// at once; host, when not "", is reached at addr on the server whatever
// DNS says, so that a site nginx serves opens by its own name.
type desktopBrowserVisit struct{ url, host, addr string }

// desktopBrowserHosts are the names the browser's proxy connects to at
// another address, as a hosts file on the server would. The proxy reads
// them on its own goroutines.
type desktopBrowserHosts struct {
	mu    sync.Mutex
	names map[string]string
}

func (hosts *desktopBrowserHosts) set(host, addr string) {
	hosts.mu.Lock()
	defer hosts.mu.Unlock()
	if hosts.names == nil {
		hosts.names = map[string]string{}
	}
	hosts.names[strings.ToLower(host)] = addr
}

func (hosts *desktopBrowserHosts) resolve(host string) (string, bool) {
	if hosts == nil {
		return "", false
	}
	hosts.mu.Lock()
	defer hosts.mu.Unlock()
	addr, ok := hosts.names[strings.ToLower(host)]
	return addr, ok
}

// list is "host → addr" for each name, sorted.
func (hosts *desktopBrowserHosts) list() []string {
	if hosts == nil {
		return nil
	}
	hosts.mu.Lock()
	defer hosts.mu.Unlock()
	var lines []string
	for host, addr := range hosts.names {
		lines = append(lines, host+" → "+addr)
	}
	slices.Sort(lines)
	return lines
}

func (a *App) desktopBrowserOpened(w *desktopWindow, arg any) {
	a.desktopToolsNoteOpened(w)
	state, exists := w.state.(*desktopBrowserState)
	if !exists {
		state = &desktopBrowserState{discovering: true, hosts: &desktopBrowserHosts{}}
		w.state = state
		go func() {
			choices := desktopFindBrowsers(runtime.GOOS, os.Getenv, desktopBrowserRegistry, exec.LookPath, desktopBrowserExecutable)
			a.post(func() {
				if !a.desktopToolsLive(w) || !state.discovering {
					return
				}
				state.choices, state.discovering = choices, false
				if len(choices) != 0 && state.choice.path == "" {
					state.choice = choices[0]
				}
				if state.visit {
					state.visit = false
					a.desktopBrowserOpenURL(w, state, state.url)
				}
			})
		}()
	}
	switch target := arg.(type) {
	case string:
		state.url = target
	case desktopBrowserVisit:
		state.url = target.url
		if target.host != "" {
			if state.hosts == nil {
				state.hosts = &desktopBrowserHosts{}
			}
			state.hosts.set(target.host, target.addr)
		}
		if state.discovering {
			state.visit = true
		} else {
			a.desktopBrowserOpenURL(w, state, target.url)
		}
	}
}

func (a *App) desktopBrowserClosing(w *desktopWindow) bool {
	if state, ok := w.state.(*desktopBrowserState); ok && state.stop != nil {
		state.stop()
	}
	return true
}

func (a *App) desktopBrowserOpenURL(w *desktopWindow, state *desktopBrowserState, raw string) {
	if !desktopBrowserCanOpen(w, state) {
		return
	}
	target := "about:blank"
	if strings.TrimSpace(raw) != "" || state.run != nil {
		parsed, err := desktopBrowserURL(raw)
		if err != nil {
			state.issue = locales.DesktopBrowserInvalidURL
			return
		}
		target = parsed
	}
	state.issue = nil
	if run := state.run; run != nil {
		state.opening = true
		openTab := state.openTab
		if openTab == nil {
			openTab = desktopBrowserOpenTab
		}
		go func() {
			ctx, cancel := context.WithTimeout(run.ctx, 12*time.Second)
			defer cancel()
			err := openTab(ctx, run, target)
			a.post(func() {
				if state.run == run {
					state.opening = false
					if err != nil && run.ctx.Err() == nil {
						state.issue = locales.DesktopBrowserOpenFailed
					}
				}
			})
		}()
		return
	}
	a.desktopBrowserStart(w, state, target)
}

func desktopBrowserCanOpen(w *desktopWindow, state *desktopBrowserState) bool {
	if state.starting || state.opening || w.session.status != statusConnected {
		return false
	}
	if state.run != nil {
		return state.run.ctx.Err() == nil
	}
	return state.choice.path != "" && w.session.client != nil
}

func (a *App) desktopBrowserStart(w *desktopWindow, state *desktopBrowserState, target string) {
	ctx, stop := a.desktopToolsContext(w)
	state.stop, state.starting, state.exitIP = stop, true, ""
	client, choice, hostID, hosts := w.session.client, state.choice, w.session.Host.ID, state.hosts
	root := filepath.Join(config.Dir(), "browser-profiles")
	launch := state.launch
	if launch == nil {
		launch = desktopLaunchBrowser
	}
	go func() {
		finish := func(issue func() string) {
			a.post(func() {
				state.run, state.stop, state.starting, state.checking, state.opening = nil, nil, false, false, false
				state.issue = issue
				stop()
			})
		}
		if ctx.Err() != nil {
			finish(nil)
			return
		}
		tunnel, err := desktopBrowserProxy(client, hosts.resolve)
		if err != nil {
			finish(locales.DesktopBrowserProxyFailed)
			return
		}
		defer tunnel.Close()
		stopTunnel := context.AfterFunc(ctx, tunnel.Stop)
		defer stopTunnel()
		addr := tunnel.Addr().String()
		profile, cleanup, err := desktopBrowserProfile(choice, root, hostID, addr)
		if err != nil {
			if errors.Is(err, errDesktopBrowserProfileInUse) {
				finish(locales.DesktopBrowserProfileInUse)
			} else {
				finish(locales.DesktopBrowserProfileFailed)
			}
			return
		}
		if ctx.Err() != nil {
			cleanup()
			finish(nil)
			return
		}
		run := &desktopBrowserRun{choice: choice, profile: profile, addr: addr, ctx: ctx}
		process, err := launch(run, target)
		if err != nil {
			cleanup()
			finish(locales.DesktopBrowserLaunchFailed)
			return
		}
		a.post(func() {
			if ctx.Err() == nil {
				state.run, state.starting = run, false
			}
		})
		exited := make(chan error, 1)
		go func() { exited <- process.Wait() }()
		var issue func() string
		select {
		case err := <-exited:
			if err != nil && ctx.Err() == nil {
				issue = locales.DesktopBrowserLaunchFailed
			}
		case <-ctx.Done():
			tunnel.Stop()
			desktopBrowserStopProcess(process, exited)
		case <-tunnel.Done():
			desktopBrowserStopProcess(process, exited)
			if ctx.Err() == nil {
				issue = locales.DesktopBrowserProxyFailed
			}
		}
		tunnel.Stop()
		cleanup()
		finish(issue)
	}()
}

func desktopBrowserStopProcess(process desktopBrowserProcess, exited <-chan error) {
	select {
	case <-exited:
		return
	default:
	}
	process.Quit()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		process.Kill()
		<-exited
	}
}

func (a *App) desktopBrowserCheckIP(state *desktopBrowserState) {
	run := state.run
	if run == nil || state.checking || run.ctx.Err() != nil {
		return
	}
	state.checking, state.issue = true, nil
	go func() {
		ip, err := desktopBrowserExitIP(run.ctx, run.addr)
		a.post(func() {
			if state.run != run || run.ctx.Err() != nil {
				return
			}
			state.checking = false
			if err != nil {
				state.issue = locales.DesktopBrowserIPFailed
			} else {
				state.exitIP = ip
			}
		})
	}()
}

func desktopBrowserExitIP(ctx context.Context, addr string) (string, error) {
	transport := &http.Transport{DialContext: desktopBrowserDialer(addr).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("exit address unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64))
	if err != nil {
		return "", err
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(data)))
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

func (a *App) desktopBrowserChoose(w *desktopWindow, state *desktopBrowserState) {
	title, kind := locales.DesktopBrowserChoose(), locales.DesktopBrowserExecutable()
	go func() {
		var extensions []string
		if runtime.GOOS == "windows" {
			extensions = []string{"exe"}
		}
		path, err := a.plat.OpenFileOfType(WindowMain, title, kind, extensions)
		valid := path != "" && desktopBrowserExecutable(path)
		a.post(func() {
			if !a.desktopToolsLive(w) || state.starting || state.run != nil {
				return
			}
			if err != nil || path != "" && !valid {
				state.issue = locales.DesktopBrowserInvalidExecutable
			} else if valid {
				choice := desktopBrowserChoice{filepath.Base(path), path, strings.Contains(strings.ToLower(filepath.Base(path)), "firefox")}
				if !slices.Contains(state.choices, choice) {
					state.choices = append(state.choices, choice)
				}
				state.choice, state.issue = choice, nil
			}
		})
	}()
}

func desktopBrowserPorts(snapshot monitor.NetSnapshot) []int {
	var result []int
	for _, group := range snapshot.Groups {
		if !group.Listen || group.Proto != "tcp" || !group.Local.IsValid() {
			continue
		}
		addr := group.Local.Addr().Unmap()
		if !addr.IsUnspecified() && addr != netip.AddrFrom4([4]byte{127, 0, 0, 1}) {
			continue
		}
		port := int(group.Local.Port())
		switch port {
		case 80, 443, 3000, 3001, 4000, 4200, 5000, 5173, 5601, 8000, 8008, 8080, 8081, 8088, 8443, 8888, 9000, 9090, 9443:
			result = append(result, port)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func desktopBrowserLocalURL(port int) string {
	scheme := "http"
	if port == 443 || port == 8443 || port == 9443 {
		scheme = "https"
	}
	return scheme + "://127.0.0.1:" + strconv.Itoa(port)
}

func (a *App) desktopBrowserView(c *ui.Context, w *desktopWindow) {
	state := w.state.(*desktopBrowserState)
	if execAllowed(w.session.Host) && w.session.client != nil {
		a.pollNetwork(w.session)
	}
	ui.Scroll(c).Fill().MinHeight(0).Background(colors.overviewPage).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(13).Children(func() {
			a.desktopBrowserHeader(c, w, state)
			a.desktopBrowserAddress(c, w, state)
			a.desktopBrowserLinks(c, w, state)
			if state.issue != nil {
				ui.Text(c, state.issue()).TextColor(theme.Danger)
			} else if w.session.status != statusConnected {
				ui.Text(c, w.session.statusText()).TextColor(theme.TextMuted)
			}
		})
	})
}

func (a *App) desktopBrowserHeader(c *ui.Context, w *desktopWindow, state *desktopBrowserState) {
	ui.Column(c).FillWidth().Gap(10).Padding(13).Radius(10).Background(theme.Background).Border(1, theme.Border).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			desktopToolsIcon(c, w.app, 40)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, locales.DesktopBrowserHeading()).FontSize(18).Bold()
				ui.Text(c, locales.DesktopBrowserSubtitle()).FontSize(12).TextColor(theme.TextMuted)
			})
		})
		ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			label, tint := locales.DesktopBrowserStopped(), theme.TextMuted
			switch {
			case state.stop != nil && state.run != nil && state.run.ctx.Err() != nil:
				label = locales.DesktopBrowserStopping()
			case state.starting:
				label, tint = locales.DesktopBrowserStarting(), theme.AccentText
				ui.Spinner(c).FontSize(16).Label(label)
			case state.run != nil:
				label, tint = locales.DesktopBrowserRunning(), ui.Hex("#14734b")
				if theme.Dark {
					tint = ui.Hex("#76e2a5")
				}
			}
			ui.Text(c, label).FontSize(12).Bold().Padding(4, 8).Radius(8).Background(tint.Alpha(0.12)).TextColor(tint)
			if state.run != nil {
				ui.Text(c, locales.DesktopBrowserProxy()+"  "+state.run.addr).Font("monospace").FontSize(12).Selectable()
			}
			if state.stop != nil && ui.Button(c, locales.DesktopBrowserStop()).Clicked() {
				state.stop()
			}
		})
	})
}

func (a *App) desktopBrowserAddress(c *ui.Context, w *desktopWindow, state *desktopBrowserState) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			input := ui.TextInput(c, &state.url).Grow(1).MinWidth(0).Label(locales.DesktopBrowserAddress()).Placeholder("https://example.com")
			label := locales.DesktopBrowserStart()
			if state.run != nil {
				label = locales.OpenFile()
			}
			disabled := !desktopBrowserCanOpen(w, state)
			if ui.PrimaryButton(c, label).Disabled(disabled).Shrink(0).Clicked() || !disabled && input.Shortcut(0, ui.KeyEnter) {
				a.desktopBrowserOpenURL(w, state, state.url)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			label := locales.DesktopBrowserNoChoice()
			if state.discovering {
				label = locales.DesktopBrowserDiscovering()
			} else if state.choice.path != "" {
				label = state.choice.name
			}
			ui.Text(c, label).TextColor(theme.TextMuted).FontSize(12).SingleLine().Ellipsis("…").MaxWidth(185)
			button := ui.Button(c, locales.DesktopBrowserChoose()).Disabled(state.discovering || state.stop != nil || state.run != nil)
			button.PopoverMenuButton(func(menu *ui.Menu) {
				for _, choice := range state.choices {
					if menu.Item(choice.name).Subtitle(choice.path).Checked(choice == state.choice).Chosen() {
						state.choice, state.issue = choice, nil
					}
				}
				if len(state.choices) != 0 {
					menu.Separator()
				}
				if menu.Item(locales.DesktopBrowserBrowse()).Chosen() {
					a.desktopBrowserChoose(w, state)
				}
			})
			if ui.Button(c, locales.DesktopBrowserCheckIP()).Disabled(state.run == nil || state.checking || state.run.ctx.Err() != nil).Clicked() {
				a.desktopBrowserCheckIP(state)
			}
			if state.checking {
				ui.Spinner(c).FontSize(14).Label(locales.DesktopBrowserCheckIP())
			} else if state.exitIP != "" {
				ui.Text(c, state.exitIP).Font("monospace").FontSize(12).Selectable()
			}
		})
	})
}

func (a *App) desktopBrowserLinks(c *ui.Context, w *desktopWindow, state *desktopBrowserState) {
	ui.Column(c).FillWidth().Padding(12).Gap(7).Radius(10).Background(theme.Background).Border(1, theme.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.DesktopBrowserLocal()).Bold().Grow(1).MinWidth(0)
			if widgets.IconButton(c, "copy", locales.Copy()).Clicked() {
				c.WriteClipboard("http://127.0.0.1")
			}
		})
		ui.Text(c, locales.DesktopBrowserLocalNote()).TextColor(theme.TextMuted).FontSize(12)
		if mapped := state.hosts.list(); len(mapped) != 0 {
			ui.Text(c, locales.DesktopBrowserHostsNote()).TextColor(theme.TextMuted).FontSize(12)
			ui.Text(c, strings.Join(mapped, "\n")).Font("monospace").FontSize(12).Selectable()
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			ports := desktopBrowserPorts(w.session.net.snap)
			if !slices.Contains(ports, 80) {
				ports = append([]int{80}, ports...)
			}
			for _, port := range ports {
				target := desktopBrowserLocalURL(port)
				button := ui.Button(c.Key(port), ":"+strconv.Itoa(port)).Tooltip(target).Disabled(!desktopBrowserCanOpen(w, state))
				button.ContextMenu(func(menu *ui.Menu) {
					if menu.Item(locales.Copy()).Chosen() {
						c.WriteClipboard(target)
					}
				})
				if button.Clicked() {
					state.url = target
					a.desktopBrowserOpenURL(w, state, target)
				}
			}
		})
	})
}
