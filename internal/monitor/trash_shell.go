package monitor

const trashShell = `export LC_ALL='C' TZ="${TZ-:/etc/localtime}"
umask '077'
exists() { [ -e "$1" ] || [ -L "$1" ]; }
fail() { printf '%s\n' "$1" >&2; exit '1'; }
unsafe() { printf 'path\n'; fail 'Unsafe or protected path'; }
permission() { printf 'permission\n'; fail "$1"; }
changed() { printf 'changed\n'; fail 'Trash item or destination changed'; }
canonical() { [ "$(realpath '-m' '--' "$1")" = "$1" ]; }
trusted() {
    [ "$root_trash" = '1' ] || return '0';
    ancestor=$1;
    while [ "$ancestor" != '/' ]; do
        if exists "$ancestor"; then
            case "$(stat '-c' '%u:%a' '--' "$ancestor")" in
                '0:'*[0145][0145]) :;;
                '0:'1[0-7][0-7][0-7]|'0:'3[0-7][0-7][0-7]|'0:'5[0-7][0-7][0-7]|'0:'7[0-7][0-7][0-7]) :;;
                *) return '1';;
            esac
        fi
        ancestor=${ancestor%/*}; [ -n "$ancestor" ] || ancestor='/';
    done
}
private() {
    [ ! -L "$1" ] && [ -d "$1" ] &&
        [ "$(stat '-c' '%u:%a' '--' "$1")" = "$uid:700" ];
}
branch() {
    [ ! -L "$1" ] && [ -d "$1" ] || return '1';
    case "$(stat '-c' '%u:%a' '--' "$1")" in
        "$uid":*[0145][0145]) return '0';;
    esac
    return '1';
}
ready_store() {
    canonical "$1" && trusted "$1" && private "$1" && branch "$1/files" && branch "$1/info"
}
init_roots() {
    if [ "$root_trash" = '1' ]; then export PATH='/usr/sbin:/usr/bin:/sbin:/bin'; fi
    command '-v' 'realpath' >/dev/null || fail 'GNU realpath is required';
    [ -r '/proc/self/mountinfo' ] || fail 'Linux mount inventory is unavailable';
    uid=$(id '-u') || exit '1';
    if [ "$root_trash" = '1' ]; then
        [ "$uid" = '0' ] || fail 'Root authorization is required';
        home=$(getent 'passwd' '0' | cut '-d' ':' '-f' '6');
        data="$home/.local/share";
    else
        home=$HOME;
        case ${XDG_DATA_HOME-} in
            /*) data=$XDG_DATA_HOME ;;
            *) data="$home/.local/share" ;;
        esac
    fi
    if [ "$uid" = '0' ]; then root_trash='1'; fi
    case "$home" in ''|'/') fail 'Invalid home directory';; /*) :;; *) fail 'Invalid home directory';; esac
    home=$(realpath '-m' '--' "$home") || exit '1';
    data=$(realpath '-m' '--' "$data") || exit '1';
    home_store="${data%/}/Trash";
    mountpoints=$(awk '{print $5}' '/proc/self/mountinfo') || exit '1';
    [ -n "$mountpoints" ] || fail 'Empty mount inventory';
}
mount_paths() {
    printf '%s\n' "$mountpoints" | while IFS= read -r escaped; do
        case "$escaped" in *'\012'*|*'\015'*) continue;; esac
        point=$(printf '%b' "$escaped");
        case "$point" in /proc|/proc/*|/sys|/sys/*|/dev|/dev/*) continue;; esac
        printf '%s\n' "$point";
    done | sort '-u';
}
known_stores() {
    if ready_store "$home_store"; then printf '%s\n\n' "$home_store"; fi
    mount_paths | while IFS= read -r point; do
        prefix=${point%/};
        candidate="$prefix/.Trash-$uid";
        if [ "$candidate" != "$home_store" ] && ready_store "$candidate"; then
            printf '%s\n%s\n' "$candidate" "$point";
        fi
        shared="$prefix/.Trash";
        candidate="$shared/$uid";
        if [ "$candidate" != "$home_store" ] && [ -k "$shared" ] && ready_store "$candidate"; then
            printf '%s\n%s\n' "$candidate" "$point";
        fi
    done
}
safe_origin() {
    canonical "$1" || unsafe;
    case "$home" in "$2"|"$2/"*) unsafe;; esac
    case "$home_store" in "$2"|"$2/"*) unsafe;; esac
    case "$2" in "$home_store"/*) unsafe;; esac
    case "$2" in /proc|/proc/*|/sys|/sys/*|/dev|/dev/*|*/Trash|*/Trash/*|*/.Trash|*/.Trash/*|*/.Trash-*) unsafe;; esac
}
no_mounts_below() {
    found=$(mount_paths | while IFS= read -r point; do
        case "$point" in "$1"|"$1/"*) printf 'yes'; break;; esac
    done);
    [ -z "$found" ] || unsafe;
}
mount_for() {
    mount_paths | while IFS= read -r point; do
        case "$1" in "$point"|"${point%/}/"*) printf '%s\n' "$point";; esac
    done | awk 'length > n {p=$0;n=length} END {print p}';
}
same_filesystem() {
    first_dev=$(stat '-c' '%d' '--' "$1") || return '1';
    second_dev=$(stat '-c' '%d' '--' "$2") || return '1';
    [ "$first_dev" = "$second_dev" ] && [ "$(mount_for "$1")" = "$(mount_for "$2")" ];
}
file_stamp() { stat '-c' '%d:%i:%f:%s:%y:%z' '--' "$1"; }
item_stamp() {
    file_stamp "$1" || return '1';
    if exists "$2"; then file_stamp "$2"; fi
}
size_of() {
    if [ -d "$1" ] && [ ! -L "$1" ]; then
        blocks=$(du '-s' '-B1' '-x' '--' "$1") || return '1';
        printf '%s' "$blocks" | cut '-f' '1';
    else
        stat '-c' '%s' '--' "$1";
    fi
}
prepare_store() {
    original_parent=${1%/*}; [ -n "$original_parent" ] || original_parent='/';
    ancestor=$data;
    while [ ! -d "$ancestor" ]; do ancestor=${ancestor%/*}; [ -n "$ancestor" ] || ancestor='/'; done
    mount='';
    store=$home_store;
    if same_filesystem "$original_parent" "$ancestor"; then
        mkdir '-p' '--' "$data" || permission 'Cannot create the data directory';
    else
        mount=$(mount_for "$original_parent");
        [ -n "$mount" ] || fail 'Unable to locate the source filesystem';
        store="${mount%/}/.Trash-$uid";
    fi
    canonical "$store" || fail 'Trash directory is not a real directory';
    trusted "$store" || fail 'Root trash needs a trusted parent directory';
    if ! exists "$store"; then
        mkdir '-m' '700' '--' "$store" || exists "$store" || permission 'Cannot create the trash directory';
    fi
    private "$store" || fail 'Trash directory must be owned by its user and have mode 0700';
    for child in "$store/files" "$store/info"; do
        if ! exists "$child"; then mkdir '--' "$child" || exists "$child" || permission 'Cannot create the trash subdirectory'; fi
        branch "$child" || fail 'Invalid trash subdirectory';
    done
    same_filesystem "$original_parent" "$store/files" || fail 'Trash is not on the source filesystem';
}
encode_name() {
    printf '%s' "$1" | od '-An' '-v' '-tu1' | awk '
        { for (i=1;i<=NF;i++) {
            b=$i+0;
            if (b>=48 && b<=57 || b>=65 && b<=90 || b>=97 && b<=122 || b==45 || b==46 || b==95 || b==126)
                printf "%c", b;
            else printf "%%%02X", b;
        } }';
}
listed_size() {
    cache="$1/directorysizes";
    if [ "$5" = 'd' ] && [ -f "$cache" ] && [ ! -L "$cache" ] && [ -f "$4" ] && [ ! -L "$4" ]; then
        modified=$(stat '-c' '%Y' '--' "$4");
        encoded=$(encode_name "$3");
        cached=$(awk '-v' "key=$encoded" '-v' "time=$modified" '$2 == time && $3 == key && $1 ~ /^[0-9]+$/ {print $1; exit}' "$cache");
        if [ -n "$cached" ]; then printf '%s' "$cached"; return; fi
    fi
    size_of "$2";
}
update_cache() (
    root=$1 name=$2 size=$3;
    umask '077';
    cache="$root/directorysizes";
    [ ! -L "$cache" ] || exit '0';
    if [ -n "$size" ] || [ -f "$cache" ]; then
        encoded=$(encode_name "$name") || exit '0';
        tmp=$(mktemp "$root/.directorysizes.XXXXXXXX") || exit '0';
        trap 'rm "-f" "--" "$tmp"' 'EXIT';
        if [ -f "$cache" ]; then
            awk '-v' "key=$encoded" '$3 != key' "$cache" >"$tmp" || exit '0';
        fi
        if [ -n "$size" ]; then
            modified=$(stat '-c' '%Y' '--' "$root/info/$name.trashinfo") || exit '0';
            printf '%s %s %s\n' "$size" "$modified" "$encoded" >>"$tmp" || exit '0';
        fi
        mv '-fT' '--' "$tmp" "$cache";
    fi
)
put() (
    original=$1 encoded=$2 note=$3 expected=${4-};
    parent=${original%/*}; [ -n "$parent" ] || parent='/';
    safe_origin "$parent" "$original";
    trusted "$parent" || fail 'Root trash needs a trusted source folder';
    if ! exists "$original"; then
        detail=$(stat '-c' '%a' '--' "$original" 2>&1);
        case "$detail" in *'Permission denied'*|*'Operation not permitted'*) permission "$detail";; esac
        fail 'Source does not exist';
    fi
    [ -L "$original" ] || canonical "$original" || unsafe;
    [ -L "$original" ] || no_mounts_below "$original";
    [ -w "$parent" ] && [ -x "$parent" ] || permission 'Cannot write to the source folder';
    prepare_store "$original";
    if [ -n "$mount" ]; then
        prefix=$(encode_name "${mount%/}" | sed 's/%2F/\//g');
        encoded=${encoded#"$prefix"/};
    fi
    name=${original##*/};
    limit=$(getconf 'NAME_MAX' "$store/info" 2>/dev/null);
    case "$limit" in ''|*[!0-9]*) limit='255';; esac
    limit=$((limit-15));
    [ "$limit" -gt '0' ] || fail 'Trash filesystem name limit is too small';
    mode=$(stat '-c' '%a' '--' "$original") || exit '1';
    owner=$(stat '-c' '%U:%G' '--' "$original") || exit '1';
    size=$(size_of "$original") || exit '1';
    deleted=$(date '+%Y-%m-%dT%H:%M:%S') || exit '1';
    seq='1';
    stem=$(printf '%s' "$name" | cut '-b' "1-$limit");
    name=$(printf '%s' "$name" | cut '-b' "1-$((limit+5))");
    start_stamp=$(file_stamp "$original") || exit '1';
    if [ -n "$expected" ] && [ "$start_stamp" != "$expected" ]; then changed; fi
    reserved='';
    trap 'if [ -n "$reserved" ] && ! exists "$store/files/$name"; then rm "-f" "--" "$reserved"; fi' 'EXIT';
    trap 'exit 1' 'HUP' 'INT' 'TERM';
    set '-C';
    while :; do
        info="$store/info/$name.trashinfo";
        if ! exists "$store/files/$name" && ! exists "$info"; then
            if { reserve_info 3>"$info"; } 2>/dev/null; then break; fi
            [ -z "$reserved" ] || fail 'Cannot write trash metadata';
            exists "$info" || fail 'Cannot reserve a trash item name';
        fi
        seq=$((seq+1));
        [ "$seq" -le '10000' ] || fail 'Too many items with the same name';
        name="$stem.$seq";
    done
    set '+C';
    canonical "$parent" && trusted "$parent" && ready_store "$store" || unsafe;
    [ "$(file_stamp "$original")" = "$start_stamp" ] || changed;
    if ! detail=$(mv '-nT' '--' "$original" "$store/files/$name" 2>&1); then
        case "$detail" in *'Permission denied'*|*'Operation not permitted'*) exists "$original" && ! exists "$store/files/$name" && permission "$detail";; esac
        fail "$detail";
    fi
    exists "$original" && fail 'Source was not moved';
    if [ "${5-}" = 'quiet' ]; then
        printf '%s' "$store/files/$name";
    else
        printf '%s\000%s\000%s\000' "$store" "$mount" "$name";
    fi
    if [ -d "$store/files/$name" ] && [ ! -L "$store/files/$name" ]; then
        update_cache "$store" "$name" "$size" || :;
    fi
)
reserve_info() {
    reserved=$info;
    printf '[Trash Info]\nPath=%s\nDeletionDate=%s\nX-Finallyshell-Mode=%s\nX-Finallyshell-Owner=%s\nX-Finallyshell-Size=%s\nX-Finallyshell-Note=%s\n' "$encoded" "$deleted" "$mode" "$owner" "$size" "$note" >&3;
}
verify_item() {
    known_stores | while IFS= read -r store_path && IFS= read -r store_mount; do
        if [ "$store_path" = "$1" ]; then printf 'yes'; break; fi
    done | grep '-qx' 'yes' || fail 'Trash storage is no longer available';
    trashed="$1/files/$2";
    info="$1/info/$2.trashinfo";
    [ "$(item_stamp "$trashed" "$info")" = "$3" ] || changed;
}
restore() (
    store=$1 name=$2 destination=$4 choice=$5 expected=$6;
    verify_item "$1" "$2" "$3";
    parent=${destination%/*}; [ -n "$parent" ] || parent='/';
    safe_origin "$parent" "$destination";
    trusted "$parent" || fail 'Root restore needs a trusted destination folder';
    if [ ! -L "$trashed" ]; then no_mounts_below "$trashed"; fi
    if exists "$destination"; then
        stamp=$(file_stamp "$destination") || exit '1';
        if [ "$choice" = 'check' ] || [ "$expected" != "$stamp" ]; then
            printf 'conflict\000%s\000' "$stamp";
            exit '0';
        fi
        if [ "$choice" = 'replace' ]; then
            same_filesystem "$parent" "$store/files" || fail 'Original location is on a different filesystem';
            encoded=$(encode_name "$destination");
            encoded=$(printf '%s' "$encoded" | sed 's/%2F/\//g');
            if ! replacement=$(put "$destination" "$encoded" "${7-}" "$stamp" 'quiet'); then
                [ "$replacement" = 'changed' ] && changed;
                exit '1';
            fi
            printf 'displaced\000%s\000' "$replacement";
        else
            base=$destination;
            destination="$base (restored)";
            seq='1';
            while exists "$destination"; do
                seq=$((seq+1));
                [ "$seq" -le '10000' ] || fail 'Too many restored copies';
                destination="$base (restored $seq)";
            done
        fi
    elif [ "$choice" = 'replace' ]; then
        changed;
    fi
    ancestor=$parent;
    while [ ! -d "$ancestor" ]; do ancestor=${ancestor%/*}; [ -n "$ancestor" ] || ancestor='/'; done
    same_filesystem "$ancestor" "$store/files" || fail 'Original location is on a different filesystem';
    mkdir '-p' '--' "$parent" || exit '1';
    safe_origin "$parent" "$destination";
    trusted "$parent" && ready_store "$store" || unsafe;
    same_filesystem "$parent" "$store/files" || fail 'Original location is on a different filesystem';
    mv '-nT' '--' "$trashed" "$destination" || exit '1';
    if exists "$trashed"; then
        stamp=$(file_stamp "$destination") || exit '1';
        printf 'conflict\000%s\000' "$stamp";
        exit '0';
    fi
    printf '%s\000' "$destination";
    rm '-f' '--' "$info" || exit '1';
    update_cache "$store" "$name" '' || :;
)
purge() (
    verify_item "$1" "$2" "$3";
    if [ ! -L "$trashed" ]; then no_mounts_below "$trashed"; fi
    rm '-rf' '--one-file-system' '--' "$trashed" || exit '1';
    exists "$trashed" && fail 'Trash item was not deleted';
    rm '-f' '--' "$info" || exit '1';
    update_cache "$1" "$2" '' || :;
)
init_roots
`

