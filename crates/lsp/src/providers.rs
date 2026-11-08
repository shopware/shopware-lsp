//! Provider interfaces implemented by feature crates, and the request values
//! the server hands to them.
//!
//! Position-based requests carry the decoded params plus the
//! [`SyntaxContext`] of the cursor (they dereference to it, so providers read
//! `request.node` directly). Document-wide requests carry the open
//! [`TextDocument`] snapshot. Every provider runs for every request and
//! rejects unsupported contexts itself.

use std::collections::BTreeMap;
use std::ops::Deref;
use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextRange;

use crate::context::{BoxError, Context};
use crate::document::{SyntaxContext, TextDocument};

pub type ProviderResult<T> = Result<T, BoxError>;

macro_rules! syntax_request {
    ($(#[$meta:meta])* $name:ident, $params:ty) => {
        $(#[$meta])*
        #[derive(Debug, Clone)]
        pub struct $name {
            pub params: $params,
            pub syntax: SyntaxContext,
        }

        impl Deref for $name {
            type Target = SyntaxContext;

            fn deref(&self) -> &SyntaxContext {
                &self.syntax
            }
        }
    };
}

macro_rules! document_request {
    ($(#[$meta:meta])* $name:ident, $params:ty) => {
        $(#[$meta])*
        #[derive(Debug, Clone)]
        pub struct $name {
            pub params: $params,
            pub document: Arc<TextDocument>,
        }
    };
}

syntax_request!(CompletionRequest, protocol::CompletionParams);
syntax_request!(DefinitionRequest, protocol::DefinitionParams);
syntax_request!(ImplementationRequest, protocol::ImplementationParams);
syntax_request!(
    TypeHierarchyPrepareRequest,
    protocol::PrepareTypeHierarchyParams
);
syntax_request!(
    CallHierarchyPrepareRequest,
    protocol::CallHierarchyPrepareParams
);
syntax_request!(ReferenceRequest, protocol::ReferenceParams);
syntax_request!(DocumentHighlightRequest, protocol::DocumentHighlightParams);
syntax_request!(
    LinkedEditingRangeRequest,
    protocol::LinkedEditingRangeParams
);
syntax_request!(HoverRequest, protocol::HoverParams);
syntax_request!(SignatureHelpRequest, protocol::SignatureHelpParams);
syntax_request!(RenameRequest, protocol::RenameParams);
syntax_request!(CodeActionRequest, protocol::CodeActionParams);

document_request!(FoldingRangeRequest, protocol::FoldingRangeParams);
document_request!(
    DocumentFormattingRequest,
    protocol::DocumentFormattingParams
);
document_request!(SelectionRangeRequest, protocol::SelectionRangeParams);
document_request!(DocumentColorRequest, protocol::DocumentColorParams);
document_request!(ColorPresentationRequest, protocol::ColorPresentationParams);
document_request!(InlayHintRequest, protocol::InlayHintParams);
document_request!(DocumentLinkRequest, protocol::DocumentLinkParams);
document_request!(DocumentSymbolRequest, protocol::DocumentSymbolParams);
document_request!(SemanticTokensRequest, protocol::SemanticTokensParams);
document_request!(CodeLensRequest, protocol::CodeLensParams);

/// Incoming/outgoing call requests receive every open document so providers
/// can prefer unsaved editor state.
#[derive(Debug, Clone)]
pub struct CallHierarchyCallsRequest {
    pub item: protocol::CallHierarchyItem,
    pub documents: Vec<Arc<TextDocument>>,
}

#[derive(Debug, Clone)]
pub struct FileRenameRequest {
    pub params: protocol::RenameFilesParams,
    pub documents: Vec<Arc<TextDocument>>,
}

/// A single-line byte range with a token type from
/// [`protocol::SEMANTIC_TOKEN_TYPES`] and an optional modifier bitset. The
/// server encodes the LSP delta format.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SemanticToken {
    pub range: TextRange,
    pub token_type: u32,
    pub modifiers: u32,
}

/// Name used in error messages and traces (Go `%T`).
macro_rules! provider_name {
    () => {
        fn provider_name(&self) -> &'static str {
            std::any::type_name::<Self>()
        }
    };
}

pub trait CompletionProvider: Send + Sync {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem>;
    fn trigger_characters(&self) -> Vec<String>;
    provider_name!();
}

pub trait GotoDefinitionProvider: Send + Sync {
    fn get_definition(&self, ctx: &Context, request: &DefinitionRequest)
    -> Vec<protocol::Location>;
    provider_name!();
}

pub trait ImplementationProvider: Send + Sync {
    fn get_implementation(
        &self,
        ctx: &Context,
        request: &ImplementationRequest,
    ) -> Vec<protocol::Location>;
    provider_name!();
}

pub trait TypeHierarchyProvider: Send + Sync {
    fn prepare_type_hierarchy(
        &self,
        ctx: &Context,
        request: &TypeHierarchyPrepareRequest,
    ) -> Vec<protocol::TypeHierarchyItem>;
    fn supertypes(
        &self,
        ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem>;
    fn subtypes(
        &self,
        ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem>;
    provider_name!();
}

pub trait CallHierarchyProvider: Send + Sync {
    fn prepare_call_hierarchy(
        &self,
        ctx: &Context,
        request: &CallHierarchyPrepareRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyItem>>;
    fn incoming_calls(
        &self,
        ctx: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyIncomingCall>>;
    fn outgoing_calls(
        &self,
        ctx: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyOutgoingCall>>;
    provider_name!();
}

pub trait ReferencesProvider: Send + Sync {
    fn get_references(
        &self,
        ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>>;
    provider_name!();
}

/// The first provider returning a hover wins.
pub trait HoverProvider: Send + Sync {
    fn get_hover(
        &self,
        ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>>;
    provider_name!();
}

pub trait SignatureHelpProvider: Send + Sync {
    fn get_signature_help(
        &self,
        ctx: &Context,
        request: &SignatureHelpRequest,
    ) -> ProviderResult<Option<protocol::SignatureHelp>>;
    provider_name!();
}

pub trait RenameProvider: Send + Sync {
    fn rename(
        &self,
        ctx: &Context,
        request: &RenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>>;
    provider_name!();
}

pub trait InlayHintProvider: Send + Sync {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>>;
    provider_name!();
}

pub trait DocumentLinkProvider: Send + Sync {
    fn get_document_links(
        &self,
        ctx: &Context,
        request: &DocumentLinkRequest,
    ) -> ProviderResult<Vec<protocol::DocumentLink>>;
    provider_name!();
}

pub trait DocumentSymbolProvider: Send + Sync {
    fn get_document_symbols(
        &self,
        ctx: &Context,
        request: &DocumentSymbolRequest,
    ) -> ProviderResult<Vec<protocol::DocumentSymbol>>;
    provider_name!();
}

pub trait DocumentHighlightProvider: Send + Sync {
    fn get_document_highlights(
        &self,
        ctx: &Context,
        request: &DocumentHighlightRequest,
    ) -> ProviderResult<Vec<protocol::DocumentHighlight>>;
    provider_name!();
}

pub trait LinkedEditingRangeProvider: Send + Sync {
    fn get_linked_editing_ranges(
        &self,
        ctx: &Context,
        request: &LinkedEditingRangeRequest,
    ) -> ProviderResult<Option<protocol::LinkedEditingRanges>>;
    provider_name!();
}

pub trait FoldingRangeProvider: Send + Sync {
    fn get_folding_ranges(
        &self,
        ctx: &Context,
        request: &FoldingRangeRequest,
    ) -> ProviderResult<Vec<protocol::FoldingRange>>;
    provider_name!();
}

/// Returns `Some(formatted)` when the provider handles the document.
pub trait DocumentFormattingProvider: Send + Sync {
    fn format_document(
        &self,
        ctx: &Context,
        request: &DocumentFormattingRequest,
    ) -> ProviderResult<Option<String>>;
    provider_name!();
}

pub trait SelectionRangeProvider: Send + Sync {
    fn get_selection_ranges(
        &self,
        ctx: &Context,
        request: &SelectionRangeRequest,
    ) -> ProviderResult<Vec<protocol::SelectionRange>>;
    provider_name!();
}

pub trait DocumentColorProvider: Send + Sync {
    fn get_document_colors(
        &self,
        ctx: &Context,
        request: &DocumentColorRequest,
    ) -> ProviderResult<Vec<protocol::ColorInformation>>;
    fn get_color_presentations(
        &self,
        ctx: &Context,
        request: &ColorPresentationRequest,
    ) -> ProviderResult<Vec<protocol::ColorPresentation>>;
    provider_name!();
}

pub trait SemanticTokensProvider: Send + Sync {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>>;
    provider_name!();
}

pub trait CodeLensProvider: Send + Sync {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>>;
    /// `Ok(None)` when this provider does not own the lens.
    fn resolve_code_lens(
        &self,
        ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>>;
    provider_name!();
}

/// Non-inspection code actions. Edits are revalidated against the current
/// snapshots before they reach a client.
pub trait ActionProvider: Send + Sync {
    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction>;
    fn code_action_kinds(&self) -> Vec<protocol::CodeActionKind>;
    provider_name!();
}

pub trait WorkspaceSymbolProvider: Send + Sync {
    fn workspace_symbols(
        &self,
        ctx: &Context,
        query: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>>;
    provider_name!();
}

pub trait FileRenameProvider: Send + Sync {
    fn will_rename_files(
        &self,
        ctx: &Context,
        request: &FileRenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>>;
    provider_name!();
}

/// A custom method handler. `args` is `None` when the request had no params.
/// Returning an [`crate::RpcError`] selects the JSON-RPC error code.
pub type CommandFunc =
    Arc<dyn Fn(&Context, Option<&Value>) -> Result<Value, BoxError> + Send + Sync>;

pub trait CommandProvider: Send + Sync {
    fn commands(&self, ctx: &Context) -> BTreeMap<String, CommandFunc>;
}

/// Adds language-specific semantic state to a request context before
/// provider fan-out.
pub type ContextEnricher = Arc<dyn Fn(&Context, &SyntaxContext) -> Context + Send + Sync>;
