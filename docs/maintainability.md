# Maintainability Baseline

This document records the August 2026 baseline and the September 2026 follow-up.
It is a risk ranking, not a mandate to optimize code for a metric. Parser dispatch and
type-relation code can be inherently branch-heavy; refactor it only with
correctness fixtures and performance measurements in place.

## Current strengths

- Domain indexes are separated from protocol providers.
- Language frontends share one lossless CST and immutable source ranges.
- Diagnostics and fixes use typed inspections and validated rewrite plans.
- The editor, CLI, and MCP surfaces reuse the same application composition.
- The full backend suite, internal race suite, and configured linter provide a
  strong safety net for structural refactors.

## Measured hotspots

The August snapshot contained roughly 403,000 lines of Go including generated,
fixture, and test code. Its largest production files included:

| File | Approximate lines | Main risk |
| --- | ---: | --- |
| `internal/parser/php/parser/parse.go` | 1,900 | Central grammar dispatch; performance and recovery matter more than raw branch count. |
| `internal/twigcomponent/index.go` | 1,700 | Component catalog persistence and resolution still share one index facade. |
| `internal/parser/twig/ast/nodes.go` | 1,700 | Typed lossless node wrappers are repetitive but intentionally centralized. |
| `internal/php/types/type.go` | 1,600 | Type construction, canonicalization, and rendering share the core value implementation. |
| `internal/admin/type_index.go` | 1,600 | TypeScript declaration indexing and Vue contract resolution share one package file. |
| `internal/parser/twig/parser/grammar_tags.go` | 1,600 | Tag grammar dispatch is large and recovery-sensitive. |

The first exploratory production-only run found functions with cognitive
complexity as high as 301. After decomposing the indexing coordinator,
protocol dispatch, cross-language Administration providers, PHP relations,
flow analysis, validation, profiling, diagnostics, schema generation, and
serialization, the maximum was 65. At that point, 268 findings remained at
`gocognit`'s strict default threshold of 30, so that threshold remained a
noisy legacy gate. CI now enforces a ceiling of 65 while the existing
mid-complexity backlog can be reduced deliberately.

The September production-only scan now has a maximum of 64. Useful remaining
61–65 complexity targets include:

1. `entityschema.(*specValidator).validateIdentity` — separate identifier,
   class ownership, and relation identity validation with boundary-case fixtures.
2. `scaffold.(*Provider).entitySchemaLoad` — separate document acquisition,
   import, and response assembly while preserving session/history ownership.
3. `completion.(*AdminCompletionProvider).twigVueMemberCompletionsAt` — split
   receiver discovery from member filtering and LSP item rendering.

`phpdoc.Parse` and large lexer/parser switches are lower
priority. Their complexity is mostly grammar dispatch and changes can affect
recovery behavior or hot-path latency.

## September 2026 follow-up

A fresh production-only scan with golangci-lint 2.13.0 found 298 findings at
threshold 30 before these passes, and 294 afterward. The configured ceiling remains
65; this pass does not weaken or tighten the repository-wide gate.

| Entry point | Complexity before | After | Largest new helper |
| --- | ---: | ---: | ---: |
| `hover.(*AdminHoverProvider).buildHoverContent` | 65 | 3 | 10 |
| `admin.JavaScriptSymbolAt` | 63 | 17 | 8 |
| `diagnostics.(*TwigComponentAnalyzer).Analyze` | 61 | 9 | 19 |
| `entityschema.importFields` | 65 | 3 | 6 |

Administration hover now assembles focused prop, event, member, slot, and source
location sections. Characterization tests preserve the complete Markdown output,
component order, deprecation details, and definition-path precedence.

JavaScript symbol recognition keeps its ordered semantic checks and delegates
literal registrations to argument and property recognizers. Tests cover both
arguments of `Component.extend`, registration versus reference context, service
registration, unrelated strings, and incomplete input. Registry precedence and
persisted symbol meaning are unchanged.

Twig component diagnostics use one per-document run with separate component,
block, Live Action, and argument checks. The run retains diagnostic ordering,
exact ranges, suggestion payloads, and the existing catalog APIs. Cancellation is
checked between passes as well as in the reference loops. Tests cover diagnostic
ordering, case-insensitive Live Action arguments, unknown actions, and cancellation.

Three 300 ms samples on Linux/AMD EPYC measured JavaScript symbol recognition at
2.19–2.39 µs before and 2.01–2.31 µs afterward, with four allocations in both
versions. The 20-problem Twig fixture measured 129–137 µs before and 116–138 µs
afterward; allocations increased from 401 to 403 (about 256 additional bytes).
These small fixtures show comparable request costs, not a workspace-wide speedup.

Entity-schema importing now separates extension, translation, flag, field,
hierarchy, and association responsibilities. The main import file shrank from
2,456 to 906 lines. One ordered field importer owns foreign-key pairing and
hierarchy assembly; to-one constructors share flag and modifier assembly while
retaining their different argument order and autoload defaults. Public APIs and
persisted data are unchanged.

Alternating baseline/current benchmark runs for 20 associations measured
878–919 µs before and 891–938 µs afterward, with about 1.1 KB and two additional
allocations per import. The small overhead is recorded rather than claiming a
performance improvement from the structural change.

These figures describe the maintainability refactors before the follow-up
[performance review](performance-review.md), which profiles five tasks and
measures targeted diagnostic and importer optimizations separately.

