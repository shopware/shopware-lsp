# LSP Server, Documents, and Providers

This document explains the protocol layer: how a JSON-RPC request becomes a
response, how open documents are managed, how feature providers are declared
and registered, and how capabilities and gating work.

Everything here lives in [`crates/lsp`](../crates/lsp) (`swlsp-lsp`) and the
`crates/ide-<domain>` domain crates, wired together by
[`crates/app`](../crates/app). For the
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

`Server::start(input, output)` registers the command providers' methods, then
serves JSON-RPC over `tower-lsp-server`'s message framing until the client
disconnects or sends `exit`, and finally closes the server:

```rust
// crates/lsp/src/server.rs
pub fn start(&self, input: impl Read + Send + 'static, output: impl Write + Send + 'static)
    -> Result<(), String>
{
    self.register_commands();
    let handler: Arc<dyn Handler> = Arc::new(self.clone());
    let server = self.clone();
    transport::serve(input, output, handler, move |conn| {
        *write(&server.connection) = Some(conn.clone());
    });
    *write(&self.connection) = None;
    self.close_all()
}
```

**Normal requests and document notifications execute in order.**
`tower-lsp-server` reads and writes the stream on a two-thread Tokio runtime
and calls a raw `tower::Service` (`crates/lsp/src/transport.rs`) once per
message, in arrival order. The service enqueues each message synchronously
onto a crossbeam queue drained by the single handler worker, and answers with
a future that resolves when the worker has run the request. It handles
`$/cancelRequest` directly, so a long-running provider can observe
cancellation of its `Context` while later document notifications remain
ordered. Cancelled requests return LSP error `-32800`; the end of input
cancels the dispatcher before workspace teardown, and the worker still
answers requests that were queued. Analysis code never sees the runtime:
handlers stay synchronous, and blocking stdio, socket, and pipe streams are
bridged to Tokio by `crates/lsp/src/async_io.rs`.

CLI pull diagnostics may execute in lifecycle-managed background jobs. Their
request contexts remain registered until completion, and shutdown cancels them.

### The dispatch chain

`Server::handle` resolves a method in a fixed order:

```rust
// crates/lsp/src/server.rs (handle_method, abridged)
if method == "exit" { /* close the connection */ }

// 1. feature gate
if let Some(feature) = feature_for_method(method) && !self.feature_enabled(feature) {
    return Ok(disabled_feature_result(method));
}
// 2. custom commands (shopware/* methods contributed by providers)
if let Some(command) = registry.commands.get(method) { /* ... */ }
// 3. protocol methods
match self.protocol_method(ctx, method, params) {
    Some(result) => result,
    // 4. notifications are silently ignored; requests get MethodNotFound
    None if notification => Ok(Value::Null),
    None => Err(RpcError::new(codes::METHOD_NOT_FOUND, format!("Method not implemented: {method}"))),
}
```

A request whose `Context` was cancelled while it ran returns the cancellation
error.

The gate returning a *disabled result* rather than an error matters: a client
that asks for a disabled feature gets a well-formed empty answer instead of an
error popup.

`Server::protocol_method` is one `match` over the method name — the
authoritative list of what the server speaks. Two generic adapters remove the
per-method boilerplate:

```rust
fn value_handler<P: DeserializeOwned, R: Serialize>(params: Value, handle: impl FnOnce(P) -> R)
    -> Result<Value, RpcError>
fn result_handler<P: DeserializeOwned, R: Serialize>(params: Value, handle: impl FnOnce(P) -> Result<R, BoxError>)
    -> Result<Value, RpcError>
```

`value_handler` for handlers that cannot fail, `result_handler` for those that
can. Both decode the params into a typed struct, so a handler never touches raw
JSON.

## Request lifecycle

Every position-based request follows the same four steps. `textDocument/definition`
in full:

