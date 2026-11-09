//! Typed Twig usages of PHP declarations and `constant()` references.

use std::collections::HashSet;
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_php_semantic::{Snapshot, Symbol, SymbolKind};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::lowercase;

use crate::compare_fold;
use crate::enum_reference::enum_references;
use crate::php_access::{
    PhpAccessResolver, direct_twig_children, first_twig_child, twig_var_annotation_type_first,
};
use crate::token_parser::range;
use crate::types_tag::types_tag_class_references;

static TWIG_VAR_ANNOTATION: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?m)@var[ \t]+([^ \t\r\n#]+)[ \t]+([^ \t\r\n#]+)").expect("valid @var pattern")
});
static TWIG_TYPE_CLASS: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"\\?[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*(?:\\[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)*")
        .expect("valid class pattern")
});

const NON_CLASS_TWIG_TYPES: [&str; 23] = [
    "array",
    "array-key",
    "bool",
    "boolean",
    "callable",
    "false",
    "float",
    "int",
    "integer",
    "iterable",
    "list",
    "mixed",
    "never",
    "null",
    "object",
    "parent",
    "resource",
    "scalar",
    "self",
    "static",
    "string",
    "true",
    "void",
];

pub(crate) fn is_non_class_twig_type(value: &str) -> bool {
    NON_CLASS_TWIG_TYPES.contains(&lowercase(value).as_str())
}

mod symbol_kind_serde {
    use serde::{Deserialize, Deserializer, Serializer};
    use swlsp_php_semantic::SymbolKind;

