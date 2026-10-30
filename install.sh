#!/usr/bin/env sh
set -eu
case "$(uname -s)" in
 Linux) platform=linux ;;
 Darwin) platform=darwin ;;
 *) echo 'Use install.ps1 on Windows.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
 x86_64|amd64) arch=amd64 ;;
 arm64|aarch64) arch=arm64 ;;
 *) echo 'No released binary for this architecture.' >&2; exit 1 ;;
esac
asset="ggrun-client-$platform-$arch"
release="https://github.com/raketenkater/ggrun-client/releases/latest/download"
if [ -n "${GGRUN_CLIENT_VERSION:-}" ]; then
 case "$GGRUN_CLIENT_VERSION" in *[!a-zA-Z0-9._-]*|'') echo 'Invalid release version.' >&2; exit 1 ;; esac
 release="https://github.com/raketenkater/ggrun-client/releases/download/$GGRUN_CLIENT_VERSION"
fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl --fail --location --retry 3 --proto '=https' "$release/$asset" -o "$tmp/$asset"
curl --fail --location --retry 3 --proto '=https' "$release/SHA256SUMS" -o "$tmp/SHA256SUMS"
expected="$(awk -v name="$asset" '$2 == name {print $1}' "$tmp/SHA256SUMS")"
if [ -z "$expected" ]; then echo 'Release checksum is missing.' >&2; exit 1; fi
if command -v sha256sum >/dev/null 2>&1; then
 actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
 actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ "$expected" != "$actual" ]; then echo 'Release checksum mismatch.' >&2; exit 1; fi
destination="${GGRUN_CLIENT_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$destination"
install -m 755 "$tmp/$asset" "$destination/ggrun-client"
"$destination/ggrun-client" version
printf 'Installed in %s. Add that directory to PATH if needed, then run ggrun-client --help.\n' "$destination"
