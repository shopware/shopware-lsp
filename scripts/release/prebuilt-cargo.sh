#!/bin/sh
# Stand-in for `cargo` in .goreleaser.yaml. GoReleaser's Rust builder calls
#   <tool> build --target=<triple> --release --package=swlsp-cli
# and then collects target/<triple>/release/<binary>. The release binaries are
# compiled natively per target by .github/workflows/build-binaries.yml and
# placed there beforehand, so this only checks that the expected one exists.
set -eu

target=""
for argument in "$@"; do
  case "$argument" in
    --target=*) target="${argument#--target=}" ;;
  esac
done
if [ -z "$target" ]; then
  echo "prebuilt-cargo: missing --target=<triple> in: $*" >&2
  exit 2
fi

case "$target" in
  *-windows-*) binary="target/$target/release/shopware-lsp.exe" ;;
  *) binary="target/$target/release/shopware-lsp" ;;
esac
if [ ! -s "$binary" ]; then
  echo "prebuilt-cargo: $binary is missing; download the build-binaries artifacts first" >&2
  exit 1
fi
echo "prebuilt-cargo: using $binary" >&2