const trashListShell = `known_stores | while IFS= read -r store && IFS= read -r mount; do
    for file in "$store/files/"* "$store/files/".[!.]* "$store/files/"..?*; do
        exists "$file" || continue;
        name=${file##*/};
        case "$name" in *'
'*|*"$(printf '\015')"*) continue;; esac
        info="$store/info/$name.trashinfo";
        type='f'; [ -d "$file" ] && [ ! -L "$file" ] && type='d';
        stamp=$(item_stamp "$file" "$info") || continue;
        mode=$(stat '-c' '%a' '--' "$file") || continue;
        owner=$(stat '-c' '%U:%G' '--' "$file") || continue;
        size=$(listed_size "$store" "$file" "$name" "$info" "$type");
        case "$size" in ''|*[!0-9]*)
            printf 'warning\000%s\000' "$file";
            size='';;
        esac
        printf 'item\000%s\000%s\000%s\000' "$store" "$mount" "$name";
        if [ -f "$info" ] && [ ! -L "$info" ]; then head '-c' '65536' '--' "$info" | tr '-d' '\000'; fi
        printf '\000%s\000%s\000%s\000%s\000%s\000' "$type" "$mode" "$owner" "$size" "$stamp";
    done
    for info in "$store/info/"*.trashinfo "$store/info/".[!.]*.trashinfo "$store/info/"..?*.trashinfo; do
        [ -f "$info" ] && [ ! -L "$info" ] || continue;
        name=${info##*/}; name=${name%.trashinfo};
        exists "$store/files/$name" && continue;
        modified=$(stat '-c' '%Y' '--' "$info") || continue;
        now=$(date '+%s') || continue;
        [ "$((now-modified))" -ge '300' ] || continue;
        exists "$store/files/$name" && continue;
        if rm '-f' '--' "$info"; then
            printf 'orphan\000%s\000' "$info";
            update_cache "$store" "$name" '';
        fi
    done
done
printf 'end\000'
`
