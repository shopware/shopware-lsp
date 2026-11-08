//! Provider fan-out for every protocol feature, document synchronization and
//! semantic-token encoding.

use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use serde::Deserialize;
use serde_json::Value;
use swlsp_lsp_protocol as protocol;

use crate::context::{BoxError, Context, RpcError};
use crate::diagnostics::DIAGNOSTICS_DEBOUNCE;
use crate::document::{SyntaxContext, TextDocument};
use crate::providers::*;
use crate::server::{Server, decode_params};

fn provider_error(kind: &str, name: &str, error: BoxError) -> BoxError {
    if error.is::<RpcError>() {
        return error;
    }
    format!("{kind} provider {name}: {error}").into()
}

fn range_key(range: &protocol::Range) -> (u32, u32, u32, u32) {
    (
        range.start.line,
        range.start.character,
        range.end.line,
        range.end.character,
    )
}

fn valid_protocol_range(range: &protocol::Range) -> bool {
    range.start.line < range.end.line
        || (range.start.line == range.end.line && range.start.character <= range.end.character)
}

#[derive(Deserialize)]
struct DidOpenParams {
    #[serde(rename = "textDocument")]
    text_document: DidOpenDocument,
}

#[derive(Deserialize)]
struct DidOpenDocument {
    #[serde(default)]
    uri: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    version: i32,
}

#[derive(Deserialize)]
struct DidChangeParams {
    #[serde(rename = "textDocument")]
    text_document: VersionedDocument,
    #[serde(rename = "contentChanges", default)]
    content_changes: Option<Vec<ContentChange>>,
}

#[derive(Deserialize)]
struct VersionedDocument {
    #[serde(default)]
    uri: String,
    #[serde(default)]
    version: i32,
}

#[derive(Deserialize)]
struct ContentChange {
    #[serde(default)]
    text: String,
}

#[derive(Deserialize)]
struct DidCloseParams {
    #[serde(rename = "textDocument")]
    text_document: protocol::TextDocumentIdentifier,
}

impl Server {
    fn trace(&self, started: Instant, message: impl FnOnce(Duration) -> String) {
        if self.trace_providers {
            log::info!("{}", message(started.elapsed()));
        }
    }

    fn syntax(&self, uri: &str, position: protocol::Position) -> Option<SyntaxContext> {
        self.documents
            .syntax_context(uri, position.line, position.character)
    }

    // -- document synchronization (full text) -------------------------------

    pub(crate) fn handle_did_open(&self, params: Value) -> Result<Value, RpcError> {
        let params: DidOpenParams = decode_params(params)?;
        let document = params.text_document;
        self.documents
            .open_document(&document.uri, &document.text, document.version);
        if !self.cli_mode() {
            self.schedule_diagnostics(&document.uri, document.version, Duration::ZERO);
        }
        Ok(Value::Null)
    }

    pub(crate) fn handle_did_change(&self, params: Value) -> Result<Value, RpcError> {
        let params: DidChangeParams = decode_params(params)?;
        let Some(change) = params
            .content_changes
            .and_then(|changes| changes.into_iter().next())
        else {
            return Ok(Value::Null);
        };
        let document = params.text_document;
        self.documents
            .update_document(&document.uri, &change.text, document.version);
        if !self.cli_mode() {
            self.schedule_diagnostics(&document.uri, document.version, DIAGNOSTICS_DEBOUNCE);
        }
        Ok(Value::Null)
    }

    pub(crate) fn handle_did_close(&self, params: Value) -> Result<Value, RpcError> {
        let params: DidCloseParams = decode_params(params)?;
        let uri = params.text_document.uri;
        self.cancel_diagnostics(&uri);
        self.documents.close_document(&uri);
        self.clear_published_diagnostics(&uri);
        Ok(Value::Null)
    }

    // -- position requests --------------------------------------------------

