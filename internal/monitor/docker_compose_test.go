package monitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestComposePageCommands(t *testing.T) {
	project := Compose{Project: "web_2", Dir: "/opt/a b's", Files: []string{"compose.yaml", "prod's.yml"}}
	cli := ComposeCLI{Engine: DockerCmd{Tool: "docker", Sudo: true}, Flavor: ComposeV2}
	head := "export LC_ALL=C\nexec 2>&1\ncd '/opt/a b'\\''s' || exit 1\n"
	base := "sudo -n docker 'compose' '-p' 'web_2' '-f' 'compose.yaml' '-f' 'prod'\\''s.yml' "
	for _, test := range []struct {
		action  ComposeAction
		options ComposeOptions
		want    string
	}{
		{ComposeUp, ComposeOptions{Build: true, PullAlways: true, Force: true, Orphans: true}, base + "'up' '-d' '--build' '--pull' 'always' '--force-recreate' '--remove-orphans'"},
		{ComposeStop, ComposeOptions{Services: []string{"web", "worker"}}, base + "'stop' 'web' 'worker'"},
		{ComposeStart, ComposeOptions{}, base + "'start'"},
		{ComposeRestart, ComposeOptions{}, base + "'restart'"},
		{ComposePull, ComposeOptions{Services: []string{"web"}}, base + "'pull' 'web'"},
		{ComposeRecreate, ComposeOptions{Services: []string{"web"}}, base + "'pull' 'web' && " + base + "'up' '-d' '--force-recreate' '--no-deps' 'web'"},
		{ComposeDown, ComposeOptions{Volumes: true, Images: "all", Orphans: true}, base + "'down' '-v' '--rmi' 'all' '--remove-orphans'"},
		{ComposeLogs, ComposeOptions{Follow: true, Tail: 40, Services: []string{"web"}}, base + "'logs' '--no-color' '--timestamps' '--tail' '40' '--follow' 'web'"},
		{ComposeValidate, ComposeOptions{}, base + "'config' '-q'"},
		{ComposePS, ComposeOptions{}, base + "'ps' '-a' '--format' 'json'"},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			got, err := cli.Script(project, test.action, test.options)
			if err != nil || got != head+test.want+"\n" {
				t.Fatalf("script = %q, %v; want %q", got, err, head+test.want+"\n")
			}
		})
	}
	for _, bad := range []Compose{
		{Project: "-x", Dir: project.Dir, Files: project.Files},
		{Project: "UPPER", Dir: project.Dir, Files: project.Files},
		{Project: "web", Dir: "/opt/a/../b", Files: project.Files},
		{Project: "web", Dir: project.Dir, Files: []string{"-"}},
		{Project: "web", Dir: project.Dir, Files: []string{"https://example.invalid/compose.yaml"}},
	} {
		if _, err := cli.Script(bad, ComposeDown, ComposeOptions{}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if got, err := cli.VolumeListScript("web_2"); err != nil || got != scriptHead+"sudo -n docker 'volume' 'ls' '--filter' 'label=com.docker.compose.project=web_2' '--format' '{{.Name}}'\n" {
		t.Fatalf("volume list: %q, %v", got, err)
	}
	if _, err := cli.Script(project, ComposePull, ComposeOptions{Services: []string{"--all"}}); err == nil {
		t.Error("accepted service option")
	}
	cli.Flavor = ComposeV1
	if got, err := cli.Script(project, ComposeUp, ComposeOptions{}); err != nil || !strings.Contains(got, "sudo -n docker-compose '-p' 'web_2'") {
		t.Fatalf("v1: %s %v", got, err)
	}
	if _, err := cli.Script(project, ComposeUp, ComposeOptions{PullAlways: true}); err == nil {
		t.Error("accepted unsupported v1 flag")
	}
	cli.Flavor, cli.Engine.Tool = ComposePodman, "podman"
	if got, err := cli.Script(project, ComposeValidate, ComposeOptions{}); err != nil || !strings.HasSuffix(got, "'config' >/dev/null\n") || !strings.Contains(got, "sudo -n podman-compose '-p'") {
		t.Fatalf("podman validate: %s %v", got, err)
	}
	if _, err := cli.Script(project, ComposeDown, ComposeOptions{Images: "all"}); err == nil {
		t.Error("accepted unsupported Podman flag")
	}
}

func TestComposePageDetect(t *testing.T) {
	ctx := context.Background()
	var commands []string
	run := func(_ context.Context, script string) (string, error) {
		commands = append(commands, script)
		if strings.Contains(script, "docker 'compose'") {
			return "no plugin", &DockerExit{Status: 1}
		}
		return "v1.29.2", nil
	}
	cli, err := DetectCompose(ctx, DockerCmd{Tool: "docker", Sudo: true}, run)
	if err != nil || cli.Flavor != ComposeV1 || cli.ListScript() != "" || !reflect.DeepEqual(commands, []string{
		scriptHead + "sudo -n docker 'compose' 'version'\n", scriptHead + "sudo -n docker-compose 'version'\n",
	}) {
		t.Fatalf("fallback: %+v, %v, %q", cli, err, commands)
	}
	commands = nil
	cli, err = DetectCompose(ctx, DockerCmd{Tool: "podman"}, run)
	if err != nil || cli.Flavor != ComposePodman || !reflect.DeepEqual(commands, []string{scriptHead + "podman-compose 'version'\n"}) {
		t.Fatalf("podman: %+v, %v, %q", cli, err, commands)
	}
	_, err = DetectCompose(ctx, DockerCmd{Tool: "docker"}, func(context.Context, string) (string, error) { return "", context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read error hidden: %v", err)
	}
}

func TestComposePageParse(t *testing.T) {
	projects, err := ParseComposeProjects(`[{"Name":"demo","Status":"running(3), exited(1)","ConfigFiles":"/opt/demo/compose.yaml,/opt/demo/prod.yml"}]`)
	if err != nil || len(projects) != 1 || projects[0].Dir != "/opt/demo" || projects[0].Running != 3 || projects[0].Total != 4 {
		t.Fatalf("projects: %+v, %v", projects, err)
	}
	labels := ComposeProjectsFromContainers([]Container{
		{ID: "123a", Name: "demo-web-1", Project: "demo", Service: "web", ComposeDir: "/srv/demo", ComposeFiles: "compose.yaml", Status: "running"},
		{ID: "123b", Name: "demo-job-1", Labels: map[string]string{"io.podman.compose.project": "demo", "com.docker.compose.service": "job"}, Status: "exited"},
	})
	merged := MergeComposeProjects(projects, labels)
	if len(merged) != 1 || merged[0].Dir != "/srv/demo" || len(merged[0].Services) != 2 || merged[0].Total != 4 {
		t.Fatalf("merged: %+v", merged)
	}
	line := `{"ID":"aaaa","Name":"demo-web-1","Project":"demo","Service":"web","State":"running","Health":"healthy","Image":"nginx:alpine","CreatedAt":"2026-05-01 10:01:02 +0000 UTC","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}`
	for _, input := range []string{line + "\n" + strings.ReplaceAll(line, "aaaa", "bbbb"), "[" + line + "," + strings.ReplaceAll(line, "aaaa", "bbbb") + "]"} {
		services, err := ParseComposeServices(input, "demo")
		if err != nil || len(services) != 2 || services[0].Health != "healthy" || services[0].Created.IsZero() || !reflect.DeepEqual(services[0].Ports, []PortBinding{{HostIP: "0.0.0.0", Port: 80, HostPort: 8080, Proto: "tcp"}}) {
			t.Fatalf("services: %+v, %v", services, err)
		}
	}
	if _, err := ParseComposeServices(`warning: no JSON`, "demo"); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}

func TestComposePageDirectoryValidation(t *testing.T) {
	for _, dir := range []string{"/", "/etc", "/a/../b", "/opt//compose", "relative", "/a/b\nxyz"} {
		if ValidComposeDirectory(dir) {
			t.Errorf("accepted %q", dir)
		}
	}
	if !ValidComposeDirectory("/opt/compose/a b's") || !ValidComposeProject("my_project-2") || ValidComposeProject("-invalid") {
		t.Fatal("invalid project/directory rules")
	}
}

func TestComposePageStreamTail(t *testing.T) {
	var tail []byte
	for _, part := range []string{"first", "second", "third", "super-long-block", "done"} {
		tail = appendComposeTail(tail, []byte(part), 10)
	}
	if string(tail) != "-blockdone" {
		t.Fatalf("tail = %q", tail)
	}
}
