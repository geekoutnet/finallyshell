#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C CGO_ENABLED=0

die() { printf 'docker-build: %s\n' "$*" >&2; exit 1; }
retry() { bash /work/retry.sh "$@"; }

fetch_native() (
    local name=$1 url=$2 sha=$3 dir part
    [[ $name =~ ^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$ && $sha =~ ^[0-9a-f]{64}$ && $url == https://* ]] || die 'Invalid MyGo native metadata.'
    dir=$HOME/.cache/mygo/natives/$sha
    [[ ! -s $dir/$name ]] || { [[ -x $dir/$name ]] || chmod 755 "$dir/$name"; return; }
    mkdir -p "$dir"
    flock 9
    [[ ! -s $dir/$name ]] || { [[ -x $dir/$name ]] || chmod 755 "$dir/$name"; return; }
    part=$(mktemp "$dir/.download.XXXXXXXX")
    trap 'rm -f -- "$part"' EXIT
    if ! curl -fsSL --connect-timeout 10 --max-time 25 "$url" -o "$part"; then
        printf 'error: downloading %s from %s\n' "$name" "$url" >&2
        return 1
    fi
    printf '%s  %s\n' "$sha" "$part" | sha256sum -c --status
    chmod 755 "$part"
    mv -- "$part" "$dir/$name"
) 9>"$HOME/.cache/mygo/natives/.${3}.lock"

prefetch_natives() {
    local mygo rows name url sha pid failed=0
    local -a downloads=()
    mygo=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)
    rows=$(printf '%s\n' "$@" | jq -Rn '[inputs | sub("/"; "-")]')
    rows=$(jq -er --argjson targets "$rows" '[.libraries[].files as $files | $targets[] | $files[.] | [.name,.url,.sha256] | @tsv] | unique[]' \
        "$mygo/plugins/terminal/mygo-plugin.json")
    while IFS=$'\t' read -r name url sha; do
        fetch_native "$name" "$url" "$sha" &
        downloads+=("$!")
    done <<< "$rows"
    for pid in "${downloads[@]}"; do wait "$pid" || failed=1; done
    return "$failed"
}

download_modules() {
    if ! go mod download > /work/modules.log 2>&1; then
        cat /work/modules.log >&2
        if ! grep -Eqi 'unexpected EOF|timeout|connection reset|TLS handshake| [5][0-9]{2} ' /work/modules.log; then
            return 1
        fi
        printf 'error: downloading Go modules\n' >&2
        return 1
    fi
}

sign_app() {
    local app=$1 library
    for library in "$app"/Contents/Resources/libghostty-vt*.dylib; do
        [[ -f $library ]] || die "Missing Ghostty library in $app"
        chmod 755 "$library"
    done
    rcodesign sign "$app"
}

make_dmg() {
    local app=$1 output=$2 work raw bytes executable relative
    work=$(mktemp -d)
    raw=$work/volume.hfs
    mkdir "$work/root"
    mv -- "$app" "$work/root/Finallyshell.app"
    cp "$work/root/Finallyshell.app/Contents/Resources/AppIcon.icns" "$work/root/.VolumeIcon.icns"
    bytes=$(du -sb "$work/root" | cut -f1)
    truncate -s "$(( (bytes / 1048576 + 8) * 1048576 ))" "$raw"
    mkfs.hfsplus -v Finallyshell "$raw"
    hfsplus "$raw" chmod 040755 /
    hfsplus "$raw" addall "$work/root"
    hfsplus "$raw" symlink /Applications /Applications
    while IFS= read -r -d '' executable; do
        relative=${executable#"$work/root"}
        hfsplus "$raw" chmod 0755 "$relative"
    done < <(find "$work/root" -type f -perm /111 -print0)
    hfsplus "$raw" ls /Finallyshell.app/Contents/MacOS | grep -E '^100755 .* Finallyshell$'
    dmg build "$raw" "$output"
    [[ -s $output ]] || die 'dmg build produced no image.'
    rm -rf -- "$work"
}

build_platform() {
    local target=$1 key=${1//\//-} arch=${1#*/} out final start=$SECONDS
    out=/work/out/$key
    final=/work/collect/$key
    mkdir -p "$final"
    cd "/work/targets/$key"
    export GOMAXPROCS=$threads LINK_RSS_FILE=/work/logs/$key.link
    export GOFLAGS="${GOFLAGS:+$GOFLAGS }-toolexec=/work/link-time"
    if [[ $target == windows/* ]]; then export PATH=/work/nsis:$PATH; fi
    retry go tool mygo build -platform "$target" -o "$out"
    out=$out/$key
    printf 'mygo finished in %ss\n' "$((SECONDS - start))"
    start=$SECONDS
    case $target in
        darwin/*)
            sign_app "$out/Finallyshell.app"
            if (( no_dmg )); then
                tar -cf "$final/finallyshell-$version-$arch.app.tar" -C "$out" Finallyshell.app
            else
                make_dmg "$out/Finallyshell.app" "$final/finallyshell-$version-$arch.dmg"
            fi ;;
        linux/*) cp "$out"/*.deb "$out"/*.tar.gz "$out/install.sh" "$final/" ;;
        windows/*)
            cp "$out"/*-setup-*.exe "$final/"
            (cd "$out"; zip -3 "$final/finallyshell-$version-windows-amd64.zip" \
                Finallyshell.exe ghostty-vt.dll LICENSE.txt THIRD_PARTY_LICENSES.txt) ;;
    esac
    printf 'post-processing finished in %ss\n' "$((SECONDS - start))"
}

job_limits() {
    local cpus mem limit quota period resource
    cpus=$(nproc)
    if [[ -r /sys/fs/cgroup/cpu.max ]]; then
        read -r quota period < /sys/fs/cgroup/cpu.max
        if [[ $quota != max ]] && (( (quota + period - 1) / period < cpus )); then
            cpus=$(( (quota + period - 1) / period ))
        fi
    fi
    mem=$(awk '/^MemTotal:/ {printf "%.0f\n", $2 * 1024}' /proc/meminfo)
    for resource in /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory.high /sys/fs/cgroup/memory/memory.limit_in_bytes; do
        if [[ -r $resource ]]; then
            read -r limit < "$resource"
            if [[ $limit != max ]] && (( limit < mem )); then mem=$limit; fi
        fi
    done
    limit=$(( (mem - 512 * 1048576) / (1536 * 1048576) ))
    (( limit >= 1 )) || limit=1
    if (( jobs == 0 || jobs > cpus )); then jobs=$cpus; fi
    if (( jobs > limit )); then jobs=$limit; fi
    if (( jobs > $# )); then jobs=$#; fi
    threads=$((cpus / jobs))
    limit=$(( mem / jobs / (256 * 1048576) ))
    (( limit >= 1 )) || limit=1
    if (( threads > limit )); then threads=$limit; fi
    printf 'docker-build: %s platform job(s), %s Go workers/job, %s MiB RAM limit\n' "$jobs" "$threads" "$((mem / 1048576))"
}

collect() (
    local uid=$1 gid=$2 dir file destination=/exports/artifacts
    mkdir "$destination"
    for dir in /work/collect/*; do
        for file in "$dir"/*; do
            if [[ -e $destination/${file##*/} ]]; then
                cmp "$file" "$destination/${file##*/}"
            else
                cp -- "$file" "$destination/"
            fi
        done
    done
    cp resources/LICENSE.txt resources/THIRD_PARTY_LICENSES.txt "$destination/"
    (cd "$destination"; sha256sum -- * > SHA256SUMS)
    if (( uid || gid )); then chown -R "$uid:$gid" "$destination"; fi
)

build_all() {
    local version=$1 jobs=$2 no_dmg=$3 uid=$4 gid=$5
    shift 5
    local total=$SECONDS step=$SECONDS threads key next=0 index pid platform failed=0 active=0
    local -a targets=("$@")
    local -A running=() started=() elapsed=()
    unset MYGO_UPDATER_PRIVATE_KEY
    local expected_go
    expected_go=$(awk '{sub(/\r$/, "")} $1 == "go" {version = "go" $2} $1 == "toolchain" && $2 != "default" {toolchain = $2} END {print toolchain ? toolchain : version}' go.mod)
    [[ $(go env GOVERSION) == "$expected_go" ]] || die "Update Dockerfile to match go.mod ($expected_go)."
    mkdir -p /work/{logs,targets,collect,nsis}
    tr -d '\r' < .github/scripts/build-with-retries.sh > /work/retry.sh
    tr -d '\r' < .claude/skills/docker-build/link-time.sh > /work/link-time
    chmod 755 /work/link-time
    cat > /work/nsis/makensis <<'NSIS'
#!/bin/sh
case ${1:-} in
    -V2) exec /usr/bin/makensis '-XSetCompressor /SOLID /FINAL zlib' "$@" ;;
    *) exec /usr/bin/makensis "$@" ;;
esac
NSIS
    chmod 755 /work/nsis/makensis
    jq --arg version "$version" '.version = $version | del(.updates.privateKey)' mygo.json > /work/mygo.json
    mv /work/mygo.json mygo.json
    sed 's/\r$//' LICENSE > resources/LICENSE.txt
    sed -i 's/\r$//' internal/icons/{agents,svg}/LICENSE tools/release/notices/ghostty.txt
    job_limits "$@"
    printf 'docker-build: preparing modules, licenses and native libraries\n'
    export GOMAXPROCS=$threads
    export -f download_modules
    retry bash -ec download_modules
    printf '  modules: %ss\n' "$((SECONDS - step))"
    step=$SECONDS
    prefetch_natives "$@" &
    local prefetch=$!
    go run ./tools/release licenses > resources/THIRD_PARTY_LICENSES.txt
    go tool mygo --version
    if ! wait "$prefetch"; then
        printf 'docker-build: native prefetch incomplete; affected MyGo jobs will retry downloads.\n' >&2
    fi
    printf '  tools, licenses and natives: %ss\n' "$((SECONDS - step))"

    while (( next < ${#targets[@]} || active )); do
        while (( next < ${#targets[@]} && active < jobs )); do
            platform=${targets[next]}; key=${platform//\//-}
            cp -a --reflink=auto /work/src "/work/targets/$key"
            started[$next]=$SECONDS
            printf '  %-14s running\n' "$platform"
            build_platform "$platform" > "/work/logs/$key.log" 2>&1 &
            running[$next]=$!
            next=$((next + 1))
            active=$((active + 1))
        done
        for index in "${!running[@]}"; do
            pid=${running[$index]}
            if kill -0 "$pid" 2>/dev/null; then continue; fi
            platform=${targets[index]}; key=${platform//\//-}
            elapsed[$index]=$((SECONDS - started[$index]))
            if wait "$pid"; then
                printf '  %-14s done (%ss)\n' "$platform" "${elapsed[$index]}"
            else
                printf '  %-14s failed (%ss)\n' "$platform" "${elapsed[$index]}" >&2
                tail -n 35 "/work/logs/$key.log" >&2
                failed=1
            fi
            unset 'running[$index]'
            active=$((active - 1))
        done
        if (( active )); then sleep .15; fi
    done
    for index in "${!targets[@]}"; do
        platform=${targets[index]}; key=${platform//\//-}
        printf '  %-14s %ss' "$platform" "${elapsed[$index]}"
        if [[ -s /work/logs/$key.link ]]; then printf '; %s' "$(tail -n 1 "/work/logs/$key.link")"; fi
        printf '\n'
        grep '^\(mygo\|post-processing\) finished' "/work/logs/$key.log" || true
    done
    (( failed == 0 )) || die "Packaging failed after $((SECONDS - total))s; no artifacts exported."
    step=$SECONDS
    collect "$uid" "$gid"
    printf 'docker-build: export %ss; container total %ss\n' "$((SECONDS - step))" "$((SECONDS - total))"
}

check_file() {
    local description
    description=$(file -b -- "$1")
    [[ $description =~ $2 ]] || die "$1: unexpected file type: $description"
    printf '  %s: %s\n' "${1##*/}" "$description"
}

verify_macho() {
    local binary=$1 result status=0
    if result=$(rcodesign verify "$binary" 2>/tmp/rcodesign-verify.log); then
        printf '  %s: rcodesign verification passed\n' "${binary##*/}"
    else
        status=$?
        if [[ $result != 'CMS error: missing further values (at position 0)' ]]; then
            printf '%s\n' "$result" >&2
            cat /tmp/rcodesign-verify.log >&2
            return "$status"
        fi
        result=$(rcodesign print-signature-info "$binary")
        grep -Fq 'flags: CodeSignatureFlags(ADHOC)' <<< "$result"
        grep -Eq '^[[:space:]]+cms: null$' <<< "$result"
        printf '  %s: rcodesign code checks passed (ad-hoc; no CMS certificate)\n' "${binary##*/}"
    fi
}

verify_app() {
    local app=$1 version=$2 machine=$3 library
    [[ -f $app/Contents/Resources/AppIcon.icns && -f $app/Contents/_CodeSignature/CodeResources ]] || die 'Missing macOS icon or resource seal.'
    [[ $(stat -c %a "$app/Contents/MacOS/Finallyshell") == 755 ]] || die 'Incorrect macOS executable mode.'
    python3 - "$app/Contents" "$version" <<'PY'
import hashlib
import plistlib
import struct
import sys
from pathlib import Path

contents = Path(sys.argv[1])
with (contents / 'Info.plist').open('rb') as stream:
    plist = plistlib.load(stream)
expected = dict(CFBundleShortVersionString=sys.argv[2], CFBundleVersion=sys.argv[2],
                CFBundleExecutable='Finallyshell', CFBundleIdentifier='dev.finallyshell.app')
for key, value in expected.items():
    if plist.get(key) != value:
        sys.exit(f'{key}: expected {value!r}, got {plist.get(key)!r}')
with (contents / '_CodeSignature/CodeResources').open('rb') as stream:
    resources = plistlib.load(stream)['files2']
for name, seal in resources.items():
    if 'hash2' in seal and hashlib.sha256((contents / name).read_bytes()).digest() != seal['hash2']:
        sys.exit(f'Invalid macOS resource seal: {name}')

def check_signature(path, external_slots):
    data = path.read_bytes()
    if data[:4] != b'\xcf\xfa\xed\xfe':
        sys.exit(f'Not a 64-bit little-endian Mach-O: {path}')
    command = 32
    for _ in range(struct.unpack_from('<I', data, 16)[0]):
        kind, size = struct.unpack_from('<II', data, command)
        if kind == 0x1d:
            offset, length = struct.unpack_from('<II', data, command + 8)
            break
        command += size
    else:
        sys.exit(f'Missing Mach-O code signature: {path}')
    signature = data[offset:offset + length]
    magic, _, count = struct.unpack_from('>III', signature)
    if magic != 0xfade0cc0:
        sys.exit(f'Invalid Mach-O signature header: {path}')
    slots = dict(struct.unpack_from('>II', signature, 12 + index * 8) for index in range(count))
    cms = slots.get(0x10000)
    if cms is not None and struct.unpack_from('>II', signature, cms) != (0xfade0b01, 8):
        sys.exit(f'Expected an empty CMS slot for ad-hoc signing: {path}')
    directory = signature[slots[0]:]
    magic, _, _, flags, hashes, _, special, pages, limit = struct.unpack_from('>9I', directory)
    hash_size, hash_type, _, page_shift = struct.unpack_from('4B', directory, 36)
    if magic != 0xfade0c02 or flags & 2 == 0 or (hash_type, hash_size) != (2, 32):
        sys.exit(f'Expected an ad-hoc SHA-256 code directory: {path}')
    page_size = 1 << page_shift
    if limit != offset or pages != (limit + page_size - 1) // page_size:
        sys.exit(f'Invalid signed code range: {path}')
    for page in range(pages):
        start = page * page_size
        actual = hashlib.sha256(data[start:min(start + page_size, limit)]).digest()
        begin = hashes + page * hash_size
        if actual != directory[begin:begin + hash_size]:
            sys.exit(f'Invalid signed code page {page}: {path}')
    for slot, external in external_slots.items():
        begin = hashes - slot * hash_size
        if slot > special or hashlib.sha256(external.read_bytes()).digest() != directory[begin:begin + hash_size]:
            sys.exit(f'Invalid Mach-O special slot {slot}: {path}')

check_signature(contents / 'MacOS/Finallyshell',
                {1: contents / 'Info.plist', 3: contents / '_CodeSignature/CodeResources'})
for library in (contents / 'Resources').glob('libghostty-vt*.dylib'):
    check_signature(library, {})
PY
    check_file "$app/Contents/MacOS/Finallyshell" "Mach-O.*$machine"
    for library in "$app"/Contents/Resources/libghostty-vt*.dylib; do
        check_file "$library" "Mach-O.*$machine"
        verify_macho "$library"
    done
    verify_macho "$app/Contents/MacOS/Finallyshell"
    printf '  macOS %s: Info.plist and Mach-O signatures verified\n' "$version"
}

verify_all() {
    local no_dmg=$1
    shift
    local version work platform arch deb_version machine raw app archive deb suffix
    local -a candidates
    cd /artifacts
    case $1 in
        darwin/*)
            if (( no_dmg )); then suffix=-${1#*/}.app.tar; else suffix=-${1#*/}.dmg; fi ;;
        linux/*) suffix=-linux-${1#*/}.tar.gz ;;
        windows/*) suffix=-windows-amd64.zip ;;
    esac
    shopt -s nullglob
    candidates=(finallyshell-*"$suffix")
    shopt -u nullglob
    [[ ${#candidates[@]} == 1 ]] || die "Expected one artifact matching finallyshell-*$suffix."
    version=${candidates[0]#finallyshell-}
    version=${version%"$suffix"}
    [[ -s LICENSE.txt && -s THIRD_PARTY_LICENSES.txt ]] || die 'Missing licenses.'
    sha256sum --check --strict SHA256SUMS
    work=$(mktemp -d)
    for platform in "$@"; do
        arch=${platform#*/}
        mkdir -p "$work/${platform//\//-}"
        local dest=$work/${platform//\//-}
        printf '%s\n' "$platform"
        case $platform in
            darwin/*)
                machine=$arch; [[ $arch != amd64 ]] || machine=x86_64
                app=$dest/Finallyshell.app
                if (( no_dmg )); then
                    tar -xf "finallyshell-$version-$arch.app.tar" -C "$dest"
                else
                    raw=$dest/volume.hfs
                    dmg extract "finallyshell-$version-$arch.dmg" "$raw" > "$dest/dmg.log"
                    hfsplus "$raw" ls / | grep -E '^120755 .* Applications$'
                    hfsplus "$raw" ls /Finallyshell.app/Contents/Resources | grep -E '^100755 .* libghostty-vt.*\.dylib$'
                    hfsplus "$raw" ls /Finallyshell.app/Contents/MacOS | grep -E '^100755 .* Finallyshell$'
                    (cd "$dest"; hfsplus "$raw" extractall / > hfs.log)
                    [[ $(cat "$dest/Applications") == /Applications ]] || die 'Incorrect Applications symlink.'
                    cmp "$dest/.VolumeIcon.icns" "$app/Contents/Resources/AppIcon.icns"
                    chmod 755 "$app/Contents/MacOS/Finallyshell"
                fi
                verify_app "$app" "$version" "$machine" ;;
            linux/*)
                machine=x86-64; [[ $arch != arm64 ]] || machine=aarch64
                archive=finallyshell-$version-linux-$arch.tar.gz
                deb_version=${version/-/\~}
                if [[ $deb_version == *~* ]]; then deb_version=${deb_version%%~*}~$(tr '-' '.' <<< "${deb_version#*~}"); fi
                deb=finallyshell_${deb_version}_${arch}.deb
                [[ -s install.sh ]] || die 'Missing Linux install.sh.'
                dpkg-deb -I "$deb" > "$dest/deb.info"
                [[ $(dpkg-deb -f "$deb" Architecture) == "$arch" && $(dpkg-deb -f "$deb" Version) == "$deb_version" ]] || die 'Unexpected Debian architecture or version.'
                dpkg-deb -x "$deb" "$dest/deb"
                tar -xf "$archive" -C "$dest"
                [[ -x $dest/finallyshell && -x $dest/deb/opt/finallyshell/finallyshell ]] || die 'Missing Linux executables.'
                [[ -x $dest/libghostty-vt.so && -x $dest/deb/opt/finallyshell/libghostty-vt.so ]] || die 'Incorrect Linux Ghostty library mode.'
                check_file "$dest/finallyshell" "ELF 64-bit.*$machine"
                check_file "$dest/libghostty-vt.so" "ELF 64-bit.*$machine"
                cmp "$dest/finallyshell" "$dest/deb/opt/finallyshell/finallyshell"
                cmp "$dest/libghostty-vt.so" "$dest/deb/opt/finallyshell/libghostty-vt.so"
                printf '  Debian package and archive verified\n' ;;
            windows/*)
                archive=finallyshell-$version-windows-amd64.zip
                [[ $(unzip -Z1 "$archive" | sort) == $'Finallyshell.exe\nLICENSE.txt\nTHIRD_PARTY_LICENSES.txt\nghostty-vt.dll' ]] || die 'Unexpected Windows ZIP contents.'
                unzip -q "$archive" -d "$dest"
                [[ -x $dest/Finallyshell.exe && -x $dest/ghostty-vt.dll ]] || die 'Incorrect Windows ZIP modes.'
                check_file "$dest/Finallyshell.exe" 'PE32\+.*x86-64'
                check_file "$dest/ghostty-vt.dll" 'PE32\+.*x86-64'
                check_file "finallyshell-setup-$version-amd64.exe" 'PE32.*Nullsoft Installer'
                7zz t "finallyshell-setup-$version-amd64.exe" > "$dest/setup.log" ;;
        esac
    done
    printf 'docker-build: verification passed\n'
}

case $1 in
    build) shift; build_all "$@" ;;
    verify) shift; verify_all "$@" ;;
    *) die 'Invoke build.sh on the host.' ;;
esac