    pub(crate) fn completion(
        &self,
        ctx: &Context,
        params: protocol::CompletionParams,
    ) -> protocol::CompletionList {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = CompletionRequest { params, syntax };
        let mut items = Vec::new();
        for provider in &registry.completion {
            items.extend(provider.get_completions(&ctx, &request));
        }
        self.filter_completion_commands_for_client(&mut items);
        protocol::CompletionList {
            is_incomplete: false,
            items,
        }
    }

    pub(crate) fn definition(
        &self,
        ctx: &Context,
        params: protocol::DefinitionParams,
    ) -> Vec<protocol::Location> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = DefinitionRequest { params, syntax };
        let started = Instant::now();
        let mut locations = Vec::new();
        for provider in &registry.definition {
            let provider_started = Instant::now();
            let found = provider.get_definition(&ctx, &request);
            self.trace(provider_started, |elapsed| {
                format!(
                    "LSP definition provider {} took {elapsed:?} ({} locations)",
                    provider.provider_name(),
                    found.len()
                )
            });
            locations.extend(found);
        }
        self.trace(started, |elapsed| {
            format!(
                "LSP definition request took {elapsed:?} ({} locations)",
                locations.len()
            )
        });
        locations
    }

    pub(crate) fn implementation(
        &self,
        ctx: &Context,
        params: protocol::ImplementationParams,
    ) -> Vec<protocol::Location> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = ImplementationRequest { params, syntax };
        registry
            .implementation
            .iter()
            .flat_map(|provider| provider.get_implementation(&ctx, &request))
            .collect()
    }

    pub(crate) fn prepare_type_hierarchy(
        &self,
        ctx: &Context,
        params: protocol::PrepareTypeHierarchyParams,
    ) -> Vec<protocol::TypeHierarchyItem> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = TypeHierarchyPrepareRequest { params, syntax };
        registry
            .type_hierarchy
            .iter()
            .flat_map(|provider| provider.prepare_type_hierarchy(&ctx, &request))
            .collect()
    }

    pub(crate) fn type_hierarchy_supertypes(
        &self,
        ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem> {
        self.registry()
            .type_hierarchy
            .iter()
            .flat_map(|provider| provider.supertypes(ctx, item))
            .collect()
    }

    pub(crate) fn type_hierarchy_subtypes(
        &self,
        ctx: &Context,
        item: &protocol::TypeHierarchyItem,
    ) -> Vec<protocol::TypeHierarchyItem> {
        self.registry()
            .type_hierarchy
            .iter()
            .flat_map(|provider| provider.subtypes(ctx, item))
            .collect()
    }

    pub(crate) fn references(
        &self,
        ctx: &Context,
        params: protocol::ReferenceParams,
    ) -> Result<Vec<protocol::Location>, BoxError> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = ReferenceRequest { params, syntax };
        let started = Instant::now();
        let mut locations = Vec::new();
        for provider in &registry.references {
            let provider_started = Instant::now();
            let found = provider.get_references(&ctx, &request);
            self.trace(provider_started, |elapsed| {
                format!(
                    "LSP references provider {} took {elapsed:?} ({} locations)",
                    provider.provider_name(),
                    found.as_ref().map_or(0, Vec::len)
                )
            });
            locations.extend(
                found.map_err(|error| {
                    provider_error("references", provider.provider_name(), error)
                })?,
            );
        }
        self.trace(started, |elapsed| {
            format!(
                "LSP references request took {elapsed:?} ({} locations)",
                locations.len()
            )
        });
        Ok(locations)
    }

    pub(crate) fn hover(
        &self,
        ctx: &Context,
        params: protocol::HoverParams,
    ) -> Result<Option<protocol::Hover>, BoxError> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = HoverRequest { params, syntax };
        let started = Instant::now();
        for provider in &registry.hover {
            let provider_started = Instant::now();
            let hover = provider.get_hover(&ctx, &request);
            self.trace(provider_started, |elapsed| {
                format!(
                    "LSP hover provider {} took {elapsed:?} (result={})",
                    provider.provider_name(),
                    matches!(hover, Ok(Some(_)))
                )
            });
            if let Some(hover) =
                hover.map_err(|error| provider_error("hover", provider.provider_name(), error))?
            {
                self.trace(started, |elapsed| {
                    format!("LSP hover request took {elapsed:?}")
                });
                return Ok(Some(hover));
            }
        }
        self.trace(started, |elapsed| {
            format!("LSP hover request took {elapsed:?}")
        });
        Ok(None)
    }

    pub(crate) fn signature_help(
        &self,
        ctx: &Context,
        params: protocol::SignatureHelpParams,
    ) -> Result<Option<protocol::SignatureHelp>, BoxError> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = SignatureHelpRequest { params, syntax };
        for provider in &registry.signature_help {
            let result = provider
                .get_signature_help(&ctx, &request)
                .map_err(|error| {
                    provider_error("signature help", provider.provider_name(), error)
                })?;
            if result.is_some() {
                return Ok(result);
            }
        }
        Ok(None)
    }

    pub(crate) fn rename(
        &self,
        ctx: &Context,
        params: protocol::RenameParams,
    ) -> Result<Option<protocol::WorkspaceEdit>, BoxError> {
        let syntax = self
            .syntax(&params.text_document.uri, params.position)
            .unwrap_or_default();
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = RenameRequest { params, syntax };
        for provider in &registry.rename {
            let edit = provider
                .rename(&ctx, &request)
                .map_err(|error| provider_error("rename", provider.provider_name(), error))?;
            if edit.is_some() {
                return Ok(edit);
            }
        }
        Ok(None)
    }

    pub(crate) fn document_highlights(
        &self,
        ctx: &Context,
        params: protocol::DocumentHighlightParams,
    ) -> Result<Vec<protocol::DocumentHighlight>, BoxError> {
        let Some(syntax) = self.syntax(&params.text_document.uri, params.position) else {
            return Ok(Vec::new());
        };
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = DocumentHighlightRequest { params, syntax };
        let mut result: Vec<protocol::DocumentHighlight> = Vec::new();
        let mut positions: HashMap<(u32, u32, u32, u32), usize> = HashMap::new();
        for provider in &registry.document_highlights {
            ctx.check()?;
            let highlights = provider
                .get_document_highlights(&ctx, &request)
                .map_err(|error| {
                    provider_error("document highlight", provider.provider_name(), error)
                })?;
            for highlight in highlights {
                let key = range_key(&highlight.range);
                if let Some(&index) = positions.get(&key) {
                    if highlight_priority(highlight.kind) > highlight_priority(result[index].kind) {
                        result[index].kind = highlight.kind;
                    }
                    continue;
                }
                positions.insert(key, result.len());
                result.push(highlight);
            }
        }
        result.sort_by_key(|highlight| range_key(&highlight.range));
        Ok(result)
    }

    pub(crate) fn linked_editing_ranges(
        &self,
        ctx: &Context,
        params: protocol::LinkedEditingRangeParams,
    ) -> Result<Option<protocol::LinkedEditingRanges>, BoxError> {
        let Some(syntax) = self.syntax(&params.text_document.uri, params.position) else {
            return Ok(None);
        };
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = LinkedEditingRangeRequest { params, syntax };
        for provider in &registry.linked_editing {
            ctx.check()?;
            let ranges = provider
                .get_linked_editing_ranges(&ctx, &request)
                .map_err(|error| {
                    provider_error("linked editing range", provider.provider_name(), error)
                })?;
            if let Some(ranges) = ranges
                && ranges.ranges.len() >= 2
            {
                return Ok(Some(ranges));
            }
        }
        Ok(None)
    }

    pub(crate) fn prepare_call_hierarchy(
        &self,
        ctx: &Context,
        params: protocol::CallHierarchyPrepareParams,
    ) -> Result<Vec<protocol::CallHierarchyItem>, BoxError> {
        let Some(syntax) = self.syntax(&params.text_document.uri, params.position) else {
            return Ok(Vec::new());
        };
        let registry = self.registry();
        let ctx = self.enrich_context(&registry, ctx, &syntax);
        let request = CallHierarchyPrepareRequest { params, syntax };
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for provider in &registry.call_hierarchy {
            ctx.check()?;
            let items = provider
                .prepare_call_hierarchy(&ctx, &request)
                .map_err(|error| {
                    provider_error("prepare call hierarchy", provider.provider_name(), error)
                })?;
            for item in items {
                if seen.insert(call_hierarchy_key(&item)) {
                    result.push(item);
                }
            }
        }
        result.sort_by(call_hierarchy_order);
        Ok(result)
    }

    pub(crate) fn call_hierarchy_incoming_calls(
        &self,
        ctx: &Context,
        item: protocol::CallHierarchyItem,
    ) -> Result<Vec<protocol::CallHierarchyIncomingCall>, BoxError> {
        let request = CallHierarchyCallsRequest {
            item,
            documents: self.documents.documents(),
        };
        let mut result: Vec<protocol::CallHierarchyIncomingCall> = Vec::new();
        let mut positions: HashMap<CallHierarchyKey, usize> = HashMap::new();
        for provider in &self.registry().call_hierarchy {
            ctx.check()?;
            let calls = provider.incoming_calls(ctx, &request).map_err(|error| {
                provider_error("incoming call hierarchy", provider.provider_name(), error)
            })?;
            for mut call in calls {
                let key = call_hierarchy_key(&call.from);
                if let Some(&index) = positions.get(&key) {
                    let existing = std::mem::take(&mut result[index].from_ranges);
                    result[index].from_ranges = merge_ranges([existing, call.from_ranges]);
                    continue;
                }
                call.from_ranges = merge_ranges([std::mem::take(&mut call.from_ranges)]);
                positions.insert(key, result.len());
                result.push(call);
            }
        }
        result.sort_by(|left, right| call_hierarchy_order(&left.from, &right.from));
        Ok(result)
    }

    pub(crate) fn call_hierarchy_outgoing_calls(
        &self,
        ctx: &Context,
        item: protocol::CallHierarchyItem,
    ) -> Result<Vec<protocol::CallHierarchyOutgoingCall>, BoxError> {
        let request = CallHierarchyCallsRequest {
            item,
            documents: self.documents.documents(),
        };
        let mut result: Vec<protocol::CallHierarchyOutgoingCall> = Vec::new();
        let mut positions: HashMap<CallHierarchyKey, usize> = HashMap::new();
        for provider in &self.registry().call_hierarchy {
            ctx.check()?;
            let calls = provider.outgoing_calls(ctx, &request).map_err(|error| {
                provider_error("outgoing call hierarchy", provider.provider_name(), error)
            })?;
            for mut call in calls {
                let key = call_hierarchy_key(&call.to);
                if let Some(&index) = positions.get(&key) {
                    let existing = std::mem::take(&mut result[index].from_ranges);
                    result[index].from_ranges = merge_ranges([existing, call.from_ranges]);
                    continue;
                }
                call.from_ranges = merge_ranges([std::mem::take(&mut call.from_ranges)]);
                positions.insert(key, result.len());
                result.push(call);
            }
        }
        result.sort_by(|left, right| call_hierarchy_order(&left.to, &right.to));
        Ok(result)
    }

    // -- document requests --------------------------------------------------

    pub(crate) fn code_lens(
        &self,
        ctx: &Context,
        params: protocol::CodeLensParams,
    ) -> Result<Vec<protocol::CodeLens>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = CodeLensRequest { params, document };
        let mut lenses = Vec::new();
        for provider in &self.registry().code_lens {
            lenses.extend(
                provider.get_code_lenses(ctx, &request).map_err(|error| {
                    provider_error("code lens", provider.provider_name(), error)
                })?,
            );
        }
        Ok(self.filter_code_lenses_for_client(lenses))
    }

    pub(crate) fn resolve_code_lens(
        &self,
        ctx: &Context,
        lens: protocol::CodeLens,
    ) -> Result<protocol::CodeLens, BoxError> {
        for provider in &self.registry().code_lens {
            if let Some(mut resolved) = provider.resolve_code_lens(ctx, &lens)? {
                if resolved
                    .command
                    .as_ref()
                    .is_some_and(|command| !self.supports_client_command(&command.command))
                {
                    resolved.command = None;
                }
                return Ok(resolved);
            }
        }
        Ok(lens)
    }

    pub(crate) fn inlay_hints(
        &self,
        ctx: &Context,
        params: protocol::InlayHintParams,
    ) -> Result<Vec<protocol::InlayHint>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = InlayHintRequest { params, document };
        let mut hints = Vec::new();
        for provider in &self.registry().inlay_hints {
            hints.extend(
                provider.get_inlay_hints(ctx, &request).map_err(|error| {
                    provider_error("inlay hint", provider.provider_name(), error)
                })?,
            );
        }
        self.filter_inlay_hint_commands_for_client(&mut hints);
        Ok(hints)
    }

    pub(crate) fn document_links(
        &self,
        ctx: &Context,
        params: protocol::DocumentLinkParams,
    ) -> Result<Vec<protocol::DocumentLink>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = DocumentLinkRequest { params, document };
        let mut links = Vec::new();
        for provider in &self.registry().document_links {
            links.extend(
                provider
                    .get_document_links(ctx, &request)
                    .map_err(|error| {
                        provider_error("document link", provider.provider_name(), error)
                    })?,
            );
        }
        Ok(links)
    }

    pub(crate) fn document_symbols(
        &self,
        ctx: &Context,
        params: protocol::DocumentSymbolParams,
    ) -> Result<Vec<protocol::DocumentSymbol>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = DocumentSymbolRequest { params, document };
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for provider in &self.registry().document_symbols {
            ctx.check()?;
            let symbols = provider
                .get_document_symbols(ctx, &request)
                .map_err(|error| {
                    provider_error("document symbol", provider.provider_name(), error)
                })?;
            for symbol in symbols {
                let key = (
                    symbol.name.clone(),
                    symbol_kind_value(symbol.kind),
                    range_key(&symbol.range),
                );
                if seen.insert(key) {
                    result.push(symbol);
                }
            }
        }
        sort_document_symbols(&mut result);
        Ok(result)
    }

    pub(crate) fn folding_ranges(
        &self,
        ctx: &Context,
        params: protocol::FoldingRangeParams,
    ) -> Result<Vec<protocol::FoldingRange>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = FoldingRangeRequest { params, document };
        let mut result: Vec<protocol::FoldingRange> = Vec::new();
        let mut seen = HashSet::new();
        for provider in &self.registry().folding_ranges {
            ctx.check()?;
            let ranges = provider
                .get_folding_ranges(ctx, &request)
                .map_err(|error| {
                    provider_error("folding range", provider.provider_name(), error)
                })?;
            for range in ranges {
                if range.end_line <= range.start_line {
                    continue;
                }
                let key = (
                    range.start_line,
                    range.start_character.map_or(-1, i64::from),
                    range.end_line,
                    range.end_character.map_or(-1, i64::from),
                    range.kind.clone(),
                );
                if seen.insert(key) {
                    result.push(range);
                }
            }
        }
        result.sort_by(|left, right| {
            left.start_line
                .cmp(&right.start_line)
                .then_with(|| right.end_line.cmp(&left.end_line))
                .then_with(|| left.kind.cmp(&right.kind))
        });
        Ok(result)
    }

    pub(crate) fn formatting(
        &self,
        ctx: &Context,
        params: protocol::DocumentFormattingParams,
    ) -> Result<Vec<protocol::TextEdit>, BoxError> {
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = DocumentFormattingRequest {
            params,
            document: document.clone(),
        };
        for provider in &self.registry().formatting {
            ctx.check()?;
            let formatted = provider.format_document(ctx, &request).map_err(|error| {
                provider_error("document formatting", provider.provider_name(), error)
            })?;
            let Some(formatted) = formatted else {
                continue;
            };
            if formatted == document.source() {
                continue;
            }
            let (end_line, end_character) = document
                .line_index()
                .position_utf16(document.source().len() as u32);
            return Ok(vec![protocol::TextEdit::new(
                protocol::Range {
                    start: protocol::Position::default(),
                    end: protocol::Position {
                        line: end_line,
                        character: end_character,
                    },
                },
                formatted,
            )]);
        }
        Ok(Vec::new())
    }

    pub(crate) fn selection_ranges(
        &self,
        ctx: &Context,
        params: protocol::SelectionRangeParams,
    ) -> Result<Vec<protocol::SelectionRange>, BoxError> {
        if params.positions.is_empty() {
            return Ok(Vec::new());
        }
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let positions = params.positions.len();
        let request = SelectionRangeRequest { params, document };
        for provider in &self.registry().selection_ranges {
            ctx.check()?;
            let ranges = provider
                .get_selection_ranges(ctx, &request)
                .map_err(|error| {
                    provider_error("selection range", provider.provider_name(), error)
                })?;
            // LSP requires one result for every input position, in order.
            if ranges.len() == positions {
                return Ok(ranges);
            }
        }
        Ok(Vec::new())
    }

    /// Always an array (never `null`): strict clients reject `null` here.
    pub(crate) fn document_colors(
        &self,
        ctx: &Context,
        params: Option<protocol::DocumentColorParams>,
    ) -> Result<Vec<protocol::ColorInformation>, BoxError> {
        let Some(params) = params else {
            return Ok(Vec::new());
        };
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = DocumentColorRequest { params, document };
        let mut seen = HashSet::new();
        let mut result: Vec<protocol::ColorInformation> = Vec::new();
        for provider in &self.registry().colors {
            ctx.check()?;
            let colors = provider
                .get_document_colors(ctx, &request)
                .map_err(|error| {
                    provider_error("document color", provider.provider_name(), error)
                })?;
            for color in colors {
                if valid_protocol_range(&color.range) && seen.insert(range_key(&color.range)) {
                    result.push(color);
                }
            }
        }
        result.sort_by_key(|color| range_key(&color.range));
        Ok(result)
    }

    /// Always an array (never `null`).
    pub(crate) fn color_presentations(
        &self,
        ctx: &Context,
        params: Option<protocol::ColorPresentationParams>,
    ) -> Result<Vec<protocol::ColorPresentation>, BoxError> {
        let Some(params) = params else {
            return Ok(Vec::new());
        };
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let request = ColorPresentationRequest { params, document };
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for provider in &self.registry().colors {
            ctx.check()?;
            let presentations =
                provider
                    .get_color_presentations(ctx, &request)
                    .map_err(|error| {
                        provider_error("color presentation", provider.provider_name(), error)
                    })?;
            for presentation in presentations {
                let mut key = presentation.label.clone();
                if let Some(edit) = &presentation.text_edit {
                    key.push_str(&format!(
                        "\0{:?}\0{}",
                        range_key(&edit.range),
                        edit.new_text
                    ));
                }
                if seen.insert(key) {
                    result.push(presentation);
                }
            }
        }
        Ok(result)
    }

    pub(crate) fn semantic_tokens(
        &self,
        ctx: &Context,
        params: protocol::SemanticTokensParams,
    ) -> Result<protocol::SemanticTokens, BoxError> {
        let mut result = protocol::SemanticTokens::default();
        let Some(document) = self.documents.get_document(&params.text_document.uri) else {
            return Ok(result);
        };
        let request = SemanticTokensRequest {
            params,
            document: document.clone(),
        };
        let mut tokens = Vec::new();
        for provider in &self.registry().semantic_tokens {
            tokens.extend(
                provider
                    .get_semantic_tokens(ctx, &request)
                    .map_err(|error| {
                        provider_error("semantic token", provider.provider_name(), error)
                    })?,
            );
        }
        result.data = encode_semantic_tokens(&document, &tokens);
        Ok(result)
    }

    // -- workspace requests -------------------------------------------------

    pub(crate) fn will_rename_files(
        &self,
        ctx: &Context,
        params: protocol::RenameFilesParams,
    ) -> Result<Option<protocol::WorkspaceEdit>, BoxError> {
        let request = FileRenameRequest {
            params,
            documents: self.documents.documents(),
        };
        let mut changes: std::collections::BTreeMap<String, Vec<protocol::TextEdit>> =
            Default::default();
        for provider in &self.registry().file_rename {
            let edit = provider
                .will_rename_files(ctx, &request)
                .map_err(|error| provider_error("file rename", provider.provider_name(), error))?;
            let Some(edit) = edit else {
                continue;
            };
            for (uri, edits) in edit.changes {
                changes.entry(uri).or_default().extend(edits);
            }
        }
        if changes.is_empty() {
            return Ok(None);
        }
        Ok(Some(protocol::WorkspaceEdit {
            changes,
            ..protocol::WorkspaceEdit::default()
        }))
    }

    pub(crate) fn workspace_symbols(
        &self,
        ctx: &Context,
        params: protocol::WorkspaceSymbolParams,
    ) -> Result<Vec<protocol::SymbolInformation>, BoxError> {
        const MAX_WORKSPACE_SYMBOLS: usize = 500;
        let mut result: Vec<protocol::SymbolInformation> = Vec::new();
        let mut seen = HashSet::new();
        for provider in &self.registry().workspace_symbols {
            ctx.check()?;
            for symbol in provider.workspace_symbols(ctx, &params.query)? {
                let key = (
                    symbol.name.clone(),
                    symbol_kind_value(symbol.kind),
                    symbol.location.uri.clone(),
                    range_key(&symbol.location.range),
                );
                if seen.insert(key) {
                    result.push(symbol);
                }
            }
        }
        let query = params.query.trim().to_lowercase();
        let mut scored: Vec<(u8, protocol::SymbolInformation)> = result
            .into_iter()
            .map(|symbol| (workspace_symbol_score(&query, &symbol), symbol))
            .collect();
        scored.sort_by(|(left_score, left), (right_score, right)| {
            left_score
                .cmp(right_score)
                .then_with(|| right.priority.cmp(&left.priority))
                .then_with(|| left.name.cmp(&right.name))
                .then_with(|| left.container_name.cmp(&right.container_name))
                .then_with(|| left.location.uri.cmp(&right.location.uri))
        });
        scored.truncate(MAX_WORKSPACE_SYMBOLS);
        Ok(scored.into_iter().map(|(_, symbol)| symbol).collect())
    }
}

