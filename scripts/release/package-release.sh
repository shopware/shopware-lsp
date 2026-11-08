#!/usr/bin/env bash
# Package the staged release binaries (scripts/release/stage-binaries.sh lays
# them out as target/<triple>/release/shopware-lsp[.exe]) into one zip per
# platform plus checksums.txt:
#
#   scripts/release/package-release.sh <version> <output-dir>
#
# Archive names keep the former GoReleaser scheme
# (shopware-lsp_<version>_<os>_<arch>.zip). Linux archives carry the glibc
# build; *_musl archives carry the static musl build for Alpine. Each archive
# holds the binary, LICENSE, README.md and THIRD_PARTY_NOTICES.md at its root.
set -euo pipefail

version="${1:?usage: package-release.sh <version> <output-dir>}"
output="${2:?usage: package-release.sh <version> <output-dir>}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
target_dir="${CARGO_TARGET_DIR:-$repository_root/target}"

# <rust triple> <archive platform suffix>
platforms=(
  "x86_64-apple-darwin darwin_amd64"
  "aarch64-apple-darwin darwin_arm64"
  "x86_64-unknown-linux-gnu linux_amd64"
  "aarch64-unknown-linux-gnu linux_arm64"
  "armv7-unknown-linux-gnueabihf linux_arm"
  "x86_64-unknown-linux-musl linux_amd64_musl"
  "aarch64-unknown-linux-musl linux_arm64_musl"
  "armv7-unknown-linux-musleabihf linux_arm_musl"
  "x86_64-pc-windows-msvc windows_amd64"
)

mkdir -p "$output"
output="$(cd "$output" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for entry in "${platforms[@]}"; do
  read -r triple platform <<<"$entry"
  binary=shopware-lsp
  case "$triple" in *-windows-*) binary=shopware-lsp.exe ;; esac
  source="$target_dir/$triple/release/$binary"
  if [ ! -s "$source" ]; then
    echo "package-release: missing $source; run stage-binaries.sh first" >&2
    exit 1
  fi
  staging="$work/$platform"
  mkdir -p "$staging"
  cp "$source" "$staging/$binary"
  chmod 755 "$staging/$binary"
  cp "$repository_root/LICENSE" "$repository_root/README.md" \
    "$repository_root/THIRD_PARTY_NOTICES.md" "$staging/"
  archive="shopware-lsp_${version}_${platform}.zip"
  rm -f "$output/$archive"
  # -X drops extra file attributes so archives do not depend on the host.
  (cd "$staging" && zip -q -X "$output/$archive" ./*)
  echo "packaged $archive"
done

(cd "$output" && sha256sum shopware-lsp_"${version}"_*.zip > checksums.txt)
echo "wrote $output/checksums.txt"
