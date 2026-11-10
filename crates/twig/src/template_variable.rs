//! Template input contracts and include parameters.

use std::collections::{HashMap, HashSet};

use serde::{Deserialize, Serialize};
use swlsp_parser_twig::ast::{AstNode, TwigFilter, TwigFunctionCall, TwigLiteralString};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::compare_fold;
use crate::macros::{collect_macro_imports, direct_child, literal_name};

/// One value read from a Twig template's input context. `range` points at
/// the first source-level occurrence in `file_path`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TemplateVariable {
    pub name: String,
    pub file_path: String,
    pub range: TextRange,
}

/// A template whose input context can flow into another template.
/// `provided` holds names explicitly supplied by the caller and therefore
/// omitted from the inherited contract.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TemplateDependency {
    pub template: String,
    pub provided: Vec<String>,
    pub propagate: bool,
}

/// The persistent input contract for one addressable Twig template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TemplateVariableCatalog {
    pub template: String,
    pub file_path: String,
    pub variables: Vec<TemplateVariable>,
    pub dependencies: Vec<TemplateDependency>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum IncludeParameterKind {
    #[default]
    Function,
    Tag,
    Embed,
}

/// The direct parameter hash of a static include or embed expression.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct IncludeParameterContext {
    pub template: String,
    pub kind: IncludeParameterKind,
    pub hash: TextRange,
    pub existing: Vec<String>,
}

/// One key inside an include/embed parameter hash.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct IncludeParameter {
    pub template: String,
    pub name: String,
    pub kind: IncludeParameterKind,
    pub range: TextRange,
}

const BUILTIN_TEMPLATE_VARIABLES: [&str; 7] = [
    "_context",
    "_self",
    "app",
    "attributes",
    "computed",
    "loop",
    "this",
];

/// The external root variables read by a template. Locals declared through
/// set, loops, macros and imports are excluded; Twig component props are
/// retained because they form an explicit input contract.
pub fn template_input_variables_in_document(
    file_path: &str,
    root: &SyntaxNode,
) -> Vec<TemplateVariable> {
    let mut variables: HashMap<String, TemplateVariable> = HashMap::new();
    let mut prop_names: HashSet<String> = HashSet::new();
    for declaration in twigquery::iterate_nodes(root, &[TWIG_PROP_DECLARATION]) {
        let Some(name) = first_literal_name(&declaration) else {
            continue;
        };
        let (text, range) = literal_name(&name);
        if text.is_empty() {
            continue;
        }
        prop_names.insert(text.clone());
        variables.insert(
            text.clone(),
            TemplateVariable {
                name: text,
                file_path: file_path.to_owned(),
                range,
            },
        );
    }

    let global_locals = collect_global_template_locals(file_path, root);
    let scoped_locals = collect_scoped_template_locals(root);
    for name_node in twigquery::iterate_nodes(root, &[TWIG_LITERAL_NAME]) {
        let (name, range) = literal_name(&name_node);
        if name.is_empty()
            || BUILTIN_TEMPLATE_VARIABLES.contains(&name.as_str())
            || global_locals.contains(&name)
        {
            continue;
        }
        if is_template_declaration(&name_node)
            || is_function_or_filter_name(&name_node)
            || twigquery::closest_node_of_kind(&name_node, &[TWIG_LITERAL_HASH_KEY]).is_some()
            || is_accessor_member(&name_node)
            || is_component_invocation_name(&name_node)
            || is_scoped_template_local(&name, &name_node, &scoped_locals)
        {
            continue;
        }
        if prop_names.contains(&name) {
            continue;
        }
        variables
            .entry(name.clone())
            .or_insert_with(|| TemplateVariable {
                name,
                file_path: file_path.to_owned(),
                range,
            });
    }

    let mut result: Vec<TemplateVariable> = variables.into_values().collect();
    // Case-insensitive order; names equal under case folding fall back to
    // byte order so the persisted order is deterministic.
    result.sort_by(|left, right| {
        compare_fold(&left.name, &right.name)
            .then_with(|| left.name.cmp(&right.name))
            .then(left.range.start().cmp(&right.range.start()))
    });
    result
}

const DEPENDENCY_KINDS: [Kind; 5] = [
    TWIG_EXTENDS,
    SHOPWARE_TWIG_SW_EXTENDS,
    TWIG_INCLUDE,
    SHOPWARE_TWIG_SW_INCLUDE,
    TWIG_EMBED_STARTING_BLOCK,
];

