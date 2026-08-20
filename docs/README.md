# Shopware Language Server Documentation

## Start here

New to the codebase? Read [`architecture.md`](architecture.md) first. It
explains the process model, the layers and their dependency rules, what happens
between process start and a served request, and where a given change belongs.

Then read [`parser-architecture.md`](parser-architecture.md). Everything in the
system sits on the lossless CST, so understanding syntax kinds, byte ranges, and
the per-language `query` packages unblocks most work.

After that, read the subsystem you are changing.

## Architecture

| Document | Covers |
| --- | --- |
| [`architecture.md`](architecture.md) | System overview: process model, layers, package inventory, lifecycle, concurrency, configuration, a worked end-to-end example, testing strategy |
| [`parser-architecture.md`](parser-architecture.md) | Lexing, the event/marker parser, error recovery, the sink, the CST and its traversal API, positions and UTF-16, language frontends, embedded languages, the query packages, adding a language |
| [`indexing.md`](indexing.md) | File discovery, change detection, the indexing run, transactions and the single-writer gate, `DataIndexer` repositories, cache versioning, the filesystem watcher, writing an indexer |
| [`lsp-server.md`](lsp-server.md) | JSON-RPC transport and dispatch, the document manager, `SyntaxContext`, the provider pattern, registration, capabilities, gating, custom commands, the CLI and MCP frontends |
| [`diagnostics-pipeline.md`](diagnostics-pipeline.md) | Inspections and problems, the reporter, the diagnostic envelope and element anchors, quick fixes, configuration gating, scheduling and caching, parse-error diagnostics |
| [`refactoring-engine.md`](refactoring-engine.md) | Edits and conflict rules, document and workspace plans, element handles, plan validation, the PHP editor, rename, file rename, scaffolding |
| [`php-semantic-engine.md`](php-semantic-engine.md) | The PHP pipeline: binder, semantic generations, resolver, inference, the type algebra, generated runtime stubs |

## Reference and process

| Document | Covers |
| --- | --- |
| [`reference.md`](reference.md) | The exhaustive catalog: every CLI command, feature, configuration key, supported file type, and performance note |
| [`../AGENTS.md`](../AGENTS.md) | The contributor rulebook: invariants, conventions, per-area checklists, definition of done |
| [`../LSP.md`](../LSP.md) | Custom LSP methods and a Neovim configuration example |
| [`maintainability.md`](maintainability.md) | The maintainability review: measured hotspots, complexity ceilings, and why the large grammar files are intentionally centralized |

## Integration and coverage

| Document | Covers |
| --- | --- |
| [`phpstorm-integration.md`](phpstorm-integration.md) | The versioned editor-integration contract and the framework-only presentation profile |
| [`symfony-plugin-roadmap.md`](symfony-plugin-roadmap.md) | Symfony feature coverage mapped against the PhpStorm Symfony plugin |
| [`shopware-rector-parity.md`](shopware-rector-parity.md) | Shopware migration inspections and their Rector counterparts |

## Reading paths by task

**"I want to support new syntax."**
[`parser-architecture.md`](parser-architecture.md) →
`internal/parser/<lang>/{lexer,parser,syntax}` → add a lossless parser test and a
recovery test.

**"I want to add a completion / hover / definition feature."**
[`lsp-server.md`](lsp-server.md#writing-a-provider) →
[`parser-architecture.md`](parser-architecture.md#querying-nodes) for the query
helper you need → [`indexing.md`](indexing.md) if the data is not indexed yet.

**"I want to add a diagnostic."**
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#writing-an-analyzer) →
[`refactoring-engine.md`](refactoring-engine.md) if it ships a quick fix.

**"I want to index something new."**
[`indexing.md`](indexing.md#writing-an-indexer) →
[`architecture.md`](architecture.md#a-worked-example-end-to-end) for the full
index-to-provider path.

**"I want to change PHP type or inference behavior."**
[`php-semantic-engine.md`](php-semantic-engine.md) → `internal/php/{types,resolver,inference}`.

**"I need to understand why my quick fix is disabled."**
[`refactoring-engine.md`](refactoring-engine.md#validation) and
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#the-diagnostic-envelope-and-element-anchors).

**"Something is slow."**
[`architecture.md`](architecture.md#performance-guardrails) for the measurement
entry points, then the relevant subsystem's performance section.
