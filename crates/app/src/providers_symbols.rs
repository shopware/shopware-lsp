//! Workspace/document symbols and document-level editor providers.

use std::sync::Arc;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_symbol_and_document_providers(server: &Registrar<'_>, services: &Services) {
    let admin = services
        .admin
        .clone()
        .filter(|_| server.domain_enabled("administration"));
    #[cfg(feature = "symbol")]
    {
        use swlsp_lsp::WorkspaceSymbolProvider;
        use swlsp_lsp_symbol::{
            AdminWorkspaceSymbolProvider, CatalogWorkspaceSymbolProvider,
            DalWorkspaceSymbolProvider, SymfonyWorkspaceSymbolProvider,
        };
        let symfony_symbols: Arc<dyn WorkspaceSymbolProvider> =
            Arc::new(SymfonyWorkspaceSymbolProvider::new(
                Some(services.services.clone()),
                Some(services.routes.clone()),
                Some(services.console.clone()),
                Some(services.twig.clone()),
                Some(services.twig_components.clone()),
                Some(services.translations.clone()),
                Some(services.php.clone()),
            ));
        let dal_symbols: Arc<dyn WorkspaceSymbolProvider> =
            Arc::new(DalWorkspaceSymbolProvider::new(Some(services.dal.clone())));
        let catalog_provider = |fallbacks: Vec<Arc<dyn WorkspaceSymbolProvider>>| {
            let mut provider =
                CatalogWorkspaceSymbolProvider::new(Some(services.symbols.clone()), fallbacks);
            if server.framework_presentation() {
                provider.exclude_domains(["php"]);
            }
            Arc::new(provider)
        };
        let fallbacks = match &admin {
            Some(admin) => {
                let admin_symbols: Arc<dyn WorkspaceSymbolProvider> =
                    Arc::new(AdminWorkspaceSymbolProvider::new(Some(admin.clone())));
                vec![symfony_symbols, admin_symbols, dal_symbols]
            }
            None => vec![symfony_symbols, dal_symbols],
        };
        server.workspace_symbol(catalog_provider(fallbacks));
    }
    // The workspace symbol provider is registered first; it stays
    // unregistered while the `symbol` feature is off.
    if let Some(admin) = &admin {
        #[cfg(feature = "symbol")]
        server.document_symbol(Arc::new(
            swlsp_lsp_symbol::AdminDocumentSymbolProvider::new(Some(admin.clone())),
        ));
        #[cfg(feature = "highlight")]
        server.document_highlight(Arc::new(
            swlsp_lsp_highlight::AdminDocumentHighlightProvider::new(Some(admin.clone())),
        ));
        server.call_hierarchy(Arc::new(
            swlsp_lsp_callhierarchy::AdminCallHierarchyProvider::new(admin.clone()),
        ));
        server.linked_editing(Arc::new(
            swlsp_lsp_linkedediting::AdminLinkedEditingProvider::new(),
        ));
        server.folding_range(Arc::new(swlsp_lsp_folding::AdminFoldingProvider::new()));
        server.selection_range(Arc::new(
            swlsp_lsp_selection::AdminSelectionRangeProvider::new(),
        ));
    }
    if server.domain_enabled("scss") && !server.framework_presentation() {
        server.document_color(Arc::new(swlsp_lsp_color::AdminScssColorProvider::new()));
    }
    server.document_link(Arc::new(swlsp_lsp_documentlink::RelatedProvider::new(
        Some(services.twig.clone()),
        Some(services.configuration.clone()),
        Some(services.php.clone()),
    )));
}
