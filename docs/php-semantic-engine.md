# PHP semantic engine

The PHP engine is a native Go pipeline. It does not depend on tree-sitter, a
PHP subprocess, or an LSP-specific syntax representation. Its main design
constraint is that syntax, semantic state, and transport remain replaceable
independently.

## Pipeline

1. `internal/parser/php` lexes and parses source into a lossless immutable CST.
   Every source byte belongs to a token, including trivia and malformed input.
2. `internal/php/binder` creates declarations, lexical scopes, imports,
   references, PHPDoc metadata, templates, promoted properties, and local
   symbols. Binding is file-local and deterministic.
3. `internal/php/semantic` publishes immutable document and workspace
   generations. A namespaced SQLite repository persists documents; open files
   are overlaid without mutating the indexed generation.
4. `internal/php/resolver` resolves names, generic inheritance, members, and
   call signatures. Class templates are invariant by default and honor
   explicit covariance or contravariance.
5. `internal/php/inference` evaluates expressions and control flow. A bounded
   fixed-point pass makes inferred returns available to calls in the same
   document. Framework extensions may refine call results without coupling
   the core engine to Shopware or Symfony.
6. `internal/lsp/phpsemantic` adapts the semantic graph to completion, hover,
   definition, references, signature help, rename, and diagnostics.

The workspace index contains stable symbol IDs rather than CST pointers.
References retain semantic names and receiver types, allowing them to resolve
when a target is indexed later. Unsaved overlays rebuild reverse references
against the overlay generation, so navigation never mixes source versions.

## Type model

`internal/php/types.Type` is an immutable algebra with canonical
serialization. It represents:

- scalar, `mixed`, `never`, `void`, `null`, and literal types;
- named objects, `self`, `static`, and `parent`;
- unions, intersections, nullable types, and DNF combinations;
- arrays, lists, iterables, array/object shapes, and `array-key`;
- callables with named, optional, variadic, and by-reference parameters;
- `class-string`, templates, bounds, defaults, and declared variance.

`types.Relations` owns subtype, assignability, join, narrowing, and exclusion
rules. Workspace hierarchy details are supplied through small interfaces, so
the type package has no dependency on the index or parser.

## Project and runtime model

`internal/php/project` reads the root Composer metadata, dependency autoload
metadata, and the configured PHP platform version. `internal/php/stubs`
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
- Add domain call inference by implementing `inference.Extension` and
  registering it in `internal/app`.
- Add a semantic consumer against `PHPIndex.SemanticSnapshot`; do not retain
  CST nodes across document versions.

Every new syntax feature needs a lossless parser test. Type changes need
canonicalization and relation tests. Cross-file features need an overlay test
and a delayed-index-order test. LSP features should be tested through a real
`TextDocument` so UTF-16 ranges are covered.

## Performance guardrails

Benchmarks live in `internal/php/benchmark_test.go` and separately measure
parsing, binding, analysis, and indexed lookup. The parser and binder are
linear in source size. Member lookup uses per-container indexes; class lookup
uses normalized-name indexes. Fixed-point inference overlays only changed
symbol values and shares immutable indexes.

Run:

```bash
go test ./internal/php -run '^$' -bench . -benchmem
go test ./internal/parser/php/parser -run '^$' \
  -fuzz FuzzPHPParserLossless -fuzztime=10s
```

The next useful depth improvements are extension-aware runtime catalog
selection, class-scoped PHPDoc type aliases/imported aliases, and a dependency
worklist for workspace-wide incremental re-analysis. Those are additive; they
do not require another engine replacement.
