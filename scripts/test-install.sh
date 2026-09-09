#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/ccu-installer-test.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/mock" "$stage/payload" "$stage/installed"
printf '#!/bin/sh\nprintf "installer-test\\n"\n' > "$stage/payload/claude-codex-usage"
tar -C "$stage/payload" -czf "$stage/archive.tar.gz" ./claude-codex-usage
if command -v sha256sum >/dev/null 2>&1; then digest=$(sha256sum "$stage/archive.tar.gz" | awk '{print $1}')
else digest=$(shasum -a 256 "$stage/archive.tar.gz" | awk '{print $1}'); fi
cat > "$stage/mock/curl" <<'MOCK'
#!/bin/sh
set -eu
url=''; output=''
while [ "$#" -gt 0 ]; do
 case "$1" in -o) output=$2; shift ;; https://*) url=$1 ;; esac
 shift
done
case "$url" in
 */latest) printf 'https://github.com/n-hiraha/claude-codex-usage/releases/tag/v9.9.9' ;;
 */SHA256SUMS) printf '%s  %s\n' "$TEST_DIGEST" "$(cat "$TEST_STAGE/name")" > "$output" ;;
 *.tar.gz) printf '%s' "${url##*/}" > "$TEST_STAGE/name"; cp "$TEST_STAGE/archive.tar.gz" "$output" ;;
 *) exit 1 ;;
esac
MOCK
chmod +x "$stage/mock/curl"
export TEST_STAGE="$stage" TEST_DIGEST="$digest" INSTALL_DIR="$stage/installed" PATH="$stage/mock:$PATH"
sh "$repo_root/scripts/install.sh"
[ "$("$INSTALL_DIR/claude-codex-usage")" = installer-test ]
printf 'keep-existing' > "$INSTALL_DIR/claude-codex-usage"
TEST_DIGEST=0000000000000000000000000000000000000000000000000000000000000000
export TEST_DIGEST
if sh "$repo_root/scripts/install.sh" > "$stage/failure.log" 2>&1; then
 printf 'Installer accepted a corrupt archive\n' >&2; exit 1
fi
[ "$(cat "$INSTALL_DIR/claude-codex-usage")" = keep-existing ]
printf 'Installer: installation succeeded; checksum mismatch preserved existing binary.\n'
