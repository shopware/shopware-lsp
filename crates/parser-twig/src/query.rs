//! Semantic Twig CST queries shared by LSP features and indexers. Queries use
//! syntax kinds and typed AST accessors instead of grammar-specific parent
//! depths. Absent names and indexes are `None`.

use std::collections::BTreeMap;

use swlsp_syntax::{Kind, SyntaxNode};

use crate::ast::{
    AstNode, HtmlAttribute, HtmlStartingTag, TwigBlock, TwigFilter, TwigFunctionCall,
    TwigLiteralName, TwigLiteralString,
};
use crate::syntax::*;

/// Every node in `root`'s subtree (including `root`) whose kind is in
/// `kinds`, in source order.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    iterate_nodes(root, kinds).collect()
}

/// Lazily yields what [`nodes`] returns.
pub fn iterate_nodes<'k>(
    root: &SyntaxNode,
    kinds: &'k [Kind],
) -> impl Iterator<Item = SyntaxNode> + use<'k> {
    root.descendants()
        .filter(move |node| kinds.contains(&node.kind()))
}

/// The closest node (including `node`) of `kind`.
pub fn ancestor_of_kind(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

/// The closest node (including `node`) whose kind is in `kinds`.
pub fn closest_node_of_kind(node: &SyntaxNode, kinds: &[Kind]) -> Option<SyntaxNode> {
    node.ancestors()
        .find(|current| kinds.contains(&current.kind()))
}

/// The string literal containing `node`.
pub fn literal_string_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[TWIG_LITERAL_STRING])
}

/// The name of a Twig function call node.
pub fn function_name(node: &SyntaxNode) -> Option<String> {
    let call = TwigFunctionCall::cast(node.clone())?;
    first_literal_name(call.name_operand()?.syntax())
}

/// The unquoted contents of a Twig string literal node.
pub fn string_value(node: &SyntaxNode) -> Option<String> {
    let literal = TwigLiteralString::cast(node.clone())?;
    if let Some(inner) = literal.inner() {
        return Some(inner.syntax().text().to_string());
    }
    let text = node.text().to_string();
    Some(text.trim().trim_matches(['"', '\'', '`']).to_owned())
}

/// Whether the string literal containing `node` has no interpolation.
pub fn string_is_static(node: &SyntaxNode) -> bool {
    let Some(literal) = literal_string_at(node) else {
        return false;
    };
    iterate_nodes(&literal, &[TWIG_LITERAL_STRING_INTERPOLATION])
        .next()
        .is_none()
}

/// Direct string arguments of calls to any of `function_names`. Strings nested
/// in hashes, arrays or nested calls are not arguments of the outer call.
pub fn string_arguments_in_functions(
    root: &SyntaxNode,
    function_names: &[&str],
) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    for node in iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if !name_in(function_name(&node), function_names) {
            continue;
        }
        let Some(arguments) = function_arguments(&node) else {
            continue;
        };
        result.extend(
            iterate_nodes(&arguments, &[TWIG_LITERAL_STRING])
                .filter(|literal| is_direct_string_value(literal, Some(&arguments))),
        );
    }
    result
}

/// The function call containing `node`.
pub fn function_call_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[TWIG_FUNCTION_CALL])
}

/// The zero-based direct argument of the enclosing call containing `node`.
pub fn function_argument_index(node: &SyntaxNode) -> Option<usize> {
    let call = function_call_at(node)?;
    let arguments = function_arguments(&call)?;
    let argument = node
        .ancestors()
        .take_while(|current| *current != arguments)
        .find(|current| current.parent().as_ref() == Some(&arguments))?;
    arguments.children().position(|child| child == argument)
}

/// The direct positional or named argument at `index` (the named-argument
/// wrapper when the call uses `name:` / `name=` syntax).
pub fn function_argument(call: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    function_arguments(call)?.children().nth(index)
}

/// The direct string argument at `index`.
pub fn string_argument(call: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    if call.kind() != TWIG_FUNCTION_CALL {
        return None;
    }
    let arguments = function_arguments(call);
    iterate_nodes(call, &[TWIG_LITERAL_STRING]).find(|literal| {
        function_call_at(literal).as_ref() == Some(call)
            && function_argument_index(literal) == Some(index)
            && is_direct_string_value(literal, arguments.as_ref())
    })
}

