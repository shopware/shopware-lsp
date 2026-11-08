//! Formatting, signature help, rename, inlay hints and semantic tokens (port
//! of Go `internal/app/providers_editor.go`).

use std::sync::Arc;

use swlsp_lsp::RenameProvider;
use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_editor_providers(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    s: &Services,
) {
    let admin = s
        .admin
        .clone()
        .filter(|_| server.domain_enabled("administration"));
    server.formatting(Arc::new(swlsp_lsp_formatting::TwigProvider::new()));
    server.signature_help(Arc::new(
        swlsp_lsp_signature::TwigMacroSignatureProvider::new(s.twig.clone()),
    ));
    if let Some(admin) = &admin {
        server.signature_help(Arc::new(swlsp_lsp_signature::AdminSignatureProvider::new(
            admin.clone(),
        )));
    }
    if !server.framework_presentation() {
        server.document_symbol(php_features.clone());
        server.document_highlight(php_features.clone());
        server.folding_range(Arc::new(swlsp_lsp_folding::PhpFoldingProvider::new()));
        server.selection_range(Arc::new(
            swlsp_lsp_selection::PhpSelectionRangeProvider::new(),
        ));
        server.signature_help(php_features.clone());
        let base: Arc<dyn RenameProvider> = php_features.clone();
        server.rename(Arc::new(swlsp_lsp_refactor::PhpTwigRenameProvider::new(
            Some(base),
            Some(s.twig.clone()),
            Some(s.php.clone()),
        )));
    }
    if let Some(admin) = &admin {
        server.rename(Arc::new(swlsp_lsp_refactor::AdminRenameProvider::new(
            Some(admin.clone()),
        )));
    }
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::ServiceArgumentProvider::new(
        Some(s.services.clone()),
        Some(s.php.clone()),
    )));
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::RouteControllerProvider::new(
        Some(s.services.clone()),
        Some(s.php.clone()),
    )));
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::RoutePathProvider::new(
        s.routes.clone(),
        Some(s.php.clone()),
    )));
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::TwigVariableProvider::new(Some(
        s.php.clone(),
    ))));
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::SnippetPreviewProvider::new(
        s.snippets.clone(),
    )));
    server.inlay_hint(Arc::new(swlsp_lsp_inlay::PhpUnitProviderProvider::new(
        Some(s.php.clone()),
    )));
    if let Some(admin) = &admin {
        server.inlay_hint(Arc::new(swlsp_lsp_inlay::AdminParameterProvider::new(
            admin.clone(),
        )));
    }
    server.semantic_tokens(Arc::new(swlsp_lsp_semantic::TwigUxToolkitProvider::new()));
    if let Some(admin) = &admin {
        server.semantic_tokens(Arc::new(swlsp_lsp_semantic::AdminMarkupProvider::new(
            admin.clone(),
        )));
    }
    if !server.framework_presentation() {
        server.semantic_tokens(Arc::new(swlsp_lsp_semantic::EmbeddedLanguageProvider::new(
            Some(s.php.clone()),
        )));
    }
}
