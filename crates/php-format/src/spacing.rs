//! What stands between two tokens no layout rule has decided: nothing, a
//! space, or the line break the source had at a place an expression may be
//! split at.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::Kind;

use crate::tokens::{Blank, Doc, Gap, Indent};

/// Keywords written like a function name: no space before their `(`.
const CALLED_KEYWORDS: &[&str] = &[
    "array",
    "declare",
    "die",
    "empty",
    "eval",
    "exit",
    "fn",
    "include",
    "include_once",
    "isset",
    "list",
    "parent",
    "print",
    "require",
    "require_once",
    "self",
    "static",
    "unset",
];

/// The gap between `prev` and `next`, the token written before it.
pub(crate) fn auto(doc: &Doc, prev: usize, next: usize) -> Gap {
    if doc.toks[next].nl > 0 && breakable(doc, prev, next) {
        return Gap::Break(Blank::None, Indent::Continue);
    }
    if space(doc, prev, next) {
        Gap::Space
    } else {
        Gap::Glue
    }
}

/// Whether a line break of the source between the two tokens is kept.
fn breakable(doc: &Doc, prev: usize, next: usize) -> bool {
    // A heredoc starts on the line of what it belongs to.
    if doc.kind(next) == TK_STRING && doc.source_text(next).starts_with("<<<") {
        return false;
    }
    if in_error(doc, prev) || in_error(doc, next) {
        return true;
    }
    if is_binary_operator(doc, prev) || is_binary_operator(doc, next) {
        return true;
    }
    let kind = doc.kind(next);
    if kind == TK_OBJECT_OPERATOR || kind == TK_NULLSAFE_OBJECT_OPERATOR {
        return true;
    }
    if is_ternary_operator(doc, next) || is_ternary_operator(doc, prev) {
        return !(doc.kind(prev) == TK_QUESTION && doc.kind(next) == TK_COLON);
    }
    if is_assignment_operator(doc, prev) || is_assignment_operator(doc, next) {
        return true;
    }
    if doc.kind(prev) == TK_ARROW {
        return doc.parent_kind(prev) != PHP_ARROW_FUNCTION;
    }
    doc.kind(prev) == TK_COMMA
}

fn in_error(doc: &Doc, index: usize) -> bool {
    doc.parent_kind(index) == ERROR
}

/// The operator of a binary expression: every token of the expression
/// itself, its operands being nodes.
pub(crate) fn is_binary_operator(doc: &Doc, index: usize) -> bool {
    doc.parent_kind(index) == PHP_BINARY_EXPRESSION
}

fn is_ternary_operator(doc: &Doc, index: usize) -> bool {
    let kind = doc.kind(index);
    (kind == TK_QUESTION || kind == TK_COLON) && doc.parent_kind(index) == PHP_TERNARY_EXPRESSION
}

/// `=` and the compound assignment operators, where a value follows.
fn is_assignment_operator(doc: &Doc, index: usize) -> bool {
    let parent = doc.parent_kind(index);
    let kind = doc.kind(index);
    if parent == PHP_ASSIGNMENT_EXPRESSION {
        return (kind == TK_EQUALS || kind == TK_OPERATOR) && !in_declare(doc, index);
    }
    kind == TK_EQUALS
        && [
            PHP_PROPERTY_DECLARATION,
            PHP_CLASS_CONST_DECLARATION,
            PHP_CONST_DECLARATION,
            PHP_PARAMETER,
            PHP_ENUM_CASE_DECLARATION,
            PHP_STATIC_STATEMENT,
        ]
        .contains(&parent)
}

/// Whether the token is inside the parentheses of `declare(…)`.
fn in_declare(doc: &Doc, index: usize) -> bool {
    doc.toks[index]
        .parent
        .ancestors()
        .find(|node| node.kind() == PHP_FUNCTION_CALL)
        .and_then(|call| doc.first(&call))
        .is_some_and(|first| doc.is_keyword(first, "declare"))
}

fn is_word(kind: Kind) -> bool {
    kind == TK_IDENTIFIER
        || kind == TK_KEYWORD
        || kind == TK_VARIABLE
        || kind == TK_NUMBER
        || kind == TK_STRING
}

