//! The composition root (port of Go `internal/app`): workspace construction,
//! indexer wiring, provider and inspection registration.
//!
//! [`Workspace`] is the only place that knows the whole system. It opens the
//! shared store, constructs every domain index with its dependencies, adds
//! the indexers of enabled domains to the file scanner and registers every
//! provider, inspection, context enricher and document observer on the
//! [`swlsp_lsp::Server`]. The server installs the scanner's update callback
//! itself. Registration order, feature gating and domain gating follow Go's
//! `providers*.go` and `inspections.go` exactly; [`Workspace::registrations`]
//! records them.

mod cache;
mod inspections;
mod integration;
mod providers;
mod providers_actions;
mod providers_codelens;
mod providers_completion;
mod providers_definition;
mod providers_editor;
mod providers_extension_config;
mod providers_hover;
mod providers_reference;
mod providers_symbols;
mod registrar;
mod workspace;

use std::io::{Read, Write};

use swlsp_lsp::Server;

pub use cache::{CACHE_DIR_ENV, project_cache_folder, project_cache_folder_in, user_cache_dir};
pub use integration::{CATALOG_COMMAND, Provider as IntegrationProvider};
pub use registrar::Registration;
pub use workspace::{
    Services, Workspace, domain_for_indexer, workspace_factory, workspace_factory_in,
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
    ) -> Result<(), String> {
        self.server.start(input, output)
    }

    /// [`Application::run`] on the caller's Tokio runtime; serving also ends
    /// when `stop` completes.
    pub async fn serve(
        &self,
        input: swlsp_lsp::async_io::Input,
        output: swlsp_lsp::async_io::Output,
        stop: impl std::future::Future<Output = ()>,
    ) -> Result<(), String> {
        self.server.serve(input, output, stop).await
    }

    pub fn close(&self) -> Result<(), String> {
        self.server.close_all()
    }
}

#[cfg(test)]
mod tests;
