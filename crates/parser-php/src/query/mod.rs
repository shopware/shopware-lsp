//! Structural queries over PHP syntax trees.
//!
//! Functions return `Option` for nodes and `String` for text, empty when the
//! text is absent. Text derived from several tokens is normalized (white
//! space and comments removed) so callers can compare it directly.

mod extract;
mod imports;
mod strings;
#[cfg(test)]
mod tests;

pub use extract::*;
pub use imports::*;
pub use strings::*;

use swlsp_syntax::rowan::{NodeOrToken, WalkEvent};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange, TextSize};

use crate::eq_ignore_case;
use crate::syntax::*;

const CLASS_KINDS: &[Kind] = &[
    PHP_CLASS_DECLARATION,
    PHP_INTERFACE_DECLARATION,
    PHP_TRAIT_DECLARATION,
    PHP_ENUM_DECLARATION,
];
const CALL_KINDS: &[Kind] = &[PHP_MEMBER_CALL, PHP_SCOPED_CALL, PHP_FUNCTION_CALL];
const FUNCTION_LIKE_KINDS: &[Kind] = &[
    PHP_METHOD_DECLARATION,
    PHP_FUNCTION_DECLARATION,
    PHP_CLOSURE,
    PHP_ARROW_FUNCTION,
];

// -- traversal ------------------------------------------------------------------

/// All nodes of the given kinds beneath and including `root`, in source order.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| kinds.contains(&node.kind()))
        .collect()
}

/// Walks matching nodes in source order without materializing a result.
/// Returning `false` from `visit` stops the traversal; the result reports
/// whether the whole tree was visited.
pub fn visit(
    root: &SyntaxNode,
    kinds: &[Kind],
    mut visit: impl FnMut(&SyntaxNode) -> bool,
) -> bool {
    for event in root.preorder() {
        if let WalkEvent::Enter(node) = event
            && kinds.contains(&node.kind())
            && !visit(&node)
        {
            return false;
        }
    }
    true
}

/// First direct child node of `kind`.
pub fn direct_child(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.child_node_of_kind(kind)
}

/// `node` or its nearest ancestor whose kind is one of `kinds`.
pub fn ancestor_or_self(node: &SyntaxNode, kinds: &[Kind]) -> Option<SyntaxNode> {
    node.ancestors()
        .find(|current| kinds.contains(&current.kind()))
}

fn child_nodes_of_kind(node: &SyntaxNode, kind: Kind) -> impl Iterator<Item = SyntaxNode> {
    node.children().filter(move |child| child.kind() == kind)
}

/// Direct child tokens, including trivia.
pub(crate) fn child_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> {
    node.children_with_tokens()
        .filter_map(NodeOrToken::into_token)
}

/// First token of `kind` in document order beneath `node`.
pub(crate) fn descendant_token(node: &SyntaxNode, kind: Kind) -> Option<SyntaxToken> {
    node.descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| token.kind() == kind)
}

/// First strict descendant node of `kind` in document order.
pub(crate) fn descendant_node(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.descendants()
        .skip(1)
        .find(|child| child.kind() == kind)
}

fn has_direct_token(node: &SyntaxNode, kind: Kind) -> bool {
    child_tokens(node).any(|token| token.kind() == kind)
}

fn contains(parent: &SyntaxNode, child: &SyntaxNode) -> bool {
    parent.text_range().contains_range(child.text_range())
}

fn is_type_node(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
    )
}

/// Full source text of `node`.
pub fn node_text(node: &SyntaxNode) -> String {
    node.text().to_string()
}

/// Source text of `node` without leading trivia.
fn trimmed_text(node: &SyntaxNode) -> String {
    let start = node.range_trimmed_trivia().start();
    let mut text = String::new();
    for token in node
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
    {
        let range = token.text_range();
        if range.end() <= start {
            continue;
        }
        let skip = u32::from(start.max(range.start()) - range.start()) as usize;
        text.push_str(&token.text()[skip..]);
    }
    text
}

