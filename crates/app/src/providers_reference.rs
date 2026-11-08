//! Reference and file-rename providers (port of Go
//! `internal/app/providers_reference.go`).

use std::sync::Arc;

use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_reference_providers(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    services: &Services,
) {
    if !server.framework_presentation() {
        server.references(php_features.clone());
    }
    #[cfg(feature = "reference")]
    register_domain_reference_providers(server, services);
    server.file_rename(Arc::new(
        swlsp_lsp_refactor::TwigTemplateRenameProvider::new(Some(services.twig.clone())),
    ));
}

#[cfg(feature = "reference")]
fn register_domain_reference_providers(server: &Registrar<'_>, s: &Services) {
    use swlsp_lsp_reference::*;

    let php = || Some(s.php.clone());
    server.references(Arc::new(DoctrineReferenceProvider::new(
        Some(s.doctrine.clone()),
        php(),
    )));
    server.references(Arc::new(ControllerReferenceProvider::new(
        Some(s.route_usage.clone()),
        Some(s.services.clone()),
        php(),
    )));
    server.references(Arc::new(RouteReferenceProvider::new(
        s.routes.clone(),
        s.route_usage.clone(),
        php(),
    )));
    server.references(Arc::new(EventReferenceProvider::new(
        Some(s.events.clone()),
        php(),
    )));
    server.references(Arc::new(MessengerReferenceProvider::new(
        Some(s.messenger.clone()),
        php(),
    )));
    server.references(Arc::new(EnvironmentReferenceProvider::new(Some(
        s.environment.clone(),
    ))));
    server.references(Arc::new(SerializerReferenceProvider::new(
        Some(s.serializer.clone()),
        php(),
    )));
    server.references(Arc::new(AssetReferenceProvider::new(
        Some(s.assets.clone()),
        php(),
    )));
    if server.domain_enabled("scss") {
        server.references(Arc::new(StyleClassReferenceProvider::new(s.styles.clone())));
    }
    server.references(Arc::new(TwigMacroReferenceProvider::new(Some(
        s.twig.clone(),
    ))));
    server.references(Arc::new(TwigTemplateReferenceProvider::new(Some(
        s.twig.clone(),
    ))));
    server.references(Arc::new(TwigConstantReferenceProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.references(Arc::new(TwigPhpReferenceProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.references(Arc::new(TwigComponentReferenceProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.references(Arc::new(LiveComponentEventReferenceProvider::new(Some(
        s.twig_components.clone(),
    ))));
    if server.domain_enabled("administration")
        && let Some(admin) = &s.admin
    {
        server.references(Arc::new(AdminReferenceProvider::new(Some(admin.clone()))));
    }
}
