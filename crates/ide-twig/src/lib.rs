//! The Twig domain: templates, blocks, macros, Twig components and the PHP
//! code that renders them.

mod actions;
pub mod analytics;
pub mod codeaction;
pub mod codelens;
pub mod commands;
pub mod completion;
pub mod definition;
pub mod diagnostics;
pub mod documentlink;
pub mod formatting;
pub mod hover;
pub mod inlay;
pub mod inspections;
mod navigation;
pub mod refactor;
pub mod reference;
pub mod semantic;
pub mod signature;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};
use swlsp_language::LanguageId;
use swlsp_lsp::{RenameProvider, comment_suppression_reader};
use swlsp_twig::VersioningService;

pub const DOMAIN: Domain = Domain {
    id: "twig",
    register,
};

/// The Twig block versioning service, when its configuration domain is on.
pub(crate) fn versioning(server: &Registrar<'_>, s: &Services) -> Option<Arc<VersioningService>> {
    server
        .domain_enabled("shopware.twigVersioning")
        .then(|| s.twig_versioning.clone())
}

fn register(server: &Registrar<'_>, s: &Services) {
    server.suppressions(
        LanguageId::Twig,
        comment_suppression_reader(&[
            swlsp_parser_twig::syntax::TWIG_COMMENT,
            swlsp_parser_twig::syntax::HTML_COMMENT,
        ]),
    );
    completion(server, s);
    navigation::register(server, s);
    inspections::register(server, s);
    actions::register(server, s);

    server.formatting(Arc::new(crate::formatting::TwigProvider::new()));
    server.signature_help(Arc::new(crate::signature::TwigMacroSignatureProvider::new(
        s.twig.clone(),
    )));
    if !server.framework_presentation() {
        // Renaming a PHP symbol also renames its uses in templates.
        let base: Arc<dyn RenameProvider> = s.php_features.clone();
        server.rename(Arc::new(crate::refactor::PhpTwigRenameProvider::new(
            Some(base),
            Some(s.twig.clone()),
            s.php(),
        )));
    }
    server.file_rename(Arc::new(crate::refactor::TwigTemplateRenameProvider::new(
        Some(s.twig.clone()),
    )));
    server.inlay_hint(Arc::new(crate::inlay::TwigVariableProvider::new(s.php())));
    server.semantic_tokens(Arc::new(crate::semantic::TwigUxToolkitProvider::new()));
    server.document_link(Arc::new(crate::documentlink::RelatedProvider::new(
        Some(s.twig.clone()),
        Some(s.configuration.clone()),
        s.php(),
    )));
}

fn completion(server: &Registrar<'_>, s: &Services) {
    use crate::completion::*;

    let php = || s.php();
    server.completion(Arc::new(TwigMacroCompletionProvider::new(Some(
        s.twig.clone(),
    ))));
    server.completion(Arc::new(TwigEnumCompletionProvider::new(php())));
    // Attribute arguments naming Twig components, and container constants
    // in templates.
    server.completion(Arc::new(PhpAttributeCompletionProvider::new(php())));
    server.completion(Arc::new(ContainerConstantCompletionProvider::new(php())));
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
    let mut twig = TwigCompletionProvider::new(
        s.root.clone(),
        Some(s.twig.clone()),
        Some(s.extensions.clone()),
        php(),
    );
    if server.framework_presentation() {
        twig = twig.without_builtin_twig_completions();
    }
    server.completion(Arc::new(twig));
}
