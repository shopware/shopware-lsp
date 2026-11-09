//! Completion of templates, filters, functions, variables,
//! operators, icons and PHP member access in Twig, plus template-name
//! completion in PHP.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind, InsertTextFormat};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_VAR};
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Snapshot, Symbol, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::TextRange;
use swlsp_textutil::eq_ignore_case;
use swlsp_theme::IconProvider;
use swlsp_twig::{Global, PhpAccessResolver, TwigIndexer};

use super::{append_documentation, is_identifier_digit, lower_ext, markdown, request_offset};
use crate::common::{php_context, protocol_range, text_edit};
use swlsp_textutil::lowercase;

pub struct TwigCompletionProvider {
    pub(crate) project_root: String,
    pub(crate) twig_indexer: Option<Arc<TwigIndexer>>,
    pub(crate) icon_provider: Option<IconProvider>,
    pub(crate) php_index: Option<PhpIndex>,
    pub(crate) include_builtin_completions: bool,
}

impl TwigCompletionProvider {
    pub fn new(
        project_root: impl Into<String>,
        twig_indexer: Option<Arc<TwigIndexer>>,
        extension_indexer: Option<Arc<ExtensionIndexer>>,
        php_index: Option<PhpIndex>,
    ) -> Self {
        let project_root = project_root.into();
        Self {
            icon_provider: extension_indexer
                .map(|extensions| IconProvider::new(project_root.clone(), extensions)),
            project_root,
            twig_indexer,
            php_index,
            include_builtin_completions: true,
        }
    }

    /// Keeps project and framework-aware Twig completions while leaving
    /// built-in language suggestions to the host IDE.
    pub fn without_builtin_twig_completions(mut self) -> Self {
        self.include_builtin_completions = false;
        self
    }

    fn twig_completions(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if swlsp_twig::is_twig_template_string(node) {
            return self.template_file_items();
        }

        if twigquery::is_filter_position(node) {
            let filters = self
                .twig_indexer
                .as_ref()
                .and_then(|index| index.get_all_twig_filters().ok())
                .unwrap_or_default();
            let callables: Vec<(&str, &str, bool, &str)> = filters
                .iter()
                .map(|filter| {
                    (
                        filter.name.as_str(),
                        filter.usage.as_str(),
                        filter.deprecated,
                        filter.deprecation.as_str(),
                    )
                })
                .collect();
            return callable_items(&callables, "filter");
        }

        if twigquery::string_in_tag(node, &["sw_icon"]) {
            let config = twigquery::hash_string_map(node);
            let pack = config.get("pack").map_or("default", String::as_str);
            let icons = self
                .icon_provider
                .as_ref()
                .map(|provider| provider.icons(pack))
                .unwrap_or_default();
            return icons
                .into_iter()
                .map(|icon| protocol::CompletionItem {
                    label: icon,
                    ..Default::default()
                })
                .collect();
        }

        if twigquery::string_is_hash_value_for_key(node, "pack")
            && twigquery::tag_at(node)
                .and_then(|tag| twigquery::tag_name(&tag))
                .is_some_and(|name| name == "sw_icon")
        {
            let packs = self
                .icon_provider
                .as_ref()
                .map(IconProvider::icon_packs)
                .unwrap_or_default();
            return packs
                .into_iter()
                .map(|pack| protocol::CompletionItem {
                    label: pack,
                    ..Default::default()
                })
                .collect();
        }

        if let Some(items) = self.twig_operator_completions(request) {
            return items;
        }

        if let Some(items) = self.twig_member_completions(request) {
            return items;
        }

        if twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_some() {
            let functions = self
                .twig_indexer
                .as_ref()
                .and_then(|index| index.get_all_twig_functions().ok())
                .unwrap_or_default();
            let callables: Vec<(&str, &str, bool, &str)> = functions
                .iter()
                .map(|function| {
                    (
                        function.name.as_str(),
                        function.usage.as_str(),
                        function.deprecated,
                        function.deprecation.as_str(),
                    )
                })
                .collect();
            let mut items =
                self.twig_variable_completions(&request.params.text_document.uri, request);
            items.extend(callable_items(&callables, "function"));
            return items;
        }

        Vec::new()
    }

    pub(crate) fn template_file_items(&self) -> Vec<protocol::CompletionItem> {
        self.twig_indexer
            .as_ref()
            .and_then(|index| index.get_all_template_files().ok())
            .unwrap_or_default()
            .into_iter()
            .map(|file| protocol::CompletionItem {
                label: file,
                ..Default::default()
            })
            .collect()
    }