/// Removes ASCII white space from a name.
fn compact_name(text: &str) -> String {
    text.chars()
        .filter(|c| !matches!(c, ' ' | '\t' | '\r' | '\n'))
        .collect()
}

/// Removes white space and comment-like regions (comment detection is textual
/// and also applies inside string literals).
pub(crate) fn compact_expression(text: &str) -> String {
    let bytes = text.as_bytes();
    let needs_slow = bytes.iter().enumerate().any(|(index, &byte)| match byte {
        b' ' | b'\t' | b'\r' | b'\n' => true,
        b'/' => matches!(bytes.get(index + 1), Some(b'/' | b'*')),
        _ => false,
    });
    if !needs_slow {
        return text.to_owned();
    }
    let mut out = Vec::with_capacity(bytes.len());
    let mut line_comment = false;
    let mut block_comment = false;
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        let next = bytes.get(index + 1).copied();
        if line_comment {
            if byte == b'\n' || byte == b'\r' {
                line_comment = false;
            }
        } else if block_comment {
            if byte == b'*' && next == Some(b'/') {
                block_comment = false;
                index += 1;
            }
        } else if byte == b'/' && next == Some(b'/') {
            line_comment = true;
            index += 1;
        } else if byte == b'/' && next == Some(b'*') {
            block_comment = true;
            index += 1;
        } else if !matches!(byte, b' ' | b'\t' | b'\r' | b'\n') {
            out.push(byte);
        }
        index += 1;
    }
    // Only ASCII bytes are removed, so the result stays valid UTF-8 unless a
    // comment marker cut a character, which cannot happen for ASCII markers.
    String::from_utf8(out)
        .unwrap_or_else(|error| String::from_utf8_lossy(error.as_bytes()).into_owned())
}

fn compact_node(node: &SyntaxNode) -> String {
    compact_expression(&node_text(node))
}

// -- names and namespaces -------------------------------------------------------

/// Normalized text of a `PHP_NAME` node (leading trivia and white space
/// removed); empty for other nodes.
pub fn name_value(node: &SyntaxNode) -> String {
    if node.kind() != PHP_NAME {
        return String::new();
    }
    compact_name(&trimmed_text(node))
}

fn child_name_value(node: Option<&SyntaxNode>) -> String {
    node.and_then(|node| direct_child(node, PHP_NAME))
        .map_or_else(String::new, |name| name_value(&name))
}

/// Name of the first namespace declaration.
pub fn namespace(root: &SyntaxNode) -> String {
    let mut result = String::new();
    visit(root, &[PHP_NAMESPACE], |namespace| {
        result = child_name_value(Some(namespace));
        false
    });
    result
}

pub fn use_declarations(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, &[PHP_USE_DECLARATION])
}

// -- classes --------------------------------------------------------------------

pub fn classes(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, CLASS_KINDS)
}

pub fn class_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, CLASS_KINDS)
}

pub fn class_name(node: &SyntaxNode) -> String {
    child_name_value(class_at(node).as_ref())
}

pub fn class_body(node: &SyntaxNode) -> Option<SyntaxNode> {
    direct_child(&class_at(node)?, PHP_CLASS_BODY)
}

fn clause_names(class: Option<SyntaxNode>, kind: Kind) -> Vec<String> {
    let Some(clause) = class.and_then(|class| direct_child(&class, kind)) else {
        return Vec::new();
    };
    child_nodes_of_kind(&clause, PHP_NAME)
        .map(|name| name_value(&name))
        .collect()
}

pub fn class_extends(node: &SyntaxNode) -> Vec<String> {
    clause_names(class_at(node), PHP_EXTENDS_CLAUSE)
}

pub fn class_implements(node: &SyntaxNode) -> Vec<String> {
    clause_names(class_at(node), PHP_IMPLEMENTS_CLAUSE)
}

fn class_kind_is(node: &SyntaxNode, kind: Kind) -> bool {
    class_at(node).is_some_and(|class| class.kind() == kind)
}

pub fn is_interface(node: &SyntaxNode) -> bool {
    class_kind_is(node, PHP_INTERFACE_DECLARATION)
}

