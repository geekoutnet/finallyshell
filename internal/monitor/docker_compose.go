package monitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type ComposeFlavor string

const (
	ComposeV2     ComposeFlavor = "docker compose"
	ComposeV1     ComposeFlavor = "docker-compose"
	ComposePodman ComposeFlavor = "podman-compose"
)

type ComposeCLI struct {
	Engine DockerCmd
	Flavor ComposeFlavor
}

type ComposeRun func(context.Context, string) (string, error)

func (cli ComposeCLI) line(args ...string) string {
	if cli.Flavor == ComposeV2 {
		return cli.Engine.Line(append([]string{"compose"}, args...)...)
	}
	words := []string{string(cli.Flavor)}
	if cli.Engine.Sudo {
		words = append([]string{"sudo", "-n"}, words...)
	}
	for _, arg := range args {
		words = append(words, dockerQuote(arg))
	}
	return strings.Join(words, " ")
}

func DetectCompose(ctx context.Context, engine DockerCmd, run ComposeRun) (ComposeCLI, error) {
	flavors := []ComposeFlavor{ComposeV2, ComposeV1}
	if engine.Tool == "podman" {
		flavors = []ComposeFlavor{ComposePodman}
	}
	for _, flavor := range flavors {
		cli := ComposeCLI{Engine: engine, Flavor: flavor}
		if _, err := run(ctx, scriptHead+cli.line("version")+"\n"); err == nil {
			return cli, nil
		} else {
			var exit *DockerExit
			if !errors.As(err, &exit) {
				return ComposeCLI{Engine: engine}, err
			}
		}
	}
	return ComposeCLI{Engine: engine}, nil
}

func (cli ComposeCLI) ListScript() string {
	if cli.Flavor != ComposeV2 {
		return ""
	}
	return scriptHead + cli.line("ls", "-a", "--format", "json") + "\n"
}

func (cli ComposeCLI) VolumeListScript(project string) (string, error) {
	if !ValidComposeProject(project) {
		return "", errors.New("invalid Compose project name")
	}
	return scriptHead + cli.Engine.Line("volume", "ls", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Name}}") + "\n", nil
}

type ComposeAction string

const (
	ComposeUp       ComposeAction = "up"
	ComposeStop     ComposeAction = "stop"
	ComposeStart    ComposeAction = "start"
	ComposeRestart  ComposeAction = "restart"
	ComposePull     ComposeAction = "pull"
	ComposeRecreate ComposeAction = "recreate"
	ComposeDown     ComposeAction = "down"
	ComposeLogs     ComposeAction = "logs"
	ComposeValidate ComposeAction = "validate"
	ComposePS       ComposeAction = "ps"
)

type ComposeOptions struct {
	Services                          []string
	Build, PullAlways, Force, Orphans bool
	Volumes, Follow                   bool
	Images                            string
	Tail                              int
}

var composeProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func ValidComposeProject(name string) bool { return composeProjectName.MatchString(name) }

func composePathText(value string) bool { return !strings.ContainsAny(value, "\x00\r\n") }

func ValidComposeDirectory(dir string) bool {
	return strings.HasPrefix(dir, "/") && strings.Count(dir, "/") >= 2 && path.Clean(dir) == dir && composePathText(dir)
}

func CheckComposeProject(project Compose) error {
	if err := checkCompose(project, nil); err != nil {
		return err
	}
	if !ValidComposeProject(project.Project) || !composePathText(project.Dir) || path.Clean(project.Dir) != project.Dir {
		return errors.New("invalid Compose project name or working directory")
	}
	for _, file := range project.Files {
		if !composePathText(file) || strings.Contains(file, "://") || strings.HasPrefix(file, "oci:") || strings.HasPrefix(file, "git@") {
			return fmt.Errorf("not a local Compose configuration file: %q", file)
		}
	}
	return nil
}

func (cli ComposeCLI) Script(project Compose, action ComposeAction, options ComposeOptions) (string, error) {
	if cli.Flavor != ComposeV2 && cli.Flavor != ComposeV1 && cli.Flavor != ComposePodman {
		return "", errors.New("Compose unavailable")
	}
	if err := CheckComposeProject(project); err != nil {
		return "", err
	}
	if len(options.Services) != 0 {
		if err := checkCompose(project, options.Services); err != nil {
			return "", err
		}
	}
	if options.PullAlways && cli.Flavor != ComposeV2 || cli.Flavor == ComposePodman && (options.Images != "" || options.Orphans) {
		return "", errors.New("option is not supported by this Compose CLI")
	}
	base := []string{"-p", project.Project}
	for _, file := range project.Files {
		base = append(base, "-f", file)
	}
	line := func(args ...string) string { return cli.line(append(slices.Clone(base), args...)...) }
	args, err := composeArgs(action, options)
	if err != nil {
		return "", err
	}
	command := line(append(args, options.Services...)...)
	if action == ComposeRecreate {
		command = line(append([]string{"pull"}, options.Services...)...) + " && " + command
	} else if action == ComposeValidate && cli.Flavor == ComposePodman {
		command = line("config") + " >/dev/null"
	} else if action == ComposeLogs && cli.Flavor == ComposePodman {
		command = line(append(slices.Delete(args, 1, 2), options.Services...)...)
	}
	return scriptHead + "cd " + dockerQuote(project.Dir) + " || exit 1\n" + command + "\n", nil
}

func composeArgs(action ComposeAction, options ComposeOptions) ([]string, error) {
	args := []string{string(action)}
	switch action {
	case ComposeUp, ComposeRecreate:
		args = []string{"up", "-d"}
		if options.Build {
			args = append(args, "--build")
		}
		if options.PullAlways {
			args = append(args, "--pull", "always")
		}
		if options.Force || action == ComposeRecreate {
			args = append(args, "--force-recreate")
		}
		if options.Orphans {
			args = append(args, "--remove-orphans")
		}
		if len(options.Services) != 0 {
			args = append(args, "--no-deps")
		}
	case ComposeStart, ComposeStop, ComposeRestart, ComposePull:
	case ComposeDown:
		if options.Volumes {
			args = append(args, "-v")
		}
		if options.Images != "" {
			if options.Images != "local" && options.Images != "all" {
				return nil, fmt.Errorf("invalid image removal option: %q", options.Images)
			}
			args = append(args, "--rmi", options.Images)
		}
		if options.Orphans {
			args = append(args, "--remove-orphans")
		}
	case ComposeLogs:
		args = append(args, "--no-color", "--timestamps", "--tail", strconv.Itoa(max(1, min(options.Tail, 10000))))
		if options.Follow {
			args = append(args, "--follow")
		}
	case ComposeValidate:
		args = []string{"config", "-q"}
	case ComposePS:
		args = append(args, "-a", "--format", "json")
	default:
		return nil, fmt.Errorf("unknown Compose action: %q", action)
	}
	if len(options.Services) != 0 && (action == ComposeDown || action == ComposeValidate || action == ComposePS) {
		return nil, errors.New("action requires the whole Compose project")
	}
	return args, nil
}
