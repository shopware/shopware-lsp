//! Definition, implementation and type hierarchy providers.

use std::sync::Arc;

use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_definition_providers(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    services: &Services,
) {
    if !server.framework_presentation() {
        server.definition(php_features.clone());
        server.implementation(php_features.clone());
        server.type_hierarchy(php_features.clone());
    }
    #[cfg(feature = "definition")]
    register_domain_definition_providers(server, services);
    #[cfg(not(feature = "definition"))]
    let _ = services;
}

#[cfg(feature = "definition")]
fn register_domain_definition_providers(server: &Registrar<'_>, s: &Services) {
    use swlsp_lsp_definition::*;

    let php = || Some(s.php.clone());
    server.definition(Arc::new(ContainerConstantDefinitionProvider::new(php())));
    server.definition(Arc::new(HttpClientDefinitionProvider::new(php())));
    server.definition(Arc::new(ConsoleHelperDefinitionProvider::new(php())));
    server.definition(Arc::new(ConsoleDefinitionProvider::new(
        Some(s.console.clone()),
        php(),
    )));
    server.definition(Arc::new(DoctrineDefinitionProvider::new(
        Some(s.doctrine.clone()),
        php(),
    )));
    server.definition(Arc::new(AssetDefinitionProvider::new(
        Some(s.assets.clone()),
        php(),
    )));
    if server.domain_enabled("scss") {
        server.definition(Arc::new(StyleClassDefinitionProvider::new(
            s.styles.clone(),
        )));
    }
    server.definition(Arc::new(EventDefinitionProvider::new(
        Some(s.events.clone()),
        php(),
        Some(s.services.clone()),
    )));
    server.definition(Arc::new(MessengerDefinitionProvider::new(php())));
    server.definition(Arc::new(EnvironmentDefinitionProvider::new(Some(
        s.environment.clone(),
    ))));
    server.definition(Arc::new(SymfonyConfigDefinitionProvider::new(Some(
        s.configuration.clone(),
    ))));
    server.definition(Arc::new(SerializerDefinitionProvider::new(
        Some(s.serializer.clone()),
        php(),
    )));
    server.definition(Arc::new(ValidationDefinitionProvider::new(php())));
    server.definition(Arc::new(TwigEnumDefinitionProvider::new(php())));
    server.definition(Arc::new(TwigConstantDefinitionProvider::new(
        php(),
        Some(s.twig.clone()),
    )));
    // Service definitions from XML service references.
    server.definition(Arc::new(ServiceDefinitionProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.definition(Arc::new(TwigMacroDefinitionProvider::new(Some(
        s.twig.clone(),
    ))));
    server.definition(Arc::new(TwigComponentDefinitionProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.definition(Arc::new(LiveComponentEventDefinitionProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.definition(Arc::new(TwigIncludeParameterDefinitionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.definition(Arc::new(TwigRenderBlockDefinitionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.definition(Arc::new(TwigDefinitionProvider::new(
        s.root.clone(),
        Some(s.twig.clone()),
        Some(s.extensions.clone()),
        php(),
    )));
    server.definition(Arc::new(RouteDefinitionProvider::new(
        Some(s.routes.clone()),
        php(),
    )));
    server.definition(Arc::new(ControllerDefinitionProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.definition(Arc::new(TranslationDefinitionProvider::new(
        Some(s.translations.clone()),
        php(),
    )));
    server.definition(Arc::new(SnippetDefinitionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.definition(Arc::new(FeatureDefinitionProvider::new(Some(
        s.features.clone(),
    ))));
    server.definition(Arc::new(DalDefinitionProvider::new(Some(s.dal.clone()))));
    server.definition(Arc::new(SystemConfigDefinitionProvider::new(
        Some(s.system_config.clone()),
        php(),
    )));
    server.definition(Arc::new(ThemeDefinitionProvider::new(Some(
        s.theme.clone(),
    ))));
    if server.domain_enabled("administration")
        && let Some(admin) = &s.admin
    {
        server.definition(Arc::new(AdminDefinitionProvider::new(admin.clone())));
    }
}
