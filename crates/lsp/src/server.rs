//! The LSP server: provider registration, lifecycle and method dispatch.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::io::{Read, Write};
use std::sync::atomic::AtomicU64;
use std::sync::{Arc, Condvar, Mutex, RwLock, Weak};
use std::thread;

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use swlsp_indexer::FileScanner;
use swlsp_language::LanguageId;
use swlsp_lsp_protocol as protocol;

use crate::configuration::{
    ConfigurationState, disabled_feature_result, feature_for_method, is_configuration_method,
};
use crate::context::{BoxError, CancellationToken, Context, RpcError, codes, lock};
use crate::diagnostics::DiagnosticsState;
use crate::document::{DocumentManager, DocumentObserver, SyntaxContext};
use crate::inspection::{
    DiagnosticId, Inspection, InspectionRegistry, RuleHelp, SuppressionReader,
};
use crate::providers::*;
use crate::transport::{self, Connection, Dispatcher, Handler, Work};
use crate::workspace::{WorkspaceFactory, WorkspaceRuntime};

/// Every registered provider. Registration happens during workspace
/// composition (inside `initialize`); requests read an immutable snapshot.
#[derive(Clone, Default)]
pub(crate) struct Registry {
    pub completion: Vec<Arc<dyn CompletionProvider>>,
    pub definition: Vec<Arc<dyn GotoDefinitionProvider>>,
    pub implementation: Vec<Arc<dyn ImplementationProvider>>,
    pub type_hierarchy: Vec<Arc<dyn TypeHierarchyProvider>>,
    pub call_hierarchy: Vec<Arc<dyn CallHierarchyProvider>>,
    pub references: Vec<Arc<dyn ReferencesProvider>>,
    pub code_lens: Vec<Arc<dyn CodeLensProvider>>,
    pub actions: Vec<Arc<dyn ActionProvider>>,
    pub hover: Vec<Arc<dyn HoverProvider>>,
    pub signature_help: Vec<Arc<dyn SignatureHelpProvider>>,
    pub rename: Vec<Arc<dyn RenameProvider>>,
    pub inlay_hints: Vec<Arc<dyn InlayHintProvider>>,
    pub document_links: Vec<Arc<dyn DocumentLinkProvider>>,
    pub document_symbols: Vec<Arc<dyn DocumentSymbolProvider>>,
    pub document_highlights: Vec<Arc<dyn DocumentHighlightProvider>>,
    pub linked_editing: Vec<Arc<dyn LinkedEditingRangeProvider>>,
    pub folding_ranges: Vec<Arc<dyn FoldingRangeProvider>>,
    pub formatting: Vec<Arc<dyn DocumentFormattingProvider>>,
    pub selection_ranges: Vec<Arc<dyn SelectionRangeProvider>>,
    pub colors: Vec<Arc<dyn DocumentColorProvider>>,
    pub semantic_tokens: Vec<Arc<dyn SemanticTokensProvider>>,
    pub file_rename: Vec<Arc<dyn FileRenameProvider>>,
    pub workspace_symbols: Vec<Arc<dyn WorkspaceSymbolProvider>>,
    pub command_providers: Vec<Arc<dyn CommandProvider>>,
    pub commands: BTreeMap<String, CommandFunc>,
    pub enrichers: HashMap<LanguageId, ContextEnricher>,
    pub inspections: InspectionRegistry,
    pub suppressions: HashMap<LanguageId, SuppressionReader>,
    /// By diagnostic code.
    pub rule_help: BTreeMap<&'static str, RuleHelp>,
}

/// Client integration negotiated in `initialize`.
#[derive(Debug, Clone, Default)]
pub(crate) struct ClientState {
    pub protocol_version: i32,
    pub presentation_profile: String,
    pub filter_commands: bool,
    pub supported_commands: BTreeSet<String>,
}

/// Per-session state written by `initialize`.
#[derive(Debug, Clone, Default)]
pub(crate) struct Session {
    pub root_path: String,
    pub options: protocol::InitializationOptions,
    pub client: ClientState,
    pub work_done_progress: bool,
    pub pull_diagnostics: bool,
    pub diagnostic_refresh_support: bool,
    pub code_action_resolve_support: bool,
    pub initialized: bool,
    pub inactive_project: bool,
    pub project_detection_required: bool,
    pub allow_unsupported_project: bool,
}