fn function_arguments(call: &SyntaxNode) -> Option<SyntaxNode> {
    Some(
        TwigFunctionCall::cast(call.clone())?
            .arguments()?
            .syntax()
            .clone(),
    )
}

/// Whether `node` is inside a direct string argument of one of
/// `function_names`.
pub fn string_in_function(node: &SyntaxNode, function_names: &[&str]) -> bool {
    let Some(literal) = literal_string_at(node) else {
        return false;
    };
    let Some(call) = function_call_at(&literal) else {
        return false;
    };
    if !name_in(function_name(&call), function_names) {
        return false;
    }
    function_arguments(&call).is_some_and(|arguments| {
        is_descendant_of(&literal, &arguments) && is_direct_string_value(&literal, Some(&arguments))
    })
}

/// The name of the function whose name operand contains `node`.
pub fn function_name_at(node: &SyntaxNode) -> Option<String> {
    let call = function_call_at(node)?;
    let name = TwigFunctionCall::cast(call.clone())?.name_operand()?;
    if !is_descendant_of(node, name.syntax()) {
        return None;
    }
    function_name(&call)
}

/// The right-hand name of a Twig filter node.
pub fn filter_name(node: &SyntaxNode) -> Option<String> {
    let filter = TwigFilter::cast(node.clone())?;
    first_literal_name(filter.filter()?.syntax())
}

fn first_literal_name(node: &SyntaxNode) -> Option<String> {
    if let Some(name) = TwigLiteralName::cast(node.clone()).and_then(|literal| literal.name()) {
        return Some(name.text().to_owned());
    }
    node.children().find_map(|child| first_literal_name(&child))
}

/// Whether `node` is a string literal on the left side of a filter named one
/// of `filter_names`.
pub fn string_in_filter(node: &SyntaxNode, filter_names: &[&str]) -> bool {
    let Some(literal) = literal_string_at(node) else {
        return false;
    };
    let Some(filter_node) = closest_node_of_kind(&literal, &[TWIG_FILTER]) else {
        return false;
    };
    if !name_in(filter_name(&filter_node), filter_names) {
        return false;
    }
    TwigFilter::cast(filter_node)
        .and_then(|filter| filter.operand())
        .is_some_and(|operand| {
            is_descendant_of(&literal, operand.syntax())
                && is_direct_string_value(&literal, Some(operand.syntax()))
        })
}

/// Whether `node` is in the right-hand filter operand, including an
/// incomplete filter right after the pipe.
pub fn is_filter_position(node: &SyntaxNode) -> bool {
    let Some(operand) = closest_node_of_kind(node, &[TWIG_FILTER])
        .and_then(TwigFilter::cast)
        .and_then(|filter| filter.filter())
    else {
        return false;
    };
    operand.syntax().text_range().is_empty() || is_descendant_of(node, operand.syntax())
}

const TAG_KINDS: &[Kind] = &[
    TWIG_BLOCK,
    TWIG_EXTENDS,
    TWIG_INCLUDE,
    TWIG_USE,
    TWIG_EMBED,
    TWIG_FROM,
    TWIG_IMPORT,
    TWIG_FORM_THEME,
    TWIG_ASSETIC_STARTING_BLOCK,
    SHOPWARE_TWIG_SW_EXTENDS,
    SHOPWARE_TWIG_SW_INCLUDE,
    SHOPWARE_ICON,
    SHOPWARE_THUMBNAILS,
];

/// The Twig tag-like node containing `node`.
pub fn tag_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, TAG_KINDS)
}

