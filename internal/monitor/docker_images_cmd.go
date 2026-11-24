package monitor

import (
	"errors"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrImageReference = errors.New("invalid image reference")
	ErrImagePlatform  = errors.New("invalid image platform")
	ErrImagePath      = errors.New("invalid absolute server path")
	ErrImageBuildArg  = errors.New("invalid build argument")
	ErrImageTarget    = errors.New("invalid build stage")
	ErrImageForce     = errors.New("Podman force removal can delete containers")
	imageIDPattern    = regexp.MustCompile(`^(sha256:[0-9a-f]{64}|[0-9a-f]{12,64})$`)
	imageDigest       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	imageComponent    = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	imageTag          = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
	imageDomain       = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)
	imagePlatform     = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9][a-z0-9_-]*(?:/[a-z0-9][a-z0-9_.-]*)?$`)
	imageArgKey       = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	imageStage        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

func ValidImageID(value string) bool { return imageIDPattern.MatchString(value) }

func imageRegistry(host string) bool {
	name := host
	if strings.HasPrefix(host, "[") || strings.Contains(host, ":") {
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			return net.ParseIP(host[1:len(host)-1]) != nil && strings.Contains(host, ":")
		}
		var port string
		var err error
		name, port, err = net.SplitHostPort(host)
		value, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || portErr != nil || value == 0 {
			return false
		}
		if strings.HasPrefix(host, "[") {
			return net.ParseIP(name) != nil && strings.Contains(name, ":")
		}
	}
	return imageDomain.MatchString(name)
}

func imageReference(ref string) (tag, digest string, valid bool) {
	name, digest, hasDigest := strings.Cut(ref, "@")
	if hasDigest && !imageDigest.MatchString(digest) {
		return "", "", false
	}
	if colon := strings.LastIndexByte(name, ':'); colon > strings.LastIndexByte(name, '/') {
		name, tag = name[:colon], name[colon+1:]
		if !imageTag.MatchString(tag) {
			return "", "", false
		}
	}
	if len(name) > 255 || imageIDPattern.MatchString(ref) && len(ref) == 64 {
		return "", "", false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 && (parts[0] == "localhost" || strings.ContainsAny(parts[0], ".:") || strings.ToLower(parts[0]) != parts[0]) {
		if !imageRegistry(parts[0]) {
			return "", "", false
		}
		parts = parts[1:]
	}
	for _, part := range parts {
		if !imageComponent.MatchString(part) {
			return "", "", false
		}
	}
	return tag, digest, true
}

func ValidImageReference(ref string) bool {
	_, _, valid := imageReference(ref)
	return valid
}

func ValidImageTag(ref string) bool {
	tag, digest, valid := imageReference(ref)
	return valid && tag != "" && digest == ""
}

func imageOperands(refs []string) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if !ValidImageID(ref) && !ValidImageReference(ref) {
			return false
		}
	}
	return true
}

func validImagePath(value string) bool {
	return strings.HasPrefix(value, "/") && strings.IndexFunc(value, unicode.IsControl) == -1
}

func (c DockerCmd) imageScript(args ...string) string { return scriptHead + c.Line(args...) + "\n" }

func (c DockerCmd) imageExec(args ...string) string {
	return scriptHead + "exec " + c.Line(args...) + "\n"
}

func (c DockerCmd) ImageList() string {
	return c.imageList(false)
}

func (c DockerCmd) ImageDangling() string {
	return c.imageList(true)
}

func (c DockerCmd) imageList(dangling bool) string {
	format := "{{json .}}"
	if c.Tool == "podman" {
		format = "json"
	}
	args := []string{"image", "ls", "-a", "--no-trunc", "--format", format}
	if dangling {
		args = append(args, "--filter", "dangling=true")
	}
	return c.imageScript(args...)
}

func (c DockerCmd) ImageInspect(ref string) (string, error) {
	if !imageOperands([]string{ref}) {
		return "", ErrImageReference
	}
	return c.imageScript("image", "inspect", "--", ref), nil
}

func (c DockerCmd) ImageGuard(rows []ImageRow) (string, error) {
	if len(rows) == 0 {
		return "", ErrImageReference
	}
	var script strings.Builder
	script.WriteString(scriptHead)
	for _, row := range rows {
		if !ValidImageID(row.ID) || !imageOperands([]string{row.Ref}) {
			return "", ErrImageReference
		}
		script.WriteString("actual=$(" + c.Line("image", "inspect", "--format", "{{.Id}}", "--", row.Ref) + ") || exit $?\n")
		id := ImageIDKey(row.ID)
		script.WriteString(`case "${actual#sha256:}" in ` + dockerQuote(id))
		if len(id) < 64 {
			script.WriteByte('*')
		}
		script.WriteString(") ;; *) printf '%s\\n' " + dockerQuote("Image reference changed: "+row.Ref) + "; exit 1 ;; esac\n")
	}
	return script.String(), nil
}

func (c DockerCmd) ImagePlatforms(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", ErrImageReference
	}
	for _, id := range ids {
		if !ValidImageID(id) {
			return "", ErrImageReference
		}
	}
	return c.imageScript(append([]string{"image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", "--"}, ids...)...), nil
}

func (c DockerCmd) ImageHistory(ref string) (string, error) {
	if !imageOperands([]string{ref}) {
		return "", ErrImageReference
	}
	format := "{{json .}}"
	if c.Tool == "podman" {
		format = "json"
	}
	return c.imageScript("history", "--no-trunc", "--format", format, "--", ref), nil
}

func (c DockerCmd) ImageSearch(term string) (string, error) {
	if c.Tool == "podman" || len(term) > 100 || !ValidImageReference(term) || strings.ContainsAny(term, ":@") {
		return "", ErrImageReference
	}
	return c.imageScript("search", "--format", "{{json .}}", "--limit", "15", "--", term), nil
}

func (c DockerCmd) ImagePull(ref, platform string, all bool) (string, error) {
	tag, digest, valid := imageReference(ref)
	if !valid || all && (tag != "" || digest != "") {
		return "", ErrImageReference
	}
	args := []string{"image", "pull"}
	if platform != "" {
		if !imagePlatform.MatchString(platform) {
			return "", ErrImagePlatform
		}
		args = append(args, "--platform", platform)
	}
	if all {
		args = append(args, "--all-tags")
	}
	return c.imageExec(append(args, "--", ref)...), nil
}

func (c DockerCmd) ImageTag(from, to string) (string, error) {
	if !imageOperands([]string{from}) || !ValidImageTag(to) {
		return "", ErrImageReference
	}
	return c.imageScript("image", "tag", "--", from, to), nil
}

func (c DockerCmd) ImagePush(ref string) (string, error) {
	if !ValidImageTag(ref) {
		return "", ErrImageReference
	}
	return c.imageExec("image", "push", "--", ref), nil
}

func (c DockerCmd) ImageRemove(force bool, refs ...string) (string, error) {
	if !imageOperands(refs) {
		return "", ErrImageReference
	}
	args := []string{"image", "rm", "--no-prune"}
	if force {
		if c.Tool == "podman" {
			return "", ErrImageForce
		}
		args = append(args, "--force")
	}
	return c.imageScript(append(append(args, "--"), refs...)...), nil
}

func (c DockerCmd) ImageLoad(file string) (string, error) {
	if !validImagePath(file) || strings.HasSuffix(file, "/") {
		return "", ErrImagePath
	}
	return c.imageExec("image", "load", "-i", file), nil
}

func (c DockerCmd) ImageImport(file, tag string) (string, error) {
	if !validImagePath(file) || strings.HasSuffix(file, "/") {
		return "", ErrImagePath
	}
	if !ValidImageTag(tag) {
		return "", ErrImageReference
	}
	return c.imageExec("image", "import", "--", file, tag), nil
}

func (c DockerCmd) ImageSave(file string, gzip bool, refs ...string) (string, error) {
	if !validImagePath(file) || strings.HasSuffix(file, "/") || path.Base(file) == "." || path.Base(file) == ".." {
		return "", ErrImagePath
	}
	if !imageOperands(refs) {
		return "", ErrImageReference
	}
	lines := []string{scriptHead + "set -e", `umask 077`, "temp=$(mktemp -d -- " + dockerQuote(path.Join(path.Dir(file), ".docker-image.XXXXXXXX")) + ")",
		`trap 'rm -f -- "$temp/archive.tar" "$temp/archive.tar.gz"; rmdir -- "$temp"' EXIT`, `trap 'exit 1' HUP INT TERM`}
	args := append([]string{"--"}, refs...)
	if c.Sudo || gzip {
		lines = append(lines, c.Line(append([]string{"save"}, args...)...)+` > "$temp/archive.tar"`)
	} else {
		lines = append(lines, c.Line("save", "-o")+` "$temp/archive.tar" `+strings.TrimPrefix(c.Line(args...), c.prefix()+" "))
	}
	archive := `"$temp/archive.tar"`
	if gzip {
		lines = append(lines, `gzip -c "$temp/archive.tar" > "$temp/archive.tar.gz"`)
		archive = `"$temp/archive.tar.gz"`
	}
	lines = append(lines, "ln -T -- "+archive+" "+dockerQuote(file))
	return strings.Join(lines, "\n") + "\n", nil
}

type ImageBuild struct {
	Context, Dockerfile, Target, Platform string
	Tags, Args                            []string
	NoCache, Pull                         bool
}

func (c DockerCmd) ImageBuild(build ImageBuild) (string, error) {
	if build.Dockerfile == "" {
		build.Dockerfile = path.Join(build.Context, "Dockerfile")
	} else if !strings.HasPrefix(build.Dockerfile, "/") {
		return "", ErrImagePath
	}
	if !validImagePath(build.Context) || !validImagePath(build.Dockerfile) || strings.HasSuffix(build.Dockerfile, "/") {
		return "", ErrImagePath
	}
	args := []string{"build"}
	args = append(args, "-f", build.Dockerfile)
	for _, tag := range build.Tags {
		if !ValidImageTag(tag) {
			return "", ErrImageReference
		}
		args = append(args, "-t", tag)
	}
	for _, arg := range build.Args {
		key, value, found := strings.Cut(arg, "=")
		if !found || !imageArgKey.MatchString(key) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", ErrImageBuildArg
		}
		args = append(args, "--build-arg", arg)
	}
	if build.Target != "" {
		if !imageStage.MatchString(build.Target) {
			return "", ErrImageTarget
		}
		args = append(args, "--target", build.Target)
	}
	if build.Platform != "" {
		if !imagePlatform.MatchString(build.Platform) {
			return "", ErrImagePlatform
		}
		args = append(args, "--platform", build.Platform)
	}
	if build.NoCache {
		args = append(args, "--no-cache")
	}
	if build.Pull {
		pull := "--pull"
		if c.Tool == "podman" {
			pull = "--pull=always"
		}
		args = append(args, pull)
	}
	args = append(args, "--", build.Context)
	if c.Tool == "podman" {
		return c.imageExec(args...), nil
	}
	plain := append([]string{"build", "--progress=plain"}, args[1:]...)
	return scriptHead + "export BUILDKIT_PROGRESS=plain\nif " + c.Line("build", "--help") + " 2>/dev/null | grep -q -- '--progress'; then\nexec " + c.Line(plain...) + "\nfi\nexec " + c.Line(args...) + "\n", nil
}

func (c DockerCmd) ImagePrune(all bool) string {
	args := []string{"image", "prune", "--force"}
	if all {
		args = append(args, "--all")
	}
	return c.imageScript(args...)
}

func (c DockerCmd) ImagePruneContainerIDs() string {
	return c.imageScript("ps", "--all", "--no-trunc", "--format", "{{.ID}}")
}