pub fn is_trait(node: &SyntaxNode) -> bool {
    class_kind_is(node, PHP_TRAIT_DECLARATION)
}

pub fn is_enum(node: &SyntaxNode) -> bool {
    class_kind_is(node, PHP_ENUM_DECLARATION)
}

pub fn is_abstract(node: &SyntaxNode) -> bool {
    class_at(node).is_some_and(|class| {
        class.kind() == PHP_CLASS_DECLARATION
            && child_tokens(&class).any(|token| eq_ignore_case(token.text(), "abstract"))
    })
}

fn class_body_members(node: &SyntaxNode, kind: Kind) -> Vec<SyntaxNode> {
    class_body(node).map_or_else(Vec::new, |body| child_nodes_of_kind(&body, kind).collect())
}

pub fn methods(node: &SyntaxNode) -> Vec<SyntaxNode> {
    class_body_members(node, PHP_METHOD_DECLARATION)
}

pub fn properties(node: &SyntaxNode) -> Vec<SyntaxNode> {
    class_body_members(node, PHP_PROPERTY_DECLARATION)
}

// -- functions and parameters ---------------------------------------------------

pub fn method_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_METHOD_DECLARATION])
}

pub fn function_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_FUNCTION_DECLARATION])
}

pub fn function_like_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, FUNCTION_LIKE_KINDS)
}

pub fn functions(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, &[PHP_FUNCTION_DECLARATION])
}

pub fn function_name(node: &SyntaxNode) -> String {
    child_name_value(function_at(node).as_ref())
}

pub fn method_name(node: &SyntaxNode) -> String {
    child_name_value(method_at(node).as_ref())
}

/// Parameters of the enclosing function-like node, in source order.
pub fn parameters(node: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    function_like_at(node)
        .and_then(|function| direct_child(&function, PHP_PARAMETER_LIST))
        .into_iter()
        .flat_map(|list| child_nodes_of_kind(&list, PHP_PARAMETER).collect::<Vec<_>>())
}

fn is_parameter(node: &SyntaxNode) -> bool {
    node.kind() == PHP_PARAMETER
}

/// Parameter variable including `$`.
pub fn parameter_name(node: &SyntaxNode) -> String {
    if !is_parameter(node) {
        return String::new();
    }
    descendant_token(node, TK_VARIABLE).map_or_else(String::new, |token| token.text().to_owned())
}

fn direct_type_text(node: &SyntaxNode) -> String {
    node.children()
        .find(|child| is_type_node(child.kind()))
        .map_or_else(String::new, |child| compact_node(&child))
}

pub fn parameter_type(node: &SyntaxNode) -> String {
    if !is_parameter(node) {
        return String::new();
    }
    direct_type_text(node)
}

pub fn parameter_optional(node: &SyntaxNode) -> bool {
    descendant_token(node, TK_EQUALS).is_some()
}

/// The direct default expression after `=`.
pub fn parameter_default(node: &SyntaxNode) -> Option<SyntaxNode> {
    if !is_parameter(node) {
        return None;
    }
    let mut found_equals = false;
    for child in node.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) => found_equals |= token.kind() == TK_EQUALS,
            NodeOrToken::Node(node) if found_equals => return Some(node),
            NodeOrToken::Node(_) => {}
        }
    }
    None
}

pub fn parameter_variadic(node: &SyntaxNode) -> bool {
    is_parameter(node) && descendant_token(node, TK_ELLIPSIS).is_some()
}

// -- properties and variables ---------------------------------------------------

pub fn property_variables(node: &SyntaxNode) -> Vec<SyntaxNode> {
    if node.kind() != PHP_PROPERTY_DECLARATION {
        return Vec::new();
    }
    child_nodes_of_kind(node, PHP_VARIABLE).collect()
}

/// The variable token of the enclosing `PHP_VARIABLE` node.
pub fn variable_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    descendant_token(&ancestor_or_self(node, &[PHP_VARIABLE])?, TK_VARIABLE)
}

