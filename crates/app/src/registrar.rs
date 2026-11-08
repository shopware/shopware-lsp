//! Records every registration the composition root makes, so tests and the
//! Go oracle can compare what is registered, in which order, per method.

use std::sync::{Arc, Mutex};

use swlsp_language::LanguageId;
use swlsp_lsp::*;

/// One registration: the server method family and the provider type (the
/// last path segment of its Rust type name) or inspection ID.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Registration {
    pub method: &'static str,
    pub name: String,
    /// The languages of an inspection; empty for everything else.
    pub languages: Vec<String>,
}

/// Wraps the server's registration API and logs each call.
pub(crate) struct Registrar<'a> {
    pub server: &'a Server,
    log: Mutex<Vec<Registration>>,
}

/// `package.Type` like Go's `%T` without the pointer: the defining crate
/// without its `swlsp_lsp_`/`swlsp_` prefix and the type's last segment.
pub(crate) fn short_type_name<T: ?Sized>() -> String {
    let full = std::any::type_name::<T>();
    let base = full.split('<').next().unwrap_or(full);
    let krate = base.split("::").next().unwrap_or(base);
    let krate = krate
        .strip_prefix("swlsp_lsp_")
        .or_else(|| krate.strip_prefix("swlsp_"))
        .unwrap_or(krate);
    let name = base.rsplit("::").next().unwrap_or(base);
    format!("{krate}.{name}")
}

macro_rules! registrations {
    ($($fn:ident => $register:ident, $trait:ident, $method:literal;)*) => {
        $(
            pub fn $fn<P: $trait + 'static>(&self, provider: Arc<P>) {
                self.record($method, short_type_name::<P>());
                self.server.$register(provider);
            }
        )*
    };
}

impl<'a> Registrar<'a> {
    pub fn new(server: &'a Server) -> Self {
        Registrar {
            server,
            log: Mutex::new(Vec::new()),
        }
    }

    pub fn into_log(self) -> Vec<Registration> {
        self.log
            .into_inner()
            .unwrap_or_else(|error| error.into_inner())
    }

    pub fn record(&self, method: &'static str, name: impl Into<String>) {
        self.record_languages(method, name, Vec::new());
    }

    fn record_languages(
        &self,
        method: &'static str,
        name: impl Into<String>,
        languages: Vec<String>,
    ) {
        self.log
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .push(Registration {
                method,
                name: name.into(),
                languages,
            });
    }

    pub fn domain_enabled(&self, id: &str) -> bool {
        self.server.domain_enabled(id)
    }

    pub fn framework_presentation(&self) -> bool {
        self.server.framework_presentation()
    }

    registrations! {
        completion => register_completion_provider, CompletionProvider, "completion";
        definition => register_definition_provider, GotoDefinitionProvider, "definition";
        implementation => register_implementation_provider, ImplementationProvider, "implementation";
        type_hierarchy => register_type_hierarchy_provider, TypeHierarchyProvider, "typeHierarchy";
        call_hierarchy => register_call_hierarchy_provider, CallHierarchyProvider, "callHierarchy";
        linked_editing => register_linked_editing_range_provider, LinkedEditingRangeProvider, "linkedEditingRange";
        folding_range => register_folding_range_provider, FoldingRangeProvider, "foldingRange";
        formatting => register_document_formatting_provider, DocumentFormattingProvider, "formatting";
        selection_range => register_selection_range_provider, SelectionRangeProvider, "selectionRange";
        document_color => register_document_color_provider, DocumentColorProvider, "documentColor";
        references => register_references_provider, ReferencesProvider, "references";
        code_lens => register_code_lens_provider, CodeLensProvider, "codeLens";
        action => register_action_provider, ActionProvider, "codeAction";
        hover => register_hover_provider, HoverProvider, "hover";
        signature_help => register_signature_help_provider, SignatureHelpProvider, "signatureHelp";
        rename => register_rename_provider, RenameProvider, "rename";
        inlay_hint => register_inlay_hint_provider, InlayHintProvider, "inlayHint";
        document_link => register_document_link_provider, DocumentLinkProvider, "documentLink";
        document_symbol => register_document_symbol_provider, DocumentSymbolProvider, "documentSymbol";
        document_highlight => register_document_highlight_provider, DocumentHighlightProvider, "documentHighlight";
        semantic_tokens => register_semantic_tokens_provider, SemanticTokensProvider, "semanticTokens";
        file_rename => register_file_rename_provider, FileRenameProvider, "fileRename";
        workspace_symbol => register_workspace_symbol_provider, WorkspaceSymbolProvider, "workspaceSymbol";
        command => register_command_provider, CommandProvider, "command";
    }

    /// Registers an inspection, logged under its definition ID.
    pub fn inspection(&self, inspection: Arc<dyn Inspection>) {
        let definition = inspection.definition();
        let languages = definition
            .languages
            .iter()
            .map(|language| language.as_str().to_owned())
            .collect();
        self.record_languages("inspection", definition.id, languages);
        self.server.register_inspection(inspection);
    }

    pub fn context_enricher(&self, language: LanguageId, enricher: ContextEnricher) {
        self.record("contextEnricher", language.as_str());
        self.server.register_context_enricher(language, enricher);
    }

    pub fn document_observer(&self, name: &str, observer: DocumentObserver) {
        self.record("documentObserver", name);
        self.server.register_document_observer(observer);
    }
}
