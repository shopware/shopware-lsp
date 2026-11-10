//! Collects what the domains register and applies it to the server in the
//! order the composition root decides.
//!
//! Order matters: hover and signature help take the first provider that
//! answers, and the other requests concatenate results in provider order.
//! Which provider goes first is a decision between domains, so no domain can
//! make it. Domains therefore only declare their providers; the
//! [`Precedence`] of the composition root orders them per request kind.

use std::sync::{Arc, Mutex};

use swlsp_language::LanguageId;
use swlsp_lsp::*;

/// One registration: the request kind and the provider (`crate.Type`: the
/// defining crate without its `swlsp_` prefix and the type's last path
/// segment), the inspection ID, or the language or observer name.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Registration {
    pub method: &'static str,
    pub name: String,
    /// The languages of an inspection; empty for everything else.
    pub languages: Vec<String>,
}

/// The order of registrations per request kind: `(method, names)`. A listed
/// name goes before every unlisted one; unlisted ones keep the order in
/// which the domains registered them.
#[derive(Debug, Clone, Copy)]
pub struct Precedence(pub &'static [(&'static str, &'static [&'static str])]);

impl Precedence {
    fn rank(&self, method: &str, name: &str) -> usize {
        self.0
            .iter()
            .find(|(listed, _)| *listed == method)
            .and_then(|(_, names)| names.iter().position(|listed| *listed == name))
            .unwrap_or(usize::MAX)
    }
}

struct Pending<'a> {
    registration: Registration,
    apply: Box<dyn FnOnce(&Server) + 'a>,
}

/// The registration interface of the domains.
pub struct Registrar<'a> {
    pub server: &'a Server,
    pending: Mutex<Vec<Pending<'a>>>,
    workspace_symbol_sources: Mutex<Vec<(String, Arc<dyn WorkspaceSymbolProvider>)>>,
}

/// A short `crate.Type` name of `T`.
fn short_type_name<T: ?Sized>() -> String {
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
                self.defer($method, short_type_name::<P>(), Vec::new(), move |server| {
                    server.$register(provider)
                });
            }
        )*
    };
}

impl<'a> Registrar<'a> {
    pub fn new(server: &'a Server) -> Self {
        Registrar {
            server,
            pending: Mutex::new(Vec::new()),
            workspace_symbol_sources: Mutex::new(Vec::new()),
        }
    }

    fn defer(
        &self,
        method: &'static str,
        name: impl Into<String>,
        languages: Vec<String>,
        apply: impl FnOnce(&Server) + 'a,
    ) {
        self.pending
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .push(Pending {
                registration: Registration {
                    method,
                    name: name.into(),
                    languages,
                },
                apply: Box::new(apply),
            });
    }

    /// Applies everything registered so far to the server, ordered by
    /// `precedence` within each request kind, and returns what was applied
    /// in that order.
    pub fn finish(self, precedence: Precedence) -> Vec<Registration> {
        let mut pending = self
            .pending
            .into_inner()
            .unwrap_or_else(|error| error.into_inner());
        // Stable: registrations of equal rank keep their order, and the
        // request kinds keep the order in which they first appeared.
        let mut methods: Vec<&'static str> = Vec::new();
        for entry in &pending {
            if !methods.contains(&entry.registration.method) {
                methods.push(entry.registration.method);
            }
        }
        pending.sort_by_key(|entry| {
            let method = entry.registration.method;
            (
                methods.iter().position(|listed| *listed == method),
                precedence.rank(method, &entry.registration.name),
            )
        });
        let mut log = Vec::with_capacity(pending.len());
        for entry in pending {
            (entry.apply)(self.server);
            log.push(entry.registration);
        }
        log
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

    /// Registers a production inspection, logged under its definition ID.
    /// Like an invalid definition, one without a configuration domain fails
    /// composition: users must be able to switch every diagnostic off by
    /// domain.
    pub fn inspection(&self, inspection: Arc<dyn Inspection>) {
        let definition = inspection.definition();
        assert!(
            !definition.domain.is_empty(),
            "inspection {:?} declares no configuration domain",
            definition.id
        );
        let languages = definition
            .languages
            .iter()
            .map(|language| language.as_str().to_owned())
            .collect();
        self.defer("inspection", definition.id, languages, move |server| {
            server.register_inspection(inspection)
        });
    }

    pub fn context_enricher(&self, language: LanguageId, enricher: ContextEnricher) {
        self.defer(
            "contextEnricher",
            language.as_str(),
            Vec::new(),
            move |server| server.register_context_enricher(language, enricher),
        );
    }

    pub fn suppressions(&self, language: LanguageId, reader: SuppressionReader) {
        self.defer(
            "suppressions",
            language.as_str(),
            Vec::new(),
            move |server| server.register_suppressions(language, reader),
        );
    }

    pub fn document_observer(&self, name: &str, observer: DocumentObserver) {
        self.defer("documentObserver", name, Vec::new(), move |server| {
            server.register_document_observer(observer)
        });
    }

    /// Contributes a source of workspace symbols that are not in the symbol
    /// catalog. The composition root combines the sources of all domains
    /// into the one workspace symbol provider.
    pub fn workspace_symbol_source<P: WorkspaceSymbolProvider + 'static>(&self, source: Arc<P>) {
        self.workspace_symbol_sources
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .push((short_type_name::<P>(), source));
    }

    /// The contributed workspace symbol sources, ordered by `precedence`
    /// (request kind `workspaceSymbolSource`).
    pub fn take_workspace_symbol_sources(
        &self,
        precedence: Precedence,
    ) -> Vec<Arc<dyn WorkspaceSymbolProvider>> {
        let mut sources = std::mem::take(
            &mut *self
                .workspace_symbol_sources
                .lock()
                .unwrap_or_else(|error| error.into_inner()),
        );
        sources.sort_by_key(|(name, _)| precedence.rank("workspaceSymbolSource", name));
        sources.into_iter().map(|(_, source)| source).collect()
    }
}