#[derive(Default)]
struct Background {
    closing: bool,
    active: usize,
}

/// Shutting the server down failed.
#[derive(Debug, Clone, thiserror::Error)]
pub enum CloseError {
    /// Closing the workspace (its store and background work) failed.
    #[error("close the workspace: {0}")]
    Workspace(Arc<dyn std::error::Error + Send + Sync>),
    /// The shutdown task stopped before finishing.
    #[error("close the server: {0}")]
    Interrupted(String),
}

/// Shared server state (reachable only inside this crate).
pub struct ServerInner {
    pub(crate) version: String,
    pub(crate) session: RwLock<Session>,
    pub(crate) initialize_lock: Mutex<()>,
    registry: RwLock<Arc<Registry>>,
    pub(crate) documents: DocumentManager,
    pub(crate) scanner: RwLock<Option<Arc<FileScanner>>>,
    pub(crate) workspace_factory: Mutex<Option<WorkspaceFactory>>,
    pub(crate) workspace: Mutex<Option<Box<dyn WorkspaceRuntime>>>,
    pub(crate) lifecycle: CancellationToken,
    background: Mutex<Background>,
    background_done: Condvar,
    close_result: Mutex<Option<Result<(), CloseError>>>,
    pub(crate) diagnostics: Mutex<DiagnosticsState>,
    pub(crate) diagnostics_publish: Mutex<()>,
    pub(crate) configuration: RwLock<ConfigurationState>,
    connection: RwLock<Option<Arc<Connection>>>,
    pub(crate) progress_sequence: AtomicU64,
    pub(crate) trace_providers: bool,
}

/// The Shopware language server. Cloning yields another handle to the same
/// server.
#[derive(Clone)]
pub struct Server {
    pub(crate) inner: Arc<ServerInner>,
}

/// A non-owning handle to a [`Server`]. Providers and callbacks stored inside
/// the server hold this instead of a clone, which would keep the server alive
/// through a reference cycle.
#[derive(Clone)]
pub struct WeakServer(std::sync::Weak<ServerInner>);

impl Server {
    pub fn downgrade(&self) -> WeakServer {
        WeakServer(Arc::downgrade(&self.inner))
    }
}

impl WeakServer {
    /// The server, unless it has been dropped.
    pub fn upgrade(&self) -> Option<Server> {
        self.0.upgrade().map(|inner| Server { inner })
    }

    /// [`Server::notice`], unless the server has been dropped.
    pub fn notice(&self, message: &str) {
        if let Some(server) = self.upgrade() {
            server.notice(message);
        }
    }
}

impl Server {
    /// Tells the person at the client a fact about the project they should
    /// know, such as a prerequisite an inspection is missing: a
    /// `window/logMessage` warning, which editors show in the server's
    /// output and the CLI prints after a command's results.
    pub fn notice(&self, message: &str) {
        self.notify(
            "window/logMessage",
            serde_json::json!({"type": 2, "message": message}),
        );
    }
}

impl std::ops::Deref for Server {
    type Target = ServerInner;

    fn deref(&self) -> &ServerInner {
        &self.inner
    }
}

pub(crate) fn read<T>(lock: &RwLock<T>) -> std::sync::RwLockReadGuard<'_, T> {
    lock.read().unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub(crate) fn write<T>(lock: &RwLock<T>) -> std::sync::RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub(crate) fn trace_enabled(variable: &str) -> bool {
    trace_value_enabled(&std::env::var(variable).unwrap_or_default())
}

/// Whether a trace environment value enables tracing: anything but empty,
/// `0`, `false` or `off` (case-insensitive).
pub(crate) fn trace_value_enabled(value: &str) -> bool {
    let value = value.trim().to_lowercase();
    !value.is_empty() && value != "0" && value != "false" && value != "off"
}

pub(crate) fn provider_performance_trace_enabled() -> bool {
    trace_enabled("SHOPWARE_LSP_TRACE_PROVIDERS")
}

