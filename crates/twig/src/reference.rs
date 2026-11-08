//! Statically known template references in Twig and PHP (port of
//! `reference.go`).

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::ast::{AstNode, TwigBinaryExpression};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::path::base;

const TEMPLATE_TAG_NAMES: [&str; 9] = [
    "extends",
    "sw_extends",
    "include",
    "sw_include",
    "embed",
    "use",
    "from",
    "import",
    "form_theme",
];

const PHP_TEMPLATE_CALL_NAMES: [&str; 8] = [
    "render",
    "renderView",
    "renderStorefront",
    "renderBlock",
    "renderBlockView",
    "stream",
    "htmlTemplate",
    "textTemplate",
];

#[derive(
    Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize,
)]
pub enum TemplateReferenceKind {
    #[default]
    #[serde(rename = "extends")]
    Extends,
    #[serde(rename = "include")]
    Include,
    #[serde(rename = "embed")]
    Embed,
    #[serde(rename = "import")]
    Import,
    #[serde(rename = "use")]
    Use,
    #[serde(rename = "form_theme")]
    FormTheme,
    #[serde(rename = "source")]
    Source,
    #[serde(rename = "block")]
    Block,
    #[serde(rename = "render")]
    Render,
    #[serde(rename = "attribute")]
    Attribute,
}

impl TemplateReferenceKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Extends => "extends",
            Self::Include => "include",
            Self::Embed => "embed",
            Self::Import => "import",
            Self::Use => "use",
            Self::FormTheme => "form_theme",
            Self::Source => "source",
            Self::Block => "block",
            Self::Render => "render",
            Self::Attribute => "attribute",
        }
    }
}

/// One statically known use of a Twig template. `range` covers only the
/// string contents, not its quotes.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TemplateReference {
    pub template: String,
    pub file_path: String,
    pub range: TextRange,
    pub kind: TemplateReferenceKind,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TemplateReferenceCatalog {
    pub file_path: String,
    pub references: Vec<TemplateReference>,
}

/// One statically known candidate produced by a Twig template expression.
/// `range` covers the string contents for a direct literal and the complete
/// expression for a constant-folded value.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigTemplateTarget {
    pub template: String,
    pub range: TextRange,
    direct_literal: bool,
}

/// All possible templates produced by one Twig template expression. `exact`
/// is false when any runtime value participates. Fallback arrays are one
/// group so callers can apply Twig's "first existing template" semantics.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigTemplateTargetGroup {
    pub targets: Vec<TwigTemplateTarget>,
    pub range: TextRange,
    pub exact: bool,
    pub ignore_missing: bool,
    pub kind: TemplateReferenceKind,
}

/// Maps Shopware's inheritance-aware tags to the shared `Resources/views`
/// alias. Ordinary Twig tags and functions keep their exact namespace.
pub(crate) fn twig_template_lookup_name(node: Option<&SyntaxNode>, template: &str) -> String {
    let Some(node) = node else {
        return template.to_owned();
    };
    if twigquery::function_call_at(node).is_some() {
        return template.to_owned();
    }
    let name = twigquery::tag_at(node).and_then(|tag| twigquery::tag_name(&tag));
    if !matches!(name.as_deref(), Some("sw_include" | "sw_extends")) {
        return template.to_owned();
    }
    let mut template = template;
    if template.starts_with('@') {
        let Some((_, relative)) = template.split_once('/') else {
            return template.to_owned();
        };
        template = relative;
    }
    format!("@Storefront/{template}")
}

