# Releasing Shopware LSP

This document describes how to cut a release of the Shopware Language Server:
the Rust server binaries, the platform-specific VSIX packages, the GitHub release, the
Homebrew cask, and the Marketplace publications.

Releases are fully automated. A maintainer's only manual action is pushing a
version tag; GitHub Actions does everything else.

## Release channels

The minor version number selects the channel. Both workflows trigger on the
same `X.Y.Z` tag pattern and then route on minor parity, so only one pipeline
runs per tag.

| Tag example | Minor | Channel | Workflow | GitHub release | Marketplaces |
|---|---|---|---|---|---|
| `0.2.1` | even | **stable** | `release.yml` | regular release | `vsce publish` / `ovsx publish` |
| `0.3.59` | odd | **pre-release** | `pre-release.yml` | flagged `--prerelease` | `vsce publish --pre-release` / `ovsx publish --pre-release` |

Rules that follow from this:

- Tags must be plain numeric `major.minor.patch` (e.g. `0.2.1`). The VS Code
  Marketplace and Open VSX reject semver pre-release identifiers such as
  `0.3.0-beta.1`, so the pre-release channel is encoded in the odd minor
  number plus the `--pre-release` packaging flag, not in the version string.
- To ship a stable release, pick the next **even** minor (or a patch on the
  current even minor). To ship a preview, pick the next **odd** minor.
- The VSIX filename gains a `-pre-release` infix on the pre-release channel
  (e.g. `shopware-lsp-0.3.59-pre-release-linux-x64.vsix`).

## What a release produces

Every release produces the same set of artifacts, differing only in version,
pre-release marking, and (for stable) the Homebrew cask:

**Server binaries** (the Rust `swlsp-cli` crate).

`.github/workflows/build-binaries.yml` compiles each target and uploads it as
an artifact named `server-<triple>`:

| Rust target | Runner | Archive name | Notes |
|---|---|---|---|
| `x86_64-apple-darwin` | `macos-15` (cross from arm64) | `darwin_amd64` | |
| `aarch64-apple-darwin` | `macos-15` | `darwin_arm64` | |
| `x86_64-unknown-linux-gnu` | `ubuntu-24.04` + `cargo-zigbuild` | `linux_amd64` | glibc, dynamic, glibc 2.17 floor |
| `aarch64-unknown-linux-gnu` | `ubuntu-24.04-arm` + `cargo-zigbuild` | `linux_arm64` | glibc, dynamic, glibc 2.17 floor |
| `armv7-unknown-linux-gnueabihf` | `ubuntu-24.04` + `cargo-zigbuild` | `linux_arm` | glibc, dynamic, glibc 2.17 floor |
| `x86_64-unknown-linux-musl` | `ubuntu-24.04` + `musl-tools` | `linux_amd64_musl` | static musl + mimalloc, for Alpine |
| `aarch64-unknown-linux-musl` | `ubuntu-24.04-arm` + `musl-tools` | `linux_arm64_musl` | static musl + mimalloc, for Alpine |
| `armv7-unknown-linux-musleabihf` | `ubuntu-24.04` + `cargo-zigbuild` | `linux_arm_musl` | static musl + mimalloc |
| `x86_64-pc-windows-msvc` | `windows-2025` | `windows_amd64` | static CRT |

- Linux ships two builds. The default (`linux_*`, the `linux-*` VSIX
  packages and Homebrew) is a dynamically linked glibc build, compiled by
  cargo-zigbuild against glibc 2.17 so it runs on every mainstream
  distribution; it uses the system resolver. CI fails the
  build if a binary needs a newer glibc symbol. The `*_musl` archives and
  the `alpine-*` VSIX packages carry a fully static musl build. Every
  Linux build uses mimalloc (`crates/cli/src/main.rs`): the indexer frees on
  other threads than it allocates on, where musl's malloc serializes on one
  lock and glibc's contends.
