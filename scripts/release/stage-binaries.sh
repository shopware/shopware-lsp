#!/usr/bin/env bash
# Move build-binaries artifacts into the layout GoReleaser and the VSIX
# packager read: target/<triple>/release/shopware-lsp[.exe] (under
# CARGO_TARGET_DIR instead of target/ when that is set; GoReleaser itself
# always reads target/).
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
expected=(
  x86_64-apple-darwin
  aarch64-apple-darwin
  x86_64-unknown-linux-gnu
  aarch64-unknown-linux-gnu
  armv7-unknown-linux-gnueabihf
  x86_64-pc-windows-msvc
)

for triple in "${expected[@]}"; do
  binary=shopware-lsp
  case "$triple" in *-windows-*) binary=shopware-lsp.exe ;; esac
  from="$source_dir/server-$triple/$binary"
  if [ ! -s "$from" ]; then
    echo "stage-binaries: missing $from" >&2
    exit 1
  fi
  to="$target_dir/$triple/release"
  mkdir -p "$to"
  cp "$from" "$to/$binary"
  chmod 755 "$to/$binary"
  echo "staged $to/$binary"
done