    fn twig_operator_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        if !twig_operator_completion_position(request) {
            return None;
        }
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        if self.include_builtin_completions {
            for name in TWIG_BUILTIN_OPERATORS {
                items_by_name.insert(
                    name.to_string(),
                    protocol::CompletionItem {
                        label: name.to_string(),
                        kind: Some(CompletionItemKind::OPERATOR),
                        detail: "Built-in Twig operator".to_string(),
                        ..Default::default()
                    },
                );
            }
        }
        if let Some(index) = &self.twig_indexer {
            for operator in index.get_all_twig_operators().unwrap_or_default() {
                if operator.name.trim().is_empty() {
                    continue;
                }
                let mut detail = if operator.unary {
                    "Custom Twig unary operator".to_string()
                } else {
                    "Custom Twig binary operator".to_string()
                };
                if operator.alias {
                    detail.push_str(" alias");
                }
                let item = protocol::CompletionItem {
                    label: operator.name.clone(),
                    kind: Some(CompletionItemKind::OPERATOR),
                    detail,
                    documentation: markdown(format!(
                        "Declared in `{}`.",
                        swlsp_uriutil::filepath::base(&operator.file_path)
                    )),
                    ..Default::default()
                };
                items_by_name.insert(operator.name, item);
            }
        }
        let mut items: Vec<protocol::CompletionItem> = items_by_name.into_values().collect();
        items.sort_by(|left, right| {
            lowercase(&left.label)
                .cmp(&lowercase(&right.label))
                .then_with(|| left.label.cmp(&right.label))
        });
        Some(items)
    }

    fn twig_variable_completions(
        &self,
        uri: &str,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let variables = self.template_variables(uri);
        let declarations = request
            .root
            .as_ref()
            .map(swlsp_twig::twig_type_declarations)
            .unwrap_or_default();
        let mut items = Vec::with_capacity(variables.len() + declarations.len());
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        // Ordered by lowercase name; ties between case variants are ordered
        // by name for determinism.
        let mut annotation_names: Vec<&String> = declarations.keys().collect();
        annotation_names.sort_by(|left, right| {
            lowercase(left)
                .cmp(&lowercase(right))
                .then_with(|| left.cmp(right))
        });
        for name in annotation_names {
            let declaration = &declarations[name];
            seen.insert(name.clone());
            let mut documentation = "Declared by a Twig type annotation.".to_string();
            if declaration.from_types_tag {
                let status = if declaration.optional {
                    "Optional"
                } else {
                    "Required"
                };
                documentation = format!("{status} variable declared by the Twig `types` tag.");
            }
            if !declaration.documentation.is_empty() {
                documentation.push_str("\n\n");
                documentation.push_str(&declaration.documentation);
            }
            items.push(protocol::CompletionItem {
                label: name.clone(),
                kind: Some(CompletionItemKind::VARIABLE),
                detail: declaration.ty.to_string(),
                documentation: markdown(documentation),
                ..Default::default()
            });
        }
        for variable in &variables {
            if !seen.insert(variable.name.clone()) {
                continue;
            }
            items.push(protocol::CompletionItem {
                label: variable.name.clone(),
                kind: Some(CompletionItemKind::VARIABLE),
                detail: variable.ty.clone(),
                documentation: markdown(format!(
                    "Provided by `{}` for `{}`.",
                    swlsp_uriutil::filepath::base(&variable.file),
                    variable.template
                )),
                ..Default::default()
            });
        }
        let Some(index) = &self.twig_indexer else {
            return items;
        };
        let globals = index.get_all_globals().unwrap_or_default();
        for global in effective_twig_globals(globals) {
            if !seen.insert(global.name.clone()) {
                continue;
            }
            let detail = if global.ty.is_empty() {
                "mixed".to_string()
            } else {
                global.ty.clone()
            };
            let mut item = protocol::CompletionItem {
                label: global.name.clone(),
                kind: Some(CompletionItemKind::VARIABLE),
                detail,
                documentation: markdown(format!(
                    "Workspace-wide Twig global from {}.",
                    global.source
                )),
                ..Default::default()
            };
            let display = self.global_display_path(&global.file);
            if !display.is_empty() {
                append_documentation(&mut item, &format!("\n\nDeclared in `{display}`."));
            }
            items.push(item);
        }
        items
    }

    fn twig_member_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let node = request.node.as_ref()?;
        let accessor = twigquery::closest_node_of_kind(node, &[TWIG_ACCESSOR])?;
        let offset = request_offset(request)?;
        let start = u32::from(accessor.text_range().start());
        if offset < start {
            return None;
        }
        let local_offset = (offset - start) as usize;
        let source = accessor.text().to_string();
        let last_dot = source.rfind('.')?;
        if local_offset <= last_dot {
            return None;
        }
        let template_path = swlsp_uriutil::path(&request.params.text_document.uri).ok()?;
        if swlsp_twig::loop_accessor_in_scope(&accessor) {
            if let Some(loop_type) = swlsp_twig::loop_context_type(self.php_index.as_ref()) {
                let php = self.php_index.as_ref()?;
                return Some(twig_php_member_completions(
                    &php.semantic_snapshot(),
                    &loop_type,
                ));
            }
            return Some(twig3_loop_completions());
        }
        let php = self.php_index.as_ref()?;
        let root = request.root.as_ref()?;
        let receiver = PhpAccessResolver::new(Some(php), self.twig_indexer.as_deref())
            .accessor_receiver_type(&template_path, root, &accessor);
        if receiver.is_unknown() {
            return None;
        }
        Some(twig_php_member_completions(
            &php.semantic_snapshot(),
            &receiver,
        ))
    }

    fn global_display_path(&self, path: &str) -> String {
        if path.is_empty() {
            return String::new();
        }
        let display = swlsp_uriutil::filepath::rel(&self.project_root, path)
            .unwrap_or_else(|_| path.to_string());
        swlsp_uriutil::filepath::to_slash(&display)
    }

    pub(crate) fn template_variables(&self, uri: &str) -> Vec<swlsp_php::TwigTemplateVariable> {
        let Some(php) = &self.php_index else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        php.twig_template_variables(&swlsp_twig::template_names(&path))
            .unwrap_or_default()
    }

    fn php_completions(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(php) = &self.php_index
            && let Some(context) = php_context(php, &request.syntax)
            && let Some(reference) =
                swlsp_php::assistant_argument_reference(&context, node, "Template")
        {
            return self.assistant_template_completions(request, reference);
        }
        if swlsp_twig::is_php_template_string(node) {
            return self.template_file_items();
        }
        Vec::new()
    }

    fn assistant_template_completions(
        &self,
        request: &CompletionRequest,
        reference: TextRange,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = &self.twig_indexer else {
            return Vec::new();
        };
        let Ok(files) = index.get_all_template_files() else {
            return Vec::new();
        };
        let replacement = request
            .line_index()
            .map(|line_index| protocol_range(reference, line_index));
        files
            .into_iter()
            .map(|file| protocol::CompletionItem {
                kind: Some(CompletionItemKind::FILE),
                detail: "Twig template".to_string(),
                text_edit: replacement.map(|range| text_edit(range, file.clone())),
                label: file,
                ..Default::default()
            })
            .collect()
    }
}