/// Inheritance and context-preserving include edges used to resolve a
/// template's effective input contract.
pub fn template_dependencies_in_document(root: &SyntaxNode) -> Vec<TemplateDependency> {
    let mut result = Vec::new();
    for kind in DEPENDENCY_KINDS {
        for node in twigquery::iterate_nodes(root, &[kind]) {
            let template = direct_template_string(&node);
            if template.is_empty() {
                continue;
            }
            let mut dependency = TemplateDependency {
                template,
                provided: Vec::new(),
                propagate: !has_token(&node, TK_ONLY),
            };
            if kind == TWIG_EXTENDS || kind == SHOPWARE_TWIG_SW_EXTENDS {
                dependency.propagate = true;
            } else if let Some(hash) = direct_with_hash(&node) {
                dependency.provided = direct_hash_keys(&hash);
            }
            result.push(dependency);
        }
    }
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if !twigquery::function_name(&call)
            .is_some_and(|name| swlsp_textutil::eq_ignore_case(&name, "include"))
        {
            continue;
        }
        let Some(template_node) = twigquery::string_argument(&call, 0) else {
            continue;
        };
        if !twigquery::string_is_static(&template_node) {
            continue;
        }
        let mut dependency = TemplateDependency {
            template: twigquery::string_value(&template_node).unwrap_or_default(),
            provided: Vec::new(),
            propagate: include_function_propagates_context(&call),
        };
        if let Some(hash) = direct_function_hash(&call) {
            dependency.provided = direct_hash_keys(&hash);
        }
        result.push(dependency);
    }
    unique_template_dependencies(result)
}

/// The direct parameter hash under the cursor. Nested hashes and expression
/// values are rejected.
pub fn include_parameter_context_at(
    root: &SyntaxNode,
    node: Option<&SyntaxNode>,
    offset: u32,
) -> Option<IncludeParameterContext> {
    let hash = hash_at_offset(root, node, offset)?;
    let mut context = include_context_for_hash(&hash)?;
    context.hash = hash.range_trimmed_trivia();
    context.existing = direct_hash_keys(&hash);
    Some(context)
}

/// A parsed key under the cursor.
pub fn include_parameter_at(
    root: &SyntaxNode,
    node: Option<&SyntaxNode>,
    offset: u32,
) -> Option<IncludeParameter> {
    let context = include_parameter_context_at(root, node, offset)?;
    let hash = hash_at_offset(root, node, offset)?;
    for key in twigquery::iterate_nodes(&hash, &[TWIG_LITERAL_HASH_KEY]) {
        if twigquery::hash_at(&key).as_ref() != Some(&hash) {
            continue;
        }
        let (name, range) = hash_key_name(&key);
        if name.is_empty() || !range_contains_offset(range, offset) {
            continue;
        }
        return Some(IncludeParameter {
            template: context.template,
            name,
            kind: context.kind,
            range,
        });
    }
    None
}

struct TemplateLocalScope {
    scope: SyntaxNode,
    names: HashSet<String>,
}

fn collect_global_template_locals(file_path: &str, root: &SyntaxNode) -> HashSet<String> {
    let mut result = HashSet::new();
    for assignment in twigquery::iterate_nodes(root, &[TWIG_ASSIGNMENT]) {
        let equal = first_token_offset(&assignment, TK_EQUAL);
        for child in assignment.children() {
            if child.kind() != TWIG_LITERAL_NAME {
                continue;
            }
            if let Some(equal) = equal
                && u32::from(child.text_range().start()) > equal
            {
                continue;
            }
            let (name, _) = literal_name(&child);
            if !name.is_empty() {
                result.insert(name);
            }
        }
    }
    let imports = collect_macro_imports(file_path, root);
    result.extend(imports.namespaces.into_keys());
    result.extend(imports.direct.into_keys());
    result
}

fn collect_scoped_template_locals(root: &SyntaxNode) -> Vec<TemplateLocalScope> {
    let mut result = Vec::new();
    for loop_node in twigquery::iterate_nodes(root, &[TWIG_FOR]) {
        let Some(start) = direct_child(&loop_node, TWIG_FOR_BLOCK) else {
            continue;
        };
        let Some(in_offset) = first_token_offset(&start, TK_IN) else {
            continue;
        };
        let mut names = HashSet::new();
        for child in start.children() {
            if child.kind() != TWIG_LITERAL_NAME
                || u32::from(child.text_range().start()) > in_offset
            {
                continue;
            }
            names.insert(literal_name(&child).0);
        }
        result.push(TemplateLocalScope {
            scope: loop_node,
            names,
        });
    }
    for macro_node in twigquery::iterate_nodes(root, &[TWIG_MACRO]) {
        let Some(arguments) = direct_child(&macro_node, TWIG_MACRO_STARTING_BLOCK)
            .and_then(|start| direct_child(&start, TWIG_ARGUMENTS))
        else {
            continue;
        };
        let mut names = HashSet::new();
        for child in arguments.children() {
            if let Some(name) = first_literal_name(&child) {
                names.insert(literal_name(&name).0);
            }
        }
        result.push(TemplateLocalScope {
            scope: macro_node,
            names,
        });
    }
    result
}

