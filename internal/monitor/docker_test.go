package monitor

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// dockerSample is a testdata file with LF line ends, as a server writes
// them, also in a checkout that made them CRLF.
func dockerSample(t *testing.T, name string) string {
	return strings.ReplaceAll(readSample(t, name), "\r\n", "\n")
}

func TestParseDockerStates(t *testing.T) {
	for _, tc := range []struct {
		file  string
		tool  string
		state DockerState
		error string // in Error
	}{
		{"docker-absent.txt", "", DockerOK, ""},
		{"docker-denied.txt", "docker", DockerDenied, "connect: permission denied"},
		{"docker-down.txt", "docker", DockerDown, "Is the docker daemon running?"},
		{"docker-empty.txt", "docker", DockerOK, ""},
	} {
		d := ParseDocker(dockerSample(t, tc.file))
		if d.Tool != tc.tool || d.State != tc.state || !strings.Contains(d.Error, tc.error) || (tc.error == "") != (d.Error == "") {
			t.Errorf("%s: tool %q, state %d, error %q", tc.file, d.Tool, d.State, d.Error)
		}
	}
	for _, tc := range []struct {
		msg   string
		state DockerState
	}{
		{"Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get http://%2Fvar%2Frun%2Fdocker.sock/v1.40/info: dial unix /var/run/docker.sock: connect: permission denied", DockerDenied},
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", DockerDown},
		{"Error: unable to connect to Podman socket: dial unix /run/user/1000/podman/podman.sock: connect: no such file or directory", DockerDown},
		{"error during connect: this error may indicate that the docker daemon is not running", DockerFailed},
		{"", DockerFailed},
	} {
		if got, _ := dockerError(tc.msg); got != tc.state {
			t.Errorf("dockerError(%q) = %d, want %d", tc.msg, got, tc.state)
		}
	}
}

func TestDockerPrivilegeRead(t *testing.T) {
	for _, test := range []struct {
		name, sample, tool string
		state              DockerState
		sudo, root         bool
	}{
		{"user", "docker-centos.txt", "docker", DockerOK, false, false},
		{"noninteractive", "docker-empty.txt", "docker", DockerOK, true, false},
		{"needs root", "docker-denied.txt", "docker", DockerNeedsRoot, false, false},
		{"root", "docker-centos.txt", "docker", DockerOK, false, true},
		{"root still denied", "docker-denied.txt", "docker", DockerDenied, false, true},
		{"rootless Podman", "docker-podman.txt", "podman", DockerOK, false, false},
		{"rootful Podman", "docker-podman.txt", "podman", DockerOK, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := dockerSample(t, test.sample)
			run := func(_ context.Context, script string) (string, string, error) {
				if test.root && (!strings.Contains(script, "d='"+test.tool+"'") || strings.Contains(script, "sudo -n")) {
					t.Fatal("privileged read must use the selected engine without nested sudo")
				}
				return sample, "", nil
			}
			var data Docker
			var err error
			if test.root {
				sample = strings.ReplaceAll(sample, "\n", "\r\n")
				data, err = ReadDockerRoot(t.Context(), test.tool, run)
			} else {
				data, err = ReadDockerWith(t.Context(), run)
			}
			if err != nil || data.State != test.state || data.Tool != test.tool || data.Sudo != test.sudo || data.Root != (test.root && test.state == DockerOK) {
				t.Fatalf("read: state %d, tool %q, sudo %t, root %t, error %v", data.State, data.Tool, data.Sudo, data.Root, err)
			}
		})
	}
	if _, err := ReadDockerRoot(t.Context(), "docker; exit", nil); err == nil {
		t.Fatal("invalid tool accepted")
	}
	rootful := "---FINALLYSHELL:tool---\npodman\n---FINALLYSHELL:info---\nError: cannot clone: Operation not permitted\nError: cannot re-exec process\n---FINALLYSHELL:status---\n125\n---FINALLYSHELL:end---\n"
	data, err := ReadDockerWith(t.Context(), func(context.Context, string) (string, string, error) { return rootful, "", nil })
	if err != nil || data.Tool != "podman" || data.State != DockerNeedsRoot {
		t.Fatalf("rootful Podman: %+v, %v", data, err)
	}
}

