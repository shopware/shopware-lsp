//! PHP completion.

use std::collections::HashSet;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{CompletionItem, CompletionItemKind, InsertTextFormat, TextEdit};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_NAME, PHP_SCOPED_CALL, PHP_STRING, PHP_VARIABLE,
    TK_BLOCK_COMMENT, TK_LINE_COMMENT, TK_STRING, TK_VARIABLE,
};
use swlsp_php::PhpContext;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{
    Attribute, AttributeValue, AttributeValueKind, CallContract, CallValue, CallValueKind, Flags,
    Parameter, Snapshot, Symbol, SymbolId, SymbolKind, SymbolView, attribute_named,
};
use swlsp_php_types::Type;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::lowercase;

use crate::Provider;
use crate::completion_imports::GlobalCompletionContext;
use crate::helpers::{
    active_argument, compact_name, completion_kind, completion_label, completion_rank,
    completion_snippet, direct_nodes, first_direct_kind, format_symbol, is_php, markdown,
    member_expression, member_visible, name_context_at, object_creation_at, range_from_text,
    static_receiver_type, type_detail,
};

pub(crate) fn request_offset(
    line_index: &LineIndex,
    position: swlsp_lsp_protocol::Position,
) -> u32 {
    line_index.offset_utf16(position.line, position.character)
}

impl Provider {
    pub(crate) fn completions(&self, request: &CompletionRequest) -> Vec<CompletionItem> {
        if !is_php(&request.params.text_document.uri) || request.root.is_none() {
            return Vec::new();
        }
        let Some(context) = self.php_context(request) else {
            return Vec::new();
        };
        if let Some(node) = &request.node
            && let Some((mode, reference)) = assistant_class_reference(&context, node)
        {
            return self.assistant_class_completions(mode, reference, request.line_index());
        }
        let items = expected_argument_completions(&context, request);
        if !items.is_empty() {
            return items;
        }
        if completion_suppressed(request) {
            return Vec::new();
        }

        if let Some((expression, is_static)) = member_expression(request.node.as_ref())
            && let Some(first) = expression.children().next()
        {
            let mut receiver = context.document.type_of(&first).ty;
            if receiver.is_unknown() && is_static && first.kind() == PHP_NAME {
                receiver = static_receiver_type(
                    &context,
                    &compact_name(&first.text().to_string()),
                    first.text_range().start().into(),
                );
            }
            if !receiver.is_unknown() {
                return member_completions(&context, &receiver, is_static);
            }
        }
        self.scope_completions(&context, request, !completion_is_variable_only(request))
    }