fn highlight_priority(kind: Option<protocol::DocumentHighlightKind>) -> u8 {
    match kind {
        Some(protocol::DocumentHighlightKind::WRITE) => 3,
        Some(protocol::DocumentHighlightKind::READ) => 2,
        _ => 1,
    }
}

fn symbol_kind_value(kind: protocol::SymbolKind) -> i64 {
    serde_json::to_value(kind)
        .ok()
        .and_then(|value| value.as_i64())
        .unwrap_or_default()
}

fn sort_document_symbols(symbols: &mut [protocol::DocumentSymbol]) {
    for symbol in symbols.iter_mut() {
        sort_document_symbols(&mut symbol.children);
    }
    symbols.sort_by(|left, right| {
        range_key(&left.range)
            .cmp(&range_key(&right.range))
            .then_with(|| symbol_kind_value(left.kind).cmp(&symbol_kind_value(right.kind)))
            .then_with(|| left.name.cmp(&right.name))
    });
}

type CallHierarchyKey = (String, String, (u32, u32, u32, u32));

fn call_hierarchy_key(item: &protocol::CallHierarchyItem) -> CallHierarchyKey {
    (
        item.uri.clone(),
        item.name.clone(),
        range_key(&item.selection_range),
    )
}

fn call_hierarchy_order(
    left: &protocol::CallHierarchyItem,
    right: &protocol::CallHierarchyItem,
) -> std::cmp::Ordering {
    left.uri
        .cmp(&right.uri)
        .then_with(|| {
            left.selection_range
                .start
                .line
                .cmp(&right.selection_range.start.line)
        })
        .then_with(|| {
            left.selection_range
                .start
                .character
                .cmp(&right.selection_range.start.character)
        })
        .then_with(|| left.name.cmp(&right.name))
}

