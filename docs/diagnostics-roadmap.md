# Diagnostics Architecture Roadmap

The number of diagnostics will keep growing, so the cost of adding one, and the
cost of running all of them, matter more than any single inspection. This
document compares the pipeline described in
[`diagnostics-pipeline.md`](diagnostics-pipeline.md) with other analysis
engines and records the steps we take to close the gaps. Each step is
independently shippable and leaves the published diagnostics unchanged unless
it says otherwise.

## Comparison

| | Shopware LSP | IntelliJ | Roslyn | rust-analyzer | PHPStan |
|---|---|---|---|---|---|
| Rules declare IDs and default severities up front | yes, validated at composition | yes | `DiagnosticDescriptor` | partly | error identifiers |
| Quick fixes bound when the problem is reported | yes | `LocalQuickFix` | separate `CodeFixProvider` | yes | n/a |
| Stale-safe fix anchors | element handle + document version | smart pointers | partly | yes | n/a |
| Generated edits re-validated | yes (re-parse) | partly | no | no | n/a |
| Rule metadata next to the rule | yes (step 1) | `plugin.xml` + description beside the class | descriptor beside the analyzer | module per diagnostic | rule class |
| One tree walk shared by all rules | no; measured as unnecessary (step 4) | `buildVisitor` | `RegisterSyntaxNodeAction(kind)` | semantic diagnostics from queries | `Rule::getNodeType()` |
| Shared per-run context | PHP analysis memoized per document (`swlsp-lsp-phpanalysis`); no context object | `ProblemsHolder`, PSI caches | `SemanticModel` | salsa database | `Scope` |
| Central suppression | yes for PHP (step 2) | `@noinspection`, `SuppressQuickFix` | `#pragma`, `.editorconfig` | `#[allow]` | `@phpstan-ignore` |
| Project-wide rules | `ProjectAnalysis` (step 5) | `GlobalInspectionTool` | compilation-end actions | salsa queries | Collectors |
| Dependency-aware invalidation | declared inspection inputs; results reused while their revisions are unchanged (step 5) | modification trackers | incremental compilation | salsa | result cache |
| Test fixtures | `{\|code:text\|}` markup (step 3) | `<warning descr="…">` markup | `{|#0:…|}` markup | `//^^ error: …` | expected-errors arrays |

## Step 1: self-describing inspections

**Problem.** Adding an analyzer-backed inspection touches four places: the
analyzer in `swlsp-lsp-diagnostics`, its codes as string lists in
`crates/app/src/inspections.rs`, its configuration domain in the prefix table
`inspection_domain` (`crates/lsp/src/configuration.rs`), and, for language
diagnostics, the list in `inspection_presented_to_client`
(`crates/lsp/src/client.rs`). A new ID with an unexpected prefix silently lands
in the wrong domain.

**Change.**

- `InspectionDefinition` gains `domain` (the configuration domain that gates
  it; empty for none) and `scope` (`Language` for core-language diagnostics
  hidden from framework-only clients such as PhpStorm, `Framework` otherwise).
- The server gates on these fields; `inspection_domain` and the hard-coded
  client list are removed.
- `Registrar::inspection` rejects a domain missing from
  `swlsp_projectconfig::DOMAIN_CATALOG`, so a typo fails at startup.
- Every inspection, including the analyzer-backed ones wired inline in
  `app`, gets a constructor in `swlsp-lsp-inspections` beside its definition.
  `register_inspections` becomes one line per inspection.

**Done when** the published diagnostics, code actions and configuration
behavior are unchanged and every definition states its domain.

## Step 2: central suppression

**Problem.** Suppression comments (`@phpstan-ignore`, `@noinspection`) were
honored only by the analyzers that called `swlsp-php-suppression` themselves,
so a new inspection ignored them unless its author remembered.

**Change.** The server applies a per-language `SuppressionReader` to every
inspection's problems in `InspectionProblemReporter`; composition registers
the PHP reader. Analyzers no longer filter suppressed problems.

**Finding.** The comparison above assumed analyzers fetch PHP semantic data
independently. They do not: every analyzer that needs it goes through
`swlsp_lsp_phpanalysis::for_document` / `context_for_document`, which
memoize one analysis per document version and workspace revision. A shared
`InspectionContext` would therefore be ergonomic, not a performance fix, and
step 4 found no need for node dispatch that would require one.

## Step 3: marker-based test fixtures

**Problem.** A diagnostic test builds a document, runs the inspection and
compares problem lists by hand, so tests are long and assert ranges as
numbers.

