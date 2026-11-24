package monitor

import (
	"strings"
	"testing"
	"time"
)

func TestDockerResourcesCommands(t *testing.T) {
	cmd := DockerCmd{Tool: "docker", Sudo: true}
	for _, test := range []struct {
		name  string
		build func() (string, error)
		want  string
	}{
		{"network", func() (string, error) {
			return cmd.NetworkCreate(DockerNetworkSpec{Name: "dev-net", Driver: "macvlan", IPv4: DockerSubnet{Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", IPRange: "192.0.2.128/25"}, Options: "parent=eth0\nnote=a'b"})
		}, "sudo -n docker 'network' 'create' '--driver' 'macvlan' '--subnet' '192.0.2.0/24' '--gateway' '192.0.2.1' '--ip-range' '192.0.2.128/25' '--opt' 'parent=eth0' '--opt' 'note=a'\\''b' 'dev-net'"},
		{"connect", func() (string, error) { return cmd.NetworkConnect("net", "ctr", "192.0.2.5", "web,api") }, "sudo -n docker 'network' 'connect' '--ip' '192.0.2.5' '--alias' 'web' '--alias' 'api' 'net' 'ctr'"},
		{"volume", func() (string, error) {
			return cmd.VolumeCreate("data", "local", "device=:/exports/data\no=addr=192.0.2.10,rw", "team=dev")
		}, "sudo -n docker 'volume' 'create' '--driver' 'local' '--opt' 'device=:/exports/data' '--opt' 'o=addr=192.0.2.10,rw' '--label' 'team=dev' 'data'"},
		{"prune", func() (string, error) { return cmd.ResourcePrune("volume", true, "24.0.1") }, "sudo -n docker 'volume' 'prune' '--force' '--all'"},
		{"anonymous", func() (string, error) { return cmd.ResourcePrune("volume", false, "23.0.0") }, "sudo -n docker 'volume' 'prune' '--force' '--filter' 'all=false'"},
		{"bad name", func() (string, error) { return cmd.VolumeCreate("--help", "local", "", "") }, ""},
		{"bad IP", func() (string, error) { return cmd.NetworkConnect("net", "ctr", "2001:db8::1", "") }, ""},
		{"bad CIDR", func() (string, error) {
			return cmd.NetworkCreate(DockerNetworkSpec{Name: "net", Driver: "bridge", IPv4: DockerSubnet{Subnet: "192.0.2.42/24"}})
		}, ""},
		{"unsafe old prune", func() (string, error) { return cmd.ResourcePrune("volume", false, "22.0.0") }, ""},
		{"built-in", func() (string, error) { return cmd.NetworkRemove(DockerNetworkDetails{Name: "bridge", ID: "a"}) }, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.build()
			if test.want == "" {
				if err == nil || got != "" {
					t.Fatalf("unsafe command: %q, %v", got, err)
				}
				return
			}
			prefix := scriptHead
			if test.name == "prune" || test.name == "anonymous" {
				prefix += cmd.volumePruneGuard(test.name == "prune")
			}
			if err != nil || got != prefix+test.want+"\n" {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestDockerResourcesParse(t *testing.T) {
	rows, err := parseResourceList(`{"ID":"1111","Name":"docs-net","Driver":"bridge","Scope":"local"}`)
	if err != nil || len(rows) != 1 || rows[0].ID != "1111" {
		t.Fatalf("network list: %+v %v", rows, err)
	}
	nets, err := ParseDockerNetworkDetails(`[{"Name":"docs-net","Id":"1111","Created":"2026-01-02T03:04:05Z","IPAM":{"Config":[{"Subnet":"192.0.2.0/24","Gateway":"192.0.2.1"}]},"Containers":{"17":{"Name":"docs","IPv4Address":"192.0.2.2/24","MacAddress":"02:42:c0:00:02:02"}}}]`)
	if err != nil || len(nets) != 1 || nets[0].Subnets[0].Gateway != "192.0.2.1" || nets[0].Endpoints[0].ID != "17" || nets[0].Created.IsZero() {
		t.Fatalf("networks: %+v %v", nets, err)
	}
	rows, err = parseResourceList(`{"Name":"docs-data","Driver":"local","Mountpoint":"/example/docs-data"}`)
	if err != nil || rows[0].Name != "docs-data" {
		t.Fatalf("volume list: %+v %v", rows, err)
	}
	vols, err := ParseDockerVolumeDetails(`[{"Name":"docs-data","Driver":"local","Mountpoint":"/example/docs-data","Labels":{"com.docker.volume.anonymous":""}}]`)
	if err != nil || len(vols) != 1 || !vols[0].Anonymous {
		t.Fatalf("volumes: %+v %v", vols, err)
	}
}

func TestDockerSystemFormats(t *testing.T) {
	info, err := ParseDockerEngineInfo(`{"KernelVersion":"6.8","Architecture":"x86_64","NCPU":4,"Runtimes":{"runc":{"path":"runc"}},"RegistryConfig":{"Mirrors":["https://mirror.example"],"IndexConfigs":{"registry.example":{"Secure":false}}}}`, `{"Client":{"Version":"27.0","ApiVersion":"1.46"},"Server":{"Version":"27.0","ApiVersion":"1.46","Os":"linux"}}`)
	if err != nil || info.Client.Version != "27.0" || info.Server.OS != "linux" || info.Runtimes[0] != "runc" || info.Insecure[0] != "registry.example" {
		t.Fatalf("info: %+v %v", info, err)
	}
	usage, err := ParseDockerVolumeUsage("Local Volumes space usage:\n" + `{"Name":"docs-data","Links":"0","Size":"12.4MB"}` + "\nBuild cache usage: 0B\n")
	if err != nil || usage["docs-data"].Size != "12.4MB" || usage["docs-data"].Links != 0 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	usage, err = ParseDockerVolumeUsage(`{"Images":[],"Containers":[],"Volumes":[{"Name":"docs-data","Links":"1","Size":"12.4MB"},{"Name":"remote-data","Links":"N/A","Size":"N/A"}],"BuildCache":[]}`)
	if err != nil || usage["docs-data"].Links != 1 || usage["remote-data"].Links != -1 {
		t.Fatalf("current Docker usage: %+v %v", usage, err)
	}
	disk, err := ParseDockerResourceDisk(`{"Type":"Local Volumes","TotalCount":"3","Active":"1","Size":"20MB","Reclaimable":"12.4MB (62%)"}`)
	if err != nil || len(disk) != 1 || disk[0].Total != "3" {
		t.Fatalf("current Docker disk: %+v %v", disk, err)
	}
	event, ok := ParseDockerEvent(`{"Type":"container","Action":"start","timeNano":1700000000000000000,"Actor":{"ID":"111","Attributes":{"name":"docs"}}}`)
	if !ok || event.Name != "docs" || event.Time.Unix() != 1700000000 {
		t.Fatalf("event: %+v", event)
	}
	reg, err := ParseDockerRegistries(`{"registries":["https://index.docker.io/v1/"],"store":"pass","helpers":{"registry.example":"secretservice"},"auths":{"unsafe.example":{"auth":"synthetic-secret"}}}`)
	if err != nil || len(reg.Registries) != 1 || reg.Registries[0] != "docker.io" || reg.Helpers["registry.example"] != "secretservice" {
		t.Fatalf("registries: %+v %v", reg, err)
	}
}

func TestDockerSystemCommandSafety(t *testing.T) {
	cmd := DockerCmd{Tool: "docker", Sudo: true}
	login, err := cmd.RegistryLogin("docker.io", "reader", "synthetic-secret")
	if err != nil || login.script != "exec 2>&1\n"+registryLoginGuard+"'docker.io'"+registryHelperLookup+"docker 'login' '--username' 'reader' '--password-stdin' 'docker.io'\n" || login.input != "synthetic-secret\n" || strings.Contains(login.script, "synthetic-secret") {
		t.Fatal("password must only be sent on stdin")
	}
	config, err := cmd.DaemonSave("", `{"registry-mirrors":["https://mirror.example"]}`, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil || !strings.Contains(config.script, "daemon.json.bak-20260102T030405") || !strings.Contains(config.script, "'tee'") || strings.Contains(config.script, "mirror.example") {
		t.Fatalf("save: %q %v", config.script, err)
	}
	if _, err := cmd.DaemonSave("", `{"registry-mirrors":["http://mirror.example"]}`, time.Now()); err == nil {
		t.Fatal("accepted insecure mirror")
	}
	if logout, err := cmd.RegistryLogout("docker.io"); err != nil || logout != scriptHead+"unset DOCKER_AUTH_CONFIG\nDOCKER_CONFIG=\"$HOME/.docker\" docker 'logout'\n" {
		t.Fatalf("Hub logout: %q %v", logout, err)
	}
	if cmd.VolumePruneSupported("27.0", "1.41") || !cmd.VolumePruneSupported("23.0", "1.42") {
		t.Fatal("incorrect API support gate")
	}
}

func TestDockerResourcesPodmanFormats(t *testing.T) {
	refs, err := DockerResourceRefs(`[{"name":"docs-net","id":"111"}]`, true)
	if err != nil || len(refs) != 1 || refs[0] != "111" {
		t.Fatalf("podman list: %v %v", refs, err)
	}
	networks, err := ParseDockerNetworkDetails(`[{"name":"docs-net","id":"111","driver":"bridge","ipv6_enabled":true,"subnets":[{"subnet":"2001:db8::/64","gateway":"2001:db8::1"}],"ipam_options":{"driver":"host-local"},"containers":{"abcd":{"name":"docs","interfaces":{"eth0":{"mac_address":"02:00:00:00:00:01","subnets":[{"ipnet":"2001:db8::2/64"}]}}}}}]`)
	if err != nil || len(networks) != 1 || !networks[0].IPv6 || networks[0].IPAMDriver != "host-local" || networks[0].Endpoints[0].IPv6 != "2001:db8::2/64" {
		t.Fatalf("podman inspect: %+v %v", networks, err)
	}
	disk, err := ParseDockerResourceDisk(`[{"type":"Images","total":2,"active":1,"size":1234,"reclaimable":512}]`)
	if err != nil || len(disk) != 1 || disk[0].Reclaimable != "512 B" {
		t.Fatalf("podman disk: %+v %v", disk, err)
	}
}

func TestDockerResourcesVolumeIdentity(t *testing.T) {
	cmd := DockerCmd{Tool: "docker"}
	rows, err := ParseDockerVolumeDetails(`[{"Name":"docs-data","Driver":"local","Mountpoint":"/example/it's-data","CreatedAt":"2026-01-02T03:04:05Z"}]`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("fixture: %+v %v", rows, err)
	}
	script, err := cmd.VolumeRemove(false, rows...)
	want := scriptHead + "set -e\nactual=$(docker 'volume' 'inspect' '--format' '{{.CreatedAt}}\n{{.Driver}}\n{{.Mountpoint}}' 'docs-data')\n[ \"$actual\" = '2026-01-02T03:04:05Z\nlocal\n/example/it'\\''s-data' ] || { printf '%s\\n' 'Volume changed since confirmation: docs-data'; exit 1; }\ndocker 'volume' 'rm' 'docs-data'\n"
	if err != nil || script != want {
		t.Fatalf("volume guard: %q %v", script, err)
	}
	rows[0].Created = time.Time{}
	if script, err := cmd.VolumeRemove(false, rows...); err == nil || script != "" {
		t.Fatal("removed a volume without a reliable creation time")
	}
}

func TestDockerSystemStreamAndPrivateMetadata(t *testing.T) {
	var events []DockerEvent
	report, flush := DockerEventLines(func(event DockerEvent) { events = append(events, event) })
	report("{\"Type\":\"container\",\"Act")
	report("ion\":\"start\"}\r\n" + strings.Repeat("x", 70<<10) + "\n" + `{"Type":"container","Status":"die","Name":"docs","Time":"2026-01-02T03:04:05Z"}`)
	flush()
	if len(events) != 2 || events[0].Action != "start" || events[1].Name != "docs" || events[1].Time.IsZero() {
		t.Fatalf("events: %+v", events)
	}
	if amount, ok := DockerReclaimed("Total reclaimed space: 512B\nTotal reclaimed space: 1MB\n"); !ok || amount != 1000512 {
		t.Fatalf("reclaimed %d, %v", amount, ok)
	}
	info, err := ParseDockerEngineInfo(`{"HttpProxy":"http://user:synthetic@proxy.example:8080/path?token=synthetic","HttpsProxy":"https://proxy.example"}`, `{}`)
	if err != nil || info.HTTPProxy != "http://proxy.example:8080" || info.HTTPSProxy != "https://proxy.example" {
		t.Fatalf("proxy fields: %+v %v", info, err)
	}
}