/// The source-level name of a supported tag node (see [`tag_at`]).
pub fn tag_name(node: &SyntaxNode) -> Option<String> {
    let name = match node.kind() {
        TWIG_BLOCK => "block",
        TWIG_EXTENDS => "extends",
        TWIG_INCLUDE => "include",
        TWIG_USE => "use",
        TWIG_EMBED => "embed",
        TWIG_FROM => "from",
        TWIG_IMPORT => "import",
        TWIG_FORM_THEME => "form_theme",
        TWIG_ASSETIC_STARTING_BLOCK => {
            return node
                .children_with_tokens()
                .filter_map(|element| element.into_token())
                .find(|token| token.kind() == TK_WORD)
                .map(|token| token.text().to_owned());
        }
        SHOPWARE_TWIG_SW_EXTENDS => "sw_extends",
        SHOPWARE_TWIG_SW_INCLUDE => "sw_include",
        SHOPWARE_ICON => "sw_icon",
        SHOPWARE_THUMBNAILS => "sw_thumbnails",
        _ => return None,
    };
    Some(name.to_owned())
}

/// Whether `node` is a direct string argument of one of `tag_names`. Strings
/// nested in an option hash, array, call, binary expression or named argument
/// are excluded (`form_theme` accepts any string).
pub fn string_in_tag(node: &SyntaxNode, tag_names: &[&str]) -> bool {
    let Some(literal) = literal_string_at(node) else {
        return false;
    };
    let Some(tag) = tag_at(&literal) else {
        return false;
    };
    let name = tag_name(&tag);
    if !name_in(name.clone(), tag_names) {
        return false;
    }
    if name.as_deref() == Some("form_theme") {
        return true;
    }
    !literal
        .ancestors()
        .skip(1)
        .take_while(|current| *current != tag)
        .any(|current| {
            matches!(
                current.kind(),
                TWIG_LITERAL_HASH
                    | TWIG_LITERAL_ARRAY
                    | TWIG_FUNCTION_CALL
                    | TWIG_BINARY_EXPRESSION
                    | TWIG_NAMED_ARGUMENT
            )
        })
}

/// The Twig block containing `node`.
pub fn block_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[TWIG_BLOCK])
}

/// The declared name of a Twig block node.
pub fn block_name(node: &SyntaxNode) -> Option<String> {
    Some(TwigBlock::cast(node.clone())?.name()?.text().to_owned())
}

/// String-to-string entries of the closest Twig hash (or the first hash in the
/// enclosing tag). Keys may be quoted strings, names or numbers; values must be
/// static string literals. Dynamic keys and non-string values are omitted.
pub fn hash_string_map(node: &SyntaxNode) -> BTreeMap<String, String> {
    let mut result = BTreeMap::new();
    let Some(hash) = hash_for(node) else {
        return result;
    };
    for pair in own_hash_pairs(&hash) {
        let Some(key) = hash_pair_key(&pair) else {
            continue;
        };
        if let Some(value) =
            hash_pair_string_literal(&pair).and_then(|literal| string_value(&literal))
        {
            result.insert(key, value);
        }
    }
    result
}

/// Whether the hash used by [`hash_string_map`] has an entry with the static
/// `key`, whatever its value.
pub fn hash_has_key(node: &SyntaxNode, key: &str) -> bool {
    hash_for(node).is_some_and(|hash| {
        own_hash_pairs(&hash).any(|pair| hash_pair_key(&pair).as_deref() == Some(key))
    })
}

/// Pairs belonging to `hash` itself (not to nested hashes).
fn own_hash_pairs(hash: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    let owner = hash.clone();
    iterate_nodes(hash, &[TWIG_LITERAL_HASH_PAIR])
        .filter(move |pair| hash_at(pair).as_ref() == Some(&owner))
}

fn hash_for(node: &SyntaxNode) -> Option<SyntaxNode> {
    if let Some(hash) = closest_node_of_kind(node, &[TWIG_LITERAL_HASH]) {
        return Some(hash);
    }
    let scope = tag_at(node).unwrap_or_else(|| node.clone());
    iterate_nodes(&scope, &[TWIG_LITERAL_HASH]).next()
}

fn hash_pair_parts(pair: &SyntaxNode) -> (Option<SyntaxNode>, Option<SyntaxNode>) {
    let mut key = None;
    let mut value = None;
    for child in pair.children() {
        match child.kind() {
            TWIG_LITERAL_HASH_KEY if key.is_none() => key = Some(child),
            TWIG_LITERAL_HASH_VALUE | TWIG_EXPRESSION if value.is_none() => value = Some(child),
            _ => {}
        }
    }
    (key, value)
}

