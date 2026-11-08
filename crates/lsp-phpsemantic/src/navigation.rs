//! Definition, hover, references, rename and signature help (port of
//! `definition.go`, `hover.go`, `reference.go`, `rename.go` and
//! `signature.go`).

use std::collections::{BTreeMap, HashSet};

use swlsp_lsp::{
    BoxError, DefinitionRequest, HoverRequest, ReferenceRequest, RenameRequest,
    SignatureHelpRequest,
};
use swlsp_lsp_protocol::{
    Hover, Location, ParameterInformation, SignatureHelp, SignatureInformation, TextEdit,
    WorkspaceEdit,
};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_NAME, PHP_SCOPED_CALL};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Symbol};
use swlsp_php_types::{Type, equal_fold};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};

use crate::Provider;
use crate::completion::{assistant_class_kind_allowed, assistant_class_reference, request_offset};
use crate::diagnostics::{format_deprecation_hover, reference_candidates};
use crate::helpers::{
    LocationCache, active_argument, compact_name, direct_nodes, first_direct_kind,
    format_parameter, format_symbol, is_php, locations_for_symbols, markdown, name_context_at,
    object_creation_at, range_from_text, static_receiver_type, symbol_range_at,
    valid_php_identifier,
};

impl Provider {
    pub(crate) fn definition(&self, request: &DefinitionRequest) -> Vec<Location> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !is_php(&request.params.text_document.uri) {
            return Vec::new();
        }
        let Some(context) = self.php_context(request) else {
            return Vec::new();
        };
        if let Some(node) = &request.node
            && let Some((mode, _)) = assistant_class_reference(&context, node)
        {
            let value = query::string_value(node);
            let name = value.trim();
            let name = name.strip_prefix('\\').unwrap_or(name);
            let symbols: Vec<Symbol> = context
                .snapshot
                .classes(name)
                .into_iter()
                .filter(|symbol| {
                    assistant_class_kind_allowed(mode, symbol.kind)
                        && !symbol.flags.has(Flags::INTERNAL)
                })
                .collect();
            return locations_for_symbols(&symbols, request.document.as_deref());
        }
        let offset = request_offset(line_index, request.params.position);
        let mut symbols = Vec::new();
        if let Some(reference) = swlsp_php::reference_at(&context.document, offset) {
            symbols.extend(reference_candidates(&context.snapshot, reference));
        }
        if symbols.is_empty()
            && let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset)
        {
            symbols.push(symbol);
        }
        locations_for_symbols(&symbols, request.document.as_deref())
    }

    pub(crate) fn hover(&self, request: &HoverRequest) -> Option<Hover> {
        let line_index = request.line_index()?;
        if !is_php(&request.params.text_document.uri) {
            return None;
        }
        let context = self.php_context(request)?;
        let offset = request_offset(line_index, request.params.position);
        if let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset) {
            let mut value = format!("```php\n{}\n```", format_symbol(&symbol));
            if !symbol.doc_summary().is_empty() {
                value.push_str("\n\n");
                value.push_str(symbol.doc_summary());
            }
            if symbol.flags.has(Flags::DEPRECATED) {
                value.push_str(&format_deprecation_hover(&symbol));
            }
            let range = symbol_range_at(&context.document, offset);
            return Some(Hover {
                contents: markdown(value),
                range: Some(range_from_text(line_index, range)),
            });
        }

        for current in request.node.iter().flat_map(|node| node.ancestors()) {
            let fact = context.document.type_of(&current);
            if fact.ty.is_unknown() {
                continue;
            }
            let mut value = format!("```php\n{}\n```", fact.ty);
            if !fact.reason.is_empty() {
                value.push_str("\n\n");
                value.push_str(&fact.reason);
            }
            return Some(Hover {
                contents: markdown(value),
                range: Some(range_from_text(line_index, current.range_trimmed_trivia())),
            });
        }
        None
    }

    pub(crate) fn references(&self, request: &ReferenceRequest) -> Vec<Location> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !is_php(&request.params.text_document.uri) {
            return Vec::new();
        }
        let Some(context) = self.php_context(request) else {
            return Vec::new();
        };
        let offset = request_offset(line_index, request.params.position);
        let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset)
        else {
            return Vec::new();
        };
        let mut cache = LocationCache::new(request.document.as_deref());
        let mut locations = Vec::new();
        if request.params.context.include_declaration {
            locations.push(cache.symbol(&symbol));
        }
        for reference in context.snapshot.references_to(symbol.id.as_str()) {
            locations.push(cache.text_range(
                &reference.path,
                TextRange::new(
                    TextSize::from(reference.range_start),
                    TextSize::from(reference.range_end),
                ),
            ));
        }
        locations
    }

    pub(crate) fn rename_symbol(
        &self,
        request: &RenameRequest,
    ) -> Result<Option<WorkspaceEdit>, BoxError> {
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        if !is_php(&request.params.text_document.uri) {
            return Ok(None);
        }
        let Some(context) = self.php_context(request) else {
            return Ok(None);
        };
        let offset = request_offset(line_index, request.params.position);
        let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset)
        else {
            return Ok(None);
        };
        if symbol.flags.has(Flags::INTERNAL) || symbol.path.starts_with("phpstub://") {
            return Err(format!("cannot rename internal PHP symbol {}", symbol.name).into());
        }
        let trimmed = request.params.new_name.trim();
        let new_name = trimmed.strip_prefix('$').unwrap_or(trimmed);
        if !valid_php_identifier(new_name) {
            return Err(format!(
                "{} is not a valid PHP identifier",
                go_quote(&request.params.new_name)
            )
            .into());
        }

        let mut cache = LocationCache::new(request.document.as_deref());
        let mut changes: BTreeMap<String, Vec<TextEdit>> = BTreeMap::new();
        let mut seen = HashSet::new();
        let mut add = |path: &str, range: TextRange| {
            if !seen.insert((path.to_owned(), range)) {
                return;
            }
            let location = cache.text_range(path, range);
            let replacement = cache.rename_text(path, range, new_name);
            changes
                .entry(location.uri)
                .or_default()
                .push(TextEdit::new(location.range, replacement));
        };
        add(&symbol.path, symbol.selection_range);
        for reference in context.snapshot.references_to(symbol.id.as_str()) {
            add(
                &reference.path,
                TextRange::new(
                    TextSize::from(reference.range_start),
                    TextSize::from(reference.range_end),
                ),
            );
        }
        Ok(Some(WorkspaceEdit {
            changes,
            ..WorkspaceEdit::default()
        }))
    }

    pub(crate) fn signature_help(&self, request: &SignatureHelpRequest) -> Option<SignatureHelp> {
        let line_index = request.line_index()?;
        if !is_php(&request.params.text_document.uri) {
            return None;
        }
        let context = self.php_context(request)?;
        let node = request.node.as_ref();
        let mut candidates: Vec<Symbol> = Vec::new();
        let call = match node.and_then(query::call_at) {
            Some(call) => {
                let nodes = direct_nodes(&call);
                match call.kind() {
                    PHP_FUNCTION_CALL => {
                        if let Some(first) = nodes.first().filter(|first| first.kind() == PHP_NAME)
                        {
                            name_context_at(&context.document, call.text_range().start().into())
                                .visit_function_names(
                                    &compact_name(&first.text().to_string()),
                                    |name| {
                                        candidates.extend(context.snapshot.functions(name));
                                        candidates.is_empty()
                                    },
                                );
                        }
                    }
                    PHP_MEMBER_CALL | PHP_SCOPED_CALL if nodes.len() >= 2 => {
                        let is_static = call.kind() == PHP_SCOPED_CALL;
                        let mut receiver = context.document.type_of(&nodes[0]).ty;
                        if receiver.is_unknown() && is_static && nodes[0].kind() == PHP_NAME {
                            receiver = static_receiver_type(
                                &context,
                                &compact_name(&nodes[0].text().to_string()),
                                nodes[0].text_range().start().into(),
                            );
                        }
                        let name = compact_name(&nodes[1].text().to_string());
                        candidates.extend(
                            MemberResolver::new(&context.snapshot)
                                .methods(&receiver, &name)
                                .into_iter()
                                .map(|member| member.symbol),
                        );
                    }
                    _ => {}
                }
                Some(call)
            }
            None => {
                let object = object_creation_at(node);
                if let Some(object) = &object
                    && let Some(name_node) = first_direct_kind(object, PHP_NAME)
                {
                    let name =
                        name_context_at(&context.document, name_node.text_range().start().into())
                            .resolve_class(&compact_name(&name_node.text().to_string()));
                    candidates.extend(
                        MemberResolver::new(&context.snapshot)
                            .methods(&Type::named(&name, Vec::new()), "__construct")
                            .into_iter()
                            .map(|member| member.symbol),
                    );
                }
                object
            }
        };
        let call = call?;
        if candidates.is_empty() {
            return None;
        }

        let offset = request_offset(line_index, request.params.position);
        let (active_argument, active_name) = active_argument(&call, offset);
        let mut result = SignatureHelp {
            signatures: Vec::with_capacity(candidates.len()),
            active_parameter: active_argument as i32,
            ..SignatureHelp::default()
        };
        let mut seen = HashSet::with_capacity(candidates.len());
        for candidate in &candidates {
            if !seen.insert(candidate.id.clone()) {
                continue;
            }
            let mut active_parameter = active_argument;
            if !active_name.is_empty()
                && let Some(index) = candidate.parameters.iter().position(|parameter| {
                    equal_fold(
                        parameter.name.strip_prefix('$').unwrap_or(&parameter.name),
                        &active_name,
                    )
                })
            {
                active_parameter = index;
            }
            result.signatures.push(SignatureInformation {
                label: format_symbol(candidate),
                active_parameter: active_parameter as i32,
                documentation: (!candidate.doc_summary().is_empty())
                    .then(|| markdown(candidate.doc_summary())),
                parameters: candidate
                    .parameters
                    .iter()
                    .map(|parameter| ParameterInformation {
                        label: format_parameter(parameter),
                        documentation: None,
                    })
                    .collect(),
            });
        }
        Some(result)
    }
}

/// Go `%q` for the identifier error message.
fn go_quote(value: &str) -> String {
    swlsp_php_types::go_quote(value)
}