// A server with Docker and no container yet, read with sudo.
func TestParseDockerEmpty(t *testing.T) {
	d := ParseDocker(dockerSample(t, "docker-empty.txt"))
	want := Docker{
		Tool: "docker", Sudo: true, Version: "27.3.1", APIVersion: "1.47", OS: "Debian GNU/Linux 12 (bookworm)",
		StorageDriver: "overlay2", CgroupDriver: "systemd", CgroupVersion: "2", RootDir: "/var/lib/docker", LoggingDriver: "json-file",
		CPUs: 2, MemTotal: 4105691136,
		Networks: []DockerNetwork{
			{ID: "6a62b3f74e33", Name: "bridge", Driver: "bridge", Scope: "local"},
			{ID: "7ccf34f9877d", Name: "host", Driver: "host", Scope: "local"},
			{ID: "3b0e4f1a2c5d", Name: "none", Driver: "null", Scope: "local"},
		},
		Disk: []DockerDiskUsage{
			{Type: "Images", Total: "0", Active: "0", Size: "0B", Reclaimable: "0B"},
			{Type: "Containers", Total: "0", Active: "0", Size: "0B", Reclaimable: "0B"},
			{Type: "Local Volumes", Total: "0", Active: "0", Size: "0B", Reclaimable: "0B"},
			{Type: "Build Cache", Total: "0", Active: "0", Size: "0B", Reclaimable: "0B"},
		},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("got\n%+v\nwant\n%+v", d, want)
	}
}

func findContainer(t *testing.T, d Docker, name string) Container {
	t.Helper()
	for _, c := range d.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no container %q", name)
	return Container{}
}

