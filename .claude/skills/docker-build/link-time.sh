#!/usr/bin/env bash
set -euo pipefail
if [[ ${1##*/} == link && ${2:-} != -V=full ]]; then
    exec /usr/bin/time -a -o "$LINK_RSS_FILE" -f 'link: %M KiB peak RSS, %e s' "$@"
fi
exec "$@"
