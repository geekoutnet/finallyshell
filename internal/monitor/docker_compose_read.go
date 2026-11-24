package monitor

import (
	"cmp"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ComposeProject struct {
	Compose
	Running, Total int
	Services       []Container
}

var composeStatusCount = regexp.MustCompile(`([a-z]+)\(([0-9]+)\)`)

func ParseComposeProjects(output string) ([]ComposeProject, error) {
	type row struct{ Name, Status, ConfigFiles string }
	rows, err := composeJSON[row](output)
	if err != nil {
		return nil, err
	}
	var projects []ComposeProject
	for _, row := range rows {
		if !ValidComposeProject(row.Name) {
			continue
		}
		project := ComposeProject{Compose: Compose{Project: row.Name}}
		for file := range strings.SplitSeq(row.ConfigFiles, ",") {
			if file = strings.TrimSpace(file); file != "" {
				project.Files = append(project.Files, file)
			}
		}
		if len(project.Files) != 0 && path.IsAbs(project.Files[0]) {
			project.Dir = path.Dir(project.Files[0])
		}
		for _, match := range composeStatusCount.FindAllStringSubmatch(row.Status, -1) {
			count, _ := strconv.ParseUint(match[2], 10, 31)
			project.Total += int(count)
			if match[1] == "running" {
				project.Running += int(count)
			}
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func ParseComposeServices(output, project string) ([]Container, error) {
	if strings.TrimSpace(output) == "" {
		return []Container{}, nil
	}
	type publisher struct {
		URL, Protocol             string
		TargetPort, PublishedPort int
	}
	type row struct {
		ID, Name, Project, Service, State, Health, Image, CreatedAt string
		Created                                                     int64
		Publishers                                                  []publisher
	}
	rows, err := composeJSON[row](output)
	if err != nil {
		return nil, err
	}
	services := make([]Container, 0, len(rows))
	for _, row := range rows {
		if row.Project != "" && row.Project != project || !ValidName(row.Service) || !ValidName(row.ID) {
			continue
		}
		ct := Container{ID: row.ID, Name: strings.TrimPrefix(row.Name, "/"), Project: project, Service: row.Service,
			Status: row.State, Health: row.Health, Image: row.Image, Running: row.State == "running", Paused: row.State == "paused"}
		for _, format := range []string{time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST"} {
			if created, err := time.Parse(format, row.CreatedAt); err == nil {
				ct.Created = created
				break
			}
		}
		if ct.Created.IsZero() && row.Created > 0 {
			ct.Created = time.Unix(row.Created, 0)
		}
		for _, binding := range row.Publishers {
			if binding.PublishedPort > 0 {
				ct.Ports = append(ct.Ports, PortBinding{HostIP: binding.URL, HostPort: binding.PublishedPort, Port: binding.TargetPort, Proto: binding.Protocol})
			} else if binding.TargetPort > 0 {
				ct.Exposed = append(ct.Exposed, strconv.Itoa(binding.TargetPort)+"/"+binding.Protocol)
			}
		}
		services = append(services, ct)
	}
	return services, nil
}

func composeJSON[T any](output string) ([]T, error) {
	var rows []T
	for rest := strings.TrimSpace(output); rest != ""; rest = strings.TrimSpace(rest) {
		if rest[0] != '{' && rest[0] != '[' {
			_, rest, _ = strings.Cut(rest, "\n")
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(rest))
		if rest[0] == '[' {
			var batch []T
			if err := decoder.Decode(&batch); err != nil {
				return nil, err
			}
			rows = append(rows, batch...)
			if rows == nil {
				rows = []T{}
			}
		} else {
			var row T
			if err := decoder.Decode(&row); err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		rest = rest[decoder.InputOffset():]
	}
	if rows == nil {
		return nil, errors.New("Compose did not return JSON")
	}
	return rows, nil
}

func ComposeProjectsFromContainers(containers []Container) []ComposeProject {
	projects := make(map[string]ComposeProject)
	for _, ct := range containers {
		first := func(values ...string) string {
			for _, value := range values {
				if value != "" {
					return value
				}
			}
			return ""
		}
		ct.Project = first(ct.Project, ct.Labels["com.docker.compose.project"], ct.Labels["io.podman.compose.project"])
		if !ValidComposeProject(ct.Project) {
			continue
		}
		ct.Service = first(ct.Service, ct.Labels["com.docker.compose.service"], ct.Labels["io.podman.compose.service"])
		ct.ComposeDir = first(ct.ComposeDir, ct.Labels["com.docker.compose.project.working_dir"], ct.Labels["io.podman.compose.project.working_dir"])
		ct.ComposeFiles = first(ct.ComposeFiles, ct.Labels["com.docker.compose.project.config_files"], ct.Labels["io.podman.compose.project.config_files"])
		project := projects[ct.Project]
		metadata := ComposeOf(&ct)
		if project.Project == "" || CheckComposeProject(project.Compose) != nil && CheckComposeProject(metadata) == nil {
			project.Compose = metadata
		}
		project.Total++
		if ct.Status == "running" && !ct.Paused {
			project.Running++
		}
		project.Services = append(project.Services, ct)
		projects[ct.Project] = project
	}
	return mapComposeProjects(projects)
}

func mapComposeProjects(projects map[string]ComposeProject) []ComposeProject {
	var ordered []ComposeProject
	for _, project := range projects {
		ordered = append(ordered, project)
	}
	slices.SortFunc(ordered, func(left, right ComposeProject) int { return cmp.Compare(left.Project, right.Project) })
	return ordered
}

func MergeComposeProjects(list, labels []ComposeProject) []ComposeProject {
	projects := make(map[string]ComposeProject)
	for _, project := range list {
		projects[project.Project] = project
	}
	for _, label := range labels {
		project, exists := projects[label.Project]
		if !exists {
			projects[label.Project] = label
			continue
		}
		if CheckComposeProject(label.Compose) == nil {
			project.Compose = label.Compose
		}
		project.Running, project.Total = max(project.Running, label.Running), max(project.Total, label.Total)
		project.Services = label.Services
		projects[label.Project] = project
	}
	return mapComposeProjects(projects)
}
