# Rust port — working notes

The Rust implementation under `crates/` is the only implementation: CI,
releases and the VS Code and Zed launchers build `shopware-lsp` from
`crates/cli`. The Go implementation (`main.go`, `internal/`, `cmd/`) that
served as the differential oracle during the port has been removed; it is
in git history up to commit `254e995` (`git show 254e995:internal/...`).
References below to Go packages, the oracle, or `scripts/compare-cst.sh`
describe how the port was verified, not current tooling. This file records
the conventions the crates follow, the deliberate deviations from the Go
behavior, and the porting status.

## Conventions

- One crate per Go subsystem, named `swlsp-<name>`, in `crates/<name>`.
- `swlsp-syntax` replaces `internal/parser/cst` and `internal/parser/parsekit`:
  - `Kind(u16)` values and debug names stay identical to Go. Declare a
    language's kinds with `swlsp_syntax::language!` (tokens first, then nodes,
    in the exact Go order and with the exact Go base).
  - Trees are rowan green trees (`Tree`, `SyntaxNode`, `SyntaxToken`). Ranges
    are byte offsets (`TextRange`/`TextSize`). Use `Tree::node_text` or
    `token.text()` for text; avoid `node.text().to_string()` in hot paths.
  - `Parser` mirrors `parsekit.Parser` (`peek`, `at`, `at_set`, `bump`,
    `bump_as`, `bump_next_n_as`, `start`/`complete`/`precede`, `expect`,
    `recover`, `add_error`, `finish`). Go `*Marker` becomes the move-only
    `Marker`.
  - `SyntaxNodeExt` mirrors Go node helpers (`child_of_kind`,
    `child_token_of_kind`, `ancestor_of_kind`, `token_at`,
    `descendant_for_range`, `node_at`, `range_trimmed_trivia`).
- Parsers expose `pub fn parse(source: &str) -> swlsp_syntax::Parse` and a
  `src/bin/dump-cst-<lang>.rs` calling `swlsp_syntax::dump::run`.
- Go `query` packages become a `query` module in the same crate.

## Intentional differences from Go

- Tokens never split a UTF-8 character. Where Go emits one unknown token per
  byte of a multi-byte character, Rust emits one token for the character.
  `scripts/compare-cst.sh` normalizes this.
- Sources are read with lossy UTF-8 decoding; non-UTF-8 files are excluded
  from differential runs.
- Go's debug printer only approximates Rust's `{:?}` printable table (see
  `internal/parser/cst/debug.go`); Rust escapes some characters, such as
  Hangul fillers, that Go prints verbatim. `scripts/compare-cst.sh` decodes
  `\u{..}` escapes on both sides.
- Go's suppression and `@method static` parsing cut the original text at a
  byte offset found in a lowercased copy; with characters like `İ` Go returns
  wrong text or panics. Rust keeps the cut on a character boundary.
- Fixed in both (index version 180): nested composite types without cached
  text (`callable(): T`, `array{}`, `object{...}`, uncached unions) used to
  render as their kind name inside a parent (`list<callable>`,
  `list<array-shape>`), so canonical text, equality keys and persisted types
  were lossy. The nested renderer now renders them in full; the persisted
  form is still the canonical text.
- `swlsp-php-stubs` embeds its own copy, `crates/php-stubs/data/phpstorm-stubs.msgpack`,
  written by the Rust generator (`crates/php-stubs-generate`); a test keeps
  it byte-identical to the Go oracle's copy (`cmd/generate_phpstorm_stubs`
  writes that one).
- `swlsp-projectconfig` embeds its own `crates/projectconfig/schema.json`
  with `include_str!`; a test keeps it in sync with the Go copy.
- `swlsp-lsp-protocol` builds on `lsp-types` but keeps URIs as `String` and
  `Color` as f64 to match Go's wire behavior.
- The cache version file is written as `<IndexVersion>-rs`: Rust stores
  MessagePack values, so Go and Rust caches must never be shared.
- Indexer optional capabilities are `Indexer::as_*` methods returning
  `Option` instead of Go runtime interface checks.
- `swlsp-php-semantic` interns identifiers of published graphs in a
  generational, sharded, append-only arena (`atom.rs`: 4-byte atoms, about
  7 bytes of overhead per string, lock-free reads); see the collection entry
  below. Binder output and open-document overlays do not intern. Its cache format is its own (not MessagePack).