- the tag is embedded at compile time through `SHOPWARE_LSP_VERSION`
  (`option_env!`, `dev` for local builds)
- release binaries keep symbol names but drop DWARF line tables
  (`CARGO_PROFILE_RELEASE_STRIP=debuginfo`)
- `scripts/release/stage-binaries.sh` lays the artifacts out as
  `target/<triple>/release/shopware-lsp[.exe]`, and
  `scripts/release/package-release.sh` zips each one (binary, `LICENSE`,
  `README.md`, `THIRD_PARTY_NOTICES.md`) as
  `shopware-lsp_<version>_<os>_<arch>[_musl].zip` and writes `checksums.txt`

**VSIX packages** (built by `scripts/build-vsix-release.mjs`, 8 targets):

| VSIX target | Rust target | Binary staged into the extension |
|---|---|---|
| `darwin-x64` | `x86_64-apple-darwin` | `shopware-lsp` |
| `darwin-arm64` | `aarch64-apple-darwin` | `shopware-lsp` |
| `linux-x64` | `x86_64-unknown-linux-gnu` | `shopware-lsp` |
| `linux-arm64` | `aarch64-unknown-linux-gnu` | `shopware-lsp` |
| `linux-armhf` | `armv7-unknown-linux-gnueabihf` | `shopware-lsp` |
| `alpine-x64` | `x86_64-unknown-linux-musl` | `shopware-lsp` |
| `alpine-arm64` | `aarch64-unknown-linux-musl` | `shopware-lsp` |
| `win32-x64` | `x86_64-pc-windows-msvc` | `shopware-lsp.exe` |

The binary stays at the extension root (`extension/shopware-lsp[.exe]` inside
the VSIX), which is where the VS Code client and the Zed extension's Open VSX
download look for it.

Plus a `SHA256SUMS` file covering all eight `.vsix` files.

**Distribution:**

- GitHub release on `shopware/shopware-lsp` with the zips, `checksums.txt`,
  all VSIX files, and `SHA256SUMS` attached. The `release-lsp` job creates it
  as a draft with `gh release create --draft` (`--prerelease` on the
  pre-release channel); the workflow publishes it (stable, or
  `--prerelease --latest=false`) only after the VSIX files are attached.
  Both channels use plain `X.Y.Z` tags; minor parity decides the channel.
- Release notes are GitHub's generated notes (`--generate-notes`).
- Stable releases only: after the release is public, the `homebrew` job
  writes `Casks/shopware-lsp.rb` in `shopware/homebrew-tap`
  (`scripts/release/update-homebrew-cask.sh`, glibc archives on Linux) and
  pushes it with the octo-sts tap token. No `deb`/`rpm`/`apk` packages are
  published.