impl Server {
    /// Creates a server. The server installs its update callback on a
    /// scanner passed here and on the one a [`WorkspaceFactory`] creates.
    pub fn new(scanner: Option<FileScanner>, root_path: &str, version: &str) -> Server {
        let inner = Arc::new_cyclic(|weak: &Weak<ServerInner>| {
            let scanner = scanner.map(|scanner| {
                scanner.set_on_update(update_callback(weak.clone()));
                Arc::new(scanner)
            });
            ServerInner {
                version: version.to_owned(),
                session: RwLock::new(Session {
                    root_path: root_path.to_owned(),
                    ..Session::default()
                }),
                initialize_lock: Mutex::new(()),
                registry: RwLock::new(Arc::new(Registry::default())),
                documents: DocumentManager::new(),
                scanner: RwLock::new(scanner),
                workspace_factory: Mutex::new(None),
                workspace: Mutex::new(None),
                lifecycle: CancellationToken::new(),
                background: Mutex::new(Background::default()),
                background_done: Condvar::new(),
                close_result: Mutex::new(None),
                diagnostics: Mutex::new(DiagnosticsState::default()),
                diagnostics_publish: Mutex::new(()),
                configuration: RwLock::new(ConfigurationState::default()),
                connection: RwLock::new(None),
                progress_sequence: AtomicU64::new(0),
                trace_providers: provider_performance_trace_enabled(),
            }
        });
        Server { inner }
    }