- The committed stubs artifact was produced by an older Go binder that kept
  some empty lists as `[]`; current Go HEAD writes `nil`. The Rust generator
  defaults to the committed shape (`EmptyListPolicy::Committed`) and has
  `-go-head` for the current Go shape.
- Go bug: conditionally declared traits with duplicate IDs in one file make
  Go's lazy detail loader return the first duplicate's parameters, so Go's
  fresh and restored snapshots differ. Rust's agree.
- Go bug: twigcomponent's argument-attribute parser panics on
  `data-live-param`; Rust ignores the attribute.
- `PhpIndex::twig_template_variables` keeps the first value per variable
  name. Go reads rows in commit order, which only happens to be stable; Rust
  orders them by source file first, so the chosen literal type is
  deterministic (it can differ from Go where several controllers pass
  different literals).
- Fixed in both: hover ranges for snippet keys, `sw_icon` names and admin
  component tags/registry names used to start at the cursor and end at cursor
  + name length in bytes (wrong on non-ASCII lines and mid-name). They now
  cover the name's CST byte range, converted to UTF-16 at the boundary.
- Fixed in both: `shopware.admin` now declares the four `admin.event-bus.*`
  codes (a file triggering one used to fail the whole inspection).
- Ordering made deterministic in both: key lookups and catalog views read
  `ORDER BY file_path, rowid` (storage order depended on parallel commit
  timing), and completion items equal up to case are ordered by label. Go
  still builds per-file batches from maps, so its within-file order can vary
  between cold indexes; Rust's is sorted by key.
- Rust-only: the Administration document observer skips the editor overlay
  when the open text equals the file the index was built from
  (`FileScanner::indexed_content_matches`), and drops an overlay when edits
  revert to the saved text. Go publishes an overlay for every open file and
  invalidates every Administration cache on open and close.
- Rust-only: the index store disables SQLite's inline auto-checkpoint and
  checkpoints WAL frames from a background thread (`Checkpointer` in
  `swlsp-indexer::store`). Go checkpoints inside commits, where the `fsync`
  stalls every indexing worker waiting for the writer.
- Rust-only: the Administration indexer is a `PreparingIndexer`; parsing and
  extraction run outside the write transaction and the recorded writes are
  replayed in the same order (persisted rows and workspace symbols are
  identical, including per-file symbol order).
- Fixed during the port: `ElementHandle` anchor ranges now serialize as
  Go's `{"Start","End"}` (the untagged `cst.TextRange` shape) and accept
  either case on input.
- Go bug fixed in Rust: Go sends `shopware/indexingCompleted` while
  diagnostics are still suspended (resume happens after the watcher starts),
  so a pull right after the notification can return empty (20/50 cold runs).
  Rust resumes before the notification. The same reorder in Go's
  `indexAll`/`handleInitialized` would fix Go.
- Go bug fixed in Rust: a SCSS file ending in a lone `\` makes the Go lexer
  emit a zero-length token and the Go parser loop forever; Rust emits a
  1-byte identifier.

## Differential testing

```bash
go build ./cmd/dump_cst   # oracle
scripts/compare-cst.sh <lang> <corpus> <ext>...   # e.g. json ~/shopware json
```

Large corpora (PHP dumps are ~5 GB each) belong on disk, not in `/tmp`:
`OUT=target/cmp/php scripts/compare-cst.sh php ~/shopware php`. The script
streams both dumps, so memory stays flat.

`~/shopware` is a read-only Shopware checkout used as corpus. Never modify it.

## End-to-end benchmark

`scripts/bench/lsp_bench.py` drives any server binary over stdio with a fixed
request mix (pull diagnostics, symbols, links, lenses, folding, semantic
tokens, inlay hints, plus hover, definition, references, completion,
highlight and signature help at sampled identifiers) on files sampled from
admin JS/TS/Twig, Storefront Twig, PHP, migrations, XML, YAML and SCSS. It
records index time, peak RSS, per-request latency and a hash of every
response; `scripts/bench/compare.py` diffs two runs.

```bash
go build -o target/bench/go-shopware-lsp .
cargo build --release -p swlsp-cli
python3 scripts/bench/lsp_bench.py --server target/bench/go-shopware-lsp \
    --root ~/shopware --out target/bench/go.json --cold
