//! Read-only queries over YAML syntax trees.
//!
//! Functions take the node they inspect and return `None` (or an empty
//! result) when it is not of the expected kind, so lookups chain with
//! `Option::and_then`.

mod decode;

pub use decode::{
    DecodeError, Value, decode, decode_block_scalar, decode_double_quoted, decode_value, is_blank,
    is_yaml_float,
};

use swlsp_syntax::{Kind, SyntaxNode, SyntaxToken};

use crate::syntax::*;

/// All nodes of the given kinds in `root`'s subtree (including `root`), in
/// preorder.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| kinds.contains(&node.kind()))
        .collect()
}

/// The value of the first document in a `YAML_STREAM` (or the first value
/// child of `root`).
pub fn root_value(root: &SyntaxNode) -> Option<SyntaxNode> {
    for child in root.children() {
        if child.kind() == YAML_DOCUMENT
            && let Some(value) = child.children().find(is_value)
        {
            return Some(value);
        }
        if is_value(&child) {
            return Some(child);
        }
    }
    None
}

/// Whether `node` is a mapping, sequence, scalar, or implicit null.
pub fn is_value(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        YAML_MAPPING
            | YAML_FLOW_MAPPING
            | YAML_SEQUENCE
            | YAML_FLOW_SEQUENCE
            | YAML_SCALAR
            | YAML_NULL
    )
}

/// Whether `node` is a block or flow mapping.
pub fn is_mapping(node: &SyntaxNode) -> bool {
    matches!(node.kind(), YAML_MAPPING | YAML_FLOW_MAPPING)
}

/// Whether `node` is a block or flow sequence.
pub fn is_sequence(node: &SyntaxNode) -> bool {
    matches!(node.kind(), YAML_SEQUENCE | YAML_FLOW_SEQUENCE)
}

/// Whether `node` is an implicit null or a plain `~` / `null` scalar
/// (case-insensitive). Quoted `'null'` is a string.
pub fn is_null(node: &SyntaxNode) -> bool {
    if node.kind() == YAML_NULL {
        return true;
    }
    match scalar_token(node) {
        Some(token) if token.kind() == TK_PLAIN_SCALAR => {
            let text = token.text().trim();
            text == "~" || text.eq_ignore_ascii_case("null")
        }
        _ => false,
    }
}

/// The `YAML_PAIR` children of a mapping.
pub fn pairs(mapping: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    let children = is_mapping(mapping).then(|| mapping.children());
    children
        .into_iter()
        .flatten()
        .filter(|child| child.kind() == YAML_PAIR)
}

/// The key scalar of a pair.
pub fn pair_key(pair: &SyntaxNode) -> Option<SyntaxNode> {
    if pair.kind() != YAML_PAIR {
        return None;
    }
    pair.children().find(|child| child.kind() == YAML_SCALAR)
}

/// The value of a pair. An anchor/tag decorator (`key: &name` followed by an
/// indented block) is skipped in favour of the decorated collection.
pub fn pair_value(pair: &SyntaxNode) -> Option<SyntaxNode> {
    if pair.kind() != YAML_PAIR {
        return None;
    }
    let mut values = pair
        .children()
        .skip_while(|child| child.kind() != YAML_SCALAR)
        .skip(1);
    decorated_value(&mut values)
}

/// The value of an item of a block or flow sequence, skipping decorators
/// like [`pair_value`].
pub fn item_value(item: &SyntaxNode) -> Option<SyntaxNode> {
    if item.kind() != YAML_SEQUENCE_ITEM {
        return None;
    }
    decorated_value(&mut item.children())
}

fn decorated_value(children: &mut dyn Iterator<Item = SyntaxNode>) -> Option<SyntaxNode> {
    let mut values = children.filter(is_value);
    let first = values.next()?;
    if is_collection_decorator(&first)
        && let Some(decorated) = values.next()
    {
        return Some(decorated);
    }
    Some(first)
}

/// The value of the pair named `name` in `mapping`.
pub fn property(mapping: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    property_pair(mapping, name).and_then(|pair| pair_value(&pair))
}

/// The first pair of `mapping` whose key scalar value equals `name`.
pub fn property_pair(mapping: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    pairs(mapping).find(|pair| pair_key(pair).is_some_and(|key| scalar_value(&key) == name))
}

/// The `YAML_SEQUENCE_ITEM` children of a sequence.
pub fn items(sequence: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    let children = is_sequence(sequence).then(|| sequence.children());
    children
        .into_iter()
        .flatten()
        .filter(|child| child.kind() == YAML_SEQUENCE_ITEM)
}

/// The unescaped-quote content of a scalar: quotes are stripped (escapes are
/// kept), block scalars are returned verbatim, plain scalars are trimmed.
/// Empty for anything that is not a scalar.
pub fn scalar_value(node: &SyntaxNode) -> String {
    let Some(token) = scalar_token(node) else {
        return String::new();
    };
    let text = token.text();
    let unquote = |quote: char| match text.strip_prefix(quote) {
        Some(inner) if text.len() >= 2 && text.ends_with(quote) => &inner[..inner.len() - 1],
        Some(inner) => inner,
        None => text,
    };
    match token.kind() {
        TK_SINGLE_QUOTED_SCALAR => unquote('\''),
        TK_DOUBLE_QUOTED_SCALAR => unquote('"'),
        TK_BLOCK_SCALAR => text,
        _ => text.trim(),
    }
    .to_owned()
}

/// The node text without surrounding whitespace.
pub fn raw_text(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

/// The nearest `YAML_PAIR` at or above `node`.
pub fn ancestor_pair(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == YAML_PAIR)
}

/// The keys of all pairs enclosing `node`, outermost first.
pub fn pair_path(node: &SyntaxNode) -> Vec<String> {
    let mut path: Vec<String> = node
        .ancestors()
        .filter(|current| current.kind() == YAML_PAIR)
        .map(|pair| {
            pair_key(&pair)
                .map(|key| scalar_value(&key))
                .unwrap_or_default()
        })
        .collect();
    path.reverse();
    path
}

/// Whether `descendant` is `ancestor` or lies inside it.
pub fn contains(ancestor: &SyntaxNode, descendant: &SyntaxNode) -> bool {
    descendant.ancestors().any(|current| current == *ancestor)
}

/// The scalar token of a `YAML_SCALAR` node.
pub fn scalar_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    if node.kind() != YAML_SCALAR {
        return None;
    }
    node.children_with_tokens()
        .filter_map(|child| child.into_token())
        .find(|token| {
            matches!(
                token.kind(),
                TK_PLAIN_SCALAR
                    | TK_SINGLE_QUOTED_SCALAR
                    | TK_DOUBLE_QUOTED_SCALAR
                    | TK_BLOCK_SCALAR
            )
        })
}

fn is_collection_decorator(node: &SyntaxNode) -> bool {
    if node.kind() != YAML_SCALAR {
        return false;
    }
    let value = scalar_value(node);
    value.len() >= 2
        && (value.starts_with('&') || value.starts_with('!'))
        && !value.contains([' ', '\t'])
}

#[cfg(test)]
mod tests;
