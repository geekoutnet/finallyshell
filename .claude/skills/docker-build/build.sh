#!/usr/bin/env bash
set -euo pipefail
export MSYS_NO_PATHCONV=1

skill_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_dir=$(cd -- "$skill_dir/../../.." && pwd -P)
cache=${FINALLYSHELL_BUILD_CACHE:-finallyshell-build}
label=dev.finallyshell.docker-build
platforms=()
jobs=0
no_dmg=0
version=

die() { printf 'docker-build: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: build.sh image | shell | clean
       build.sh build [--version X] [-j N] [--no-dmg] [mac|linux|win|OS/ARCH ...]
       build.sh verify DIR [--no-dmg] [mac|linux|win|OS/ARCH ...]
EOF
}

host_path() (
    cd -- "$1"
    case $(uname -s) in MINGW*|MSYS*) pwd -W ;; *) pwd -P ;; esac
)

add_platform() {
    local chosen
    for chosen in "${platforms[@]}"; do
        [[ $chosen != "$1" ]] || return 0
    done
    platforms+=("$1")
}

parse_targets() {
    local action=$1
    shift
    while [[ $# -gt 0 ]]; do
        case $1 in
            --version)
                [[ $action == build && -n ${2:-} && -z $version ]] || die 'Expected --version X once for build.'
                version=$2; shift ;;
            -j)
                [[ $action == build && ${2:-} =~ ^[1-9][0-9]{0,5}$ ]] || die 'Expected -j followed by a positive integer for build.'
                jobs=$2; shift ;;
            --no-dmg) no_dmg=1 ;;
            mac) add_platform darwin/arm64; add_platform darwin/amd64 ;;
            linux) add_platform linux/amd64; add_platform linux/arm64 ;;
            win) add_platform windows/amd64 ;;
            darwin/arm64|darwin/amd64|linux/amd64|linux/arm64|windows/amd64) add_platform "$1" ;;
            *) die "Unknown option or platform: $1" ;;
        esac
        shift
    done
    if [[ ${#platforms[@]} == 0 ]]; then
        platforms=(darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64)
    fi
}

next_version() (
    local tags tag
    cd -- "$repo_dir"
    tags=$(git tag --list 'v2.0.*' --sort=-v:refname) || exit 1
    while IFS= read -r tag; do
        if [[ $tag =~ ^v2\.0\.([0-9]+)$ ]]; then
            printf '2.0.%s-local\n' "$((10#${BASH_REMATCH[1]} + 1))"
            return
        fi
    done <<< "$tags"
    printf '2.0.0-local\n'
)

docker_ready() {
    local target
    command -v docker >/dev/null || die 'Install Docker first.'
    if [[ -n ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} ]]; then
        target=$DOCKER_HOST
    else
        target=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
    fi
    case $target in
        unix:///*|npipe:////./pipe/*) ;;
        *) die 'Select a local Docker engine; remote contexts are not supported.' ;;
    esac
    [[ $(docker info --format '{{.OSType}}') == linux ]] || die 'Start the local Linux Docker engine first.'
}

image_tag() {
    local hash
    if command -v sha256sum >/dev/null; then
        hash=$(tr -d '\r' < "$skill_dir/Dockerfile" | sha256sum)
    else
        hash=$(tr -d '\r' < "$skill_dir/Dockerfile" | shasum -a 256)
    fi
    printf 'finallyshell-build:%s\n' "${hash%% *}"
}

build_image() {
    tr -d '\r' < "$skill_dir/Dockerfile" | docker build --tag "$image" -
}

ensure_image() {
    docker image inspect "$image" >/dev/null 2>&1 || build_image
}

mount_caches() {
    local spec name owner variable
    mounts=()
    for spec in mod:/go/pkg/mod go:/root/.cache/go-build natives:/root/.cache/mygo/natives; do
        name=$cache-${spec%%:*}
        if ! docker volume inspect "$name" >/dev/null 2>&1; then
            docker volume create --label "$label=1" "$name" >/dev/null
        fi
        owner=$(docker volume inspect --format "{{index .Labels \"$label\"}}" "$name")
        [[ $owner == 1 ]] || die "Not a docker-build cache: $name"
        mounts+=(-v "$name:${spec#*:}")
    done
    for variable in GOPROXY GOFLAGS HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy RETRY_BUDGET_SECONDS RETRY_WAITS; do
        if [[ -n ${!variable+x} ]]; then mounts+=(-e "$variable"); fi
    done
}

stream_source() (
    cd -- "$repo_dir"
    git ls-files --cached --others --exclude-standard --deduplicate -z |
        while IFS= read -r -d '' path; do
            if [[ -f $path || -L $path ]]; then printf '%s\0' "$path"; fi
        done | COPYFILE_DISABLE=1 tar -c --no-recursion --null -T -
)

