//! Helpers shared by the PHP semantic providers.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{CompletionItemKind, Location, Position, Range};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_OBJECT_CREATION, PHP_SCOPED_ACCESS, PHP_SCOPED_CALL,
    TK_SCOPE_RESOLUTION,
};
use swlsp_php::PhpContext;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Document, Flags, Parameter, Symbol, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::{Kind, LineIndex, SyntaxNode, TextRange, TextSize};
use swlsp_textutil::lowercase;

/// The member access or call enclosing `node` and whether it is static.
pub(crate) fn member_expression(node: Option<&SyntaxNode>) -> Option<(SyntaxNode, bool)> {
    for current in node?.ancestors() {
        match current.kind() {
            PHP_SCOPED_CALL | PHP_SCOPED_ACCESS => return Some((current, true)),
            PHP_MEMBER_CALL => return Some((current, false)),
            PHP_MEMBER_ACCESS => {
                let is_static = current
                    .children_with_tokens()
                    .filter_map(|child| child.into_token())
                    .any(|token| token.kind() == TK_SCOPE_RESOLUTION);
                return Some((current, is_static));
            }
            _ => {}
        }
    }
    None
}

pub(crate) fn direct_nodes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    node.children().collect()
}

pub(crate) fn first_direct_kind(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.children().find(|child| child.kind() == kind)
}

pub(crate) fn object_creation_at(node: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    node?
        .ancestors()
        .find(|current| current.kind() == PHP_OBJECT_CREATION)
}

/// The argument index and name the cursor at `offset` addresses.
pub(crate) fn active_argument(call: &SyntaxNode, offset: u32) -> (usize, String) {
    let arguments: Vec<SyntaxNode> = query::arguments(call).collect();
    let Some(last) = arguments.last() else {
        return (0, String::new());
    };
    for (index, argument) in arguments.iter().enumerate() {
        if offset <= u32::from(argument.text_range().end()) {
            return (index, query::argument_name(argument));
        }
    }
    (arguments.len(), query::argument_name(last))
}

/// The name resolution context at a document offset.
pub(crate) fn name_context_at(document: &Document, offset: u32) -> NameContext {
    match document.scope_at(offset) {
        Some(scope) => NameContext::with_imports(scope.namespace.clone(), &scope.imports),
        None => NameContext::new(&document.namespace),
    }
}

pub(crate) fn static_receiver_type(context: &PhpContext, name: &str, offset: u32) -> Type {
    match lowercase(name).as_str() {
        "self" | "static" => {
            if let Some(class) = &context.inside_class {
                return Type::named(&class.fully_qualified, Vec::new());
            }
        }
        "parent" => {
            if let Some(class) = &context.inside_class
                && let Some(parent) = class.extends().first()
            {
                return Type::named(parent, Vec::new());
            }
        }
        _ => {
            return Type::named(
                &name_context_at(&context.document, offset).resolve_class(name),
                Vec::new(),
            );
        }
    }
    Type::unknown()
}

pub(crate) fn member_visible(context: &PhpContext, symbol: &Symbol) -> bool {
    if symbol.visibility == Visibility::Public {
        return true;
    }
    let Some(inside) = &context.inside_class else {
        return false;
    };
    if symbol.container == inside.id {
        return true;
    }
    if symbol.visibility == Visibility::Private {
        return false;
    }
    context
        .snapshot
        .symbol(symbol.container.as_str())
        .is_some_and(|container| {
            context
                .snapshot
                .is_subtype_of(&inside.fully_qualified, &container.fully_qualified)
        })
}

pub(crate) fn completion_label(symbol: &Symbol) -> String {
    if symbol.kind == SymbolKind::Property {
        return format!("${}", symbol.name.strip_prefix('$').unwrap_or(&symbol.name));
    }
    symbol.name.to_string()
}

pub(crate) fn completion_kind(symbol: &Symbol) -> CompletionItemKind {
    match symbol.kind {
        SymbolKind::Method => CompletionItemKind::METHOD,
        SymbolKind::Function => CompletionItemKind::FUNCTION,
        SymbolKind::Property => CompletionItemKind::PROPERTY,
        SymbolKind::Class => CompletionItemKind::CLASS,
        SymbolKind::Interface => CompletionItemKind::INTERFACE,
        SymbolKind::Enum => CompletionItemKind::ENUM,
        SymbolKind::ClassConstant | SymbolKind::GlobalConstant => CompletionItemKind::CONSTANT,
        SymbolKind::EnumCase => CompletionItemKind::ENUM_MEMBER,
        SymbolKind::Parameter | SymbolKind::Local => CompletionItemKind::VARIABLE,
        _ => CompletionItemKind::TEXT,
    }
}

pub(crate) fn completion_rank(symbol: &Symbol) -> u8 {
    match symbol.kind {
        SymbolKind::Method => 0,
        SymbolKind::Property => 1,
        _ => 2,
    }
}