/// Whether the source had nothing between the two tokens.
fn adjacent(doc: &Doc, prev: usize, next: usize) -> bool {
    doc.toks[prev].end == doc.toks[next].start
}

/// Whether the token is the first of its parent node.
fn leads_parent(doc: &Doc, index: usize) -> bool {
    doc.first(&doc.toks[index].parent) == Some(index)
}

/// Whether a space stands between the two tokens.
pub(crate) fn space(doc: &Doc, prev: usize, next: usize) -> bool {
    let (before, after) = (doc.kind(prev), doc.kind(next));
    let (before_parent, after_parent) = (doc.parent_kind(prev), doc.parent_kind(next));

    // Around `$$name` and `${…}` the parser does not tell a binary operator
    // from a unary one: an operator there stays as it is set.
    if (before == TK_OPERATOR || after == TK_OPERATOR)
        && (doc.raw[prev] || doc.source_text(next) == "$" || doc.source_text(prev) == "$")
    {
        return !adjacent(doc, prev, next);
    }
    // The `&` of `$a = &$b`, which the parser reads as an error: it stands
    // by what it refers to.
    if before == TK_AMPERSAND && in_error(doc, prev) {
        return false;
    }
    if after == TK_AMPERSAND && in_error(doc, next) {
        return true;
    }
    if in_error(doc, prev) || in_error(doc, next) {
        return !adjacent(doc, prev, next);
    }
    if after == TK_SEMICOLON || after == TK_COMMA {
        return false;
    }
    if before == TK_OPEN_PAREN || before == TK_OPEN_BRACKET || before == TK_ATTRIBUTE_OPEN {
        return false;
    }
    if after == TK_CLOSE_PAREN || after == TK_CLOSE_BRACKET {
        return false;
    }
    if before == TK_COMMA {
        return true;
    }
    if before == TK_SEMICOLON {
        return true;
    }
    for kind in [before, after] {
        if kind == TK_OBJECT_OPERATOR
            || kind == TK_NULLSAFE_OBJECT_OPERATOR
            || kind == TK_SCOPE_RESOLUTION
        {
            return false;
        }
    }
    if before == TK_BACKSLASH {
        return false;
    }
    // A backslash inside a name stands by the part before it; one that
    // starts a name is spaced like the name.
    let after = if after == TK_BACKSLASH {
        if (before == TK_IDENTIFIER || before == TK_KEYWORD) && adjacent(doc, prev, next) {
            return false;
        }
        TK_IDENTIFIER
    } else {
        after
    };

    // Two operators the source set apart stay apart: `- -$a` is not `--$a`.
    if before == TK_OPERATOR && after == TK_OPERATOR && !adjacent(doc, prev, next) {
        return true;
    }

    // Casts: nothing inside the parentheses, one space after them.
    if before == TK_CLOSE_PAREN && before_parent == PHP_CAST_EXPRESSION {
        return true;
    }

    // Unary operators stand next to their operand.
    if before == TK_OPERATOR && before_parent == PHP_UNARY_EXPRESSION && leads_parent(doc, prev) {
        return false;
    }
    if after == TK_OPERATOR && after_parent == PHP_UNARY_EXPRESSION && !leads_parent(doc, next) {
        return false;
    }

    if is_binary_operator(doc, prev) || is_binary_operator(doc, next) {
        return true;
    }
    for (index, kind, parent) in [(prev, before, before_parent), (next, after, after_parent)] {
        if kind == TK_EQUALS || (kind == TK_OPERATOR && parent == PHP_ASSIGNMENT_EXPRESSION) {
            return !in_declare(doc, index);
        }
        if kind == TK_ARROW {
            return true;
        }
    }

    // `?`: a nullable type or a ternary.
    if before == TK_QUESTION {
        if before_parent == PHP_TERNARY_EXPRESSION {
            return after != TK_COLON;
        }
        return before_parent != PHP_NULLABLE_TYPE && !adjacent(doc, prev, next);
    }
    if after == TK_QUESTION && after_parent == PHP_TERNARY_EXPRESSION {
        return true;
    }
    if after == TK_COLON {
        return after_parent == PHP_TERNARY_EXPRESSION && before != TK_QUESTION;
    }
    if before == TK_COLON {
        return true;
    }

    // `&`: an intersection type, or a reference that stands by its target.
    if before == TK_AMPERSAND {
        return false;
    }
    if after == TK_AMPERSAND && after_parent == PHP_INTERSECTION_TYPE {
        return false;
    }
    // `|` in a type; the types of a catch are set apart.
    for (index, kind, parent) in [(prev, before, before_parent), (next, after, after_parent)] {
        if kind == TK_PIPE && parent == PHP_UNION_TYPE {
            let node = &doc.toks[index].parent;
            return node
                .parent()
                .is_some_and(|outer| outer.kind() == PHP_CATCH_CLAUSE);
        }
    }
    if before == TK_ELLIPSIS {
        return false;
    }

    if after == TK_OPEN_PAREN {
        return space_before_paren(doc, prev, next);
    }
    if after == TK_OPEN_BRACKET {
        if before == TK_KEYWORD {
            return before_parent != PHP_NAME && !doc.is_keyword(prev, "static");
        }
        return !(is_word(before)
            || before == TK_CLOSE_PAREN
            || before == TK_CLOSE_BRACKET
            || before == TK_CLOSE_BRACE);
    }
    // Braces stand apart, but for a name computed in them: `$a->{$b}`.
    if after == TK_OPEN_BRACE || after == TK_CLOSE_BRACE {
        return !names_member(doc, next);
    }
    if before == TK_OPEN_BRACE {
        return !names_member(doc, prev);
    }
    if is_word(before) && after == TK_QUESTION {
        return true;
    }
    if is_word(before) && is_word(after) {
        return true;
    }
    if (before == TK_CLOSE_PAREN || before == TK_CLOSE_BRACKET || before == TK_CLOSE_BRACE)
        && is_word(after)
    {
        return true;
    }
    // `fn&()`: nothing follows `fn`.
    if after == TK_AMPERSAND && doc.is_keyword(prev, "fn") {
        return false;
    }
    if is_word(before) && after == TK_ELLIPSIS || is_word(before) && after == TK_AMPERSAND {
        return true;
    }
    if is_word(before) && after == TK_ATTRIBUTE_OPEN {
        return true;
    }
    !adjacent(doc, prev, next)
}

