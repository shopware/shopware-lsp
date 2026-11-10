//! Twig code actions and commands.

use std::sync::Arc;

use crate::analytics;
use crate::codeaction;
use crate::commands;
use swlsp_ide::{Registrar, Services};
use swlsp_lsp::WorkspaceEditHost;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    let versioning = crate::versioning(server, s);
    let host: Arc<dyn WorkspaceEditHost> = Arc::new(server.server.downgrade());
    let edit_host: Arc<dyn commands::EditHost> = Arc::new(server.server.downgrade());

    server.action(Arc::new(codeaction::TwigCodeActionProvider::new(
        versioning.clone(),
    )));
    // These offer code actions and run them as commands.
    let template_generator = Arc::new(codeaction::TwigTemplateGeneratorProvider::new(Some(
        s.twig.clone(),
    )));
    server.action(template_generator.clone());
    server.command(template_generator);
    server.action(Arc::new(
        codeaction::TwigExtensionAttributeCodeActionProvider::new(php()),
    ));
    let translation_extractor = Arc::new(codeaction::TwigTranslationExtractProvider::new(
        Some(s.translations.clone()),
        Some(host),
    ));
    server.action(translation_extractor.clone());
    server.command(translation_extractor);

    server.command(Arc::new(commands::TwigCommandProvider::new(
        &s.root,
        Some(s.extensions.clone()),
        versioning,
        edit_host,
    )));
    server.command(Arc::new(analytics::TwigExtensionCatalogProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.command(Arc::new(analytics::TwigTemplateUsageCatalogProvider::new(
        &s.root,
        Some(s.twig.clone()),
        php(),
        Some(s.routes.clone()),
        Some(s.services.clone()),
        Some(s.twig_components.clone()),
    )));
    server.command(Arc::new(analytics::TwigComponentCatalogProvider::new(
        Some(s.twig_components.clone()),
    )));
    server.command(Arc::new(
        analytics::TwigTemplateVariableCatalogProvider::new(
            &s.root,
            Some(s.twig.clone()),
            php(),
            Some(s.twig_components.clone()),
        ),
    ));
}
