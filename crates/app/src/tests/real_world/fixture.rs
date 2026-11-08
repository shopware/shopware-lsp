//! Port of Go `real_world_fixture_test.go` and the workspace helpers of
//! `real_world_helpers_fixture_test.go`.

use std::path::PathBuf;
use std::time::{Duration, Instant};

use swlsp_lsp::{Context, Server};
use swlsp_php::PhpIndex;

use crate::Workspace;

/// The configured real-world checkout, or `None` (the test is skipped) when
/// the default `~/Developer/sw-trunk` does not exist.
pub fn real_world_project_root() -> Option<String> {
    if let Some(configured) = std::env::var_os("SHOPWARE_LSP_REAL_WORLD_ROOT")
        && !configured.is_empty()
    {
        let root = std::fs::canonicalize(PathBuf::from(&configured))
            .unwrap_or_else(|error| panic!("{configured:?}: {error}"));
        assert!(root.is_dir(), "{root:?} is not a directory");
        return Some(root.to_string_lossy().into_owned());
    }
    let home = std::env::var_os("HOME")?;
    let root = PathBuf::from(home).join("Developer").join("sw-trunk");
    if !root.exists() {
        eprintln!(
            "real-world checkout not found at {}; set SHOPWARE_LSP_REAL_WORLD_ROOT",
            root.display()
        );
        return None;
    }
    Some(root.to_string_lossy().into_owned())
}

/// Skips the calling test when no checkout is configured.
#[macro_export]
macro_rules! real_world_root_or_skip {
    () => {
        match $crate::tests::real_world::real_world_project_root() {
            Some(root) => root,
            None => return,
        }
    };
}

/// Builds the production workspace like Go `openRealWorldWorkspaceWithServer`:
/// a server with the default configuration and the workspace constructed
/// directly (not through `initialize`).
pub fn open_real_world_workspace_with_server(
    root: &str,
    cache_base: &std::path::Path,
) -> (Workspace, PhpIndex, Server) {
    let server = Server::new(None, root, "integration-test");
    let workspace = Workspace::new_in(&Context::background(), root, &server, cache_base)
        .expect("open real-world workspace");
    let php = workspace.services().php.clone();
    (workspace, php, server)
}

/// The expensive indexed workspace shared by the feature checks of the
/// real-world indexing test (Go `realWorldWorkspaceFixture`).
pub struct RealWorldWorkspaceFixture {
    pub root: String,
    pub ctx: Context,
    pub workspace: Workspace,
    pub php: PhpIndex,
    pub server: Server,
    pub cold_elapsed: Duration,
    /// Keeps the isolated cache alive for the fixture's lifetime.
    pub cache: tempfile::TempDir,
}

impl RealWorldWorkspaceFixture {
    /// Opens and cold-indexes the checkout in an isolated cache; `None` when
    /// no checkout is configured.
    pub fn new() -> Option<RealWorldWorkspaceFixture> {
        let root = real_world_project_root()?;
        let cache = tempfile::tempdir().expect("cache directory");
        let ctx = Context::background();
        let (workspace, php, server) = open_real_world_workspace_with_server(&root, cache.path());
        let started = Instant::now();
        workspace
            .file_scanner()
            .index_all(&ctx.token().indexer_token())
            .expect("index real-world checkout");
        Some(RealWorldWorkspaceFixture {
            root,
            ctx,
            workspace,
            php,
            server,
            cold_elapsed: started.elapsed(),
            cache,
        })
    }
}

impl Drop for RealWorldWorkspaceFixture {
    fn drop(&mut self) {
        let _ = self.workspace.close_workspace();
    }
}
