//! The adapter layer from domain indexes to LSP capabilities (port of Go
//! `internal/app/providers.go`). Construction stays in `workspace.rs`;
//! protocol wiring stays here and in the `providers_*` modules.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, DocumentObserver, SyntaxContext, TextDocument};
use swlsp_lsp_phpsemantic::Provider as PhpFeatures;
use swlsp_php::PhpIndex;
use swlsp_uriutil::filepath;

use crate::registrar::Registrar;
use crate::workspace::Services;

/// Registers every provider, inspection, context enricher and document
/// observer of the workspace, in Go's order.
pub(crate) fn register_features(server: &Registrar<'_>, services: &Services) {
    if server.domain_enabled("administration") {
        register_administration_document_observer(
            server,
            services.admin.clone(),
            Some(services.paths.clone()),
        );
    }
    let php = services.php.clone();
    server.context_enricher(
        LanguageId::Php,
        Arc::new(move |ctx: &Context, syntax: &SyntaxContext| {
            enrich_php_context(ctx, &php, syntax)
        }),
    );
    let php_features = Arc::new(PhpFeatures::new(Some(services.php.clone())));

    crate::providers_symbols::register_symbol_and_document_providers(server, services);
    crate::providers_completion::register_completion_providers(server, &php_features, services);
    crate::providers_definition::register_definition_providers(server, &php_features, services);
    crate::providers_codelens::register_code_lens_providers(server, services);
    crate::providers_reference::register_reference_providers(server, &php_features, services);
    crate::inspections::register_diagnostic_inspections(server, &php_features, services);
    let versioning =
        crate::providers_hover::register_hover_providers(server, &php_features, services);
    crate::providers_editor::register_editor_providers(server, &php_features, services);
    crate::providers_actions::register_action_and_command_providers(server, versioning, services);
}

/// The request's linked PHP analysis (Go `enrichPHPContext`). Rust providers
/// read the memoized analysis from the document themselves; the enricher
/// runs it once per request up front, like Go, and publishes the state as a
/// context value.
pub(crate) fn enrich_php_context(
    ctx: &Context,
    index: &PhpIndex,
    syntax: &SyntaxContext,
) -> Context {
    let Some(document) = syntax.document.as_deref() else {
        return ctx.clone();
    };
    match swlsp_lsp_phpanalysis::for_document(index, document) {
        Ok(Some(state)) => ctx.with_value(state),
        _ => ctx.clone(),
    }
}

pub(crate) fn is_administration_path(path: &str) -> bool {
    filepath::to_slash(&filepath::clean(path)).contains("/Resources/app/administration/src/")
}

fn document_path(uri: &str) -> Option<String> {
    swlsp_uriutil::path(uri).ok()
}

pub(crate) fn register_administration_document_observer(
    server: &Registrar<'_>,
    admin: Option<Arc<swlsp_admin::AdminComponentIndexer>>,
    scanner: Option<Arc<swlsp_indexer::FileScanner>>,
) {
    let Some(admin) = admin else {
        return;
    };
    // Weak handles: these callbacks are stored inside the server itself.
    let lsp = server.server.downgrade();
    let refresh = {
        let lsp = lsp.clone();
        move || {
            let Some(lsp) = lsp.upgrade() else {
                return;
            };
            lsp.refresh_open_document_diagnostics(|document: &TextDocument| {
                document_path(&document.uri).is_some_and(|path| is_administration_path(&path))
            });
        }
    };
    let refresh = Arc::new(refresh);
    let open_admin = admin.clone();
    let open_lsp = lsp.clone();
    let open_refresh = refresh.clone();
    let close_refresh = refresh;
    let close_lsp = lsp;
    server.document_observer(
        "administration",
        DocumentObserver {
            did_open_or_change: Some(Arc::new(move |document: &Arc<TextDocument>| {
                // CLI checks run only after the scanner has brought the
                // on-disk index current. Publishing identical editor overlays
                // for every checked file invalidates Administration caches
                // and makes concurrent checks copy the entire component
                // registry repeatedly.
                if open_lsp
                    .upgrade()
                    .is_none_or(|lsp| lsp.initialization_options().cli_mode)
                {
                    return;
                }
                let Some(path) = document_path(&document.uri) else {
                    return;
                };
                if !is_administration_path(&path) {
                    return;
                }
                // A document whose text is exactly what the index was built
                // from (typically a file just opened) needs no overlay, and
                // publishing one would invalidate every Administration cache.
                // A document edited back to its saved text drops its overlay.
                if scanner.as_ref().is_some_and(|scanner| {
                    scanner.indexed_content_matches(&path, document.source().as_bytes())
                }) {
                    if open_admin.has_live_document(&path) {
                        open_admin.remove_live_document(&path);
                        open_refresh();
                    }
                    return;
                }
                let root = document.root();
                open_admin.update_live_document(
                    &path,
                    root.as_ref(),
                    document.source(),
                    Some(document.line_index()),
                );
                open_refresh();
            })),
            did_close: Some(Arc::new(move |uri: &str| {
                if close_lsp
                    .upgrade()
                    .is_none_or(|lsp| lsp.initialization_options().cli_mode)
                {
                    return;
                }
                if let Some(path) = document_path(uri)
                    && is_administration_path(&path)
                    && admin.has_live_document(&path)
                {
                    admin.remove_live_document(&path);
                    close_refresh();
                }
            })),
        },
    );
}