// Docker 26 on CentOS 7: seven of the server's containers, their
// environment's values scrubbed.
func TestParseDockerCentOS(t *testing.T) {
	d := ParseDocker(dockerSample(t, "docker-centos.txt"))
	if d.State != DockerOK || d.Tool != "docker" || d.Sudo {
		t.Fatalf("state %d, tool %q, sudo %v, error %q", d.State, d.Tool, d.Sudo, d.Error)
	}
	if d.Version != "26.1.4" || d.APIVersion != "1.45" || d.OS != "CentOS Linux 7 (Core)" || d.StorageDriver != "overlay2" ||
		d.CgroupDriver != "cgroupfs" || d.CgroupVersion != "1" || d.RootDir != "/www/server/docker" || d.LoggingDriver != "json-file" ||
		d.CPUs != 12 || d.MemTotal != 57874083840 {
		t.Errorf("engine %+v", d)
	}
	if d.NumContainers != 64 || d.Running != 64 || d.Stopped != 0 || d.Paused != 0 || d.NumImages != 92 {
		t.Errorf("counts %d %d %d %d %d", d.NumContainers, d.Running, d.Stopped, d.Paused, d.NumImages)
	}
	if len(d.Warnings) != 2 || d.Warnings[0] != "WARNING: bridge-nf-call-iptables is disabled" {
		t.Errorf("warnings %q", d.Warnings)
	}
	if len(d.Containers) != 7 || len(d.Images) != 7 || len(d.Volumes) != 3 || len(d.Networks) != 5 || len(d.Disk) != 4 {
		t.Errorf("%d containers, %d images, %d volumes, %d networks, %d df", len(d.Containers), len(d.Images), len(d.Volumes), len(d.Networks), len(d.Disk))
	}

	// Compose, privileged, its port on 0.0.0.0 and ::.
	c := findContainer(t, d, "docker_sub_store")
	if c.ID != "0b3ba1e2c90129309ed28db5cabdfd3dea9d89b85ef6905c471678b879dd2334" || c.Image != "xream/sub-store:http-meta" ||
		c.ImageID != "sha256:b4663ea4a1ceafa8bdfa9a8732908a36b14ee94114f82a5278512204dac3be6f" ||
		c.Status != "running" || !c.Running || c.Health != "" || !c.Privileged || c.RestartPolicy != "always" ||
		c.Project != "docker_sub_store" || c.Service != "sub-store" || c.ComposeDir != "/home/docker-compose-files/SubStore" ||
		c.ComposeFiles != "/home/docker-compose-files/SubStore/docker-compose.yml" || c.WorkingDir != "/opt/app" ||
		c.Hostname != "0b3ba1e2c901" || c.LogDriver != "json-file" || c.NetworkMode != "docker_sub_store_default" {
		t.Errorf("sub store %+v", c)
	}
	if !c.Created.Equal(time.Date(2026, 11, 23, 20, 13, 10, 957500353, time.UTC)) || !c.StartedAt.Equal(time.Date(2026, 11, 23, 20, 13, 21, 593485433, time.UTC)) || !c.FinishedAt.IsZero() {
		t.Errorf("times %v %v %v", c.Created, c.StartedAt, c.FinishedAt)
	}
	if want := []string{"docker-entrypoint.sh"}; !reflect.DeepEqual(c.Entrypoint, want) || len(c.Command) != 3 || c.Command[0] != "/bin/sh" {
		t.Errorf("entrypoint %q, command %q", c.Entrypoint, c.Command)
	}
	if want := []PortBinding{{Port: 3001, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 13041, AlsoIPv6: true}}; !reflect.DeepEqual(c.Ports, want) || len(c.Exposed) != 0 {
		t.Errorf("ports %+v, exposed %q", c.Ports, c.Exposed)
	}
	if want := []ContainerNetwork{{Name: "docker_sub_store_default", IP: "172.0.5.2", PrefixLen: 24, Gateway: "172.0.5.1", MAC: "02:42:ac:00:05:02", Aliases: []string{"docker_sub_store", "sub-store"}}}; !reflect.DeepEqual(c.Networks, want) {
		t.Errorf("networks %+v", c.Networks)
	}
	if want := []Mount{
		{Type: "bind", Source: "/home/docker-compose-files/SubStore/sub-store-data", Destination: "/opt/app/data", Mode: "rw", RW: true},
		{Type: "bind", Source: "/etc/resolv.conf", Destination: "/etc/resolv.conf", Mode: "ro"},
	}; !reflect.DeepEqual(c.Mounts, want) {
		t.Errorf("mounts %+v", c.Mounts)
	}
	if want := (&ContainerStats{CPU: 0, MemUsed: 97706312, MemLimit: 57874684314, MemPercent: 0.17, NetRx: 36200000, NetTx: 16900000, BlockRead: 1040000, BlockWrite: 2000000000, PIDs: 18}); !reflect.DeepEqual(c.Stats, want) {
		t.Errorf("stats %+v", c.Stats)
	}
	if !strings.HasPrefix(string(c.Inspect), `{"Id":"0b3ba1e2c901`) || !strings.HasSuffix(string(c.Inspect), "}") {
		t.Errorf("inspect %.40q", c.Inspect)
	}
	if len(c.Env) != 7 || c.Env[3] != "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("env %q", c.Env)
	}

	// Healthy, in a project of three.
	c = findContainer(t, d, "docker_immich_postgres14")
	if c.Health != "healthy" || c.FailingStreak != 0 || c.HealthOutput != "/var/run/postgresql:5432 - accepting connections" || c.Project != "docker_immich" || c.Service != "postgres14" {
		t.Errorf("postgres %q %d %q %q %q", c.Health, c.FailingStreak, c.HealthOutput, c.Project, c.Service)
	}
	if want := []PortBinding{{Port: 5432, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 16810, AlsoIPv6: true}}; !reflect.DeepEqual(c.Ports, want) || len(c.Exposed) != 0 {
		t.Errorf("postgres ports %+v, exposed %q", c.Ports, c.Exposed)
	}
	project := 0
	for _, c := range d.Containers {
		if c.Project == "docker_immich" {
			project++
		}
	}
	if project != 3 {
		t.Errorf("%d in docker_immich", project)
	}

	// The host's network.
	c = findContainer(t, d, "docker_lucky")
	if c.NetworkMode != "host" || len(c.Networks) != 1 || c.Networks[0].Name != "host" || c.Networks[0].IP != "" || len(c.Ports) != 0 {
		t.Errorf("lucky %q %+v %+v", c.NetworkMode, c.Networks, c.Ports)
	}

	// A memory limit.
	c = findContainer(t, d, "docker_mysql_8035")
	if c.Memory != 4<<30 || c.Stats == nil || c.Stats.MemLimit != 4<<30 {
		t.Errorf("mysql memory %d, stats %+v", c.Memory, c.Stats)
	}

	// Not compose's.
	c = findContainer(t, d, "docker_gitea")
	if c.Project != "" || c.NetworkMode != "bridge" || len(c.Ports) != 2 || c.Ports[0].Port != 22 || c.Ports[1].Port != 3000 {
		t.Errorf("gitea %q %q %+v", c.Project, c.NetworkMode, c.Ports)
	}

	if im := d.Images[0]; im.ID != "8a9a743b6f96" || im.Repository != "gdy666/lucky" || im.Tag != "latest" || im.Size != 34_500_000 || im.Created != "12 months ago" {
		t.Errorf("image %+v", im)
	}
	if v := d.Volumes[0]; v.Driver != "local" || !strings.HasPrefix(v.Name, "32a8075a80a1") || !strings.HasSuffix(v.Mountpoint, "/_data") {
		t.Errorf("volume %+v", v)
	}
	if want := (DockerDiskUsage{Type: "Images", Total: "92", Active: "57", Size: "34.91GB", Reclaimable: "10.58GB (30%)"}); d.Disk[0] != want {
		t.Errorf("df %+v", d.Disk[0])
	}
}

