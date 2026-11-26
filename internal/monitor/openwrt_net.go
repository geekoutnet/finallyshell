package monitor

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// OpenWrtInterface is a logical interface of netifd, as lan or wan, with
// the traffic of its device.
type OpenWrtInterface struct {
	Name      string
	Up        bool
	Pending   bool // coming up
	Proto     string
	Device    string // the layer 3 device, as br-lan or pppoe-wan
	Uptime    int64  // seconds up
	IPv4      []string
	IPv6      []string
	Gateway   string
	DNS       []string
	Error     string // why it is down, as NO_DEVICE
	MAC       string
	RX, TX    uint64 // bytes received and sent by the device
	Autostart bool
}

// OpenWrtInterfacesScript reads netifd's interfaces and their devices'
// counters.
const OpenWrtInterfacesScript = openWrtMarks + `m dump; ubus call network.interface dump 2>/dev/null
m devices; for d in /sys/class/net/*; do n=${d##*/}; echo "$n $(cat $d/statistics/rx_bytes 2>/dev/null) $(cat $d/statistics/tx_bytes 2>/dev/null) $(cat $d/address 2>/dev/null)"; done
m end
`

// ParseOpenWrtInterfaces reads the output of OpenWrtInterfacesScript, but
// the loopback.
func ParseOpenWrtInterfaces(out string) ([]OpenWrtInterface, error) {
	sec := procSections(out)
	type addr struct {
		Address string
		Mask    int
	}
	var dump struct {
		Interface []struct {
			Interface, Proto, Device string
			L3Device                 string `json:"l3_device"`
			Up, Pending, Autostart   bool
			Uptime                   int64
			IPv4                     []addr `json:"ipv4-address"`
			IPv6                     []addr `json:"ipv6-address"`
			Prefix                   []struct {
				Local *addr `json:"local-address"`
			} `json:"ipv6-prefix-assignment"`
			Route []struct {
				Target, Nexthop string
				Mask            int
			}
			DNS    []string `json:"dns-server"`
			Errors []struct{ Code string }
		}
	}
	if err := json.Unmarshal([]byte(sec["dump"]), &dump); err != nil {
		return nil, ErrScriptOutput
	}
	devices := map[string][]string{}
	for line := range strings.SplitSeq(sec["devices"], "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			devices[f[0]] = f[1:]
		}
	}
	var out2 []OpenWrtInterface
	for _, d := range dump.Interface {
		if d.Interface == "loopback" {
			continue
		}
		it := OpenWrtInterface{Name: d.Interface, Up: d.Up, Pending: d.Pending, Proto: d.Proto, Uptime: d.Uptime, DNS: d.DNS, Autostart: d.Autostart}
		it.Device = cmpOr(d.L3Device, d.Device)
		for _, a := range d.IPv4 {
			it.IPv4 = append(it.IPv4, a.Address+"/"+strconv.Itoa(a.Mask))
		}
		for _, a := range d.IPv6 {
			it.IPv6 = append(it.IPv6, a.Address+"/"+strconv.Itoa(a.Mask))
		}
		for _, p := range d.Prefix {
			if p.Local != nil && p.Local.Address != "" {
				it.IPv6 = append(it.IPv6, p.Local.Address+"/"+strconv.Itoa(p.Local.Mask))
			}
		}
		for _, r := range d.Route {
			if r.Target == "0.0.0.0" && r.Mask == 0 && it.Gateway == "" {
				it.Gateway = r.Nexthop
			}
		}
		if len(d.Errors) > 0 {
			it.Error = d.Errors[0].Code
		}
		if f := devices[it.Device]; len(f) == 3 {
			it.RX, _ = strconv.ParseUint(f[0], 10, 64)
			it.TX, _ = strconv.ParseUint(f[1], 10, 64)
			it.MAC = f[2]
		}
		out2 = append(out2, it)
	}
	slices.SortStableFunc(out2, func(a, b OpenWrtInterface) int { return strings.Compare(a.Name, b.Name) })
	return out2, nil
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// OpenWrtInterfaceCommand brings an interface up again (ifup, which
// restarts one that is up) or down (ifdown).
func OpenWrtInterfaceCommand(name string, up bool) (string, error) {
	if !openWrtNameRE.MatchString(name) {
		return "", ErrOpenWrtInvalid
	}
	tool := "ifdown"
	if up {
		tool = "ifup"
	}
	return tool + " " + privilegeQuote(name), nil
}