    fn assistant_class_completions(
        &self,
        mode: AssistantClassMode,
        reference: TextRange,
        line_index: Option<&LineIndex>,
    ) -> Vec<CompletionItem> {
        let (Some(index), Some(line_index)) = (&self.index, line_index) else {
            return Vec::new();
        };
        let replacement = range_from_text(line_index, reference);
        let mut seen = HashSet::new();
        let mut items = Vec::new();
        for symbol in index.class_symbols_view().iter() {
            if !assistant_class_kind_allowed(mode, symbol.kind) {
                continue;
            }
            let label = symbol
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&symbol.fully_qualified);
            if label.is_empty() || !seen.insert(lowercase(label)) {
                continue;
            }
            items.push(CompletionItem {
                label: label.to_owned(),
                kind: Some(completion_kind(symbol)),
                detail: format_symbol(symbol),
                deprecated: symbol.flags.has(Flags::DEPRECATED),
                text_edit: Some(TextEdit::new(replacement, label)),
                documentation: (!symbol.doc_summary().is_empty())
                    .then(|| markdown(symbol.doc_summary())),
                ..CompletionItem::default()
            });
        }
        items
    }

    fn scope_completions(
        &self,
        context: &PhpContext,
        request: &CompletionRequest,
        include_globals: bool,
    ) -> Vec<CompletionItem> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let offset = request_offset(line_index, request.params.position);
        let document = &context.document;
        let mut seen = HashSet::new();
        let mut items = Vec::new();

        if let Some(mut scope) = document.scope_at(offset) {
            loop {
                for id in scope.all_symbol_ids(&document.symbols) {
                    let Some(symbol) = context.snapshot.symbol_view(id.as_str()) else {
                        continue;
                    };
                    if !matches!(symbol.kind(), SymbolKind::Local | SymbolKind::Parameter) {
                        continue;
                    }
                    let symbol = symbol.materialize();
                    if !seen.insert(symbol.name.to_string()) {
                        continue;
                    }
                    items.push(CompletionItem {
                        label: symbol.name.to_string(),
                        kind: Some(CompletionItemKind::VARIABLE),
                        detail: type_detail(&symbol.ty),
                        sort_text: format!("00-{}", lowercase(&symbol.name)),
                        ..CompletionItem::default()
                    });
                }
                if scope.id == scope.parent || scope.parent as usize >= document.scopes.len() {
                    break;
                }
                scope = &document.scopes[scope.parent as usize];
            }
        }

        if !include_globals {
            return items;
        }
        let project = self.index.as_ref().and_then(|index| index.project());
        let completion_context =
            GlobalCompletionContext::new(Some(request), Some(document), project.as_deref(), offset);
        for view in context.snapshot.global_symbol_views() {
            if !view.container().is_empty()
                || !matches!(
                    view.kind(),
                    SymbolKind::Class
                        | SymbolKind::Interface
                        | SymbolKind::Trait
                        | SymbolKind::Enum
                        | SymbolKind::Function
                )
            {
                continue;
            }
            let key = format!(
                "{}:{}",
                view.kind() as u8,
                lowercase(view.fully_qualified())
            );
            if !seen.insert(key) {
                continue;
            }
            let symbol = completion_symbol(&view);
            let mut label = symbol.fully_qualified.to_string();
            let mut insert_text = label.clone();
            let mut imported = false;
            let mut class_edit = None;
            if symbol.is_class_like() {
                let plan = completion_context.class_plan(&symbol);
                label = symbol.name.to_string();
                insert_text = plan.qualifier;
                imported = plan.imported;
                class_edit = plan.edit;
            }
            if label.is_empty() {
                label = symbol.name.to_string();
            }
            let mut item = CompletionItem {
                label,
                filter_text: format!("{} {}", symbol.name, symbol.fully_qualified),
                kind: Some(completion_kind(&symbol)),
                detail: format_symbol(&symbol),
                deprecated: symbol.flags.has(Flags::DEPRECATED),
                sort_text: completion_context.sort_text(&symbol, imported),
                ..CompletionItem::default()
            };
            if symbol.is_class_like() {
                item.text_edit = Some(TextEdit::new(
                    range_from_text(line_index, completion_context.replacement),
                    insert_text,
                ));
                if let Some(edit) = class_edit {
                    item.additional_text_edits = vec![edit];
                }
            } else if symbol.kind == SymbolKind::Function {
                item.insert_text = completion_snippet(&symbol);
                item.insert_text_format = Some(InsertTextFormat::SNIPPET);
            }
            items.push(item);
        }
        items.sort_by(|left, right| left.sort_text.cmp(&right.sort_text));
        items
    }
}

/// The declaration a global completion item renders. Class-like items only
/// need resident summary data (name, flags, hierarchy names), so they skip
/// the lazy detail load a full materialization triggers for every indexed
/// file; functions need parameters and the return type. The list shows
/// return types as indexed: recomputing inferred ones would analyze every
/// file declaring such a function.
fn completion_symbol(view: &SymbolView<'_>) -> Symbol {
    if !view.kind().is_class_like() {
        return view.materialize_stored();
    }
    let (_, extends, implements) = view.hierarchy_names();
    let mut symbol = Symbol {
        id: view.symbol_id(),
        kind: view.kind(),
        name: view.name().into(),
        fully_qualified: view.fully_qualified().into(),
        container: SymbolId::new(view.container()),
        flags: view.flags(),
        ..Symbol::default()
    };
    symbol.set_extends(extends.to_vec());
    symbol.set_implements(implements.to_vec());
    symbol
}