/// Variable name without `$`.
pub fn variable_name(node: &SyntaxNode) -> String {
    variable_token(node).map_or_else(String::new, |token| {
        token.text().trim_start_matches('$').to_owned()
    })
}

/// The complete variable token text, including its leading `$`.
pub fn variable_key(node: &SyntaxNode) -> String {
    variable_token(node).map_or_else(String::new, |token| token.text().to_owned())
}

/// Lowercased visibility modifier among the direct tokens before `function`.
pub fn declaration_visibility(node: &SyntaxNode) -> String {
    for token in child_tokens(node) {
        let text = token.text();
        for visibility in ["public", "protected", "private"] {
            if text.eq_ignore_ascii_case(visibility) {
                return visibility.to_owned();
            }
        }
        if eq_ignore_case(text, "function") {
            break;
        }
    }
    String::new()
}

pub fn property_type(node: &SyntaxNode) -> String {
    if node.kind() != PHP_PROPERTY_DECLARATION {
        return String::new();
    }
    direct_type_text(node)
}

pub fn method_return_type(node: &SyntaxNode) -> String {
    function_like_at(node).map_or_else(String::new, |method| direct_type_text(&method))
}

// -- attributes -----------------------------------------------------------------

pub fn attribute_groups(node: &SyntaxNode) -> Vec<SyntaxNode> {
    child_nodes_of_kind(node, PHP_ATTRIBUTE_GROUP).collect()
}

pub fn attributes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    attribute_groups(node)
        .iter()
        .flat_map(|group| child_nodes_of_kind(group, PHP_ATTRIBUTE))
        .collect()
}

pub fn attribute_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_ATTRIBUTE])
}

pub fn attribute_name(node: &SyntaxNode) -> String {
    child_name_value(attribute_at(node).as_ref())
}

// -- calls ----------------------------------------------------------------------

/// Calls beneath `root`; when `names` is non-empty only calls whose
/// [`call_name`] is one of them.
pub fn calls(root: &SyntaxNode, names: &[&str]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| CALL_KINDS.contains(&node.kind()))
        .filter(|call| names.is_empty() || names.contains(&call_name(call).as_str()))
        .collect()
}

pub fn expression_statements(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, &[PHP_EXPRESSION_STATEMENT])
}

/// The variable (with `$`) on the left side of a simple assignment such as
/// `$services = $container->services()`.
pub fn assigned_variable(node: &SyntaxNode) -> String {
    let Some(statement) = ancestor_or_self(node, &[PHP_EXPRESSION_STATEMENT]) else {
        return String::new();
    };
    if let Some(assignment) = descendant_or_self(&statement, PHP_ASSIGNMENT_EXPRESSION) {
        // Only inspect the assignment's left operand.
        if let Some(first) = assignment.children().next()
            && first.kind() == PHP_VARIABLE
        {
            let name = variable_name(&first);
            if !name.is_empty() {
                return format!("${name}");
            }
        }
    }
    let mut variable = String::new();
    for child in statement.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) if token.kind() == TK_EQUALS => return variable,
            NodeOrToken::Node(node) if variable.is_empty() && node.kind() == PHP_VARIABLE => {
                let name = variable_name(&node);
                if !name.is_empty() {
                    variable = format!("${name}");
                }
            }
            _ => {}
        }
    }
    String::new()
}

fn descendant_or_self(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.descendants().find(|child| child.kind() == kind)
}

/// The right-hand operand of the first assignment beneath `node`.
pub fn assignment_value(node: &SyntaxNode) -> Option<SyntaxNode> {
    let assignment = descendant_or_self(node, PHP_ASSIGNMENT_EXPRESSION)?;
    let mut operands = assignment.children();
    operands.next()?;
    operands.last()
}

pub fn call_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, CALL_KINDS)
}