pub(crate) fn completion_snippet(symbol: &Symbol) -> String {
    if !symbol.is_function_like() {
        return symbol.name.to_string();
    }
    let mut arguments = Vec::new();
    let mut position = 1;
    for parameter in &symbol.parameters {
        if parameter.optional {
            continue;
        }
        let name = parameter.name.strip_prefix('$').unwrap_or(&parameter.name);
        arguments.push(format!("${{{position}:{name}}}"));
        position += 1;
    }
    format!("{}({})", symbol.name, arguments.join(", "))
}

pub(crate) fn format_symbol(symbol: &Symbol) -> String {
    match symbol.kind {
        SymbolKind::Class | SymbolKind::Interface | SymbolKind::Trait | SymbolKind::Enum => {
            let mut modifiers: Vec<String> = Vec::new();
            if symbol.flags.has(Flags::FINAL) {
                modifiers.push("final".into());
            }
            if symbol.flags.has(Flags::ABSTRACT) {
                modifiers.push("abstract".into());
            }
            if symbol.flags.has(Flags::READONLY) {
                modifiers.push("readonly".into());
            }
            let kind = match symbol.kind {
                SymbolKind::Class => "class",
                SymbolKind::Interface => "interface",
                SymbolKind::Trait => "trait",
                _ => "enum",
            };
            modifiers.push(kind.into());
            modifiers.push(symbol.fully_qualified.to_string());
            if !symbol.extends().is_empty() {
                modifiers.push("extends".into());
                modifiers.push(join_str(symbol.extends(), ", "));
            }
            if !symbol.implements().is_empty() {
                modifiers.push("implements".into());
                modifiers.push(join_str(symbol.implements(), ", "));
            }
            modifiers.join(" ")
        }
        SymbolKind::Method | SymbolKind::Function => {
            let mut prefix: Vec<&str> = Vec::new();
            if symbol.kind == SymbolKind::Method {
                prefix.push(visibility_name(symbol.visibility));
                if symbol.flags.has(Flags::STATIC) {
                    prefix.push("static");
                }
            }
            prefix.push("function");
            let name: &str = if symbol.kind == SymbolKind::Function {
                &symbol.fully_qualified
            } else {
                &symbol.name
            };
            let parameters: Vec<String> = symbol.parameters.iter().map(format_parameter).collect();
            let mut signature = format!("{} {}({})", prefix.join(" "), name, parameters.join(", "));
            if !symbol.return_type.is_unknown() {
                signature.push_str(": ");
                signature.push_str(&symbol.return_type.to_string());
            }
            signature
        }
        SymbolKind::Property => {
            let mut result = format!("{} ", visibility_name(symbol.visibility));
            if symbol.has_write_visibility {
                result.push_str(visibility_name(symbol.write_visibility));
                result.push_str("(set) ");
            }
            if symbol.flags.has(Flags::STATIC) {
                result.push_str("static ");
            }
            if symbol.flags.has(Flags::READONLY) {
                result.push_str("readonly ");
            }
            if !symbol.ty.is_unknown() {
                result.push_str(&symbol.ty.to_string());
                result.push(' ');
            }
            result.push('$');
            result.push_str(symbol.name.strip_prefix('$').unwrap_or(&symbol.name));
            result
        }
        SymbolKind::Parameter | SymbolKind::Local => {
            if symbol.ty.is_unknown() {
                return symbol.name.to_string();
            }
            format!("{} {}", symbol.ty, symbol.name)
        }
        SymbolKind::ClassConstant | SymbolKind::GlobalConstant => {
            let mut result = format!("const {}", symbol.name);
            if !symbol.ty.is_unknown() {
                result.push_str(": ");
                result.push_str(&symbol.ty.to_string());
            }
            result
        }
        SymbolKind::EnumCase => format!("case {}", symbol.name),
        _ => symbol.name.to_string(),
    }
}

fn join_str(values: &[swlsp_php_semantic::Str], separator: &str) -> String {
    values
        .iter()
        .map(|value| &**value)
        .collect::<Vec<&str>>()
        .join(separator)
}

pub(crate) fn format_parameter(parameter: &Parameter) -> String {
    let mut result = String::new();
    if !parameter.ty.is_unknown() {
        result.push_str(&parameter.ty.to_string());
        result.push(' ');
    }
    if parameter.flags.has(Flags::BY_REFERENCE) {
        result.push('&');
    }
    if parameter.flags.has(Flags::VARIADIC) {
        result.push_str("...");
    }
    result.push_str(&parameter.name);
    if parameter.optional {
        result.push_str(" = ...");
    }
    result
}

pub(crate) fn visibility_name(visibility: Visibility) -> &'static str {
    match visibility {
        Visibility::Protected => "protected",
        Visibility::Private => "private",
        Visibility::Public => "public",
    }
}

pub(crate) fn type_detail(value: &Type) -> String {
    if value.is_unknown() {
        return String::new();
    }
    value.to_string()
}