fn expected_argument_completions(
    context: &PhpContext,
    request: &CompletionRequest,
) -> Vec<CompletionItem> {
    let Some(line_index) = request.line_index() else {
        return Vec::new();
    };
    let node = request.node.as_ref();
    let mut constructor = false;
    let call = match node.and_then(query::call_at) {
        Some(call) => call,
        None => match object_creation_at(node) {
            Some(call) => {
                constructor = true;
                call
            }
            None => return Vec::new(),
        },
    };
    let offset = request_offset(line_index, request.params.position);
    let (argument_index, argument_name) = active_argument(&call, offset);
    let snapshot: &Snapshot = &context.snapshot;
    let mut values: Vec<CallValue> = Vec::new();
    let visit_contract = |contract: &CallContract, values: &mut Vec<CallValue>| {
        for expected in &contract.expected_arguments {
            if usize::from(expected.argument) == argument_index {
                values.extend(expected.values.iter().cloned());
            }
        }
    };
    let visit_symbol = |parameters: &[Parameter], values: &mut Vec<CallValue>| {
        let Some(parameter) = callable_parameter_at(parameters, argument_index, &argument_name)
        else {
            return;
        };
        let Some(attribute) = attribute_named(&parameter.attributes, "ExpectedValues") else {
            return;
        };
        append_expected_value_attribute_values(values, attribute, snapshot);
    };
    if constructor {
        let Some(name_node) = first_direct_kind(&call, PHP_NAME) else {
            return Vec::new();
        };
        let class_name = name_context_at(&context.document, name_node.text_range().start().into())
            .resolve_class(&compact_name(&name_node.text().to_string()));
        let receiver = Type::named(&class_name, Vec::new());
        snapshot.visit_method_call_contracts(&receiver, "__construct", |contract| {
            visit_contract(contract, &mut values);
            true
        });
        for member in MemberResolver::new(snapshot).methods(&receiver, "__construct") {
            visit_symbol(&member.symbol.parameters, &mut values);
        }
    } else {
        let nodes = direct_nodes(&call);
        match call.kind() {
            PHP_FUNCTION_CALL => {
                let Some(first) = nodes.first().filter(|first| first.kind() == PHP_NAME) else {
                    return Vec::new();
                };
                name_context_at(&context.document, call.text_range().start().into())
                    .visit_function_names(&compact_name(&first.text().to_string()), |name| {
                        snapshot.visit_function_call_contracts(name, |contract| {
                            visit_contract(contract, &mut values);
                            true
                        });
                        snapshot.visit_function_views(name, |view| {
                            visit_symbol(&view.materialize().parameters, &mut values);
                            true
                        });
                        true
                    });
            }
            PHP_MEMBER_CALL | PHP_SCOPED_CALL => {
                if nodes.len() < 2 {
                    return Vec::new();
                }
                let is_static = call.kind() == PHP_SCOPED_CALL;
                let mut receiver = context.document.type_of(&nodes[0]).ty;
                if receiver.is_unknown() && is_static && nodes[0].kind() == PHP_NAME {
                    receiver = static_receiver_type(
                        context,
                        &compact_name(&nodes[0].text().to_string()),
                        nodes[0].text_range().start().into(),
                    );
                }
                let name = compact_name(&nodes[1].text().to_string());
                snapshot.visit_method_call_contracts(&receiver, &name, |contract| {
                    visit_contract(contract, &mut values);
                    true
                });
                for member in MemberResolver::new(snapshot).methods(&receiver, &name) {
                    visit_symbol(&member.symbol.parameters, &mut values);
                }
            }
            _ => {}
        }
    }
    if values.is_empty() {
        return Vec::new();
    }
    let at = TextSize::from(offset);
    let mut replacement = TextRange::empty(at);
    if let Some(expression) = query::argument_expression(&call, argument_index) {
        let range = expression.text_range();
        if at >= range.start() && at <= range.end() {
            replacement = expression.range_trimmed_trivia();
        }
    }
    let range = range_from_text(line_index, replacement);
    let mut seen = HashSet::with_capacity(values.len());
    let mut items = Vec::with_capacity(values.len());
    for value in &values {
        if value.expression.is_empty() || !seen.insert(lowercase(&value.expression)) {
            continue;
        }
        let kind = if matches!(value.kind, CallValueKind::String | CallValueKind::Number) {
            CompletionItemKind::VALUE
        } else {
            CompletionItemKind::CONSTANT
        };
        items.push(CompletionItem {
            label: value.label().to_owned(),
            kind: Some(kind),
            detail: "Expected by PHP metadata".to_owned(),
            sort_text: format!("00-{}", value.label()),
            text_edit: Some(TextEdit::new(range, value.expression.to_string())),
            ..CompletionItem::default()
        });
    }
    items
}