    pub fn serialize<S: Serializer>(kind: &SymbolKind, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_u8(*kind as u8)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<SymbolKind, D::Error> {
        let value = u8::deserialize(deserializer)?;
        Ok(SymbolKind::ALL
            .get(usize::from(value))
            .copied()
            .unwrap_or_default())
    }
}

/// A typed Twig usage of a PHP declaration. `member` is empty for direct
/// class-name usages such as `enum()` and `@var`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct PhpUsageReference {
    pub class: String,
    pub member: String,
    #[serde(with = "symbol_kind_serde")]
    pub kind: SymbolKind,
    pub access: String,
    pub file_path: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct PhpUsageCatalog {
    pub key: String,
    pub references: Vec<PhpUsageReference>,
}

/// The persistent semantic key for a class or member target.
pub fn php_usage_reference_key(reference: &PhpUsageReference) -> String {
    let class = reference.class.trim();
    let class = lowercase(class.strip_prefix('\\').unwrap_or(class));
    if class.is_empty() {
        return String::new();
    }
    let member = reference.member.trim();
    let member = lowercase(member.strip_prefix('$').unwrap_or(member));
    if member.is_empty() {
        return format!("class\0{class}");
    }
    format!("member\0{class}\0{}\0{member}", reference.kind as u8)
}

/// Maps a PHP declaration onto the key used by persisted Twig usages.
pub fn php_usage_target_for_symbol(
    snapshot: &Snapshot,
    symbol: &Symbol,
) -> Option<PhpUsageReference> {
    if symbol.is_class_like() {
        if symbol.fully_qualified.is_empty() {
            return None;
        }
        return Some(PhpUsageReference {
            class: symbol.fully_qualified.to_string(),
            ..PhpUsageReference::default()
        });
    }
    if !matches!(
        symbol.kind,
        SymbolKind::Method
            | SymbolKind::Property
            | SymbolKind::ClassConstant
            | SymbolKind::EnumCase
    ) {
        return None;
    }
    let container = snapshot.symbol(&symbol.container)?;
    if !container.is_class_like() || container.fully_qualified.is_empty() {
        return None;
    }
    Some(PhpUsageReference {
        class: container.fully_qualified.to_string(),
        member: symbol
            .name
            .strip_prefix('$')
            .unwrap_or(&symbol.name)
            .to_owned(),
        kind: symbol.kind,
        ..PhpUsageReference::default()
    })
}

/// Typed Twig member accesses and direct class-name references, resolved
/// with the same PHP access semantics as completion, definition and hover.
pub fn php_usage_references_in_document(
    template_path: &str,
    root: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Vec<PhpUsageReference> {
    let resolver = resolver.clone().for_document(root);
    let mut result = Vec::new();
    let snapshot = resolver.snapshot().clone();
    for accessor in twigquery::iterate_nodes(root, &[TWIG_ACCESSOR]) {
        let Some(resolution) = resolver.resolve_accessor(template_path, root, &accessor) else {
            continue;
        };
        for member in &resolution.members {
            let Some(mut target) = php_usage_target_for_symbol(&snapshot, &member.symbol) else {
                continue;
            };
            target.access = resolution.name_node.text().to_string().trim().to_owned();
            target.file_path = template_path.to_owned();
            target.range = resolution.name_node.range_trimmed_trivia();
            result.push(target);
        }
    }
    for reference in enum_references(root) {
        result.push(PhpUsageReference {
            class: reference.name,
            access: twigquery::string_value(&reference.node).unwrap_or_default(),
            file_path: template_path.to_owned(),
            range: reference.range,
            ..PhpUsageReference::default()
        });
    }
    result.extend(twig_var_class_references(template_path, root));
    unique_php_usage_references(result)
}

fn twig_var_class_references(template_path: &str, root: &SyntaxNode) -> Vec<PhpUsageReference> {
    let mut result = Vec::new();
    for comment in twigquery::iterate_nodes(root, &[TWIG_COMMENT]) {
        let source = comment.text().to_string();
        let comment_start = u32::from(comment.text_range().start()) as usize;
        for captures in TWIG_VAR_ANNOTATION.captures_iter(&source) {
            let (Some(first), Some(second)) = (captures.get(1), captures.get(2)) else {
                continue;
            };
            let type_match = if second.as_str().starts_with('$')
                || twig_var_annotation_type_first(first.as_str(), second.as_str())
            {
                first
            } else {
                second
            };
            if type_match.end() <= type_match.start() {
                continue;
            }
            let type_text = type_match.as_str();
            for class_match in TWIG_TYPE_CLASS.find_iter(type_text) {
                let raw = class_match.as_str();
                let class_name = raw.strip_prefix('\\').unwrap_or(raw);
                if !twig_annotation_class_name(class_name) {
                    continue;
                }
                let start = comment_start + type_match.start() + class_match.start();
                result.push(PhpUsageReference {
                    class: class_name.to_owned(),
                    access: raw.to_owned(),
                    file_path: template_path.to_owned(),
                    range: range(start, start + raw.len()),
                    ..PhpUsageReference::default()
                });
            }
        }
    }
    for reference in types_tag_class_references(root.text().to_string().as_bytes()) {
        result.push(PhpUsageReference {
            class: reference.name,
            access: reference.raw,
            file_path: template_path.to_owned(),
            range: reference.range,
            ..PhpUsageReference::default()
        });
    }
    result
}

fn twig_annotation_class_name(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty() && !is_non_class_twig_type(value)
}

pub(crate) fn unique_php_usage_references(
    references: Vec<PhpUsageReference>,
) -> Vec<PhpUsageReference> {
    let mut seen = HashSet::with_capacity(references.len());
    let mut result: Vec<(String, PhpUsageReference)> = Vec::with_capacity(references.len());
    for reference in references {
        let key = php_usage_reference_key(&reference);
        if key.is_empty()
            || !seen.insert((key.clone(), reference.file_path.clone(), reference.range))
        {
            continue;
        }
        result.push((key, reference));
    }
    result.sort_by(|(left_key, left), (right_key, right)| {
        left.file_path
            .cmp(&right.file_path)
            .then(left.range.start().cmp(&right.range.start()))
            .then_with(|| left_key.cmp(right_key))
    });
    result.into_iter().map(|(_, reference)| reference).collect()
}

// --- constant() references ---

/// One static PHP constant used through Twig's `constant()` function.
/// `class` is empty for global constants.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ConstantReference {
    pub class: String,
    pub name: String,
    pub file_path: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ConstantUsageCatalog {
    pub key: String,
    pub references: Vec<ConstantReference>,
}

/// The first argument of `constant()`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ConstantCompletionContext {
    pub value: String,
    pub class: String,
    pub receiver_classes: Vec<String>,
    pub object_argument: bool,
    pub content_range: TextRange,
}

pub fn constant_reference_key(reference: &ConstantReference) -> String {
    let name = lowercase(reference.name.trim());
    if name.is_empty() {
        return String::new();
    }
    let class = lowercase(&normalize_twig_constant_class(&reference.class));
    if class.is_empty() {
        return format!("global\0{name}");
    }
    format!("class\0{class}\0{name}")
}

fn second_argument(literal: &SyntaxNode) -> Option<SyntaxNode> {
    twigquery::function_call_at(literal).and_then(|call| twigquery::function_argument(&call, 1))
}

pub fn constant_completion_context_at(
    template_path: &str,
    root: &SyntaxNode,
    node: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Option<ConstantCompletionContext> {
    let literal = twigquery::literal_string_at(node)?;
    if !constant_string_literal(&literal) {
        return None;
    }
    let value = unescape_twig_constant(&twigquery::string_value(&literal).unwrap_or_default());
    let mut context = ConstantCompletionContext {
        content_range: twig_string_content_range(&literal),
        ..ConstantCompletionContext::default()
    };
    if let Some(separator) = value.rfind("::") {
        context.class = normalize_twig_constant_class(&value[..separator]);
        context.value = value;
        return Some(context);
    }
    context.object_argument = second_argument(&literal).is_some();
    context.receiver_classes = constant_receiver_classes(template_path, root, &literal, resolver);
    context.value = value;
    Some(context)
}

pub fn constant_references_at(
    template_path: &str,
    root: &SyntaxNode,
    node: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Vec<ConstantReference> {
    match twigquery::literal_string_at(node) {
        Some(literal) if constant_string_literal(&literal) => {
            constant_references_for_literal(template_path, root, &literal, resolver)
        }
        _ => Vec::new(),
    }
}

pub fn constant_references_in_document(
    template_path: &str,
    root: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Vec<ConstantReference> {
    let resolver = resolver.clone().for_document(root);
    let mut result = Vec::new();
    for literal in twigquery::string_arguments_in_functions(root, &["constant"]) {
        result.extend(constant_references_for_literal(
            template_path,
            root,
            &literal,
            &resolver,
        ));
    }
    unique_constant_references(result)
}

fn constant_references_for_literal(
    template_path: &str,
    root: &SyntaxNode,
    literal: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Vec<ConstantReference> {
    if !constant_string_literal(literal) {
        return Vec::new();
    }
    let value = unescape_twig_constant(&twigquery::string_value(literal).unwrap_or_default());
    if value.is_empty() {
        return Vec::new();
    }
    let range = twig_string_content_range(literal);
    if let Some(separator) = value.rfind("::") {
        let class = normalize_twig_constant_class(&value[..separator]);
        let name = value[separator + 2..].trim();
        if class.is_empty() || name.is_empty() {
            return Vec::new();
        }
        return vec![ConstantReference {
            class,
            name: name.to_owned(),
            file_path: template_path.to_owned(),
            range,
        }];
    }
    let classes = constant_receiver_classes(template_path, root, literal, resolver);
    if !classes.is_empty() {
        return classes
            .into_iter()
            .map(|class| ConstantReference {
                class,
                name: value.clone(),
                file_path: template_path.to_owned(),
                range,
            })
            .collect();
    }
    if second_argument(literal).is_some() {
        return Vec::new();
    }
    vec![ConstantReference {
        class: String::new(),
        name: normalize_twig_constant_class(&value),
        file_path: template_path.to_owned(),
        range,
    }]
}

fn constant_string_literal(literal: &SyntaxNode) -> bool {
    twigquery::string_is_static(literal)
        && twigquery::string_in_function(literal, &["constant"])
        && twigquery::function_argument_index(literal) == Some(0)
}

fn constant_receiver_classes(
    template_path: &str,
    root: &SyntaxNode,
    literal: &SyntaxNode,
    resolver: &PhpAccessResolver<'_>,
) -> Vec<String> {
    let argument = second_argument(literal);
    if !constant_object_expression_allowed(argument.as_ref()) {
        return Vec::new();
    }
    let value = resolver.expression_type(template_path, root, argument.as_ref());
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    fn collect(current: &Type, seen: &mut HashSet<String>, result: &mut Vec<String>) {
        match current.kind() {
            TypeKind::Object => {
                let name = normalize_twig_constant_class(current.name());
                if name.is_empty() || !seen.insert(lowercase(&name)) {
                    return;
                }
                result.push(name);
            }
            TypeKind::Union | TypeKind::Intersection => {
                for argument in current.arguments() {
                    collect(argument, seen, result);
                }
            }
            _ => {}
        }
    }
    collect(&value, &mut seen, &mut result);
    result.sort_by(|left, right| compare_fold(left, right).then_with(|| left.cmp(right)));
    result
}

fn constant_object_expression_allowed(node: Option<&SyntaxNode>) -> bool {
    let mut current = node.cloned();
    while let Some(node) = current {
        match node.kind() {
            TWIG_EXPRESSION | TWIG_OPERAND | TWIG_PARENTHESES_EXPRESSION => {
                current = first_twig_child(&node)
            }
            TWIG_LITERAL_NAME => return true,
            TWIG_ACCESSOR => {
                let operands = direct_twig_children(&node, TWIG_OPERAND);
                let Some(first) = operands.first() else {
                    return false;
                };
                current = first_twig_child(first);
            }
            _ => return false,
        }
    }
    false
}

fn normalize_twig_constant_class(value: &str) -> String {
    value.trim().trim_matches('\\').to_owned()
}

fn unescape_twig_constant(value: &str) -> String {
    value.trim().replace("\\\\", "\\")
}

fn twig_string_content_range(literal: &SyntaxNode) -> TextRange {
    let raw = twigquery::string_value(literal).unwrap_or_default();
    let mut result = literal.range_trimmed_trivia();
    if let Some(relative) = literal.text().to_string().find(&raw) {
        let start = u32::from(literal.text_range().start()) as usize + relative;
        result = range(start, start + raw.len());
    }
    result
}

pub(crate) fn unique_constant_references(
    references: Vec<ConstantReference>,
) -> Vec<ConstantReference> {
    let mut seen = HashSet::with_capacity(references.len());
    let mut result: Vec<(String, ConstantReference)> = Vec::with_capacity(references.len());
    for reference in references {
        let key = constant_reference_key(&reference);
        if !seen.insert((key.clone(), reference.file_path.clone(), reference.range)) {
            continue;
        }
        result.push((key, reference));
    }
    result.sort_by(|(left_key, left), (right_key, right)| {
        left.file_path
            .cmp(&right.file_path)
            .then(left.range.start().cmp(&right.range.start()))
            .then_with(|| left_key.cmp(right_key))
    });
    result.into_iter().map(|(_, reference)| reference).collect()
}
