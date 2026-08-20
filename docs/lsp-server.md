# LSP Server, Documents, and Providers

This document explains the protocol layer: how a JSON-RPC request becomes a
response, how open documents are managed, how feature providers are declared
and registered, and how capabilities and gating work.

Everything here lives in [`internal/lsp`](../internal/lsp) and its feature
subpackages, wired together by [`internal/app`](../internal/app). For the
system-wide picture see [`architecture.md`](architecture.md).

## Table of contents

- [Transport and dispatch](#transport-and-dispatch)
- [Request lifecycle](#request-lifecycle)
- [The document manager](#the-document-manager)
- [SyntaxContext](#syntaxcontext)
- [Context enrichment](#context-enrichment)
- [The provider pattern](#the-provider-pattern)
- [Writing a provider](#writing-a-provider)
- [Registration](#registration)
- [Capabilities](#capabilities)
- [Gating: features, domains, client profiles](#gating-features-domains-client-profiles)
- [Custom commands and methods](#custom-commands-and-methods)
- [Shutdown](#shutdown)
- [The CLI and MCP frontends](#the-cli-and-mcp-frontends)
- [Testing](#testing)

## Transport and dispatch

`Server.Start(in, out)` builds a buffered JSON-RPC stream with the VS Code
object codec and connects it to a handler:

```go
// internal/lsp/server.go
stream := jsonrpc2.NewBufferedStream(rwc{in, out}, jsonrpc2.VSCodeObjectCodec{})
ordered := jsonrpc2.HandlerWithError(s.handle)
conn := jsonrpc2.NewConn(context.Background(), stream, &cliDiagnosticHandler{
    server: s, ordered: ordered,
})
s.setConnection(conn)
<-conn.DisconnectNotify()
return s.CloseAll()
```

**Requests are handled sequentially.** `jsonrpc2.HandlerWithError` processes one
request at a time, in arrival order. This is a deliberate simplification — it
removes a large class of ordering bugs around document versions — but it has a
direct consequence for anyone writing a provider: **a slow provider blocks the
next request.** Providers must return fast and must honor `ctx` cancellation.

The single exception is `cliDiagnosticHandler`, which moves
`textDocument/diagnostic` to a background goroutine **in CLI mode only**, so
`shopware-lsp check` can analyze several files concurrently. Ordinary LSP
ordering is untouched: each `didOpen` is still handled synchronously before its
diagnostic request, and the CLI sends `didClose` only after that request
completes.

### The dispatch chain

`Server.handle` resolves a method in a fixed order:

```go
func (s *Server) handle(ctx, conn, req) (interface{}, error) {
    s.setConnection(conn)

    if req.Method == "exit" { /* close the connection */ }

    // 1. feature gate
    if feature := featureForMethod(req.Method); feature != "" && !s.featureEnabled(feature) {
        return disabledFeatureResult(req.Method), nil
    }
    // 2. custom commands (shopware/* methods contributed by providers)
    if command, ok := s.commandMap[req.Method]; ok { /* ... */ }
    // 3. protocol methods
    if handler, ok := s.methodHandlers[req.Method]; ok {
        return handler(ctx, req)
    }
    // 4. notifications are silently ignored; requests get MethodNotFound
    if req.ID == (jsonrpc2.ID{}) { return nil, nil }
    return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, ...}
}
```

The gate returning a *disabled result* rather than an error matters: a client
that asks for a disabled feature gets a well-formed empty answer instead of an
error popup.

`protocolMethodHandlers()` is one map literal in `server_handlers.go` — the
authoritative list of what the server speaks. Two generic adapters remove the
per-method boilerplate:

```go
func rpcValueHandler[Params, Result any](handle func(context.Context, *Params) Result) rpcMethodHandler
func rpcResultHandler[Params, Result any](handle func(context.Context, *Params) (Result, error)) rpcMethodHandler
```

`rpcValueHandler` for handlers that cannot fail, `rpcResultHandler` for those
that can. Both decode the params into a typed struct, so a handler never touches
`json.RawMessage`.

## Request lifecycle

Every position-based request follows the same four steps. `textDocument/definition`
in full:

```go
// internal/lsp/definition.go
func (s *Server) definition(ctx context.Context, params *protocol.DefinitionParams) []protocol.Location {
    // 1. resolve the document and the CST position context
    syntax, _ := s.documentManager.SyntaxContext(
        params.TextDocument.URI, params.Position.Line, params.Position.Character,
    )
    request := &DefinitionRequest{DefinitionParams: params, SyntaxContext: syntax}

    // 2. add language-specific semantic context
    ctx = s.enrichContext(ctx, syntax)

    // 3. fan out to every registered provider
    var locations []protocol.Location
    for _, provider := range s.definitionProviders {
        locations = append(locations, provider.GetDefinition(ctx, request)...)
    }

    // 4. return the concatenation
    return locations
}
```

Two properties of the fan-out are worth internalizing:

- **All providers run, always.** There is no routing table from language to
  provider and no early exit. Each provider decides for itself whether the
  request is its business. That is why step one of every provider is a cheap
  rejection.
- **Results are concatenated, not merged.** The server does not deduplicate
  definition or reference locations. Each provider is responsible for returning
  deterministic, deduplicated, exactly-ranged results for its own domain, and
  for not answering a context that belongs to another provider. Diagnostics are
  the exception — `normalizeDiagnostics` sorts and deduplicates there, because
  overlapping inspections are expected.

## The document manager

```go
// internal/lsp/document.go
type TextDocument struct {
    URI            string
    Text           []byte
    Source         string
    Version        int
    SyntaxTree     *cst.Tree
    SyntaxLanguage language.ID
    ParseErrors    []parsekit.Error
    LineIndex      *cst.LineIndex

    analysisCache *documentAnalysisCache
}
```

`NewTextDocumentWithRegistry` builds the line index and parses through the
language registry:

```go
if id, result, ok := registry.ParsePath(uri, source); ok {
    doc.SyntaxLanguage = id
    doc.SyntaxTree = result.Tree
    doc.ParseErrors = result.Errors
}
```

An unregistered extension leaves `SyntaxTree` nil and `SyntaxLanguage` empty.
**Every consumer must handle that** — providers routinely start with a nil-tree
guard.

### Immutability

A `TextDocument` is an immutable snapshot. `OpenDocument` and `UpdateDocument`
both construct a **new** document and publish it; nothing is mutated in place.
Three things depend on this:

- Diagnostics cache freshness by pointer identity (`cached.document == document`).
- Providers can hold a `*TextDocument` for the duration of a request without
  locking.
- `MemoizedAnalysis` can cache derived analysis safely.

Document sync is **full text** (`textDocumentSync.change: 1`). Each
`didChange` carries the whole document and triggers a full reparse. Given the
parser's throughput this is cheaper than maintaining incremental reparse
machinery, and it removes an entire class of stale-tree bugs.

### MemoizedAnalysis

```go
func (d *TextDocument) MemoizedAnalysis(owner any, revision uint64, compute func() any) any
```

One derived analysis per (document snapshot, workspace revision). A newer
workspace revision replaces the previous value, so open documents do not retain
stale semantic generations forever. `owner` must be comparable — use a stable
service pointer so independent LSP features share the same computed value rather
than each paying for it.

### Observers

```go
type DocumentObserver struct {
    DidOpenOrChange func(*TextDocument)
    DidClose        func(string)
}

func (m *DocumentManager) RegisterObserver(observer DocumentObserver)
```

This is the narrow boundary through which a domain index can see unsaved editor
state. Registration **replays** every already-open document, so workspace
initialization order is irrelevant. Delivery is synchronous, before diagnostics
and interactive requests are scheduled. See
[`indexing.md`](indexing.md#open-document-overlays) for the rules.

## SyntaxContext

```go
// internal/lsp/request.go
type SyntaxContext struct {
    Document        *TextDocument
    Language        language.ID
    DocumentContent []byte
    DocumentTree    *cst.Tree
    LineIndex       *cst.LineIndex
    Root            *cst.Node
    Token           *cst.Token   // the token under the cursor
    Node            *cst.Node    // the smallest node containing the cursor
}
```

Every request type embeds it:

```go
type CompletionRequest struct {
    *protocol.CompletionParams
    SyntaxContext
}
```

So a provider reads `request.Node`, `request.Token`, `request.Document` directly
and never converts positions itself.

`syntaxAtPosition` builds `Token` and `Node` from
`LineIndex.OffsetUTF16(line, character)` plus `Root.TokenAtOffset` /
`Root.NodeAtOffset`, with two adjustments that matter enormously for editor
feel:

```go
// At EOF there is no token to the right of the cursor. Retry one byte left so
// incomplete trailing input still has useful context.
if token == nil && offset > tree.Root.Range().Start {
    token = tree.Root.TokenAtOffset(offset - 1)
    node = tree.Root.NodeAtOffset(offset - 1)
}
// On trivia, walk left to the previous non-trivia token.
for token != nil && token.Kind().IsTrivia() && token.Range().Start > tree.Root.Range().Start {
    previousOffset := token.Range().Start - 1
    previousToken := tree.Root.TokenAtOffset(previousOffset)
    if previousToken == nil || previousToken == token { break }
    token = previousToken
    node = tree.Root.NodeAtOffset(previousOffset)
}
```

Without these, a cursor immediately after `trans('` or at end of file would
resolve to whitespace or nothing, and completion would silently do nothing —
which is the single most common symptom of building position logic by hand
instead of using `SyntaxContext`.

## Context enrichment

```go
type ContextEnricher func(context.Context, SyntaxContext) context.Context

func (s *Server) RegisterContextEnricher(languageID language.ID, enricher ContextEnricher)
```

One enricher per language, applied before provider fan-out. It exists so
language-specific semantic state can ride along in the `context.Context` without
the protocol server importing a domain package. PHP uses it to attach the
semantic document for the file under the cursor:

```go
// internal/app/providers.go
server.RegisterContextEnricher(language.PHP, func(ctx context.Context, syntax lsp.SyntaxContext) context.Context {
    path, version := "", 0
    if syntax.Document != nil {
        path, _ = uriutil.Path(syntax.Document.URI)
        version = syntax.Document.Version
    }
    return services.php.AddDocumentContext(ctx, path, version, syntax.Node, syntax.Root)
})
```

Every PHP provider in the same request then reads that one semantic document
instead of each rebuilding it.

## The provider pattern

Provider interfaces are declared in `internal/lsp/types.go`; implementations
live in `internal/lsp/<feature>`. Each is a small interface returning protocol
types:

```go
type CompletionProvider interface {
    GetCompletions(ctx context.Context, request *CompletionRequest) []protocol.CompletionItem
    GetTriggerCharacters() []string
}

type HoverProvider interface {
    GetHover(ctx context.Context, request *HoverRequest) (*protocol.Hover, error)
}

type CodeLensProvider interface {
    GetCodeLenses(ctx context.Context, request *CodeLensRequest) ([]protocol.CodeLens, error)
    ResolveCodeLens(ctx context.Context, codeLens *protocol.CodeLens) (*protocol.CodeLens, error)
}
```

The full set, with its registration method:

| Interface | Register with | Serves |
| --- | --- | --- |
| `CompletionProvider` | `RegisterCompletionProvider` | `textDocument/completion` |
| `GotoDefinitionProvider` | `RegisterDefinitionProvider` | `textDocument/definition` |
| `ImplementationProvider` | `RegisterImplementationProvider` | `textDocument/implementation` |
| `TypeHierarchyProvider` | `RegisterTypeHierarchyProvider` | `prepareTypeHierarchy`, `supertypes`, `subtypes` |
| `CallHierarchyProvider` | `RegisterCallHierarchyProvider` | `prepareCallHierarchy`, `incomingCalls`, `outgoingCalls` |
| `ReferencesProvider` | `RegisterReferencesProvider` | `textDocument/references` |
| `HoverProvider` | `RegisterHoverProvider` | `textDocument/hover` |
| `SignatureHelpProvider` | `RegisterSignatureHelpProvider` | `textDocument/signatureHelp` |
| `RenameProvider` | `RegisterRenameProvider` | `textDocument/rename` |
| `CodeLensProvider` | `RegisterCodeLensProvider` | `textDocument/codeLens`, `codeLens/resolve` |
| `ActionProvider` | `RegisterActionProvider` | `textDocument/codeAction` (non-inspection actions) |
| `InlayHintProvider` | `RegisterInlayHintProvider` | `textDocument/inlayHint` |
| `DocumentLinkProvider` | `RegisterDocumentLinkProvider` | `textDocument/documentLink` |
| `DocumentSymbolProvider` | `RegisterDocumentSymbolProvider` | `textDocument/documentSymbol` |
| `DocumentHighlightProvider` | `RegisterDocumentHighlightProvider` | `textDocument/documentHighlight` |
| `LinkedEditingRangeProvider` | `RegisterLinkedEditingRangeProvider` | `textDocument/linkedEditingRange` |
| `FoldingRangeProvider` | `RegisterFoldingRangeProvider` | `textDocument/foldingRange` |
| `SelectionRangeProvider` | `RegisterSelectionRangeProvider` | `textDocument/selectionRange` |
| `DocumentColorProvider` | `RegisterDocumentColorProvider` | `documentColor`, `colorPresentation` |
| `SemanticTokensProvider` | `RegisterSemanticTokensProvider` | `textDocument/semanticTokens/full` |
| `WorkspaceSymbolProvider` | `RegisterWorkspaceSymbolProvider` | `workspace/symbol` |
| `FileRenameProvider` | `RegisterFileRenameProvider` | `workspace/willRenameFiles` |
| `CommandProvider` | `RegisterCommandProvider` | custom methods and `workspace/executeCommand` |
| `Inspection` | `RegisterInspection` | diagnostics and quick fixes — see [diagnostics](diagnostics-pipeline.md) |

Do not rely on a fixed provider count anywhere; registration changes often.
`internal/app/providers*.go` and `internal/app/inspections.go` are the
authoritative lists.

`SemanticTokensProvider` is slightly different from the rest: it returns
byte-oriented `lsp.SemanticToken` values, and the server encodes the LSP delta
format:

```go
type SemanticToken struct {
    Range     cst.TextRange
    Type      uint32   // index into protocol.SemanticTokenTypes
    Modifiers uint32   // bitset
}
```

Providers therefore never compute delta-encoded UTF-16 triples by hand.

## Writing a provider

A complete provider — feature-flag completion across four languages:

```go
// internal/lsp/completion/feature_completion.go
type FeatureCompletionProvider struct {
    featureIndex *feature.FeatureIndexer
}

func NewFeatureCompletionProvider(featureIndexer *feature.FeatureIndexer) *FeatureCompletionProvider {
    return &FeatureCompletionProvider{featureIndex: featureIndexer}
}

func (p *FeatureCompletionProvider) GetCompletions(
    ctx context.Context, params *lsp.CompletionRequest,
) []protocol.CompletionItem {
    matches := false
    switch filepath.Ext(params.TextDocument.URI) {
    case ".twig":
        matches = params.Node != nil && twigquery.StringInFunction(params.Node, "feature")
    case ".scss":
        matches = params.Node != nil && scssquery.StringInFunction(params.Node, "feature")
    case ".php":
        matches = params.Node != nil && phpquery.StringInCall(params.Node, 0, "Feature::isActive")
    case ".js", ".ts":
        matches = params.Node != nil && jsquery.StringInCall(
            params.Node, 0, "Feature.isActive", "Shopware.Feature.isActive",
        )
    }
    if !matches {
        return nil
    }
    features, _ := p.featureIndex.GetAllFeatures()
    items := make([]protocol.CompletionItem, 0, len(features))
    for _, feature := range features {
        items = append(items, protocol.CompletionItem{
            Label: feature.Name, Kind: int(protocol.FunctionCompletion),
        })
    }
    return items
}

func (p *FeatureCompletionProvider) GetTriggerCharacters() []string { return nil }
```

The checklist, in the order it appears above:

1. **Take dependencies through the constructor.** A provider never constructs a
   workspace, opens a database, or reaches for a global.
2. **Reject unsupported contexts immediately.** Extension first, then a query
   predicate. Remember every provider runs for every request.
3. **Use `SyntaxContext`.** `params.Node` is already resolved; never reparse and
   never compute offsets from line/character yourself.
4. **Ask the language's `query` package**, not the tree directly. `StringInCall`
   encodes the argument-position semantics once, for everyone.
5. **Answer from an index or the current document.** Never scan the workspace.
6. **Honor `ctx` cancellation** in any non-trivial loop.
7. **Return deterministic, deduplicated results with exact ranges.** Sort
   anything derived from a map or repository iteration.
8. **Use `uriutil`** for URI ↔ path conversion — not string concatenation.
9. **Return `nil`, not an empty-but-present result,** when the provider does not
   apply.

## Registration

`internal/app/providers.go` is the adapter layer from domain repositories to LSP
capabilities. Construction stays in `workspace.go`; protocol wiring stays here.

```go
func registerFeatures(server *lsp.Server, root string, services workspaceServices) {
    if server.DomainEnabled("administration") {
        registerAdministrationDocumentObserver(server, services.admin)
    }
    server.RegisterContextEnricher(language.PHP, /* ... */)
    phpFeatures := phpsemantic.New(services.php)

    registerSymbolAndDocumentProviders(server, services)
    registerCompletionProviders(server, root, phpFeatures, services)
    registerDefinitionProviders(server, root, phpFeatures, services)
    registerCodeLensProviders(server, root, services)
    registerReferenceProviders(server, phpFeatures, services)
    registerDiagnosticInspections(server, root, phpFeatures, services)
    versioning := registerHoverProviders(server, root, phpFeatures, services)
    registerEditorProviders(server, phpFeatures, services)
    registerActionAndCommandProviders(server, root, versioning, services)
}
```

`workspaceServices` is a plain struct of every constructed domain index. It is
passed by value to each registration function, which keeps the signatures short
and makes it obvious which services a capability group actually uses.

Registration order determines provider order, which determines result order
within a response. If a change makes results reorder, this is where to look.

Gate registration by domain when the provider belongs to an optional subsystem:

```go
if server.DomainEnabled("administration") { /* register admin providers */ }
```

A domain that is off may not even have a constructed index — the corresponding
field in `workspaceServices` will be nil.

## Capabilities

`serverCapabilities()` is computed, not declared. Each entry is advertised only
if something was registered **and** the corresponding feature is enabled:

```go
setProviderCapability(capabilities, "hoverProvider",
    len(s.hoverProviders), s.featureEnabled("hover"))
```

So a build with no hover providers, or a workspace with hover disabled,
truthfully reports no hover support and the client stops asking. Completion
trigger characters are collected from the registered providers; code-action
kinds from the registered actions and inspections; the semantic-tokens legend
from `protocol.SemanticTokenTypes`.

`codeActionProvider.resolveProvider` mirrors what the client advertised:
lazy quick fixes only work if the client supports `codeAction/resolve` with
`edit` in `resolveSupport.properties`. When it does not, the server computes
edits eagerly instead.

An unsupported project root returns `inactiveProjectInitializeResult()` — a
minimal capability set — rather than refusing to start. The editor stays
functional, the server simply does nothing.

## Gating: features, domains, client profiles

Four orthogonal gates. Knowing which one applies saves a lot of confusion.

**1. Features** (`featureForMethod` → `featureEnabled`) gate whole LSP methods.
Configured under `features` in project configuration. A disabled feature is not
advertised and its method returns a disabled result.

**2. Domains** (`DomainEnabled`) gate subsystems — `php`, `symfony.routes`,
`shopware.snippets`, `administration`, `scss`, … A disabled domain skips index
construction, scanner registration, provider registration, and inspection
dispatch. Mapping lives in `domainForIndexer` (indexers) and
`inspectionDomain` (inspections).

**3. Diagnostic rules** set per-code severity or `off`. See
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#configuration-and-gating).

**4. Client presentation profile** shapes what a host IDE is shown:

```go
const (
    PresentationProfileFull      PresentationProfile = "full"
    PresentationProfileFramework PresentationProfile = "framework"
)
```

A client identifies itself through `initializationOptions.shopwareClient` with a
protocol version, a profile, and the list of commands it can execute. The
protocol version must match `integration.ProtocolVersion` exactly — a mismatch
fails `initialize` rather than degrading silently.

Under the `framework` profile the server withholds generic PHP presentation
(`php.semantic`, `symfony.embedded_language` inspections) and leaves it to the
host IDE, keeping only Shopware and Symfony intelligence. This is what the
PhpStorm integration uses; see
[`phpstorm-integration.md`](phpstorm-integration.md).

`supportedCommands` drives command filtering. A code action or code lens whose
command the client cannot execute is either stripped of its command (when it
also carries an edit) or dropped entirely:

```go
// internal/lsp/client_command_filter.go
if action.Command == nil || s.supportsClientCommand(action.Command.Command) {
    result = append(result, action)
    continue
}
if action.Edit != nil {
    action.Command = nil            // keep the usable edit
    result = append(result, action)
}
```

This is why quick fixes should prefer `RewriteQuickFix` over `CommandQuickFix`:
an edit works everywhere, a command only in clients that implement it. MCP, in
particular, can apply workspace edits but cannot execute editor-only commands.

## Custom commands and methods

Beyond standard LSP the server exposes Shopware-specific methods:

| Method | Purpose |
| --- | --- |
| `shopware/configuration/catalog` | Full configuration catalog with scopes and issues |
| `shopware/configuration/effective` | The resolved effective configuration |
| `shopware/configuration/reload` | Re-read configuration from disk |
| `shopware/forceReindex` | Clear hashes and reindex |
| `shopware/index/stats` | Indexing statistics |
| `shopware/commands` | Enumerate available custom commands |

Plus notifications the server sends: `shopware/indexingCompleted`,
`shopware/indexingFailed`.

A `CommandProvider` contributes named handlers:

```go
type CommandFunc func(ctx context.Context, args *json.RawMessage) (interface{}, error)

type CommandProvider interface {
    GetCommands(ctx context.Context) map[string]CommandFunc
}
```

`registerCommands()` flattens every provider's map into `commandMap` at
`Start`. Entries are reachable both as direct JSON-RPC methods and through
`workspace/executeCommand`, and are gated by the `commands` feature (except
configuration methods, which stay available so a client can always inspect and
fix its configuration).

See [`LSP.md`](../LSP.md) for the custom-method contract and a Neovim example.

## Shutdown

```
"shutdown"  → handleShutdown
"exit"      → close the connection
DisconnectNotify → Server.CloseAll()
```

`CloseAll` cancels the lifecycle context, marks the server closing (so
`startBackground` refuses new work), waits on the lifecycle `WaitGroup`, then
closes the workspace — which closes the scanner, then every indexer in reverse
construction order, then the store. `Close` on the store runs `PRAGMA optimize`,
`incremental_vacuum`, and a truncating WAL checkpoint.

A cancelled background job must not publish. Diagnostics check `ctx.Err()`
before notifying and hold `diagnosticsPublishMu` across the version re-check and
the notify, so a superseded analysis cannot overwrite a newer result or write
after a close.

## The CLI and MCP frontends

`internal/cli` builds a real in-process LSP session (`cliSession`) over a
`net.Pipe` pair: the same `app.Application`, the same server, the same
providers. Feature commands are LSP requests, and their output is the protocol
response rendered as text or JSON.

Consequences that are easy to get wrong:

- **CLI and MCP positions are one-based `file:line:column`.** LSP positions are
  zero-based UTF-16. Convert at the adapter boundary, nowhere else.
- **Read-only commands must not mutate workspace files.** `codeaction` and
  `rename` preview a unified diff and write only with an explicit `-w`.
- **MCP tools must validate every path against the workspace root.**
- **`workspace-symbol` keeps its fast direct FTS path** unless `--fresh` is
  requested; do not make it restore semantic graphs by default.
- **CLI mode changes behavior in a few places** (`initializationOptions.CLIMode`):
  no watcher, no debounced diagnostics on open/change, no Administration
  overlay per checked file, and concurrent pull diagnostics.

Roughly 30 feature commands exist; `shopware-lsp help` and
`shopware-lsp api-json` enumerate them authoritatively. See
[`reference.md#command-line-interface`](reference.md#command-line-interface).

## Testing

### Provider tests

Construct the domain index and the provider directly, build a `TextDocument`,
and assert on protocol values:

```go
func TestFeatureCompletion(t *testing.T) {
    index, err := feature.NewFeatureIndexer(t.TempDir())
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, index.Close()) })
    require.NoError(t, index.Index(indexer.NewParsedFile("/p/feature.yaml", []byte(yamlSource))))

    document := lsp.NewTextDocument("file:///p/template.twig", twigSource, 1)
    context, ok := /* build a SyntaxContext at the cursor */
    require.True(t, ok)

    items := provider.GetCompletions(context.Background(), request)
    require.Len(t, items, 1)
}
```

Cover the negative cases too — a provider that answers in a context it should
not is a worse bug than one that answers nothing, because it pollutes every
other provider's results.

### Server-level tests

Add one when protocol routing, document lifecycle, UTF-16 positions, capability
computation, or lazy fix resolution is what you changed. `internal/lsp` has
tests for diagnostics scheduling, progress reporting, execute-command routing,
and workspace lifecycle; `internal/app` has document-lifecycle, client-profile,
and request-latency suites.

Always exercise a real `TextDocument` when positions matter, so UTF-16
conversion is actually covered. Hand-built ranges silently skip the most
error-prone part of the stack.

### Running

```bash
go test ./internal/lsp/...
go test ./internal/app -run TestProviders
go test -race ./internal/...
```

## Further reading

- [`architecture.md`](architecture.md) — the system this layer sits in
- [`parser-architecture.md`](parser-architecture.md) — `SyntaxContext`, trees,
  and the query packages providers depend on
- [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — the inspection system,
  which replaces the provider pattern for diagnostics
- [`refactoring-engine.md`](refactoring-engine.md) — how providers return edits
- [`indexing.md`](indexing.md) — where provider data comes from
- [`LSP.md`](../LSP.md) — custom methods and a Neovim configuration example
- [`phpstorm-integration.md`](phpstorm-integration.md) — the versioned client
  contract and the framework presentation profile
