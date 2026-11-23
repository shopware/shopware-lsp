//! The place of the cursor inside a flow mapping (`{ key: value }`) of a
//! module description.

use std::collections::BTreeSet;

use swlsp_parser_yaml::query as yaml;
use swlsp_parser_yaml::syntax::{
    TK_CLOSE_BRACE, TK_CLOSE_BRACKET, TK_COMMA, TK_OPEN_BRACE, TK_OPEN_BRACKET, YAML_FLOW_MAPPING,
    YAML_FLOW_SEQUENCE,
};
use swlsp_syntax::{Kind, SyntaxNode, TextRange, TextSize};

use super::completion::{Place, key_name, key_start};

/// The offsets of the direct tokens of `node` that are of `kind`.
fn tokens(node: &SyntaxNode, kind: Kind) -> impl Iterator<Item = TextRange> + use<> {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(move |token| token.kind() == kind)
        .map(|token| token.text_range())
}

/// Whether `offset` is between the brackets of the flow collection `node`;
/// one that is not closed yet reaches to the end of its node.
fn holds(node: &SyntaxNode, open: Kind, close: Kind, offset: usize) -> bool {
    let Some(open) = tokens(node, open).next() else {
        // A pair of a flow sequence: a mapping without braces.
        return false;
    };
    usize::from(open.end()) <= offset
        && tokens(node, close)
            .last()
            .is_none_or(|close| offset <= usize::from(close.start()))
}

fn word_end(text: &str, stops: &[char]) -> usize {
    text.find(|character: char| character.is_whitespace() || stops.contains(&character))
        .unwrap_or(text.len())
}

/// The place of `offset` when it is inside a flow collection: `None` when
/// it is in none, `Some(None)` when nothing is completed there.
pub(super) fn flow_place(source: &str, root: &SyntaxNode, offset: usize) -> Option<Option<Place>> {
    // The innermost collection is the last one that starts before the cursor.
    let collection = yaml::nodes(root, &[YAML_FLOW_MAPPING, YAML_FLOW_SEQUENCE])
        .into_iter()
        .rfind(|node| {
            holds(node, TK_OPEN_BRACE, TK_CLOSE_BRACE, offset)
                || holds(node, TK_OPEN_BRACKET, TK_CLOSE_BRACKET, offset)
        })?;
    Some(mapping_place(source, &collection, offset))
}

fn mapping_place(source: &str, mapping: &SyntaxNode, offset: usize) -> Option<Place> {
    if mapping.kind() != YAML_FLOW_MAPPING {
        return None;
    }
    // The entry under the cursor starts after the last brace or comma.
    let entry = tokens(mapping, TK_OPEN_BRACE)
        .chain(tokens(mapping, TK_COMMA))
        .map(|range| usize::from(range.end()))
        .filter(|end| *end <= offset)
        .max()?;
    let before = source.get(entry..offset)?;
    let start = entry + (before.len() - before.trim_start().len());
    let typed = &source[start..offset];
    let rest = &source[offset..];
    let size = |index: usize| TextSize::from(index as u32);
    let mut path = yaml::pair_path(mapping);
    let place = |path, value, range, existing, complete| Place {
        path,
        value,
        range,
        existing,
        indent: 0,
        step: 0,
        complete,
        flow: true,
    };
    // A quoted key needs no space after its colon.
    let colon = typed
        .find(": ")
        .or_else(|| {
            typed
                .find("\":")
                .or(typed.find("':"))
                .map(|quote| quote + 1)
        })
        .or_else(|| typed.strip_suffix(':').map(str::len));
    if let Some(colon) = colon {
        let key = typed[..colon].trim().trim_matches(['"', '\'']);
        let after = start + colon + 1;
        let value = &source[after..offset];
        let value_start = after + (value.len() - value.trim_start().len());
        if key.is_empty()
            || key.contains(char::is_whitespace)
            || source[value_start..offset].contains(char::is_whitespace)
        {
            return None;
        }
        path.push(key.to_owned());
        let end = offset + word_end(rest, &[',', '}']);
        return Some(place(
            path,
            true,
            TextRange::new(size(value_start), size(end)),
            BTreeSet::new(),
            false,
        ));
    }
    if typed.contains(char::is_whitespace) {
        return None;
    }
    let end = offset + word_end(rest, &[':', ',', '}']);
    let existing = yaml::pairs(mapping)
        .filter(|pair| key_start(pair).is_none_or(|key| !(start..=end).contains(&key)))
        .map(|pair| key_name(&pair))
        .collect();
    Some(place(
        path,
        false,
        TextRange::new(size(start), size(end)),
        existing,
        source[end..].starts_with(':'),
    ))
}