fn merge_ranges<const N: usize>(groups: [Vec<protocol::Range>; N]) -> Vec<protocol::Range> {
    let mut seen = HashSet::new();
    let mut result: Vec<protocol::Range> = groups
        .into_iter()
        .flatten()
        .filter(|range| seen.insert(range_key(range)))
        .collect();
    result.sort_by_key(range_key);
    result
}

fn workspace_symbol_score(query: &str, symbol: &protocol::SymbolInformation) -> u8 {
    if query.is_empty() {
        return 3;
    }
    let mut best = 4;
    for value in [&symbol.name, &symbol.container_name] {
        let value = value.to_lowercase();
        if value == query {
            best = 0;
        } else if value.starts_with(query) {
            best = best.min(1);
        } else if value.contains(query) {
            best = best.min(2);
        }
    }
    best
}

/// Encodes byte-range tokens into the LSP relative format. Multiline,
/// out-of-range, unknown-type and overlapping tokens are dropped.
pub(crate) fn encode_semantic_tokens(
    document: &TextDocument,
    tokens: &[SemanticToken],
) -> Vec<u32> {
    struct Positioned {
        line: u32,
        start: u32,
        length: u32,
        token_type: u32,
        modifiers: u32,
    }
    let source_len = document.source().len() as u32;
    let line_index = document.line_index();
    let mut positioned: Vec<Positioned> = tokens
        .iter()
        .filter_map(|token| {
            let start: u32 = token.range.start().into();
            let end: u32 = token.range.end().into();
            if start >= end
                || end > source_len
                || token.token_type as usize >= protocol::SEMANTIC_TOKEN_TYPES.len()
            {
                return None;
            }
            let (start_line, start_character) = line_index.position_utf16(start);
            let (end_line, end_character) = line_index.position_utf16(end);
            // Token lengths cannot cross lines unless the client advertises
            // multiline support; providers emit lexical ranges.
            if start_line != end_line || end_character <= start_character {
                return None;
            }
            Some(Positioned {
                line: start_line,
                start: start_character,
                length: end_character - start_character,
                token_type: token.token_type,
                modifiers: token.modifiers,
            })
        })
        .collect();
    positioned.sort_by_key(|token| (token.line, token.start, token.length, token.token_type));
    let mut data = Vec::with_capacity(positioned.len() * 5);
    let mut previous: Option<(u32, u32)> = None;
    let mut last_end = 0;
    for token in positioned {
        if let Some((line, _)) = previous
            && token.line == line
            && token.start < last_end
        {
            // Overlapping tokens are invalid unless negotiated; the earlier
            // sorted token wins.
            continue;
        }
        let (delta_line, delta_start) = match previous {
            Some((line, start)) if token.line == line => (0, token.start - start),
            Some((line, _)) => (token.line - line, token.start),
            None => (token.line, token.start),
        };
        data.extend([
            delta_line,
            delta_start,
            token.length,
            token.token_type,
            token.modifiers,
        ]);
        previous = Some((token.line, token.start));
        last_end = token.start + token.length;
    }
    data
}
