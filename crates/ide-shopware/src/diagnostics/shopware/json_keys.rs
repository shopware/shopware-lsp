//! The JSON tree queries the metadata and snippet file analyzers share.

use swlsp_parser_json::syntax::{
    JSON_ARRAY, JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_PAIR, JSON_STRING,
    TK_STRING,
};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

/// The document's top-level object.
pub(crate) fn root_object(root: &SyntaxNode) -> Option<SyntaxNode> {
    root.descendants().find(|node| node.kind() == JSON_OBJECT)
}

pub(crate) fn pairs(object: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> + use<> {
    object.children().filter(|pair| pair.kind() == JSON_PAIR)
}

/// The unquoted text and range of a pair's key.
pub(crate) fn pair_key(pair: &SyntaxNode) -> Option<(String, TextRange)> {
    let key = pair.children().find(|child| child.kind() == JSON_STRING)?;
    let token = key
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == TK_STRING)?;
    let text = token.text();
    let inner = text.strip_prefix('"')?.strip_suffix('"')?;
    let start = token.text_range().start() + TextSize::from(1);
    Some((
        inner.to_owned(),
        TextRange::new(start, start + TextSize::of(inner)),
    ))
}

/// The value of a pair: the first value node after its key.
pub(crate) fn pair_value(pair: &SyntaxNode) -> Option<SyntaxNode> {
    pair.children().skip(1).find(|child| {
        matches!(
            child.kind(),
            JSON_OBJECT | JSON_ARRAY | JSON_STRING | JSON_NUMBER | JSON_BOOLEAN | JSON_NULL_VALUE
        )
    })
}

/// The key at `path` (object keys from the document root) when every
/// segment exists, otherwise the deepest key that does, otherwise the whole
/// document.
pub(crate) fn nearest_key_range(root: &SyntaxNode, path: &[&str]) -> TextRange {
    let mut found = root.text_range();
    let Some(mut object) = root_object(root) else {
        return found;
    };
    for key in path {
        let Some(pair) =
            pairs(&object).find(|pair| pair_key(pair).is_some_and(|(text, _)| text == *key))
        else {
            break;
        };
        if let Some((_, range)) = pair_key(&pair) {
            found = range;
        }
        match pair_value(&pair) {
            Some(value) if value.kind() == JSON_OBJECT => object = value,
            _ => break,
        }
    }
    found
}
