package certs

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/huaweicloud"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
)

// DNSProvider is a DNS service whose API adds and removes the TXT records
// that prove a domain. Adding one is adding an entry to DNSProviders.
type DNSProvider struct {
	// ID names it in the settings and the store, for good.
	ID string
	// Name is what it is called: a brand, the same in every language.
	Name string
	// Fields are the credentials its API takes.
	Fields []DNSField
	// Help is where its credentials are made.
	Help string
	// Nameservers are the endings of its name servers' names: a domain
	// whose name servers end so is hosted there.
	Nameservers []string
	// New makes its lego provider from the values of Fields, by key.
	New func(values map[string]string) (challenge.Provider, error)
}

// DNSField is a credential of a DNS provider.
type DNSField struct {
	Key   string
	Label string // the provider's own name for it
	// Secret ones are kept in the system's credential store, and hidden.
	Secret bool
	// Default fills it when it is left empty; Optional ones may stay so.
	Default  string
	Optional bool
}

// dnsWait is how long an API's record is waited for, and how often it is
// looked for, before the CA is asked to check it.
const (
	dnsWait     = 3 * time.Minute
	dnsInterval = 5 * time.Second
)

// DNSProviders are the DNS services the app adds records with.
var DNSProviders = []DNSProvider{
	{
		ID: "cloudflare", Name: "Cloudflare",
		Fields: []DNSField{{Key: "token", Label: "API Token", Secret: true}},
		Help:   "https://dash.cloudflare.com/profile/api-tokens", Nameservers: []string{".ns.cloudflare.com"},
		New: func(v map[string]string) (challenge.Provider, error) {
			cfg := cloudflare.NewDefaultConfig()
			cfg.AuthToken = v["token"]
			cfg.PropagationTimeout, cfg.PollingInterval, cfg.TTL = dnsWait, dnsInterval, 120
			return cloudflare.NewDNSProviderConfig(cfg)
		},
	},
	{
		ID: "alidns", Name: "Alibaba Cloud DNS",
		Fields: []DNSField{{Key: "id", Label: "AccessKey ID"}, {Key: "secret", Label: "AccessKey Secret", Secret: true}},
		Help:   "https://ram.console.aliyun.com/manage/ak", Nameservers: []string{".alidns.com", ".hichina.com"},
		New: func(v map[string]string) (challenge.Provider, error) {
			cfg := alidns.NewDefaultConfig()
			cfg.APIKey, cfg.SecretKey = v["id"], v["secret"]
			cfg.PropagationTimeout, cfg.PollingInterval = dnsWait, dnsInterval
			return alidns.NewDNSProviderConfig(cfg)
		},
	},
	{
		ID: "tencentcloud", Name: "Tencent Cloud DNSPod",
		Fields: []DNSField{{Key: "id", Label: "SecretId"}, {Key: "secret", Label: "SecretKey", Secret: true}},
		Help:   "https://console.cloud.tencent.com/cam/capi", Nameservers: []string{".dnspod.net", ".dnspod.com"},
		New: func(v map[string]string) (challenge.Provider, error) {
			cfg := tencentcloud.NewDefaultConfig()
			cfg.SecretID, cfg.SecretKey = v["id"], v["secret"]
			cfg.PropagationTimeout, cfg.PollingInterval = dnsWait, dnsInterval
			return tencentcloud.NewDNSProviderConfig(cfg)
		},
	},
	{
		ID: "huaweicloud", Name: "Huawei Cloud DNS",
		Fields: []DNSField{{Key: "id", Label: "Access Key ID"}, {Key: "secret", Label: "Secret Access Key", Secret: true},
			{Key: "region", Label: "Region", Default: "cn-north-4"}},
		Help:        "https://console.huaweicloud.com/iam/#/mine/accessKey",
		Nameservers: []string{".hwclouddns.cn", ".huaweicloud-dns.com", ".huaweicloud-dns.cn", ".huaweicloud-dns.net"},
		New: func(v map[string]string) (challenge.Provider, error) {
			cfg := huaweicloud.NewDefaultConfig()
			cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Region = v["id"], v["secret"], v["region"]
			cfg.PropagationTimeout, cfg.PollingInterval = dnsWait, dnsInterval
			return huaweicloud.NewDNSProviderConfig(cfg)
		},
	},
}

// FindDNSProvider is the provider id, nil for none.
func FindDNSProvider(id string) *DNSProvider {
	for i := range DNSProviders {
		if DNSProviders[i].ID == id {
			return &DNSProviders[i]
		}
	}
	return nil
}

// Provider makes p's lego provider from values, filling the defaults and
// checking that nothing needed is missing.
func (p *DNSProvider) Provider(values map[string]string) (challenge.Provider, error) {
	v := map[string]string{}
	for _, f := range p.Fields {
		s := strings.TrimSpace(values[f.Key])
		if s == "" {
			s = f.Default
		}
		if s == "" && !f.Optional {
			return nil, &MissingFieldError{Provider: p.Name, Field: f.Label}
		}
		v[f.Key] = s
	}
	return p.New(v)
}

// MissingFieldError says that a credential a DNS provider needs is empty.
type MissingFieldError struct{ Provider, Field string }

func (e *MissingFieldError) Error() string {
	return "certs: " + e.Provider + ": " + e.Field + " is empty"
}

// ErrNoDNSProvider says that a domain needs a DNS provider and has none.
var ErrNoDNSProvider = errors.New("certs: no DNS provider")

// HostedBy is the provider whose name servers are among nameservers, nil
// for none of DNSProviders.
func HostedBy(nameservers []string) *DNSProvider {
	for _, ns := range nameservers {
		ns = strings.ToLower(strings.TrimSuffix(ns, "."))
		for i := range DNSProviders {
			for _, end := range DNSProviders[i].Nameservers {
				if strings.HasSuffix(ns, end) {
					return &DNSProviders[i]
				}
			}
		}
	}
	return nil
}

// Nameservers are the name servers of the zone domain is in: those of the
// nearest of its parents that has some ("*." dropped), looked up with r,
// nil for the system's resolver.
func Nameservers(ctx context.Context, r *net.Resolver, domain string) ([]string, error) {
	if r == nil {
		r = net.DefaultResolver
	}
	name := strings.TrimPrefix(strings.TrimSuffix(domain, "."), "*.")
	var last error
	for strings.Contains(name, ".") {
		ns, err := r.LookupNS(ctx, name)
		if err == nil && len(ns) > 0 {
			out := make([]string, len(ns))
			for i, n := range ns {
				out[i] = strings.ToLower(strings.TrimSuffix(n.Host, "."))
			}
			return out, nil
		}
		if err != nil {
			last = err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_, name, _ = strings.Cut(name, ".")
	}
	if last == nil {
		last = errors.New("certs: no name servers")
	}
	return nil, last
}
