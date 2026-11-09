//! Live extension configuration overlays.

use std::sync::Arc;

use swlsp_extension::{ConfigIndex, configuration_root};
use swlsp_lsp::{DocumentObserver, TextDocument};
use swlsp_uriutil::filepath;

use crate::registrar::Registrar;

pub(crate) fn register_extension_config_document_observer(
    server: &Registrar<'_>,
    compliance: &Arc<ConfigIndex>,
) {
    // Weak handles: these callbacks are stored inside the server itself.
    let lsp = server.server.downgrade();
    compliance.set_on_change(Some(Arc::new(move |configuration: &str| {
        let Some(lsp) = lsp.upgrade() else {
            return;
        };
        let root = configuration_root(configuration);
        lsp.refresh_open_document_diagnostics(|document: &TextDocument| {
            let Ok(path) = swlsp_uriutil::path(&document.uri) else {
                return false;
            };
            filepath::base(&path) == "composer.json"
                && (configuration.is_empty()
                    || root.as_deref() == Some(filepath::dir(&path).as_str()))
        });
    })));
    let open_lsp = server.server.downgrade();
    let open_index = compliance.clone();
    let close_index = compliance.clone();
    server.document_observer(
        "extensionConfiguration",
        DocumentObserver {
            did_open_or_change: Some(Arc::new(move |document: &Arc<TextDocument>| {
                if open_lsp
                    .upgrade()
                    .is_none_or(|lsp| lsp.initialization_options().cli_mode)
                {
                    return;
                }
                let Ok(path) = swlsp_uriutil::path(&document.uri) else {
                    return;
                };
                if !ConfigIndex::is_configuration(&path) {
                    return;
                }
                // Configuration files are YAML and always carry a tree.
                if let Some(root) = document.root() {
                    open_index.update_live_document(&path, &root);
                }
            })),
            did_close: Some(Arc::new(move |uri: &str| {
                if let Ok(path) = swlsp_uriutil::path(uri)
                    && ConfigIndex::is_configuration(&path)
                {
                    close_index.remove_live_document(&path);
                }
            })),
        },
    );
}
