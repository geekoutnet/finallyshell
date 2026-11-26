package proxycheck

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// useServices points the services at local ones for a test.
func useServices(t *testing.T, ipapi, ipsb, cf http.HandlerFunc) {
	t.Helper()
	old := [3]string{IPAPIURL, IPSBURL, CloudflareURL}
	t.Cleanup(func() { IPAPIURL, IPSBURL, CloudflareURL = old[0], old[1], old[2] })
	start := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	IPAPIURL = start(ipapi) + "/json/?lang="
	IPSBURL = start(ipsb) + "/geoip"
	CloudflareURL = start(cf) + "/trace"
}

func failing(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "busy", http.StatusTooManyRequests)
}

// direct stands for a proxy that lets every connection through.
func direct(dials *int) Dial {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		*dials++
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

func TestCheck(t *testing.T) {
	t.Run("ip-api", func(t *testing.T) {
		langs := make(chan string, 1)
		useServices(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case langs <- r.URL.Query().Get("lang"):
			default:
			}
			w.Write([]byte(`{"status":"success","country":"日本","regionName":"东京都","city":"东京","isp":"Example KK","query":"203.0.113.7"}`))
		}, failing, failing)
		var dials int
		res, err := Check(context.Background(), direct(&dials), "zh-CN")
		if err != nil || res.IP != "203.0.113.7" || res.Location.String() != "日本 东京都 东京 Example KK" || res.Source != "ip-api.com" || res.Latency <= 0 {
			t.Fatalf("%+v, %v", res, err)
		}
		if lang := <-langs; lang != "zh-CN" {
			t.Fatalf("asked in %q", lang)
		}
	})
	t.Run("the next service", func(t *testing.T) {
		useServices(t, failing, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"ip":"198.51.100.4","country":"Germany","region":"Hesse","city":"Frankfurt","organization":"Example GmbH"}`))
		}, failing)
		var dials int
		res, err := Check(context.Background(), direct(&dials), "en")
		if err != nil || res.IP != "198.51.100.4" || res.Location.ISP != "Example GmbH" || res.Source != "ip.sb" {
			t.Fatalf("%+v, %v", res, err)
		}
		cfOnly := func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("fl=1\nip=192.0.2.9\nloc=SG\n")) }
		useServices(t, failing, failing, cfOnly)
		res, err = Check(context.Background(), direct(&dials), "en")
		if err != nil || res.IP != "192.0.2.9" || res.Location.Country != "SG" {
			t.Fatalf("%+v, %v", res, err)
		}
	})
	t.Run("through, nothing answered", func(t *testing.T) {
		useServices(t, failing, failing, failing)
		var dials int
		res, err := Check(context.Background(), direct(&dials), "en")
		if err != nil || res.IP != "" || res.Latency <= 0 || dials == 0 {
			t.Fatalf("%+v, %v", res, err)
		}
	})
	t.Run("the proxy fails", func(t *testing.T) {
		useServices(t, failing, failing, failing)
		refused := errors.New("proxy refused")
		res, err := Check(context.Background(), func(context.Context, string) (net.Conn, error) { return nil, refused }, "en")
		if !errors.Is(err, refused) || res.Latency != 0 {
			t.Fatalf("%+v, %v", res, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		useServices(t, failing, failing, failing)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var dials int
		if _, err := Check(ctx, direct(&dials), "en"); err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("%v", err)
		}
	})
}
