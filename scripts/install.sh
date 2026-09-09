#!/bin/sh
set -eu

name=claude-codex-usage
repo=n-hiraha/claude-codex-usage
fail() { printf '%s\n' "$*" >&2; exit 1; }
for tool in curl tar mktemp; do command -v "$tool" >/dev/null 2>&1 || fail "Required command: $tool"; done
case $(uname -s) in Darwin) platform=darwin ;; Linux) platform=linux ;; *) fail 'Supported systems: macOS and Linux' ;; esac
case $(uname -m) in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) fail 'Supported CPUs: ARM64 and AMD64' ;; esac
if command -v sha256sum >/dev/null 2>&1; then checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum=shasum
else fail 'Required command: sha256sum or shasum'; fi

version=${VERSION:-}
if [ -z "$version" ]; then
  latest=$(curl -fsSL --connect-timeout 10 --max-time 60 -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
  version=${latest##*/}
fi
version=${version#v}
case "$version" in ''|*[!0-9A-Za-z.-]*) fail 'Invalid release version' ;; esac
case "$version" in [0-9]*) ;; *) fail 'Cannot determine release version' ;; esac
archive="$name-$version-$platform-$arch.tar.gz"
base="https://github.com/$repo/releases/download/v$version"
stage=$(mktemp -d "${TMPDIR:-/tmp}/claude-codex-install.XXXXXX")
pending=''
cleanup() { rm -rf "$stage"; if [ -n "$pending" ]; then rm -f "$pending"; fi; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
printf 'Downloading %s\n' "$archive"
curl -fsSL --connect-timeout 10 --max-time 180 "$base/$archive" -o "$stage/$archive"
curl -fsSL --connect-timeout 10 --max-time 60 "$base/SHA256SUMS" -o "$stage/SHA256SUMS"
expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$stage/SHA256SUMS")
[ "${#expected}" -eq 64 ] || fail 'Missing or invalid checksum'
case "$expected" in *[!0-9a-fA-F]*) fail 'Invalid checksum' ;; esac
if [ "$checksum" = sha256sum ]; then actual=$(sha256sum "$stage/$archive" | awk '{print $1}')
else actual=$(shasum -a 256 "$stage/$archive" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || fail 'Checksum mismatch; installation stopped'
tar -xzf "$stage/$archive" -C "$stage" ./claude-codex-usage
[ -f "$stage/$name" ] && [ ! -L "$stage/$name" ] || fail 'Release binary not found'
install_dir=${INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$install_dir"
pending=$(mktemp "$install_dir/.$name.XXXXXX")
cp "$stage/$name" "$pending"
chmod 755 "$pending"
mv -f "$pending" "$install_dir/$name"
pending=''
printf 'Installed v%s to %s/%s\n' "$version" "$install_dir" "$name"
printf 'Try: "%s/%s" usage --demo\n' "$install_dir" "$name"
