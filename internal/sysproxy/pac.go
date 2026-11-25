package sysproxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

var pacDomain = regexp.MustCompile(`^(?:\*\.)?[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func BuildPAC(settings Settings) (string, error) {
	host, port, err := proxyAddress(settings.Address)
	if err != nil {
		return "", err
	}
	protocol := "PROXY "
	if settings.SOCKS {
		protocol = "SOCKS5 "
	}
	proxy := pacQuote(protocol + net.JoinHostPort(host, port))
	var script strings.Builder
	script.WriteString("function FindProxyForURL(url, host) {\n  host = host.toLowerCase().replace(/\\.$/, \"\");\n")
	var global bool
	for _, value := range settings.PAC {
		rule := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
		if rule == "all" {
			global = true
			continue
		}
		condition, err := pacCondition(rule)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&script, "  if (%s) return %s;\n", condition, proxy)
	}
	if global {
		fmt.Fprintf(&script, "  return %s;\n", proxy)
	} else {
		script.WriteString("  return \"DIRECT\";\n")
	}
	script.WriteString("}\n")
	return script.String(), nil
}

func pacCondition(rule string) (string, error) {
	if prefix, err := netip.ParsePrefix(rule); err == nil {
		prefix = prefix.Masked()
		if prefix.Addr().Is4() {
			return fmt.Sprintf("isInNet(host, %s, %s)", pacQuote(prefix.Addr().String()), pacQuote(net.IP(net.CIDRMask(prefix.Bits(), 32)).String())), nil
		}
		return fmt.Sprintf(`(typeof isInNetEx === "function" && isInNetEx(host, %s))`, pacQuote(prefix.String())), nil
	}
	if len(rule) > 253 || !pacDomain.MatchString(rule) {
		return "", ErrRule
	}
	if strings.HasPrefix(rule, "*.") {
		return "shExpMatch(host, " + pacQuote(rule) + ")", nil
	}
	return "host === " + pacQuote(rule), nil
}

func pacQuote(value string) string {
	text, _ := json.Marshal(value)
	return string(text)
}
