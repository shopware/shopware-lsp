# PHP semantic engine

The PHP engine is a native Rust pipeline. It does not depend on tree-sitter, a
PHP subprocess, or an LSP-specific syntax representation. Its main design
constraint is that syntax, semantic state, and transport remain replaceable
independently.

## Pipeline

1. `swlsp-parser-php` (`crates/parser-php`) lexes and parses source into a lossless immutable CST.
   Every source byte belongs to a token, including trivia and malformed input.
2. `swlsp-php-binder` creates declarations, lexical scopes, imports,
   references, PHPDoc metadata, templates, promoted properties, and local
   symbols. Binding is file-local and deterministic.
3. `swlsp-php-semantic` publishes immutable document and workspace
   generations. A namespaced SQLite repository persists documents; open files
   are overlaid without mutating the indexed generation.
4. `swlsp-php-resolver` resolves names, generic inheritance, members, and
   call signatures. Class templates are invariant by default and honor
   explicit covariance or contravariance.
5. `swlsp-php-inference` evaluates expressions and control flow. A bounded
   fixed-point pass makes inferred returns available to calls in the same
   document; after a bulk indexing run, files with inferred returns are
   analyzed again so returns inferred from other files of the run are
   complete (see [settling](indexing.md#settling-cross-file-results)).
   Framework extensions may refine call results without coupling the core
   engine to Shopware or Symfony.
6. `swlsp-lsp-phpsemantic` adapts the semantic graph to completion, hover,
   definition, references, signature help, rename, and diagnostics.

The workspace index contains stable symbol IDs rather than CST pointers.
References retain semantic names and receiver types, allowing them to resolve
when a target is indexed later. Unsaved overlays rebuild reverse references
against the overlay generation, so navigation never mixes source versions.

## On-demand inferred return types

A function or method without a declared return type gets one inferred from
its `return` statements, and the declaration is flagged
`Flags::INFERRED_RETURN`. That type can depend on other files, so the value
persisted with the declaration is only as good as the generation its file was
indexed against, and it goes stale when a dependency changes.

Requests therefore do not trust it, the way rust-analyzer recomputes derived
facts as queries instead of storing them. `PhpIndex` registers a
`ReturnInference` with the semantic store (`crates/php/src/return_inference.rs`);
every published generation carries a memo of it
(`crates/php-semantic/src/lazy_returns.rs`). Materializing a flagged
declaration through a snapshot (`SymbolView::materialize`, and the lookups
built on it: `functions`, `members`, `symbol`, member resolution) asks the
memo for the declaring file; on first use the file is read from disk and its
function bodies are analyzed against that generation, recursing into other
files on demand. Results live as long as the generation; overlays for open
documents share it, and the next published generation starts empty. A cycle
(a file whose inference needs its own inferred returns, possibly through
other files) falls back to the stored types, as does a file that cannot be
read.

Rules for consumers:

- Request-time analysis uses `PhpIndex::analyze_document` (current
  generation, on demand). Indexing uses `PhpIndex::analyze_parsed_file`,
  which analyzes against `PhpIndex::indexing_snapshot` (stored types), so
  preparing one file never analyzes others.
- Listings over many declarations must not trigger analyses: use
  `SymbolView::materialize_stored`. `Snapshot::global_symbols`,
  `Snapshot::all_symbols`, and global completion items already do.

On a Shopware checkout the request benchmark shows no latency change, and
`shopware-lsp check src/Core/Checkout` infers 17 files on demand (about
0.13 s).

## Type model

`swlsp_php_types::Type` (`crates/php-types/src/ty.rs`) is an immutable algebra with canonical
serialization. It represents:

- scalar, `mixed`, `never`, `void`, `null`, and literal types;
- named objects, `self`, `static`, and `parent`;
- unions, intersections, nullable types, and DNF combinations;
- arrays, lists, iterables, array/object shapes, and `array-key`;
- callables with named, optional, variadic, and by-reference parameters;
- `class-string`, templates, bounds, defaults, and declared variance.

`Relations` (`crates/php-types/src/relations.rs`) owns subtype, assignability, join, narrowing, and exclusion
rules. Workspace hierarchy details are supplied through small interfaces, so
the type crate has no dependency on the index or parser.

## Project and runtime model

`swlsp-php-project` reads the root Composer metadata, dependency autoload
metadata, and the configured PHP platform version. `swlsp-php-stubs`
publishes a version-aware internal semantic document generated from a pinned
JetBrains phpstorm-stubs revision. The generator parses the selected upstream
extensions with the same native lossless PHP frontend used for project source,
applies `@since`, `@removed`, `PhpStormStubsElementAvailable`, and
`LanguageLevelTypeAware` metadata, then coalesces identical declarations across
PHP versions into a compact embedded catalog. Startup decodes that catalog
once; it never parses upstream PHP stub source at runtime. Internal symbols
participate in resolution but are excluded from project-class listings and
cannot be renamed.

The generated document is intentionally expressed through the same semantic
model as project source. A small hand-written overlay retains Shopware LSP's
more precise PHPDoc generic and inference contracts. Expanding ordinary native
runtime coverage therefore happens by updating the pinned source and catalog;
only semantic refinements belong in the overlay.

## Diagnostics

The engine currently reports:

- parser recovery errors;
- undefined classes, variables, methods, properties, and class constants;
- deprecated symbol use;
- inaccessible private/protected/asymmetric-write members;
- argument, named-argument, variadic, template-bound, and constructor
  mismatches;
- incompatible return values;
- duplicate declarations;
- final-class/final-member violations;
- method variance, property invariance, static/readonly, and visibility
  override violations;
- invalid `#[Override]` and missing abstract/interface implementations.

Unknown or dynamic values are treated conservatively. Undefined-member
diagnostics are suppressed for `mixed`, unindexed receiver classes, and
classes providing the relevant PHP magic fallback.

## Extension points

- Add syntax without affecting semantics by extending PHP syntax kinds,
  lexer/parser tests, and query helpers.
- Add ordinary runtime declarations upstream or update the pinned generated
  catalog; add only generic/inference refinements to the stubs overlay.
- Add domain call inference by implementing the `Extension` trait
  (`crates/php-inference/src/extensions/mod.rs`) and registering it in
  `swlsp-app`.
- Add a semantic consumer against `PhpIndex::semantic_snapshot`; do not retain
  CST nodes across document versions.

Every new syntax feature needs a lossless parser test. Type changes need
canonicalization and relation tests. Cross-file features need an overlay test
and a delayed-index-order test. LSP features should be tested through a real
`TextDocument` so UTF-16 ranges are covered.

## Performance guardrails

Benchmarks live in `crates/bench/benches/php.rs` and separately measure
parsing, binding, and semantic analysis. The parser and binder are
linear in source size. Member lookup uses per-container indexes; class lookup
uses normalized-name indexes. Fixed-point inference overlays only changed
symbol values and shares immutable indexes.

Run:

```bash
cargo bench -p swlsp-bench --bench php
cargo test -p swlsp-parser-php
```

The next useful depth improvements are extension-aware runtime catalog
selection, class-scoped PHPDoc type aliases/imported aliases, and a dependency
worklist for workspace-wide incremental re-analysis. Those are additive; they
do not require another engine replacement.
