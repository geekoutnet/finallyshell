package app

import (
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/sysproxy"
	"finallyshell/locales"
)

type tunnelRecipe struct {
	id      string
	group   int
	title   func() string
	summary func() string
	kind    config.TunnelKind
	listen  int
	target  int
	bind    string
	host    string
	socket  string
	lan     bool
}

var tunnelRecipeGroups = []func() string{
	locales.TrecGroupService, locales.TrecGroupIntranet, locales.TrecGroupForward, locales.TrecGroupReverse,
	locales.TrecGroupPublish, locales.TrecGroupLAN,
}

var tunnelRecipes = []tunnelRecipe{
	{id: "mysql", group: 0, title: locales.TrecMySQL, summary: locales.TrecServiceSummary, kind: config.TunnelLocal, listen: 3306, target: 3306},
	{id: "postgres", group: 0, title: locales.TrecPostgres, summary: locales.TrecServiceSummary, kind: config.TunnelLocal, listen: 5432, target: 5432},
	{id: "redis", group: 0, title: locales.TrecRedis, summary: locales.TrecServiceSummary, kind: config.TunnelLocal, listen: 6379, target: 6379},
	{id: "mongo", group: 0, title: locales.TrecMongo, summary: locales.TrecServiceSummary, kind: config.TunnelLocal, listen: 27017, target: 27017},
	{id: "elastic", group: 0, title: locales.TrecElastic, summary: locales.TrecServiceSummary, kind: config.TunnelLocal, listen: 9200, target: 9200},
	{id: "web", group: 0, title: locales.TrecWeb, summary: locales.TrecWebSummary, kind: config.TunnelLocal, listen: 8443},
	{id: "docker", group: 0, title: locales.TrecDocker, summary: locales.TrecDockerSummary, kind: config.TunnelLocal, listen: 2375, socket: "/var/run/docker.sock"},
	{id: "mysql-socket", group: 0, title: locales.TrecMySQLSocket, summary: locales.TrecSocketSummary, kind: config.TunnelLocal, listen: 3306, socket: "/var/run/mysqld/mysqld.sock"},
	{id: "rdp", group: 1, title: locales.TrecRDP, summary: locales.TrecIntranetSummary, kind: config.TunnelLocal, listen: 13389, target: 3389},
	{id: "ssh", group: 1, title: locales.TrecSSH, summary: locales.TrecIntranetSummary, kind: config.TunnelLocal, listen: 2222, target: 22},
	{id: "socks", group: 2, title: locales.TrecSOCKS, summary: locales.TrecForwardSummary, kind: config.TunnelDynamic, listen: 1080},
	{id: "http", group: 2, title: locales.TrecHTTP, summary: locales.TrecForwardSummary, kind: config.TunnelHTTP, listen: 3128},
	{id: "mixed", group: 2, title: locales.TrecMixed, summary: locales.TrecForwardSummary, kind: config.TunnelHTTP, listen: 7890},
	{id: "reverse-http", group: 3, title: locales.TrecReverseHTTP, summary: locales.TrecReverseSummary, kind: config.TunnelRemoteHTTP, listen: 3128},
	{id: "reverse-socks", group: 3, title: locales.TrecReverseSOCKS, summary: locales.TrecReverseSummary, kind: config.TunnelRemoteDynamic, listen: 1080},
	{id: "reverse-lan", group: 3, title: locales.TrecReverseLAN, summary: locales.TrecReverseLANSummary, kind: config.TunnelRemoteDynamic, listen: 1080},
	{id: "publish-web", group: 4, title: locales.TrecPublishWeb, summary: locales.TrecPublishSummary, kind: config.TunnelRemote, listen: 8080, target: 3000, bind: "0.0.0.0", host: config.LoopbackHost},
	{id: "publish-rdp", group: 4, title: locales.TrecPublishRDP, summary: locales.TrecPublishSummary, kind: config.TunnelRemote, listen: 3389, target: 3389, bind: "0.0.0.0", host: config.LoopbackHost},
	{id: "publish-port", group: 4, title: locales.TrecPublishPort, summary: locales.TrecPublishSummary, kind: config.TunnelRemote, listen: 8080, bind: "0.0.0.0", host: config.LoopbackHost},
	{id: "lan-web", group: 5, title: locales.TrecLANService, summary: locales.TrecLANSummary, kind: config.TunnelLocal, listen: 8080, target: 80, host: config.LoopbackHost, bind: "0.0.0.0", lan: true},
	{id: "lan-proxy", group: 5, title: locales.TrecLANProxy, summary: locales.TrecLANSummary, kind: config.TunnelHTTP, listen: 3128, bind: "0.0.0.0", lan: true},
}

func (recipe tunnelRecipe) draft(existing []config.Tunnel, canListen func(string) bool, lan []string) config.Tunnel {
	tunnel := config.Tunnel{Kind: recipe.kind, ListenHost: recipe.bind, TargetHost: recipe.host, TargetPort: recipe.target,
		TargetSocket: recipe.socket, Mixed: recipe.id == "mixed" || recipe.id == "lan-proxy"}
	if recipe.group == 0 && recipe.socket == "" {
		tunnel.TargetHost = config.LoopbackHost
	}
	if recipe.lan {
		tunnel.AllowFrom = slices.Clone(lan)
	}
	for port := recipe.listen; port <= min(recipe.listen+127, 65535); port++ {
		tunnel.ListenPort = port
		localProbe := net.JoinHostPort(config.LoopbackHost, strconv.Itoa(port))
		if !recipePortInUse(tunnel, existing) && (tunnel.Kind.IsRemote() || canListen(localProbe)) {
			return tunnel
		}
	}
	tunnel.ListenPort = 0
	return tunnel
}

