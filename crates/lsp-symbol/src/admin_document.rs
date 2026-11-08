//! Live hierarchical outline for Administration component definitions and
//! their Twig templates (port of `admin_document_symbol.go`). The open CST is
//! authoritative so unsaved declarations appear without a workspace reindex.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminSourceRange, ComponentDefinition, VueComponentMemberKind,
    VueComponentProp, canonical_event_name, component_definition_object, javascript_deprecation,
    parse_component_object, slot_declaration, vue_object_binding_fields,
    vue_prop_reference_for_attribute,
};
use swlsp_lsp::{
    Context, DocumentSymbolProvider, DocumentSymbolRequest, ProviderResult, TextDocument,
    protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax as jssyntax;
use swlsp_parser_twig::ast::{AstNode, HtmlStartingTag, TwigBlock};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::filepath;

const COMPONENT_CALLS: &[&str] = &[
    "Component.register",
    "Shopware.Component.register",
    "Component.extend",
    "Shopware.Component.extend",
    "Component.override",
    "Shopware.Component.override",
];

pub struct AdminDocumentSymbolProvider {
    index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminDocumentSymbolProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { index }
    }
}

impl DocumentSymbolProvider for AdminDocumentSymbolProvider {
    fn get_document_symbols(
        &self,
        ctx: &Context,
        request: &DocumentSymbolRequest,
    ) -> ProviderResult<Vec<protocol::DocumentSymbol>> {
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&document.uri)?;
        if !is_administration_document_path(&path) {
            return Ok(Vec::new());
        }
        let mut result = match filepath::ext(&path).to_lowercase().as_str() {
            ".js" | ".ts" => {
                let definition_name = self.component_definition_name(&path)?;
                self.script_symbols(ctx, &path, &definition_name, document, &root)
            }
            ".twig" => self.template_symbols(ctx, &path, document, &root)?,
            ".vue" => {
                let definition_name = self.component_definition_name(&path)?;
                let mut result = self.script_symbols(ctx, &path, &definition_name, document, &root);
                result.extend(self.template_symbols(ctx, &path, document, &root)?);
                result
            }
            _ => return Ok(Vec::new()),
        };
        sort_document_symbols(&mut result);
        Ok(result)
    }
}

