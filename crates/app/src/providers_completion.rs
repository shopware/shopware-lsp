//! Completion providers.

use std::sync::Arc;

use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_completion_providers(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    services: &Services,
) {
    if !server.framework_presentation() {
        server.completion(php_features.clone());
    }
    #[cfg(feature = "completion")]
    register_domain_completion_providers(server, services);
    #[cfg(not(feature = "completion"))]
    let _ = services;
}

#[cfg(feature = "completion")]
fn register_domain_completion_providers(server: &Registrar<'_>, s: &Services) {
    use swlsp_lsp_completion::*;
    use swlsp_symfony::IndexedResourcePaths;

    let php = || Some(s.php.clone());
    server.completion(Arc::new(PhpAttributeCompletionProvider::new(php())));
    server.completion(Arc::new(ResponseConstantCompletionProvider::new(Some(
        s.php.clone(),
    ))));
    server.completion(Arc::new(HttpClientCompletionProvider::new(Some(
        s.php.clone(),
    ))));
    server.completion(Arc::new(ContainerConstantCompletionProvider::new(php())));
    server.completion(Arc::new(ConsoleHelperCompletionProvider::new(Some(
        s.php.clone(),
    ))));
    server.completion(Arc::new(ConsoleCompletionProvider::new(
        s.console.clone(),
        php(),
    )));
    server.completion(Arc::new(DoctrineCompletionProvider::new(
        s.doctrine.clone(),
        php(),
    )));
    server.completion(Arc::new(AssetCompletionProvider::new(
        s.assets.clone(),
        php(),
    )));
    server.completion(Arc::new(EventCompletionProvider::new(
        s.events.clone(),
        php(),
        Some(s.services.clone()),
    )));
    server.completion(Arc::new(MessengerCompletionProvider::new(
        Some(s.php.clone()),
        Some(s.messenger.clone()),
    )));
    server.completion(Arc::new(EnvironmentCompletionProvider::new(
        s.environment.clone(),
    )));
    server.completion(Arc::new(SymfonyConfigCompletionProvider::new(
        s.configuration.clone(),
    )));
    server.completion(Arc::new(YamlServiceAuthoringCompletionProvider::new(
        s.php.project(),
    )));
    server.completion(Arc::new(YamlRouteAuthoringCompletionProvider::new(Some(
        s.routes.clone(),
    ))));
    server.completion(Arc::new(ValidationCompletionProvider::new(php())));
    server.completion(Arc::new(ServiceCompletionProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.completion(Arc::new(TranslationCompletionProvider::new(
        Some(s.translations.clone()),
        php(),
    )));
    server.completion(Arc::new(TwigMacroCompletionProvider::new(Some(
        s.twig.clone(),
    ))));
    server.completion(Arc::new(TwigEnumCompletionProvider::new(php())));
    server.completion(Arc::new(TwigConstantCompletionProvider::new(
        php(),
        Some(s.twig.clone()),
    )));
    server.completion(Arc::new(TwigComponentCompletionProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.completion(Arc::new(TwigComponentVariableCompletionProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.completion(Arc::new(TwigComponentPhpCompletionProvider::new()));
    server.completion(Arc::new(LiveComponentEventCompletionProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.completion(Arc::new(TwigIncludeParameterCompletionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.completion(Arc::new(TwigRenderBlockCompletionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    let mut twig_completion = TwigCompletionProvider::new(
        s.root.clone(),
        Some(s.twig.clone()),
        Some(s.extensions.clone()),
        php(),
    );
    if server.framework_presentation() {
        twig_completion = twig_completion.without_builtin_twig_completions();
    }
    server.completion(Arc::new(twig_completion));
    server.completion(Arc::new(ControllerCompletionProvider::new(
        php(),
        Some(s.services.clone()),
        Some(s.routes.clone()),
    )));
    server.completion(Arc::new(RouteCompletionProvider::new(
        s.routes.clone(),
        php(),
    )));
    let paths: Arc<dyn IndexedResourcePaths> = s.paths.clone();
    server.completion(Arc::new(BundleResourceCompletionProvider::new(
        php(),
        Some(paths),
    )));
    server.completion(Arc::new(SnippetCompletionProvider::new(Some(
        s.snippets.clone(),
    ))));
    server.completion(Arc::new(FeatureCompletionProvider::new(s.features.clone())));
    server.completion(Arc::new(DalCompletionProvider::new(s.dal.clone())));
    server.completion(Arc::new(SystemConfigCompletionProvider::new(
        s.system_config.clone(),
        Some(s.php.clone()),
    )));
    server.completion(Arc::new(ThemeCompletionProvider::new(s.theme.clone())));
    if server.domain_enabled("administration")
        && let Some(admin) = &s.admin
    {
        server.completion(Arc::new(AdminCompletionProvider::new(admin.clone())));
    }
}
