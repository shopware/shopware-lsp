# Performance reviews

- [2026-11-08: indexing and storage](#2026-11-08-indexing-and-storage)
- [2026-09-06: operation profiles](#2026-09-06-operation-profiles)

## 2026-11-08: indexing and storage

This pass compared the index pipeline with other language servers and IDE
indexers, profiled a cold and a warm `shopware-lsp index` of a Shopware
checkout (41,412 indexed files, 217.7 MiB of source; 16 CPUs, btrfs), and
adopted the ideas that measurably fit this architecture.

### What other indexers do

| System | Mechanism | Adopted here |
| --- | --- | --- |
| IntelliJ Platform | VFS assigns integer file ids; file-based indexes store keys and values per file id with compact externalizers; "dumb mode" while indexing; shared indexes keyed by content hash | Interned path ids in every table; compressed values |
| rust-analyzer | Interned `FileId`s; the VFS drops changes whose content hash is unchanged; salsa durability separates library from workspace inputs; fst symbol index; nothing persisted | Content-hash change detection, path interning |
| clangd background index | Per-file shards keyed by content digest, loaded at startup, stale shards re-indexed by digest | Content-hash change detection |
| gopls (v0.12+) | Persistent per-package results in a file-based cache instead of whole-program memory | Already the model here (persistent per-file rows, lazy detail loading) |
| SQLite-backed indexers in general | Few large transactions during bulk loads | Group commit |

Not adopted: dropping secondary indexes during bulk loads (index preparation
reads committed repositories while indexing), `synchronous=OFF` (saved 1.7 s
per cold index, but an OS crash could corrupt the cache), more index workers
(8 workers were no faster than 4 and 12 were slower: the single writer is the
limit), and parallel restore decoding (the restore shares one type-dedup
table across documents).

### Findings and changes

| Finding | Change |
| --- | --- |
| Warm start spent 0.4 s inflating DEFLATE graph payloads | Size-prefixed LZ4 blocks (`swlsp-php` persistence format 2): decode 40× faster for 20% more graph bytes |
| Startup built the PHP snapshot twice (restore, then runtime stubs), 0.45 s each | `PhpIndex::open_project` publishes the restored graphs and the stubs as one generation |
| Discovery asked the compiled-catalog indexers about every unclaimed path; each call cleaned and joined four paths | Precomputed cache directories, allocation-free parent checks |
| `index_route_resources` walked every string literal of every PHP, YAML and XML file inside the writer (1.2 s) | Text prefilter (`RoutingConfigurator` / `resource`) and a vectorized `contains_fold_ascii` |
| Paths repeated in every row and both indexes: ~90 MB of a 485 MB cache | `paths`/`namespaces` tables, integer ids (see [indexing.md](indexing.md#storage-layout)) |
| Named MessagePack values repeat field names | LZ4 for values of 256 bytes or more: 139 MB → 55 MB |
| A cold index wrote 2.1 GB to disk for a ~0.45 GB database; commits waited on I/O for 6 s | Group commit: batches are savepoints, the transaction commits every eight batches |
| Touched-but-unchanged files (`git checkout`, `touch`) were reindexed | xxh3 content hash in `file_hashes` |
| Non-preparing indexers parsed inside the serialized writer | Parsing moved to `PreparingIndexer::prepare` |

### Results

Release builds of the baseline (`5f2f106`) and the changed tree, run
alternately on the same machine (`shopware-lsp index`, `/usr/bin/time`):

| Operation | Before | After |
| --- | ---: | ---: |
| Cold index (empty cache) | 22.2–23.1 s | 18.9–19.0 s (about 2 s more with [settling](#cache-history-dependence-of-inferred-return-types)) |
| Warm start (nothing changed) | 3.56–3.69 s | 1.97–2.18 s |
| Every file touched, content unchanged | 32.5 s, 885 MB peak RSS | 2.5 s, 215 MB peak RSS |
| `indexes.db` + `file_scanner.db` | 458 + 11 MB | 261 + 11 MB |

The touched scenario shifts every stored mtime in `file_scanner.db`, which is
what the scanner sees after a branch switch that restores the same content.
The end-to-end request benchmark (`scripts/bench/lsp_bench.py`, both builds
from a cold cache) reports equal request latency (total 123.9 s → 124.3 s
over 7,489 requests), equal RSS after indexing (327 MiB) and no response
differences. Peak RSS of a warm start is about 7 MB (3.5%) higher; its cause
is not identified yet.

#### Cache-history dependence of inferred return types

A first request benchmark against the baseline reported 151 differing
completion items such as `array_find(array $array, callable $callback):
mixed` versus no return type. Both builds produced the same results from
equal caches; the difference came from how the files were last indexed. A
scanner run analyzes every file against the generation published before the
run, so a return type inferred from another file of the same run (here the
Symfony polyfill's `array_find()` returning `Php84::array_find()`) was
missing after a cold index but present after reindexing the file alone.
Persisted graphs also stored the generation number they were analyzed
against, so equal analyses never encoded equally.

The fix settles a run after it publishes: files with inferred returns are
analyzed again against the complete generation (see
[indexing.md](indexing.md#settling-cross-file-results)), and workspace
projections no longer store the analysis generation. A cold index followed by
`index --force` (every file re-analyzed on top of the complete cache) and a
plain cold index now answer the 7,489 benchmark requests identically, except
for one completion list whose anonymous-class labels embed the cache
directory of extracted PHAR sources (compared with the pre-settling cold index, 135 completions and 2 reference
lookups change: missing inferred types appear, and `DateTime::format`
returns 1,122 instead of 1,104 references). The cost is about 2 s per cold
index (18.3 s → 20.3–20.4 s) and about 15 MB of peak RSS; disk size and
request latency are unchanged. Running the settling analysis on 8 instead of
4 threads did not help and cost about 40 MB of peak RSS.

Settling cannot follow dependencies across runs: after `Item::label()`
changes, a function inferring its type from it keeps the stored type until
its own file is reindexed. Following rust-analyzer and IntelliJ, which derive
such facts at query time instead of storing them, inferred return types are
now flagged and recomputed on demand per generation
([on-demand inferred return types](php-semantic-engine.md#on-demand-inferred-return-types)).
Two listings first materialized every global declaration through this path
(`Snapshot::global_symbols` for the Symfony bundle catalog, making the first
PHP code lens 75 ms slower, and `Snapshot::all_symbols`); listings now use the
stored types. Results with a freshly settled cache: 0 of 7,489 benchmark
responses differ, request latency is unchanged (121.4 s → 120.0 s total),
and `shopware-lsp check src/Core/Checkout` reports identical diagnostics in
5.1–5.2 s → 5.3–5.5 s while inferring 17 files on demand.

## 2026-09-06: operation profiles

This pass predates the Rust port: it profiled five production operations of
the former Go implementation through its benchmarks, then optimized measured
work in PHP suppression detection, Twig component diagnostics, and entity field
assembly. The Rust crates were ported from the optimized code
(`swlsp-php-suppression`, `swlsp-lsp-diagnostics`,
`swlsp-shopware-entityschema`), so the algorithms described below still apply;
the timings are Go measurements and are kept as history.

### Workloads and attribution

Linux amd64, AMD EPYC-Genoa, Go 1.27.0, default GOMAXPROCS 16.
Each initial CPU/allocation profile ran separately for a two-second benchmark
window. These initial timings include profiling overhead and are observations,
not before/after comparisons. CPU percentages include runtime/GC samples;
allocation percentages refer to sampled allocated bytes, not retained heap.

| Operation | Fixture | Initial time | Bytes / allocations per operation | Profile finding |
| --- | --- | ---: | ---: | --- |
| PHP semantic analysis | `01.php` (now `crates/bench/data/01.php`), parsed/bound once; analysis includes its defensive document clone | 58.4 µs | 9,704 / 82 | Suppression-marker prefilter: 36.8% of CPU samples |
| Twig component diagnostics | Ten repeated missing components and invalid blocks, 20 problems | 110.8 µs | 36,026 / 403 | CST traversal: 36.1% of CPU; repeated `Blocks` calls: 12.4% of allocated bytes cumulatively |
| Legacy code-action validation | One edit in an open YAML document with 500 comment lines | 154.7 µs | 57,768 / 57 | Edited-source parsing: 45.7% of CPU; source digest: 22.2% of allocated bytes cumulatively |
| Scanner file-state query | 4,096 indexed paths, combined state/stale-file query | 4.79 ms | 553,861 / 24,116 | SQLite row iteration: 64.6% of CPU; C calls into SQLite: 43.7% |
| Entity definition import | ID plus 20 foreign-key/association pairs, 21 resulting fields | 852.9 µs | 228,703 / 581 | Field-result slice growth: 43.5% of allocated bytes; PHP parsing: 26.7% of CPU |

The scanner already uses the combined query. Redesigning its storage protocol
is not justified by this isolated benchmark. Code-action edited-source parsing
and stale-state validation remain intact. The digest copy is a potential future
allocation target, but this pass does not add unsafe string conversions or a
persistent source cache to remove it.

### Verified comparisons

Before/after executables were built from the same worktree, preserving the
pre-optimization implementation for the baseline. Benchmarks ran sequentially,
without profiling or concurrent builds/tests, for six 500 ms samples per variant.
Pair order alternated before/after and after/before. Values below are medians;
shared-host scheduling causes visible timing variation.

| Operation | Before | After | Median change | Allocations before → after |
| --- | ---: | ---: | ---: | ---: |
| PHP semantic analysis | 60.84 µs | 43.79 µs | −28.0% | 82 → 82 |
| Twig component diagnostics | 126.40 µs | 106.82 µs | −15.5% | 403 → 341 |
| Entity import, 20 associations | 868.79 µs | 837.56 µs | −3.6% | 582 → 577 |
| Entity import, 40 scalar fields | 704.05 µs | 586.58 µs | −16.7% | 261 → 255 |

PHP allocated bytes remain 9,704 per operation. Twig median allocated bytes
fall from 36,210 to 31,683 (12.5%). Individual time ranges were 56.65–71.42 µs
before and 34.81–47.39 µs after for PHP, and 103.82–131.98 µs before and
95.62–111.36 µs after for Twig. These are fixture-level improvements, not an
end-to-end editor or workspace-indexing speedup claim.

#### Suppression markers

Previously, PHP analysis compared a full marker at every byte offset in as
many as three passes over a source file. A single scan now checks full markers
only at `@`, `n`, or `N`. The `noinspection` candidate also covers its `@` form.
The directive parser and diagnostic matching rules are unchanged. Unicode
case-fold comparison remains in place at candidate starts. The post-change
profile attributes 7.4% of CPU samples to the prefilter, down from 36.8%.

Case-variant regression tests exercise actual suppression behavior. A fuzz
test compared candidate detection with the original byte-window algorithm,
including malformed bytes, truncated markers, Unicode, and mixed case. A
15-second run with four workers completed 519,619 comparisons without a mismatch.

#### Component block lookups

The block diagnostic pass now retrieves and deduplicates block names once per
component within one analysis. It still reports each usage at its original
range and checks cancellation. The cache is local to that pass, so later
analyses observe index updates. Regression coverage includes repeated usages,
different component catalogs, an initially empty block list, and subsequent
block additions/removals.

#### Entity field assembly

The importer knows the literal array's item count before it assembles fields.
Reserving that upper bound avoids repeatedly allocating and copying a growing
slice of large `FieldSpec` values. Foreign-key pairing and hierarchy collapse
retain their existing order and behavior, including empty results. No parsing,
target-resolution, or preservation checks are skipped.

Median allocated bytes fall from 230,537 to 198,288 for associations (14.0%),
and from 309,783 to 177,089 for scalar fields (42.8%). The association timing
difference is small relative to sample variation; the allocation reduction is
the stronger result. The returned field slice has capacity 21 for associations
and 41 for scalar fields in both variants. This measures the returned slice,
not process RSS. The scalar-field benchmark and `field-cap` metric were compiled
into both variants; a build overlay restored the baseline importer while
keeping the benchmark code identical.

### Reproducing measurements on the Rust server

The Go profiling commands no longer apply. For the Rust server:

- `crates/bench` holds the CodSpeed suite (`codspeed-criterion-compat`):
  parser throughput and PHP parsing, binding, and semantic analysis on
  `crates/bench/data/01.php`. Run it locally with `cargo bench -p swlsp-bench`,
  or with `cargo codspeed build -p swlsp-bench && cargo codspeed run -p swlsp-bench`
  as CI does.
- `scripts/bench/lsp_bench.py` drives a server binary over stdio with a fixed
  request mix on a real checkout and records index time, RSS, and per-request
  latency; `scripts/bench/compare.py` diffs two runs.
- The real-world index profile is
  `crates/app/src/tests/real_world/index_profile.rs`
  (`shopware_trunk_indexing_profile`).

For timing comparisons, build each variant once, run them sequentially and in
alternating order, and keep profiling and validation jobs out of the timed
comparison.

This review does not measure full-workspace cold/warm indexing, RSS, or an LSP
transport round trip.

### Validation

At the time, the full Go suite, its race suite, its linter, and compilation of
the integration packages passed, and the suppression fuzz run and alternating
benchmark comparisons described above were completed. No VS Code or protocol
changes were made.