impl AdminDocumentSymbolProvider {
    fn script_symbols(
        &self,
        ctx: &Context,
        path: &str,
        definition_name: &str,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> Vec<protocol::DocumentSymbol> {
        let line_index = document.line_index();
        let source = document.source();
        let mut result = Vec::new();
        let mut covered_objects = HashSet::new();
        for call in jsquery::iter_calls(root, COMPONENT_CALLS) {
            if ctx.is_cancelled() {
                return result;
            }
            let call_name = jsquery::call_name(&call).unwrap_or_default();
            let name_node = jsquery::string_argument(&call, 0);
            let name = name_node
                .as_ref()
                .map(jsquery::string_value)
                .unwrap_or_default();
            if name.is_empty() {
                continue;
            }
            let mut definition_argument = 1;
            let mut detail = "Shopware Administration component".to_owned();
            if call_name.ends_with(".extend") {
                definition_argument = 2;
                let parent = jsquery::string_argument(&call, 1)
                    .as_ref()
                    .map(jsquery::string_value)
                    .unwrap_or_default();
                detail = format!("extends {parent}");
            } else if call_name.ends_with(".override") {
                detail = "component override".to_owned();
            }
            let object = jsquery::argument_expression(&call, definition_argument)
                .and_then(|expression| component_definition_object(&expression));
            if let Some(object) = &object {
                covered_objects.insert(object.range_trimmed_trivia());
            }
            result.push(protocol::DocumentSymbol {
                name,
                detail,
                kind: SymbolKind::CLASS,
                deprecated: !document_javascript_deprecation(&call).is_empty(),
                range: protocol_range(line_index, call.range_trimmed_trivia()),
                selection_range: javascript_name_range(name_node.as_ref(), source, line_index),
                children: component_definition_symbols(object.as_ref(), path, line_index, source),
            });
        }

        let exports = jsquery::export_defaults(root);
        for export in &exports {
            if ctx.is_cancelled() {
                return result;
            }
            let Some(object) = jsquery::export_default_expression(export)
                .and_then(|expression| component_definition_object(&expression))
            else {
                continue;
            };
            if covered_objects.contains(&object.range_trimmed_trivia()) {
                continue;
            }
            let selection = empty_at_start(object.range_trimmed_trivia());
            result.push(protocol::DocumentSymbol {
                name: definition_name.to_owned(),
                detail: "Shopware Administration component definition".to_owned(),
                kind: SymbolKind::CLASS,
                deprecated: !javascript_deprecation(export).is_empty(),
                range: protocol_range(line_index, export.range_trimmed_trivia()),
                selection_range: protocol_range(line_index, selection),
                children: component_definition_symbols(Some(&object), path, line_index, source),
            });
        }
        if filepath::ext(path).eq_ignore_ascii_case(".vue")
            && exports.is_empty()
            && let Some(index) = &self.index
            && let Ok(Some(definition)) = index.get_component_definition(path)
            && let Some(program) = jsquery::nodes(root, &[jssyntax::JS_PROGRAM]).first()
        {
            let range = program.range_trimmed_trivia();
            result.push(protocol::DocumentSymbol {
                name: definition_name.to_owned(),
                detail: "Shopware Administration script-setup component".to_owned(),
                kind: SymbolKind::CLASS,
                deprecated: false,
                range: protocol_range(line_index, range),
                selection_range: protocol_range(line_index, empty_at_start(range)),
                children: script_setup_document_symbols(&definition),
            });
        }
        result
    }

    fn component_definition_name(&self, path: &str) -> ProviderResult<String> {
        let fallback = administration_component_name_from_path(path);
        let Some(index) = &self.index else {
            return Ok(fallback);
        };
        let components = index.get_components_by_definition_path(path)?;
        let names: HashSet<&str> = components
            .iter()
            .filter(|component| !component.name.is_empty())
            .map(|component| component.name.as_str())
            .collect();
        if names.len() == 1 {
            return Ok(names.into_iter().next().unwrap_or_default().to_owned());
        }
        Ok(fallback)
    }

    fn template_symbols(
        &self,
        ctx: &Context,
        path: &str,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> ProviderResult<Vec<protocol::DocumentSymbol>> {
        let mut name = administration_component_name_from_template_path(path);
        if let Some(index) = &self.index
            && let Some(component) = index.get_component_by_template_path(path)?
            && !component.name.is_empty()
        {
            name = component.name;
        }
        let line_index = document.line_index();
        let source = document.source();
        let mut blocks: Vec<protocol::DocumentSymbol> = Vec::new();
        for node in twigquery::iterate_nodes(root, &[twigsyntax::TWIG_BLOCK]) {
            ctx.check()?;
            let Some(block) = TwigBlock::cast(node.clone()) else {
                continue;
            };
            let Some(name_token) = block.name() else {
                continue;
            };
            let block_name = twigquery::block_name(&node).unwrap_or_default();
            if block_name.is_empty() {
                continue;
            }
            let deprecation = swlsp_twig::block_deprecation(&node, source);
            let mut detail = "Twig block".to_owned();
            if !deprecation.is_empty() {
                detail.push_str(" · deprecated");
            }
            blocks.push(protocol::DocumentSymbol {
                name: block_name,
                detail,
                kind: SymbolKind::METHOD,
                deprecated: !deprecation.is_empty(),
                range: protocol_range(line_index, node.range_trimmed_trivia()),
                selection_range: protocol_range(line_index, name_token.text_range()),
                children: Vec::new(),
            });
        }
        let mut top_level_slots = Vec::new();
        for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
            ctx.check()?;
            let Some(tag) = HtmlStartingTag::cast(node.clone()) else {
                continue;
            };
            if tag.name().is_none_or(|name| name.text() != "slot") {
                continue;
            }
            let Some(slot) = slot_declaration(&tag, line_index) else {
                continue;
            };
            let display_name = slot.display_name();
            if display_name.is_empty() {
                continue;
            }
            let symbol = protocol::DocumentSymbol {
                name: display_name,
                detail: "Vue slot".to_owned(),
                kind: SymbolKind::PROPERTY,
                deprecated: false,
                range: protocol_range(line_index, node.range_trimmed_trivia()),
                selection_range: twig_slot_name_range(&tag, line_index),
                children: twig_slot_member_symbols(&tag, line_index),
            };
            match smallest_containing_document_symbol(&blocks, symbol.range) {
                Some(index) => blocks[index].children.push(symbol),
                None => top_level_slots.push(symbol),
            }
        }
        let mut children = blocks;
        children.extend(top_level_slots);
        let full_range = protocol_range(
            line_index,
            TextRange::new(TextSize::from(0), TextSize::of(source)),
        );
        Ok(vec![protocol::DocumentSymbol {
            name,
            detail: "Shopware Administration component template".to_owned(),
            kind: SymbolKind::CLASS,
            deprecated: false,
            range: full_range,
            selection_range: protocol::Range {
                start: full_range.start,
                end: full_range.start,
            },
            children,
        }])
    }
}

fn empty_at_start(range: TextRange) -> TextRange {
    TextRange::empty(range.start())
}

fn script_setup_document_symbols(
    definition: &ComponentDefinition,
) -> Vec<protocol::DocumentSymbol> {
    let props: HashMap<&str, &VueComponentProp> = definition
        .props
        .iter()
        .map(|prop| (prop.name.as_str(), prop))
        .collect();
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for member in &definition.members {
        let Some(selection) = source_protocol_range(&member.name_range) else {
            continue;
        };
        if !seen.insert(member.name.as_str()) {
            continue;
        }
        let prop = props.get(member.name.as_str()).copied();
        let mut kind = SymbolKind::FIELD;
        let mut detail = member.type_.clone();
        match member.kind {
            VueComponentMemberKind::Prop => {
                kind = SymbolKind::PROPERTY;
                detail = component_prop_document_detail(&prop.cloned().unwrap_or_default());
            }
            VueComponentMemberKind::Computed => kind = SymbolKind::PROPERTY,
            VueComponentMemberKind::Method => kind = SymbolKind::METHOD,
            _ => {}
        }
        result.push(protocol::DocumentSymbol {
            name: member.name.clone(),
            detail,
            kind,
            deprecated: !member.deprecated.is_empty()
                || prop.is_some_and(|prop| !prop.deprecated.is_empty()),
            range: selection,
            selection_range: selection,
            children: Vec::new(),
        });
    }
    result
}

fn component_definition_symbols(
    object: Option<&SyntaxNode>,
    path: &str,
    line_index: &LineIndex,
    source: &str,
) -> Vec<protocol::DocumentSymbol> {
    let Some(object) = object else {
        return Vec::new();
    };
    let Some(definition) = parse_component_object(object, path, line_index) else {
        return Vec::new();
    };
    let props: HashMap<&str, &VueComponentProp> = definition
        .props
        .iter()
        .map(|prop| (prop.name.as_str(), prop))
        .collect();
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for member in &definition.members {
        let Some(selection) = source_protocol_range(&member.name_range) else {
            continue;
        };
        let prop = props.get(member.name.as_str()).copied();
        let mut kind = SymbolKind::FIELD;
        let mut detail = member.type_.clone();
        match member.kind {
            VueComponentMemberKind::Prop => {
                kind = SymbolKind::PROPERTY;
                if let Some(prop) = prop {
                    detail = component_prop_document_detail(prop);
                }
            }
            VueComponentMemberKind::Computed => kind = SymbolKind::PROPERTY,
            VueComponentMemberKind::Method => kind = SymbolKind::METHOD,
            VueComponentMemberKind::Inject => detail = "injected service".to_owned(),
            _ => {}
        }
        let deprecated = !member.deprecated.is_empty()
            || (member.kind == VueComponentMemberKind::Prop
                && prop.is_some_and(|prop| !prop.deprecated.is_empty()));
        if deprecated && !detail.contains("deprecated") {
            if !detail.is_empty() {
                detail.push_str(" · ");
            }
            detail.push_str("deprecated");
        }
        let key = (
            member.kind,
            member.name.clone(),
            selection.start.line,
            selection.start.character,
        );
        if !seen.insert(key) {
            continue;
        }
        result.push(protocol::DocumentSymbol {
            name: member.name.clone(),
            detail,
            kind,
            deprecated,
            range: javascript_declaration_range(object, selection, line_index),
            selection_range: selection,
            children: Vec::new(),
        });
    }

    result.extend(component_event_document_symbols(
        object,
        &definition,
        line_index,
        source,
    ));
    result.extend(injected_document_symbols(object, line_index, source));
    for local in &definition.local_components {
        let Some(selection) = source_protocol_range(&local.name_range) else {
            continue;
        };
        result.push(protocol::DocumentSymbol {
            name: local.name.clone(),
            detail: "local component".to_owned(),
            kind: SymbolKind::CLASS,
            deprecated: false,
            range: javascript_declaration_range(object, selection, line_index),
            selection_range: selection,
            children: Vec::new(),
        });
    }
    for directive in &definition.local_directives {
        let Some(selection) = source_protocol_range(&directive.name_range) else {
            continue;
        };
        result.push(protocol::DocumentSymbol {
            name: format!("v-{}", directive.name),
            detail: "local Vue directive".to_owned(),
            kind: SymbolKind::FUNCTION,
            deprecated: false,
            range: javascript_declaration_range(object, selection, line_index),
            selection_range: selection,
            children: Vec::new(),
        });
    }
    result
}

fn component_prop_document_detail(prop: &VueComponentProp) -> String {
    let mut detail = if prop.type_.is_empty() {
        "prop".to_owned()
    } else {
        prop.type_.clone()
    };
    if prop.required {
        detail.push_str(" · required");
    }
    if !prop.default.is_empty() {
        detail.push_str(" · default ");
        detail.push_str(&prop.default);
    }
    if !prop.deprecated.is_empty() {
        detail.push_str(" · deprecated");
    }
    detail
}

fn document_javascript_deprecation(node: &SyntaxNode) -> String {
    for current in node.ancestors() {
        if current.kind() == jssyntax::JS_PROGRAM {
            return String::new();
        }
        let deprecation = javascript_deprecation(&current);
        if !deprecation.is_empty() {
            return deprecation;
        }
    }
    String::new()
}

/// The declarations of an `emits`/`inject` list or object.
fn list_declarations(object: &SyntaxNode, property: &str) -> Option<Vec<SyntaxNode>> {
    let value =
        jsquery::property(object, property).and_then(|node| jsquery::property_value(&node))?;
    Some(match value.kind() {
        jssyntax::JS_ARRAY => jsquery::array_items(&value),
        jssyntax::JS_OBJECT => jsquery::properties(&value),
        _ => Vec::new(),
    })
}

/// The declared name and the node to select for one `emits`/`inject` entry.
fn declaration_name(declaration: &SyntaxNode) -> (String, Option<SyntaxNode>) {
    if matches!(
        declaration.kind(),
        jssyntax::JS_PROPERTY | jssyntax::JS_METHOD
    ) {
        (
            jsquery::property_name(declaration).unwrap_or_default(),
            jsquery::property_name_node(declaration),
        )
    } else {
        (
            jsquery::string_value(declaration),
            Some(declaration.clone()),
        )
    }
}

fn component_event_document_symbols(
    object: &SyntaxNode,
    definition: &ComponentDefinition,
    line_index: &LineIndex,
    source: &str,
) -> Vec<protocol::DocumentSymbol> {
    let types: HashMap<String, &str> = definition
        .events
        .iter()
        .map(|event| (canonical_event_name(&event.name), event.type_.as_str()))
        .collect();
    let Some(declarations) = list_declarations(object, "emits") else {
        return Vec::new();
    };
    let mut result = Vec::with_capacity(declarations.len());
    for declaration in &declarations {
        let (name, name_node) = declaration_name(declaration);
        let name = canonical_event_name(&name);
        let Some(name_node) = name_node else {
            continue;
        };
        if name.is_empty() {
            continue;
        }
        let detail = types
            .get(&name)
            .filter(|value| !value.is_empty())
            .map_or_else(|| "component event".to_owned(), |value| (*value).to_owned());
        result.push(protocol::DocumentSymbol {
            name,
            detail,
            kind: SymbolKind::EVENT,
            deprecated: false,
            range: protocol_range(line_index, declaration.range_trimmed_trivia()),
            selection_range: javascript_name_range(Some(&name_node), source, line_index),
            children: Vec::new(),
        });
    }
    result
}

fn injected_document_symbols(
    object: &SyntaxNode,
    line_index: &LineIndex,
    source: &str,
) -> Vec<protocol::DocumentSymbol> {
    let Some(declarations) = list_declarations(object, "inject") else {
        return Vec::new();
    };
    let mut result = Vec::with_capacity(declarations.len());
    for declaration in &declarations {
        let (name, name_node) = declaration_name(declaration);
        let Some(name_node) = name_node else {
            continue;
        };
        if name.is_empty() {
            continue;
        }
        result.push(protocol::DocumentSymbol {
            name,
            detail: "injected service".to_owned(),
            kind: SymbolKind::FIELD,
            deprecated: false,
            range: protocol_range(line_index, declaration.range_trimmed_trivia()),
            selection_range: javascript_name_range(Some(&name_node), source, line_index),
            children: Vec::new(),
        });
    }
    result
}

/// The smallest property or method of `object` enclosing `selection`.
fn javascript_declaration_range(
    object: &SyntaxNode,
    selection: protocol::Range,
    line_index: &LineIndex,
) -> protocol::Range {
    let start = line_index.offset_utf16(selection.start.line, selection.start.character);
    let end = line_index.offset_utf16(selection.end.line, selection.end.character);
    let mut best = (start, end);
    let mut found = false;
    for declaration in jsquery::nodes(object, &[jssyntax::JS_PROPERTY, jssyntax::JS_METHOD]) {
        let candidate = declaration.range_trimmed_trivia();
        let (candidate_start, candidate_end) =
            (u32::from(candidate.start()), u32::from(candidate.end()));
        if candidate_start > start || candidate_end < end {
            continue;
        }
        if found && best.1.wrapping_sub(best.0) <= candidate_end - candidate_start {
            continue;
        }
        best = (candidate_start, candidate_end);
        found = true;
    }
    offsets_protocol_range(best.0, best.1, line_index)
}

fn offsets_protocol_range(start: u32, end: u32, line_index: &LineIndex) -> protocol::Range {
    let (start_line, start_character) = line_index.position_utf16(start);
    let (end_line, end_character) = line_index.position_utf16(end);
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

fn twig_slot_member_symbols(
    tag: &HtmlStartingTag,
    line_index: &LineIndex,
) -> Vec<protocol::DocumentSymbol> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let push = |result: &mut Vec<protocol::DocumentSymbol>, name: &str, range: TextRange| {
        let range = protocol_range(line_index, range);
        result.push(protocol::DocumentSymbol {
            name: name.to_owned(),
            detail: "slot payload".to_owned(),
            kind: SymbolKind::FIELD,
            deprecated: false,
            range,
            selection_range: range,
            children: Vec::new(),
        });
    };
    for attribute in tag.attributes() {
        let Some(name_token) = attribute.name() else {
            continue;
        };
        let attribute_name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        if matches!(attribute_name.as_str(), "name" | ":name" | "v-bind:name") {
            continue;
        }
        if attribute_name == "v-bind" {
            let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                continue;
            };
            let inner = inner.syntax();
            let (fields, _) = vue_object_binding_fields(
                &inner.text().to_string(),
                inner.text_range().start().into(),
            );
            for field in fields {
                if field.name.is_empty() || !seen.insert(field.name.clone()) {
                    continue;
                }
                push(&mut result, &field.name, field.name_range);
            }
            continue;
        }
        let Some(prop) = vue_prop_reference_for_attribute(&attribute_name, name_token.text_range())
        else {
            continue;
        };
        if prop.name.is_empty() || !seen.insert(prop.name.clone()) {
            continue;
        }
        push(&mut result, &prop.name, prop.range);
    }
    result
}

