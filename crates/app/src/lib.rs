//! The composition root: workspace construction,
//! indexer wiring, provider and inspection registration.
//!
//! [`Workspace`] is the only place that knows the whole system. It opens the
//! shared store, constructs every domain index with its dependencies, adds
//! the indexers of enabled domains to the file scanner and lets every
//! domain ([`DOMAINS`], the `swlsp-ide-*` crates) register its providers,
//! inspections, context enrichers and document observers on the
//! [`swlsp_lsp::Server`]. The server installs the scanner's update callback
//! itself. The domains gate their own features; the order between their
//! providers is `precedence`. [`Workspace::registrations`] records what was
//! applied.

mod cache;
mod cache_manage;
mod domains;
mod integration;
mod precedence;
mod workspace;

use std::io::{Read, Write};

use swlsp_lsp::Server;

pub use cache::{
    CACHE_DIR_ENV, CacheDirError, PlatformCacheDirError, project_cache_folder,
    project_cache_folder_in, user_cache_dir,
};
pub use cache_manage::{
    CacheEntry, CacheState, ClearError, ClearReport, ClearScope, caches_dir, clear_caches,
    list_caches,
};
pub use domains::DOMAINS;
pub use integration::{CATALOG_COMMAND, Provider as IntegrationProvider};
pub use swlsp_ide::{Registration, Services};
pub use workspace::{
    CloseError, Workspace, WorkspaceError, domain_for_indexer, workspace_factory,
    workspace_factory_in,
};

/// Process-level options.
#[derive(Debug, Clone, Default)]
pub struct Options {
    pub allow_unsupported_project: bool,
}

/// The process-level composition root. Workspace resources are created only
/// after the LSP `initialize` request supplies a root.
pub struct Application {
    server: Server,
}

impl Application {
    pub fn new(version: &str) -> Application {
        Self::with_options(version, Options::default())
    }

    pub fn with_options(version: &str, options: Options) -> Application {
        let server = Server::new(None, "", version);
        server.configure_project_detection(true, options.allow_unsupported_project);
        server.set_workspace_factory(workspace_factory());
        Application { server }
    }

    pub fn server(&self) -> &Server {
        &self.server
    }

    /// Serves JSON-RPC until the client disconnects.
    pub fn run(
        &self,
        input: impl Read + Send + 'static,
        output: impl Write + Send + 'static,
    ) -> Result<(), swlsp_lsp::CloseError> {
        self.server.start(input, output)
    }

    /// [`Application::run`] on the caller's Tokio runtime; serving also ends
    /// when `stop` completes.
    pub async fn serve(
        &self,
        input: swlsp_lsp::async_io::Input,
        output: swlsp_lsp::async_io::Output,
        stop: impl std::future::Future<Output = ()>,
    ) -> Result<(), swlsp_lsp::CloseError> {
        self.server.serve(input, output, stop).await
    }

    pub fn close(&self) -> Result<(), swlsp_lsp::CloseError> {
        self.server.close_all()
    }
}

#[cfg(test)]
mod tests;