python3 scripts/bench/lsp_bench.py --server target/release/shopware-lsp \
    --root ~/shopware --out target/bench/rs.json --cold
python3 scripts/bench/compare.py target/bench/go.json target/bench/rs.json
```

Run the two servers one after the other, never concurrently.

## Wave plan (Go package dependency levels)

Ports proceed bottom-up; a wave starts when the crates it depends on exist.

1. Syntax + parsers: cst, parsekit, json, xml, xpath, yaml, scss, js, php,
   twig, then vue (needs js, scss, twig).
2. Independent foundations: php/types, php/phpdoc, php/suppression,
   uriutil, pathmatch, textutil, suggestion, phar, projectconfig,
   projectdetect, lsp/protocol (lsp-types + custom extensions).
3. Infrastructure: indexer (store, scanner, watcher, workspace symbols),
   rewrite, language registry.
4. PHP engine: literal, project, languagelevel, semantic, phpstormmeta,
   resolver, binder, imports, inference, refactoring, rewrite, stubs, php.
5. LSP core (internal/lsp root, ~6k lines) and simple providers (color,
   folding, formatting, selection).
6. Domain indexes: shopware, shopware/dal, environment, feature, snippet,
   systemconfig, appscript, then asset, console, doctrine, event, extension,
   httpclient, messenger, serializer, entityschema, symfony, symfonyconfig,
   translation, validation, then theme, twig, then admin, style,
   twigcomponent.
7. Feature providers: lsp/* (completion, definition, hover, references,
   diagnostics, inspections, code actions, refactor, ...).
8. Composition and frontends: app, cli (serve, check, index, feature
   commands), MCP (`rmcp`), editor launcher switch.

## Status

| Area | Crate | Status |
|---|---|---|
| cst + parsekit | `swlsp-syntax` | done |
| json | `swlsp-parser-json` | done, 1556/1556 corpus files identical |
| xml, xpath | `swlsp-parser-xml`, `swlsp-parser-xpath` | done, 641/641 xml identical; xpath identical on 3313 generated inputs |
| yaml | `swlsp-parser-yaml` | done, 702/702 identical |
| scss | `swlsp-parser-scss` | done, 984/984 identical |
| javascript | `swlsp-parser-js` | done, 4196/4196 identical |
| php | `swlsp-parser-php` | done, 53573/53573 identical |
| twig | `swlsp-parser-twig` | done, 3881/3881 identical; formatter output byte-identical on 7762 corpus formats |
| vue | `swlsp-parser-vue` | done, 13/13 corpus files identical, plus 2034/2034 on an extended corpus of real and synthetic SFCs |
| language registry | `swlsp-language` | all parsers wired |
| integration catalog | `swlsp-integration` | done, JSON identical to Go |
| php types, phpdoc, suppression | `swlsp-php-types`, `swlsp-php-phpdoc`, `swlsp-php-suppression` | done; identical to Go on 264k docblocks, 224k type inputs, 150k type-relation pairs |
| small infra + projectconfig + protocol | `swlsp-uriutil`, `swlsp-pathmatch`, `swlsp-textutil`, `swlsp-suggestion`, `swlsp-phar`, `swlsp-projectconfig`, `swlsp-projectdetect`, `swlsp-lsp-protocol` | done; config fingerprint and default JSON byte-identical to Go |
| indexer, rewrite | `swlsp-indexer`, `swlsp-rewrite` | done (91 + 13 tests) |
| lsp core | `swlsp-lsp` | done (100 tests); `lsp-server` transport, one ordered worker like Go |
| php refactoring | `swlsp-php-refactoring` | done; identical edits on 806k extraction probes |
| php literal, project, languagelevel, semantic | `swlsp-php-literal`, `swlsp-php-project`, `swlsp-php-languagelevel`, `swlsp-php-semantic` | done; 2.07M semantic facts identical to Go on 53,578 files; graph ~10% (full) / ~5% (warm cache) smaller than Go |
| php resolver, imports, rewrite | `swlsp-php-resolver`, `swlsp-php-imports`, `swlsp-php-rewrite` | done; names (2M lines), members/signatures (62M lines), imports and rewrites identical to Go on the corpus |
| php phpstormmeta, stubs | `swlsp-php-phpstormmeta`, `swlsp-php-stubs` | done; stub output byte-identical to Go for PHP 7.3–9.0 (610k lines); stub document 6x faster, 30% smaller |
| php inference | `swlsp-php-inference` | done (176 tests); 19.38M facts identical to Go on 53,574 files except 15 receivers Go loses to slice aliasing; peak memory 5.9 GB vs Go 10.6 GB |
| php binder | `swlsp-php-binder` | done (45 tests); full bound documents identical to Go on 53,574 files; ~6.2k files/s, on par with Go |
| php root, shopware, stubs generator | `swlsp-php`, `swlsp-shopware`, `swlsp-php-stubs-generate` | done; corpus index identical to Go (see notes); cold index 22.8 s / 295 MB vs Go 28 s / 718 MB; warm start 2.9 s / 207 MB vs Go 4.1 s / 400 MB; stubs artifact regenerated byte-identical |
| domain: symfony | `swlsp-symfony` | done (146 tests); 27,107 corpus lines identical to Go except one line that is nondeterministic in Go too; cold index 26.7 s / 321 MB vs Go 31.6 s / 763 MB; XML→YAML conversion byte-identical on 2,045 inputs |
| domain: entityschema | `swlsp-shopware-entityschema` | done (123 tests); ~270k oracle records (corpus + 16.5k fuzzed specs) identical to Go; 6.4 s vs Go 11.4 s |
| providers: commands, refactor | `swlsp-lsp-commands`, `swlsp-lsp-refactor` | done (8 + 24 tests); 7,055 corpus probes + 1,838 sjson cases identical to Go |
| providers: documentlink, signature, inlay | `swlsp-lsp-documentlink`, `swlsp-lsp-signature`, `swlsp-lsp-inlay` | done (3 + 8 + 18 tests); 25,270 provider records + 4,797 signature results identical to Go |
| providers: scaffold | `swlsp-lsp-scaffold` | done (47 tests); 29,962 command cases identical to Go |
| entityschema catalog performance | `swlsp-shopware-entityschema` | fixed: repeated catalog scan 17.5 ms vs Go ~115 ms, first scan 383–435 ms vs Go 508–549 ms; scaffold apply phase not re-timed |
| admin request-time performance | `swlsp-admin` | fixed: end-to-end admin signature help 16.3 s vs Go 14.2 s, parameter inlays 13.8 s vs 12.5 s (0 diffs on 25,270 records); live Twig live Twig `get_component_for_document` 1.00 s vs Go 4.03 s (300 files × 5); earlier fixes: template-path lookup 0.005 s vs Go ~10 s, document lookup 1.85 s vs 10.3 s, Shopware.Utils 0.12 s vs 0.6 s (228 files × 5) |
| providers: analytics | `swlsp-lsp-analytics` | done (11 tests); 81,473 response lines + synthetic profiler data identical to Go; 57.7 s / 382 MB vs Go 68.6 s / 767 MB |
| domain: asset, console, serializer, httpclient, validation, symfonyconfig; lsp phpanalysis | `swlsp-<name>` | done (45 tests); 138,855 corpus lines identical to Go |
| domain: doctrine, event, messenger, translation | `swlsp-<name>` | done (10 + 5 + 7 + 17 tests); ~60k corpus + synthetic lines identical to Go |
| providers: symbol, reference, highlight | `swlsp-lsp-symbol`, `swlsp-lsp-reference`, `swlsp-lsp-highlight` | done (6 + 43 + 2 tests); 21.4k corpus records identical to Go except 8 stub-vs-polyfill picks caused by oracle cache names (not a port difference) and order-only workspace-symbol ties; providers 1,098 s vs Go 2,119 s |
| providers: php semantic (diagnostics, completion, navigation, hover, hierarchy) | `swlsp-lsp-phpsemantic` | done (40 tests); 957k probe lines identical to Go on 53,545 files; 518 s vs Go 1106 s. Oracle peak RSS 4.3 GB vs Go 2.6 GB came from 16 concurrent global completions storing edits as JSON trees; `CompletionItem` edits are now typed (400-file completion run 3.5 GB → 0.93 GB in the agent's experiment); full re-measure pending |
| providers: definition, codelens | `swlsp-lsp-definition`, `swlsp-lsp-codelens` | done (110 + 27 tests); 469,858 definition probes (+449k admin) and 2,484 lenses + resolves identical to Go apart from SQLite row-order ties; peak RSS 408 MB vs Go 979 MB |
| providers: completion | `swlsp-lsp-completion` | done (166 tests); 74,194 non-empty corpus records, 653 diffs at port time: the 239 class-catalog tie-order diffs are fixed at port time (now explicit tie-breaks); 74 Twig function ordering + 2 feature records open; the rest Go-nondeterministic order or a Go panic; p95 1.46 ms vs Go 3.30 ms, 451 MB vs 880 MB; admin Twig component-tag completion slower (~19 ms/request) |
| providers: diagnostics, inspections | `swlsp-lsp-diagnostics`, `swlsp-lsp-inspections` | done (222 + 66 tests); 41 inspections / 188 codes identical; 9,517 corpus problems + 38,613 fix plans identical to Go (61 order-only records where Go itself varies); version boundaries 6.4–6.8 and server-level config overrides identical; 187 s vs Go 196 s, 618 MB vs 1.34 GB |
| providers: hover | `swlsp-lsp-hover` | done (76 tests); 36,735 hover records identical to Go; peak RSS 419 MB vs Go 943 MB, providers 740 s vs Go 544 s (cold, loaded machine) |
| providers: codeaction | `swlsp-lsp-codeaction` | done (89 tests); 27,852 probes (corpus + synthetic), 37k actions, 14.9k resolved commands identical to Go; 339 MB vs Go 712 MB |
| lsp semantic tokens | `swlsp-lsp-semantic` | done (21 tests); 70,056 tokens identical to Go |
| domain: extension, theme, style | `swlsp-extension`, `swlsp-theme`, `swlsp-style` | done; 28,282 corpus lines + 4,072 synthetic cases identical to Go |
| domain: twig | `swlsp-twig` | done (93 tests); 41,967 corpus lines + 5,152 workspace symbols identical to Go; full PHP+services+twig pipeline 29.0 s / 394 MB vs Go 51.2 s / 718 MB |
| domain: twigcomponent | `swlsp-twigcomponent` | done (24 tests); 1,716 synthetic (cold + warm) + 4,188 corpus lines identical to Go; 323 MB vs Go 793 MB |
| domain: appscript, environment, feature, snippet, systemconfig | `swlsp-<name>` | done; indexed records identical to Go on 47,854 corpus files |
| domain: admin | `swlsp-admin` | done (209 tests); 72,100 persisted records identical to Go on 4,261 Administration files |
| admin twig migration | `swlsp-admin-twigmigration` | done; identical to Go on 5748 migrations |
| simple providers: color, folding, formatting, selection | `swlsp-lsp-<name>` | done (21 tests) |
| providers: callhierarchy, linkedediting | `swlsp-lsp-callhierarchy`, `swlsp-lsp-linkedediting` | done |
| domain: shopware dal | `swlsp-shopware-dal` | done; 386 definitions / 2,780 fields identical to Go |
| request-time domain hot paths (twig, twigcomponent, snippet, symfony, doctrine, admin, migration) | various | done: all faster than Go except the `twig.component.live_event` inspection (~1.3x); 0 oracle diffs. Examples: live-event hover 0.27 s vs Go 14.6 s, admin hover 78 s vs 212 s, admin parent lookup 1.0 s vs 24.8 s, route-resource lens 2.7 s vs 4.9 s |
| composition root | `swlsp-app` | done (27 tests + real-world suite); registrations, inspections, 28 indexers, catalog and capabilities identical to Go; cold index 65.5 s / 340 MB vs Go 66.1 s / 750 MB; warm start 228 MB vs 370 MB; p95 latency within every Go budget |
| cli, MCP | `swlsp-cli` (binary `shopware-lsp`) | done (56 tests); all 34 commands, flags, exit codes, MCP (16 tools, wire shape) and `serve` identical to Go apart from documented deviations; cold `index` 67.5 s / 340 MB vs Go 72.8 s / 691 MB |
| pull-diagnostics race after didOpen | `swlsp-lsp` | fixed: diagnostics resume before `shopware/indexingCompleted` (0/50 empty vs 9/10 before; Go has the same race, 20/50) |
| CI, release packaging, editor launcher switch | `Makefile`, `rust-toolchain.toml`, `.github/`, `scripts/release`, `editors/` | done locally (artifact names identical to 0.3.61; VSIX, Zed contract and MCP/LSP smoke checks pass); new workflows not yet run on GitHub |
| end-to-end performance (2026-11-08) | `scripts/bench` | Rust beats Go on the full mix: 9,967 requests over 15 categories (admin JS/TS/Twig, Storefront Twig, PHP, migrations, XML, YAML, SCSS, plus route/translation/event/repository-focused positions) take 125.7 s vs Go 265.7 s; cold index 21.5 s vs 54.8 s; RSS after indexing 333 MiB vs 696 MiB; peak 759 MiB vs 1012 MiB. Only admin codeLens is within noise of Go (1.06x, +5 ms over 20 files); every other group is faster. Symfony UX fixtures (300 / 3,000 components): 1.6 s vs 5.6 s and 1.5 s vs 18.7 s. Response mismatches are completion ordering only (Go returns several catalogs in map or commit order) |

### Follow-ups

Nothing below blocks the port; these are improvements and documented
deviations.

- Codelens (messenger, validation, php_service), inlay (route_path, phpunit)
  and codeaction providers now reuse the memoized
  `swlsp_lsp_phpanalysis::for_document` analysis instead of re-analyzing per
  call (Go re-analyzes); they fall back to a direct analysis only for
  non-file URIs.

- Embedded artifacts live in the crates: `crates/projectconfig/schema.json`
  and `crates/php-stubs/data/phpstorm-stubs.msgpack` (the stubs generator
  writes there). Go embeds its own copies; `schema_matches_the_go_oracle_copy`
  and `catalog_matches_the_go_oracle_copy` fail when they drift. When the Go
  tree is deleted, move the editors' schema URL
  (`internal/projectconfig/schema.json` on `main`) to the crate path.
- `crates/cli/src/mcp/tools.json` is the source of truth for MCP tool
  schemas (it started as Go's `tools/list` output); edit it with the tool.
- Rust benchmarks run in CodSpeed next to Go's (`crates/bench`, job
  `rust-benchmarks`); the cases mirror Go's parser and PHP semantic suites
  (Rust is 3.4-6x faster on the parsers, 2.7-3.8x on PHP parse/bind/analyze).
- Shipped binaries use the `dist` profile (stripped, no debug info, fat
  LTO): 40 MB vs Go 34 MB (stripped). The local `release` profile keeps line
  tables for profiles and backtraces. Release binaries target baseline
  x86-64 (Go required x86-64-v3).
- `shopwareLSP.memoryLimitMiB` is deprecated (no `GOMEMLIMIT`).
- `gh actions-lock --no-fix` reports all workflows valid. Workflows reference
  the repository's composite actions as `$/.github/actions/...`
  (`--migrate-local-actions`) so the actions those use are locked as well.
- Linux releases ship two builds per architecture: a dynamically linked
  glibc build (`*-unknown-linux-gnu`, glibc 2.17 floor via cargo-zigbuild;
  default archives, `linux-*` VSIX, Homebrew) and a static musl build with
  mimalloc (`*_musl` archives, `alpine-*` VSIX). Both use the standard
  resolver, so no custom DNS code is needed. GoReleaser is gone:
  `scripts/release/package-release.sh` zips the binaries and writes
  `checksums.txt`, `gh release create --draft` creates the release, and
  `scripts/release/update-homebrew-cask.sh` writes the cask after
  publication; deb/rpm/apk packages are no longer published. musl build
  numbers: On `~/shopware` (cold,
  7,489 requests) against the former static-glibc build: requests 123-125 s
  vs 138-144 s, index time equal, peak RSS +2-4%, but RSS right after
  indexing 432-438 MiB vs 320 MiB (mimalloc returns freed pages after 1 s;
  `MIMALLOC_PURGE_DELAY=0` gets 378 MiB at 14% slower requests). Identical
  responses. aarch64/armv7 builds link statically but were not run locally.
- `THIRD_PARTY_NOTICES.md` is generated by cargo-about (`about.toml`,
  `about.hbs`, `make notices`; CI runs `make notices-check`) and covers the
  crates linked into `shopware-lsp` on every release target.

- Standardized on crates instead of Go ports (Go byte-parity is no longer a
  goal outside the differential oracle): `globset` behind `swlsp-pathmatch`
  (same pattern syntax and validation; `?` and classes now match bytes, so
  `a?b` no longer matches `aéb`), the `url` crate plus `percent-encoding` in
  `swlsp-uriutil` (outgoing file URIs byte-identical; odd inputs such as
  `file:///a%zz` or `..` segments now follow the URL standard), std sorting
  with explicit tie-breaks instead of `swlsp-gosort` (same-name class/alias
  pairs in one file now resolve by source position), `strsim`, `lru`, and in
  the CLI `similar` (standard unified diffs), plain `serde_json` (no HTML
  escaping, sorted `config` keys) and `humantime` (`1s 234ms`). Only
  `shopware-entityschema`'s `gojson` stays: committed snapshot IDs hash Go
  `json.Marshal` bytes.

