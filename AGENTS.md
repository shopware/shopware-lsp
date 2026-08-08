# Shopware LSP Contributor Guide

This file applies to the entire repository. Add a more specific `AGENTS.md`
inside a subdirectory only when that area needs additional rules.

## Product and architectural invariants

Shopware LSP provides completion, navigation, hover, diagnostics, refactoring,
symbols, and other IDE features for Shopware and Symfony projects. The backend
is written in Go; the VS Code client is TypeScript.

Keep these invariants intact:

- The server supports one workspace folder per process. Clients implement
  multi-root support by starting one process per workspace folder.
- The editor, CLI, and MCP server use the same production workspace,
  configuration, indexes, diagnostics, providers, and rewrite engine. Do not
  implement a second analysis path for one frontend.
- Language parsing uses the native, lossless CST infrastructure under
  `internal/parser`. Do not introduce Tree-sitter or a second parser stack.
- CST ranges are byte offsets into the exact source snapshot. Convert to or
  from UTF-16 positions only at the LSP boundary.
- Parsers must tolerate incomplete editor input and return a usable, lossless
  tree. Syntax errors are data, not a reason to discard the tree.
- Workspace data is stored in one namespaced SQLite store. CGO is currently
  required by `github.com/mattn/go-sqlite3`; do not assume release builds can
  use `CGO_ENABLED=0`.
- `fsnotify` through `FileScanner` is the only source of filesystem index
  events. Do not add independent watchers in feature packages.
- Request-time features must query existing indexes or the current open
  document. They must not rescan the workspace.

## Toolchain and common commands

`mise.toml` is the local toolchain source of truth. It currently pins Go,
Node.js, golangci-lint, and VSIX tooling and sets `CGO_ENABLED=1`.

```bash
# Initial setup
mise install
mise run setup

# Backend
go build ./...
go test ./...
go test ./internal/php/... -count=1
go test ./internal/twig -run '^TestName$' -count=1
go test -race ./internal/...
golangci-lint run

# VS Code extension
npm --prefix vscode-extension ci
npm --prefix vscode-extension run check-types
npm --prefix vscode-extension run compile
npm --prefix vscode-extension run test:configuration
npm --prefix vscode-extension run test:entity-designer
npm --prefix vscode-extension run test:mcp

# Full local validation
mise run check
```

Prefer targeted tests while iterating, then run validation proportional to the
change. `go test ./...`, relevant race tests, and `golangci-lint run` are the
normal backend pre-handoff checks. Run the VS Code checks when protocol shape,
configuration, commands, server startup, MCP wiring, or extension code changes.

Use `npm ci`, not `npm install`, unless intentionally changing dependencies and
the lockfile.

## Repository map

| Path | Responsibility |
|---|---|
| `main.go` | Process entry point; applies runtime policy and delegates to the CLI |
| `internal/cli` | `serve`, `check`, `index`, feature commands, profiling, and MCP transport |
| `internal/app` | Workspace construction, ownership, indexer wiring, inspections, and provider registration |
| `internal/lsp` | JSON-RPC/LSP server, document lifecycle, protocol conversion, inspection and quick-fix infrastructure |
| `internal/lsp/<feature>` | Completion, definition, hover, references, symbols, refactors, and other provider implementations |
| `internal/language` | Built-in language registry and extension-to-parser mapping |
| `internal/parser/cst` | Language-neutral lossless tree, byte ranges, kinds, and line indexes |
| `internal/parser/parsekit` | Language-neutral event parser, recovery, and CST sink |
| `internal/parser/<language>` | Native lexer, parser, syntax kinds, and query helpers for one language |
| `internal/indexer` | File scanning, coordinated mutations, SQLite repositories, cache versions, and workspace-symbol FTS |
| `internal/rewrite` | Validated immutable source edits and workspace edit plans |
| `internal/php` | PHP project model, symbols, semantic graph, types, inference, stubs, and rewrites |
| `internal/symfony`, `internal/doctrine`, etc. | Domain indexes and framework intelligence |
| `internal/shopware` | Shopware versioning and Shopware-specific domains such as DAL and entity schemas |
| `internal/projectconfig` | Root/extension configuration, validation, domains, diagnostics policy, and JSON schema |
| `vscode-extension/src` | Thin VS Code client, command UI, configuration UI, generators, and MCP registration |
| `scripts` | Release and packaging helpers |
| `third_party/phpstorm-stubs` | Pinned source metadata for generated PHP runtime stubs |