// Podman 5 without docker: its info's own shape, a string Entrypoint,
// Healthcheck, an IPv6 port, a container that exited and one paused.
func TestParseDockerPodman(t *testing.T) {
	d := ParseDocker(dockerSample(t, "docker-podman.txt"))
	if d.State != DockerOK || d.Tool != "podman" || d.Version != "5.0.2" || d.OS != "fedora 40" || d.CPUs != 8 || d.MemTotal != 16624107520 ||
		d.StorageDriver != "overlay" || d.CgroupDriver != "systemd" || d.NumContainers != 3 || d.Running != 1 || d.Paused != 1 || d.Stopped != 1 || d.NumImages != 4 {
		t.Errorf("engine %+v", d)
	}
	web := findContainer(t, d, "web")
	if want := []string{"/docker-entrypoint.sh"}; !reflect.DeepEqual(web.Entrypoint, want) {
		t.Errorf("entrypoint %q", web.Entrypoint)
	}
	if web.Health != "unhealthy" || web.FailingStreak != 3 || web.HealthOutput != "curl: (7) Failed to connect to localhost port 80" ||
		web.Project != "site" || web.Service != "web" || web.RestartPolicy != "on-failure" || web.MaxRetries != 5 || web.RestartCount != 2 ||
		web.NanoCPUs != 1_500_000_000 || web.CPUShares != 512 || web.Memory != 512<<20 || web.PidsLimit != 2048 || !web.ReadOnly ||
		web.User != "nginx" || web.LogDriver != "journald" || web.LogOptions["tag"] != "web" || len(web.CapAdd) != 1 {
		t.Errorf("web %+v", web)
	}
	if want := []PortBinding{
		{Port: 53, Proto: "udp", HostIP: "::1", HostPort: 5353},
		{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8080, AlsoIPv6: true},
	}; !reflect.DeepEqual(web.Ports, want) {
		t.Errorf("ports %+v", web.Ports)
	}
	if want := []string{"443/tcp"}; !reflect.DeepEqual(web.Exposed, want) {
		t.Errorf("exposed %q", web.Exposed)
	}
	if n := web.Networks; len(n) != 1 || n[0].IP != "10.88.0.5" || n[0].PrefixLen != 16 || n[0].IPv6 != "fd00::5" || n[0].IPv6PrefixLen != 64 || n[0].IPv6Gateway != "fd00::1" {
		t.Errorf("networks %+v", n)
	}
	if m := web.Mounts; len(m) != 2 || m[0].Type != "volume" || m[0].Name != "site-data" || m[1].RW {
		t.Errorf("mounts %+v", m)
	}
	if st := web.Stats; st == nil || st.CPU != 12.5 || st.MemUsed != 24_300_000 || st.MemLimit != 536_900_000 || st.PIDs != 3 || st.NetRx != 1200 {
		t.Errorf("stats %+v", st)
	}

	// Stopped: the ports it is set to publish.
	w := findContainer(t, d, "worker")
	if w.Status != "exited" || w.ExitCode != 137 || !w.OOMKilled || w.Running || w.Stats != nil ||
		!w.FinishedAt.Equal(time.Date(2026, 11, 23, 21, 40, 10, 0, time.UTC)) || w.Entrypoint != nil {
		t.Errorf("worker %+v", w)
	}
	if want := []PortBinding{{Port: 9000, Proto: "tcp", HostIP: "127.0.0.1", HostPort: 9000}}; !reflect.DeepEqual(w.Ports, want) {
		t.Errorf("worker ports %+v", w.Ports)
	}
	if c := findContainer(t, d, "cache"); c.Status != "paused" || !c.Paused || c.NetworkMode != "host" || c.Stats == nil || c.Stats.CPU != 0 || c.Stats.PIDs != 0 {
		t.Errorf("cache %+v %+v", c, c.Stats)
	}
	if len(d.Images) != 2 || d.Images[1].Size != 1_040_000_000 {
		t.Errorf("images %+v", d.Images)
	}
	if len(d.Networks) != 1 || !d.Networks[0].IPv6 || d.Networks[0].Driver != "bridge" {
		t.Errorf("networks %+v", d.Networks)
	}
	if want := (DockerDiskUsage{Type: "Images", Total: "4", Active: "2", Size: "1.3GB", Reclaimable: "590MB (45%)"}); len(d.Disk) != 3 || d.Disk[0] != want {
		t.Errorf("df %+v", d.Disk)
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want uint64
	}{
		{"0B", 0}, {"512B", 512}, {"1.04MB", 1_040_000}, {"3.4 kB", 3400}, {"2GB", 2_000_000_000},
		{"93.18MiB", 97706312}, {"53.9GiB", 57874684314}, {"4GiB", 4 << 30}, {"1.5TB", 1_500_000_000_000},
		{"--", 0}, {"", 0}, {"12 parsecs", 0},
	} {
		if got := ParseSize(tc.s); got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// A container whose port docker lists on :: before 0.0.0.0, and ports
// of a range.
func TestMergePorts(t *testing.T) {
	got := mergePorts(map[string][]portJSON{
		"80/tcp":   {{HostIP: "::", HostPort: "8080"}, {HostIP: "0.0.0.0", HostPort: "8080"}},
		"81/tcp":   {{HostIP: "::", HostPort: "8081"}},
		"5000/udp": {{HostIP: "127.0.0.1", HostPort: "5000"}},
		"443":      {{HostPort: "443"}},
	})
	want := []PortBinding{
		{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8080, AlsoIPv6: true},
		{Port: 81, Proto: "tcp", HostIP: "::", HostPort: 8081},
		{Port: 443, Proto: "tcp", HostPort: 443},
		{Port: 5000, Proto: "udp", HostIP: "127.0.0.1", HostPort: 5000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// TestDockerLive reads the Docker of the server FINALLYSHELL_TEST_SSH
// names, which only reads: it lists, inspects and asks for stats.
//
//	FINALLYSHELL_TEST_SSH=root:secret@10.0.0.2:22 go test ./internal/monitor -run DockerLive -v
func TestDockerLive(t *testing.T) {
	client := dialTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	d, err := ReadDocker(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s in %v: %s %s, state %d, %d containers (%d running), %d images, %d volumes, %d networks",
		d.Tool, time.Since(start).Round(time.Millisecond), d.Tool, d.Version, d.State, len(d.Containers), d.Running, len(d.Images), len(d.Volumes), len(d.Networks))
	if d.Tool == "" || d.State != DockerOK {
		t.Skipf("no Docker to read: %q", d.Error)
	}
	if len(d.Containers) != d.NumContainers {
		t.Errorf("%d containers inspected, the engine has %d", len(d.Containers), d.NumContainers)
	}
	stats, ports, ips := 0, 0, 0
	for _, c := range d.Containers {
		if c.ID == "" || c.Name == "" || c.Image == "" || c.Status == "" || c.Created.IsZero() {
			t.Errorf("container %+v", c)
		}
		if c.Running && !c.Paused && c.Stats == nil {
			t.Errorf("%s runs without stats", c.Name)
		}
		if c.Stats != nil {
			stats++
		}
		ports += len(c.Ports)
		for _, n := range c.Networks {
			if n.IP != "" {
				ips++
			}
		}
	}
	t.Logf("%d with stats, %d published ports, %d addresses, %d df lines", stats, ports, ips, len(d.Disk))
}