impl CompletionProvider for TwigCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        match lower_ext(&request.params.text_document.uri).as_str() {
            ".php" => {
                if request.node.is_none() {
                    return Vec::new();
                }
                self.php_completions(request)
            }
            ".twig" => {
                if let Some(items) = self.twig_types_tag_completions(request) {
                    return items;
                }
                if let Some(items) = self.twig_see_completions(request) {
                    return items;
                }
                if let Some(items) = self.twig_guard_completions(request) {
                    return items;
                }
                if let Some(items) = self.twig_test_completions(request) {
                    return items;
                }
                if let Some(items) = self.twig_statement_completions(request) {
                    return items;
                }
                if let Some(items) = self.twig_tag_completions(request) {
                    return items;
                }
                if request.node.is_none() {
                    return Vec::new();
                }
                self.twig_completions(request)
            }
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["\"".into(), "'".into(), "|".into()]
    }
}

/// Filter or function items: one item per
/// distinct name, deprecated only when every declaration is.
fn callable_items(
    callables: &[(&str, &str, bool, &str)],
    kind: &str,
) -> Vec<protocol::CompletionItem> {
    let mut deprecations: HashMap<&str, TwigCallableCompletionDeprecation> = HashMap::new();
    for (name, _, deprecated, deprecation) in callables {
        let status = deprecations
            .entry(name)
            .or_insert(TwigCallableCompletionDeprecation {
                all: true,
                message: String::new(),
            });
        status.all = status.all && *deprecated;
        if status.message.is_empty() {
            status.message = deprecation.to_string();
        }
    }
    let mut unique = std::collections::HashSet::new();
    let mut items = Vec::new();
    for (name, usage, _, _) in callables {
        if name.contains('*') || !unique.insert(*name) {
            continue;
        }
        let mut item = protocol::CompletionItem {
            label: usage.to_string(),
            insert_text: format!("{name}($0)"),
            insert_text_format: Some(InsertTextFormat::SNIPPET),
            ..Default::default()
        };
        if let Some(status) = deprecations.get(name) {
            apply_twig_callable_completion_deprecation(&mut item, kind, status);
        }
        items.push(item);
    }
    items
}