- `crates/cli/src/mcp/tools.json` embeds Go's exact `tools/list` output;
  regenerate it whenever an MCP tool's input/output shape changes.
- Decided, not a gap: responses built as `serde_json::Value` serialize with
  sorted keys (Go keeps struct order). JSON objects are unordered for every
  client, and the sorted order is deterministic. serde_json's
  `preserve_order` was rejected because it would also change iteration order
  for every parsed `Value` map in the workspace. Typed protocol structs
  already serialize in field order.
- Intentional break: the CLI no longer mirrors Go's `flag` package. It is
  declared with `usage-rs` (`crates/cli/src/args.rs`): GNU-style flags
  (`--root DIR`, `--json`, `-v`, `-w`/`-d`), global flags on either side of
  the command, generated help (`help <command>`, `--help`) and clap-style
  errors with exit code 2. Renamed: `-profile.cpu|mem|trace` →
  `--profile-cpu|mem|trace`, `serve -rpc.trace` → `--rpc-trace`,
  `-listen.timeout` → `--listen-timeout`. Go-style `-root` is rejected with a
  hint. The VS Code and Zed clients pass the new spelling; language clients
  still append `--stdio --clientProcessId=<pid>`. The Go oracle keeps its own
  flags. `api-json` builds its command list from the same declaration.
