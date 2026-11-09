//! Code lens providers.

use crate::registrar::Registrar;
use crate::workspace::Services;

#[cfg(feature = "codelens")]
pub(crate) fn register_code_lens_providers(server: &Registrar<'_>, s: &Services) {
    use std::sync::Arc;

    use swlsp_lsp_codelens::*;

    let php = || Some(s.php.clone());
    server.code_lens(Arc::new(PhpServiceCodeLensProvider::new(
        php(),
        Some(s.services.clone()),
    )));
    if server.domain_enabled("administration")
        && let Some(admin) = &s.admin
    {
        server.code_lens(Arc::new(AdminComponentCodeLensProvider::new(Some(
            admin.clone(),
        ))));
    }
    server.code_lens(Arc::new(SymfonyConfigCodeLensProvider::new(Some(
        s.configuration.clone(),
    ))));
    server.code_lens(Arc::new(RouteResourceCodeLensProvider::new(
        Some(s.routes.clone()),
        php(),
    )));
    server.code_lens(Arc::new(RouteEndpointCodeLensProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.code_lens(Arc::new(ConsoleCommandCodeLensProvider::new(
        s.root.clone(),
    )));
    server.code_lens(Arc::new(SerializerCodeLensProvider::new(
        Some(s.serializer.clone()),
        php(),
    )));
    server.code_lens(Arc::new(ValidationCodeLensProvider::new(
        php(),
        Some(s.translations.clone()),
    )));
    server.code_lens(Arc::new(TwigCodeLensProvider::new(Some(s.twig.clone()))));
    server.code_lens(Arc::new(TwigComponentRelatedCodeLensProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.code_lens(Arc::new(RelatedNavigationCodeLensProvider::new(
        Some(s.twig.clone()),
        php(),
        Some(s.routes.clone()),
        Some(s.services.clone()),
    )));
    server.code_lens(Arc::new(ControllerRelatedCodeLensProvider::new(
        Some(s.route_usage.clone()),
        Some(s.services.clone()),
        php(),
    )));
    server.code_lens(Arc::new(ViteCodeLensProvider::new(Some(s.assets.clone()))));
    server.code_lens(Arc::new(MessengerCodeLensProvider::new(
        Some(s.messenger.clone()),
        php(),
    )));
    server.code_lens(Arc::new(ServiceRelatedCodeLensProvider::new(
        Some(s.services.clone()),
        php(),
    )));
}

#[cfg(not(feature = "codelens"))]
pub(crate) fn register_code_lens_providers(_server: &Registrar<'_>, _services: &Services) {
    // TODO(app): enable when swlsp-lsp-codelens lands.
}
