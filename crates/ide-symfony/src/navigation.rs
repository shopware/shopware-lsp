//! Symfony definitions, references, hovers and code lenses.

use std::sync::Arc;

use swlsp_ide::{Registrar, Services};

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    definitions(server, s);
    references(server, s);
    hovers(server, s);
    code_lenses(server, s);
}

fn definitions(server: &Registrar<'_>, s: &Services) {
    use crate::definition::*;

    let php = || s.php();
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
    // Service definitions from XML service references.
    server.definition(Arc::new(ServiceDefinitionProvider::new(
        Some(s.services.clone()),
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
}

fn references(server: &Registrar<'_>, s: &Services) {
    use crate::reference::*;

    let php = || s.php();
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
}

fn hovers(server: &Registrar<'_>, s: &Services) {
    use crate::hover::*;

    let php = || s.php();
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
}

fn code_lenses(server: &Registrar<'_>, s: &Services) {
    use crate::codelens::*;

    let php = || s.php();
    server.code_lens(Arc::new(PhpServiceCodeLensProvider::new(
        php(),
        Some(s.services.clone()),
    )));
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