pub fn is_twig_template_string(node: &SyntaxNode) -> bool {
    let Some(literal) = twigquery::literal_string_at(node) else {
        return false;
    };
    if !twigquery::string_is_static(&literal) {
        return false;
    }
    if let Some(tag) = twigquery::tag_at(&literal) {
        let name = twigquery::tag_name(&tag).unwrap_or_default();
        if name == "form_theme" {
            return true;
        }
        if TEMPLATE_TAG_NAMES.contains(&name.as_str()) {
            // Include/extends expressions may be arrays of fallback names.
            // Context hashes and later option expressions are excluded.
            if let Some(child) = tag.children().find(|child| child.kind() == TWIG_EXPRESSION) {
                if !twig_node_contains(&child, &literal) {
                    return false;
                }
                if twigquery::closest_node_of_kind(&literal, &[TWIG_FUNCTION_CALL]).is_some() {
                    return false;
                }
                if twigquery::closest_node_of_kind(&literal, &[TWIG_LITERAL_HASH]).is_some() {
                    return twig_string_is_hash_value_for_key(&literal, "template");
                }
                return true;
            }
            return twigquery::string_in_tag(&literal, &TEMPLATE_TAG_NAMES);
        }
    }
    let Some(call) = twigquery::function_call_at(&literal) else {
        return false;
    };
    match twigquery::function_name(&call).as_deref() {
        Some("include" | "source") => twigquery::function_argument_index(&literal) == Some(0),
        Some("block") => twigquery::function_argument_index(&literal) == Some(1),
        _ => false,
    }
}

fn hash_pair_parts(pair: &SyntaxNode) -> (Option<SyntaxNode>, Option<SyntaxNode>) {
    let mut key = None;
    let mut value = None;
    for child in pair.children() {
        match child.kind() {
            TWIG_LITERAL_HASH_KEY => key = Some(child),
            TWIG_LITERAL_HASH_VALUE | TWIG_EXPRESSION => value = Some(child),
            _ => {}
        }
    }
    (key, value)
}

fn hash_key_text(key: &SyntaxNode) -> String {
    key.text()
        .to_string()
        .trim()
        .trim_matches(['"', '\'', '`'])
        .to_owned()
}

fn twig_string_is_hash_value_for_key(literal: &SyntaxNode, key: &str) -> bool {
    let Some(pair) = twigquery::closest_node_of_kind(literal, &[TWIG_LITERAL_HASH_PAIR]) else {
        return false;
    };
    let (Some(key_node), Some(value_node)) = hash_pair_parts(&pair) else {
        return false;
    };
    if !twig_node_contains(&value_node, literal) {
        return false;
    }
    swlsp_php_types::equal_fold(&hash_key_text(&key_node), key)
}

pub fn twig_template_strings(root: &SyntaxNode) -> Vec<SyntaxNode> {
    twigquery::iterate_nodes(root, &[TWIG_LITERAL_STRING])
        .filter(is_twig_template_string)
        .collect()
}

pub fn twig_template_references(path: &str, root: &SyntaxNode) -> Vec<TemplateReference> {
    let mut result = Vec::new();
    for group in twig_template_target_groups(root) {
        for target in group.targets {
            // A constant concatenation is useful for validation, but its range
            // is not safe for reference rename or file-move edits.
            if !target.direct_literal || target.template.is_empty() {
                continue;
            }
            result.push(TemplateReference {
                template: target.template,
                file_path: path.to_owned(),
                range: target.range,
                kind: group.kind,
            });
        }
    }
    unique_template_references(result)
}

const TEMPLATE_TARGET_KINDS: [swlsp_syntax::Kind; 10] = [
    TWIG_EXTENDS,
    TWIG_INCLUDE,
    TWIG_USE,
    TWIG_EMBED,
    TWIG_FROM,
    TWIG_IMPORT,
    TWIG_FORM_THEME,
    SHOPWARE_TWIG_SW_EXTENDS,
    SHOPWARE_TWIG_SW_INCLUDE,
    TWIG_FUNCTION_CALL,
];

/// Evaluates template-bearing Twig expressions: folds static string
/// concatenations, preserves fallback arrays as groups, and marks expressions
/// involving runtime values as inexact.
pub fn twig_template_target_groups(root: &SyntaxNode) -> Vec<TwigTemplateTargetGroup> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &TEMPLATE_TARGET_KINDS) {
        if node.kind() == TWIG_FUNCTION_CALL {
            if let Some(group) = twig_function_template_target_group(&node) {
                result.push(group);
            }
            continue;
        }
        result.extend(twig_tag_template_target_groups(&node));
    }
    result
}