```rust
// crates/lsp/src/features.rs (tracing omitted)
pub(crate) fn definition(&self, ctx: &Context, params: protocol::DefinitionParams)
    -> Vec<protocol::Location>
{
    // 1. resolve the document and the CST position context
    let syntax = self.syntax(&params.text_document.uri, params.position).unwrap_or_default();
    // 2. add language-specific semantic context
    let registry = self.registry();
    let ctx = self.enrich_context(&registry, ctx, &syntax);
    let request = DefinitionRequest { params, syntax };
    // 3. fan out to every registered provider
    let mut locations = Vec::new();
    for provider in &registry.definition {
        locations.extend(provider.get_definition(&ctx, &request));
    }
    // 4. return the concatenation
    locations
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
  the exception — `normalize_diagnostics` sorts and deduplicates there, because
  overlapping inspections are expected.

## The document manager

```rust
// crates/lsp/src/document.rs
pub struct TextDocument {
    pub uri: String,
    pub version: i32,
    pub language: Option<LanguageId>, // None for files without a registered parser
    source: Arc<str>,
    tree: Option<Tree>,
    parse_errors: Vec<ParseError>,
    line_index: Arc<LineIndex>,
    analysis: Mutex<HashMap<AnalysisKey, AnalysisValue>>,
}
```

`TextDocument::new(uri, source, version)` parses through the language registry
(by the URI's extension) and builds the line index over the same shared
source. Accessors: `source()`, `tree()`, `root()`, `parse_errors()`,
`line_index()`.

An unregistered extension leaves `tree()` and `language` as `None`. **Every
consumer must handle that** — providers routinely start with a `let Some(root)
= document.root() else { … }` guard.

### Immutability

A `TextDocument` is an immutable snapshot. Opening and changing a document both
construct a **new** document and publish it as an `Arc<TextDocument>`; nothing
is mutated in place. Three things depend on this:

- Diagnostics cache freshness by pointer identity (`Arc::ptr_eq`).
- Providers can hold an `Arc<TextDocument>` for the duration of a request
  without locking.
- `memoized_analysis` can cache derived analysis safely.

Document sync is **full text** (`textDocumentSync.change: 1`). Each
`didChange` carries the whole document and triggers a full reparse. Given the
parser's throughput this is cheaper than maintaining incremental reparse
machinery, and it removes an entire class of stale-tree bugs.

### memoized_analysis

```rust
pub fn memoized_analysis<T: Any + Send + Sync>(
    &self,
    owner: usize,
    revision: u64,
    compute: impl FnOnce() -> T,
) -> Arc<T>
```

One derived analysis per (document snapshot, workspace revision, owner, value
type). A newer workspace revision replaces the previous value, so open
documents do not retain stale semantic generations forever. Use a stable
service address as `owner` (`Arc::as_ptr(&service) as usize`) so independent
LSP features share the same computed value rather than each paying for it.

### Observers

```rust
pub struct DocumentObserver {
    pub did_open_or_change: Option<Arc<dyn Fn(&Arc<TextDocument>) + Send + Sync>>,
    pub did_close: Option<Arc<dyn Fn(&str) + Send + Sync>>,
}