fn callable_parameter_at<'a>(
    parameters: &'a [Parameter],
    argument_index: usize,
    argument_name: &str,
) -> Option<&'a Parameter> {
    if !argument_name.is_empty() {
        let name = argument_name.strip_prefix('$').unwrap_or(argument_name);
        return parameters.iter().find(|parameter| {
            swlsp_textutil::eq_ignore_case(
                parameter.name.strip_prefix('$').unwrap_or(&parameter.name),
                name,
            )
        });
    }
    if let Some(parameter) = parameters.get(argument_index) {
        return Some(parameter);
    }
    parameters
        .last()
        .filter(|parameter| parameter.flags.has(Flags::VARIADIC))
}

fn append_expected_value_attribute_values(
    result: &mut Vec<CallValue>,
    attribute: &Attribute,
    snapshot: &Snapshot,
) {
    for (name, index) in [("values", 0), ("flags", 1)] {
        let Some(value) = attribute.argument(name, index) else {
            continue;
        };
        if value.kind != AttributeValueKind::Array {
            continue;
        }
        for item in &value.items {
            if let Some(converted) = expected_attribute_call_value(&item.value) {
                result.push(converted);
            }
        }
    }
    for (name, index) in [("valuesFromClass", 2), ("flagsFromClass", 3)] {
        if let Some(value) = attribute.argument(name, index) {
            append_expected_values_from_class(result, value, snapshot);
        }
    }
}

fn expected_attribute_call_value(value: &AttributeValue) -> Option<CallValue> {
    if value.expression.is_empty() {
        return None;
    }
    let kind = match value.kind {
        AttributeValueKind::String => CallValueKind::String,
        AttributeValueKind::Integer | AttributeValueKind::Float => CallValueKind::Number,
        AttributeValueKind::Constant => CallValueKind::Constant,
        AttributeValueKind::ClassConstant => CallValueKind::ClassConstant,
        AttributeValueKind::Bool | AttributeValueKind::Null | AttributeValueKind::Expression => {
            CallValueKind::Expression
        }
        _ => return None,
    };
    Some(CallValue {
        kind,
        value: value.value.clone(),
        expression: value.expression.clone(),
    })
}

