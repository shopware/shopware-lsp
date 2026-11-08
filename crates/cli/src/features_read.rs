//! Read-only feature commands (Go `features_read.go`). Each command opens
//! the target in the production session, sends one LSP request, decodes the
//! typed result and prints it as JSON.

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use swlsp_lsp_protocol::{
    CallHierarchyIncomingCall, CallHierarchyItem, CallHierarchyOutgoingCall, CodeLens,
    ColorInformation, CompletionList, DocumentHighlight, DocumentLink, DocumentSymbol,
    FoldingRange, Hover, InlayHint, LinkedEditingRanges, Location, Position, Range, SelectionRange,
    SemanticTokens, SignatureHelp, SymbolInformation, SymbolKind, TypeHierarchyItem,
};

use crate::session::{CliDocument, CliSession, position_params, text_document_params};
use crate::target::{parse_position_target, require_one_argument};
use crate::{CliError, Runner};

impl Runner {
    pub(crate) fn open_position_session(
        &self,
        args: &[String],
        usage: &str,
    ) -> Result<(CliSession, CliDocument, Position), CliError> {
        let value = require_one_argument(args, usage)?;
        let target = parse_position_target(&value)?;
        let session = self.connect()?;
        let document = session.open_document(&target.path)?;
        Ok((session, document, target.position))
    }

    fn position_request<T: DeserializeOwned>(
        &self,
        args: &[String],
        method: &str,
    ) -> Result<T, CliError> {
        let (session, document, position) =
            self.open_position_session(args, &format!("{method} expects one file position"))?;
        let result = session.call(method, position_params(&document.uri, position));
        let _ = session.close();
        result
    }

    fn document_request<T: DeserializeOwned>(
        &self,
        args: &[String],
        method: &str,
    ) -> Result<T, CliError> {
        let path = require_one_argument(args, &format!("{method} expects one file"))?;
        let session = self.connect()?;
        let document = session.open_document(&path)?;
        let result = session.call(method, text_document_params(&document.uri));
        let _ = session.close();
        result
    }
}

pub(crate) fn run_completion(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let (session, document, position) =
        runner.open_position_session(args, "completion expects one file position")?;
    let mut params = position_params(&document.uri, position);
    params["context"] = json!({"triggerKind": 1});
    let result: Option<CompletionList> = session.call("textDocument/completion", params)?;
    let _ = session.close();
    // Go decodes into a CompletionList value, so null prints as the zero list.
    match result {
        Some(result) => runner.write_json(&result),
        None => runner.write_json(&json!({"isIncomplete": false, "items": null})),
    }
}

pub(crate) fn run_definition(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<Location>> = runner.position_request(args, "textDocument/definition")?;
    runner.write_json(&result)
}

pub(crate) fn run_implementation(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<Location>> =
        runner.position_request(args, "textDocument/implementation")?;
    runner.write_json(&result)
}