const TWIG_BUILTIN_OPERATORS: [&str; 35] = [
    "or",
    "||",
    "and",
    "&&",
    "b-or",
    "b-xor",
    "b-and",
    "==",
    "!=",
    "<=>",
    "<",
    ">",
    ">=",
    "<=",
    "in",
    "not in",
    "matches",
    "starts with",
    "ends with",
    "===",
    "!==",
    "..",
    "+",
    "-",
    "~",
    "*",
    "/",
    "//",
    "%",
    "is",
    "is not",
    "**",
    "??",
    "?:",
    "not",
];

fn twig_operator_completion_position(request: &CompletionRequest) -> bool {
    let content = request.source().as_bytes();
    if content.is_empty() {
        return false;
    }
    let Some(offset) = request_offset(request) else {
        return false;
    };
    if offset as usize > content.len() {
        return false;
    }
    let prefix = &content[..offset as usize];
    let Some(open) = rfind(prefix, b"{%") else {
        return false;
    };
    if rfind(prefix, b"%}").is_some_and(|close| close > open) {
        return false;
    }
    if rfind(prefix, b"{{").is_some_and(|index| index > open)
        || rfind(prefix, b"{#").is_some_and(|index| index > open)
    {
        return false;
    }
    let mut statement = &prefix[open + 2..];
    // TrimLeftFunc(unicode.IsSpace || '-' || '~') on the valid UTF-8 prefix.
    loop {
        let (value, size) = decode_char(statement);
        if size == 0 || !(value.is_whitespace() || value == '-' || value == '~') {
            break;
        }
        statement = &statement[size..];
    }
    if statement.len() < 2
        || !statement[..2].eq_ignore_ascii_case(b"if")
        || statement.len() > 2 && is_twig_operator_word_rune(char::from(statement[2]))
    {
        return false;
    }
    let mut expression = &statement[2..];
    if trim_space(expression).is_empty() {
        return false;
    }

    // When completion is requested while an operator fragment is being
    // typed, evaluate the expression before that fragment. A request
    // directly inside the first operand therefore remains excluded.
    if !decode_last_char(expression).is_whitespace() {
        let mut index = expression.len() as isize - 1;
        while index >= 0 && !char::from(expression[index as usize]).is_whitespace() {
            index -= 1;
        }
        if index < 0 {
            return false;
        }
        expression = &expression[..index as usize + 1];
    }
    twig_expression_can_accept_operator(expression)
}

fn twig_expression_can_accept_operator(expression: &[u8]) -> bool {
    let expression = trim_space(expression);
    if expression.is_empty() {
        return false;
    }
    let lowered = lowercase(&String::from_utf8_lossy(expression));
    let Some(last_field) = lowered.split_whitespace().last() else {
        return false;
    };
    if matches!(
        last_field,
        "and"
            | "or"
            | "not"
            | "is"
            | "in"
            | "matches"
            | "starts"
            | "ends"
            | "with"
            | "b-and"
            | "b-or"
            | "b-xor"
    ) {
        return false;
    }
    let last = decode_last_char(expression);
    last.is_alphabetic()
        || is_identifier_digit(last)
        || last == '_'
        || last == '\''
        || last == '"'
        || last == ')'
        || last == ']'
        || last == '}'
}

fn is_twig_operator_word_rune(value: char) -> bool {
    value.is_alphabetic() || is_identifier_digit(value) || value == '_'
}

fn rfind(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .rposition(|window| window == needle)
}