fn is_scoped_template_local(name: &str, node: &SyntaxNode, scopes: &[TemplateLocalScope]) -> bool {
    scopes
        .iter()
        .any(|scope| scope.names.contains(name) && is_descendant_node(node, &scope.scope))
}

fn is_template_declaration(node: &SyntaxNode) -> bool {
    let start = u32::from(node.text_range().start());
    for current in node.ancestors().skip(1) {
        match current.kind() {
            TWIG_PROP_DECLARATION | TWIG_IMPORT | TWIG_FROM => return true,
            TWIG_ASSIGNMENT => {
                return match first_token_offset(&current, TK_EQUAL) {
                    Some(equal) => start < equal,
                    None => true,
                };
            }
            TWIG_FOR_BLOCK => {
                return first_token_offset(&current, TK_IN)
                    .is_some_and(|in_offset| start < in_offset);
            }
            TWIG_ARGUMENTS
                if twigquery::closest_node_of_kind(&current, &[TWIG_MACRO_STARTING_BLOCK])
                    .is_some() =>
            {
                return true;
            }
            _ => {}
        }
    }
    false
}

fn is_function_or_filter_name(node: &SyntaxNode) -> bool {
    if let Some(call) = twigquery::closest_node_of_kind(node, &[TWIG_FUNCTION_CALL])
        && let Some(operand) = TwigFunctionCall::cast(call).and_then(|call| call.name_operand())
        && is_descendant_node(node, operand.syntax())
    {
        return true;
    }
    if let Some(filter) = twigquery::closest_node_of_kind(node, &[TWIG_FILTER])
        && let Some(operand) = TwigFilter::cast(filter).and_then(|filter| filter.filter())
        && is_descendant_node(node, operand.syntax())
    {
        return true;
    }
    false
}

fn is_accessor_member(node: &SyntaxNode) -> bool {
    let Some(accessor) = twigquery::closest_node_of_kind(node, &[TWIG_ACCESSOR]) else {
        return false;
    };
    twigquery::iterate_nodes(&accessor, &[TWIG_LITERAL_NAME])
        .next()
        .is_some_and(|first| first != *node)
}

fn is_component_invocation_name(node: &SyntaxNode) -> bool {
    twigquery::closest_node_of_kind(node, &[TWIG_COMPONENT_STARTING_BLOCK]).is_some()
        && twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_HASH]).is_none()
}

fn include_context_for_hash(hash: &SyntaxNode) -> Option<IncludeParameterContext> {
    if let Some(call) = twigquery::closest_node_of_kind(hash, &[TWIG_FUNCTION_CALL])
        && twigquery::function_argument_index(hash) == Some(1)
        && twigquery::function_name(&call)
            .is_some_and(|name| swlsp_textutil::eq_ignore_case(&name, "include"))
        && let Some(template) = twigquery::string_argument(&call, 0)
        && twigquery::string_is_static(&template)
    {
        return Some(IncludeParameterContext {
            template: twigquery::string_value(&template).unwrap_or_default(),
            kind: IncludeParameterKind::Function,
            ..IncludeParameterContext::default()
        });
    }
    let with = twigquery::closest_node_of_kind(hash, &[TWIG_INCLUDE_WITH])?;
    if direct_expression_hash(&with).as_ref() != Some(hash) {
        return None;
    }
    let container = twigquery::closest_node_of_kind(
        &with,
        &[
            TWIG_INCLUDE,
            SHOPWARE_TWIG_SW_INCLUDE,
            TWIG_EMBED_STARTING_BLOCK,
        ],
    )?;
    let template = direct_template_string(&container);
    if template.is_empty() {
        return None;
    }
    let kind = if container.kind() == TWIG_EMBED_STARTING_BLOCK {
        IncludeParameterKind::Embed
    } else {
        IncludeParameterKind::Tag
    };
    Some(IncludeParameterContext {
        template,
        kind,
        ..IncludeParameterContext::default()
    })
}

fn hash_at_offset(root: &SyntaxNode, node: Option<&SyntaxNode>, offset: u32) -> Option<SyntaxNode> {
    if let Some(node) = node
        && let Some(hash) = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_HASH])
        && range_contains_offset(hash.text_range(), offset)
    {
        return Some(hash);
    }
    let mut result: Option<SyntaxNode> = None;
    for hash in twigquery::iterate_nodes(root, &[TWIG_LITERAL_HASH]) {
        if !range_contains_offset(hash.text_range(), offset) {
            continue;
        }
        if result
            .as_ref()
            .is_none_or(|current| hash.text_range().len() < current.text_range().len())
        {
            result = Some(hash);
        }
    }
    result
}

