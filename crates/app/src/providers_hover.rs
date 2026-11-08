//! Hover providers (port of Go `internal/app/providers_hover.go`).

use std::sync::Arc;

use swlsp_lsp_phpsemantic::Provider as PhpFeatures;
use swlsp_twig::VersioningService;

use crate::registrar::Registrar;
use crate::workspace::Services;

/// Registers hover providers and returns the Twig versioning service when
/// the `shopware.twigVersioning` domain is enabled; actions and commands
/// share that decision.
pub(crate) fn register_hover_providers(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    services: &Services,
) -> Option<Arc<VersioningService>> {
    if !server.framework_presentation() {
        server.hover(php_features.clone());
    }
    #[cfg(feature = "hover")]
    register_domain_hover_providers(server, services);
    let versioning = server
        .domain_enabled("shopware.twigVersioning")
        .then(|| services.twig_versioning.clone());
    #[cfg(feature = "hover")]
    if let Some(versioning) = &versioning {
        server.hover(Arc::new(swlsp_lsp_hover::TwigVersioningHoverProvider::new(
            Some(versioning.clone()),
        )));
    }
    #[cfg(feature = "hover")]
    if server.domain_enabled("administration")
        && let Some(admin) = &services.admin
    {
        server.hover(Arc::new(swlsp_lsp_hover::AdminHoverProvider::new(
            services.root.clone(),
            admin.clone(),
        )));
    }
    versioning
}

#[cfg(feature = "hover")]
fn register_domain_hover_providers(server: &Registrar<'_>, s: &Services) {
    use swlsp_lsp_hover::*;

    let php = || Some(s.php.clone());
    let root = || s.root.clone();
    server.hover(Arc::new(HttpClientHoverProvider::new(php())));
    server.hover(Arc::new(ConsoleHelperHoverProvider::new(php())));
    server.hover(Arc::new(ConsoleHoverProvider::new(
        root(),
        Some(s.console.clone()),
        php(),
    )));
    server.hover(Arc::new(DoctrineHoverProvider::new(
        Some(s.doctrine.clone()),
        php(),
    )));
    server.hover(Arc::new(AssetHoverProvider::new(
        root(),
        Some(s.assets.clone()),
        php(),
    )));
    server.hover(Arc::new(EventHoverProvider::new(
        root(),
        Some(s.events.clone()),
        php(),
        Some(s.services.clone()),
    )));
    server.hover(Arc::new(MessengerHoverProvider::new(
        php(),
        Some(s.messenger.clone()),
    )));
    server.hover(Arc::new(EnvironmentHoverProvider::new(
        root(),
        Some(s.environment.clone()),
    )));
    server.hover(Arc::new(SerializerHoverProvider::new(
        Some(s.serializer.clone()),
        php(),
    )));
    server.hover(Arc::new(ValidationHoverProvider::new(php())));
    server.hover(Arc::new(TwigEnumHoverProvider::new(php())));
    server.hover(Arc::new(TwigConstantHoverProvider::new(
        php(),
        Some(s.twig.clone()),
    )));
    server.hover(Arc::new(RouteHoverProvider::new(
        Some(s.routes.clone()),
        php(),
    )));
    server.hover(Arc::new(ControllerHoverProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.hover(Arc::new(TranslationHoverProvider::new(
        root(),
        Some(s.translations.clone()),
        php(),
    )));
    server.hover(Arc::new(TwigMacroHoverProvider::new(
        root(),
        Some(s.twig.clone()),
    )));
    server.hover(Arc::new(TwigComponentHoverProvider::new(
        root(),
        Some(s.twig_components.clone()),
    )));
    server.hover(Arc::new(LiveComponentEventHoverProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.hover(Arc::new(TwigIncludeParameterHoverProvider::new(
        root(),
        Some(s.twig.clone()),
        php(),
    )));
    server.hover(Arc::new(TwigRenderBlockHoverProvider::new(
        root(),
        Some(s.twig.clone()),
        php(),
    )));
    server.hover(Arc::new(TwigHoverProvider::new(
        root(),
        Some(s.extensions.clone()),
        php(),
        Some(s.twig.clone()),
    )));
    server.hover(Arc::new(SnippetHoverProvider::new(
        root(),
        s.snippets.clone(),
    )));
    server.hover(Arc::new(DalHoverProvider::new(Some(s.dal.clone()))));
}