fn twig_slot_name_range(tag: &HtmlStartingTag, line_index: &LineIndex) -> protocol::Range {
    for attribute in tag.attributes() {
        let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        if !matches!(name.as_str(), "name" | ":name" | "v-bind:name") {
            continue;
        }
        if let Some(inner) = attribute.value().and_then(|value| value.inner()) {
            return protocol_range(line_index, inner.syntax().range_trimmed_trivia());
        }
    }
    tag.name()
        .map(|name| protocol_range(line_index, name.text_range()))
        .unwrap_or_default()
}

fn smallest_containing_document_symbol(
    symbols: &[protocol::DocumentSymbol],
    range: protocol::Range,
) -> Option<usize> {
    let mut best: Option<usize> = None;
    for (index, symbol) in symbols.iter().enumerate() {
        if !protocol_range_contains(symbol.range, range) {
            continue;
        }
        if best.is_none_or(|best| protocol_range_contains(symbols[best].range, symbol.range)) {
            best = Some(index);
        }
    }
    best
}

fn protocol_range_contains(outer: protocol::Range, inner: protocol::Range) -> bool {
    !position_less(inner.start, outer.start) && !position_less(outer.end, inner.end)
}

fn position_less(left: protocol::Position, right: protocol::Position) -> bool {
    (left.line, left.character) < (right.line, right.character)
}