fn append_expected_values_from_class(
    result: &mut Vec<CallValue>,
    value: &AttributeValue,
    snapshot: &Snapshot,
) {
    let class_name: &str = match value.kind {
        AttributeValueKind::ClassConstant => {
            value.value.strip_suffix("::class").unwrap_or(&value.value)
        }
        AttributeValueKind::String | AttributeValueKind::Constant => &value.value,
        _ => return,
    };
    let class_name = class_name.trim_matches('\\');
    if class_name.is_empty() {
        return;
    }
    snapshot.visit_class_views(class_name, |class| {
        let class_id = class.id();
        let fully_qualified = class.fully_qualified();
        for member in snapshot.member_views_of(class_id) {
            if !matches!(
                member.kind(),
                SymbolKind::ClassConstant | SymbolKind::EnumCase
            ) {
                continue;
            }
            let expression = format!("\\{}::{}", fully_qualified, member.name());
            result.push(CallValue {
                kind: CallValueKind::ClassConstant,
                value: expression[1..].into(),
                expression: expression.into(),
            });
        }
        true
    });
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum AssistantClassMode {
    Classes,
    Interfaces,
    ClassesAndInterfaces,
}

pub(crate) fn assistant_class_reference(
    context: &PhpContext,
    node: &SyntaxNode,
) -> Option<(AssistantClassMode, TextRange)> {
    for (tag, mode) in [
        ("ClassInterface", AssistantClassMode::ClassesAndInterfaces),
        ("Interface", AssistantClassMode::Interfaces),
        ("Class", AssistantClassMode::Classes),
    ] {
        if let Some(reference) = swlsp_php::assistant_argument_reference(context, node, tag) {
            return Some((mode, reference));
        }
    }
    None
}

pub(crate) fn assistant_class_kind_allowed(mode: AssistantClassMode, kind: SymbolKind) -> bool {
    match mode {
        AssistantClassMode::Interfaces => kind == SymbolKind::Interface,
        AssistantClassMode::ClassesAndInterfaces => {
            matches!(kind, SymbolKind::Class | SymbolKind::Interface)
        }
        AssistantClassMode::Classes => kind == SymbolKind::Class,
    }
}

fn member_completions(
    context: &PhpContext,
    receiver: &Type,
    is_static: bool,
) -> Vec<CompletionItem> {
    let resolved = MemberResolver::new(&context.snapshot).all(receiver);
    let mut seen = HashSet::with_capacity(resolved.len());
    let mut items = Vec::with_capacity(resolved.len());
    for member in &resolved {
        let symbol = &member.symbol;
        if !member_visible(context, symbol) {
            continue;
        }
        if is_static {
            if matches!(symbol.kind, SymbolKind::Method | SymbolKind::Property)
                && !symbol.flags.has(Flags::STATIC)
            {
                continue;
            }
        } else if matches!(
            symbol.kind,
            SymbolKind::ClassConstant | SymbolKind::EnumCase
        ) {
            continue;
        }
        if !seen.insert(format!("{}:{}", symbol.kind as u8, lowercase(&symbol.name))) {
            continue;
        }
        let mut item = CompletionItem {
            label: completion_label(symbol),
            kind: Some(completion_kind(symbol)),
            detail: format_symbol(symbol),
            deprecated: symbol.flags.has(Flags::DEPRECATED),
            sort_text: format!("{:02}-{}", completion_rank(symbol), symbol.name),
            documentation: (!symbol.doc_summary().is_empty())
                .then(|| markdown(symbol.doc_summary())),
            ..CompletionItem::default()
        };
        if symbol.is_function_like() {
            item.insert_text = completion_snippet(symbol);
            item.insert_text_format = Some(InsertTextFormat::SNIPPET);
        } else if is_static && symbol.kind == SymbolKind::Property {
            item.insert_text =
                format!("${}", symbol.name.strip_prefix('$').unwrap_or(&symbol.name));
        } else {
            item.insert_text = symbol.name.to_string();
        }
        items.push(item);
    }
    items.sort_by(|left, right| left.sort_text.cmp(&right.sort_text));
    items
}

fn completion_suppressed(request: &CompletionRequest) -> bool {
    if let Some(token) = &request.token
        && matches!(token.kind(), TK_LINE_COMMENT | TK_BLOCK_COMMENT | TK_STRING)
    {
        return true;
    }
    request
        .node
        .as_ref()
        .is_some_and(|node| node.ancestors().any(|node| node.kind() == PHP_STRING))
}

fn completion_is_variable_only(request: &CompletionRequest) -> bool {
    request
        .token
        .as_ref()
        .is_some_and(|token| token.kind() == TK_VARIABLE)
        || request
            .node
            .as_ref()
            .is_some_and(|node| node.kind() == PHP_VARIABLE)
}
