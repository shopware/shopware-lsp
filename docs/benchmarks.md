# Benchmarks

Benchmarks are the regression early-warning system: CodSpeed runs them on
every pull request and tracks each benchmark by name over time. A benchmark
that nobody can attribute to a user-visible cost is noise, so the suite favors
a small number of realistic workloads over micro-benchmarks of trivial
helpers.

## Where benchmarks run

`.github/workflows/codspeed.yml` runs `go test -run '^$' -bench=.` with the
walltime instrument, sharded per package group; CodSpeed merges the shards
into one report. Unit tests are skipped (`-run '^$'`) because the test
workflow already covers them.

When you add a benchmark to a package that no shard covers yet, add the
package to a shard in the same change, or the benchmark will never run in CI.

Run the suite locally through the CodSpeed CLI (once `codspeed` is installed):

```bash
mise run bench
```

or plain Go while iterating:

```bash
go test -run '^$' -bench '^BenchmarkSyntheticWorkspaceIndexing/cold$' \
  -benchtime=300ms -count=1 ./internal/app
```

## What to benchmark

Pick operations a user can feel:

- parsing and lexing hot paths (`internal/parser/performance_validation_test.go`);
- the indexing pipeline: cold index, warm no-op rescan, single-file change
  (`internal/app/benchmark_test.go`);
- request-time latency end to end through the JSON-RPC server: completion,
  hover, definition, pull diagnostics, workspace symbols;
- index lookups against a realistically populated store, not a
  single-document one.

The real-world, opt-in integration suite (`internal/app`, `integration` build
tag) measures full Shopware checkouts and is not part of CodSpeed. Synthetic
benchmarks complement it: deterministic, fast, and always runnable.

## Conventions

- Use `for b.Loop()`. It excludes setup from the timing automatically and
  keeps the compiler from hoisting or eliminating benchmarked calls. Do not
  combine it with `b.ResetTimer()`.
- Always `b.ReportAllocs()`. Allocation regressions are often the first
  visible signal in a garbage-collected server.
- Assign results to a package-level sink variable when the result would
  otherwise be discarded.
- Call `b.SetBytes(...)` for byte-throughput workloads such as lexers and
  parsers.
- Keep fixtures deterministic: generate content from loop indices, never use
  randomness, wall-clock values, network access, or external checkouts. Use
  `b.TempDir()` and point `SHOPWARE_LSP_CACHE_DIR` at it instead of touching a
  developer cache.
- Assert a meaningful result once before the timed loop (for example that a
  completion provider returns items). A silently broken provider must not
  masquerade as a fast one.
- Keep setup outside the loop. Per-iteration state — a fresh cache directory
  for a cold-index benchmark — belongs inside the loop only when it is part
  of the measured scenario.
- Keep names stable. CodSpeed tracks history by benchmark name; renaming
  discards the baseline. Use sub-benchmarks (`b.Run`) for variants of one
  workload.
- Keep one benchmark iteration comfortably below a second so the default
  benchtime collects enough samples.

## Measuring beyond CodSpeed

For profiling methodology, before/after comparison discipline, and the limits
of fixture-level measurements, see [`performance-review.md`](performance-review.md).