// OpenWrtLease is a DHCP lease of dnsmasq.
type OpenWrtLease struct {
	Expires  int64 // Unix seconds; 0 for a lease that does not end
	MAC, IP  string
	Hostname string // "" when the client gave none
}

// OpenWrtStaticLease is a host section of the dhcp config: an address
// given to a MAC address.
type OpenWrtStaticLease struct {
	Section string // the uci section, as cfg0afc6b
	Name    string
	MACs    []string
	IP      string
}

// OpenWrtDHCPScript reads dnsmasq's leases and the dhcp config.
const OpenWrtDHCPScript = openWrtMarks + `lf=$(uci -q get dhcp.@dnsmasq[0].leasefile)
m now; date +%s
m leases; cat "${lf:-/tmp/dhcp.leases}" 2>/dev/null
m uci; uci -q -X show dhcp
m end
`

// ParseOpenWrtDHCP reads the output of OpenWrtDHCPScript: the leases,
// newest to end last, and the static leases, and the server's clock.
func ParseOpenWrtDHCP(out string) (leases []OpenWrtLease, static []OpenWrtStaticLease, now int64) {
	sec := procSections(out)
	now, _ = strconv.ParseInt(strings.TrimSpace(sec["now"]), 10, 64)
	for line := range strings.SplitSeq(sec["leases"], "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		expires, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		l := OpenWrtLease{Expires: expires, MAC: strings.ToLower(f[1]), IP: f[2], Hostname: f[3]}
		if l.Hostname == "*" {
			l.Hostname = ""
		}
		leases = append(leases, l)
	}
	for _, s := range parseUCI(sec["uci"]) {
		if s.Type != "host" {
			continue
		}
		macs := strings.Fields(strings.ToLower(s.Get("mac")))
		static = append(static, OpenWrtStaticLease{Section: s.Name, Name: s.Get("name"), MACs: macs, IP: s.Get("ip")})
	}
	return leases, static, now
}

// OpenWrtAddStaticLeaseCommand gives ip to mac, named name ("" for
// none), and has dnsmasq read it.
func OpenWrtAddStaticLeaseCommand(name, mac, ip string) (string, error) {
	if name != "" && !ValidOpenWrtHostname(name) || !ValidOpenWrtMAC(mac) || !ValidOpenWrtIPv4(ip) {
		return "", ErrOpenWrtInvalid
	}
	script := "set -e\ns=$(uci add dhcp host)\n"
	if name != "" {
		script += `uci set "dhcp.$s.name"=` + privilegeQuote(name) + "\n"
	}
	script += `uci set "dhcp.$s.mac"=` + privilegeQuote(strings.ToLower(mac)) + "\n" +
		`uci set "dhcp.$s.ip"=` + privilegeQuote(ip) + "\n" + openWrtCommit("dhcp", "dnsmasq") + "\n"
	return script, nil
}

// OpenWrtDeleteSectionCommand deletes section of config, when it is of
// the type kind, and has service read the config again.
func OpenWrtDeleteSectionCommand(config, section, kind, service string) (string, error) {
	if !openWrtSectionRE.MatchString(section) || !openWrtSectionRE.MatchString(config) || !openWrtSectionRE.MatchString(kind) || !openWrtNameRE.MatchString(service) {
		return "", ErrOpenWrtInvalid
	}
	key := privilegeQuote(config + "." + section)
	return "set -e\n[ \"$(uci -q get " + key + ")\" = " + privilegeQuote(kind) + " ]\nuci delete " + key + "\n" + openWrtCommit(config, service) + "\n", nil
}

