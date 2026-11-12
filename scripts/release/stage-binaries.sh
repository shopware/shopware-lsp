#!/usr/bin/env bash
# Move build-binaries artifacts into the two layouts the release reads:
#
# - target/<triple>/release/shopware-lsp[.exe] for the VSIX packager
#   (scripts/build-vsix-release.mjs);
# - target/prebuilt/<variant>/<os>_<arch>/shopware-lsp[.exe] for GoReleaser's
#   prebuilt builder (.goreleaser.yaml), which addresses a binary by Go's
#   os/arch names. <variant> is `default` (glibc on Linux) or `musl`.
#
# Both live under CARGO_TARGET_DIR instead of target/ when that is set.
#
#   scripts/release/stage-binaries.sh <downloaded-artifacts-dir>
#
# The input holds one directory per artifact, named server-<triple>, as
# actions/download-artifact writes them. Artifacts lose the executable bit,
# so it is restored here.
set -euo pipefail

source_dir="${1:?usage: stage-binaries.sh <downloaded-artifacts-dir>}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
target_dir="${CARGO_TARGET_DIR:-$repository_root/target}"
# <rust triple> <prebuilt variant> <go os>_<go arch>
expected=(
  "x86_64-apple-darwin default darwin_amd64"
  "aarch64-apple-darwin default darwin_arm64"
  "x86_64-unknown-linux-gnu default linux_amd64"
  "aarch64-unknown-linux-gnu default linux_arm64"
  "armv7-unknown-linux-gnueabihf default linux_arm"
  "x86_64-unknown-linux-musl musl linux_amd64"
  "aarch64-unknown-linux-musl musl linux_arm64"
  "armv7-unknown-linux-musleabihf musl linux_arm"
  "x86_64-pc-windows-msvc default windows_amd64"
)

for entry in "${expected[@]}"; do
  read -r triple variant platform <<<"$entry"
  binary=shopware-lsp
  case "$triple" in *-windows-*) binary=shopware-lsp.exe ;; esac
  from="$source_dir/server-$triple/$binary"
  if [ ! -s "$from" ]; then
    echo "stage-binaries: missing $from" >&2
    exit 1
  fi
  for to in "$target_dir/$triple/release" "$target_dir/prebuilt/$variant/$platform"; do
    mkdir -p "$to"
    cp "$from" "$to/$binary"
    chmod 755 "$to/$binary"
    echo "staged $to/$binary"
  done
done
