#!/usr/bin/env bash
# A disposable OpenWrt test machine: the official x86-64 rootfs with procd,
# dropbear and BusyBox, as on a router. See SKILL.md.
set +x
set -euo pipefail

export MSYS_NO_PATHCONV=1
box=fs-openwrt
# Another release or a derivative: OPENWRT_IMAGE=openwrt/rootfs:x86-64-21.02.7
# or immortalwrt/rootfs:x86-64-openwrt-24.10, with reset to switch.
image=${OPENWRT_IMAGE:-openwrt/rootfs:x86-64-24.10.4}
port=2223
env_file=${OPENWRT_ENV:-"$HOME/.finallyshell-openwrt.env"}

die() {
    printf 'openwrt: %s\n' "$*" >&2
    exit 1
}

usage() {
    printf 'Usage: %s {up|down|reset|ssh-env [root|tester]|exec [COMMAND [ARG...]]|status}\n' "${0##*/}"
}

credentials() {
    if [[ ! -f $env_file ]]; then
        local root tester
        root=$(head -c 30 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20)
        tester=$(head -c 30 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20)
        (umask 077; printf 'OPENWRT_ROOT_PASSWORD=%s\nOPENWRT_TESTER_PASSWORD=%s\n' "$root" "$tester" >"$env_file")
    fi
    # shellcheck disable=SC1090
    . "$env_file"
    [[ -n ${OPENWRT_ROOT_PASSWORD:-} && -n ${OPENWRT_TESTER_PASSWORD:-} ]] || die "$env_file lacks the passwords."
}

running() { [[ $(docker container inspect --format '{{.State.Running}}' "$box" 2>/dev/null) == true ]]; }

# The address Docker gave the container, and its gateway.
docker_net() {
    docker container inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}/{{.IPPrefixLen}} {{.Gateway}}{{end}}' "$box"
}

# netifd puts the LAN on 192.168.1.1 at boot, where the published port
# goes nowhere: this gives it Docker's address back, with Docker's
# gateway as its route and DNS server. ImmortalWrt's x86 image has eth0
# as a DHCP wan and no lan: that wan becomes the lan.
lan_script='[ "$(uci -q get network.lan.ipaddr)" = "$1" ] && exit 0
if ! uci -q get network.lan >/dev/null; then
  uci -q delete network.wan6
  uci rename network.wan=lan
  uci set network.lan.proto=static
fi
uci set network.lan.ipaddr="$1"
uci -q delete network.lan.netmask
uci set network.lan.gateway="$2"
uci -q delete network.lan.dns
uci add_list network.lan.dns="$2"
uci commit network
# ImmortalWrt turns on flow offloading, which the WSL and Docker Desktop
# kernels lack: fw4 then loads no rules at all.
uci -q set firewall.@defaults[0].flow_offloading=0 && uci commit firewall
/etc/init.d/network restart'

wait_ready() {
    local addr gw
    for ((i = 0; i < 60; i++)); do
        docker exec "$box" sh -c 'pidof dropbear >/dev/null && pidof netifd >/dev/null && ubus call network.interface dump >/dev/null' 2>/dev/null && break
        sleep 2
    done
    read -r addr gw <<<"$(docker_net)"
    docker exec "$box" sh -c "$lan_script" sh "$addr" "$gw"
    for ((i = 0; i < 30; i++)); do
        docker exec "$box" sh -c "ip -4 addr | grep -q 'inet $addr'" 2>/dev/null && break
        sleep 1
    done
    # The network restart may restart dropbear too: ready once it greets
    # through the published port.
    local banner
    for ((i = 0; i < 30; i++)); do
        if banner=$(timeout 3 bash -c "exec 3<>/dev/tcp/127.0.0.1/$port && head -c 4 <&3" 2>/dev/null) && [[ $banner == SSH- ]]; then
            return
        fi
        sleep 1
    done
    die 'SSH did not come up; see docker logs fs-openwrt.'
}

account_script='grep -q "^tester:" /etc/passwd && exit 0
echo "tester:x:1000:1000:tester:/home/tester:/bin/ash" >>/etc/passwd
echo "tester:x:1000:" >>/etc/group
echo "tester:*:0:0:99999:7:::" >>/etc/shadow
mkdir -p /home/tester && chown 1000:1000 /home/tester'

setup_accounts() {
    docker exec "$box" sh -c "$account_script"
    printf '%s\n%s\n' "$OPENWRT_ROOT_PASSWORD" "$OPENWRT_ROOT_PASSWORD" | docker exec -i "$box" passwd root >/dev/null 2>&1
    printf '%s\n%s\n' "$OPENWRT_TESTER_PASSWORD" "$OPENWRT_TESTER_PASSWORD" | docker exec -i "$box" passwd tester >/dev/null 2>&1
}

up() {
    credentials
    if ! docker container inspect "$box" >/dev/null 2>&1; then
        docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image" >/dev/null
        docker create --name "$box" --hostname openwrt-test --privileged \
            -p "127.0.0.1:$port:22" "$image" /sbin/init >/dev/null
    fi
    running || docker start "$box" >/dev/null
    wait_ready
    setup_accounts
    printf 'openwrt: %s ready, SSH 127.0.0.1:%s (root, tester)\n' "$box" "$port"
}

cmd=${1:-}
[[ $# -gt 0 ]] && shift
case $cmd in
    up) up ;;
    down) docker stop "$box" >/dev/null && echo 'openwrt: stopped' ;;
    reset)
        docker rm -f "$box" >/dev/null 2>&1 || true
        up
        ;;
    ssh-env)
        credentials
        case ${1:-root} in
            root) printf "export FINALLYSHELL_TEST_SSH='root:%s@127.0.0.1:%s'\n" "$OPENWRT_ROOT_PASSWORD" "$port" ;;
            tester) printf "export FINALLYSHELL_TEST_SSH='tester:%s@127.0.0.1:%s'\n" "$OPENWRT_TESTER_PASSWORD" "$port" ;;
            *) usage; exit 2 ;;
        esac
        ;;
    exec)
        running || die "$box is not running; run up first."
        if [[ $# -eq 0 ]]; then docker exec -it "$box" /bin/ash; else docker exec "$box" "$@"; fi
        ;;
    status) docker ps -a --filter "name=^$box\$" --format '{{.Names}}  {{.Status}}  {{.Ports}}' ;;
    *) usage; exit 2 ;;
esac
