# Performance review — 2026-09-06

This pass profiled five production operations through existing portable Go
benchmarks, then optimized measured work in PHP suppression detection, Twig
component diagnostics, and entity field assembly. The baseline includes the
uncommitted maintainability refactors on top of `7b2b748`; comparing directly
with that commit would also measure those refactors.

## Workloads and attribution

Linux amd64, AMD EPYC-Genoa, Go 1.27.0, CGO enabled, default GOMAXPROCS 16.
Each initial CPU/allocation profile ran separately for a two-second benchmark
window. These initial timings include profiling overhead and are observations,
not before/after comparisons. CPU percentages include runtime/GC samples;
allocation percentages refer to sampled allocated bytes, not retained heap.

| Operation | Fixture | Initial time | Bytes / allocations per operation | Profile finding |
| --- | --- | ---: | ---: | --- |
| PHP semantic analysis | `internal/php/testdata/01.php`, parsed/bound once; analysis includes its defensive document clone | 58.4 µs | 9,704 / 82 | Suppression-marker prefilter: 36.8% of CPU samples |
| Twig component diagnostics | Ten repeated missing components and invalid blocks, 20 problems | 110.8 µs | 36,026 / 403 | CST traversal: 36.1% of CPU; repeated `Blocks` calls: 12.4% of allocated bytes cumulatively |
| Legacy code-action validation | One edit in an open YAML document with 500 comment lines | 154.7 µs | 57,768 / 57 | Edited-source parsing: 45.7% of CPU; source digest: 22.2% of allocated bytes cumulatively |
| Scanner file-state query | 4,096 indexed paths, combined state/stale-file query | 4.79 ms | 553,861 / 24,116 | SQLite row iteration: 64.6% of CPU; CGO calls: 43.7% |
| Entity definition import | ID plus 20 foreign-key/association pairs, 21 resulting fields | 852.9 µs | 228,703 / 581 | Field-result slice growth: 43.5% of allocated bytes; PHP parsing: 26.7% of CPU |

The scanner already uses the combined query. Redesigning its storage protocol
is not justified by this isolated benchmark. Code-action edited-source parsing
and stale-state validation remain intact. The digest copy is a potential future
allocation target, but this pass does not add unsafe string conversions or a
persistent source cache to remove it.

## Verified comparisons

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

### Suppression markers

Previously, PHP analysis compared a full marker at every byte offset in as
many as three passes over a source file. A single scan now checks full markers
only at `@`, `n`, or `N`. The `noinspection` candidate also covers its `@` form.
The directive parser and diagnostic matching rules are unchanged. Unicode
case-fold comparison remains in place at candidate starts. The post-change
profile attributes 7.4% of CPU samples to the prefilter, down from 36.8%.

Case-variant regression tests exercise actual suppression behavior. A fuzz
oracle compares candidate detection with the original byte-window algorithm,
including malformed bytes, truncated markers, Unicode, and mixed case. A
15-second run with four workers completed 519,619 comparisons without a mismatch.

### Component block lookups

The block diagnostic pass now retrieves and deduplicates block names once per
component within one analysis. It still reports each usage at its original
range and checks cancellation. The cache is local to that pass, so later
analyses observe index updates. Regression coverage includes repeated usages,
different component catalogs, an initially empty block list, and subsequent
block additions/removals.

### Entity field assembly

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
into both variants; a Go build overlay restored the baseline importer while
keeping the benchmark code identical.

## Reproducing the profiles

Run from the repository root with the Go toolchain from `go.mod` (these profile the Go oracle). Benchmark fixtures own
their temporary indexes; no external checkout is needed.

```bash
profile_dir=$(mktemp -d)
while read -r name package benchmark; do
  go test "$package" -run '^$' -bench "^${benchmark}$" \
    -benchtime=2s -count=1 -benchmem \
    -o "$profile_dir/$name.test" \
    -cpuprofile="$profile_dir/$name.cpu" \
    -memprofile="$profile_dir/$name.mem"
  go tool pprof -top -cum "$profile_dir/$name.test" "$profile_dir/$name.cpu"
  go tool pprof -top -sample_index=alloc_space \
    "$profile_dir/$name.test" "$profile_dir/$name.mem"
done <<'TASKS'
php ./internal/php BenchmarkSemanticAnalysis
diagnostics ./internal/lsp/diagnostics BenchmarkTwigComponentDiagnostics
actions ./internal/lsp BenchmarkLegacyActionValidation
scanner ./internal/indexer BenchmarkFileScannerScanFileStates/combined
import ./internal/shopware/entityschema BenchmarkImportDefinitionAssociations
TASKS
```

For timing comparisons, build each variant once with `go test -c`, then run
its executable with `-test.run='^$'`, `-test.bench`, `-test.benchtime=500ms`,
and `-test.benchmem`, alternating variants. Execute the PHP binary from
`internal/php` so its relative fixture path resolves. Keep profiling and
validation jobs out of the timed comparison.

This review does not measure full-workspace cold/warm indexing, RSS, or an LSP
transport round trip. The default real-world fixture at
`/home/shyim/Developer/sw-trunk` is absent; integration compilation alone cannot
establish those costs.

## Validation

The full Go suite, full internal race suite, golangci-lint, and compilation of
all integration-tagged packages passed. After the importer optimization, the
full Go suite and integration compilation passed again, with race checks for
entityschema, scaffold, app, and CLI. The final empty-result ordering adjustment
passed the entityschema suite, its race tests, and lint. The suppression fuzz
run and alternating benchmark comparisons are described above. No VS Code or
protocol changes were made; client checks were not run.
