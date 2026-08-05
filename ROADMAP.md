# Shopware LSP Roadmap

This roadmap captures the PHP correctness and code-analytics work identified by
comparing Shopware LSP with the reverse-engineered PhpStorm PHP implementation
in `php-research`.

The goal is compatibility with useful PHP metadata and behavior, not a port of
IntelliJ's architecture. Shopware LSP should remain a lightweight Go language
server built around its native lossless CST, immutable semantic snapshots, and
shared SQLite index.

## Existing Foundation

The following parts already provide the right long-term foundation and should
be extended rather than replaced:

- Native, lossless PHP CST parsing with malformed-source recovery.
- Compact per-file workspace graphs for declarations and references.
- Immutable semantic snapshots with global, member, and reverse-reference
  indexes.
- A structured PHP type algebra covering unions, intersections, generics,
  shapes, literals, callables, conditionals, and PHPDoc types.
- Flow-sensitive local inference and declaration-based type assertions.
- Pluggable call inference for framework-specific behavior.
- Compressed MessagePack workspace graphs stored in SQLite with WAL.
- Generated, PHP-version-aware declarations from JetBrains PhpStorm stubs.

## Phase 1: Dynamic Call Contracts

Implement `.phpstorm.meta.php` support as typed semantic call contracts. Parse
metadata files with the existing PHP frontend and store normalized contracts
instead of copying PhpStorm's opaque signature encoding.

- [x] Define a compact `CallContract` model keyed by callable FQN and, where
  applicable, argument position.
- [x] Parse `override()` and `map()` directives.
- [x] Support `type()` and `elementType()` return rules.
- [x] Support `expectedArguments()` and `expectedReturnValues()`.
- [x] Support `registerArgumentsSet()` and `argumentsSet()`.
- [x] Support conditional and unconditional `exitPoint()` contracts in
  control-flow analysis.
- [x] Persist project and generated JetBrains contracts in the semantic
  workspace graph.
- [x] Apply return contracts during call inference.
- [x] Surface effective contract return types in signature-resolution results.
- [x] Use expected arguments and reusable argument sets in completion.
- [x] Add fixtures for functions, inherited methods, argument/element returns,
  maps, expected values, argument sets, persistence, and terminating calls.
- [x] Add real framework fixtures for service locators, Doctrine-style
  repositories, and PHPUnit metadata patterns.

## Phase 2: JetBrains Attribute Semantics

The binder currently retains an attribute's resolved name and range but not its
arguments. Preserve only the payloads that have concrete semantic consumers and
normalize them into typed metadata.

- [x] Bind constant attribute arguments, including named arguments and arrays.
- [x] Implement `ReturnTypeContract` for conditional return types.
- [x] Implement `ExpectedValues` for literal and constant completion.
- [x] Implement `NoReturn` for control-flow termination.
- [x] Convert `ArrayShape` and `ObjectShape` into native semantic types.
- [x] Preserve `Deprecated` reason, replacement, and version metadata.
- [x] Keep `Pure`, `Immutable`, `FileReference`, and `Language` deferred until
  diagnostics or language features consume them.

Attribute support must not retain complete CST subtrees in published workspace
symbols. Normalize relevant payloads while binding and discard syntax-only
state before publication.

Completed in July 2026. Attribute payloads are typed, deeply detached from the
CST, persisted through sparse parameter/symbol side records, and embedded in
catalog format 4. Only the six attributes above retain arguments; all other
attributes retain their resolved name without arbitrary payload. Contract-only
parameter defaults are retained so omitted boolean arguments select the correct
return branch. Focused tests cover live source, compact workspace round trips,
generated stubs, completion, inference, hover, and diagnostics. The 59,954-file
`sw-trunk` oracle passes with a 9.45-second cold index, 0.83-second warm scan,
2.13-second cache restore, and 278.0 MiB retained Go heap on the recorded run.

## Phase 3: Diagnostic Correctness

### Centralized suppression

Replace the separate inference and deprecation suppression checks with one
diagnostic filtering service.

- [x] Match suppression directives to specific diagnostic identifiers.
- [x] Support `@phpstan-ignore`, `@phpstan-ignore-line`, and
  `@phpstan-ignore-next-line` with correct scope.
- [x] Support `@noinspection` aliases for compatible inspection identifiers.
- [ ] Add PHPCS suppression support when PHPCS diagnostics are introduced.
- [x] Ensure an unrelated suppression never hides all diagnostics on a line.
- [x] Test inline, preceding-line, block-comment, and multiple-diagnostic cases.