    /// The callback installed with `FileScanner::set_on_update`: committed
    /// index changes refresh the diagnostics of every open document.
    pub fn index_update_callback(&self) -> impl Fn() + Send + Sync + 'static {
        update_callback(Arc::downgrade(&self.inner))
    }

    pub(crate) fn from_inner(inner: Arc<ServerInner>) -> Server {
        Server { inner }
    }

    pub fn configure_project_detection(&self, required: bool, allow_unsupported: bool) {
        let mut session = write(&self.session);
        session.project_detection_required = required;
        session.allow_unsupported_project = allow_unsupported;
    }

    pub fn set_workspace_factory(&self, factory: WorkspaceFactory) {
        *lock(&self.workspace_factory) = Some(factory);
    }

    /// The initialization options as seen by workspace composition, with
    /// PHP and Shopware settings resolved from the effective configuration.
    pub fn initialization_options(&self) -> protocol::InitializationOptions {
        let configuration = self.effective_configuration();
        let session = read(&self.session);
        protocol::InitializationOptions {
            php_extensions: configuration.php.extensions.clone().unwrap_or_default(),
            disabled_php_extensions: configuration
                .php
                .disabled_extensions
                .clone()
                .unwrap_or_default(),
            shopware_target_version: configuration.shopware.target_version.clone(),
            allow_unsupported_project: session.options.allow_unsupported_project
                || session.allow_unsupported_project,
            cli_mode: session.options.cli_mode,
            shopware_client: session.options.shopware_client.clone(),
            ..protocol::InitializationOptions::default()
        }
    }

    // -- registration -------------------------------------------------------

    pub(crate) fn update_registry(&self, update: impl FnOnce(&mut Registry)) {
        let mut registry = write(&self.inner.registry);
        update(Arc::make_mut(&mut registry));
    }

    pub fn register_completion_provider(&self, provider: Arc<dyn CompletionProvider>) {
        self.update_registry(|registry| registry.completion.push(provider));
    }

    pub fn register_definition_provider(&self, provider: Arc<dyn GotoDefinitionProvider>) {
        self.update_registry(|registry| registry.definition.push(provider));
    }

    pub fn register_implementation_provider(&self, provider: Arc<dyn ImplementationProvider>) {
        self.update_registry(|registry| registry.implementation.push(provider));
    }

    pub fn register_type_hierarchy_provider(&self, provider: Arc<dyn TypeHierarchyProvider>) {
        self.update_registry(|registry| registry.type_hierarchy.push(provider));
    }

    pub fn register_call_hierarchy_provider(&self, provider: Arc<dyn CallHierarchyProvider>) {
        self.update_registry(|registry| registry.call_hierarchy.push(provider));
    }

    pub fn register_linked_editing_range_provider(
        &self,
        provider: Arc<dyn LinkedEditingRangeProvider>,
    ) {
        self.update_registry(|registry| registry.linked_editing.push(provider));
    }

    pub fn register_folding_range_provider(&self, provider: Arc<dyn FoldingRangeProvider>) {
        self.update_registry(|registry| registry.folding_ranges.push(provider));
    }

    pub fn register_document_formatting_provider(
        &self,
        provider: Arc<dyn DocumentFormattingProvider>,
    ) {
        self.update_registry(|registry| registry.formatting.push(provider));
    }

    pub fn register_selection_range_provider(&self, provider: Arc<dyn SelectionRangeProvider>) {
        self.update_registry(|registry| registry.selection_ranges.push(provider));
    }

    pub fn register_document_color_provider(&self, provider: Arc<dyn DocumentColorProvider>) {
        self.update_registry(|registry| registry.colors.push(provider));
    }

    pub fn register_references_provider(&self, provider: Arc<dyn ReferencesProvider>) {
        self.update_registry(|registry| registry.references.push(provider));
    }

    pub fn register_code_lens_provider(&self, provider: Arc<dyn CodeLensProvider>) {
        self.update_registry(|registry| registry.code_lens.push(provider));
    }

    pub fn register_action_provider(&self, provider: Arc<dyn ActionProvider>) {
        self.update_registry(|registry| registry.actions.push(provider));
    }

    pub fn register_hover_provider(&self, provider: Arc<dyn HoverProvider>) {
        self.update_registry(|registry| registry.hover.push(provider));
    }

    pub fn register_signature_help_provider(&self, provider: Arc<dyn SignatureHelpProvider>) {
        self.update_registry(|registry| registry.signature_help.push(provider));
    }

    pub fn register_rename_provider(&self, provider: Arc<dyn RenameProvider>) {
        self.update_registry(|registry| registry.rename.push(provider));
    }

    pub fn register_inlay_hint_provider(&self, provider: Arc<dyn InlayHintProvider>) {
        self.update_registry(|registry| registry.inlay_hints.push(provider));
    }

    pub fn register_document_link_provider(&self, provider: Arc<dyn DocumentLinkProvider>) {
        self.update_registry(|registry| registry.document_links.push(provider));
    }

    pub fn register_document_symbol_provider(&self, provider: Arc<dyn DocumentSymbolProvider>) {
        self.update_registry(|registry| registry.document_symbols.push(provider));
    }

    pub fn register_document_highlight_provider(
        &self,
        provider: Arc<dyn DocumentHighlightProvider>,
    ) {
        self.update_registry(|registry| registry.document_highlights.push(provider));
    }

    pub fn register_semantic_tokens_provider(&self, provider: Arc<dyn SemanticTokensProvider>) {
        self.update_registry(|registry| registry.semantic_tokens.push(provider));
    }

    pub fn register_file_rename_provider(&self, provider: Arc<dyn FileRenameProvider>) {
        self.update_registry(|registry| registry.file_rename.push(provider));
    }

    pub fn register_workspace_symbol_provider(&self, provider: Arc<dyn WorkspaceSymbolProvider>) {
        self.update_registry(|registry| registry.workspace_symbols.push(provider));
    }

    /// Registers a command provider; its commands are reachable as direct
    /// JSON-RPC methods and through `workspace/executeCommand`.
    pub fn register_command_provider(&self, provider: Arc<dyn CommandProvider>) {
        let commands = provider.commands(&Context::with_token(self.lifecycle.clone()));
        self.update_registry(|registry| {
            registry.command_providers.push(provider);
            registry.commands.extend(commands);
        });
    }

    /// Registers one command handler directly.
    pub fn register_command(&self, name: impl Into<String>, command: CommandFunc) {
        let name = name.into();
        self.update_registry(|registry| {
            registry.commands.insert(name, command);
        });
    }

    fn register_commands(&self) {
        let ctx = Context::with_token(self.lifecycle.clone());
        let providers = self.registry().command_providers.clone();
        for provider in providers {
            let commands = provider.commands(&ctx);
            self.update_registry(|registry| registry.commands.extend(commands));
        }
    }

    /// Registers an inspection with its quick fixes.
    ///
    /// # Panics
    /// When the definition is invalid; workspace composition fails fast.
    pub fn register_inspection(&self, inspection: Arc<dyn Inspection>) {
        let mut result = Ok(());
        self.update_registry(|registry| result = registry.inspections.register(inspection));
        if let Err(error) = result {
            panic!("register inspection: {error}");
        }
    }

    /// Adds what to do about diagnostic codes to the configuration catalog.
    /// A code may be explained once.
    pub fn register_rule_help(&self, help: &'static [RuleHelp]) {
        self.update_registry(|registry| {
            for entry in help {
                assert!(
                    registry.rule_help.insert(entry.code, *entry).is_none(),
                    "diagnostic {:?} is explained more than once",
                    entry.code
                );
            }
        });
    }

    /// The explained codes no inspection declares: a renamed or removed
    /// rule left its explanation behind.
    pub fn unknown_rule_help(&self) -> Vec<&'static str> {
        let registry = self.registry();
        registry
            .rule_help
            .keys()
            .filter(|code| {
                !registry
                    .inspections
                    .by_code
                    .contains_key(&DiagnosticId::from(**code))
            })
            .copied()
            .collect()
    }

    /// Adds language-specific semantic context without exposing domain
    /// indexes through the protocol server.
    pub fn register_context_enricher(&self, language: LanguageId, enricher: ContextEnricher) {
        self.update_registry(|registry| {
            registry.enrichers.insert(language, enricher);
        });
    }

    /// Applies the suppression comments of `language` documents to the
    /// problems of every inspection.
    pub fn register_suppressions(&self, language: LanguageId, reader: SuppressionReader) {
        self.update_registry(|registry| {
            registry.suppressions.insert(language, reader);
        });
    }

    /// Exposes the open-document lifecycle to workspace composition.
    pub fn register_document_observer(&self, observer: DocumentObserver) {
        self.documents.register_observer(observer);
    }

    // -- lifecycle ----------------------------------------------------------

    /// Runs `work` on a lifecycle-managed thread. Returns `false` once the
    /// server is closing.
    pub(crate) fn start_background(&self, work: impl FnOnce(Context) + Send + 'static) -> bool {
        {
            let mut background = lock(&self.background);
            if background.closing {
                return false;
            }
            background.active += 1;
        }
        let inner = self.inner.clone();
        let ctx = Context::with_token(self.lifecycle.clone());
        let spawned = thread::Builder::new()
            .name("lsp-background".into())
            .spawn(move || {
                struct Done(Arc<ServerInner>);
                impl Drop for Done {
                    fn drop(&mut self) {
                        let mut background = lock(&self.0.background);
                        background.active -= 1;
                        self.0.background_done.notify_all();
                    }
                }
                let _done = Done(inner);
                work(ctx);
            });
        if spawned.is_err() {
            let mut background = lock(&self.background);
            background.active -= 1;
            self.background_done.notify_all();
            return false;
        }
        true
    }

    /// Cancels background work, waits for it, then closes documents and the
    /// workspace. Idempotent; later calls return the first result.
    pub fn close_all(&self) -> Result<(), CloseError> {
        let mut result = lock(&self.close_result);
        if let Some(result) = result.as_ref() {
            return result.clone();
        }
        {
            let mut background = lock(&self.background);
            background.closing = true;
            self.lifecycle.cancel();
        }
        self.cancel_all_diagnostics();
        {
            let mut background = lock(&self.background);
            while background.active > 0 {
                background = self
                    .background_done
                    .wait(background)
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
            }
        }
        self.documents.close();
        let closed = if let Some(workspace) = lock(&self.workspace).as_ref() {
            workspace
                .close()
                .map_err(|error| CloseError::Workspace(Arc::from(error)))
        } else if let Some(scanner) = read(&self.scanner).as_ref() {
            scanner
                .close()
                .map_err(|error| CloseError::Workspace(Arc::new(error)))
        } else {
            Ok(())
        };
        *result = Some(closed.clone());
        closed
    }

    /// Serves JSON-RPC on `input`/`output` until the client disconnects or
    /// sends `exit`, then closes the server.
    pub fn start(
        &self,
        input: impl Read + Send + 'static,
        output: impl Write + Send + 'static,
    ) -> Result<(), CloseError> {
        self.register_commands();
        let handler: Arc<dyn Handler> = Arc::new(self.clone());
        let server = self.clone();
        transport::serve_blocking(input, output, handler, move |conn| {
            *write(&server.connection) = Some(conn.clone());
        });
        *write(&self.connection) = None;
        self.close_all()
    }

    /// [`Server::start`] on the caller's Tokio runtime, for async or
    /// blocking streams. Serving also ends when `stop` completes.
    pub async fn serve(
        &self,
        input: crate::async_io::Input,
        output: crate::async_io::Output,
        stop: impl std::future::Future<Output = ()>,
    ) -> Result<(), CloseError> {
        self.register_commands();
        let handler: Arc<dyn Handler> = Arc::new(self.clone());
        let server = self.clone();
        transport::serve(
            input,
            output,
            handler,
            move |conn| {
                *write(&server.connection) = Some(conn.clone());
            },
            stop,
        )
        .await;
        *write(&self.connection) = None;
        let server = self.clone();
        tokio::task::spawn_blocking(move || server.close_all())
            .await
            .map_err(|error| CloseError::Interrupted(error.to_string()))?
    }

    pub(crate) fn connection(&self) -> Option<Arc<Connection>> {
        read(&self.connection).clone()
    }

    /// Sends a notification when a client is connected.
    pub(crate) fn notify(&self, method: &str, params: impl Serialize) -> bool {
        match self.connection() {
            Some(conn) => match conn.notify(method, params) {
                Ok(()) => true,
                Err(error) => {
                    log::warn!("notify {method}: {error}");
                    false
                }
            },
            None => false,
        }
    }

    pub(crate) fn registry(&self) -> Arc<Registry> {
        read(&self.inner.registry).clone()
    }

    pub(crate) fn root_path(&self) -> String {
        read(&self.session).root_path.clone()
    }

    pub(crate) fn cli_mode(&self) -> bool {
        read(&self.session).options.cli_mode
    }

    pub(crate) fn enrich_context(
        &self,
        registry: &Registry,
        ctx: &Context,
        syntax: &SyntaxContext,
    ) -> Context {
        match syntax
            .language
            .and_then(|language| registry.enrichers.get(&language))
        {
            Some(enrich) => enrich(ctx, syntax),
            None => ctx.clone(),
        }
    }

    // -- dispatch -----------------------------------------------------------

    /// Resolves and runs one method. `params` is `Value::Null` when absent.
    pub(crate) fn handle(
        &self,
        ctx: &Context,
        method: &str,
        params: Value,
        notification: bool,
    ) -> Result<Value, RpcError> {
        let result = self.handle_method(ctx, method, params, notification);
        if ctx.is_cancelled() && !notification {
            return Err(RpcError::cancelled());
        }
        result
    }

    fn handle_method(
        &self,
        ctx: &Context,
        method: &str,
        params: Value,
        notification: bool,
    ) -> Result<Value, RpcError> {
        if method == "exit" {
            log::info!("Received exit notification, exiting");
            if let Some(conn) = self.connection() {
                conn.close();
            }
            return Ok(Value::Null);
        }
        if let Some(feature) = feature_for_method(method)
            && !self.feature_enabled(feature)
        {
            return Ok(disabled_feature_result(method));
        }
        let registry = self.registry();
        if let Some(command) = registry.commands.get(method) {
            if !self.feature_enabled("commands") && !is_configuration_method(method) {
                return Err(RpcError::new(
                    codes::METHOD_NOT_FOUND,
                    "Shopware commands are disabled by configuration",
                ));
            }
            let args = (!params.is_null()).then_some(&params);
            return command(ctx, args).map_err(RpcError::from_error);
        }
        match self.protocol_method(ctx, method, params) {
            Some(result) => result,
            None if notification => Ok(Value::Null),
            None => Err(RpcError::new(
                codes::METHOD_NOT_FOUND,
                format!("Method not implemented: {method}"),
            )),
        }
    }

    /// The protocol method table. `None` means
    /// the method is unknown.
    fn protocol_method(
        &self,
        ctx: &Context,
        method: &str,
        params: Value,
    ) -> Option<Result<Value, RpcError>> {
        let result = match method {
            "initialize" => self.handle_initialize(ctx, params),
            "initialized" => {
                self.handle_initialized();
                Ok(Value::Null)
            }
            "textDocument/didOpen" => self.handle_did_open(params),
            "textDocument/didChange" => self.handle_did_change(params),
            "textDocument/didClose" => self.handle_did_close(params),
            "textDocument/completion" => value_handler(params, |p| self.completion(ctx, p)),
            "textDocument/definition" => {
                value_handler(params, |p| null_if_empty(self.definition(ctx, p)))
            }
            "textDocument/implementation" => {
                value_handler(params, |p| null_if_empty(self.implementation(ctx, p)))
            }
            "textDocument/prepareTypeHierarchy" => value_handler(params, |p| {
                null_if_empty(self.prepare_type_hierarchy(ctx, p))
            }),
            "typeHierarchy/supertypes" => {
                value_handler(params, |p: protocol::TypeHierarchySupertypesParams| {
                    null_if_empty(self.type_hierarchy_supertypes(ctx, &p.item))
                })
            }
            "typeHierarchy/subtypes" => {
                value_handler(params, |p: protocol::TypeHierarchySubtypesParams| {
                    null_if_empty(self.type_hierarchy_subtypes(ctx, &p.item))
                })
            }
            "textDocument/prepareCallHierarchy" => result_handler(params, |p| {
                self.prepare_call_hierarchy(ctx, p).map(null_if_empty)
            }),
            "callHierarchy/incomingCalls" => {
                result_handler(params, |p: protocol::CallHierarchyIncomingCallsParams| {
                    self.call_hierarchy_incoming_calls(ctx, p.item)
                        .map(null_if_empty)
                })
            }
            "callHierarchy/outgoingCalls" => {
                result_handler(params, |p: protocol::CallHierarchyOutgoingCallsParams| {
                    self.call_hierarchy_outgoing_calls(ctx, p.item)
                        .map(null_if_empty)
                })
            }
            "textDocument/references" => {
                result_handler(params, |p| self.references(ctx, p).map(null_if_empty))
            }
            "textDocument/codeLens" => {
                result_handler(params, |p| self.code_lens(ctx, p).map(null_if_empty))
            }
            "textDocument/hover" => result_handler(params, |p| self.hover(ctx, p)),
            "textDocument/signatureHelp" => result_handler(params, |p| self.signature_help(ctx, p)),
            "textDocument/rename" => result_handler(params, |p| self.rename(ctx, p)),
            "textDocument/inlayHint" => {
                result_handler(params, |p| self.inlay_hints(ctx, p).map(null_if_empty))
            }
            "textDocument/documentLink" => {
                result_handler(params, |p| self.document_links(ctx, p).map(null_if_empty))
            }
            "textDocument/documentSymbol" => {
                result_handler(params, |p| self.document_symbols(ctx, p).map(null_if_empty))
            }
            "textDocument/documentHighlight" => result_handler(params, |p| {
                self.document_highlights(ctx, p).map(null_if_empty)
            }),
            "textDocument/linkedEditingRange" => {
                result_handler(params, |p| self.linked_editing_ranges(ctx, p))
            }
            "textDocument/foldingRange" => {
                result_handler(params, |p| self.folding_ranges(ctx, p).map(null_if_empty))
            }
            "textDocument/formatting" => {
                result_handler(params, |p| self.formatting(ctx, p).map(null_if_empty))
            }
            "textDocument/selectionRange" => {
                result_handler(params, |p| self.selection_ranges(ctx, p).map(null_if_empty))
            }
            "textDocument/documentColor" => {
                result_handler(params, |p| self.document_colors(ctx, Some(p)))
            }
            "textDocument/colorPresentation" => {
                result_handler(params, |p| self.color_presentations(ctx, Some(p)))
            }
            "textDocument/semanticTokens/full" => {
                result_handler(params, |p| self.semantic_tokens(ctx, p))
            }
            "textDocument/diagnostic" => value_handler(params, |p| self.diagnostic(ctx, &p)),
            "workspace/diagnostic" => value_handler(params, |p| self.workspace_diagnostic(ctx, &p)),
            "shopware/configuration/catalog" => to_value(&self.configuration_catalog()),
            "shopware/configuration/effective" => to_value(&self.effective_configuration()),
            "shopware/configuration/reload" => to_value(&self.reload_project_configuration()),
            "workspace/didChangeConfiguration" => self.handle_did_change_configuration(params),
            "codeLens/resolve" => result_handler(params, |p| self.resolve_code_lens(ctx, p)),
            "textDocument/codeAction" => {
                value_handler(params, |p| null_if_empty(self.code_action(ctx, &p)))
            }
            "codeAction/resolve" => value_handler(params, |p| self.resolve_code_action(ctx, p)),
            "shopware/forceReindex" => self.handle_force_reindex(),
            "shopware/index/stats" => self.handle_index_stats(),
            "shopware/commands" => to_value(&self.registry().commands.keys().collect::<Vec<_>>()),
            "workspace/executeCommand" => self.handle_execute_command(ctx, params),
            "workspace/willRenameFiles" => {
                result_handler(params, |p| self.will_rename_files(ctx, p))
            }
            "workspace/symbol" => result_handler(params, |p| {
                self.workspace_symbols(ctx, p).map(null_if_empty)
            }),
            "shutdown" => {
                if let Err(error) = self.close_all() {
                    log::warn!("Error closing indexers: {error}");
                }
                log::info!("Received shutdown request, waiting for exit notification");
                Ok(Value::Null)
            }
            // fsnotify-style watching in the scanner is the single source of
            // index file events; accepting client notifications as well
            // would process the same change twice.
            "workspace/didCreateFiles"
            | "workspace/didRenameFiles"
            | "workspace/didDeleteFiles"
            | "workspace/didChangeWatchedFiles" => Ok(Value::Null),
            _ => return None,
        };
        Some(result)
    }

    pub(crate) fn handle_execute_command(
        &self,
        ctx: &Context,
        params: Value,
    ) -> Result<Value, RpcError> {
        let params: protocol::CommandRequest = decode_params(params)?;
        let Some(command) = self.registry().commands.get(&params.command).cloned() else {
            return Err(RpcError::new(
                codes::METHOD_NOT_FOUND,
                format!("Unknown Shopware command: {}", params.command),
            ));
        };
        if params.arguments.len() > 1 {
            return Err(RpcError::new(
                codes::INVALID_PARAMS,
                "Shopware commands accept at most one JSON argument",
            ));
        }
        let argument = params
            .arguments
            .into_iter()
            .next()
            .unwrap_or_else(|| json!({}));
        command(ctx, Some(&argument)).map_err(RpcError::from_error)
    }
}