- `--profile-cpu` writes a pprof CPU profile (read with `go tool pprof`) when
  built with `--features cpu-profile` on Unix (`SHOPWARE_LSP_PROFILE_HZ` sets
  the rate). `--profile-mem` and `--profile-trace` create their files for
  script compatibility and print on stderr that they stay empty (Go's heap
  profile and runtime trace have no Rust equivalent; use heaptrack or
  massif); so does `--profile-cpu` in builds without the feature.
  `serve --debug` answers 501 instead of pprof.
- Fixed: `colors` (and any float) matched Go only after serde_json's
  `float_roundtrip` feature; the default parser was one ulp off for values
  such as 54/255 read back from JSON.

- Fixed: providers and document observers registered by `swlsp-app` hold a
  `WeakServer` (`Server::downgrade`) instead of a clone, so the server no
  longer keeps itself alive; the app smoke test asserts it is freed.
- Completion trigger characters are sorted (deterministic); Go emits them in
  random map order, so no Go order exists to match.

- Symfony UX paths are benchmarked on `scripts/bench/ux_fixture.py`
  projects (the Shopware corpus has none): 300 components 1.6 s vs Go 5.6 s,
  3,000 components 1.5 s vs Go 18.7 s for references, definition, hover,
  diagnostics and lenses, with identical responses.

