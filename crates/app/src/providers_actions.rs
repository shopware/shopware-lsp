//! Code actions and command providers.

use std::sync::Arc;

use swlsp_lsp::WorkspaceEditHost;
use swlsp_lsp_codeaction as codeaction;
use swlsp_lsp_commands as commands;
use swlsp_twig::VersioningService;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_action_and_command_providers(
    server: &Registrar<'_>,
    versioning: Option<Arc<VersioningService>>,
    s: &Services,
) {
    let host: Arc<dyn WorkspaceEditHost> = Arc::new(server.server.downgrade());
    let php = || Some(s.php.clone());
    if !server.framework_presentation() && server.domain_enabled("php") {
        server.action(Arc::new(codeaction::PhpImportsProvider::new(Some(
            s.php.clone(),
        ))));
        server.action(Arc::new(codeaction::PhpExtractVariableProvider::new(php())));
        server.action(Arc::new(codeaction::PhpExtractMethodProvider::new(Some(
            s.php.clone(),
        ))));
    }
    server.action(Arc::new(codeaction::SnippetCodeActionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.action(Arc::new(codeaction::SnippetCopyCodeActionProvider::new()));
    server.action(Arc::new(codeaction::TwigCodeActionProvider::new(
        versioning.clone(),
    )));
    let mut admin_twig_override = None;
    if server.domain_enabled("administration") {
        server.action(Arc::new(codeaction::AdminContextProvider::new()));
        let provider = Arc::new(codeaction::AdminTwigOverrideProvider::new(
            s.admin.clone(),
            Some(s.extensions.clone()),
            Some(host.clone()),
        ));
        server.action(provider.clone());
        admin_twig_override = Some(provider);
    }
    server.action(Arc::new(codeaction::EventListenerContextProvider::new(
        php(),
    )));
    server.action(Arc::new(codeaction::ServiceTagCodeActionProvider::new(
        php(),
    )));
    server.action(Arc::new(
        codeaction::XmlServiceSuggestionCodeActionProvider::new(Some(s.services.clone()), php()),
    ));
    server.action(Arc::new(
        codeaction::PropertyServiceCodeActionProvider::new(
            Some(s.php.clone()),
            Some(s.services.clone()),
        ),
    ));
    let symfony_generators = Arc::new(codeaction::SymfonyGeneratorProvider::new(
        php(),
        Some(s.services.clone()),
    ));
    server.action(symfony_generators.clone());
    let twig_template_generator = Arc::new(codeaction::TwigTemplateGeneratorProvider::new(Some(
        s.twig.clone(),
    )));
    server.action(twig_template_generator.clone());
    server.action(Arc::new(
        codeaction::InvokableCommandMigrationCodeActionProvider::new(Some(s.php.clone())),
    ));
    server.action(Arc::new(
        codeaction::CommandInvokeParameterCodeActionProvider::new(Some(s.php.clone())),
    ));
    server.action(Arc::new(codeaction::RouteAttributeCodeActionProvider::new(
        php(),
    )));
    server.action(Arc::new(
        codeaction::RouteActionParameterCodeActionProvider::new(),
    ));
    server.action(Arc::new(
        codeaction::TwigExtensionAttributeCodeActionProvider::new(php()),
    ));
    let twig_translation_extractor = Arc::new(codeaction::TwigTranslationExtractProvider::new(
        Some(s.translations.clone()),
        Some(host),
    ));
    server.action(twig_translation_extractor.clone());

    let edit_host: Arc<dyn commands::EditHost> = Arc::new(server.server.downgrade());
    server.command(Arc::new(commands::SnippetCommandProvider::new(
        Some(s.snippets.clone()),
        edit_host.clone(),
    )));
    server.command(Arc::new(crate::integration::Provider));
    server.command(Arc::new(commands::ExtensionCommandProvider::new(
        s.extensions.clone(),
    )));
    server.command(Arc::new(commands::TwigCommandProvider::new(
        &s.root,
        Some(s.extensions.clone()),
        versioning,
        edit_host,
    )));
    server.command(symfony_generators);
    server.command(twig_template_generator);
    server.command(twig_translation_extractor);
    if let Some(provider) = admin_twig_override {
        server.command(provider);
    }
    server.command(Arc::new(commands::ConsoleCatalogProvider::new(
        s.console.clone(),
        Some(&s.root),
    )));
    use swlsp_lsp_analytics as analytics;
    server.command(Arc::new(analytics::RouteCatalogProvider::new(
        &s.root,
        Some(s.routes.clone()),
        Some(s.services.clone()),
        php(),
        Some(s.twig.clone()),
    )));
    server.command(Arc::new(analytics::ServiceLocatorProvider::new(
        Some(s.services.clone()),
        php(),
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
    server.command(Arc::new(analytics::ProfilerCatalogProvider::new(
        &s.root,
        php(),
        Some(s.twig.clone()),
    )));
    let mut entity_scaffolds = swlsp_lsp_scaffold::Provider::new(
        &s.root,
        php(),
        Some(s.console.clone()),
        Some(s.dal.clone()),
    );
    entity_scaffolds.set_entity_catalog(Arc::new(entity_catalog(s)));
    server.command(Arc::new(entity_scaffolds));
}

/// The indexed entity-schema catalog shared by scaffolding and snapshot
/// diagnostics.
pub(crate) fn entity_catalog(s: &Services) -> swlsp_shopware_entityschema::IndexedCatalog {
    swlsp_shopware_entityschema::IndexedCatalog::new(
        s.php.clone(),
        Some(s.entity_schema_sources.clone()),
        Some(s.symbols.clone()),
    )
}