### PHP language-level validation

Keep the parser permissive and capable of parsing the newest supported syntax.
Validate feature availability in the semantic diagnostic layer using the PHP
version selected from project configuration.

- [x] Introduce a centralized `LanguageFeature` registry with minimum PHP
  versions.
- [x] Cover attributes, named arguments, enums, readonly declarations,
  intersections, DNF types, property hooks, asymmetric visibility, and future
  syntax additions.
- [x] Emit stable `php.version` diagnostic codes for unavailable features.
- [x] Reuse the registry in diagnostics, completion, and code actions instead
  of scattering version comparisons across providers.
- [x] Test version boundaries against Composer `require.php` and
  `config.platform.php` settings.

Completed in July 2026. The registry covers PHP 8.0 through 8.4 syntax,
including union types, match/throw expressions, the nullsafe operator, and
constructor promotion in addition to the features listed above. The provider
emits suppressible `php.version` diagnostics only for configured projects;
attribute completion and property-injection code actions consult the same
registry. Composer requirement and platform-override tests cover boundaries.
This work also fixed property-hook parsing so a completed hook block no longer
absorbs following class members; index version 98 invalidates affected graphs.

## Phase 4: Semantic Navigation Indexes

Extend snapshot publication with narrow reverse indexes. Keep the indexes
compact and derive them from the existing per-file workspace graph.

- [x] Index base classes and interfaces to direct subclasses/implementors.
- [x] Index traits to consuming classes.
- [x] Index methods to their overriding implementations.
- [x] Model `class_alias()` as a synthetic alias declaration and target
  relationship.
- [x] Implement `textDocument/implementation` using the reverse indexes.
- [x] Implement LSP prepare/supertypes/subtypes type-hierarchy requests.
- [x] Add navigation tests for inheritance, interfaces, traits, aliases, and
  workspace overlays.

Prefer direct-edge indexes with traversal at query time. Do not materialize
transitive closure unless profiling demonstrates a clear benefit.

Completed in July 2026. Immutable snapshots derive direct type and trait edges
lazily; method override indexing is guarded separately so ordinary type
hierarchy queries do not scan member tables. Open-document overlays build their
own derived generation. Static `class_alias()` calls publish flagged synthetic
class declarations and target relationships without appearing as nominal
subclasses. Standard implementation and type-hierarchy requests are advertised
and routed by the server. Focused provider and snapshot tests cover transitive
implementation lookup, interfaces, traits, method overrides, aliases, and
overlay replacement. The full suite, focused race suite, and linter pass.

## Phase 5: PHP Completion Quality

- [x] Insert short class names and add missing `use` imports through additional
  text edits.
- [x] Complete literals, constants, and flags from call contracts and
  `ExpectedValues`.
- [x] Rank candidates deterministically using exact/prefix match, current
  namespace, existing imports, same package, and deprecation state.
- [x] Apply metadata-driven completion to Symfony and Shopware service/factory
  APIs without coupling the core PHP evaluator to those frameworks.
- [x] Add integration tests for insertion text, import edits, duplicate-import
  avoidance, aliases, and grouped imports.

Core PHP class completion now inserts a short name plus a `use` edit, reuses
simple/grouped aliases, avoids duplicate or conflicting imports, and respects
explicit fully-qualified input. Ranking uses stable sort keys for textual
match, current namespace, existing imports, Composer PSR-4 package ownership,
and deprecation. Focused provider tests verify the resulting edits and order.
Normalized metadata return rules are evaluated by signature resolution, so a
contract only wins after a compatible declaration overload is found;
metadata-only callables retain a safe fallback. Framework-shaped tests cover a
PSR/Symfony service provider implemented by a Shopware container, a Shopware
factory, Doctrine repository lookup, and PHPUnit mock creation without adding
framework names to the generic evaluator.

Machine-learned ranking is not planned. A deterministic ranker is easier to
test, cheaper to run, and appropriate for a lightweight language server.

## Phase 6: Stub Coverage and Selection

Continue generating stubs from `jetbrains/phpstorm-stubs`, but make extension
selection project-aware.

- [x] Retain Composer `ext-*` requirements in the project model.
- [x] Generate the full supported extension catalog as separable bundles.
- [x] Load PHP core plus extensions required by the project.
- [x] Provide configuration overrides for extensions not declared in Composer.
- [x] Diagnose unavailable extension symbols only when project/runtime
  configuration provides enough evidence.
- [x] Version the stub catalog independently from unrelated workspace indexes
  where practical.

