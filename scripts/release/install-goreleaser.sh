#!/usr/bin/env bash
# Install a pinned, checksum-verified GoReleaser (Linux x86_64) into $1.
# GoReleaser only packages here: the binaries are built natively per target.
# >= 2.20 is required so the Rust builder maps armv7 to armhf/armv7 packages.
set -euo pipefail

destination="${1:?usage: install-goreleaser.sh <bin-dir>}"
version=v2.20.0
asset=goreleaser_Linux_x86_64.tar.gz
sha256=d2483159e6007f814132cb56635ad30a70013dbeb813d6033a7e01adf6ae108c

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl -fsSL -o "$work/$asset" \
  "https://github.com/goreleaser/goreleaser/releases/download/$version/$asset"
echo "$sha256  $work/$asset" | sha256sum --check --strict -
tar -xzf "$work/$asset" -C "$work" goreleaser
mkdir -p "$destination"
install -m 755 "$work/goreleaser" "$destination/goreleaser"
"$destination/goreleaser" --version | grep GitVersion