/// The first UTF-8 character of `bytes` and its length: `(U+FFFD, 1)` for
/// an invalid sequence, `(U+FFFD, 0)` when empty.
fn decode_char(bytes: &[u8]) -> (char, usize) {
    if bytes.is_empty() {
        return ('\u{FFFD}', 0);
    }
    for size in 1..=bytes.len().min(4) {
        if let Ok(value) = std::str::from_utf8(&bytes[..size])
            && let Some(first) = value.chars().next()
        {
            return (first, size);
        }
    }
    ('\u{FFFD}', 1)
}

/// The last UTF-8 character of `bytes`: U+FFFD for an invalid sequence or
/// empty input.
fn decode_last_char(bytes: &[u8]) -> char {
    for size in 1..=bytes.len().min(4) {
        if let Ok(value) = std::str::from_utf8(&bytes[bytes.len() - size..]) {
            let mut chars = value.chars();
            if let (Some(first), None) = (chars.next(), chars.next()) {
                return first;
            }
        }
    }
    '\u{FFFD}'
}

/// `bytes` without leading and trailing Unicode whitespace (invalid UTF-8
/// bytes are not whitespace).
fn trim_space(mut bytes: &[u8]) -> &[u8] {
    loop {
        let (value, size) = decode_char(bytes);
        if size == 0 || !value.is_whitespace() {
            break;
        }
        bytes = &bytes[size..];
    }
    while !bytes.is_empty() {
        let value = decode_last_char(bytes);
        if !value.is_whitespace() {
            break;
        }
        bytes = &bytes[..bytes.len() - value.len_utf8()];
    }
    bytes
}

#[derive(Clone, Debug, Default)]
pub(crate) struct TwigCallableCompletionDeprecation {
    pub(crate) all: bool,
    pub(crate) message: String,
}

pub(crate) fn apply_twig_callable_completion_deprecation(
    item: &mut protocol::CompletionItem,
    kind: &str,
    deprecation: &TwigCallableCompletionDeprecation,
) {
    if !deprecation.all {
        return;
    }
    item.deprecated = true;
    item.detail = format!("Deprecated Twig {kind}");
    let mut value = format!("**Deprecated Twig {kind}**");
    if !deprecation.message.is_empty() {
        value.push_str("\n\n");
        value.push_str(&deprecation.message);
    }
    item.documentation = markdown(value);
}

fn twig3_loop_completions() -> Vec<protocol::CompletionItem> {
    swlsp_twig::twig3_loop_variables()
        .into_iter()
        .map(|name| protocol::CompletionItem {
            label: name,
            kind: Some(CompletionItemKind::PROPERTY),
            detail: "Twig 3 loop variable".to_string(),
            ..Default::default()
        })
        .collect()
}

pub(crate) fn effective_twig_globals(globals: Vec<Global>) -> Vec<Global> {
    let mut by_name: HashMap<String, Global> = HashMap::with_capacity(globals.len());
    let mut order: Vec<String> = Vec::new();
    for global in globals {
        if global.name.is_empty() {
            continue;
        }
        if let Some(existing) = by_name.get_mut(&global.name) {
            existing.ty = merge_twig_global_types(&existing.ty, &global.ty);
            continue;
        }
        order.push(global.name.clone());
        by_name.insert(global.name.clone(), global);
    }
    order
        .into_iter()
        .filter_map(|name| by_name.remove(&name))
        .collect()
}

fn merge_twig_global_types(left: &str, right: &str) -> String {
    if left.is_empty() {
        return right.to_string();
    }
    if right.is_empty() || left == right {
        return left.to_string();
    }
    if left.split('|').any(|value| value == right) {
        return left.to_string();
    }
    format!("{left}|{right}")
}

fn is_public_instance(symbol: &Symbol) -> bool {
    symbol.visibility == Visibility::Public && !symbol.flags.has(Flags::STATIC)
}

