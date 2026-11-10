//! The Symfony domain: the service container, routing, console, events,
//! messenger, Doctrine, serializer, validation, assets and configuration.

mod actions;
pub mod analytics;
pub mod codeaction;
pub mod codelens;
pub mod commands;
pub mod completion;
mod completions;
pub mod definition;
pub mod diagnostics;
pub mod hover;
pub mod inlay;
pub mod inspections;
mod navigation;
pub mod reference;
pub mod symbol;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};
use swlsp_language::LanguageId;
use swlsp_lsp::comment_suppression_reader;

pub const DOMAIN: Domain = Domain {
    id: "symfony",
    register,
};

fn register(server: &Registrar<'_>, s: &Services) {
    server.suppressions(
        LanguageId::Yaml,
        comment_suppression_reader(&[swlsp_parser_yaml::syntax::TK_COMMENT]),
    );
    server.suppressions(
        LanguageId::Xml,
        comment_suppression_reader(&[
            swlsp_parser_xml::syntax::XML_COMMENT,
            swlsp_parser_xml::syntax::TK_COMMENT,
        ]),
    );
    completions::register(server, s);
    navigation::register(server, s);
    inspections::register(server, s);
    actions::register(server, s);

    server.inlay_hint(Arc::new(crate::inlay::ServiceArgumentProvider::new(
        Some(s.services.clone()),
        s.php(),
    )));
    server.inlay_hint(Arc::new(crate::inlay::RouteControllerProvider::new(
        Some(s.services.clone()),
        s.php(),
    )));
    server.inlay_hint(Arc::new(crate::inlay::RoutePathProvider::new(
        s.routes.clone(),
        s.php(),
    )));
    server.workspace_symbol_source(Arc::new(
        crate::symbol::SymfonyWorkspaceSymbolProvider::new(
            Some(s.services.clone()),
            Some(s.routes.clone()),
            Some(s.console.clone()),
            Some(s.twig.clone()),
            Some(s.twig_components.clone()),
            Some(s.translations.clone()),
            s.php(),
        ),
    ));
}
