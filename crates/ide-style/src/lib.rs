//! The SCSS domain: style classes and variables.

pub mod color;
pub mod definition;
pub mod diagnostics;
pub mod inspections;
pub mod reference;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};
use swlsp_language::LanguageId;
use swlsp_lsp::comment_suppression_reader;

pub const DOMAIN: Domain = Domain {
    id: "scss",
    register,
};

fn register(server: &Registrar<'_>, s: &Services) {
    server.suppressions(
        LanguageId::Scss,
        comment_suppression_reader(&[
            swlsp_parser_scss::syntax::TK_LINE_COMMENT,
            swlsp_parser_scss::syntax::TK_BLOCK_COMMENT,
        ]),
    );
    server.inspection(inspections::new_scss_variable(
        s.styles.clone(),
        Some(s.symbols.clone()),
    ));
    if !server.domain_enabled("scss") {
        return;
    }
    server.definition(Arc::new(
        crate::definition::StyleClassDefinitionProvider::new(s.styles.clone()),
    ));
    server.references(Arc::new(
        crate::reference::StyleClassReferenceProvider::new(s.styles.clone()),
    ));
    if !server.framework_presentation() {
        server.document_color(Arc::new(crate::color::AdminScssColorProvider::new()));
    }
}
