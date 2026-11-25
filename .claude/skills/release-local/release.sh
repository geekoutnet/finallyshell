#!/usr/bin/env bash
# Publishes a GitHub release of dev's HEAD from packages built locally with
# the docker-build skill: the same files, names and notes as the dev-build
# workflow's releases, without waiting for CI. See SKILL.md.
set -euo pipefail

skill_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_dir=$(cd -- "$skill_dir/../../.." && pwd -P)
pack=$repo_dir/.claude/skills/docker-build/build.sh

die() { printf 'release-local: %s\n' "$*" >&2; exit 1; }
say() { printf 'release-local: %s\n' "$*"; }

usage() {
    cat <<'EOF'
Usage: release.sh plan    [--version X.Y.Z] [--branch dev]
       release.sh publish [--version X.Y.Z] [--branch dev] [--reuse]

plan     checks everything and prints the version, commit, files and notes;
         builds nothing and publishes nothing.
publish  builds the five platforms in Docker at that version, verifies them,
         creates the release as a draft, uploads every file (retrying), checks
         the uploads, then publishes it as the latest release.
--reuse  uploads dist/docker/<version>/ as it is instead of building again
         (only when it was built from this same commit).
EOF
}

command=${1:-}
[[ -n $command ]] || { usage >&2; exit 1; }
shift
version='' branch=dev reuse=0
while [[ $# -gt 0 ]]; do
    case $1 in
        --version) [[ $# -ge 2 ]] || die '--version needs a value'; version=$2; shift 2 ;;
        --branch) [[ $# -ge 2 ]] || die '--branch needs a value'; branch=$2; shift 2 ;;
        --reuse) reuse=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done
case $command in plan|publish) ;; *) usage >&2; exit 1 ;; esac

cd -- "$repo_dir"
command -v gh >/dev/null || die 'Install the GitHub CLI (gh) and log in first.'
command -v sha256sum >/dev/null || die 'sha256sum is missing.'

# The repository the release goes to: origin's owner/name.
origin=$(git remote get-url origin)
repo=${origin%.git}
repo=${repo#*github.com[:/]}
[[ $repo =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "origin is not a GitHub repository: $origin"

# What is released is exactly a commit that is on GitHub: the branch's
# head, clean, the same as origin's.
say "fetching $branch and tags from origin"
git fetch -q origin "$branch" --tags
[[ $(git branch --show-current) == "$branch" ]] || die "Check out $branch first (now on $(git branch --show-current))."
[[ -z $(git status --porcelain) ]] || die 'The working tree has changes: commit and push them, or stash them, first.'
head=$(git rev-parse HEAD)
remote=$(git rev-parse "origin/$branch")
[[ $head == "$remote" ]] || die "HEAD $(git rev-parse --short HEAD) is not origin/$branch $(git rev-parse --short "$remote"): push (or pull) first."
short=${head:0:7}

# The version: the newest v2.0.N tag plus one, as dev-build numbers them.
last=$(git tag --list 'v2.0.*' --sort=-v:refname | grep -E '^v2\.0\.[0-9]+$' | head -n 1 || true)
if [[ -z $version ]]; then
    if [[ -z $last ]]; then version=2.0.0; else version="2.0.$(( ${last##*.} + 1 ))"; fi
fi
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "Use a plain version such as 2.0.19, not $version."
tag=v$version
if existing=$(git tag --points-at HEAD --list 'v2.0.*' | head -n 1) && [[ -n $existing && $existing != "$tag" ]]; then
    die "$short is already released as $existing."
fi

# A draft of this version is resumed (an upload that failed half way); a
# published one is never touched.
state=none
if info=$(gh release view "$tag" -R "$repo" --json isDraft,targetCommitish -q '"\(.isDraft) \(.targetCommitish)"' 2>/dev/null); then
    draft=${info%% *} target=${info#* }
    [[ $draft == true ]] || die "$tag is already published on GitHub."
    [[ $target == "$head" || $target == "$branch" ]] || die "The draft $tag points at $target, not $short: delete it on GitHub first."
    state=draft
elif git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    die "The tag $tag already exists without a release: choose another --version."
fi

dist=$repo_dir/dist/docker/$version
files=(
    "finallyshell-$version-arm64.dmg"
    "finallyshell-$version-amd64.dmg"
    "finallyshell-$version-linux-amd64.tar.gz"
    "finallyshell-$version-linux-arm64.tar.gz"
    "finallyshell_${version}_amd64.deb"
    "finallyshell_${version}_arm64.deb"
    "install.sh"
    "finallyshell-setup-$version-amd64.exe"
    "finallyshell-$version-windows-amd64.zip"
    "LICENSE.txt"
    "THIRD_PARTY_LICENSES.txt"
)

# The notes, as dev-build writes them: the commits since the last release,
# then how to install.
notes=$(mktemp)
trap 'rm -f -- "$notes"' EXIT
link="[$short](https://github.com/$repo/commit/$head)"
{
    echo "Build of $branch at $link, packaged locally with Docker. / $branch 分支在 $link 的构建，本地 Docker 打包。"
    echo
    echo "## Changes"
    echo
    if [[ -n $last ]]; then
        git log --no-merges --format='- %s (%h)' "$last..HEAD" | head -n 200
    else
        git log --no-merges --format='- %s (%h)' -n 30 HEAD
    fi
    echo
    cat .github/release-install.md
} > "$notes"

say "repository  $repo"
say "commit      $short ($branch)"
say "version     $tag (previous ${last:-none})"
say "release     $([[ $state == draft ]] && echo "resuming the draft $tag" || echo "new")"
say "artifacts   dist/docker/$version/ ($((${#files[@]} + 1)) files with SHA256SUMS)"
if [[ $command == plan ]]; then
    echo '----- notes -----'
    cat "$notes"
    exit 0
fi

# Build and verify, unless an existing build of this version is reused.
if [[ $reuse == 1 ]]; then
    [[ -d $dist ]] || die "--reuse: dist/docker/$version does not exist."
else
    say "building the five platforms at $version"
    "$pack" build --version "$version"
fi
say 'verifying the packages'
"$pack" verify "dist/docker/$version"

for f in "${files[@]}"; do
    [[ -s $dist/$f ]] || die "missing or empty: dist/docker/$version/$f"
done
# Checksums of exactly the files uploaded.
(cd -- "$dist" && sha256sum -- "${files[@]}" > SHA256SUMS)
files+=(SHA256SUMS)

# A draft first: the tag and the release only appear once every file is
# there, and a failed upload is resumed by running publish again.
if [[ $state == none ]]; then
    say "creating the draft $tag at $short"
    gh release create "$tag" -R "$repo" --draft --target "$head" --title "$tag" --notes-file "$notes"
else
    gh release edit "$tag" -R "$repo" --notes-file "$notes" >/dev/null
fi

upload() { # <file>: up to 6 tries, as GitHub's uploads time out at times
    local file=$1 try
    for try in 1 2 3 4 5 6; do
        if gh release upload "$tag" -R "$repo" --clobber -- "$dist/$file" >/dev/null 2>&1; then
            say "uploaded $file"
            return 0
        fi
        say "upload of $file failed (try $try), retrying"
        sleep $((try * 5))
    done
    return 1
}
for f in "${files[@]}"; do
    upload "$f" || die "could not upload $f: the draft $tag stays; run publish again to resume."
done

# Every file is there with its size before publishing.
assets=$(gh release view "$tag" -R "$repo" --json assets -q '.assets[] | "\(.name) \(.size)"')
for f in "${files[@]}"; do
    size=$(wc -c < "$dist/$f" | tr -d ' ')
    grep -qxF -- "$f $size" <<<"$assets" || die "$f is not on the draft with $size bytes: run publish again."
done

say "publishing $tag as the latest release"
gh release edit "$tag" -R "$repo" --draft=false --latest >/dev/null
git fetch -q origin --tags
url=$(gh release view "$tag" -R "$repo" --json url -q .url)
say "published $url"
