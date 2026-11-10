//! The domains of the server and what the composition root adds itself.

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Registration, Services};
use swlsp_lsp::WorkspaceSymbolProvider;

use crate::precedence::PRECEDENCE;

/// Every domain. The order only decides between providers that
/// [`PRECEDENCE`] does not list.
pub const DOMAINS: &[Domain] = &[
    swlsp_ide_php::DOMAIN,
    swlsp_ide_symfony::DOMAIN,
    swlsp_ide_twig::DOMAIN,
    swlsp_ide_style::DOMAIN,
    swlsp_ide_admin::DOMAIN,
    swlsp_ide_shopware::DOMAIN,
];

/// Lets every domain register its providers and inspections, then applies
/// them to the server in precedence order.
pub(crate) fn register_features(
    registrar: Registrar<'_>,
    services: &Services,
) -> Vec<Registration> {
    for domain in DOMAINS {
        (domain.register)(&registrar, services);
    }
    registrar.command(Arc::new(crate::integration::Provider));
    register_workspace_symbols(&registrar, services);
    registrar.finish(PRECEDENCE)
}

/// The one workspace symbol provider: the symbol catalog, falling back to
/// the sources the domains contributed for what the catalog does not hold.
fn register_workspace_symbols(registrar: &Registrar<'_>, services: &Services) {
    let sources: Vec<Arc<dyn WorkspaceSymbolProvider>> =
        registrar.take_workspace_symbol_sources(PRECEDENCE);
    let mut catalog = swlsp_ide::symbol::CatalogWorkspaceSymbolProvider::new(
        Some(services.symbols.clone()),
        sources,
    );
    if registrar.framework_presentation() {
        catalog.exclude_domains(["php"]);
    }
    registrar.workspace_symbol(Arc::new(catalog));
}