#[derive(Default)]
struct Evaluation {
    targets: Vec<TwigTemplateTarget>,
    exact: bool,
    scalar: String,
    scalar_known: bool,
}

fn twig_tag_template_target_groups(tag: &SyntaxNode) -> Vec<TwigTemplateTargetGroup> {
    let name = twigquery::tag_name(tag).unwrap_or_default();
    if !TEMPLATE_TAG_NAMES.contains(&name.as_str()) {
        return Vec::new();
    }
    let mut scope = tag.clone();
    if name == "embed"
        && let Some(child) = tag
            .children()
            .find(|child| child.kind() == TWIG_EMBED_STARTING_BLOCK)
    {
        scope = child;
    }

    let mut target_nodes: Vec<SyntaxNode> = Vec::new();
    if name == "use" {
        if let Some(child) = scope
            .children()
            .find(|child| child.kind() == TWIG_LITERAL_STRING)
        {
            target_nodes.push(child);
        }
    } else {
        let expressions: Vec<SyntaxNode> = scope
            .children()
            .filter(|child| child.kind() == TWIG_EXPRESSION)
            .collect();
        if name == "form_theme" {
            if expressions.len() > 1 {
                target_nodes.extend(expressions[1..].iter().cloned());
            }
        } else if let Some(first) = expressions.into_iter().next() {
            target_nodes.push(first);
        }
    }

    if name == "sw_extends"
        && let Some(first) = target_nodes.first()
        && let Some(value) = twig_hash_value_for_key(first, "template")
    {
        target_nodes[0] = value;
    }
    if name == "form_theme" {
        let mut expanded = Vec::new();
        for target in target_nodes {
            let items = twig_array_expression_items(&target);
            if items.is_empty() {
                expanded.push(target);
            } else {
                expanded.extend(items);
            }
        }
        target_nodes = expanded;
    }

    let ignore_missing = twig_node_contains_token(&scope, TK_IGNORE_MISSING);
    let kind = twig_template_reference_kind_for_name(&name);
    target_nodes
        .iter()
        .map(|target| new_twig_template_target_group(target, kind, ignore_missing))
        .collect()
}

fn twig_function_template_target_group(call: &SyntaxNode) -> Option<TwigTemplateTargetGroup> {
    let name = twigquery::function_name(call)?;
    let (target, kind, ignore_missing) = match name.as_str() {
        "include" => (
            twig_function_argument(call, 0, "template"),
            TemplateReferenceKind::Include,
            twig_function_argument_is_true(
                twig_function_argument(call, 3, "ignore_missing").as_ref(),
            ),
        ),
        "source" => (
            twig_function_argument(call, 0, "name"),
            TemplateReferenceKind::Source,
            twig_function_argument_is_true(
                twig_function_argument(call, 1, "ignore_missing").as_ref(),
            ),
        ),
        "block" => (
            twig_function_argument(call, 1, "template"),
            TemplateReferenceKind::Block,
            false,
        ),
        _ => return None,
    };
    Some(new_twig_template_target_group(
        &target?,
        kind,
        ignore_missing,
    ))
}

fn twig_function_argument(call: &SyntaxNode, position: usize, name: &str) -> Option<SyntaxNode> {
    let mut index = 0;
    while let Some(argument) = twigquery::function_argument(call, index) {
        index += 1;
        if argument.kind() != TWIG_NAMED_ARGUMENT {
            continue;
        }
        if argument
            .child_token_of_kind(TK_WORD)
            .is_some_and(|token| swlsp_php_types::equal_fold(token.text(), name))
        {
            return Some(argument);
        }
    }
    twigquery::function_argument(call, position)
}

