package monitor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDockerLiveCompose(t *testing.T) {
	box := openDockerLiveBox(t)
	const plugin = "/usr/local/lib/docker/cli-plugins/docker-compose"
	for _, flavor := range []ComposeFlavor{ComposeV2, ComposeV1} {
		t.Run(string(flavor), func(t *testing.T) {
			if flavor == ComposeV1 {
				stash := box.dir + "/docker-compose"
				box.raw(t, "test -x "+plugin+" && mv -- "+plugin+" "+stash)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if out, err := RunDocker(ctx, box.client, "mv -- "+stash+" "+plugin); err != nil {
						t.Errorf("restore compose v2: %v: %s", err, out)
					}
				})
			}
			cli, err := DetectCompose(box.ctx, box.cmd, func(ctx context.Context, script string) (string, error) {
				return RunDocker(ctx, box.client, script)
			})
			if err != nil || cli.Flavor != flavor {
				t.Fatalf("detect: %+v, %v", cli, err)
			}
			dir := box.dir + "/" + box.name
			file := dir + "/compose.yaml"
			project := Compose{Dir: dir, Project: box.name, Files: []string{file}}
			yaml := `services:
  worker:
    image: alpine:3.20
    command: [sh, -c, "echo fslive-compose; trap 'exit 0' TERM; while :; do sleep 1; done"]
    volumes: [data:/data]
  side:
    image: alpine:3.20
    command: [sh, -c, "trap 'exit 0' TERM; while :; do sleep 1; done"]
volumes:
  data: {}
`
			box.raw(t, "mkdir -p -- "+dir+"; printf %s "+dockerQuote(yaml)+" > "+file)
			down := dockerLiveScript(cli.Script(project, ComposeDown, ComposeOptions{Volumes: true}))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if out, err := RunDocker(ctx, box.client, down); err != nil {
					t.Errorf("compose cleanup: %v: %s", err, out)
				}
			})
			run := func(action ComposeAction, options ComposeOptions) string {
				t.Helper()
				var progress strings.Builder
				out, err := RunComposeStream(box.ctx, box.client, dockerLiveScript(cli.Script(project, action, options)), func(chunk string) { progress.WriteString(chunk) })
				if err != nil {
					t.Fatalf("%s: %v: %s", action, err, out)
				}
				if action == ComposeLogs && !strings.Contains(progress.String(), "fslive-compose") {
					t.Errorf("Compose logs did not stream: %q", out)
				}
				return out
			}
			services := func() []Container {
				t.Helper()
				docker, err := ReadDocker(box.ctx, box.client)
				if err != nil {
					t.Fatal(err)
				}
				list := ComposeProjectsFromContainers(docker.Containers)
				if flavor == ComposeV2 {
					projects, err := ParseComposeProjects(box.raw(t, cli.ListScript()))
					if err != nil || !slices.ContainsFunc(projects, func(found ComposeProject) bool { return found.Project == box.name }) {
						t.Fatalf("projects: %+v, %v", projects, err)
					}
					list = MergeComposeProjects(projects, list)
					rows, err := ParseComposeServices(box.raw(t, dockerLiveScript(cli.Script(project, ComposePS, ComposeOptions{}))), box.name)
					if err != nil || len(rows) != 2 {
						t.Fatalf("ps: %+v, %v", rows, err)
					}
				}
				for _, found := range list {
					if found.Project == box.name && len(found.Services) == 2 && found.Dir == dir {
						return found.Services
					}
				}
				t.Fatalf("project services not found: %+v", list)
				return nil
			}
			run(ComposeValidate, ComposeOptions{})
			run(ComposeUp, ComposeOptions{})
			old := services()
			run(ComposeStop, ComposeOptions{})
			for _, ct := range services() {
				if ct.Running {
					t.Fatalf("%s still running", ct.Name)
				}
			}
			run(ComposeStart, ComposeOptions{})
			run(ComposeRestart, ComposeOptions{})
			run(ComposeLogs, ComposeOptions{Services: []string{"worker"}, Tail: 10})
			run(ComposePull, ComposeOptions{Services: []string{"worker"}})
			run(ComposeRecreate, ComposeOptions{Services: []string{"worker"}})
			for _, ct := range services() {
				index := slices.IndexFunc(old, func(before Container) bool { return before.Service == ct.Service })
				if index < 0 {
					t.Fatalf("unexpected service after update: %s", ct.Service)
				}
				prior := old[index]
				if !ct.Running || (ct.ID != prior.ID) != (ct.Service == "worker") {
					t.Fatalf("unexpected update: %s unchanged=%v running=%v", ct.Service, ct.ID == prior.ID, ct.Running)
				}
			}
			box.contains(t, dockerLiveScript(cli.VolumeListScript(box.name)), box.name+"_data")
			run(ComposeDown, ComposeOptions{Volumes: true})
			box.raw(t, "test -z \"$(docker ps -aq --filter label=com.docker.compose.project="+box.name+")\" && ! docker volume inspect "+box.name+"_data >/dev/null 2>&1")
		})
	}
}