command=${1:-help}
case $command in
    help|-h|--help) usage; exit 0 ;;
    build) shift; parse_targets build "$@" ;;
    verify)
        [[ $# -ge 2 && -d $2 ]] || die 'Expected verify DIR for an existing output directory.'
        verify_dir=$(host_path "$2")
        shift 2; parse_targets verify "$@" ;;
    image|shell|clean) [[ $# == 1 ]] || die "$command takes no arguments." ;;
    *) usage >&2; exit 1 ;;
esac

[[ $cache =~ ^finallyshell-build([.-][a-z0-9]+)*$ ]] || die 'Cache names must start with finallyshell-build.'
image=$(image_tag)
docker_ready
case $command in
    image) build_image ;;
    build)
        start=$SECONDS
        if [[ -z $version ]]; then version=$(next_version); fi
        [[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)*)?$ ]] || die 'Use a version such as 2.0.18-local.'
        ensure_image >&2
        mount_caches
        owner=("$(id -u)" "$(id -g)")
        case $(uname -s) in MINGW*|MSYS*) owner=(0 0) ;; esac
        for dir in "$repo_dir/dist" "$repo_dir/dist/docker"; do
            [[ ! -L $dir ]] || die "Refusing to write through a symlink: $dir"
        done
        mkdir -p -- "$repo_dir/dist/docker"
        exports=$(cd -- "$repo_dir/dist/docker" && pwd -P)
        [[ $exports == "$repo_dir/dist/docker" ]] || die "Output resolves outside the checkout: $exports"
        staging=$(mktemp -d "$exports/.packaging.XXXXXXXX")
        destination=$exports/$version
        lock=$exports/.packaging-$version.lock
        trap 'rmdir -- "$staging" 2>/dev/null || true' EXIT
        stream_source | docker run --rm -i "${mounts[@]}" -v "$(host_path "$staging"):/exports" "$image" \
            bash -euc 'mkdir -p /work/src; cd /work/src; tar -xf -; tr -d "\r" < .claude/skills/docker-build/package.sh > /work/package.sh; exec bash /work/package.sh build "$@"' \
            bash "$version" "$jobs" "$no_dmg" "${owner[@]}" "${platforms[@]}"
        mkdir -- "$lock" || die "Another build is exporting $version; new artifacts remain in $staging."
        trap 'rmdir -- "$lock" "$staging" 2>/dev/null || true' EXIT
        [[ ! -L $destination && ( ! -e $destination || -d $destination ) ]] || die "Unsafe output path: $destination"
        previous=$staging/previous
        if [[ -d $destination ]]; then mv -- "$destination" "$previous"; fi
        if ! mv -- "$staging/artifacts" "$destination"; then
            if [[ -d $previous ]]; then mv -- "$previous" "$destination"; fi
            die "Cannot install the artifacts; check $staging."
        fi
        if [[ -d $previous ]]; then
            previous_path=$(cd -- "$previous" && pwd -P)
            [[ $previous_path == "$exports/"* && $previous_path == "$previous" ]] || die "Unsafe backup path: $previous_path"
            rm -rf -- "$previous_path"
        fi
        printf 'docker-build: wall %ss; dist/docker/%s\n' "$((SECONDS - start))" "$version"
        ;;
    verify)
        ensure_image
        tr -d '\r' < "$skill_dir/package.sh" | docker run --rm -i -v "$verify_dir:/artifacts:ro" "$image" \
            bash -s -- verify "$no_dmg" "${platforms[@]}"
        ;;
    shell)
        ensure_image
        mount_caches
        terminal=(-i)
        wrapper=()
        if [[ -t 0 && -t 1 ]]; then
            terminal+=(-t)
            case $(uname -s) in MINGW*|MSYS*) if command -v winpty >/dev/null; then wrapper=(winpty); fi ;; esac
        fi
        "${wrapper[@]}" docker run --rm "${terminal[@]}" "${mounts[@]}" -v "$(host_path "$repo_dir"):/checkout:ro" "$image" \
            bash -c 'mkdir -p /work/src; cd /work/src; exec bash' ;;
    clean)
        for suffix in mod go natives; do
            name=$cache-$suffix
            if [[ $(docker volume inspect --format "{{index .Labels \"$label\"}}" "$name" 2>/dev/null) == 1 ]]; then
                docker volume rm "$name"
            fi
        done
        if [[ $(docker image inspect --format "{{index .Config.Labels \"$label\"}}" "$image" 2>/dev/null) == 1 ]]; then
            docker image rm "$image"
        fi ;;
esac
