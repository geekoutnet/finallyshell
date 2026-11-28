package app

import (
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"
)

func TestDockerInternalURLs(t *testing.T) {
	ct := &monitor.Container{
		Running: true,
		Ports: []monitor.PortBinding{
			{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 10161},
			{Port: 443, Proto: "tcp", HostIP: "10.0.0.5", HostPort: 8443},
			{Port: 53, Proto: "udp", HostPort: 53},
		},
		Exposed:  []string{"8080/tcp", "9000/udp"},
		Networks: []monitor.ContainerNetwork{{Name: "web", IP: "191.10.10.20"}, {Name: "none"}},
	}
	want := []string{"http://127.0.0.1:10161/", "http://10.0.0.5:8443/", "http://191.10.10.20:80/", "http://191.10.10.20:443/", "http://191.10.10.20:8080/"}
	if got := dockerInternalURLs(ct); !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	host := &monitor.Container{NetworkMode: "host", Exposed: []string{"3000/tcp"}}
	if got := dockerInternalURLs(host); !slices.Equal(got, []string{"http://127.0.0.1:3000/"}) {
		t.Fatalf("%q", got)
	}
}

// A site opened from the sites table reaches nginx on the server by its
// own name, whatever DNS says, through the browser's proxy.
func TestDesktopBrowserVisitMapsHost(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	server := sshtest.Start(t)
	session.client, session.Host.NoMonitor = server.Dial(t), true
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			io.WriteString(conn, "nginx")
			conn.Close()
		}
	}()
	port := target.Addr().(*net.TCPAddr).Port
	launched := make(chan *desktopBrowserRun, 1)
	window := a.desktopOpen(session, appBrowser, nil)
	state := window.state.(*desktopBrowserState)
	state.launch = func(run *desktopBrowserRun, url string) (desktopBrowserProcess, error) {
		launched <- run
		return &desktopTestBrowserProcess{done: make(chan struct{})}, nil
	}
	// The window keeps a browser it can find: the fake one, once the
	// search and the kernels are done.
	platform.until(t, "browsers found", func() bool { return !state.discovering && a.browserKernels.loaded })
	fake := desktopBrowserChoice{name: "Test Chromium", path: "fake"}
	state.choices, state.choice = []desktopBrowserChoice{fake}, fake
	url := "http://site.example.org:" + strings.TrimPrefix(target.Addr().String(), "127.0.0.1:") + "/"
	a.desktopOpen(session, appBrowser, desktopBrowserVisit{url, "site.example.org", "127.0.0.1"})
	platform.until(t, "browser started", func() bool { return state.run != nil })
	run := <-launched
	if state.url != url || !slices.Equal(state.hosts.list(), []string{"site.example.org → 127.0.0.1"}) {
		t.Fatalf("%q %q", state.url, state.hosts.list())
	}
	conn, err := desktopBrowserDialer(run.addr).DialContext(t.Context(), "tcp", net.JoinHostPort("site.example.org", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(conn)
	conn.Close()
	if string(reply) != "nginx" {
		t.Fatalf("reply %q", reply)
	}
	tester.Frame()
	if !tester.HasText(locales.DesktopBrowserHostsNote()) {
		t.Fatal("mapped names not shown")
	}
	state.stop()
}
