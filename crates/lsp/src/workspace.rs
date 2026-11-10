//! Workspace initialization and indexing lifecycle.

use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::{Duration, Instant};

use serde_json::{Value, json};
use swlsp_indexer::{FileScanner, IndexProgress};
use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig as projectconfig;
use swlsp_textutil::number::grouped;
use swlsp_uriutil::filepath;

use crate::client::ClientContractError;
use crate::context::{BoxError, Context, RpcError, codes, lock};
use crate::diagnostics::DiagnosticsSuspension;
use crate::server::{Server, decode_params, read, write};

/// Owns the resources created for one initialized workspace. The server
/// supports one workspace root per process.
pub trait WorkspaceRuntime: Send + Sync {
    fn root(&self) -> &str;
    /// The file scanner. The server installs its update callback.
    fn scanner(&self) -> Option<Arc<FileScanner>>;
    fn initial_force_reindex(&self) -> bool;
    fn close(&self) -> Result<(), BoxError>;
}

/// Builds the workspace during `initialize`. It receives the server so it
/// can register providers, inspections and document observers.
pub type WorkspaceFactory = Arc<
    dyn Fn(&Context, &str, &Server) -> Result<Box<dyn WorkspaceRuntime>, BoxError> + Send + Sync,
>;

/// Completes an indexing progress report with the elapsed time and error.
type FinishProgress = Box<dyn FnOnce(Duration, Option<&str>) + Send>;

/// What an indexing progress report says: the files looked at, then the
/// phase after the last one (writing the rest and publishing the indexes).
fn indexing_progress_message(progress: IndexProgress) -> String {
    if progress.done >= progress.total {
        return "finishing".to_owned();
    }
    format!(
        "{}/{} files",
        grouped(progress.done),
        grouped(progress.total)
    )
}