/// Compacted source text of the call before its argument list, e.g.
/// `$this->renderStorefront`.
pub fn call_name(node: &SyntaxNode) -> String {
    let Some(call) = call_at(node) else {
        return String::new();
    };
    let Some(list) = direct_child(&call, PHP_ARGUMENT_LIST) else {
        return String::new();
    };
    let end = list.text_range().start();
    let mut text = String::new();
    for token in call
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
    {
        if token.text_range().start() >= end {
            break;
        }
        text.push_str(token.text());
    }
    compact_expression(&text)
}

/// The direct call target before the argument list (the method name of a
/// member call, the function name of a function call).
pub fn call_method_name(node: &SyntaxNode) -> String {
    let Some(call) = call_at(node) else {
        return String::new();
    };
    let mut target: Option<SyntaxNode> = None;
    for child in call.children() {
        if child.kind() == PHP_ARGUMENT_LIST {
            return target.map_or_else(String::new, |target| compact_node(&target));
        }
        target = Some(child);
    }
    String::new()
}

/// The direct receiver expression of a member or scoped call.
pub fn call_receiver(node: &SyntaxNode) -> Option<SyntaxNode> {
    let call = call_at(node)?;
    if call.kind() != PHP_MEMBER_CALL && call.kind() != PHP_SCOPED_CALL {
        return None;
    }
    call.children()
        .next()
        .filter(|child| child.kind() != PHP_ARGUMENT_LIST)
}

// -- arguments ------------------------------------------------------------------

fn argument_list(node: &SyntaxNode) -> Option<SyntaxNode> {
    let call = match node.kind() {
        PHP_MEMBER_CALL | PHP_SCOPED_CALL | PHP_FUNCTION_CALL | PHP_ATTRIBUTE
        | PHP_OBJECT_CREATION => node.clone(),
        _ => call_at(node)?,
    };
    direct_child(&call, PHP_ARGUMENT_LIST)
}

fn is_argument(node: &SyntaxNode) -> bool {
    matches!(node.kind(), PHP_ARGUMENT | PHP_NAMED_ARGUMENT)
}

/// Arguments of a call, attribute or object creation (or of the call
/// enclosing `node`), in source order.
pub fn arguments(node: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    argument_list(node)
        .into_iter()
        .flat_map(|list| list.children().filter(is_argument).collect::<Vec<_>>())
}

pub fn argument(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    arguments(node).nth(index)
}

/// Index of the argument of `container` that contains `node`.
pub fn argument_index(container: &SyntaxNode, node: &SyntaxNode) -> Option<usize> {
    arguments(container).position(|argument| contains(&argument, node))
}

/// Label of the named argument containing `node`; stops at a positional
/// argument boundary so an outer name does not leak into an inner call.
pub fn argument_name(node: &SyntaxNode) -> String {
    for current in node.ancestors() {
        match current.kind() {
            PHP_NAMED_ARGUMENT => return child_name_value(Some(&current)),
            PHP_ARGUMENT => return String::new(),
            _ => {}
        }
    }
    String::new()
}

/// First expression node of the `index`-th argument (skipping a named
/// argument's label).
pub fn argument_expression(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    let argument = argument(node, index)?;
    let named = argument.kind() == PHP_NAMED_ARGUMENT;
    argument
        .children()
        .find(|child| !(named && child.kind() == PHP_NAME))
}

/// Complete expression text of the `index`-th argument.
pub fn argument_value_text(node: &SyntaxNode, index: usize) -> String {
    argument(node, index).map_or_else(String::new, |argument| argument_value(&argument))
}

/// Complete expression text of one argument node, without a named label.
pub fn argument_value(argument: &SyntaxNode) -> String {
    let text = node_text(argument);
    let text = text.trim();
    if argument.kind() != PHP_NAMED_ARGUMENT {
        return text.to_owned();
    }
    text.find(':')
        .map_or_else(String::new, |colon| text[colon + 1..].trim().to_owned())
}

// -- strings --------------------------------------------------------------------

pub fn string_argument(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    descendant_node(&argument(node, index)?, PHP_STRING)
}

pub fn string_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_STRING])
}

