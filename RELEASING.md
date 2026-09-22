# Releasing Shopware LSP

This document describes how to cut a release of the Shopware Language Server:
the Go binaries, the platform-specific VSIX packages, the GitHub release, the
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

**Go binaries** (built by GoReleaser, `goreleaser release --clean`):

- `darwin-amd64`, `darwin-arm64`
- `linux-amd64`, `linux-arm64`, `linux-armv7` (statically linked)
- `windows-amd64`
- published as `<project>_<version>_<os>_<arch>.zip` plus `checksums.txt`
- Linux `deb`, `rpm`, and `apk` packages (nfpm) for `amd64`, `arm64`, and
  `armv7`, installing the static binary to `/usr/bin/shopware-lsp`
- the tagged version is embedded via `-X main.version={{.Version}}`
  (`main.go` defaults to `"dev"` for local builds)

**VSIX packages** (built by `scripts/build-vsix-release.mjs`, 8 targets):

| VSIX target | GoReleaser build | Binary staged into the extension |
|---|---|---|
| `darwin-x64` | `darwin-amd64` | `shopware-lsp` |
| `darwin-arm64` | `darwin-arm64` | `shopware-lsp` |
| `linux-x64` | `linux-amd64` | `shopware-lsp` |
| `linux-arm64` | `linux-arm64` | `shopware-lsp` |
| `linux-armhf` | `linux-armv7` | `shopware-lsp` |
| `alpine-x64` | `linux-amd64` (reused) | `shopware-lsp` |
| `alpine-arm64` | `linux-arm64` (reused) | `shopware-lsp` |
| `win32-x64` | `windows-amd64` | `shopware-lsp.exe` |

Plus a `SHA256SUMS` file covering all eight `.vsix` files.

**Distribution:**

- GitHub release on `shopware/shopware-lsp` with the zips, Linux packages,
  `checksums.txt`, all VSIX files, and `SHA256SUMS` attached. GoReleaser
  creates it as a draft; the workflow publishes it (stable, or
  `--prerelease --latest=false`) only after the VSIX files are attached.
  GoReleaser cannot detect the channel itself because both channels use plain
  `X.Y.Z` tags.
- Release notes come from the GoReleaser changelog (conventional commits,
  excluding `docs:` and `test:` prefixes) on both channels.
- Stable releases only: a Homebrew cask update in `shopware/homebrew-tap`.
  The pre-release pipeline runs GoReleaser with `--skip=homebrew`.