- Marketplace publication to the
  [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp)
  and [Open VSX](https://open-vsx.org/extension/shopware/shopware-lsp).

## Prerequisites

- Push access to `shopware/shopware-lsp` with permission to push tags.
- Approval rights for the `release` (stable) or `preview` (pre-release)
  GitHub environment, if environment reviewers are configured. The publish
  jobs do not run until the environment gate passes.
- For local reproduction only: rustup (the toolchain comes from
  `rust-toolchain.toml`), a C compiler, Node.js 24, `@vscode/vsce@3.9.1`,
  and `zip` for `make release-dry-run`.

CI secrets and identities (maintainers normally never touch these, but this is
where publishing credentials live):

| Secret / identity | Used by | Purpose |
|---|---|---|
| `GITHUB_TOKEN` (automatic) | both pipelines | create/upload GitHub releases, download artifacts |
| `VSCODE_PUBLISH_TOKEN` | publish jobs | publish to the VS Code Marketplace |
| `OVSX_PUBLISH_TOKEN` | publish jobs | publish to Open VSX |
| `octo-sts` identity `lsp` → `shopware/homebrew-tap` | `release.yml` only | short-lived `HOMEBREW_TAP_GITHUB_TOKEN` for the Homebrew cask update |

## How to cut a release

1. **Decide the channel and version.** Check existing tags
   (`git tag --sort=-v:refname | head`) and pick the next patch on the
   current channel minor, or bump the minor when starting a new line. Remember:
   even minor = stable, odd minor = pre-release.
2. **Make sure `main` is green.** The pre-release pipeline runs the full
   `tests.yml` suite (Rust fmt/clippy/tests on Linux, macOS and Windows, the
   third-party notices check, VS Code and Zed checks) before
   packaging. The stable pipeline assumes the tag was validated the same way,
   so run the equivalent locally:
   ```sh
   make check
   ``` See `AGENTS.md` for the full validation policy.
3. **Update `editors/vscode/package.json` if needed.** The release script
   applies the tag as a version override (`npm version <tag>
   --no-git-tag-version`) before packaging, so a manual bump
   is not strictly required. Keeping the manifest version in sync with the
   channel avoids confusion for local `vsce package` runs.
4. **Create and push the tag.** `gh release create --verify-tag` requires the
   tag to exist on GitHub before the draft is created.
   ```sh
   git tag 0.2.1
   git push origin 0.2.1
   ```
5. **Watch the workflow.** Pushing the tag triggers `Release` (even minor) or
   `Pre-release` (odd minor) under *Actions*. The stage order is:
   - Stable: `route` → `build-binaries.yml` (9 builds) → `release-lsp`
     (zip archives, `checksums.txt`, draft release) → `build-vsix.yml`
     (`version: <tag>`, `pre-release: false`) →
     attach VSIX + `SHA256SUMS` to the draft release and publish it →
     Homebrew cask update and publication to both marketplaces
     (`environment: release`).
   - Pre-release: `route` → `tests.yml` → `build-binaries.yml` →
     `release-lsp` (draft pre-release, no Homebrew) in parallel with
     `build-vsix.yml` (`version: <tag>`, `pre-release: true`) → attach VSIX
     + `SHA256SUMS` and publish as pre-release → publish with
     `--pre-release` flags (`environment: preview`) + summary links.
   - Both pipelines verify `SHA256SUMS` (`sha256sum --check`) before every
     upload/publish step; a checksum failure fails the job before anything
     is published.
6. **Verify the GitHub release.** Confirm the tag page lists the nine
   binary zips (six platforms plus three `_musl` archives), `checksums.txt`,
   all eight `.vsix` files, and `SHA256SUMS`,
   and that the pre-release flag is set only for odd minors.
7. **Verify the marketplaces.** Check the Marketplace and Open VSX pages for
   the new version (stable channel) or the pre-release version
   (preview channel). Both publishers run with `--skip-duplicate`, so a
   re-run after a partial publish resumes without failing on already
   published packages.
8. **Verify Homebrew (stable only).** Confirm the cask bump landed in
   `shopware/homebrew-tap`. Pre-releases never touch Homebrew.

## Local reproduction and dry runs

You do not need these steps for a normal release; they exist for debugging
the packaging itself.

```sh
# Host-only VSIX: builds target/release/shopware-lsp with the package version
# embedded and packages the VSIX for this machine into out/.
make release
node scripts/build-vsix-release.mjs --local --version=0.2.1

# All 8 VSIX targets from CI binaries: download the server-<triple>
# artifacts of a build-binaries run into one directory, then
scripts/release/stage-binaries.sh /path/to/artifacts
node scripts/build-vsix-release.mjs --version=0.3.59 --pre-release
node scripts/build-vsix-release.mjs --dry-run   # check binaries, print the plan

# Zip the staged binaries into dist/ with checksums.txt (no publishing)
make release-dry-run
```

Notes:

- The script requires a numeric `--version`; anything else (including
  semver suffixes) is rejected up front.
- The script asserts the musl binaries in the Alpine packages are statically
  linked (`file` must report `statically linked` or `static-pie linked`);
  `build-binaries.yml` checks the same right after compiling, and checks the
  glibc builds against their 2.17 floor.
- Staged `shopware-lsp` / `shopware-lsp.exe` files inside
  `editors/vscode/` are build scratch space and are always removed in a
  `finally` block; do not commit them.
- Only CI publishes: `gh release create` with `GITHUB_TOKEN`, and the cask
  push with the octo-sts token for `shopware/homebrew-tap`.

## Troubleshooting

- **Neither workflow ran after pushing a tag.** The tag must match
  `[0-9]+.[0-9]+.[0-9]+` exactly. A `v` prefix (`v0.2.1`) or a suffix
  (`0.2.1-rc.1`) matches neither pipeline and is silently ignored.
- **`package-release.sh` reports `missing target/<triple>/...`.** A
  `server-<triple>` artifact was not downloaded or staged; re-run the failed
  `build-binaries` job.
- **`gh release create` fails because the release exists.** A previous run
  already created the draft. Delete the draft (not the tag) and re-run the
  `release-lsp` job.
- **"Invalid VSCode extension version".** The version passed to
  `build-vsix-release.mjs` (or read from `editors/vscode/package.json`
  when no override is given) was not numeric `X.Y.Z`.
- **Checksum step fails.** Do not re-package by hand and upload; re-run the
  failed job so binaries, VSIX files, and `SHA256SUMS` stay consistent.
- **Publish job is waiting.** It is gated on the `release` / `preview`
  environment. An environment approver must approve it.
- **`--skip-duplicate` published nothing.** The version was already
  published (usually after a re-run). This is expected and the job still
  succeeds; bump the version for a new publication.
- **Pre-release installed as stable (or vice versa).** The Marketplace
  derives this from the `--pre-release` flag at publish time, which CI sets
  from the tag's minor parity. A tag on the wrong minor puts the build in
  the wrong channel — retag with the correct minor rather than publishing
  by hand.

## Reference

| File | Role in a release |
|---|---|
| `.github/workflows/release.yml` | stable pipeline (even minors) |
| `.github/workflows/pre-release.yml` | pre-release pipeline (odd minors, runs `tests.yml` first) |
| `.github/workflows/build-binaries.yml` | reusable native Rust builds, one `server-<triple>` artifact per target |
| `.github/workflows/build-vsix.yml` | reusable VSIX build (`version` + `pre-release` inputs) from those artifacts |
| `.github/workflows/tests.yml` | Rust (Linux/macOS/Windows), lint, notices, VS Code, Zed gates |
| `.github/actions/setup-rust` | installs the `rust-toolchain.toml` toolchain, extra targets, Cargo cache |
| `scripts/release/stage-binaries.sh` | artifacts → `target/<triple>/release/` |
| `scripts/release/package-release.sh` | zip archives (glibc and `_musl` Linux builds) and `checksums.txt` |
| `scripts/release/update-homebrew-cask.sh` | writes `Casks/shopware-lsp.rb` for the tap (stable only) |
| `scripts/build-vsix-release.mjs` | stage binary → `vsce package` × 8 → `SHA256SUMS` |
| `Makefile` (`release`, `release-dry-run`) | local host-only VSIX and archive packaging |
| `editors/vscode/package.json` | extension manifest; `version` is overridden by the tag at release time |
| `crates/cli/src/main.rs` (`option_env!("SHOPWARE_LSP_VERSION")`) | server version; `dev` unless set at build time |

## Zed extension

The Zed client lives in `editors/zed` and has its own version in
`extension.toml`. `make zed-build` produces
`editors/zed/target/wasm32-wasip2/release/zed_shopware_lsp.wasm`; the normal test
workflow checks this build plus Rust/Python tests and local server contracts.
The Zed extension downloads the platform server from the existing Open VSX
VSIX packages, so server packaging and artifact paths stay shared.

The release workflows do not publish the Zed extension to its registry. For
local installation, select `editors/zed` as a development extension in Zed.
Registry publication is a separate step from the server/VSIX release pipeline.