/// String literal content without matching quotes.
pub fn string_value(node: &SyntaxNode) -> String {
    let Some(token) = string_at(node).and_then(|string| descendant_token(&string, TK_STRING))
    else {
        return String::new();
    };
    let text = token.text();
    let bytes = text.as_bytes();
    if bytes.len() >= 2
        && matches!(bytes[0], b'\'' | b'"' | b'`')
        && bytes[bytes.len() - 1] == bytes[0]
    {
        return text[1..text.len() - 1].to_owned();
    }
    text.to_owned()
}

/// Source range inside matching single or double quotes; other string forms
/// keep their full trimmed range.
pub fn string_content_range(node: &SyntaxNode) -> TextRange {
    let range = node.range_trimmed_trivia();
    let text = node_text(node);
    let text = text.trim().as_bytes();
    if text.len() >= 2 && matches!(text[0], b'\'' | b'"') && text[text.len() - 1] == text[0] {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

pub fn string_argument_index(node: &SyntaxNode) -> Option<usize> {
    let string = string_at(node)?;
    argument_index(&call_at(&string)?, &string)
}

/// Whether `node` is a string literal passed as argument `argument_index` to
/// a call whose name or method name is one of `names`.
pub fn string_in_call(node: &SyntaxNode, argument_index: usize, names: &[&str]) -> bool {
    if string_at(node).is_none() || string_argument_index(node) != Some(argument_index) {
        return false;
    }
    let call_name = call_name(node);
    let method_name = call_method_name(node);
    names
        .iter()
        .any(|name| call_name == *name || method_name == *name)
}

// -- object creation and scoped access ------------------------------------------

pub fn object_creations(root: &SyntaxNode, class_names: &[&str]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| node.kind() == PHP_OBJECT_CREATION)
        .filter(|object| {
            class_names.is_empty() || class_names.contains(&object_class_name(object).as_str())
        })
        .collect()
}

pub fn object_creation_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_OBJECT_CREATION])
}

pub fn object_class_name(node: &SyntaxNode) -> String {
    child_name_value(object_creation_at(node).as_ref())
}

/// The class-like name from a `Foo::class` expression.
pub fn class_constant_name(node: &SyntaxNode) -> String {
    scoped_access_class(node, "class")
}

/// The class expression left of a specific static member access such as
/// `ProductDefinition::ENTITY_NAME`.
pub fn scoped_access_class(node: &SyntaxNode, member_name: &str) -> String {
    const ACCESS: &[Kind] = &[PHP_SCOPED_ACCESS, PHP_MEMBER_ACCESS];
    let candidates = ancestor_or_self(node, ACCESS).into_iter().chain(
        node.descendants()
            .filter(|candidate| ACCESS.contains(&candidate.kind())),
    );
    for member in candidates {
        let text = compact_node(&member);
        let Some(separator) = text.rfind("::") else {
            continue;
        };
        if !eq_ignore_case(&text[separator + 2..], member_name) {
            continue;
        }
        return text[..separator].trim().to_owned();
    }
    String::new()
}

// -- arrays ---------------------------------------------------------------------

pub fn arrays(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, &[PHP_ARRAY])
}

pub fn array_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_ARRAY])
}

pub fn array_item_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, &[PHP_ARRAY_ITEM])
}

pub fn array_items(node: &SyntaxNode) -> Vec<SyntaxNode> {
    array_at(node).map_or_else(Vec::new, |array| {
        child_nodes_of_kind(&array, PHP_ARRAY_ITEM).collect()
    })
}

/// The expression left of `=>`; list-style items have no key.
pub fn array_item_key(node: &SyntaxNode) -> Option<SyntaxNode> {
    let item = array_item_at(node)?;
    if !has_direct_token(&item, TK_ARROW) {
        return None;
    }
    item.children().next()
}

/// The expression right of `=>`, or the sole expression of a list item.
pub fn array_item_value(node: &SyntaxNode) -> Option<SyntaxNode> {
    let item = array_item_at(node)?;
    let keyed = has_direct_token(&item, TK_ARROW);
    let mut children = item.children();
    let first = children.next()?;
    if !keyed {
        return Some(first);
    }
    Some(children.next().unwrap_or(first))
}
