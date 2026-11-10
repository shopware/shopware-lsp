//! Sets a string at a dot-separated key path in a JSON document.
//!
//! The snippet commands use this to add a translation key to a snippet file.
//! The document is parsed into its lossless CST and changed with one
//! validated source edit, so everything outside the touched value keeps its
//! original text. Every path segment names an object key; a `\` escapes the
//! next character, so `a\.b` addresses the key `a.b`.

use swlsp_parser_json::syntax::{
    JSON_ARRAY, JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_PAIR, JSON_STRING,
    TK_CLOSE_BRACE, TK_COMMA, TK_OPEN_BRACE,
};
use swlsp_rewrite::{Builder, RewriteError};
use swlsp_syntax::{SyntaxNode, SyntaxToken};

/// Why a value could not be set.
#[derive(Debug, thiserror::Error)]
pub(crate) enum SetError {
    #[error("key path is empty")]
    EmptyPath,
    #[error("document root is not a JSON object")]
    RootNotObject,
    #[error("JSON object at byte {offset} is not closed")]
    UnclosedObject { offset: u32 },
    #[error("apply JSON edit")]
    Rewrite(#[from] RewriteError),
}

/// Returns `source` with the string `value` stored at `path`.
///
/// An existing value at the path is replaced, missing keys are appended to
/// their parent object, and intermediate objects are created as needed. An
/// intermediate value that is not an object is replaced by one. A blank
/// document is treated as an empty object.
pub(crate) fn set_string(source: &str, path: &str, value: &str) -> Result<String, SetError> {
    let segments = split_path(path)?;
    if source.trim().is_empty() {
        return Ok(nested_value(&segments, value));
    }
    let parse = swlsp_parser_json::parse(source);
    let mut object = parse
        .root()
        .children()
        .find(is_value)
        .filter(|node| node.kind() == JSON_OBJECT)
        .ok_or(SetError::RootNotObject)?;
    let mut builder = Builder::new(source);
    for (index, segment) in segments.iter().enumerate() {
        let rest = &segments[index + 1..];
        let Some(pair) = find_pair(&object, segment) else {
            insert_member(&mut builder, &object, &segments[index..], value)?;
            break;
        };
        match pair_value(&pair) {
            Some(child) if child.kind() == JSON_OBJECT && !rest.is_empty() => object = child,
            Some(child) => {
                builder.replace(&child, nested_value(rest, value))?;
                break;
            }
            None => {
                builder.replace(&pair, member(&segments[index..], value))?;
                break;
            }
        }
    }
    Ok(swlsp_rewrite::apply(source, &builder.finish()?)?)
}

/// Splits `path` at unescaped dots, removing the escapes.
fn split_path(path: &str) -> Result<Vec<String>, SetError> {
    if path.is_empty() {
        return Err(SetError::EmptyPath);
    }
    let mut segments = vec![String::new()];
    let mut characters = path.chars();
    while let Some(character) = characters.next() {
        let character = match character {
            '.' => {
                segments.push(String::new());
                continue;
            }
            '\\' => match characters.next() {
                Some(escaped) => escaped,
                None => continue,
            },
            other => other,
        };
        if let Some(segment) = segments.last_mut() {
            segment.push(character);
        }
    }
    Ok(segments)
}

fn is_value(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        JSON_OBJECT | JSON_ARRAY | JSON_STRING | JSON_NUMBER | JSON_BOOLEAN | JSON_NULL_VALUE
    )
}

/// The first pair of `object` whose decoded key equals `key`.
fn find_pair(object: &SyntaxNode, key: &str) -> Option<SyntaxNode> {
    object
        .children()
        .filter(|child| child.kind() == JSON_PAIR)
        .find(|pair| {
            pair.children()
                .find(|child| child.kind() == JSON_STRING)
                .is_some_and(|node| decode_string(&node.text().to_string()) == key)
        })
}

/// The value after the key of `pair`, if the pair is complete.
fn pair_value(pair: &SyntaxNode) -> Option<SyntaxNode> {
    let mut children = pair.children();
    children.find(|child| child.kind() == JSON_STRING)?;
    children.find(is_value)
}

/// The text of a JSON string literal; invalid escapes fall back to the raw
/// text between the quotes.
fn decode_string(literal: &str) -> String {
    serde_json::from_str(literal).unwrap_or_else(|_| {
        let inner = literal.strip_prefix('"').unwrap_or(literal);
        inner.strip_suffix('"').unwrap_or(inner).to_owned()
    })
}

/// Appends the member for `segments` after the last member of `object`,
/// reusing a trailing comma when there is one.
fn insert_member(
    builder: &mut Builder,
    object: &SyntaxNode,
    segments: &[String],
    value: &str,
) -> Result<(), SetError> {
    let unclosed = || SetError::UnclosedObject {
        offset: object.text_range().start().into(),
    };
    let tokens: Vec<SyntaxToken> = object
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .collect();
    if !tokens.iter().any(|token| token.kind() == TK_CLOSE_BRACE) {
        return Err(unclosed());
    }
    let text = member(segments, value);
    let Some(last) = object
        .children()
        .filter(|child| child.kind() == JSON_PAIR)
        .last()
    else {
        let open = tokens
            .iter()
            .find(|token| token.kind() == TK_OPEN_BRACE)
            .ok_or_else(unclosed)?;
        builder.insert_after(open, text)?;
        return Ok(());
    };
    let trailing_comma = tokens.iter().find(|token| {
        token.kind() == TK_COMMA && token.text_range().start() >= last.text_range().end()
    });
    match trailing_comma {
        Some(comma) => builder.insert_after(comma, text)?,
        None => builder.insert_after(&last, format!(",{text}"))?,
    }
    Ok(())
}

/// `"first":<nested value for the rest>`.
fn member(segments: &[String], value: &str) -> String {
    let Some((first, rest)) = segments.split_first() else {
        return quote(value);
    };
    format!("{}:{}", quote(first), nested_value(rest, value))
}

/// The JSON value storing `value` below `segments`: the quoted string when
/// no segments remain, otherwise nested single-member objects.
fn nested_value(segments: &[String], value: &str) -> String {
    if segments.is_empty() {
        quote(value)
    } else {
        format!("{{{}}}", member(segments, value))
    }
}

fn quote(value: &str) -> String {
    serde_json::Value::from(value).to_string()
}