/// The range of a JavaScript name node without the quotes of a string key.
fn javascript_name_range(
    node: Option<&SyntaxNode>,
    source: &str,
    line_index: &LineIndex,
) -> protocol::Range {
    let Some(node) = node else {
        return protocol::Range::default();
    };
    let range = node.range_trimmed_trivia();
    let (mut start, mut end) = (u32::from(range.start()), u32::from(range.end()));
    let bytes = source.as_bytes();
    if end - start >= 2 && end as usize <= bytes.len() {
        let first = bytes[start as usize];
        let last = bytes[end as usize - 1];
        if matches!(first, b'\'' | b'"' | b'`') && last == first {
            start += 1;
            end -= 1;
        }
    }
    offsets_protocol_range(start, end, line_index)
}

fn source_protocol_range(range: &AdminSourceRange) -> Option<protocol::Range> {
    let start = protocol::Position {
        line: range.start_line,
        character: range.start_character,
    };
    let end = protocol::Position {
        line: range.end_line,
        character: range.end_character,
    };
    if position_less(end, start) || start == end {
        return None;
    }
    Some(protocol::Range { start, end })
}

fn administration_component_name_from_path(path: &str) -> String {
    let base = filepath::base(path);
    if base == "index.js" || base == "index.ts" {
        return filepath::base(&dir(path));
    }
    base.strip_suffix(filepath::ext(&base))
        .unwrap_or(&base)
        .to_owned()
}

/// Go `filepath.Dir`.
fn dir(path: &str) -> String {
    let cut = path
        .bytes()
        .rposition(filepath::is_path_separator)
        .map_or(0, |index| index + 1);
    let directory = filepath::clean(&path[..cut]);
    if directory.is_empty() {
        ".".to_owned()
    } else {
        directory
    }
}

fn administration_component_name_from_template_path(path: &str) -> String {
    let base = filepath::base(path);
    base.strip_suffix(".html.twig").unwrap_or(&base).to_owned()
}

fn is_administration_document_path(path: &str) -> bool {
    filepath::to_slash(path).contains("/Resources/app/administration/")
}

fn sort_document_symbols(symbols: &mut [protocol::DocumentSymbol]) {
    for symbol in symbols.iter_mut() {
        sort_document_symbols(&mut symbol.children);
    }
    symbols.sort_by(|left, right| {
        (left.range.start.line, left.range.start.character)
            .cmp(&(right.range.start.line, right.range.start.character))
    });
}