fn hash_pair_key(pair: &SyntaxNode) -> Option<String> {
    let key = hash_pair_parts(pair).0?;
    if let Some(child) = key.children().next() {
        return match child.kind() {
            TWIG_LITERAL_STRING => string_value(&child),
            TWIG_LITERAL_NUMBER => Some(child.text().to_string().trim().to_owned()),
            TWIG_LITERAL_NAME => first_literal_name(&child),
            // Dynamic keys such as (expression) have no static name.
            _ => None,
        };
    }
    key.children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == TK_WORD)
        .map(|token| token.text().to_owned())
}

fn hash_pair_string_literal(pair: &SyntaxNode) -> Option<SyntaxNode> {
    static_string_literal(hash_pair_parts(pair).1?)
}

/// Descends through single-child chains to a static string literal.
fn static_string_literal(mut node: SyntaxNode) -> Option<SyntaxNode> {
    loop {
        if node.kind() == TWIG_LITERAL_STRING {
            return string_is_static(&node).then_some(node);
        }
        let mut children = node.children();
        let only = children.next()?;
        if children.next().is_some() {
            return None;
        }
        node = only;
    }
}

/// The hash containing `node`.
pub fn hash_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[TWIG_LITERAL_HASH])
}

/// The hash key containing `node`.
pub fn hash_key_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[TWIG_LITERAL_HASH_KEY])
}

/// Whether `node` is the static string value of a hash pair whose (quoted or
/// unquoted) key equals `key`.
pub fn string_is_hash_value_for_key(node: &SyntaxNode, key: &str) -> bool {
    let Some(literal) = literal_string_at(node) else {
        return false;
    };
    let Some(pair) = closest_node_of_kind(&literal, &[TWIG_LITERAL_HASH_PAIR]) else {
        return false;
    };
    hash_pair_key(&pair).as_deref() == Some(key)
        && hash_pair_string_literal(&pair).as_ref() == Some(&literal)
}

/// The HTML start tag containing `node`.
pub fn starting_html_tag_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[HTML_STARTING_TAG])
}

/// The HTML attribute containing `node`.
pub fn html_attribute_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    closest_node_of_kind(node, &[HTML_ATTRIBUTE])
}

/// The name of an HTML start tag node.
pub fn html_tag_name(node: &SyntaxNode) -> Option<String> {
    Some(
        HtmlStartingTag::cast(node.clone())?
            .name()?
            .text()
            .to_owned(),
    )
}

/// The source-level attribute name including Vue binding prefixes.
pub fn html_attribute_name(node: &SyntaxNode) -> Option<String> {
    HtmlAttribute::cast(node.clone())?.name()?;
    let text = node.text().to_string();
    let text = text.trim();
    let name = text.split_once('=').map_or(text, |(name, _)| name);
    Some(name.trim().to_owned())
}

fn name_in(name: Option<String>, names: &[&str]) -> bool {
    name.is_some_and(|name| names.contains(&name.as_str()))
}

fn is_descendant_of(node: &SyntaxNode, ancestor: &SyntaxNode) -> bool {
    node.ancestors().any(|current| current == *ancestor)
}

fn is_direct_string_value(literal: &SyntaxNode, scope: Option<&SyntaxNode>) -> bool {
    let Some(scope) = scope else {
        return false;
    };
    let nested = literal
        .ancestors()
        .skip(1)
        .take_while(|current| current != scope)
        .any(|current| {
            // A string inside a concatenation, a condition, a filter, an
            // interpolation or a container is not the value itself.
            matches!(
                current.kind(),
                TWIG_LITERAL_HASH
                    | TWIG_LITERAL_ARRAY
                    | TWIG_FUNCTION_CALL
                    | TWIG_BINARY_EXPRESSION
                    | TWIG_CONDITIONAL_EXPRESSION
                    | TWIG_UNARY_EXPRESSION
                    | TWIG_FILTER
                    | TWIG_ARROW_FUNCTION
                    | TWIG_INDEX_LOOKUP
                    | TWIG_LITERAL_STRING
            )
        });
    !nested && is_descendant_of(literal, scope)
}

#[cfg(test)]
mod tests;