fn update_callback(weak: Weak<ServerInner>) -> impl Fn() + Send + Sync + 'static {
    move || {
        if let Some(inner) = weak.upgrade() {
            log::info!("Refreshing diagnostics of open files after an index change");
            Server::from_inner(inner).refresh_after_index_change();
        }
    }
}

impl Handler for Server {
    fn handle(&self, _conn: &Arc<Connection>, work: &Work) -> Result<Value, RpcError> {
        let ctx = Context::with_token(work.cancel.clone());
        Server::handle(
            self,
            &ctx,
            &work.method,
            work.params.clone(),
            work.id.is_none(),
        )
    }

    fn offload(&self, dispatcher: &Arc<Dispatcher>, work: Work) -> Option<Work> {
        if work.method != "textDocument/diagnostic" || !self.cli_mode() {
            return Some(work);
        }
        // CLI pull diagnostics run as lifecycle-managed background jobs; the
        // request stays registered until completion and shutdown cancels it.
        let slot = Arc::new(Mutex::new(Some(work)));
        let job = slot.clone();
        let dispatcher_for_job = dispatcher.clone();
        if self.start_background(move |lifecycle| {
            if let Some(work) = lock(&job).take() {
                lifecycle.token().link(&work.cancel);
                dispatcher_for_job.execute(work);
            }
        }) {
            return None;
        }
        lock(&slot).take()
    }
}