fn twig_function_argument_is_true(argument: Option<&SyntaxNode>) -> bool {
    let Some(argument) = argument else {
        return false;
    };
    let mut argument = argument.clone();
    if argument.kind() == TWIG_NAMED_ARGUMENT
        && let Some(child) = argument
            .children()
            .find(|child| child.kind() == TWIG_EXPRESSION)
    {
        argument = child;
    }
    argument
        .text()
        .to_string()
        .trim()
        .eq_ignore_ascii_case("true")
}

fn new_twig_template_target_group(
    node: &SyntaxNode,
    kind: TemplateReferenceKind,
    ignore_missing: bool,
) -> TwigTemplateTargetGroup {
    let evaluation = evaluate_twig_template_target(Some(node));
    let mut range = node.range_trimmed_trivia();
    if evaluation.targets.len() == 1 && evaluation.targets[0].direct_literal {
        range = evaluation.targets[0].range;
    }
    TwigTemplateTargetGroup {
        targets: evaluation.targets,
        range,
        exact: evaluation.exact,
        ignore_missing,
        kind,
    }
}

fn evaluate_twig_template_target(node: Option<&SyntaxNode>) -> Evaluation {
    let Some(node) = node else {
        return Evaluation::default();
    };
    match node.kind() {
        TWIG_EXPRESSION
        | TWIG_PARENTHESES_EXPRESSION
        | TWIG_NAMED_ARGUMENT
        | TWIG_LITERAL_HASH_VALUE => match node.children().next() {
            Some(child) => evaluate_twig_template_target(Some(&child)),
            None => Evaluation::default(),
        },
        TWIG_LITERAL_STRING => {
            if !twigquery::string_is_static(node) {
                return Evaluation::default();
            }
            let value = twigquery::string_value(node).unwrap_or_default();
            let name = normalize_template_reference(&value);
            if name.is_empty() {
                return Evaluation {
                    exact: true,
                    scalar: value,
                    scalar_known: true,
                    ..Evaluation::default()
                };
            }
            Evaluation {
                targets: vec![TwigTemplateTarget {
                    template: name,
                    range: string_content_range(&node.text().to_string(), node.text_range()),
                    direct_literal: true,
                }],
                exact: true,
                scalar: value,
                scalar_known: true,
            }
        }
        TWIG_BINARY_EXPRESSION => {
            let Some(binary) = TwigBinaryExpression::cast(node.clone()) else {
                return Evaluation::default();
            };
            if binary
                .operator()
                .is_none_or(|operator| operator.text().trim() != "~")
            {
                return Evaluation::default();
            }
            let (Some(lhs), Some(rhs)) = (binary.lhs_expression(), binary.rhs_expression()) else {
                return Evaluation::default();
            };
            let left = evaluate_twig_template_target(Some(lhs.syntax()));
            let right = evaluate_twig_template_target(Some(rhs.syntax()));
            if !left.scalar_known || !right.scalar_known {
                return Evaluation::default();
            }
            let value = left.scalar + &right.scalar;
            let name = normalize_template_reference(&value);
            let mut targets = Vec::new();
            if !name.is_empty() {
                targets.push(TwigTemplateTarget {
                    template: name,
                    range: node.range_trimmed_trivia(),
                    direct_literal: false,
                });
            }
            Evaluation {
                targets,
                exact: true,
                scalar: value,
                scalar_known: true,
            }
        }
        TWIG_LITERAL_ARRAY => {
            let Some(inner) = node
                .children()
                .find(|child| child.kind() == TWIG_LITERAL_ARRAY_INNER)
            else {
                return Evaluation::default();
            };
            let mut result = Evaluation {
                exact: true,
                ..Evaluation::default()
            };
            for child in inner.children() {
                if child.kind() != TWIG_EXPRESSION {
                    continue;
                }
                let item = evaluate_twig_template_target(Some(&child));
                if !item.scalar_known {
                    result.exact = false;
                }
                result.targets.extend(item.targets);
            }
            result
        }
        _ => Evaluation::default(),
    }
}

