#!/usr/bin/env bash
set +x
set -euo pipefail

export MSYS_NO_PATHCONV=1
box=fs-testbox
repository=finallyshell-testbox
base_image=$repository:debian12
endpoint=127.0.0.1:2222
ssh_binding='[{"HostIp":"127.0.0.1","HostPort":"2222"}]'
# The web ports, chosen as the box is made (up, reset): Windows reserves
# ranges of ports for Hyper-V, which may hold the defaults.
http_port=${TESTBOX_HTTP_PORT:-8080}
https_port=${TESTBOX_HTTPS_PORT:-8443}
[[ $http_port =~ ^[1-9][0-9]{0,4}$ && $https_port =~ ^[1-9][0-9]{0,4}$ ]] \
    || { printf 'testbox: TESTBOX_HTTP_PORT and TESTBOX_HTTPS_PORT must be port numbers.\n' >&2; exit 1; }
skill_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_dir=$(cd -- "$skill_dir/../../.." && pwd -P)
env_file=${TESTBOX_ENV:-"$HOME/.finallyshell-testbox.env"}
windows=false
case $(uname -s) in MINGW*|MSYS*) windows=true ;; esac

die() {
    printf 'testbox: %s\n' "$*" >&2
    exit 1
}

usage() {
    printf 'Usage: %s {up|down|reset|ssh-env [root|tester]|exec [COMMAND [ARG...]]|status|caddy|snapshot NAME|restore NAME}\n' "${0##*/}"
}

docker_ready() {
    command -v docker >/dev/null || die 'Install Docker first.'
    local target
    if [[ -n ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} ]]; then
        target=$DOCKER_HOST
    else
        target=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
    fi
    case $target in
        unix:///*|npipe:////./pipe/*) ;;
        *) die 'Select a local Docker engine; remote Docker endpoints are not supported.' ;;
    esac
    if docker info >/dev/null 2>&1; then
        return
    fi
    if "$windows" && [[ $1 == start ]]; then
        powershell.exe -NoProfile -NonInteractive -Command \
            '$ErrorActionPreference = "Stop"; Start-Process -FilePath (Join-Path $env:ProgramFiles "Docker\Docker\Docker Desktop.exe") -WindowStyle Hidden'
        for ((attempt=0; attempt<60; attempt++)); do
            if docker info >/dev/null 2>&1; then
                return
            fi
            sleep 2
        done
    fi
    die 'Docker is not ready. Start Docker Desktop or the local Linux Docker engine and check docker info.'
}

check_container() {
    local identity
    # The image, the name and the SSH port tell the box; its web ports are
    # whichever it was made with.
    identity=$(docker container inspect --format '{{.Config.Image}} {{.Config.Hostname}} {{json (index .HostConfig.PortBindings "22/tcp")}}' "$box") || exit 1
    [[ $identity == "$repository:"*" $box $ssh_binding" ]] || die "Refusing to manage an unexpected container named $box."
}

container_exists() {
    docker container inspect "$box" >/dev/null 2>&1
}