pub(crate) fn twig_php_member_completions(
    snapshot: &Snapshot,
    receiver: &Type,
) -> Vec<protocol::CompletionItem> {
    let resolved = MemberResolver::new(snapshot).all(receiver);
    let mut items: HashMap<String, protocol::CompletionItem> = HashMap::new();
    for member in resolved {
        let symbol = &member.symbol;
        if !is_public_instance(symbol) {
            continue;
        }
        let deprecated = symbol.flags.has(Flags::DEPRECATED);
        match symbol.kind {
            SymbolKind::Property => {
                let name = symbol
                    .name
                    .strip_prefix('$')
                    .unwrap_or(&symbol.name)
                    .to_string();
                let mut item = protocol::CompletionItem {
                    label: name.clone(),
                    kind: Some(CompletionItemKind::PROPERTY),
                    detail: member.ty.to_string(),
                    deprecated,
                    ..Default::default()
                };
                apply_twig_php_member_documentation(&mut item, symbol);
                items.insert(lowercase(&name), item);
            }
            SymbolKind::Method => {
                let lower_name = lowercase(&symbol.name);
                if lower_name.starts_with("set") || lower_name.starts_with("__") {
                    continue;
                }
                let attribute = swlsp_twig::twig_attribute_name(&symbol.name);
                if !attribute.is_empty() {
                    if let std::collections::hash_map::Entry::Vacant(entry) =
                        items.entry(lowercase(&attribute))
                    {
                        let mut item = protocol::CompletionItem {
                            label: attribute,
                            kind: Some(CompletionItemKind::PROPERTY),
                            deprecated,
                            detail: format!("{} via {}()", member.ty, symbol.name),
                            ..Default::default()
                        };
                        apply_twig_php_member_documentation(&mut item, symbol);
                        entry.insert(item);
                    }
                    continue;
                }
                if items.contains_key(&lower_name) {
                    continue;
                }
                let mut item = protocol::CompletionItem {
                    label: symbol.name.to_string(),
                    kind: Some(CompletionItemKind::METHOD),
                    detail: member.ty.to_string(),
                    insert_text: format!("{}($0)", symbol.name),
                    insert_text_format: Some(InsertTextFormat::SNIPPET),
                    deprecated,
                    ..Default::default()
                };
                apply_twig_php_member_documentation(&mut item, symbol);
                items.insert(lower_name, item);
            }
            SymbolKind::ClassConstant | SymbolKind::EnumCase => {
                let kind = if symbol.kind == SymbolKind::EnumCase {
                    CompletionItemKind::ENUM_MEMBER
                } else {
                    CompletionItemKind::CONSTANT
                };
                let key = lowercase(&symbol.name);
                if items.contains_key(&key) {
                    continue;
                }
                let mut item = protocol::CompletionItem {
                    label: symbol.name.to_string(),
                    kind: Some(kind),
                    detail: member.ty.to_string(),
                    deprecated,
                    ..Default::default()
                };
                apply_twig_php_member_documentation(&mut item, symbol);
                items.insert(key, item);
            }
            _ => {}
        }
    }
    // Keys are the lowercase labels, so the order is total.
    let mut keyed: Vec<(String, protocol::CompletionItem)> = items.into_iter().collect();
    keyed.sort_by(|left, right| left.0.cmp(&right.0));
    keyed.into_iter().map(|(_, item)| item).collect()
}

fn apply_twig_php_member_documentation(item: &mut protocol::CompletionItem, symbol: &Symbol) {
    let mut sections: Vec<&str> = Vec::new();
    if symbol.flags.has(Flags::DEPRECATED) {
        sections.push("**Deprecated PHP member**");
    }
    if !symbol.doc_summary().is_empty() {
        sections.push(symbol.doc_summary());
    }
    if sections.is_empty() {
        return;
    }
    item.documentation = markdown(sections.join("\n\n"));
}

pub(crate) fn twig_member_type(snapshot: &Snapshot, receiver: &Type, name: &str) -> Type {
    for member in MemberResolver::new(snapshot).all(receiver) {
        let symbol = &member.symbol;
        if !is_public_instance(symbol) {
            continue;
        }
        match symbol.kind {
            SymbolKind::Property
                if eq_ignore_case(symbol.name.strip_prefix('$').unwrap_or(&symbol.name), name) =>
            {
                return member.ty;
            }
            SymbolKind::Method
                if eq_ignore_case(&symbol.name, name)
                    || eq_ignore_case(&swlsp_twig::twig_attribute_name(&symbol.name), name) =>
            {
                return member.ty;
            }
            _ => {}
        }
    }
    Type::unknown()
}

pub(crate) fn compact_twig_accessor(source: &str) -> String {
    source
        .chars()
        .filter(|value| !value.is_whitespace())
        .collect()
}

pub(crate) fn is_twig_identifier(value: &str) -> bool {
    if value.is_empty() {
        return false;
    }
    for (index, current) in value.chars().enumerate() {
        if index == 0 {
            if current != '_' && !current.is_alphabetic() {
                return false;
            }
        } else if current != '_' && !current.is_alphabetic() && !is_identifier_digit(current) {
            return false;
        }
    }
    true
}
