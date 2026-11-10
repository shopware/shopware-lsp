//! The Administration domain: Vue components, their templates and the
//! Administration's runtime registries.

pub mod callhierarchy;
pub mod codeaction;
pub mod codelens;
pub mod completion;
pub mod definition;
pub mod diagnostics;
pub mod folding;
pub mod highlight;
pub mod hover;
pub mod inlay;
pub mod inspections;
pub mod linkedediting;
mod observer;
pub mod refactor;
pub mod reference;
pub mod selection;
pub mod semantic;
pub mod signature;
pub mod symbol;

use std::sync::Arc;

use swlsp_ide::{Domain, Registrar, Services};
use swlsp_language::LanguageId;
use swlsp_lsp::{WorkspaceEditHost, comment_suppression_reader};

pub use observer::{is_administration_path, register_administration_document_observer};

pub const DOMAIN: Domain = Domain {
    id: "administration",
    register,
};

fn register(server: &Registrar<'_>, s: &Services) {
    server.suppressions(
        LanguageId::JavaScript,
        comment_suppression_reader(&[
            swlsp_parser_js::syntax::TK_LINE_COMMENT,
            swlsp_parser_js::syntax::TK_BLOCK_COMMENT,
        ]),
    );
    inspections::register(server, s);
    if !server.domain_enabled("administration") {
        return;
    }
    register_administration_document_observer(server, s.admin.clone(), Some(s.paths.clone()));
    server.action(Arc::new(codeaction::AdminContextProvider::new()));
    let host: Arc<dyn WorkspaceEditHost> = Arc::new(server.server.downgrade());
    // Offers the override as a code action and runs it as a command.
    let twig_override = Arc::new(codeaction::AdminTwigOverrideProvider::new(
        s.admin.clone(),
        Some(s.extensions.clone()),
        Some(host),
    ));
    server.action(twig_override.clone());
    server.command(twig_override);

    let Some(admin) = &s.admin else {
        return;
    };
    let index = || admin.clone();
    server.completion(Arc::new(crate::completion::AdminCompletionProvider::new(
        index(),
    )));
    server.definition(Arc::new(crate::definition::AdminDefinitionProvider::new(
        index(),
    )));
    server.references(Arc::new(crate::reference::AdminReferenceProvider::new(
        Some(index()),
    )));
    server.hover(Arc::new(crate::hover::AdminHoverProvider::new(
        s.root.clone(),
        index(),
    )));
    server.code_lens(Arc::new(codelens::AdminComponentCodeLensProvider::new(
        Some(index()),
    )));
    server.workspace_symbol_source(Arc::new(crate::symbol::AdminWorkspaceSymbolProvider::new(
        Some(index()),
    )));
    server.document_symbol(Arc::new(crate::symbol::AdminDocumentSymbolProvider::new(
        Some(index()),
    )));
    server.document_highlight(Arc::new(
        crate::highlight::AdminDocumentHighlightProvider::new(Some(index())),
    ));
    server.call_hierarchy(Arc::new(
        crate::callhierarchy::AdminCallHierarchyProvider::new(index()),
    ));
    server.linked_editing(Arc::new(
        crate::linkedediting::AdminLinkedEditingProvider::new(),
    ));
    server.folding_range(Arc::new(crate::folding::AdminFoldingProvider::new()));
    server.selection_range(Arc::new(
        crate::selection::AdminSelectionRangeProvider::new(),
    ));
    server.signature_help(Arc::new(crate::signature::AdminSignatureProvider::new(
        index(),
    )));
    server.rename(Arc::new(crate::refactor::AdminRenameProvider::new(Some(
        index(),
    ))));
    server.inlay_hint(Arc::new(crate::inlay::AdminParameterProvider::new(index())));
    server.semantic_tokens(Arc::new(crate::semantic::AdminMarkupProvider::new(index())));
}
