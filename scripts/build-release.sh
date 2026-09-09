#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

name=claude-codex-usage
version=${VERSION:-}
if [ -z "$version" ]; then
	version=$(git describe --tags --exact-match 2>/dev/null || git describe --tags --always 2>/dev/null || printf '%s' dev)
fi
version=${version#v}

out_dir=${DIST_DIR:-dist}
mkdir -p "$out_dir"
rm -f "$out_dir"/"$name"-*.tar.gz "$out_dir"/SHA256SUMS

gocache=${GOCACHE:-/tmp/claude-codex-go-cache}
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	os=${target%/*}
	arch=${target#*/}
	stage=$(mktemp -d "${TMPDIR:-/tmp}/claude-codex-release.XXXXXX")
	trap 'rm -rf "$stage"' EXIT HUP INT TERM
	GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 GOCACHE="$gocache" \
		go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/$name" ./cmd/claude-codex-usage
	cp README.md accounts.example.json "$stage/"
	mkdir -p "$stage/docs"
	cp docs/statusbar.svg "$stage/docs/"
	if [ -f LICENSE ]; then cp LICENSE "$stage/"; fi
	tarball="$out_dir/$name-$version-$os-$arch.tar.gz"
	tar -C "$stage" -czf "$tarball" .
	rm -rf "$stage"
	trap - EXIT HUP INT TERM
done

(cd "$out_dir" && shasum -a 256 "$name"-*.tar.gz > SHA256SUMS)
printf '%s\n' "Created release archives in $out_dir (version $version)."
