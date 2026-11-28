package certs

import (
	"errors"
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
		Help:   "https://dash.cloudflare.com/profile/api-tokens",
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
		Help:   "https://ram.console.aliyun.com/manage/ak",
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
		Help:   "https://console.cloud.tencent.com/cam/capi",
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
		Help: "https://console.huaweicloud.com/iam/#/mine/accessKey",
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
