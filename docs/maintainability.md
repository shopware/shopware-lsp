# Maintainability Baseline

This document is a risk ranking, not a mandate to optimize code for a metric.
Parser dispatch and type-relation code can be inherently branch-heavy; refactor
it only with correctness fixtures and performance measurements in place.

## Current strengths

- Domain indexes are separated from protocol providers.
- Language frontends share one lossless CST (`swlsp-syntax`) and immutable
  byte ranges.
- Diagnostics and fixes use typed inspections and validated rewrite plans.
- The editor, CLI, and MCP surfaces reuse the same application composition
  (`swlsp-app`).
- `make rust-check` (rustfmt, `clippy -D warnings`, the workspace test suite,
  and compilation of the real-world integration suite) is the safety net for
  structural refactors.

## Largest production files

The largest non-test Rust source files are:

| File | Approximate lines | Main risk |
| --- | ---: | --- |
| `crates/shopware-entityschema/src/render.rs` | 2,500 | Entity-schema PHP rendering for every field and association kind. |
| `crates/shopware-entityschema/src/validate_spec.rs` | 2,300 | Ordered validation passes for identity, fields, and relations. |
| `crates/admin-twigmigration/src/entities.rs` | 2,200 | Entity tables for Administration Twig migration. |
| `crates/lsp-scaffold/src/entity_schema.rs` | 2,200 | Entity-schema load, import, and response assembly for the designer. |
| `crates/parser-php/src/parser.rs` | 1,700 | Central grammar dispatch; performance and recovery matter more than raw branch count. |
| `crates/admin/src/type_index.rs` | 1,600 | TypeScript declaration indexing and Vue contract resolution share one module. |

There is no line-count or cognitive-complexity gate for Rust; clippy's default
lints apply.

## Recommended sequence

1. Keep extracting the real-world scenario (`crates/app/src/tests/real_world`)
   into feature-focused checks backed by the shared workspace fixture. Keep one
   cold index and one restored workspace per run.
2. Split `crates/admin/src/type_index.rs` and the entity-schema render and
   validation modules by responsibility when those areas next change.
3. Continue inference and parser work one algorithm at a time, keeping
   targeted benchmarks (`crates/bench`) and focused fixtures in the same
   change.

For mechanical module splits, preserve declaration order where it affects
registration or persistence, and run `make rust-check` before merging.
