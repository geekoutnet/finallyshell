FROM golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 AS base

FROM base AS libdmg
RUN apt-get update && apt-get install -y --no-install-recommends cmake=3.25.1-1 zlib1g-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN curl -fSL --retry 5 --retry-max-time 300 \
      https://codeload.github.com/fanquake/libdmg-hfsplus/tar.gz/7ac55ec64c96f7800d9818ce64c79670e7f02b67 -o /tmp/libdmg.tar.gz \
    && echo '56fbdc48ec110966342f0ecddd6f8f89202f4143ed2a3336e42bbf88f940850c  /tmp/libdmg.tar.gz' | sha256sum -c - \
    && tar -xzf /tmp/libdmg.tar.gz --strip-components=1 \
    && sed -i 's/record->permissions.fileMode |= S_IFLNK;/record->permissions.fileMode = S_IFLNK | 0755;/' hfs/catalog.c \
    && sed -i 's/HFSX_VOLUME_TYPE/"Apple_HFS"/; s/Mac_OS_X (Apple_HFSX : 3)/Finallyshell (Apple_HFS : 3)/' dmg/dmglib.c \
    && cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_DISABLE_FIND_PACKAGE_OpenSSL=ON \
    && cmake --build build --parallel --target dmg-bin hfsplus

FROM base AS codesign
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) arch=x86_64; sha=dbe85cedd8ee4217b64e9a0e4c2aef92ab8bcaaa41f20bde99781ff02e600002 ;; \
      arm64) arch=aarch64; sha=4af92c87ddf52f5f2d1258a3b4e56c7dcb8f1b2468df744976c5f139e031961f ;; \
      *) exit 1 ;; \
    esac \
    && version=0.29.0 && name="apple-codesign-$version-$arch-unknown-linux-musl" \
    && curl -fSL --retry 5 --retry-max-time 300 \
      "https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign/$version/$name.tar.gz" -o /tmp/codesign.tar.gz \
    && echo "$sha  /tmp/codesign.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/codesign.tar.gz --strip-components=1 -C /usr/local/bin "$name/rcodesign"

FROM base
LABEL dev.finallyshell.docker-build=1
RUN sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends nsis=3.08-3+deb12u1 hfsprogs=540.1.linux3-5+b1 jq zip unzip git ca-certificates file 7zip python3 time \
    && rm -rf /var/lib/apt/lists/*
COPY --from=libdmg /src/build/dmg/dmg /src/build/hfs/hfsplus /usr/local/bin/
COPY --from=codesign /usr/local/bin/rcodesign /usr/local/bin/
ENV GOTOOLCHAIN=local GOMODCACHE=/go/pkg/mod GOCACHE=/root/.cache/go-build