/// Failure of the `initialize` request.
#[derive(Debug, thiserror::Error)]
pub enum InitializeError {
    #[error("server is already initialized")]
    AlreadyInitialized,
    #[error(
        "multiple workspace folders are not supported; start one Shopware LSP process per workspace"
    )]
    MultipleWorkspaceFolders,
    #[error("a workspace root is required")]
    MissingWorkspaceRoot,
    #[error("resolve workspace root: {0}")]
    ResolveWorkspaceRoot(#[source] swlsp_uriutil::UriError),
    #[error("make workspace root absolute: {0}")]
    AbsoluteWorkspaceRoot(#[source] std::io::Error),
    #[error(transparent)]
    ClientContract(#[from] ClientContractError),
    #[error("detect project type: {0}")]
    DetectProject(#[source] swlsp_projectdetect::DetectError),
    #[error(
        "unsupported project root {root}: no Shopware or Symfony project markers found; add {marker} or allow unsupported projects explicitly"
    )]
    UnsupportedProject { root: String, marker: &'static str },
    #[error("load configuration: {0}")]
    LoadConfiguration(String),
    #[error("initialize workspace: {0}")]
    CreateWorkspace(#[source] BoxError),
    #[error("initialize workspace: factory returned no scanner")]
    MissingScanner,
    #[error("load scoped configuration: {0}")]
    LoadScopedConfiguration(String),
    #[error("validate configuration: {0}")]
    ValidateConfiguration(String),
}

/// Resolves the single workspace root from `initialize` params.
pub(crate) fn workspace_root(
    params: &protocol::InitializeParams,
) -> Result<String, InitializeError> {
    if params.workspace_folders.len() > 1 {
        return Err(InitializeError::MultipleWorkspaceFolders);
    }
    let value = if let Some(folder) = params.workspace_folders.first() {
        folder.uri.as_str()
    } else if !params.root_uri.is_empty() {
        params.root_uri.as_str()
    } else if !params.root_path.is_empty() {
        params.root_path.as_str()
    } else {
        return Err(InitializeError::MissingWorkspaceRoot);
    };
    let path = swlsp_uriutil::path(value).map_err(InitializeError::ResolveWorkspaceRoot)?;
    let absolute = filepath::abs(&path).map_err(InitializeError::AbsoluteWorkspaceRoot)?;
    Ok(filepath::clean(&absolute))
}

impl Server {
    pub(crate) fn handle_initialize(
        &self,
        ctx: &Context,
        params: Value,
    ) -> Result<Value, RpcError> {
        let params: protocol::InitializeParams = decode_params(params)
            .map_err(|error| RpcError::new(codes::PARSE_ERROR, error.message))?;
        self.initialize(ctx, &params)
            .map_err(|error| RpcError::new(codes::INVALID_PARAMS, error.to_string()))
    }

    pub(crate) fn initialize(
        &self,
        ctx: &Context,
        params: &protocol::InitializeParams,
    ) -> Result<Value, InitializeError> {
        let _initializing = lock(&self.initialize_lock);
        if read(&self.session).initialized {
            return Err(InitializeError::AlreadyInitialized);
        }
        let root = workspace_root(params)?;
        let options = params.initialization_options.clone();
        {
            let mut session = write(&self.session);
            session.root_path = root.clone();
            session.options = options.clone();
        }
        self.configure_client_integration(options.shopware_client.as_ref())?;
        {
            let mut session = write(&self.session);
            session.work_done_progress = params.capabilities.window.work_done_progress;
            session.pull_diagnostics = params.capabilities.text_document.diagnostic.is_some();
            session.diagnostic_refresh_support =
                params.capabilities.workspace.diagnostics.refresh_support;
        }
        let (detection_required, allow_unsupported) = {
            let session = read(&self.session);
            (
                session.project_detection_required,
                session.allow_unsupported_project,
            )
        };
        if detection_required && !allow_unsupported && !options.allow_unsupported_project {
            let detection =
                swlsp_projectdetect::detect(&root).map_err(InitializeError::DetectProject)?;
            if !detection.supported {
                let unsupported = InitializeError::UnsupportedProject {
                    root: root.clone(),
                    marker: projectconfig::PROJECT_RELATIVE_PATH,
                };
                if options.cli_mode {
                    return Err(unsupported);
                }
                {
                    let mut session = write(&self.session);
                    session.inactive_project = true;
                    session.initialized = true;
                }
                log::warn!("Shopware LSP inactive: {unsupported}");
                return Ok(self.inactive_project_initialize_result());
            }
        }
        let scope_root = root.clone();
        let scope_load = std::thread::spawn(move || {
            projectconfig::load_scopes(&scope_root).map_err(|error| error.to_string())
        });
        self.initialize_configuration(&root, &options)
            .map_err(InitializeError::LoadConfiguration)?;
        {
            let mut session = write(&self.session);
            session.code_action_resolve_support = params
                .capabilities
                .text_document
                .code_action
                .as_ref()
                .is_some_and(|code_action| {
                    code_action.data_support
                        && code_action
                            .resolve_support
                            .properties
                            .iter()
                            .any(|property| property == "edit")
                });
        }

        let factory = lock(&self.workspace_factory).clone();
        if let Some(factory) = factory
            && lock(&self.workspace).is_none()
        {
            let workspace = factory(ctx, &root, self).map_err(InitializeError::CreateWorkspace)?;
            let Some(scanner) = workspace.scanner() else {
                let _ = workspace.close();
                return Err(InitializeError::MissingScanner);
            };
            scanner.set_on_update(self.index_update_callback());
            *write(&self.scanner) = Some(scanner);
            *lock(&self.workspace) = Some(workspace);
        }
        let loaded = scope_load
            .join()
            .unwrap_or_else(|_| Err("load configuration scopes panicked".to_owned()));
        if let Err(error) = self.install_initial_configuration_scopes(loaded) {
            self.discard_workspace();
            return Err(InitializeError::LoadScopedConfiguration(error));
        }
        if let Err(error) = self.validate_configured_diagnostic_ids() {
            if options.cli_mode {
                self.discard_workspace();
                return Err(InitializeError::ValidateConfiguration(error));
            }
            self.join_configuration_error(&error);
            log::warn!("Invalid Shopware LSP configuration: {error}");
        }
        write(&self.session).initialized = true;
        Ok(json!({
            "capabilities": self.server_capabilities(),
            "serverInfo": {"name": "shopware-lsp", "version": self.version},
        }))
    }

    fn discard_workspace(&self) {
        if let Some(workspace) = lock(&self.workspace).take() {
            let _ = workspace.close();
        }
        *write(&self.scanner) = None;
    }

    pub(crate) fn inactive_project_initialize_result(&self) -> Value {
        json!({
            "capabilities": {
                "experimental": {
                    "shopwareLSP": self.negotiated_client_state(false, "unsupportedProject"),
                },
            },
            "serverInfo": {"name": "shopware-lsp", "version": self.version},
        })
    }

    pub(crate) fn scanner(&self) -> Option<Arc<FileScanner>> {
        read(&self.inner.scanner).clone()
    }

    pub(crate) fn handle_initialized(&self) {
        self.notify_configuration_error();
        let Some(scanner) = self.scanner() else {
            self.notify_disabled_cli_indexing();
            return;
        };
        if !self.effective_configuration().indexing.enabled {
            self.notify_disabled_cli_indexing();
            return;
        }
        // Diagnostics observe the workspace only after the initial index is
        // published. The suspension lifts before `shopware/indexingCompleted`
        // is sent, so a client that opens a document and pulls diagnostics
        // after that notification never sees the suspended (empty) result.
        // Dropping the guard resumes on failure, cancellation, or when the
        // background job cannot start.
        let suspension = DiagnosticsSuspension::new(self, !self.cli_mode());
        let server = self.clone();
        self.start_background(move |ctx| {
            let force = lock(&server.workspace)
                .as_ref()
                .is_some_and(|workspace| workspace.initial_force_reindex());
            if let Err(error) = server.index_all(&ctx, &scanner, force, Some(&suspension)) {
                log::warn!("Error indexing: {error}");
                server.notify_indexing_failed(&error);
                if ctx.is_cancelled() {
                    return;
                }
            }
            drop(suspension);
            if !ctx.is_cancelled() && server.file_watching_enabled() {
                match scanner.start_watcher() {
                    Ok(()) => log::info!("File watcher started successfully"),
                    Err(error) => log::warn!("Error starting file watcher: {error}"),
                }
            }
        });
    }

    /// A one-shot command exits before the watcher registration pays for
    /// itself.
    pub(crate) fn file_watching_enabled(&self) -> bool {
        let session = read(&self.session);
        !session.options.cli_mode || session.options.watch_files
    }

    fn notify_disabled_cli_indexing(&self) {
        if !self.cli_mode() {
            return;
        }
        self.notify(
            "shopware/indexingCompleted",
            json!({"message": "Indexing disabled", "timeInSeconds": 0}),
        );
    }

    pub(crate) fn notify_indexing_failed(&self, cause: &str) {
        self.notify("shopware/indexingFailed", json!({"message": cause}));
    }

    /// Builds or updates every index; `force` clears hashes first. A held
    /// `suspension` is resumed once the indexes are published and before
    /// `shopware/indexingCompleted` is sent.
    pub(crate) fn index_all(
        &self,
        ctx: &Context,
        scanner: &FileScanner,
        force: bool,
        suspension: Option<&DiagnosticsSuspension>,
    ) -> Result<(), String> {
        let started = Instant::now();
        let finish = self.begin_indexing_progress(ctx, scanner);
        let result = self.index_all_inner(ctx, scanner, force, started, suspension);
        scanner.set_on_progress(None);
        finish(started.elapsed(), result.as_ref().err().map(String::as_str));
        result
    }

    fn index_all_inner(
        &self,
        ctx: &Context,
        scanner: &FileScanner,
        force: bool,
        started: Instant,
        suspension: Option<&DiagnosticsSuspension>,
    ) -> Result<(), String> {
        if let Some(conn) = self.connection() {
            conn.notify(
                "shopware/indexingStarted",
                json!({"message": "Indexing started"}),
            )
            .map_err(|error| error.to_string())?;
        }
        if force {
            ctx.check().map_err(|error| error.to_string())?;
            scanner.clear_hashes().map_err(|error| error.to_string())?;
        }
        ctx.check().map_err(|error| error.to_string())?;
        scanner
            .index_all(&ctx.token().indexer_token())
            .map_err(|error| error.to_string())?;
        if let Some(suspension) = suspension {
            suspension.resume();
        }
        if let Some(conn) = self.connection() {
            conn.notify(
                "shopware/indexingCompleted",
                json!({
                    "message": "Indexing completed",
                    "timeInSeconds": started.elapsed().as_secs_f64(),
                }),
            )
            .map_err(|error| error.to_string())?;
        }
        Ok(())
    }

    fn begin_indexing_progress(&self, ctx: &Context, scanner: &FileScanner) -> FinishProgress {
        let noop: FinishProgress = Box::new(|_, _| {});
        if !read(&self.session).work_done_progress {
            return noop;
        }
        let Some(conn) = self.connection() else {
            return noop;
        };
        let token = format!(
            "shopware-index-{}",
            self.progress_sequence.fetch_add(1, Ordering::Relaxed) + 1
        );
        if let Err(error) = conn.call(
            "window/workDoneProgress/create",
            json!({"token": token}),
            ctx.token(),
            Some(Duration::from_secs(2)),
        ) {
            if !ctx.is_cancelled() {
                log::warn!("Create indexing progress: {error}");
            }
            return noop;
        }
        if let Err(error) = conn.notify(
            "$/progress",
            json!({
                "token": token,
                "value": {
                    "kind": "begin", "title": "Shopware indexing",
                    "message": "Indexing workspace", "cancellable": false,
                },
            }),
        ) {
            if !ctx.is_cancelled() {
                log::warn!("Publish indexing progress: {error}");
            }
            return noop;
        }
        {
            // The scan reports the files it has looked at; failing to send
            // one report is not worth a log line per report.
            let (conn, token) = (conn.clone(), token.clone());
            scanner.set_on_progress(Some(Arc::new(move |progress: IndexProgress| {
                let _ = conn.notify(
                    "$/progress",
                    json!({
                        "token": token,
                        "value": {
                            "kind": "report",
                            "message": indexing_progress_message(progress),
                            "percentage": progress.done * 100 / progress.total.max(1),
                        },
                    }),
                );
            })));
        }
        let ctx = ctx.clone();
        Box::new(move |elapsed: Duration, cause: Option<&str>| {
            let message = match cause {
                Some(cause) => format!("Indexing failed: {cause}"),
                None => format!("Indexing completed in {:.2}s", elapsed.as_secs_f64()),
            };
            if let Err(error) = conn.notify(
                "$/progress",
                json!({"token": token, "value": {"kind": "end", "message": message}}),
            ) && !ctx.is_cancelled()
            {
                log::warn!("Complete indexing progress: {error}");
            }
        })
    }

    pub(crate) fn handle_force_reindex(&self) -> Result<Value, RpcError> {
        if !self.effective_configuration().indexing.enabled {
            return Err(RpcError::internal("indexing is disabled by configuration"));
        }
        let suspension = DiagnosticsSuspension::new(self, !self.cli_mode());
        let server = self.clone();
        self.start_background(move |ctx| {
            if let Some(scanner) = server.scanner()
                && let Err(error) = server.index_all(&ctx, &scanner, true, Some(&suspension))
            {
                log::warn!("Error force reindexing: {error}");
                server.notify_indexing_failed(&error);
            }
            drop(suspension);
        });
        Ok(json!({"message": "Force reindexing started"}))
    }

    pub(crate) fn handle_index_stats(&self) -> Result<Value, RpcError> {
        let Some(scanner) = self.scanner() else {
            return Err(RpcError::internal("workspace is not initialized"));
        };
        let stats = scanner
            .stats()
            .map_err(|error| RpcError::internal(error.to_string()))?;
        crate::server::to_value(&stats)
    }

    /// Indexes the given files through the workspace scanner.
    pub fn index_files(&self, ctx: &Context, files: &[String]) -> Result<(), BoxError> {
        let Some(scanner) = self.scanner() else {
            return Err("workspace is not initialized".into());
        };
        scanner.index_files(&ctx.token().indexer_token(), files)?;
        Ok(())
    }

    /// Tracked resource paths below `directory` from the persisted index.
    pub fn resource_paths(&self, directory: &str, limit: usize) -> Result<Vec<String>, BoxError> {
        if !crate::code_action::path_within_root(&self.root_path(), directory) {
            return Err("resource directory is outside the workspace".into());
        }
        let Some(scanner) = self.scanner() else {
            return Err("workspace is not initialized".into());
        };
        Ok(scanner.resource_paths(directory, limit)?)
    }
}
