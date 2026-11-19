//! Keywords, native types and the constants `true`, `false` and `null` are
//! written in lower case, and casts in their short form.

use swlsp_parser_php::syntax::*;

use crate::tokens::Doc;

/// The names that are types of the language wherever a type stands.
pub(crate) const NATIVE_TYPES: &[&str] = &[
    "array", "bool", "callable", "false", "float", "int", "iterable", "mixed", "never", "null",
    "object", "parent", "self", "static", "string", "true", "void",
];

/// The long forms of casts with their short ones.
pub(crate) const LONG_CASTS: &[(&str, &str)] = &[
    ("binary", "string"),
    ("boolean", "bool"),
    ("double", "float"),
    ("integer", "int"),
    ("real", "float"),
];

/// Language constructs that are called like functions.
const CONSTRUCTS: &[&str] = &[
    "array", "declare", "die", "empty", "eval", "exit", "isset", "list", "unset",
];

const IMPORT_KEYWORDS: &[&str] = &["as", "const", "function", "use"];
const TRAIT_KEYWORDS: &[&str] = &["as", "insteadof", "private", "protected", "public", "use"];

fn has(words: &[&str], word: &str) -> bool {
    words.iter().any(|known| word.eq_ignore_ascii_case(known))
}

pub(crate) fn run(doc: &mut Doc) {
    for index in 0..doc.toks.len() {
        if let Some(text) = lowered(doc, index) {
            doc.toks[index].text = Some(text);
        }
    }
}

fn lowered(doc: &Doc, index: usize) -> Option<String> {
    let kind = doc.kind(index);
    if kind != TK_KEYWORD && kind != TK_IDENTIFIER {
        return None;
    }
    let word = doc.source_text(index);
    let parent = doc.toks[index].parent.clone();
    let parent_kind = parent.kind();
    if parent_kind == PHP_CAST_EXPRESSION {
        let short = LONG_CASTS
            .iter()
            .find(|(long, _)| word.eq_ignore_ascii_case(long))
            .map_or_else(
                || word.to_ascii_lowercase(),
                |(_, short)| (*short).to_owned(),
            );
        return (short != word).then_some(short);
    }
    if !word.bytes().any(|byte| byte.is_ascii_uppercase()) {
        return None;
    }
    // A name: of a member, in a namespace.
    let part_of_name = |at: Option<usize>| {
        at.is_some_and(|at| matches!(doc.source_text(at), "->" | "?->" | "::" | "\\"))
    };
    if part_of_name(doc.prev_sig(index)) {
        return None;
    }
    if doc
        .next_sig(index)
        .is_some_and(|next| doc.kind(next) == TK_BACKSLASH)
    {
        return None;
    }
    let lower = if parent_kind == PHP_BOOLEAN || parent_kind == PHP_NULL {
        true
    } else if parent_kind == PHP_NAME {
        let single = doc.first(&parent) == doc.last(&parent);
        let owner = parent.parent().map(|node| node.kind());
        single
            && (owner == Some(PHP_TYPE) && has(NATIVE_TYPES, word)
                || owner == Some(PHP_FUNCTION_CALL) && has(CONSTRUCTS, word)
                || has(&["parent", "self", "static"], word)
                    && [PHP_OBJECT_CREATION, PHP_SCOPED_ACCESS, PHP_SCOPED_CALL]
                        .iter()
                        .any(|kind| owner == Some(*kind)))
    } else if kind == TK_IDENTIFIER {
        // The backing type of an enum.
        parent_kind == PHP_ENUM_DECLARATION
            && has(NATIVE_TYPES, word)
            && doc
                .prev_sig(index)
                .is_some_and(|before| doc.kind(before) == TK_COLON)
    } else if parent_kind == PHP_USE_DECLARATION {
        has(IMPORT_KEYWORDS, word)
    } else if parent_kind == PHP_TRAIT_USE_DECLARATION {
        has(TRAIT_KEYWORDS, word)
    } else {
        parent_kind != PHP_NAMED_ARGUMENT && parent_kind != ERROR
    };
    lower.then(|| word.to_ascii_lowercase())
}