fn twig_hash_value_for_key(node: &SyntaxNode, key: &str) -> Option<SyntaxNode> {
    for pair in twigquery::iterate_nodes(node, &[TWIG_LITERAL_HASH_PAIR]) {
        let (Some(key_node), Some(value_node)) = hash_pair_parts(&pair) else {
            continue;
        };
        if swlsp_php_types::equal_fold(&hash_key_text(&key_node), key) {
            return Some(value_node);
        }
    }
    None
}

fn twig_array_expression_items(node: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut current = Some(node.clone());
    while let Some(node) = &current {
        match node.kind() {
            TWIG_EXPRESSION
            | TWIG_PARENTHESES_EXPRESSION
            | TWIG_NAMED_ARGUMENT
            | TWIG_LITERAL_HASH_VALUE => {
                current = node.children().next();
            }
            _ => break,
        }
    }
    let Some(node) = current else {
        return Vec::new();
    };
    if node.kind() != TWIG_LITERAL_ARRAY {
        return Vec::new();
    }
    if let Some(inner) = node
        .children()
        .find(|child| child.kind() == TWIG_LITERAL_ARRAY_INNER)
    {
        return inner
            .children()
            .filter(|item| item.kind() == TWIG_EXPRESSION)
            .collect();
    }
    Vec::new()
}

pub(crate) fn twig_node_contains_token(node: &SyntaxNode, kind: swlsp_syntax::Kind) -> bool {
    node.descendants_with_tokens()
        .any(|element| matches!(element, NodeOrToken::Token(token) if token.kind() == kind))
}

fn twig_template_reference_kind_for_name(name: &str) -> TemplateReferenceKind {
    match name {
        "extends" | "sw_extends" => TemplateReferenceKind::Extends,
        "embed" => TemplateReferenceKind::Embed,
        "use" => TemplateReferenceKind::Use,
        "from" | "import" => TemplateReferenceKind::Import,
        "form_theme" => TemplateReferenceKind::FormTheme,
        _ => TemplateReferenceKind::Include,
    }
}

pub fn is_php_template_string(node: &SyntaxNode) -> bool {
    let Some(literal) = phpquery::string_at(node) else {
        return false;
    };
    if !php_string_is_static(&literal) {
        return false;
    }
    if php_template_call_reference(&literal) {
        return true;
    }
    let Some(attribute) = phpquery::attribute_at(&literal) else {
        return false;
    };
    if !swlsp_php_types::equal_fold(
        base(&phpquery::attribute_name(&attribute).replace('\\', "/")),
        "Template",
    ) {
        return false;
    }
    let Some(index) = phpquery::argument_index(&attribute, &literal) else {
        return false;
    };
    let argument_name = phpquery::argument(&attribute, index)
        .map(|argument| phpquery::argument_name(&argument))
        .unwrap_or_default();
    phpquery::argument_expression(&attribute, index).as_ref() == Some(&literal)
        && (argument_name.is_empty() && index == 0
            || swlsp_php_types::equal_fold(&argument_name, "template"))
}

/// An exact static PHP string that looks like a logical Twig template name.
/// It says nothing about semantic usage: callers use this only as a
/// low-noise navigation fallback.
pub fn php_template_like_string(node: &SyntaxNode) -> Option<String> {
    let literal = phpquery::string_at(node)?;
    if !php_string_is_static(&literal) {
        return None;
    }
    let name = normalize_template_reference(&phpquery::string_value(&literal));
    if name.is_empty() || !swlsp_php_types::go_to_lower(&name).ends_with(".twig") {
        return None;
    }
    Some(name)
}

pub fn php_template_strings(root: &SyntaxNode) -> Vec<SyntaxNode> {
    phpquery::nodes(root, &[PHP_STRING])
        .into_iter()
        .filter(is_php_template_string)
        .collect()
}