// OpenWrtForward is a port forward: a redirect section of the firewall
// config.
type OpenWrtForward struct {
	Section          string
	Name             string
	Proto            string // as "tcp udp"
	SrcZone, DstZone string
	ExternalPort     string
	InternalIP       string
	InternalPort     string // "" for the external one
	Enabled          bool
	Target           string // DNAT, as all port forwards; SNAT is not one
}

// OpenWrtFirewallScript reads the firewall config.
const OpenWrtFirewallScript = "uci -q -X show firewall\n"

// ParseOpenWrtFirewall reads the output of OpenWrtFirewallScript: the
// port forwards, and the names of the zones.
func ParseOpenWrtFirewall(out string) (forwards []OpenWrtForward, zones []string) {
	for _, s := range parseUCI(out) {
		switch s.Type {
		case "zone":
			if name := s.Get("name"); name != "" {
				zones = append(zones, name)
			}
		case "redirect":
			f := OpenWrtForward{
				Section: s.Name, Name: s.Get("name"), Proto: s.Get("proto"),
				SrcZone: s.Get("src"), DstZone: s.Get("dest"),
				ExternalPort: s.Get("src_dport"), InternalIP: s.Get("dest_ip"), InternalPort: s.Get("dest_port"),
				Enabled: s.Get("enabled") != "0", Target: cmpOr(s.Get("target"), "DNAT"),
			}
			if f.Proto == "" {
				f.Proto = "tcp udp" // fw3's and fw4's default
			}
			forwards = append(forwards, f)
		}
	}
	return forwards, zones
}

// OpenWrtForwardProtos are the protocols a new port forward may take,
// as the option has them.
var OpenWrtForwardProtos = []string{"tcp udp", "tcp", "udp"}

// OpenWrtAddForwardCommand adds a port forward from the zone src (wan)
// to ip in the zone dest (lan), and has the firewall read it: as LuCI
// writes one, which fw3 and fw4 both read.
func OpenWrtAddForwardCommand(f OpenWrtForward) (string, error) {
	name := strings.TrimSpace(f.Name)
	if name == "" || !opsValidText(name) || len(name) > 64 || !slices.Contains(OpenWrtForwardProtos, f.Proto) ||
		!openWrtSectionRE.MatchString(f.SrcZone) || !openWrtSectionRE.MatchString(f.DstZone) ||
		!ValidOpenWrtPorts(f.ExternalPort) || !ValidOpenWrtIPv4(f.InternalIP) ||
		f.InternalPort != "" && !ValidOpenWrtPorts(f.InternalPort) {
		return "", ErrOpenWrtInvalid
	}
	set := func(option, value string) string {
		return `uci set "firewall.$s.` + option + `"=` + privilegeQuote(value) + "\n"
	}
	script := "set -e\ns=$(uci add firewall redirect)\n" + set("name", name) + set("target", "DNAT") +
		set("src", f.SrcZone) + set("dest", f.DstZone)
	for _, proto := range strings.Fields(f.Proto) {
		script += `uci add_list "firewall.$s.proto"=` + privilegeQuote(proto) + "\n"
	}
	script += set("src_dport", f.ExternalPort) + set("dest_ip", f.InternalIP)
	if f.InternalPort != "" {
		script += set("dest_port", f.InternalPort)
	}
	return script + openWrtCommit("firewall", "firewall") + "\n", nil
}

// OpenWrtEnableForwardCommand turns the port forward in section on or
// off.
func OpenWrtEnableForwardCommand(section string, on bool) (string, error) {
	if !openWrtSectionRE.MatchString(section) {
		return "", ErrOpenWrtInvalid
	}
	key := privilegeQuote("firewall." + section)
	change := "uci set " + privilegeQuote("firewall."+section+".enabled") + "='0'"
	if on {
		change = "uci -q delete " + privilegeQuote("firewall."+section+".enabled") + " || true"
	}
	return "set -e\n[ \"$(uci -q get " + key + ")\" = 'redirect' ]\n" + change + "\n" + openWrtCommit("firewall", "firewall") + "\n", nil
}