func recipePortInUse(tunnel config.Tunnel, existing []config.Tunnel) bool {
	return tunnel.ListenPort > 0 && slices.ContainsFunc(existing, func(other config.Tunnel) bool {
		return other.ListenPort == tunnel.ListenPort && other.Kind.IsRemote() == tunnel.Kind.IsRemote()
	})
}

func recipeCanListen(addr string) bool {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

type recipeInterface struct {
	flags net.Flags
	addrs []string
}

func recipeInterfaces() []recipeInterface {
	interfaces, _ := net.Interfaces()
	var result []recipeInterface
	for _, iface := range interfaces {
		entry := recipeInterface{flags: iface.Flags}
		addresses, _ := iface.Addrs()
		for _, addr := range addresses {
			entry.addrs = append(entry.addrs, addr.String())
		}
		result = append(result, entry)
	}
	return result
}

func recipeLAN(interfaces []recipeInterface) []string {
	var subnets []string
	for _, iface := range interfaces {
		if iface.flags&net.FlagUp == 0 || iface.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		for _, addr := range iface.addrs {
			prefix, err := netip.ParsePrefix(addr)
			if err != nil || !prefix.Addr().IsPrivate() {
				continue
			}
			prefix = prefix.Masked()
			if !prefix.Addr().IsPrivate() || prefix.Bits() < 8 || !prefix.Contains(prefix.Addr().Next()) {
				continue
			}
			if subnet := prefix.String(); !slices.Contains(subnets, subnet) {
				subnets = append(subnets, subnet)
			}
		}
	}
	slices.Sort(subnets)
	return subnets
}

func recipeUpstreams(listening func(string) bool) []string {
	var found []string
	for _, candidate := range []string{"http://127.0.0.1:7890", "http://127.0.0.1:10809", "socks5://127.0.0.1:1080", "http://127.0.0.1:8889"} {
		_, address, _ := strings.Cut(candidate, "://")
		if listening(address) {
			found = append(found, candidate)
		}
	}
	return found
}

func recipeValidUpstream(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return false
	}
	_, valid := validPort(parsed.Port())
	return valid && validHost(parsed.Hostname()) && (parsed.Scheme == "http" || parsed.Scheme == "socks5")
}

type tunnelRecipeForm struct {
	tunnel                             config.Tunnel
	listen, target, upstream, acl, pac string
}

func newTunnelRecipeForm(tunnel config.Tunnel) tunnelRecipeForm {
	return tunnelRecipeForm{tunnel: tunnel, listen: strconv.Itoa(tunnel.ListenPort), target: strconv.Itoa(tunnel.TargetPort),
		upstream: tunnel.Upstream, acl: strings.Join(tunnel.AllowFrom, ", "), pac: strings.Join(tunnel.PAC, ", ")}
}

func (form *tunnelRecipeForm) result(existing []config.Tunnel, lan bool) (config.Tunnel, string) {
	tunnel := form.tunnel
	tunnel.Name = strings.TrimSpace(tunnel.Name)
	tunnel.ListenHost = strings.TrimSpace(tunnel.ListenHost)
	tunnel.TargetHost = strings.TrimSpace(tunnel.TargetHost)
	tunnel.TargetSocket = strings.TrimSpace(tunnel.TargetSocket)
	port, err := strconv.ParseUint(strings.TrimSpace(form.listen), 10, 16)
	if err != nil {
		return tunnel, locales.ErrPortInvalid()
	}
	tunnel.ListenPort = int(port)
	if tunnel.ListenHost != "" && !validHost(tunnel.ListenHost) || tunnel.Kind.IsRemote() && !config.IsLoopback(tunnel.ListenHost) && net.ParseIP(tunnel.ListenHost) == nil {
		return tunnel, locales.ErrListenAddrInvalid()
	}
	if !tunnel.Kind.IsProxy() {
		if tunnel.TargetSocket != "" {
			if tunnel.Kind != config.TunnelLocal || !strings.HasPrefix(tunnel.TargetSocket, "/") || strings.ContainsFunc(tunnel.TargetSocket, unicode.IsControl) {
				return tunnel, locales.TrecInvalidSocket()
			}
		} else {
			if !validHost(tunnel.TargetHost) {
				return tunnel, locales.ErrTargetHostRequired()
			}
			var valid bool
			tunnel.TargetPort, valid = validPort(form.target)
			if !valid {
				return tunnel, locales.ErrPortInvalid()
			}
		}
	}
	if tunnel.Kind.IsRemote() && tunnel.Kind.IsProxy() {
		tunnel.Upstream = strings.TrimSpace(form.upstream)
		if !recipeValidUpstream(tunnel.Upstream) {
			return tunnel, locales.TrecInvalidUpstream()
		}
	}
	tunnel.AllowFrom = splitProxyRules(form.acl)
	for _, rule := range tunnel.AllowFrom {
		if _, err := netip.ParseAddr(rule); err != nil {
			if _, err := netip.ParsePrefix(rule); err != nil {
				return tunnel, locales.TrecInvalidACL()
			}
		}
	}
	if lan && !config.IsLoopback(tunnel.ListenHost) && len(tunnel.AllowFrom) == 0 {
		return tunnel, locales.TrecRequireLAN()
	}
	if !systemProxyAllowed(&tunnel) {
		tunnel.SystemProxy = false
	}
	if tunnel.SystemProxy {
		if _, err := sysproxy.BuildPAC(sysproxy.Settings{Address: "127.0.0.1:1", PAC: tunnel.PAC}); err != nil {
			return tunnel, locales.SysproxyInvalidPAC()
		}
	}
	if recipePortInUse(tunnel, existing) {
		return tunnel, locales.ErrTunnelDuplicate()
	}
	return tunnel, ""
}