pub(crate) fn symbol_range_at(document: &Document, offset: u32) -> TextRange {
    if let Some(reference) = swlsp_php::reference_at(document, offset) {
        return reference.range;
    }
    let at = TextSize::from(offset);
    for symbol in &document.symbols {
        if symbol.selection_range.contains(at) || at == symbol.selection_range.end() {
            return symbol.selection_range;
        }
    }
    TextRange::empty(at)
}

pub(crate) fn range_from_text(index: &LineIndex, range: TextRange) -> Range {
    let (start_line, start_character) = index.position_utf16(range.start().into());
    let (end_line, end_character) = index.position_utf16(range.end().into());
    Range {
        start: Position {
            line: start_line,
            character: start_character,
        },
        end: Position {
            line: end_line,
            character: end_character,
        },
    }
}

pub(crate) fn locations_for_symbols(
    symbols: &[Symbol],
    current: Option<&TextDocument>,
) -> Vec<Location> {
    let mut cache = LocationCache::new(current);
    let mut seen = std::collections::HashSet::with_capacity(symbols.len());
    let mut result = Vec::new();
    for symbol in symbols {
        if !seen.insert(symbol.id.clone()) {
            continue;
        }
        result.push(cache.symbol(symbol));
    }
    result
}

/// Line indexes and sources of the files locations point into; the open
/// document wins over disk.
pub(crate) struct LocationCache<'a> {
    current: Option<&'a TextDocument>,
    entries: HashMap<String, Option<LoadedFile>>,
}

type LoadedFile = (Arc<LineIndex>, Arc<str>);

impl<'a> LocationCache<'a> {
    pub(crate) fn new(current: Option<&'a TextDocument>) -> Self {
        Self {
            current,
            entries: HashMap::new(),
        }
    }

    pub(crate) fn symbol(&mut self, symbol: &Symbol) -> Location {
        self.text_range(&symbol.path, symbol.selection_range)
    }

    pub(crate) fn text_range(&mut self, path: &str, range: TextRange) -> Location {
        let uri = if path.starts_with("phpstub://") {
            path.to_owned()
        } else {
            swlsp_uriutil::file_uri(path)
        };
        let range = self
            .entry(path)
            .map(|(index, _)| range_from_text(index, range))
            .unwrap_or_default();
        Location { uri, range }
    }

    fn entry(&mut self, path: &str) -> Option<&LoadedFile> {
        if !self.entries.contains_key(path) {
            let loaded = self.load(path);
            self.entries.insert(path.to_owned(), loaded);
        }
        self.entries.get(path).and_then(Option::as_ref)
    }

    fn load(&self, path: &str) -> Option<LoadedFile> {
        if let Some(current) = self.current
            && let Ok(current_path) = swlsp_uriutil::path(&current.uri)
            && swlsp_uriutil::filepath::clean(&current_path) == swlsp_uriutil::filepath::clean(path)
        {
            return Some((
                current.line_index_arc().clone(),
                current.source_arc().clone(),
            ));
        }
        let content = std::fs::read(path).ok()?;
        let source: Arc<str> = Arc::from(String::from_utf8_lossy(&content).into_owned());
        Some((Arc::new(LineIndex::new(source.clone())), source))
    }

    pub(crate) fn rename_text(&mut self, path: &str, range: TextRange, new_name: &str) -> String {
        if let Some((_, source)) = self.entry(path) {
            let (start, end) = (usize::from(range.start()), usize::from(range.end()));
            if end <= source.len() && start < end && source.as_bytes()[start] == b'$' {
                return format!("${new_name}");
            }
        }
        new_name.to_owned()
    }
}

/// Removes all Unicode white space (splitting on white space and joining the parts).
pub(crate) fn compact_name(source: &str) -> String {
    source.split_whitespace().collect()
}

pub(crate) fn is_implicit_variable(name: &str) -> bool {
    matches!(
        name,
        "$this"
            | "$GLOBALS"
            | "$_SERVER"
            | "$_GET"
            | "$_POST"
            | "$_FILES"
            | "$_COOKIE"
            | "$_SESSION"
            | "$_REQUEST"
            | "$_ENV"
            | "$argc"
            | "$argv"
    )
}

/// Whether the URI's file name ends in a `.php` extension, ignoring case.
pub(crate) fn is_php(uri: &str) -> bool {
    let name = uri.rsplit('/').next().unwrap_or(uri);
    name.rfind('.')
        .is_some_and(|index| name[index..].eq_ignore_ascii_case(".php"))
}

pub(crate) fn valid_php_identifier(value: &str) -> bool {
    if value.is_empty() {
        return false;
    }
    value.chars().enumerate().all(|(index, character)| {
        character == '_'
            || u32::from(character) >= 0x80
            || character.is_ascii_alphabetic()
            || (index != 0 && character.is_ascii_digit())
    })
}

/// A markdown [`protocol::MarkupContent`].
pub(crate) fn markdown(value: impl Into<String>) -> protocol::MarkupContent {
    protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    }
}
