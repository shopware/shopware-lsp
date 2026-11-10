//! The PHP language domain: semantic features of plain PHP code,
//! independent of any framework.

pub mod codeaction;
mod context;
mod diagnostics;
pub mod folding;
pub mod inlay;
pub mod inspections;
pub mod selection;
pub mod semantic;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};
use swlsp_language::LanguageId;
use swlsp_lsp::{Context, SyntaxContext};

pub use context::enrich_php_context;

pub const DOMAIN: Domain = Domain {
    id: "php",
    register,
};

fn register(server: &Registrar<'_>, s: &Services) {
    let php = s.php.clone();
    server.context_enricher(
        LanguageId::Php,
        Arc::new(move |ctx: &Context, syntax: &SyntaxContext| {
            enrich_php_context(ctx, &php, syntax)
        }),
    );
    server.suppressions(LanguageId::Php, swlsp_lsp_phpanalysis::suppression_reader());

    server.inlay_hint(Arc::new(crate::inlay::PhpUnitProviderProvider::new(
        s.php(),
    )));
    diagnostics::register(server, s);

    // A framework presentation shows the framework's features only: the
    // editor's own PHP support serves the language.
    if server.framework_presentation() {
        return;
    }
    let features = &s.php_features;
    server.completion(features.clone());
    server.definition(features.clone());
    server.implementation(features.clone());
    server.type_hierarchy(features.clone());
    server.references(features.clone());
    server.hover(features.clone());
    server.document_symbol(features.clone());
    server.document_highlight(features.clone());
    server.signature_help(features.clone());
    server.folding_range(Arc::new(crate::folding::PhpFoldingProvider::new()));
    server.selection_range(Arc::new(crate::selection::PhpSelectionRangeProvider::new()));
    server.semantic_tokens(Arc::new(crate::semantic::EmbeddedLanguageProvider::new(
        s.php(),
    )));
    if server.domain_enabled("php") {
        server.action(Arc::new(codeaction::PhpImportsProvider::new(s.php())));
        server.action(Arc::new(codeaction::PhpExtractVariableProvider::new(
            s.php(),
        )));
        server.action(Arc::new(codeaction::PhpExtractMethodProvider::new(s.php())));
    }
}