/// Whether the brace encloses the name of a member or of a variable.
fn names_member(doc: &Doc, brace: usize) -> bool {
    [
        PHP_VARIABLE,
        PHP_MEMBER_ACCESS,
        PHP_MEMBER_CALL,
        PHP_SCOPED_ACCESS,
        PHP_SCOPED_CALL,
    ]
    .contains(&doc.parent_kind(brace))
}

fn space_before_paren(doc: &Doc, prev: usize, next: usize) -> bool {
    let before = doc.kind(prev);
    if before == TK_KEYWORD {
        if doc.parent_kind(prev) == PHP_NAME {
            return false;
        }
        // A cast is no argument list: `print (int) $a`.
        if doc.parent_kind(next) == PHP_CAST_EXPRESSION {
            return true;
        }
        // `private(set)`: the visibility of writing a property. A type in
        // parentheses stands apart: `protected (A&B)|null $c`.
        let asymmetric = ["private", "protected", "public"]
            .iter()
            .any(|word| doc.is_keyword(prev, word))
            && doc
                .toks
                .get(next + 1)
                .is_some_and(|_| doc.source_text(next + 1).eq_ignore_ascii_case("set"))
            && doc.pair[next] == next + 2;
        let word = doc.source_text(prev);
        return !asymmetric
            && !CALLED_KEYWORDS
                .iter()
                .any(|called| word.eq_ignore_ascii_case(called));
    }
    if is_word(before)
        || before == TK_CLOSE_PAREN
        || before == TK_CLOSE_BRACKET
        || before == TK_CLOSE_BRACE
    {
        return false;
    }
    !adjacent(doc, prev, next)
}
