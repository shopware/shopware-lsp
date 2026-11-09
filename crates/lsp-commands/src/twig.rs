//! `shopware/twig/extendBlock` and `shopware/twig/getBlockDiff`: add an
//! override block to an extension's Storefront template, and diff an
//! overridden core block against the version recorded in its versioning
//! comment.

use std::collections::BTreeMap;
use std::process::Command;
use std::sync::Arc;

use serde::Deserialize;
use serde_json::{Value, json};
use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};
use swlsp_lsp_protocol::new_lsp_error;
use swlsp_rewrite::WorkspacePlan;
use swlsp_twig::{VersioningService, format_version_comment, is_storefront_template, parse_twig};
use swlsp_uriutil::{file_uri, filepath};

use crate::{EditHost, add_replacement, decode_args, target_snapshot};

/// Twig template commands.
pub struct TwigCommandProvider {
    inner: Arc<Inner>,
}

struct Inner {
    extensions: Option<Arc<ExtensionIndexer>>,
    versioning: Option<Arc<VersioningService>>,
    project_root: String,
    host: Arc<dyn EditHost>,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct ExtendParams {
    #[serde(rename = "textUri")]
    text_uri: String,
    #[serde(rename = "blockName")]
    block_name: String,
    extension: String,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct DiffParams {
    #[serde(rename = "textUri")]
    text_uri: String,
    #[serde(rename = "blockName")]
    block_name: String,
}

/// Why the historical content of a core block could not be loaded.
#[derive(Debug, thiserror::Error)]
enum BlockHistoryError {
    #[error("storefront package not found")]
    StorefrontNotFound,
    #[error("block {block} not found at version {version}")]
    BlockNotFound { block: String, version: String },
    #[error(transparent)]
    Fetch(#[from] FetchError),
}

/// Why `git show` could not provide a file.
#[derive(Debug, thiserror::Error)]
enum GitShowError {
    #[error("run git show")]
    Spawn(#[source] std::io::Error),
    #[error("git show failed: {stderr}")]
    Failed { stderr: String },
}

/// Why a Storefront file could not be fetched from GitHub.
#[derive(Debug, thiserror::Error)]
enum FetchError {
    #[error("configure TLS")]
    Tls(#[source] rustls::Error),
    #[error("create HTTP client")]
    Client(#[source] reqwest::Error),
    #[error("failed to fetch from GitHub")]
    Request(#[source] reqwest::Error),
    #[error("GitHub returned status {status} for version {version}")]
    Status { status: u16, version: String },
    #[error("failed to read GitHub response")]
    Body(#[source] reqwest::Error),
}

fn lsp_error(message: impl Into<String>, code: &str) -> Result<Value, BoxError> {
    crate::to_value(&new_lsp_error(message, code))
}

impl TwigCommandProvider {
    pub fn new(
        project_root: &str,
        extensions: Option<Arc<ExtensionIndexer>>,
        versioning: Option<Arc<VersioningService>>,
        host: Arc<dyn EditHost>,
    ) -> Self {
        Self {
            inner: Arc::new(Inner {
                extensions,
                versioning,
                project_root: project_root.to_owned(),
                host,
            }),
        }
    }

    /// Runs `shopware/twig/extendBlock` (test access).
    pub fn extend_block(&self, ctx: &Context, args: Option<&Value>) -> Result<Value, BoxError> {
        self.inner.extend_block(ctx, args)
    }

    /// Runs `shopware/twig/getBlockDiff` (test access).
    pub fn get_block_diff(&self, ctx: &Context, args: Option<&Value>) -> Result<Value, BoxError> {
        self.inner.get_block_diff(ctx, args)
    }
}

impl CommandProvider for TwigCommandProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let extend = self.inner.clone();
        let diff = self.inner.clone();
        let extend: CommandFunc = Arc::new(move |ctx, args| extend.extend_block(ctx, args));
        let diff: CommandFunc = Arc::new(move |ctx, args| diff.get_block_diff(ctx, args));
        BTreeMap::from([
            ("shopware/twig/extendBlock".to_owned(), extend),
            ("shopware/twig/getBlockDiff".to_owned(), diff),
        ])
    }
}

impl Inner {
    fn extend_block(&self, ctx: &Context, args: Option<&Value>) -> Result<Value, BoxError> {
        let params: ExtendParams = decode_args(args)?;
        let Some(extensions) = &self.extensions else {
            return Err("find extension: extension index is unavailable".into());
        };
        let extension = extensions
            .find_by_name(&params.extension)
            .map_err(|error| format!("find extension: {error}"))?;
        let Some(extension) = extension else {
            return lsp_error("Extension not found", "extension.not_found");
        };
        let original_path = swlsp_uriutil::path(&params.text_uri)
            .map_err(|error| format!("resolve document URI: {error}"))?;
        let Some(resources_index) = original_path.find("Resources/views/storefront") else {
            return lsp_error("Not a storefront view file", "view.not_storefront");
        };
        let storefront_relative = &original_path[resources_index + 16..];
        let view_path = filepath::join(&[
            extension.storefront_views_path().as_str(),
            storefront_relative,
        ]);
        let uri = file_uri(&view_path);
        let snapshot = target_snapshot(ctx, self.host.as_ref(), &uri)?;
        let mut content = match &snapshot {
            Some(snapshot) => snapshot.document.source().to_owned(),
            None => format!("{{% sw_extends \"@Storefront/{storefront_relative}\" %}}\n"),
        };
        let file = parse_twig(&view_path, &content);
        if file.blocks.contains_key(&params.block_name) {
            return lsp_error("Block already exists", "block.already_exists");
        }
        let mut version_comment = String::new();
        if let Some(versioning) = &self.versioning
            && let Ok(Some(original)) = versioning.block_at_path(&params.block_name, &original_path)
        {
            version_comment = format_version_comment(
                &original.hash,
                &versioning.version_for_path(&original.absolute_path),
            );
        }
        content.push_str(&format!(
            "\n\n{version_comment}{{% block {} %}}\n\n{{% endblock %}}\n",
            params.block_name
        ));
        let mut plan = WorkspacePlan::default();
        add_replacement(&mut plan, &uri, snapshot.as_ref(), &content)?;
        let edit = self.host.workspace_edit(ctx, &plan)?;
        let file = parse_twig(&view_path, &content);
        let Some(block) = file.blocks.get(&params.block_name) else {
            return lsp_error("Block not found after creation", "block.not_found");
        };
        Ok(json!({
            "uri": file_uri(&view_path),
            "line": block.line,
            "edit": edit,
        }))
    }

    fn get_block_diff(&self, _ctx: &Context, args: Option<&Value>) -> Result<Value, BoxError> {
        let params: DiffParams = decode_args(args)?;
        let Some(versioning) = &self.versioning else {
            return lsp_error("Twig versioning not available", "indexer.not_available");
        };
        let file_path = swlsp_uriutil::path(&params.text_uri)
            .map_err(|error| format!("resolve document URI: {error}"))?;
        let Ok(content) = std::fs::read(&file_path) else {
            return lsp_error("Failed to read override file", "file.read_failed");
        };
        let file = parse_twig(&file_path, &String::from_utf8_lossy(&content));
        let Some(block) = file.blocks.get(&params.block_name) else {
            return lsp_error("Block not found in override file", "block.not_found");
        };
        let Some(version_comment) = &block.version_comment else {
            return lsp_error("No version comment found for block", "version.not_found");
        };
        if version_comment.version.is_empty() {
            return lsp_error(
                "The versioning comment has no historical package version",
                "version.not_found",
            );
        }
        let Ok(resolution) = versioning.resolve(&file, &params.block_name) else {
            return lsp_error("Failed to resolve upstream block", "block.hash_failed");
        };
        let Some(current) = resolution
            .candidates
            .first()
            .filter(|candidate| is_storefront_template(&candidate.absolute_path))
        else {
            return lsp_error(
                "A historical diff is only available for Shopware core blocks",
                "block.diff_unavailable",
            );
        };
        let original = match self.block_content_at_version(
            &current.absolute_path,
            &params.block_name,
            &version_comment.version,
        ) {
            Ok(original) => original,
            Err(error) => {
                return lsp_error(
                    format!(
                        "Failed to get block at version {}: {}",
                        version_comment.version,
                        error_chain(&error)
                    ),
                    "git.failed",
                );
            }
        };
        Ok(json!({
            "blockName": params.block_name,
            "originalContent": original,
            "originalVersion": version_comment.version,
            "currentContent": current.text,
            "currentVersion": versioning.version_for_path(&current.absolute_path),
        }))
    }

    fn block_content_at_version(
        &self,
        absolute_path: &str,
        block_name: &str,
        version: &str,
    ) -> Result<String, BlockHistoryError> {
        let storefront = self.storefront_package_path();
        if storefront.is_empty() {
            return Err(BlockHistoryError::StorefrontNotFound);
        }
        let relative = absolute_path
            .strip_prefix(storefront.as_str())
            .unwrap_or(absolute_path);
        let relative = relative.strip_prefix('/').unwrap_or(relative);
        let git_ref = if version.starts_with('v') {
            version.to_owned()
        } else {
            format!("v{version}")
        };
        let mut repository_path = filepath::to_slash(absolute_path);
        if let Ok(rel) = filepath::rel(&self.project_root, absolute_path)
            && !rel.starts_with("../")
            && rel != ".."
        {
            repository_path = filepath::to_slash(&rel);
        }
        let content = match git_show(&self.project_root, &repository_path, &git_ref) {
            Ok(content) => content,
            Err(_) => github_content(relative, &git_ref)?,
        };
        let file = parse_twig(relative, &content);
        let Some(block) = file.blocks.get(block_name) else {
            return Err(BlockHistoryError::BlockNotFound {
                block: block_name.to_owned(),
                version: version.to_owned(),
            });
        };
        Ok(block.text.clone())
    }

    fn storefront_package_path(&self) -> String {
        let root = self.project_root.as_str();
        [
            filepath::join(&[root, "vendor", "shopware", "storefront"]),
            filepath::join(&[root, "vendor", "shopware", "platform", "src", "Storefront"]),
            filepath::join(&[root, "src", "Storefront"]),
        ]
        .into_iter()
        .find(|path| std::fs::metadata(path).is_ok())
        .unwrap_or_default()
    }
}

fn git_show(repository: &str, path: &str, git_ref: &str) -> Result<String, GitShowError> {
    let output = Command::new("git")
        .arg("show")
        .arg(format!("{git_ref}:{path}"))
        .current_dir(repository)
        .output()
        .map_err(GitShowError::Spawn)?;
    if !output.status.success() {
        return Err(GitShowError::Failed {
            stderr: String::from_utf8_lossy(&output.stderr).into_owned(),
        });
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

/// Fetches a Storefront file at `version` from the Shopware GitHub
/// repository, the fallback when the project has no git history for it.
fn github_content(relative: &str, version: &str) -> Result<String, FetchError> {
    let url = format!(
        "https://raw.githubusercontent.com/shopware/shopware/{version}/src/Storefront/{relative}"
    );
    let client = http_client()?;
    let response = client.get(&url).send().map_err(FetchError::Request)?;
    let status = response.status();
    if status != reqwest::StatusCode::OK {
        return Err(FetchError::Status {
            status: status.as_u16(),
            version: version.to_owned(),
        });
    }
    response.text().map_err(FetchError::Body)
}

/// A blocking client with a 30 second timeout that trusts the bundled
/// Mozilla roots. Must not be built or dropped on an async task.
fn http_client() -> Result<reqwest::blocking::Client, FetchError> {
    let roots = rustls::RootCertStore {
        roots: webpki_roots::TLS_SERVER_ROOTS.to_vec(),
    };
    let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(FetchError::Tls)?
    .with_root_certificates(roots)
    .with_no_client_auth();
    reqwest::blocking::Client::builder()
        .use_preconfigured_tls(tls)
        .timeout(std::time::Duration::from_secs(30))
        .build()
        .map_err(FetchError::Client)
}

/// `error` followed by each of its causes, separated by `: `.
fn error_chain(error: &dyn std::error::Error) -> String {
    let mut message = error.to_string();
    let mut source = error.source();
    while let Some(cause) = source {
        message.push_str(": ");
        message.push_str(&cause.to_string());
        source = cause.source();
    }
    message
}

#[cfg(test)]
mod github_tests {
    use super::github_content;

    /// Needs network access: `cargo test -p swlsp-lsp-commands -- --ignored`.
    #[test]
    #[ignore = "fetches from raw.githubusercontent.com"]
    fn github_content_fetches_storefront_templates_and_reports_missing_versions() {
        let content = github_content("Resources/views/storefront/base.html.twig", "v6.6.10.0")
            .expect("fetch base.html.twig");
        assert!(
            content.contains("{% block base_doctype %}"),
            "{content:.200}"
        );
        let error = github_content(
            "Resources/views/storefront/base.html.twig",
            "v0.0.0-missing",
        )
        .unwrap_err();
        assert_eq!(
            error.to_string(),
            "GitHub returned status 404 for version v0.0.0-missing"
        );
    }
}
