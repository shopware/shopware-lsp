//! PHP semantic graph LSP features (port of Go `internal/lsp/phpsemantic`):
//! semantic diagnostics, completion, definition, implementations, type
//! hierarchy, hover, references, rename, signature help, document symbols
//! and document highlights.
//!
//! Go reads the request's PHP analysis from the enriched `context.Context`.
//! Rust builds the same [`PhpContext`] from the request's document through
//! the memoized [`swlsp_lsp_phpanalysis::for_document`] analysis, so every
//! provider of one request shares one semantic pass.
//!
//! Diagnostics are reported through [`Provider::analyze`]; the application
//! registers them as the `php.semantic` problem inspection owning
//! [`DIAGNOSTIC_IDS`].

mod completion;
mod completion_imports;
mod diagnostics;
mod editor;
mod helpers;
mod hierarchy;
mod navigation;

#[cfg(test)]
mod tests;

use swlsp_lsp::{
    CompletionProvider, CompletionRequest, Context, DefinitionRequest, DocumentHighlightProvider,
    DocumentHighlightRequest, DocumentSymbolProvider, DocumentSymbolRequest,
    GotoDefinitionProvider, HoverProvider, HoverRequest, ImplementationProvider,
    ImplementationRequest, ProviderResult, ReferenceRequest, ReferencesProvider, RenameProvider,
    RenameRequest, SignatureHelpProvider, SignatureHelpRequest, SyntaxContext,
    TypeHierarchyPrepareRequest, TypeHierarchyProvider,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpContext, PhpIndex};

pub use diagnostics::{DIAGNOSTIC_IDS, INSPECTION, SOURCE};

/// Exposes the PHP semantic graph through LSP features. Without an index
/// only the CST outline is available.
#[derive(Clone, Default)]
pub struct Provider {
    index: Option<PhpIndex>,
}

impl Provider {
    pub fn new(index: Option<PhpIndex>) -> Self {
        Self { index }
    }

    /// The PHP index the provider analyzes against.
    pub fn index(&self) -> Option<&PhpIndex> {
        self.index.as_ref()
    }

    /// The request's PHP analysis (Go's PHP context enricher): the memoized
    /// document analysis, or a fresh unnamed analysis when the document has
    /// no file path.
    fn php_context(&self, syntax: &SyntaxContext) -> Option<PhpContext> {
        let index = self.index.as_ref()?;
        let document = syntax.document.as_deref()?;
        match swlsp_lsp_phpanalysis::for_document(index, document) {
            Ok(Some(state)) => Some(index.analyzed_snapshot_context(
                syntax.node.as_ref(),
                state.document.clone(),
                Some(state.snapshot.clone()),
            )),
            Ok(None) => None,
            Err(_) => {
                let root = syntax.root.as_ref()?;
                Some(index.document_context(
                    "",
                    i64::from(document.version),
                    syntax.node.as_ref(),
                    root,
                    document.source(),
                ))
            }
        }
    }
}

impl CompletionProvider for Provider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        self.completions(request)
    }

    fn trigger_characters(&self) -> Vec<String> {
        [">", ":", "$", "\\"].map(String::from).to_vec()
    }
}

impl GotoDefinitionProvider for Provider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        self.definition(request)
    }
}

impl ImplementationProvider for Provider {
    fn get_implementation(
        &self,
        _ctx: &Context,
        request: &ImplementationRequest,
    ) -> Vec<protocol::Location> {
        self.implementation(request)
    }
}

impl TypeHierarchyProvider for Provider {
    fn prepare_type_hierarchy(
        &self,
        _ctx: &Context,
        request: &TypeHierarchyPrepareRequest,
    ) -> Vec<protocol::TypeHierarchyItem> {
        self.prepare_hierarchy(request)
    }

    fn supertypes(
        &self,
        _ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem> {
        self.hierarchy_supertypes(item)
    }

    fn subtypes(
        &self,
        _ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem> {
        self.hierarchy_subtypes(item)
    }
}

impl ReferencesProvider for Provider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        Ok(self.references(request))
    }
}

impl HoverProvider for Provider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        Ok(self.hover(request))
    }
}

impl SignatureHelpProvider for Provider {
    fn get_signature_help(
        &self,
        _ctx: &Context,
        request: &SignatureHelpRequest,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        Ok(self.signature_help(request))
    }
}

impl RenameProvider for Provider {
    fn rename(
        &self,
        _ctx: &Context,
        request: &RenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>> {
        self.rename_symbol(request)
    }
}

impl DocumentSymbolProvider for Provider {
    fn get_document_symbols(
        &self,
        ctx: &Context,
        request: &DocumentSymbolRequest,
    ) -> ProviderResult<Vec<protocol::DocumentSymbol>> {
        self.document_symbols(ctx, &request.document)
    }
}

impl DocumentHighlightProvider for Provider {
    fn get_document_highlights(
        &self,
        ctx: &Context,
        request: &DocumentHighlightRequest,
    ) -> ProviderResult<Vec<protocol::DocumentHighlight>> {
        self.document_highlights(ctx, request)
    }
}