- Oracle harnesses must give Go and Rust cache directories that sort on the
  same side of `phpstub://`: a stub and a phar polyfill sharing a declaration
  ID resolve to the document whose path sorts last (same rule in Go and Rust),
  so `go-cache/` vs `rs-cache/` produces spurious diffs.
- Workspace-symbol ties are broken by owner path, file path, range and
  insertion id in both Go and Rust (previously storage order).

- `html_escape` (Go `html.EscapeString`) lives in `swlsp-textutil`. Provider
  and inspection constructors in `crates/lsp-*` all take `Option<PhpIndex>`
  (mirroring Go's nil checks) and return empty results when it is absent.

- Superseded by the end-to-end benchmark: PHP request paths (including
  translation and repository positions) and service-heavy XML/YAML requests
  are faster than Go; the older per-oracle gaps (doctrine DBAL probes,
  translation reference probes, the symfony query dump) came from
  harnesses that rebuilt context per probe.

- `swlsp-extension` uses `swlsp-parser-yaml`'s public scalar helpers; its
  yaml.v3 representation graph and typed decoder stay crate-local.
- Fixed in both: theme `IconProvider` resolves a bundle's `Resources/` next
  to its class file (it used to join the class file path, so only default
  Storefront icons were found); extension icons override Storefront ones.

- Done: `swlsp-php-inference::analyze_owned` publishes each changed return
  type in place through `Snapshot::override_symbol` (`ReturnPublisher`)
  instead of rebuilding the overlay snapshot; results are identical.
- Done: the interner is generational and collected. Atoms carry a 2-bit
  generation slot (up to four generations alive, 1 GiB of strings each);
  every packed document holds the generation its atoms live in, and
  packing/decoding pin one generation per document. Publishing re-homes
  documents from older generations, so each snapshot is single-generation.
  When the current generation reaches 64 MiB and twice its size after the
  last collection, publishing re-interns every retained document into a
  fresh generation (identifiers only; types and text stay shared) and the
  old one is freed with the last snapshot using it. Interned `Str`s keep
  only their 256 KiB arena chunk alive (a per-chunk count in the chunk
  header), so materialized names outlive their generation. `PhpIndex`
  drops its lazy-detail graph cache on collection (it pinned the old
  generation). Measured on `~/shopware`: one collection takes about 0.8 s
  and shrinks the table from 696,034 strings / 48.4 MiB to 367,260 /
  22.2 MiB (the rest was lazily interned IDs, member names and detail
  graphs); live memory (RSS after `malloc_trim`) is unchanged at about
  460-500 MiB across six re-index + collect rounds, and the full benchmark
  mix returns identical responses (7,489 of 7,489) at 0.99x the time.
  Tests: `tests/interner_gc.rs` (queries identical across collections, old
  snapshots keep working, generations freed, concurrent readers, decode
  after collection) and the real-world profile with
  `SHOPWARE_LSP_PROFILE_ATOM_CHURN=N SHOPWARE_LSP_PROFILE_ATOM_COLLECT=1`.
  Without collection the table does not grow on re-index
  (`interner_grows_only_for_published_graphs`, churn profile +0 atoms).
  Measured on cold `~/shopware` runs of the full benchmark mix: RSS after
  indexing 333 -> 321 MiB, peak 759 -> 752 MiB (cold index time varied
  20.6-25.2 s between runs). PHP completion in an interleaved A/B against
  the build before the change: 27.2-27.8 s vs 27.3-27.7 s, i.e. unchanged.
  Against the 01:17 build that predates the round-6 benchmark it is also
  unchanged (21.7/27.8/27.7 s old vs 22.0/27.5/27.0 s new), so the 5-7% gap
  a later full run showed against round 6 is machine-state variance, not code.
- The real-world suite (`--features integration`) is now compiled and linted
  by `make rust-test-integration-compile` (part of `rust-check` and CI); it
  had silently stopped compiling after the constructor change.

- Done: `swlsp-rewrite` emits `swlsp-lsp-protocol` workspace edits directly
  (`WorkspacePlan::workspace_edit`, `protocol_range`); its private `wire`
  types and `swlsp-lsp`'s `protocol_workspace_edit` conversion are gone. A
  golden test in `crates/rewrite` pins the serialized JSON to the old output.
- Done: every parser crate has `parse_shared(Arc<str>)` next to
  `parse(&str)` (`parse_sfc_shared` for Vue), plus
  `LanguageId::parse_shared`; `ParsedFile::syntax_tree` uses it, so the
  tree's `source_arc()` is the file's own `Arc` (`TextDocument` already
  shared one). Per file there is now one source copy plus rowan's green
  token text, which cannot be shared. Measured on a cold `~/shopware` index
  (10 interleaved runs each): peak RSS 337 MB before vs 344 MB after,
  wall time unchanged (about 17.5 s both). Only a 128 KiB batch per worker is
  live at once, so the saved copy is at most a few MB and the difference is
  allocator noise. The memory is in the long-lived indexes, not in sources.