locate_env() {
    local parent common
    parent=$(cd -P -- "$(dirname -- "$env_file")" && pwd -P) || die 'The credential file parent directory must exist.'
    env_file=$parent/$(basename -- "$env_file")
    common=$(git -C "$repo_dir" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
    if [[ -n $common ]]; then
        common=$(cd -P -- "$common/.." && pwd -P)
    fi
    if (shopt -s nocasematch; [[ $env_file == "$repo_dir/"* || ( -n $common && $env_file == "$common/"* ) || -L $env_file ]]); then
        die 'TESTBOX_ENV must be outside the repository and must not be a symlink.'
    fi
}

generate_credentials() {
    local python path=$env_file
    if command -v python3 >/dev/null && python3 -c 'import secrets' 2>/dev/null; then
        python=python3
    elif command -v python >/dev/null && python -c 'import secrets' 2>/dev/null; then
        python=python
    else
        die 'Install Python 3 to generate the testbox credentials.'
    fi
    if "$windows"; then
        path=$(cygpath -am -- "$path")
    fi
    "$python" - "$path" "$endpoint" <<'PY'
import csv
import os
import secrets
import subprocess
import sys

path, endpoint = sys.argv[1:]
try:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    sys.exit(0)
try:
    with os.fdopen(fd, "w", encoding="ascii", newline="\n") as env:
        if os.name == "nt":
            account = subprocess.check_output(["whoami.exe", "/user", "/fo", "csv", "/nh"], text=True, errors="replace")
            sid = next(csv.reader(account.splitlines()))[1]
            subprocess.run(["icacls.exe", path, "/grant:r", f"*{sid}:F", "/inheritance:r"],
                           check=True, stdout=subprocess.DEVNULL)
        env.write(f"ROOT_PW={secrets.token_urlsafe(18)}\nTESTER_PW={secrets.token_urlsafe(18)}\nSSH={endpoint}\n")
except Exception:
    os.unlink(path)
    raise
PY
}

read_credentials() {
    local line ssh_address=
    ROOT_PW= TESTER_PW=
    [[ -f $env_file && -r $env_file ]] || die 'Credential file not found. Run up or set TESTBOX_ENV.'
    while IFS= read -r line || [[ -n $line ]]; do
        line=${line%$'\r'}
        case $line in
            ROOT_PW=*) ROOT_PW=${line#*=} ;;
            TESTER_PW=*) TESTER_PW=${line#*=} ;;
            SSH=*) ssh_address=${line#*=} ;;
        esac
    done < "$env_file"
    [[ $ROOT_PW =~ ^[A-Za-z0-9_-]+$ && $TESTER_PW =~ ^[A-Za-z0-9_-]+$ && $ssh_address == "$endpoint" ]] \
        || die 'Invalid credentials or SSH endpoint in the credential file.'
}

prepare_credentials() {
    locate_env
    if [[ ! -e $env_file ]]; then
        generate_credentials
    fi
    read_credentials
}

build_base() {
    if ! docker image inspect "$base_image" >/dev/null 2>&1; then
        docker build --tag "$base_image" - < "$skill_dir/Dockerfile"
    fi
}

wait_for_ssh() {
    local state=unknown
    for ((attempt=0; attempt<60; attempt++)); do
        state=$(docker exec "$box" systemctl is-system-running 2>/dev/null || true)
        if [[ $state == running || $state == degraded ]] \
            && docker exec "$box" systemctl is-active --quiet ssh.service; then
            return
        fi
        sleep 2
    done
    die "systemd or ssh.service was not ready after 120 seconds (systemd: ${state:-unknown})."
}

start_box() {
    if container_exists; then
        check_container
        if [[ $(docker container inspect --format '{{.State.Running}}' "$box") != true ]]; then
            docker start "$box" >/dev/null
        fi
    else
        docker run -d --name "$box" --hostname "$box" \
            --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
            --tmpfs /run --tmpfs /run/lock \
            -p "$endpoint:22" -p "127.0.0.1:$http_port:80" -p "127.0.0.1:$https_port:443" "$1" >/dev/null \
            || die "Could not start the box. If a port is taken or reserved (on Windows: netsh interface ipv4 show excludedportrange protocol=tcp), run reset with TESTBOX_HTTP_PORT or TESTBOX_HTTPS_PORT set to free ports."
    fi
    wait_for_ssh
    ROOT_PW=$ROOT_PW TESTER_PW=$TESTER_PW docker exec -e ROOT_PW -e TESTER_PW -i "$box" sh -s < "$skill_dir/setup.sh"
    printf '%s is ready (SSH %s).\n' "$box" "$endpoint"
}

replace_box() {
    if container_exists; then
        check_container
        if [[ $(docker container inspect --format '{{.State.Running}}' "$box") == true ]]; then
            docker stop "$box" >/dev/null
        fi
        docker rm "$box" >/dev/null
    fi
    start_box "$1"
}

snapshot_image() {
    [[ $1 =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ && $1 != debian12 ]] \
        || die 'Use a valid Docker tag as the snapshot name (debian12 is reserved).'
    printf '%s:%s\n' "$repository" "$1"
}

install_caddy() {
    check_container
    docker exec -i "$box" sh -se <<'CADDY_SETUP'
umask 022
keyring=/usr/share/keyrings/caddy-stable-archive-keyring.gpg
source=/etc/apt/sources.list.d/caddy-stable.list
if [ ! -s "$keyring" ]; then
    key=$(mktemp)
    trap 'rm -f "$key"' EXIT
    curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key -o "$key"
    gpg --batch --yes --dearmor -o "$keyring" "$key"
    chmod 644 "$keyring"
fi
printf 'deb [signed-by=%s] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main\n' "$keyring" > "$source"
if [ "$(dpkg-query -W -f='${Status} ${Version}' caddy 2>/dev/null)" != 'install ok installed 2.11.9' ]; then
    apt-get update
    apt-get install -y --no-install-recommends caddy=2.11.9
fi
install -d -m 755 /etc/caddy/sites /srv/www
install -d -m 755 -o caddy -g caddy /var/log/caddy
hash_file=/etc/caddy/.testbox-panel.hash
if [ ! -s "$hash_file" ]; then
    panel_hash=$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))' | caddy hash-password --algorithm bcrypt --bcrypt-cost 10)
    (umask 077; printf '%s\n' "$panel_hash" > "$hash_file")