The new import tests cover column/association flag separation, optional local
columns, round trips, deterministic unpaired-column ordering, preserved unknown
expressions, and rejection of partially dynamic field arrays. The entity-schema
suite currently covers 81.8% of statements; the association and hierarchy
assembly helpers have 100% statement coverage. This is coverage of the tested
paths, not proof that all PHP constructs are supported.

Asset probes in the real-world scenario now use a shared typed snapshot for
indexed/restored subtests. A portable production-workspace test runs the same
probes with temporary fixtures, verifies restore without rescanning, applies
source edits and deletions through the scanner, and verifies another restart.
The external Shopware checkout was absent for this pass, so real-world validation
is limited to compilation; the large scenario still needs further domain splits.

Repeatable request benchmarks live alongside these tests:

```bash
go test ./internal/admin ./internal/lsp/diagnostics -run '^$' \
  -bench '^(BenchmarkJavaScriptSymbolRecognition|BenchmarkTwigComponentDiagnostics)$' \
  -benchtime=300ms -count=3
go test ./internal/shopware/entityschema -run '^$' \
  -bench '^BenchmarkImportDefinitionAssociations$' -benchtime=300ms -count=3
```

## Improvements completed in this review

- Split application provider registration by LSP capability. The former
  812-line `registerFeatures` function is now a short ordered composition root,
  with symbols, completion, definitions, code lenses, references, hover,
  editor helpers, and actions/commands in focused files.
- Replaced repeated partial-cleanup branches in `NewAdminComponentIndexer`
  with one typed repository-opening state and reverse-order failure cleanup.
  Adding another repository no longer requires copying every prior close call.
- Split the 6,000-line Administration indexer into core indexing, catalog
  queries, usage/navigation, component resolution, Twig/Vue typing, effective
  component merging, and JavaScript registration parsing files. The package
  boundary and declaration behavior remain unchanged.
- Split the 5,500-line compact PHP workspace graph into graph encoding,
  symbols, signatures, metadata, references, packing/materialization, and
  public symbol-view files. MessagePack field names and ordering were not
  changed.
- Split the 3,400-line Administration diagnostic analyzer into JavaScript/core,
  Twig member, registry/route, privilege/block, and component-contract files.
- Split the 2,700-line PHP semantic LSP adapter into completion, hover,
  definition, references, signatures, rename, diagnostics, and shared helper
  files. The same `Provider` continues to implement every interface.
- Split the 2,400-line Administration definition parser into component shape,
  Composition API setup, local registrations, members, prop validation, and
  emitted-event files. The parser API and CST query behavior remain unchanged.
- Split the 2,300-line Administration completion implementation into its
  provider facade, JavaScript, Twig routing, prop values, lexical scope,
  component contracts, and slot files. Completion precedence remains in the
  same methods and order.
- Split Administration hover, usage extraction, Twig/Vue lexical analysis,
  PHP expression/flow inference, semantic snapshots, and subtype relations by
  domain. The largest newly focused implementation files are below 700 lines,
  apart from persistence and diagnostic files with their own focused follow-up.
- Replaced the 301-complexity indexing function with an explicit indexing run
  that owns filtering, preparation, worker batches, transaction retries,
  workspace-symbol publication, and file-state commits as separate phases.
- Replaced the protocol mega-switch with a typed method-handler table and
  shared parameter decoders. Lifecycle, document, configuration, and indexing
  handlers now have independent entry points.
- Reworked entity-schema validation into ordered validation passes, PHP flow
  conditions into specialized refiners, subtype checks into relation families,
  and workspace storage profiling into a stateless collector.
- Reworked Administration semantic tokens, Twig usage/symbol lookup, rename,
  and workspace-symbol collection around small scanners, plans, and collectors.
- Split translation and asset diagnostic runs into catalog acquisition,
  reference validation, lookup, and diagnostic presentation. Split PHP
  semantic diagnostics and Symfony Form/Doctrine diagnostics into focused
  passes and role-specific handlers.
- Split type rendering by type family, array-literal inference by literal
  shape, Administration declaration lookup by symbol kind, and Vue type
  resolution by wrapper/union/intersection/special/declaration phases.
- Reworked Symfony PHP service evaluation, call-signature resolution,
  PhpStorm-stub generation, entity migration SQL, and workspace-parameter
  decoding into explicit staged workflows.
- Reworked Administration symbol usage, script-setup contract enrichment,
  Twig/Vue call scanning, and CLI workspace-edit application around small
  stateful collectors.
- Added a shared real-world workspace fixture and split integration support
  helpers into request, snapshot, workspace, and fixture files. The remaining
  scenario body is explicitly tracked below.
- Added a 2,500-line production-file regression test, a cognitive-complexity
  ceiling of 65, integration-suite compilation to normal validation, and
  aligned CI with the Go 1.27.0 / golangci-lint 2.13.0 toolchain in `mise.toml`.

## Recommended sequence

1. Continue extracting the roughly 9,600-line real-world scenario body into
   feature-focused checks backed by the new shared workspace fixture. Keep one
   cold index and one restored workspace per run.
2. Refactor the remaining 61–65 complexity band only when a cohesive domain
   split or correctness improvement is available; avoid metric-only parser
   rewrites.
3. Split `twigcomponent/index.go` and `admin/type_index.go` by persistence,
   lookup, and resolution responsibilities when those areas next change.
4. Continue inference and parser work one algorithm at a time, keeping
   targeted benchmarks and differential fixtures in the same change.
5. Lower the repository-wide complexity ceiling from 65 only after every
   current function above the new threshold has focused correctness coverage.

For mechanical file splits, preserve declaration order where it affects
registration or persistence, run `go test ./...`, and run
`go test -race ./internal/...` before merging.