**Change.** Problems are marked inline as `{|code:text|}`
(`swlsp_lsp::fixture`; the XML-like `<warning>` markup of IntelliJ would clash
with PHP, Twig and XML sources). `check_problems` (analyzers),
`check_diagnostics` and `check_fix` (inspections) compare the reported
problems rendered back into markup and print the differing lines on failure.
Every analyzer and inspection test that checks reported problems uses the
markup; the remaining direct calls test helpers, cancellation, hand-built
problems, multi-document fixes, or rule tables whose generated sources report
row-specific extras. The conversion pinned every range and surfaced problems
the old count-based assertions ignored, and three fixes that left stray text
behind: migration fixes now drop imports they orphan
(`swlsp_php_imports::remove_orphaned_imports`), the criteria ID-filter fix
removes the emptied line, and Store composer problems resolve their key by
JSON path instead of the first matching key in the file.

## Step 4: measure before sharing tree walks

**Premise.** Running every inspection costs roughly the number of inspections
times the file size, because each analyzer walks the tree, so a visitor form
with one walk per document (IntelliJ `buildVisitor`, Roslyn
`RegisterSyntaxNodeAction`) would pay off.

**Measurement.** `check --workers 1` with `SHOPWARE_LSP_TRACE_DIAGNOSTICS=1`
on a warm cache over `src/Core/Checkout`, `src/Storefront/Page`, the
storefront page templates and the `sw-order` Administration module (1,466
files) spent 12.4 s in diagnostics, about 8.5 ms per file. A full preorder
walk of the same PHP files costs about 10 µs per file (28 µs including
tokens). With roughly twenty PHP inspections walking two or three times each,
walking is at most about 6% of the total, and one shared walk would recover
only part of that. The premise does not hold: a visitor API is not worth its
cost now. The analyzers with many rules already share a scan internally (the
migration analyzer's `scan::ScanScope`).

The time goes to semantic and index work. Before the changes below:

| Inspection | Per file | Where the time goes |
|---|---|---|
| `php.semantic` | 3.3 ms | the shared PHP analysis of the document, which it triggers first |
| `shopware.admin` | 8–9 ms per Administration file | component, mixin and utility lookups across its checks |
| `twig.member.missing` | 3.2 ms per template | member resolution |
| `symfony.event` | 0.88 ms | the event catalog rebuilt for every document and every reference |
| `symfony.translation` | 0.68 ms | every message file path read from SQLite for every missing key |
| `shopware.migration` | 0.6 ms | type queries per call |

**Change.** Catalogs derived from an index are memoized per index revision
instead of rebuilt per document or per problem:

- `DataIndexer::revision()` advances after every committed change to a
  repository; `get_all_file_paths_view()` caches the path list like the other
  views.
- `swlsp_event::Index::events_view()` derives the event catalog once per
  revision of its records and of the PHP index (constants and listener types
  resolve through it).
- Translation insertion targets read the cached path list.

`symfony.event` went from 1,029 ms to 188 ms and `symfony.translation` from
881 ms to 434 ms on the same run; the published diagnostics are
byte-identical.

**Next.** Profile the remaining hot inspections the same way (per-inspection
trace first, then a CPU profile of the CLI built with `--features
cpu-profile`). `DataIndexer::revision()` and `PhpIndex::revision()` are the
inputs step 5 needs to key cached diagnostics by the indexes they read.

## Step 5: project inspections and dependencies

**Problem.** Rules about the project as a whole (a name defined in two
files, an unused snippet) had no home: an analyzer sees one document. And any
committed index change re-diagnoses every open document.

**Design.** Three options were considered for project rules:

1. A separate inspection kind run once by `check` and published per file
   (IntelliJ global inspections). Needs its own scheduling, publication for
   files that are not open, and stale-range handling when an open document
   differs from what was indexed.
2. Per-document analyzers that query the index directly. Works today, but
   each document re-derives the same project-wide facts.
3. Two phases: a project summary derived from index data and memoized per
   index revision, checked per document against the live tree (PHPStan
   collectors and `CollectedDataNode` rules).

Option 3 reuses the whole per-document pipeline (gating, suppression,
configuration, quick fixes, the CLI and MCP), never reports a range from
stale index data, and costs one derivation per index revision. It cannot show
a problem in a file that is not open, which `check` covers.

**Change.**

- `ProjectAnalysis` and its `Project` adapter in `swlsp-lsp-diagnostics`
  (see [project analyses](diagnostics-pipeline.md#project-analyses)): a
  lazy summary memoized per `revision()`, then a per-document check.
- `symfony.route.redefined` as the first project rule: a route name defined
  in another project file, where Symfony silently keeps only the definition
  loaded last. Definitions in `vendor/` (overriding a bundle route by name
  is legitimate) and test directories (fixtures redefine production routes
  on purpose; two such findings on Shopware led to this exclusion) are
  ignored. A Shopware `check` of `src`, `custom` and `tests` reports none.

**Dependency-aware refresh.** The scanner cannot tell which indexes a run
changed (every indexer sees every file, and a repository counts as touched
on deletes that remove no rows), so refresh stays document-wide and the
work is saved one level down: inspections declare their inputs
(`ProblemAnalyzer::input_revision` beside each analyzer's fields, revisions
from `DataIndexer::revision`, the compiled
catalogs and `PhpIndex::revision`), and a refresh reuses each inspection's
diagnostics while its input revision, the document and its policy are
unchanged. Declarations are opt-in and audited: the document-only rules and
the inspections reading only the PHP, service, route, console, event,
serializer, translation, snippet and DAL indexes declare them; inspections
reading files from disk, extension configuration or indexes without a
revision (Twig, assets, Administration, themes, app scripts) run on every
refresh as before.

Follow-ups closing the gaps of the first version: the per-document PHP
analysis is keyed by `PhpIndex::analysis_revision()`, which includes type
extensions, so `$container->get()` types no longer lag behind service
changes; the Twig index has a revision (its repositories plus the PHP and
service indexes it resolves through) and the Twig deprecation, member,
render-block and macro inspections declare it; repositories and the service
index's prototype caches no longer advance on writes that change nothing,
which had made every YAML save re-run all PHP inspections; and
`inspection_inputs_test.rs` audits the declarations, catching a dropped
translation, snippet or Twig input.

Measured on 32 open Shopware Checkout and Storefront documents, a refresh
whose index change touched none of the declared inputs took about 11–26 ms
instead of 24–50 ms. Refreshing was already cheaper than the 8.5 ms per
file of a first `check` because the per-document PHP analysis is memoized
across refreshes.

## Follow-ups after step 5

- **Inputs declared beside the analyzer.** `ProblemAnalyzer::input_revision`
  sums the revisions of the indexes an analyzer holds (`Revisioned` on index
  types, `Option` and `Arc`), so the declaration sits next to the fields it
  covers instead of in the composition root.
- **Refresh skips unchanged documents.** After an index change, a document
  whose inspections all declare inputs and none changed is not re-diagnosed.
  Most documents still have an undeclared inspection (assets, Doctrine,
  routing, templates, Twig components, themes), so this mostly pays off once
  those read files through tracked inputs.
- **Suppression beyond PHP.** `@noinspection` comments in Twig, YAML, XML,
  JavaScript and SCSS through one CST-based reader.
- **Inspection descriptions and a generated reference.** Every definition
  carries a description, registration rejects an empty one, the
  configuration catalog exposes it with the domain, and
  [`inspections.md`](inspections.md) is generated from the registry.
- **Fix all in file**, applied sequentially (see
  [diagnostics-pipeline.md](diagnostics-pipeline.md#fix-all-in-file)).
- **Diagnostics benchmark** in `crates/bench` covering every inspection on a
  PHP and a Twig document.

- **Inputs for every PHP and Twig inspection.** The revision may depend on
  the document (`Some(0)` for documents an analyzer ignores); asset,
  Doctrine, Twig component, app script and extension indexes have
  revisions; routing and template analyzers declare theirs; files outside
  the scanner (Assetic resources, theme icons, versioning's `composer.json`
  files) are tracked by polled fingerprints. On 39 open Shopware Checkout
  and Storefront documents, a refresh whose index change touched none of
  their inputs re-diagnoses none of them (previously all 39).

- **Focused Administration and migration inspections.** `shopware.admin`
  became six inspections (component, component API, member, module, block
  override, migration) and `shopware.migration` three (API, signature,
  behavior), with unchanged codes. Inspections split from one analyzer share
  its run per document through `SharedAnalysis` and `CodeGroup`, memoized per
  document snapshot and input revision, so splitting costs no extra analysis.
  The Administration index gained a revision (its repositories plus every
  open-document overlay change), so Administration files declare inputs too.

- **Every inspection declares its inputs.** SCSS variables (style index and
  readiness), Store composer checks (extension configuration with its open
  overlays) and entity snapshots (entity catalog and a stamp of the plugin's
  snapshot directory) were the last; the audit test now fails on any active
  inspection without a declaration.
- **Time budget.** Inspections run cheapest first; in push mode the results
  so far are published before a slow inspection runs or once a document
  exceeds 200 ms, and an inspection over the budget is logged once.
- **Workspace diagnostics.** `workspace/diagnostic` reports project rules'
  problems in files that are not open, with result IDs so polling clients get
  `unchanged` for files whose problems did not change.

The roadmap is complete. Natural next steps are new project rules (unused
snippets, duplicate services across files) on the `ProjectAnalysis` base, and
a `workspace/diagnostic/refresh` after index changes for clients that do not
poll workspace diagnostics.

## Status

| Step | Status |
|---|---|
| 1. Self-describing inspections | done |
| 2. Central suppression | done |
| 3. Marker-based test fixtures | done |
| 4. Measure before sharing tree walks | done: catalogs memoized per index revision, visitor API not needed |
| 5. Project inspections and dependencies | done: project analyses, workspace diagnostics, declared inputs for every inspection, refresh skipping, time budget |