- Marketplace publication to the
  [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp)
  and [Open VSX](https://open-vsx.org/extension/shopware/shopware-lsp).

## Prerequisites

- Push access to `shopware/shopware-lsp` with permission to push tags.
- Approval rights for the `release` (stable) or `preview` (pre-release)
  GitHub environment, if environment reviewers are configured. The publish
  jobs do not run until the environment gate passes.
- For local reproduction only: Docker, Node.js 24, Go 1.27, and
  `@vscode/vsce@3.9.1`. `mise install` provides the Go/Node/vsce toolchain;
  see `mise.toml`. The release script defaults to
  `ghcr.io/shyim/goreleaser-cross:v1.27.0` (override with
  `GORELEASER_CROSS_IMAGE`); `make release` uses `GOLANG_CROSS_VERSION`
  (same default).

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
   `tests.yml` suite (Go tests, race tests, lint, VS Code checks, build)
   before packaging. The stable pipeline assumes the tag was validated the
   same way, so run the equivalent locally:
   ```sh
   mise run check
   ```
   This covers `go test ./...`, the integration-suite compile, lint, and all
   VS Code checks. See `AGENTS.md` for the full validation policy.
3. **Update `editors/vscode/package.json` if needed.** The release script
   applies the tag as a version override (`npm version <tag>
   --no-git-tag-version`) *after* building the Go binaries, so a manual bump
   is not strictly required. Keeping the manifest version in sync with the
   channel avoids confusion for local `vsce package` runs.
4. **Create and push the tag.** The worktree must be clean — GoReleaser
   rejects a dirty worktree in release mode.
   ```sh
   git tag 0.2.1
   git push origin 0.2.1
   ```
5. **Watch the workflow.** Pushing the tag triggers `Release` (even minor) or
   `Pre-release` (odd minor) under *Actions*. The stage order is:
   - Stable: `route` → GoReleaser (`make release`, includes the Homebrew
     cask) → `build-vsix.yml` (`version: <tag>`, `pre-release: false`) →
     attach VSIX + `SHA256SUMS` to the draft release and publish it →
     publish to both marketplaces (`environment: release`).
   - Pre-release: `route` → `tests.yml` → GoReleaser (`make release
     GORELEASER_ARGS=--skip=homebrew`, draft release) in parallel with
     `build-vsix.yml` (`version: <tag>`, `pre-release: true`) → attach VSIX
     + `SHA256SUMS` and publish as pre-release → publish with
     `--pre-release` flags (`environment: preview`) + summary links.
   - Both pipelines verify `SHA256SUMS` (`sha256sum --check`) before every
     upload/publish step; a checksum failure fails the job before anything
     is published.
6. **Verify the GitHub release.** Confirm the tag page lists the six
   binary zips, the nine `deb`/`rpm`/`apk` packages, `checksums.txt`, all eight `.vsix` files, and `SHA256SUMS`,
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
# GoReleaser snapshot build in Docker (no publishing, no tag required)
make release-dry-run

# Full local VSIX build (GoReleaser + all 8 platform packages into out/)
# On an exact tag this uses `goreleaser release` so binaries embed the tag;
# otherwise it falls back to `goreleaser build --snapshot`.
mise run release
node scripts/build-vsix-release.mjs --version=0.2.1
node scripts/build-vsix-release.mjs --version=0.3.59 --pre-release
```

Notes:

- The script requires a numeric `--version`; anything else (including
  semver suffixes) is rejected up front.
- The script asserts the Linux binaries are statically linked (`file`
  must report `statically linked`) and fails otherwise.
- Staged `shopware-lsp` / `shopware-lsp.exe` files inside
  `editors/vscode/` are build scratch space and are always removed in a
  `finally` block; do not commit them.
- `make release` runs the real `goreleaser release --clean` (publishing
  enabled). Only CI should run it — it needs `GITHUB_TOKEN` and
  `HOMEBREW_TAP_GITHUB_TOKEN`.

## Troubleshooting

- **Neither workflow ran after pushing a tag.** The tag must match
  `[0-9]+.[0-9]+.[0-9]+` exactly. A `v` prefix (`v0.2.1`) or a suffix
  (`0.2.1-rc.1`) matches neither pipeline and is silently ignored.
- **GoReleaser fails with "dirty worktree".** Commit or stash first, then
  re-tag. In CI this cannot happen (fresh checkout), so it only affects
  local `make release` / script runs on exact tags.
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
| `.github/workflows/build-vsix.yml` | reusable VSIX build (`version` + `pre-release` inputs) |
| `.github/workflows/tests.yml` | Go/race/lint/VS Code/Zed/build gates |
| `scripts/build-vsix-release.mjs` | GoReleaser → stage binary → `vsce package` × 8 → `SHA256SUMS` |
| `.goreleaser.yaml` | binary matrix, ldflags version injection, archives, Linux packages, draft release, changelog, Homebrew cask |
| `Makefile` (`release`, `release-dry-run`) | Dockerized GoReleaser invocations |
| `mise.toml` (`release` task) | local entry point: `node scripts/build-vsix-release.mjs` into `out/` |
| `editors/vscode/package.json` | extension manifest; `version` is overridden by the tag at release time |
| `main.go` (`var version = "dev"`) | server version; release builds overwrite it via `-X main.version=` |

## Zed extension

The Zed client lives in `editors/zed` and has its own version in
`extension.toml`. `mise run zed:build` produces
`editors/zed/target/wasm32-wasip2/release/zed_shopware_lsp.wasm`; the normal test
workflow checks this build plus Rust/Python tests and local server contracts.
The Zed extension downloads the platform server from the existing Open VSX
VSIX packages, so server packaging and artifact paths stay shared.

The release workflows do not publish the Zed extension to its registry. For
local installation, select `editors/zed` as a development extension in Zed.
Registry publication is a separate step from the server/VSIX release pipeline.