The objective is broader correctness without retaining every extension's
symbols in memory for every workspace.

Completed in August 2026. Catalog format 6 stores each extension's versioned
declarations and metadata contracts as an independently encoded bundle, plus a
compact ownership header for evidence-gated diagnostics. Project composition
always loads the small PHP core dependency closure, adds Composer
`require`/`require-dev`
`ext-*` entries, and accepts explicit enabled/disabled extension lists from LSP
initialization options. Composer platform entries set to `false` are treated as
explicit disables. Only that negative evidence produces `php.extension`;
symbols from an unselected but otherwise unknown runtime extension do not
produce misleading undefined-symbol diagnostics. The embedded catalog was
regenerated from the unchanged pinned JetBrains commit as 10,374 records from
18,171 parsed symbols in 114 files.

## Phase 7: Inspections and Structural Rewrites

- [x] Replace protocol-facing diagnostic providers with byte-oriented
  `ProblemAnalyzer` implementations.
- [x] Register every diagnostic through the inspection registry with declared
  languages and stable problem identifiers.
- [x] Bind exact quick-fix instances while reporting a problem, including
  multiple choices backed by the same fix implementation.
- [x] Keep protocol diagnostic conversion and the private fix envelope at the
  LSP boundary.
- [x] Resolve rewrite anchors against the current lossless CST before editing.
- [x] Support eager and lazy single-file, cross-file, command, and create-file
  fixes through shared rewrite plans.
- [x] Remove diagnostic-payload edit coordinates and the legacy diagnostic
  code-action fan-out providers.
- [x] Keep context-only intentions, generators, and refactors behind the
  separate `ActionProvider` interface.

Completed in August 2026. All PHP, Twig, XML, YAML, JavaScript, JSON, CSS, and
XPath analyzers now return native problems with byte ranges. Specialized
inspections own their fixes, while generic analyzers receive suggestion fixes
through the same registry. Tests cover stale-anchor rejection, exact duplicate
fix bindings, validated YAML and PHP rewrites, cross-file method creation, and
safe template create-file plans. The integration diagnostic harness consumes
the same analyzer contract as production.

## Evidence-Gated Work

The following work should begin only after profiling or a concrete diagnostic
requires it:

### Explicit control-flow graphs

The current structured flow analyzer should remain the default. Introduce an
ephemeral, per-function CFG if precise unreachable-code, must-return,
`finally`, complex-loop, or `goto` analysis cannot be implemented correctly
with the existing model. CFGs should not be persisted by default.

### Incremental parsing and lazy function bodies

The current parser is fast enough that parser complexity is not yet justified
by the available measurements. Profile complete open-document update latency
on large PHP files first, separating parsing, binding, inference, diagnostics,
and publication costs.

### Additional refined types

Add refinements such as integer ranges, positive/non-negative integers,
non-empty strings, and numeric strings only when they materially improve a
diagnostic, completion, or framework contract.

### Persisted inverted indexes

Persist additional reverse indexes only if cold startup or snapshot rebuilds
are measured bottlenecks. SQLite remains the storage and crash-recovery layer.

## Explicit Non-Goals

- Recreating IntelliJ's PSI interface hierarchy.
- Replacing the native type algebra with PhpStorm's encoded string-set types.
- Copying IntelliJ's persistent-map, string-enumerator, or custom WAL formats.
- Porting PhpStorm's inspection catalog wholesale.
- Persisting complete syntax trees or control-flow graphs for all files.
- Implementing machine-learned completion ranking.
- Building a PHP formatter before the semantic correctness milestones above.

## Validation

The August 2026 roadmap pass is verified by `go test ./...`, focused race
coverage for the PHP, semantic LSP, and application composition packages,
`golangci-lint run`, the VS Code TypeScript build, and byte-for-byte repeated
stub generation from the pinned JetBrains revision. The sole unchecked item is
intentionally conditional: PHPCS suppression belongs with future PHPCS
diagnostics and has no current diagnostic producer to integrate.

Each phase should include focused unit tests and a real-world indexing/LSP
scenario against `sw-trunk`. Track at least:

- Diagnostic precision and false-positive regressions.
- Completion relevance and correctness of generated edits.
- Definition, implementation, and hierarchy navigation correctness.
- Cold-index time and restored-index time.
- Steady-state RSS after indexing.
- Open-document edit latency, split by parser, binder, inference, and
  diagnostics.
- Persisted index size and snapshot publication allocations.

Performance work should be driven by these measurements and must preserve
malformed-source recovery and semantic correctness.