Avoid fixed provider counts in documentation or tests; provider registration
changes frequently. `internal/app/providers.go` and
`internal/app/inspections.go` are the authoritative composition roots.

## Runtime data flow

### Startup and indexing

1. `main.go` delegates to `internal/cli`.
2. The LSP `initialize` request supplies the workspace root and client options.
3. `internal/app.NewWorkspace` opens the shared store, constructs indexes and
   their dependencies, and registers enabled indexers with `FileScanner`.
4. `registerFeatures` and `registerDiagnosticInspections` connect domain
   services to protocol capabilities.
5. The scanner parses a candidate file once through `internal/language`, lets
   indexers prepare read-only data concurrently, and commits per-file changes
   through a coordinated mutation.

### Open documents

The document manager owns the newest in-memory source and CST for an open file.
Features and diagnostics must use that snapshot so unsaved edits take
precedence over disk and index state. On close, diagnostics must be cleared and
future reads may return to indexed/disk state.

### Configuration precedence

Configuration is layered in this order:

1. built-in defaults;
2. workspace `.config/shopware-lsp/config.json`;
3. nested extension `.config/shopware-lsp/config.json` files for files below
   that extension;
4. editor-local overrides supplied by the client.

Nested extension files are diagnostics-only. Their path patterns are relative
to the extension root. Structural settings such as domains, indexing, PHP
extensions, and target versions belong at workspace/editor scope and may
require a restart and cache invalidation.

## Parser work

When changing or adding a parser:

- Keep lexing, parsing, syntax kinds, and queries in their existing separate
  packages.
- Reserve a non-overlapping `cst.Kind` range with `cst.RegisterLanguage` for a
  new language.
- Preserve all input, including whitespace, comments, unknown constructs, and
  malformed fragments.
- Assert `result.Tree.Root.Text() == source` in parser tests.
- Add recovery tests for incomplete constructs likely to occur while typing.
- Use syntax/query helpers in consumers instead of duplicating token walks or
  parsing source with regular expressions.
- Add the frontend and extensions to `internal/language/builtin.go` only after
  losslessness and recovery are covered.
- Run parser benchmarks or `internal/parser/performance_validation_test.go`
  when changing hot lexer/parser paths.

Do not mutate CST nodes. Parse trees represent immutable source snapshots;
source changes are compiled through `internal/rewrite`.

## Indexing and persistence

Indexers implement `internal/indexer.Indexer`. Follow these rules:

- Parsing and expensive extraction should happen before the SQLite write
  transaction. Implement `PreparingIndexer` when preparation is substantial.
- `IndexPrepared` should persist prepared data and arrange post-commit
  publication; it should not repeat parsing or expensive inference.
- Save and remove records per source path so file changes and deletions cannot
  leave stale rows.
- Use the shared `ParsedFile.Mutation()` and namespaced repositories. Do not
  open an unrelated feature database.
- Keep publication deterministic. Sort results where repository iteration or
  maps could otherwise leak nondeterministic ordering to LSP clients.
- Protect mutable published state with the existing synchronization pattern
  and run race tests after lifecycle or concurrency changes.
- Bump `internal/indexer.IndexVersion` whenever persisted structure or
  extraction semantics makes existing caches incompatible. Update its test in
  the same change.
- Consider the cold-index, cached-start, disk-size, and request-latency cost of
  every new indexed field. Do not load an entire catalog into memory when a
  targeted SQLite lookup is sufficient.

Use `SHOPWARE_LSP_CACHE_DIR` in tests and diagnostics instead of touching a
developer's normal workspace cache.

## LSP providers

Provider interfaces live in `internal/lsp`; implementations live in the
feature subpackages. A normal new provider change should:

