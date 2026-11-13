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
    server.rule_help(
        "scss",
        &[
            swlsp_lsp::RuleHelp {
                code: "scss.variable.unknown",
                fix: "Define the variable, or correct its name to one the workspace or a Shopware \
                  theme provides. A variable of a theme field must be declared in the theme's \
                  theme.json.",
                exceptions: "Module variables (math.$pi) and named arguments are not checked. A \
                         variable bound anywhere in the file (a parameter, a loop variable) \
                         counts as defined. Nothing is reported until indexing has finished.",
                opt_out: "",
            },
            swlsp_lsp::RuleHelp {
                code: "scss.variable.prefer-css-variable",
                fix: "Read the custom property: var(--name). The Administration emits one for \
                      every variable of its variables.scss, and it follows runtime changes \
                      such as themes. A quick fix replaces the variable.",
                exceptions: "Only a variable of the Administration's variables.scss standing on \
                             its own in a declaration value is reported, from Shopware 6.5 on. \
                             A variable that is computed with, interpolated, passed to a \
                             function or mixin, used in a query or bound in the file itself is \
                             left alone: Sass needs its value at build time.",
                opt_out: "",
            },
            swlsp_lsp::RuleHelp {
                code: "scss.import.extension",
                fix: "Import the resource without .scss: @import \"~scss/variables\". A quick fix \
                  removes the extension.",
                exceptions: "Only @import of a path starting with ~scss in Administration files \
                         is checked.",
                opt_out: "",
            },
        ],
    );
    server.inspection(inspections::new_scss_import());
    server.inspection(inspections::new_scss_css_variable(
        s.styles.clone(),
        s.shopware_version,
    ));
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