pub(crate) fn run_references(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let value = require_one_argument(args, "references expects one file position")?;
    let target = parse_position_target(&value)?;
    let session = runner.connect()?;
    let document = session.open_document(&target.path)?;
    let mut params = position_params(&document.uri, target.position);
    params["context"] = json!({"includeDeclaration": true});
    let result: Option<Vec<Location>> = session.call("textDocument/references", params)?;
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_hover(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Hover> = runner.position_request(args, "textDocument/hover")?;
    match result {
        Some(hover) if !runner.json => runner.write_text(&format!("{}\n", hover.contents.value)),
        result => runner.write_json(&result),
    }
}

pub(crate) fn run_signature(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<SignatureHelp> =
        runner.position_request(args, "textDocument/signatureHelp")?;
    runner.write_json(&result)
}

pub(crate) fn run_call_hierarchy(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let (session, document, position) =
        runner.open_position_session(args, "call-hierarchy expects one file position")?;
    let roots: Option<Vec<CallHierarchyItem>> = session.call(
        "textDocument/prepareCallHierarchy",
        position_params(&document.uri, position),
    )?;
    #[derive(Serialize)]
    struct Calls {
        item: CallHierarchyItem,
        incoming: Option<Vec<CallHierarchyIncomingCall>>,
        outgoing: Option<Vec<CallHierarchyOutgoingCall>>,
    }
    let mut result = Vec::new();
    for root in roots.unwrap_or_default() {
        let params = json!({"item": root});
        let incoming = session.call("callHierarchy/incomingCalls", params.clone())?;
        let outgoing = session.call("callHierarchy/outgoingCalls", params)?;
        result.push(Calls {
            item: root,
            incoming,
            outgoing,
        });
    }
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_type_hierarchy(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let (session, document, position) =
        runner.open_position_session(args, "type-hierarchy expects one file position")?;
    let roots: Option<Vec<TypeHierarchyItem>> = session.call(
        "textDocument/prepareTypeHierarchy",
        position_params(&document.uri, position),
    )?;
    #[derive(Serialize)]
    struct Hierarchy {
        item: TypeHierarchyItem,
        supertypes: Option<Vec<TypeHierarchyItem>>,
        subtypes: Option<Vec<TypeHierarchyItem>>,
    }
    let mut result = Vec::new();
    for root in roots.unwrap_or_default() {
        let params = json!({"item": root});
        let supertypes = session.call("typeHierarchy/supertypes", params.clone())?;
        let subtypes = session.call("typeHierarchy/subtypes", params)?;
        result.push(Hierarchy {
            item: root,
            supertypes,
            subtypes,
        });
    }
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_symbols(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<DocumentSymbol>> =
        runner.document_request(args, "textDocument/documentSymbol")?;
    runner.write_json(&result)
}

pub(crate) fn run_workspace_symbols(
    runner: &Runner,
    fresh: bool,
    query: &str,
) -> Result<(), CliError> {
    if !fresh && let Some(result) = cached_workspace_symbols(runner, query)? {
        return runner.write_json(&result);
    }
    let session = runner.connect()?;
    let result: Option<Vec<SymbolInformation>> =
        session.call("workspace/symbol", json!({"query": query}))?;
    let _ = session.close();
    runner.write_json(&result)
}

/// The fast path: query a ready, current FTS catalog without a session.
fn cached_workspace_symbols(
    runner: &Runner,
    query: &str,
) -> Result<Option<Vec<SymbolInformation>>, CliError> {
    let root = runner.workspace_root()?;
    runner.require_supported_project(&root)?;
    let cache_dir = swlsp_app::project_cache_folder(&root).map_err(CliError::fail)?;
    let current = swlsp_indexer::cache_version_current(&cache_dir).map_err(|error| {
        CliError::fail(format!("check workspace symbol cache version: {error}"))
    })?;
    if !current {
        return Ok(None);
    }
    // An absent catalog is a normal cache-upgrade path; the production
    // session initializes and populates it.
    let Ok(catalog) =
        swlsp_indexer::WorkspaceSymbolCatalog::open_read_only(cache_dir.join("indexes.db"))
    else {
        return Ok(None);
    };
    let ready = catalog
        .ready()
        .map_err(|error| CliError::fail(error.to_string()))?;
    if !ready {
        let _ = catalog.close();
        return Ok(None);
    }
    let symbols = catalog
        .query(query, 500)
        .map_err(|error| CliError::fail(error.to_string()))?;
    let _ = catalog.close();
    if symbols.is_empty() {
        // Framework-only domains still use the production providers.
        return Ok(None);
    }
    let position = |line: i32, character: i32| Position {
        line: line.max(0) as u32,
        character: character.max(0) as u32,
    };
    Ok(Some(
        symbols
            .into_iter()
            .map(|symbol| SymbolInformation {
                name: symbol.name,
                kind: serde_json::from_value::<SymbolKind>(json!(symbol.kind.0))
                    .unwrap_or(SymbolKind::FILE),
                container_name: symbol.container_name,
                location: Location {
                    uri: swlsp_uriutil::file_uri(&symbol.path),
                    range: Range {
                        start: position(symbol.range.start.line, symbol.range.start.character),
                        end: position(symbol.range.end.line, symbol.range.end.character),
                    },
                },
                priority: 0,
            })
            .collect(),
    ))
}

pub(crate) fn run_highlights(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<DocumentHighlight>> =
        runner.position_request(args, "textDocument/documentHighlight")?;
    runner.write_json(&result)
}

pub(crate) fn run_folding_ranges(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<FoldingRange>> =
        runner.document_request(args, "textDocument/foldingRange")?;
    runner.write_json(&result)
}

pub(crate) fn run_links(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<DocumentLink>> =
        runner.document_request(args, "textDocument/documentLink")?;
    runner.write_json(&result)
}

#[derive(Serialize)]
struct DecodedSemanticToken {
    line: u32,
    character: u32,
    length: u32,
    #[serde(rename = "type")]
    kind: String,
    #[serde(skip_serializing_if = "is_zero")]
    modifiers: u32,
}

fn is_zero(value: &u32) -> bool {
    *value == 0
}

pub(crate) fn run_semantic_tokens(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let encoded: Option<SemanticTokens> =
        runner.document_request(args, "textDocument/semanticTokens/full")?;
    let data = encoded.map(|tokens| tokens.data).unwrap_or_default();
    let mut tokens = Vec::with_capacity(data.len() / 5);
    let (mut line, mut character) = (0u32, 0u32);
    let mut index = 0;
    while index + 4 < data.len() {
        let (line_delta, character_delta) = (data[index], data[index + 1]);
        line = line.wrapping_add(line_delta);
        if line_delta == 0 {
            character = character.wrapping_add(character_delta);
        } else {
            character = character_delta;
        }
        let type_index = data[index + 3];
        let kind = swlsp_lsp_protocol::SEMANTIC_TOKEN_TYPES
            .get(type_index as usize)
            .map(|name| (*name).to_owned())
            .unwrap_or_else(|| format!("unknown({type_index})"));
        tokens.push(DecodedSemanticToken {
            line,
            character,
            length: data[index + 2],
            kind,
            modifiers: data[index + 4],
        });
        index += 5;
    }
    runner.write_json(&tokens)
}

pub(crate) fn run_inlay_hints(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let path = require_one_argument(args, "inlay-hints expects one file")?;
    let session = runner.connect()?;
    let document = session.open_document(&path)?;
    let line_index = swlsp_syntax::LineIndex::new(document.source.as_str());
    let (line, character) = line_index.position_utf16(document.source.len() as u32);
    let mut params = text_document_params(&document.uri);
    params["range"] = json!({
        "start": {"line": 0, "character": 0},
        "end": {"line": line, "character": character},
    });
    let result: Option<Vec<InlayHint>> = session.call("textDocument/inlayHint", params)?;
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_code_lens(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let path = require_one_argument(args, "codelens expects one file")?;
    let session = runner.connect()?;
    let document = session.open_document(&path)?;
    let mut result: Option<Vec<CodeLens>> =
        session.call("textDocument/codeLens", text_document_params(&document.uri))?;
    for lens in result.iter_mut().flatten() {
        if lens.command.is_some() || lens.data.is_none() {
            continue;
        }
        let resolved: Option<CodeLens> = session.call(
            "codeLens/resolve",
            serde_json::to_value(&*lens).unwrap_or(Value::Null),
        )?;
        *lens = resolved.unwrap_or_default();
    }
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_selection_ranges(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let value = require_one_argument(args, "selection-ranges expects one file position")?;
    let target = parse_position_target(&value)?;
    let session = runner.connect()?;
    let document = session.open_document(&target.path)?;
    let mut params = text_document_params(&document.uri);
    params["positions"] = json!([target.position]);
    let result: Option<Vec<SelectionRange>> =
        session.call("textDocument/selectionRange", params)?;
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_linked_editing(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<LinkedEditingRanges> =
        runner.position_request(args, "textDocument/linkedEditingRange")?;
    runner.write_json(&result)
}

pub(crate) fn run_colors(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let result: Option<Vec<ColorInformation>> =
        runner.document_request(args, "textDocument/documentColor")?;
    runner.write_json(&result)
}