1. depend on domain indexes/services through its constructor;
2. return quickly for unsupported languages or contexts;
3. honor `context.Context` cancellation in non-trivial loops;
4. use `uriutil` for file URI conversion;
5. use the current `SyntaxContext` rather than reparsing when it is available;
6. return deterministic, deduplicated results with exact ranges;
7. be constructed and registered in `internal/app/providers.go`;
8. be gated by the appropriate feature and domain configuration;
9. have focused provider tests plus a server-level test when protocol routing
   or document lifecycle matters.

Do not put workspace construction, scanning, or global singletons inside a
provider.

## Diagnostics and code actions

New diagnostics should use the typed inspection system, not an independent
code-action rediscovery pass.

1. Define stable diagnostic and inspection IDs. IDs are configuration and API
   contracts; do not rename them casually.
2. Analyze a `TextDocument` and report byte-oriented `lsp.Problem` values.
3. Declare every possible problem in an `InspectionDefinition`, including its
   default severity and whether it is disabled by default.
4. Put serializable context in the problem payload.
5. Bind exact quick-fix IDs and payloads when reporting the problem.
6. Implement deterministic edits as `RewriteQuickFix` values using
   `rewrite.Builder` and `rewrite.WorkspacePlan`.
7. Use `CommandQuickFix` only when editor interaction, snippets, or another
   command-only workflow is genuinely required. MCP can apply workspace edits
   but cannot execute editor-only commands.
8. Prefer lazy fixes for non-trivial or cross-file rewrites so element handles
   can reject stale document versions.
9. Register the inspection in `internal/app/inspections.go` and map it to a
   domain in `inspectionDomain` when needed.
10. Test the diagnostic, fix presentation, generated edit, stale-state
    behavior, configuration override, and MCP/CLI path when externally useful.

Diagnostic ranges and edits must be derived from CST elements or validated byte
ranges. Never construct UTF-16 offsets by counting bytes. Code actions must not
write files directly; return a workspace edit so the editor, CLI, and MCP can
validate and apply it consistently.

## PHP analysis

- Keep parsing, binding, semantic storage, name resolution, and type inference
  as separate stages.
- Prefer the shared `php/types` representation and semantic snapshots over
  feature-specific type strings.
- Resolve aliases, inheritance, interfaces, traits, PHPDoc, generics, and
  nullability through the central resolver/inference APIs.
- Do not rebuild the semantic workspace or reverse-reference graph for each
  request. Reuse immutable snapshots and existing lazy indexes.
- Changes to compact persisted PHP graph data require persistence and restart
  tests and may require an index-version bump.
- Generated runtime stubs come from phpstorm-stubs through
  `cmd/generate_phpstorm_stubs`; do not hand-maintain a competing builtin
  catalog.

## Shopware version-aware features

Use `internal/shopware.VersionResolver` and the configured target version.
Version-aware diagnostics should clearly define their version interval, remain
safe when the installed version is unknown, and include tests on both sides of
each boundary. Prefer reusable analyzers and rewrite utilities over copying one
Rector rule verbatim into each inspection.

## Configuration changes

When adding a feature, domain, inspection, or rule, check all applicable
surfaces:

- `internal/projectconfig` defaults, catalog, validation, merging, and
  structural fingerprint;
- `internal/projectconfig/schema.json`;
- inspection-to-domain routing in `internal/lsp/configuration.go`;
- provider/inspection gating in `internal/app`;
- VS Code configuration model/UI when users must control it locally;
- root and nested configuration tests;
- README examples for user-visible settings.

Unknown settings and unknown diagnostic IDs should fail validation rather than
being silently ignored. Preserve backward compatibility when the VS Code
extension can connect to a custom older server binary.

## VS Code extension

Keep `vscode-extension/src/extension.ts` as the composition entry point. Put
commands, configuration models, executable discovery, MCP wiring, and complex
webviews in their existing focused modules.