// -- RPC adapters -------------------------------------------------------------

pub(crate) fn decode_params<P: DeserializeOwned>(params: Value) -> Result<P, RpcError> {
    if params.is_null() {
        return Err(RpcError::invalid_params("missing request parameters"));
    }
    serde_json::from_value(params).map_err(|error| RpcError::invalid_params(error.to_string()))
}

pub(crate) fn to_value(value: &impl Serialize) -> Result<Value, RpcError> {
    serde_json::to_value(value).map_err(|error| RpcError::internal(error.to_string()))
}

fn value_handler<P: DeserializeOwned, R: Serialize>(
    params: Value,
    handle: impl FnOnce(P) -> R,
) -> Result<Value, RpcError> {
    let params = decode_params(params)?;
    to_value(&handle(params))
}

fn result_handler<P: DeserializeOwned, R: Serialize>(
    params: Value,
    handle: impl FnOnce(P) -> Result<R, BoxError>,
) -> Result<Value, RpcError> {
    let params = decode_params(params)?;
    match handle(params) {
        Ok(result) => to_value(&result),
        Err(error) => Err(RpcError::from_error(error)),
    }
}

/// Empty provider results are sent as `null` ("no result" in LSP) rather
/// than `[]`.
pub(crate) fn null_if_empty<T>(values: Vec<T>) -> Option<Vec<T>> {
    (!values.is_empty()).then_some(values)
}
