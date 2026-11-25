package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func (box dockerLiveBox) network(t *testing.T, name string) DockerNetworkDetails {
	t.Helper()
	rows, err := ParseDockerNetworkDetails(box.raw(t, dockerLiveScript(box.cmd.ResourceInspect("network", []string{name}))))
	if err != nil || len(rows) != 1 || rows[0].Name != name {
		t.Fatalf("network: %+v, %v", rows, err)
	}
	return rows[0]
}

func (box dockerLiveBox) volume(t *testing.T, name string) DockerVolumeDetails {
	t.Helper()
	rows, err := ParseDockerVolumeDetails(box.raw(t, dockerLiveScript(box.cmd.ResourceInspect("volume", []string{name}))))
	if err != nil || len(rows) != 1 || rows[0].Name != name {
		t.Fatalf("volume: %+v, %v", rows, err)
	}
	return rows[0]
}

func (box dockerLiveBox) prune(t *testing.T, script string) {
	t.Helper()
	script = strings.TrimSuffix(script, "\n") + " '--filter' 'label=fslive=" + box.name + "'\n"
	box.raw(t, script)
}

func TestDockerLiveResources(t *testing.T) {
	box := openDockerLiveBox(t)
	cmd, name := box.cmd, box.name
	label := "fslive=" + name
	engine, err := ParseDockerEngineInfo(box.raw(t, cmd.EngineInfo()), box.raw(t, cmd.EngineVersion()))
	if err != nil || engine.Server.Version == "" || engine.RootDir == "" || engine.Containers == 0 {
		t.Fatalf("engine: %+v, %v", engine, err)
	}
	disk, err := ParseDockerResourceDisk(box.raw(t, cmd.ResourceDisk(false)))
	if err != nil || len(disk) < 3 {
		t.Fatalf("summary: %+v, %v", disk, err)
	}
	t.Run("networks", func(t *testing.T) {
		const ip = "10.243.218.12"
		spec := DockerNetworkSpec{Name: name, Driver: "bridge", Labels: label, IPv4: DockerSubnet{Subnet: "10.243.218.0/24", Gateway: "10.243.218.1"}}
		box.raw(t, dockerLiveScript(cmd.NetworkCreate(spec)))
		rows, err := DockerResourceRefs(box.raw(t, cmd.NetworkList()), true)
		if net := box.network(t, name); net.Driver != "bridge" || !slices.Contains(net.Subnets, spec.IPv4) || err != nil || !slices.Contains(rows, net.ID) {
			t.Fatalf("network listing: %+v, %v, %v", net, rows, err)
		}
		box.raw(t, dockerLiveScript(cmd.CtrRunScript(CtrConfig{Name: name, Image: "alpine:3.20", Labels: []string{label}, Command: []string{"sleep", "3600"}})))
		ct := box.container(t, name)
		box.raw(t, dockerLiveScript(cmd.NetworkConnect(name, ct.ID, ip, "fslive-first, fslive-second")))
		ct = box.container(t, name)
		if !slices.ContainsFunc(ct.Networks, func(net ContainerNetwork) bool {
			return net.Name == name && net.IP == ip && slices.Contains(net.Aliases, "fslive-first") && slices.Contains(net.Aliases, "fslive-second")
		}) || len(box.network(t, name).Endpoints) != 1 {
			t.Fatal("network attachment did not keep IP and aliases")
		}
		box.raw(t, dockerLiveScript(cmd.NetworkDisconnect(name, ct.ID, false)))
		if net := box.network(t, name); len(net.Endpoints) != 0 {
			t.Fatalf("network still connected: %+v", net.Endpoints)
		}
		box.raw(t, dockerLiveScript(cmd.NetworkRemove(box.network(t, name))))
		box.raw(t, dockerLiveScript(cmd.NetworkCreate(spec)))
		box.prune(t, dockerLiveScript(cmd.ResourcePrune("network", false, engine.Server.Version)))
		box.raw(t, "! docker network inspect "+name+" >/dev/null 2>&1")
		box.raw(t, dockerLiveScript(cmd.OpScript(OpKill, ct.ID)))
		box.prune(t, dockerLiveScript(cmd.ResourcePrune("container", false, engine.Server.Version)))
		box.raw(t, "! docker inspect "+ct.ID+" >/dev/null 2>&1")
	})
	t.Run("volumes", func(t *testing.T) {
		box.raw(t, dockerLiveScript(cmd.VolumeCreate(name, "local", "", label)))
		vol := box.volume(t, name)
		listed, err := DockerResourceRefs(box.raw(t, cmd.VolumeList()), false)
		if err != nil || !slices.Contains(listed, name) || vol.Labels["fslive"] != name {
			t.Fatalf("volume list: %+v, %v", listed, err)
		}
		box.raw(t, "printf fslive > "+dockerQuote(vol.Mountpoint+"/fslive"))
		usage, err := ParseDockerVolumeUsage(box.raw(t, cmd.ResourceDisk(true)))
		if err != nil || usage[name].Links != 0 || ParseSize(usage[name].Size) < 6 {
			t.Fatalf("volume usage: %+v, %v", usage[name], err)
		}
		if strings.HasPrefix(engine.Server.Version, "20.10.") {
			if cmd.VolumePruneSupported(engine.Server.Version, engine.Server.APIVersion) {
				t.Fatal("legacy prune can remove named volumes without --all")
			}
			for _, all := range []bool{false, true} {
				if _, err := cmd.ResourcePrune("volume", all, engine.Server.Version); !errors.Is(err, ErrDockerResourceUnsupported) {
					t.Fatalf("unsafe 20.10 prune accepted (all=%v): %v", all, err)
				}
			}
		}
		box.raw(t, dockerLiveScript(cmd.VolumeRemove(false, vol)))
		box.raw(t, "! docker volume inspect "+name+" >/dev/null 2>&1")
		if strings.HasPrefix(engine.Server.Version, "20.10.") {
			prune := name + "prune"
			box.raw(t, dockerLiveScript(cmd.VolumeCreate(prune, "local", "", label)))
			box.prune(t, cmd.imageScript("volume", "prune", "--force"))
			box.raw(t, "! docker volume inspect "+prune+" >/dev/null 2>&1")
			box.volume(t, "fsvol")
		}
	})
	t.Run("empty-builder-cleanup", func(t *testing.T) {
		var usage struct{ BuildCache []json.RawMessage }
		if err := json.Unmarshal([]byte(box.raw(t, cmd.ResourceDisk(true))), &usage); err != nil || len(usage.BuildCache) != 0 {
			t.Skip("cannot guarantee that builder prune would leave other resources alone")
		}
		if _, ok := DockerReclaimed(box.raw(t, dockerLiveScript(cmd.ResourcePrune("builder", false, engine.Server.Version)))); !ok {
			t.Fatal("builder cleanup did not report reclaimed space")
		}
	})
	t.Run("events-and-registries", func(t *testing.T) {
		if _, err := ParseDockerRegistries(box.raw(t, cmd.RegistryList())); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(box.ctx, 15*time.Second)
		defer cancel()
		matched := make(chan DockerEvent, 1)
		report, flush := DockerEventLines(func(event DockerEvent) {
			if event.ID == name && event.Type == "volume" && event.Action == "create" {
				select {
				case matched <- event:
				default:
				}
			}
		})
		done := make(chan error, 1)
		script := cmd.Events(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
		pidFile := box.dir + "/events.pid"
		script = strings.Replace(script, "engine=$!\n", "engine=$!\nprintf '%s\\n' \"$engine\" > "+pidFile+"\n", 1)
		go func() {
			_, err := StreamDockerEvents(ctx, box.client, script, report)
			flush()
			done <- err
		}()
		box.raw(t, dockerLiveScript(cmd.VolumeCreate(name, "local", "", label)))
		select {
		case event := <-matched:
			if event.ID == "" || event.Time.IsZero() {
				t.Errorf("invalid event: %+v", event)
			}
		case err := <-done:
			t.Fatalf("events stream stopped: %v", err)
		case <-ctx.Done():
			t.Fatal("volume event did not arrive")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel stream: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel did not stop the events stream")
		}
		box.raw(t, "test -s "+pidFile+" && ! kill -0 \"$(cat "+pidFile+")\" 2>/dev/null")
	})
}
