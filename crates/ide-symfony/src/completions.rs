//! Symfony completion.

use std::sync::Arc;

use crate::completion::*;
use swlsp_ide::{Registrar, Services};
use swlsp_symfony::IndexedResourcePaths;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    server.completion(Arc::new(ResponseConstantCompletionProvider::new(php())));
    server.completion(Arc::new(HttpClientCompletionProvider::new(php())));
    server.completion(Arc::new(ConsoleHelperCompletionProvider::new(php())));
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
        php(),
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
}
