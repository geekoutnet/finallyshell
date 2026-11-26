// Package proxycheck tests a proxy: whether it lets a connection through,
// how long that takes, and the address and the place the internet sees
// its connections come from.
package proxycheck

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/geo"
)

// Dial opens a connection to addr through the proxy being tested.
type Dial func(ctx context.Context, addr string) (net.Conn, error)

// Result is what testing a proxy found.
type Result struct {
	// Latency is how long the proxy took to open a connection to the
	// first service it was asked for.
	Latency time.Duration
	// IP is the address the services saw the request come from, the
	// proxy's way out; "" when none answered.
	IP string
	// Location is where IP is, as the service that answered says.
	Location geo.Location
	// Source is the service that answered.
	Source string
}

// The services asked, in order, through the proxy: each tells the
// address a request comes from, and where it is. Variables for the tests.
var (
	IPAPIURL      = "http://ip-api.com/json/?fields=status,message,country,regionName,city,isp,query&lang="
	IPSBURL       = "https://api.ip.sb/geoip"
	CloudflareURL = "https://www.cloudflare.com/cdn-cgi/trace"
)

// Check tests the proxy that dial goes through, asking the services in
// lang ("en" or "zh-CN", as geo.Lang gives). It fails when the proxy
// cannot open a connection to any of them; a proxy that lets connections
// through but whose services all failed is fine, with no IP.
func Check(ctx context.Context, dial Dial, lang string) (Result, error) {
	var res Result
	var mu sync.Mutex
	var dialErr error
	tr := &http.Transport{
		Proxy: nil, // the proxy tested is the only one
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := dial(ctx, addr)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if dialErr == nil {
					dialErr = err
				}
				return nil, err
			}
			if res.Latency == 0 {
				res.Latency = max(time.Since(start), time.Millisecond)
			}
			return conn, nil
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	for _, svc := range services {
		ip, loc, err := svc.ask(ctx, client, lang)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if err == nil && ip != "" {
			mu.Lock()
			res.IP, res.Location, res.Source = ip, loc, svc.name
			mu.Unlock()
			return res, nil
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if res.Latency == 0 {
		// Not one connection through: the proxy's own fault, as dialing
		// said.
		if dialErr == nil {
			dialErr = errors.New("the proxy opened no connection")
		}
		return res, dialErr
	}
	return res, nil
}

// services are asked in order until one answers.
var services = []struct {
	name string
	ask  func(context.Context, *http.Client, string) (string, geo.Location, error)
}{
	{"ip-api.com", ipAPI},
	{"ip.sb", ipSB},
	{"cloudflare.com", cloudflare},
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "finallyshell-proxy-check")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

func ipAPI(ctx context.Context, client *http.Client, lang string) (string, geo.Location, error) {
	body, err := get(ctx, client, IPAPIURL+lang)
	if err != nil {
		return "", geo.Location{}, err
	}
	var r struct {
		Status, Message, Country, RegionName, City, ISP, Query string
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", geo.Location{}, err
	}
	if r.Status != "success" {
		return "", geo.Location{}, errors.New(r.Message)
	}
	return r.Query, geo.Location{Country: r.Country, Region: r.RegionName, City: r.City, ISP: r.ISP}, nil
}

func ipSB(ctx context.Context, client *http.Client, _ string) (string, geo.Location, error) {
	body, err := get(ctx, client, IPSBURL)
	if err != nil {
		return "", geo.Location{}, err
	}
	var r struct {
		IP           string `json:"ip"`
		Country      string `json:"country"`
		Region       string `json:"region"`
		City         string `json:"city"`
		ISP          string `json:"isp"`
		Organization string `json:"organization"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", geo.Location{}, err
	}
	isp := r.ISP
	if isp == "" {
		isp = r.Organization
	}
	return r.IP, geo.Location{Country: r.Country, Region: r.Region, City: r.City, ISP: isp}, nil
}

// cloudflare reads Cloudflare's trace: the address, and the country as
// its two-letter code.
func cloudflare(ctx context.Context, client *http.Client, _ string) (string, geo.Location, error) {
	body, err := get(ctx, client, CloudflareURL)
	if err != nil {
		return "", geo.Location{}, err
	}
	var ip, loc string
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "ip":
			ip = v
		case "loc":
			loc = v
		}
	}
	return ip, geo.Location{Country: loc}, nil
}