server.register_document_observer(observer);
```

This is the narrow boundary through which a domain index can see unsaved editor
state. Registration **replays** every already-open document, so workspace
initialization order is irrelevant. Delivery is synchronous, before diagnostics
and interactive requests are scheduled. See
[`indexing.md`](indexing.md#open-document-overlays) for the rules.

## SyntaxContext

```rust
// crates/lsp/src/document.rs
pub struct SyntaxContext {
    pub document: Option<Arc<TextDocument>>,
    pub language: Option<LanguageId>,
    pub root: Option<SyntaxNode>,
    pub token: Option<SyntaxToken>, // the token under the cursor
    pub node: Option<SyntaxNode>,   // the smallest node containing the cursor
}
```

Every position-based request type carries it and dereferences to it
(`crates/lsp/src/providers.rs`):

```rust
pub struct CompletionRequest {
    pub params: protocol::CompletionParams,
    pub syntax: SyntaxContext,
}
impl Deref for CompletionRequest { type Target = SyntaxContext; /* ... */ }
```

So a provider reads `request.node`, `request.token`, `request.document`
directly and never converts positions itself. Document-wide requests
(folding, symbols, links, semantic tokens, code lenses, …) carry the
`Arc<TextDocument>` instead.

`syntax_at_position` builds `token` and `node` from
`line_index().offset_utf16(line, character)` plus `root.token_at` /
`root.node_at`, with two adjustments that matter enormously for editor feel:

```rust
// At EOF there is no token on the right side of the cursor. Use the final
// token so incomplete trailing input still has useful context.
if token.is_none() && offset > root_start {
    let previous = offset - TextSize::from(1);
    token = root.token_at(previous);
    node = root.node_at(previous);
}
// On trivia, walk left to the previous non-trivia token.
while let Some(current) = token.clone() {
    if !current.kind().is_trivia() || current.text_range().start() <= root_start {
        break;
    }
    let previous_offset = current.text_range().start() - TextSize::from(1);
    let Some(previous) = root.token_at(previous_offset) else { break };
    if previous == current { break; }
    token = Some(previous);
    node = root.node_at(previous_offset);
}
```

Without these, a cursor immediately after `trans('` or at end of file would
resolve to whitespace or nothing, and completion would silently do nothing —
which is the single most common symptom of building position logic by hand
instead of using `SyntaxContext`.

## Context enrichment

```rust
pub type ContextEnricher = Arc<dyn Fn(&Context, &SyntaxContext) -> Context + Send + Sync>;

server.register_context_enricher(language, enricher);
```

One enricher per language, applied before provider fan-out. It exists so
language-specific semantic state can ride along in the request `Context`
(`Context::with_value`) without the protocol server depending on a domain
crate. PHP uses it to attach the analysis for the file under the cursor:

```rust
// crates/ide-php/src/context.rs
pub fn enrich_php_context(ctx: &Context, index: &PhpIndex, syntax: &SyntaxContext) -> Context {
    let Some(document) = syntax.document.as_deref() else {
        return ctx.clone();
    };
    match swlsp_lsp_phpanalysis::for_document(index, document) {
        Ok(Some(state)) => ctx.with_value(state),
        _ => ctx.clone(),
    }
}
```

Every PHP provider in the same request then reads that one analysis instead of
each rebuilding it.

## The provider pattern

Provider traits are declared in `crates/lsp/src/providers.rs`; implementations
live in the `crates/ide-<domain>` crates. Each is a small `Send + Sync` trait
returning protocol types (`ProviderResult<T>` is `Result<T, BoxError>`):

```rust
pub trait CompletionProvider: Send + Sync {
    fn get_completions(&self, ctx: &Context, request: &CompletionRequest)
        -> Vec<protocol::CompletionItem>;
    fn trigger_characters(&self) -> Vec<String>;
}

pub trait HoverProvider: Send + Sync {
    fn get_hover(&self, ctx: &Context, request: &HoverRequest)
        -> ProviderResult<Option<protocol::Hover>>;
}

pub trait CodeLensProvider: Send + Sync {
    fn get_code_lenses(&self, ctx: &Context, request: &CodeLensRequest)
        -> ProviderResult<Vec<protocol::CodeLens>>;
    /// `Ok(None)` when this provider does not own the lens.
    fn resolve_code_lens(&self, ctx: &Context, lens: &protocol::CodeLens)
        -> ProviderResult<Option<protocol::CodeLens>>;
}
```

Each trait also has a defaulted `provider_name()` used in traces
(`SHOPWARE_LSP_TRACE_PROVIDERS=1`).

The full set, with its registration method:

| Trait | Register with | Serves |
| --- | --- | --- |
| `CompletionProvider` | `register_completion_provider` | `textDocument/completion` |
| `GotoDefinitionProvider` | `register_definition_provider` | `textDocument/definition` |
| `ImplementationProvider` | `register_implementation_provider` | `textDocument/implementation` |
| `TypeHierarchyProvider` | `register_type_hierarchy_provider` | `prepareTypeHierarchy`, `supertypes`, `subtypes` |
| `CallHierarchyProvider` | `register_call_hierarchy_provider` | `prepareCallHierarchy`, `incomingCalls`, `outgoingCalls` |
| `ReferencesProvider` | `register_references_provider` | `textDocument/references` |
| `HoverProvider` | `register_hover_provider` | `textDocument/hover` |
| `SignatureHelpProvider` | `register_signature_help_provider` | `textDocument/signatureHelp` |
| `RenameProvider` | `register_rename_provider` | `textDocument/rename` |
| `CodeLensProvider` | `register_code_lens_provider` | `textDocument/codeLens`, `codeLens/resolve` |
| `ActionProvider` | `register_action_provider` | `textDocument/codeAction` (non-inspection actions) |
| `InlayHintProvider` | `register_inlay_hint_provider` | `textDocument/inlayHint` |
| `DocumentLinkProvider` | `register_document_link_provider` | `textDocument/documentLink` |
| `DocumentSymbolProvider` | `register_document_symbol_provider` | `textDocument/documentSymbol` |
| `DocumentHighlightProvider` | `register_document_highlight_provider` | `textDocument/documentHighlight` |
| `LinkedEditingRangeProvider` | `register_linked_editing_range_provider` | `textDocument/linkedEditingRange` |
| `FoldingRangeProvider` | `register_folding_range_provider` | `textDocument/foldingRange` |
| `DocumentFormattingProvider` | `register_document_formatting_provider` | `textDocument/formatting` |
| `SelectionRangeProvider` | `register_selection_range_provider` | `textDocument/selectionRange` |
| `DocumentColorProvider` | `register_document_color_provider` | `documentColor`, `colorPresentation` |
| `SemanticTokensProvider` | `register_semantic_tokens_provider` | `textDocument/semanticTokens/full` |
| `WorkspaceSymbolProvider` | `register_workspace_symbol_provider` | `workspace/symbol` |
| `FileRenameProvider` | `register_file_rename_provider` | `workspace/willRenameFiles` |
| `CommandProvider` | `register_command_provider` | custom methods and `workspace/executeCommand` |
| `Inspection` | `register_inspection` | diagnostics and quick fixes — see [diagnostics](diagnostics-pipeline.md) |

Do not rely on a fixed provider count anywhere; registration changes often.
Each `crates/ide-<domain>/src/lib.rs` is the authoritative list of what its
domain registers.

`SemanticTokensProvider` is slightly different from the rest: it returns
byte-oriented `SemanticToken` values, and the server encodes the LSP delta
format:

```rust
pub struct SemanticToken {
    pub range: TextRange,
    pub token_type: u32, // index into protocol::SEMANTIC_TOKEN_TYPES
    pub modifiers: u32,  // bitset
}
```

Providers therefore never compute delta-encoded UTF-16 triples by hand.

## Writing a provider

A complete provider — feature-flag completion across four languages:

```rust
// crates/ide-shopware/src/completion/shopware/feature.rs
pub struct FeatureCompletionProvider {
    index: Arc<FeatureIndexer>,
}

impl FeatureCompletionProvider {
    pub fn new(index: Arc<FeatureIndexer>) -> Self {
        Self { index }
    }
}

impl CompletionProvider for FeatureCompletionProvider {
    fn get_completions(&self, _ctx: &Context, request: &CompletionRequest)
        -> Vec<protocol::CompletionItem>
    {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let matches = match swlsp_uriutil::filepath::ext(&request.params.text_document.uri) {
            ".twig" => swlsp_parser_twig::query::string_in_function(node, &["feature"]),
            ".scss" => swlsp_parser_scss::query::string_in_function(node, &["feature"]),
            ".php" => swlsp_parser_php::query::string_in_call(node, 0, &["Feature::isActive"]),
            ".js" | ".ts" => swlsp_parser_js::query::string_in_call(
                node, 0, &["Feature.isActive", "Shopware.Feature.isActive"],
            ),
            _ => false,
        };
        if !matches {
            return Vec::new();
        }
        let features = self.index.all_features().unwrap_or_else(|_| Arc::from([]));
        features
            .iter()
            .map(|feature| protocol::CompletionItem {
                label: feature.name.clone(),
                kind: Some(CompletionItemKind::FUNCTION),
                ..Default::default()
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
```

The checklist, in the order it appears above:

1. **Take dependencies through the constructor.** A provider never constructs a
   workspace, opens a database, or reaches for a global.
2. **Reject unsupported contexts immediately.** Extension first, then a query
   predicate. Remember every provider runs for every request.
3. **Use `SyntaxContext`.** `request.node` is already resolved; never reparse
   and never compute offsets from line/character yourself.
4. **Ask the language's `query` module**, not the tree directly.
   `string_in_call` encodes the argument-position semantics once, for everyone.
5. **Answer from an index or the current document.** Never scan the workspace.
6. **Honor cancellation** (`ctx.is_cancelled()`) in any non-trivial loop.
7. **Return deterministic, deduplicated results with exact ranges.** Sort
   anything derived from a map or repository iteration.
8. **Use `swlsp-uriutil`** for URI ↔ path conversion — not string
   concatenation.
9. **Return an empty result (or `None`)** when the provider does not apply, so
   it contributes nothing to the concatenated response.

## Registration

Features are grouped by domain. Each domain crate (`crates/ide-php`,
`ide-symfony`, `ide-twig`, `ide-style`, `ide-admin`, `ide-shopware`) exports
one `Domain` whose `register` function adds everything the domain offers to a
`Registrar`: providers, inspections, suppression readers, observers. Gating by
configuration domain happens inside the domain. The composition root only
lists the domains:

```rust
// crates/app/src/domains.rs
pub const DOMAINS: &[Domain] = &[
    swlsp_ide_php::DOMAIN,
    swlsp_ide_symfony::DOMAIN,
    swlsp_ide_twig::DOMAIN,
    swlsp_ide_style::DOMAIN,
    swlsp_ide_admin::DOMAIN,
    swlsp_ide_shopware::DOMAIN,
];

pub(crate) fn register_features(registrar: Registrar<'_>, services: &Services) -> Vec<Registration> {
    for domain in DOMAINS {
        (domain.register)(&registrar, services);
    }
    registrar.command(Arc::new(crate::integration::Provider));
    register_workspace_symbols(&registrar, services);
    registrar.finish(PRECEDENCE)
}
```

```rust
// crates/ide-twig/src/lib.rs (excerpt)
fn register(server: &Registrar<'_>, s: &Services) {
    server.formatting(Arc::new(formatting::TwigProvider::new()));
    server.code_lens(Arc::new(codelens::TwigCodeLensProvider::new(Some(s.twig.clone()))));
    inspections::register(server, s);
    // ...
}
```

**Order is decided centrally.** Hover and signature help take the first
provider that answers; the other requests concatenate results in provider
order. Which provider goes first is a decision between domains, so the
`Registrar` only collects registrations and `finish` applies them in the
order of `crates/app/src/precedence.rs` (request kind → provider names). A
provider that is not listed runs after the listed ones of its request kind,
in registration order, so a new provider needs an entry there only when its
position matters.

Workspace symbols are the one composite: domains contribute sources with
`workspace_symbol_source`, and the composition root wraps them into the single
catalog-backed workspace symbol provider.

`Services` is a plain struct of every constructed domain index (mostly `Arc`s,
with `Option` for indexes an optional domain may not construct). Each registration function borrows it, which
keeps the signatures short and makes it obvious which services a capability
group actually uses. `Registrar` wraps the server's registration API and
records every registration in order, so tests can assert what is registered
per method.

Registration order determines provider order, which determines result order
within a response. If a change makes results reorder, this is where to look.

Gate registration by domain when the provider belongs to an optional subsystem:

```rust
if server.domain_enabled("administration") { /* register admin providers */ }
```

A domain that is off may not even have a constructed index — the corresponding
field in `Services` will be `None`.

## Capabilities

`server_capabilities()` (`crates/lsp/src/capabilities.rs`) is computed, not
declared. Each entry is advertised only if something was registered **and** the
corresponding feature is enabled:

```rust
for (name, count, feature) in flags {        // e.g. ("hoverProvider", registry.hover.len(), "hover")
    if count > 0 && self.feature_enabled(feature) {
        capabilities.insert(name.into(), Value::Bool(true));
    }
}
```

So a build with no hover providers, or a workspace with hover disabled,
truthfully reports no hover support and the client stops asking. Completion
trigger characters are collected from the registered providers; code-action
kinds from the registered actions and inspections; the semantic-tokens legend
from `protocol::SemanticTokensLegend::shopware()`.

`codeActionProvider.resolveProvider` mirrors what the client advertised:
lazy quick fixes only work if the client supports `codeAction/resolve` with
`edit` in `resolveSupport.properties`. When it does not, the server computes
edits eagerly instead.

An unsupported project root returns `inactive_project_initialize_result()` — a
minimal capability set — rather than refusing to start. The editor stays
functional, the server simply does nothing.

## Gating: features, domains, client profiles

Four orthogonal gates. Knowing which one applies saves a lot of confusion.

**1. Features** (`feature_for_method` → `feature_enabled`) gate whole LSP methods.
Configured under `features` in project configuration. A disabled feature is not
advertised and its method returns a disabled result.

**2. Domains** (`domain_enabled`) gate subsystems — `php`, `symfony.routes`,
`shopware.snippets`, `administration`, `scss`, … A disabled domain skips index
construction, scanner registration, provider registration, and inspection
dispatch. Mapping lives in `domain_for_indexer` (indexers) and in each inspection's
definition (`InspectionDefinition::domain`).

**3. Diagnostic rules** set per-code severity or `off`. See
[`diagnostics-pipeline.md`](diagnostics-pipeline.md#configuration-and-gating).

**4. Client presentation profile** shapes what a host IDE is shown:

```rust
// crates/lsp/src/client.rs
pub enum PresentationProfile {
    Full,      // "full"
    Framework, // "framework"
}
```

A client identifies itself through `initializationOptions.shopwareClient` with a
protocol version, a profile, and the list of commands it can execute. The
protocol version must match `swlsp_integration::PROTOCOL_VERSION` exactly — a mismatch
fails `initialize` rather than degrading silently.

Under the `framework` profile the server withholds generic PHP presentation
(`php.semantic`, `symfony.embedded_language` inspections) and leaves it to the
host IDE, keeping only Shopware and Symfony intelligence. This is what the
PhpStorm integration uses; see
[`phpstorm-integration.md`](phpstorm-integration.md).

`supportedCommands` drives command filtering. A code action or code lens whose
command the client cannot execute is either stripped of its command (when it
also carries an edit) or dropped entirely:

```rust
// crates/lsp/src/client.rs (filter_code_actions_for_client)
let supported = action
    .command
    .as_ref()
    .is_none_or(|command| self.supports_client_command(&command.command));
if supported {
    return Some(action);
}
action.edit.is_some().then(|| {
    action.command = None; // keep the usable edit
    action
})
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
| `shopware/cache/list` | The workspace caches of this machine |
| `shopware/cache/clear` | Delete stale or all caches of other projects |
| `shopware/commands` | Enumerate available custom commands |

Plus notifications the server sends: `shopware/indexingCompleted`,
`shopware/indexingFailed`.

A `CommandProvider` contributes named handlers:

```rust
pub type CommandFunc =
    Arc<dyn Fn(&Context, Option<&Value>) -> Result<Value, BoxError> + Send + Sync>;

pub trait CommandProvider: Send + Sync {
    fn commands(&self, ctx: &Context) -> BTreeMap<String, CommandFunc>;
}
```

`register_commands()` flattens every provider's map into the command registry
at `start`. Entries are reachable both as direct JSON-RPC methods and through
`workspace/executeCommand`, and are gated by the `commands` feature (except
configuration methods, which stay available so a client can always inspect and
fix its configuration).

See [`custom-commands.md`](custom-commands.md) for the custom-method contract and a Neovim example.

## Shutdown

```
"shutdown"  → Server::close_all()
"exit"      → close the connection
disconnect  → Server::close_all() (idempotent)
```

`close_all` marks the server closing (so `start_background` refuses new work),
cancels the lifecycle context and all diagnostics jobs, waits until running
background jobs finish, closes the documents, then closes the workspace — which
closes the scanner, then every indexer in reverse construction order, then the
store. `Store::close` runs `PRAGMA optimize`, `incremental_vacuum`, and a
truncating WAL checkpoint.

A cancelled background job must not publish. Diagnostics check cancellation
before notifying and hold the diagnostics publish lock across the version
re-check and the notify, so a superseded analysis cannot overwrite a newer
result or write after a close.

## The CLI and MCP frontends

`crates/cli` builds a real in-process LSP session (`crates/cli/src/session.rs`)
served over an in-memory Tokio stream: the same `swlsp_app::Application`, the same server, the
same providers. Feature commands are LSP requests, and their output is the protocol
response rendered as text or JSON.

Consequences that are easy to get wrong:

- **CLI and MCP positions are one-based `file:line:column`.** LSP positions are
  zero-based UTF-16. Convert at the adapter boundary, nowhere else.
- **Read-only commands must not mutate workspace files.** `lsp codeaction` and
  `lsp rename` preview a unified diff and write only with an explicit `-w`/`--write`.
- **MCP tools must validate every path against the workspace root.**
- **MCP is served by the official Rust SDK (`rmcp`) over stdio**
  (`crates/cli/src/mcp/mod.rs`). Tool definitions and input schemas come from
  `crates/cli/src/mcp/tools.json`; arguments are validated against them before
  a tool runs on a blocking thread against the shared `CliSession`. Each call
  has its own cancellation (`ToolCall`), cancelled by the client's
  `notifications/cancelled` or the CLI; a cancelled session request sends
  `$/cancelRequest`, so the analysis stops on the server too.
- **`lsp workspace-symbol` keeps its fast direct FTS path** unless `--fresh` is
  requested; do not make it restore semantic graphs by default.
- **CLI mode changes behavior in a few places** (`initializationOptions.cliMode`):
  no watcher unless `initializationOptions.watchFiles` opts back in, no debounced
  diagnostics on open/change, no Administration overlay per checked file, and
  concurrent pull diagnostics. The MCP server sets `watchFiles`, since it outlives
  every tool call.

Commands and their GNU-style flags are declared with `usage-rs` in
`crates/cli/src/args.rs`; `shopware-lsp help` and `shopware-lsp lsp api-json`
enumerate them authoritatively. See
[`reference.md#command-line-interface`](reference.md#command-line-interface).

## Testing

### Provider tests

Construct the domain index and the provider directly, build a `TextDocument`,
and assert on protocol values. `crates/ide/src/completion/test_support.rs` has
request builders (`request_at`, `request_with_cursor`) that resolve the cursor
exactly like the server:

```rust
#[test]
fn completes_feature_flags_in_twig() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(FeatureIndexer::new(dir.path(), None).unwrap());
    index
        .index(&ParsedFile::new("/p/feature.yaml", yaml_source.as_bytes().to_vec()))
        .unwrap();
    let provider = FeatureCompletionProvider::new(index);

    let request = request_with_cursor("file:///p/template.twig", "{{ feature('|') }}");
    let items = provider.get_completions(&Context::background(), &request);
    assert_eq!(items.len(), 1);
}
```

Cover the negative cases too — a provider that answers in a context it should
not is a worse bug than one that answers nothing, because it pollutes every
other provider's results.

### Server-level tests

Add one when protocol routing, document lifecycle, UTF-16 positions, capability
computation, or lazy fix resolution is what you changed. `crates/lsp/src/tests`
covers diagnostics scheduling, transport and cancellation, execute-command
routing, configuration, and workspace lifecycle; `crates/app/src/tests` has
document-lifecycle, client-profile, and store-compliance suites, and the
opt-in real-world suite adds request latency.

Always exercise a real `TextDocument` when positions matter, so UTF-16
conversion is actually covered. Hand-built ranges silently skip the most
error-prone part of the stack.

### Running

```bash
cargo test -p swlsp-lsp
cargo test -p swlsp-ide-twig completion
cargo test -p swlsp-app providers
make rust-test
```

## Further reading

- [`architecture.md`](architecture.md) — the system this layer sits in
- [`parser-architecture.md`](parser-architecture.md) — `SyntaxContext`, trees,
  and the query modules providers depend on
- [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — the inspection system,
  which replaces the provider pattern for diagnostics
- [`refactoring-engine.md`](refactoring-engine.md) — how providers return edits
- [`indexing.md`](indexing.md) — where provider data comes from
- [`custom-commands.md`](custom-commands.md) — custom methods and a Neovim configuration example
- [`phpstorm-integration.md`](phpstorm-integration.md) — the versioned client
  contract and the framework presentation profile
