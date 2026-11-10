//! The Shopware domain: the data abstraction layer, snippets, feature
//! flags, system configuration, themes, extensions and app scripts.

pub mod codeaction;
pub mod commands;
pub mod completion;
pub mod definition;
pub mod diagnostics;
mod extension_config;
pub mod hover;
pub mod inlay;
pub mod inspections;
pub mod scaffold;
pub mod symbol;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};

pub use extension_config::register_extension_config_document_observer;

pub const DOMAIN: Domain = Domain {
    id: "shopware",
    register,
};

fn register(server: &Registrar<'_>, s: &Services) {
    if server.domain_enabled("shopware") {
        register_extension_config_document_observer(server, &s.extension_config);
    }
    inspections::register(server, s);
    completion(server, s);
    navigation(server, s);

    server.action(Arc::new(codeaction::SnippetCodeActionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.action(Arc::new(codeaction::SnippetCopyCodeActionProvider::new()));
    let edit_host: Arc<dyn commands::EditHost> = Arc::new(server.server.downgrade());
    server.command(Arc::new(commands::SnippetCommandProvider::new(
        Some(s.snippets.clone()),
        edit_host,
    )));
    server.command(Arc::new(commands::ExtensionCommandProvider::new(
        s.extensions.clone(),
    )));
    let mut scaffolds = crate::scaffold::Provider::new(
        &s.root,
        s.php(),
        Some(s.console.clone()),
        Some(s.dal.clone()),
    );
    scaffolds.set_entity_catalog(Arc::new(s.entity_catalog()));
    server.command(Arc::new(scaffolds));

    server.inlay_hint(Arc::new(crate::inlay::SnippetPreviewProvider::new(
        s.snippets.clone(),
    )));
    server.workspace_symbol_source(Arc::new(crate::symbol::DalWorkspaceSymbolProvider::new(
        Some(s.dal.clone()),
    )));
}

fn completion(server: &Registrar<'_>, s: &Services) {
    use crate::completion::*;

    server.completion(Arc::new(SnippetCompletionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.completion(Arc::new(FeatureCompletionProvider::new(s.features.clone())));
    server.completion(Arc::new(DalCompletionProvider::new(s.dal.clone())));
    server.completion(Arc::new(SystemConfigCompletionProvider::new(
        s.system_config.clone(),
        s.php(),
    )));
    server.completion(Arc::new(ThemeCompletionProvider::new(s.theme.clone())));
}

fn navigation(server: &Registrar<'_>, s: &Services) {
    use crate::definition::*;
    use crate::hover::{DalHoverProvider, SnippetHoverProvider};

    server.definition(Arc::new(SnippetDefinitionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.definition(Arc::new(FeatureDefinitionProvider::new(Some(
        s.features.clone(),
    ))));
    server.definition(Arc::new(DalDefinitionProvider::new(Some(s.dal.clone()))));
    server.definition(Arc::new(SystemConfigDefinitionProvider::new(
        Some(s.system_config.clone()),
        s.php(),
    )));
    server.definition(Arc::new(ThemeDefinitionProvider::new(Some(
        s.theme.clone(),
    ))));
    server.hover(Arc::new(SnippetHoverProvider::new(
        s.root.clone(),
        s.snippets.clone(),
    )));
    server.hover(Arc::new(DalHoverProvider::new(Some(s.dal.clone()))));
}