fi
cat > /etc/caddy/Caddyfile <<'CADDYFILE'
{
	email ops@example.test
	admin localhost:2019
	servers {
		protocols h1 h2 h3
	}
}

(security) {
	header {
		-Server
		X-Content-Type-Options nosniff
		Referrer-Policy strict-origin-when-cross-origin
	}
}

(logging) {
	log {
		output file /var/log/caddy/{args[0]}.log {
			roll_size 10MiB
			roll_keep 5
		}
		format json
	}
}

:80 {
	root * /srv/www
	file_server
	import security
}

import sites/*.caddy
CADDYFILE
cat > /etc/caddy/sites/apps.caddy <<'CADDYFILE'
app.example.test, www.app.example.test {
	tls internal
	import security
	import logging app
	encode zstd gzip
	@api path /api/*
	handle @api {
		reverse_proxy 127.0.0.1:3000 127.0.0.1:3001 {
			lb_policy round_robin
			health_uri /health
			health_interval 10s
		}
	}
	handle {
		root * /srv/www
		try_files {path} /index.html
		file_server
	}
}
CADDYFILE
printf '\npanel.example.test {\n\ttls internal\n\tbasic_auth {\n\t\tadmin %s\n\t}\n\treverse_proxy 127.0.0.1:8090\n}\n' "$(cat "$hash_file")" >> /etc/caddy/sites/apps.caddy
cat >> /etc/caddy/sites/apps.caddy <<'CADDYFILE'

legacy.example.test {
	tls internal
	redir https://app.example.test{uri} permanent
}
CADDYFILE
printf '<!doctype html><title>Finallyshell testbox</title><h1>Finallyshell testbox</h1>\n' > /srv/www/index.html
runuser -u caddy -- caddy validate --config /etc/caddy/Caddyfile
systemctl enable --now caddy.service
systemctl reload caddy.service
systemctl is-active caddy.service
CADDY_SETUP
}

case ${1:-help} in
    up|down|reset|status|caddy) [[ $# == 1 ]] || die "${1} takes no arguments." ;;
    ssh-env) [[ $# -le 2 ]] || die 'Usage: ssh-env [root|tester].' ;;
    snapshot|restore) [[ $# == 2 ]] || die "Usage: ${1} NAME." ;;
    exec) ;;
    help|-h|--help) usage; exit 0 ;;
    *) usage >&2; exit 1 ;;
esac

case $1 in
    up)
        docker_ready start
        build_base
        prepare_credentials
        start_box "$base_image"
        ;;
    down)
        docker_ready check
        check_container
        docker stop "$box"
        ;;
    reset)
        docker_ready check
        build_base
        prepare_credentials
        replace_box "$base_image"
        ;;
    ssh-env)
        case ${2:-root} in root|tester) user=${2:-root} ;; *) die 'Choose root or tester.' ;; esac
        locate_env
        read_credentials
        case $user in root) password=$ROOT_PW ;; tester) password=$TESTER_PW ;; esac
        printf 'Warning: ssh-env writes an SSH password to stdout. Do not display or log it.\n' >&2
        printf 'export FINALLYSHELL_TEST_SSH=%q\n' "$user:$password@$endpoint"
        ;;
    exec)
        shift
        docker_ready check
        check_container
        args=(-i)
        if [[ $# == 0 ]]; then set -- bash; fi
        if [[ -t 0 && -t 1 ]]; then
            if "$windows" && command -v winpty >/dev/null; then
                exec winpty docker exec -it "$box" "$@"
            fi
            args+=(-t)
        fi
        docker exec "${args[@]}" "$box" "$@"
        ;;
    status)
        docker_ready check
        check_container
        docker container inspect --format '{{.Name}}: {{.State.Status}} ({{.Config.Image}})' "$box"
        docker port "$box"
        if [[ $(docker container inspect --format '{{.State.Running}}' "$box") == true ]]; then
            state=$(docker exec "$box" systemctl is-system-running || true)
            ssh_state=$(docker exec "$box" systemctl is-active ssh.service || true)
            caddy_state=$(docker exec "$box" systemctl is-active caddy.service || true)
            printf 'systemd: %s; ssh: %s; caddy: %s\n' "$state" "$ssh_state" "$caddy_state"
        fi
        ;;
    caddy)
        docker_ready check
        install_caddy
        ;;
    snapshot)
        image=$(snapshot_image "$2")
        docker_ready check
        check_container
        docker commit "$box" "$image"
        printf 'Saved %s locally. Do not push or share this image.\n' "$image"
        ;;
    restore)
        image=$(snapshot_image "$2")
        docker_ready check
        docker image inspect "$image" >/dev/null || die "Snapshot not found: $image."
        prepare_credentials
        replace_box "$image"
        ;;
esac