fn has_nesting_ancestor(node: &SyntaxNode, container: &SyntaxNode) -> bool {
    node.ancestors()
        .skip(1)
        .take_while(|current| current != container)
        .any(|current| {
            matches!(
                current.kind(),
                TWIG_LITERAL_HASH | TWIG_LITERAL_ARRAY | TWIG_FUNCTION_CALL
            )
        })
}

pub(crate) fn direct_template_string(node: &SyntaxNode) -> String {
    for literal in twigquery::iterate_nodes(node, &[TWIG_LITERAL_STRING]) {
        if has_nesting_ancestor(&literal, node) {
            continue;
        }
        if twigquery::string_is_static(&literal) {
            return twigquery::string_value(&literal).unwrap_or_default();
        }
    }
    String::new()
}

fn direct_with_hash(node: &SyntaxNode) -> Option<SyntaxNode> {
    direct_expression_hash(&direct_child(node, TWIG_INCLUDE_WITH)?)
}

fn direct_expression_hash(container: &SyntaxNode) -> Option<SyntaxNode> {
    twigquery::iterate_nodes(container, &[TWIG_LITERAL_HASH])
        .find(|hash| !has_nesting_ancestor(hash, container))
}

fn direct_function_hash(call: &SyntaxNode) -> Option<SyntaxNode> {
    twigquery::iterate_nodes(call, &[TWIG_LITERAL_HASH]).find(|hash| {
        twigquery::function_call_at(hash).as_ref() == Some(call)
            && twigquery::function_argument_index(hash) == Some(1)
    })
}

fn direct_hash_keys(hash: &SyntaxNode) -> Vec<String> {
    twigquery::iterate_nodes(hash, &[TWIG_LITERAL_HASH_KEY])
        .filter(|key| twigquery::hash_at(key).as_ref() == Some(hash))
        .map(|key| hash_key_name(&key).0)
        .filter(|name| !name.is_empty())
        .collect()
}

fn hash_key_name(node: &SyntaxNode) -> (String, TextRange) {
    if let Some(literal) = node
        .descendants()
        .skip(1)
        .find(|candidate| candidate.kind() == TWIG_LITERAL_STRING)
        && let Some(inner) = TwigLiteralString::cast(literal).and_then(|literal| literal.inner())
    {
        let range = inner.syntax().range_trimmed_trivia();
        return (inner.syntax().text_string(), range);
    }
    (
        node.text_string().trim().to_owned(),
        node.range_trimmed_trivia(),
    )
}

pub(crate) fn first_literal_name(node: &SyntaxNode) -> Option<SyntaxNode> {
    twigquery::iterate_nodes(node, &[TWIG_LITERAL_NAME]).next()
}

pub(crate) fn first_token_offset(node: &SyntaxNode, kind: Kind) -> Option<u32> {
    node.descendants_with_tokens()
        .find_map(|element| match element {
            NodeOrToken::Token(token) if token.kind() == kind => {
                Some(u32::from(token.text_range().start()))
            }
            _ => None,
        })
}

fn has_token(node: &SyntaxNode, kind: Kind) -> bool {
    first_token_offset(node, kind).is_some()
}

pub(crate) fn is_descendant_node(node: &SyntaxNode, ancestor: &SyntaxNode) -> bool {
    node.ancestors().any(|current| current == *ancestor)
}

pub(crate) fn range_contains_offset(range: TextRange, offset: u32) -> bool {
    let offset = TextSize::from(offset);
    offset >= range.start() && offset <= range.end()
}

fn include_function_propagates_context(call: &SyntaxNode) -> bool {
    let Some(arguments) = TwigFunctionCall::cast(call.clone()).and_then(|call| call.arguments())
    else {
        return true;
    };
    for (index, child) in arguments.syntax().children().enumerate() {
        let text = child.text_string().replace([' ', '\t'], "");
        let lower = swlsp_textutil::lowercase(&text);
        if child.kind() == TWIG_NAMED_ARGUMENT
            && (lower.starts_with("with_context=") || lower.starts_with("with_context:"))
        {
            return !lower.ends_with("false");
        }
        if index == 2 {
            return swlsp_textutil::lowercase(&child.text_string()).trim() != "false";
        }
    }
    true
}

fn unique_template_dependencies(dependencies: Vec<TemplateDependency>) -> Vec<TemplateDependency> {
    let mut seen = HashSet::with_capacity(dependencies.len());
    let mut result = Vec::with_capacity(dependencies.len());
    for mut dependency in dependencies {
        let mut provided = dependency.provided.clone();
        provided.sort();
        let mut key = format!("{}\0{}", dependency.template, provided.join("\0"));
        if dependency.propagate {
            key.push_str("\0context");
        }
        if !seen.insert(key) {
            continue;
        }
        dependency.provided = provided;
        result.push(dependency);
    }
    result
}