- The extension is a client and UI layer; domain analysis belongs in Go.
- Launch one language-server and MCP definition per workspace folder.
- Pass the same editor-local diagnostic overrides to LSP and MCP processes.
- Accept both `WorkspaceEdit.changes` and `documentChanges`, including create
  operations, when handling server responses.
- Keep webview messages typed and validate untrusted message data.
- Do not edit generated `dist/`, packaged binaries, `out/`, or `node_modules/`
  as source.
- Update command contributions in `package.json` when adding a VS Code command.

## CLI and MCP

CLI positions and MCP positions are one-based; LSP protocol positions are
zero-based UTF-16. Keep conversion at the adapter boundary.

- Feature CLI commands should call the production LSP session rather than
  feature packages directly.
- Read-only commands must not mutate workspace files.
- Refactoring commands preview diffs by default and write only with explicit
  write flags.
- MCP tools must validate all paths against the workspace root.
- List code actions before applying one and require an exact action title.
- Workspace-symbol search should keep its fast direct FTS path unless
  `--fresh` is explicitly requested.

## Testing strategy

Use `testify/require` for prerequisites and fatal assertions and
`testify/assert` for independent comparisons. Prefer `t.TempDir()` and close
indexes with `t.Cleanup`:

```go
func TestFeature(t *testing.T) {
    index, err := NewIndex(filepath.Join(t.TempDir(), "cache"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, index.Close()) })
}
```

Add tests at the lowest useful layer, then add an integration test only for a
boundary the unit test cannot cover:

- parser test for syntax and recovery;
- index test for extraction, update, removal, and persistence;
- provider/analyzer test for domain behavior;
- server test for routing, positions, diagnostics, lifecycle, or lazy fixes;
- CLI/MCP/VS Code test for adapter behavior.

Tests should not require network access. External checkouts are read-only
fixtures and must never be modified by tests.

Real-world tests use the `integration` build tag and default to
`~/Developer/sw-trunk`; override it with `SHOPWARE_LSP_REAL_WORLD_ROOT`:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  go test -tags=integration ./internal/app \
  -run '^TestShopwareTrunkIndexing$' -count=1 -v
```

Real-world fixtures move independently. If an unrelated assertion fails,
confirm it on the baseline revision before changing production code or
weakening the assertion.

For a cold/warm indexing resource profile:

```bash
go test -c -tags=integration ./internal/app \
  -o /tmp/shopware-lsp-index-profile.test
/usr/bin/time -l /tmp/shopware-lsp-index-profile.test \
  -test.run '^TestShopwareTrunkIndexingProfile$' -test.count=1 -test.v
```

Use isolated cache directories and reverse run order when comparing revisions;
filesystem cache warmth and RSS vary enough that one sample is not evidence of
a regression.

## Code quality and change discipline

- Run `gofmt` on changed Go files and keep `golangci-lint` clean.
- Wrap errors with useful operation context and `%w`; keep reusable error text
  lowercase.
- Avoid panics in request/index paths. Return errors or tolerant empty results
  where editor-time degradation is preferable.
- Keep hot loops allocation-aware, but prefer clear measured improvements over
  speculative complexity.
- Preserve existing uncommitted work. Inspect `git status` and relevant diffs
  before editing; never discard unrelated changes.
- Do not modify `vendor`, `node_modules`, generated release outputs, or a
  user's external Shopware/plugin checkout unless explicitly requested.
- Update documentation when behavior, commands, configuration, or user-facing
  defaults change.
- Use Conventional Commits when asked to commit, for example
  `feat(twig): add block version diagnostics` or
  `fix(indexer): clear stale file records`.
- Do not commit or push unless explicitly requested.

## Definition of done

Before handing off a change, confirm that:

- the implementation follows the shared parser/index/provider architecture;
- unsaved documents, changed files, removed files, and cache restarts are
  handled where relevant;
- diagnostics and actions respect configuration and domain toggles;
- user-visible edits are represented as validated workspace edits;
- focused regression tests pass;
- full Go, race, lint, and VS Code checks were run as appropriate;
- index or request-time performance was measured for potentially expensive
  changes;
- any skipped check or pre-existing fixture failure is reported precisely.
