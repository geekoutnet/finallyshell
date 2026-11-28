#!/bin/sh
# Runs the certificate issuance tests of internal/certs against Pebble,
# Let's Encrypt's test CA, and its challenge test server, in a Linux
# container: Pebble checks HTTP challenges on port 5002 and DNS challenges
# through the test server, which points every name at the container.
#
#   tools/pebble-test.sh            # from the repository root
#   tools/pebble-test.sh -run DNS   # more go test flags
set -eu
cd "$(dirname "$0")/.."
src=$(pwd)
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) src=$(pwd -W) ;; esac
MSYS_NO_PATHCONV=1 exec docker run --rm \
	-v "$src:/src:ro" \
	-v finallyshell-gomod:/go/pkg/mod \
	-v finallyshell-gocache:/root/.cache/go-build \
	-e GOFLAGS=-buildvcs=false \
	golang:1.27.3-bookworm sh -euc '
		go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.8.0 github.com/letsencrypt/pebble/v2/cmd/pebble-challtestsrv@v2.8.0
		pebble_dir=$(go env GOMODCACHE)/github.com/letsencrypt/pebble/v2@v2.8.0
		pebble-challtestsrv -management :8055 -dns01 :8053 -defaultIPv4 127.0.0.1 -defaultIPv6 "" \
			-http01 "" -https01 "" -tlsalpn01 "" -doh "" >/tmp/challtestsrv.log 2>&1 &
		cd "$pebble_dir"
		PEBBLE_VA_NOSLEEP=1 PEBBLE_WFE_NONCEREJECT=0 pebble -config test/config/pebble-config.json \
			-dnsserver 127.0.0.1:8053 >/tmp/pebble.log 2>&1 &
		for i in $(seq 50); do
			curl -sk https://127.0.0.1:14000/dir >/dev/null && break
			sleep 0.2
		done
		cp -r /src /tmp/w && cd /tmp/w
		PEBBLE_DIRECTORY=https://127.0.0.1:14000/dir CHALLTESTSRV=http://127.0.0.1:8055 CHALLTESTSRV_DNS=127.0.0.1:8053 \
			go test -count=1 -race -run "Issue" "$@" ./internal/certs || { echo "--- pebble log"; tail -40 /tmp/pebble.log; exit 1; }
	' sh "$@"