/// Template references in a PHP document. The scan is memoized per tree
/// snapshot and path, so per-request lookups do not rescan the document.
pub fn php_template_references(path: &str, root: &SyntaxNode) -> Vec<TemplateReference> {
    static MEMO: crate::DocumentMemo<Vec<TemplateReference>> = crate::DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || php_template_references_uncached(path, root))
        .as_ref()
        .clone()
}

pub(crate) fn php_template_references_uncached(
    path: &str,
    root: &SyntaxNode,
) -> Vec<TemplateReference> {
    let mut result = Vec::new();
    phpquery::visit(root, &[PHP_STRING], |literal| {
        if !is_php_template_string(literal) {
            return true;
        }
        let template = phpquery::string_value(literal);
        if template.is_empty() {
            return true;
        }
        let kind = if phpquery::attribute_at(literal).is_some() {
            TemplateReferenceKind::Attribute
        } else {
            TemplateReferenceKind::Render
        };
        result.push(TemplateReference {
            template: normalize_template_reference(&template),
            file_path: path.to_owned(),
            range: string_content_range(&literal.text().to_string(), literal.text_range()),
            kind,
        });
        true
    });
    unique_template_references(result)
}

pub fn template_reference_at(
    references: &[TemplateReference],
    offset: u32,
) -> Option<&TemplateReference> {
    let offset = TextSize::from(offset);
    references
        .iter()
        .find(|reference| offset >= reference.range.start() && offset <= reference.range.end())
}

pub(crate) fn normalize_template_reference(template: &str) -> String {
    let normalized = crate::path::to_slash(template.trim()).into_owned();
    match normalized.strip_prefix('/') {
        Some(stripped) => stripped.to_owned(),
        None => normalized,
    }
}

fn php_template_call_reference(literal: &SyntaxNode) -> bool {
    let Some(call) = phpquery::call_at(literal) else {
        return false;
    };
    let method = phpquery::call_method_name(&call);
    if !PHP_TEMPLATE_CALL_NAMES
        .iter()
        .any(|name| swlsp_php_types::equal_fold(name, &method))
    {
        return false;
    }
    let Some(index) = phpquery::argument_index(&call, literal) else {
        return false;
    };
    let name = phpquery::argument(&call, index)
        .map(|argument| swlsp_php_types::go_to_lower(&phpquery::argument_name(&argument)))
        .unwrap_or_default();
    phpquery::argument_expression(&call, index).as_ref() == Some(literal)
        && (index == 0 || name == "view" || name == "name" || name == "template")
}

pub(crate) fn php_string_is_static(literal: &SyntaxNode) -> bool {
    let text = literal.text().to_string();
    let text = text.trim().as_bytes();
    text.len() >= 2
        && matches!(text[0], b'\'' | b'"' | b'`')
        && (text[0] == b'\'' || !text[1..text.len() - 1].contains(&b'$'))
}

pub(crate) fn string_content_range(text: &str, range: TextRange) -> TextRange {
    let leading = text.len() - text.trim_start_matches([' ', '\t', '\r', '\n']).len();
    let trimmed = text.trim().as_bytes();
    if trimmed.len() >= 2 {
        let quote = trimmed[0];
        if matches!(quote, b'\'' | b'"' | b'`') && trimmed[trimmed.len() - 1] == quote {
            let start = u32::from(range.start()) + (leading + 1) as u32;
            let end = u32::from(range.start()) + (leading + trimmed.len() - 1) as u32;
            return TextRange::new(start.into(), end.into());
        }
    }
    range
}

pub(crate) fn twig_node_contains(ancestor: &SyntaxNode, node: &SyntaxNode) -> bool {
    ancestor.text_range().contains_range(node.text_range())
}

pub(crate) fn unique_template_references(
    references: Vec<TemplateReference>,
) -> Vec<TemplateReference> {
    let mut seen = std::collections::HashSet::with_capacity(references.len());
    references
        .into_iter()
        .filter(|reference| {
            seen.insert((
                reference.file_path.clone(),
                reference.range,
                reference.template.clone(),
            ))
        })
        .collect()
}
