//! The changes PER Coding Style asks for that are more than white space:
//! braces around every body, `elseif`, parentheses after `new`, the short
//! array syntax, one trait and one property per statement, the order of
//! modifiers and of imports. They are text edits to the source, which is
//! then parsed again and laid out.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::{Kind, SyntaxNode};

use crate::tokens::{Doc, NONE};

pub(crate) struct Edit {
    start: usize,
    end: usize,
    text: String,
}

impl Edit {
    fn insert(at: usize, text: &str) -> Self {
        Self {
            start: at,
            end: at,
            text: text.to_owned(),
        }
    }

    fn replace(start: usize, end: usize, text: impl Into<String>) -> Self {
        Self {
            start,
            end,
            text: text.into(),
        }
    }
}

/// The modifiers of a declaration with their place in the order of PER
/// Coding Style: inheritance, visibility, scope, mutation.
const MODIFIERS: &[(&str, u8)] = &[
    ("abstract", 0),
    ("final", 0),
    ("public", 1),
    ("protected", 1),
    ("private", 1),
    ("var", 1),
    ("static", 2),
    ("readonly", 3),
];

pub(crate) fn edits(doc: &Doc, root: &SyntaxNode) -> Vec<Edit> {
    let mut edits = Vec::new();
    for node in root.descendants() {
        let kind = node.kind();
        if is_control(kind) {
            braces(doc, &node, &mut edits);
        } else if kind == PHP_OBJECT_CREATION {
            creation(doc, &node, &mut edits);
        } else if kind == PHP_ATTRIBUTE {
            empty_arguments(doc, &node, &mut edits);
        } else if kind == PHP_ATTRIBUTE_GROUP {
            attribute_blocks(doc, &node, &mut edits);
        } else if kind == PHP_ARRAY {
            short_array(doc, &node, &mut edits);
        } else if kind == PHP_TRAIT_USE_DECLARATION {
            one_trait(doc, &node, &mut edits);
        } else if kind == PHP_USE_DECLARATION {
            import_names(doc, &node, &mut edits);
        } else if kind == PHP_PROPERTY_DECLARATION {
            one_property(doc, &node, &mut edits);
            modifiers(doc, &node, true, &mut edits);
        } else if kind == PHP_METHOD_DECLARATION || kind == PHP_CLASS_CONST_DECLARATION {
            modifiers(doc, &node, true, &mut edits);
        } else if kind == PHP_CLASS_DECLARATION || kind == PHP_PARAMETER {
            modifiers(doc, &node, false, &mut edits);
        } else if kind == PHP_PROGRAM || kind == PHP_BLOCK {
            import_order(doc, &node, &mut edits);
        }
    }
    edits
}

/// `source` with the edits made. Edits that overlap an earlier one are left
/// for the next round.
pub(crate) fn apply(source: &str, mut edits: Vec<Edit>) -> String {
    edits.sort_by_key(|edit| (edit.start, edit.end));
    let mut out = String::with_capacity(source.len() + 64);
    let mut at = 0;
    for edit in edits {
        if edit.start < at {
            continue;
        }
        out.push_str(&source[at..edit.start]);
        out.push_str(&edit.text);
        at = edit.end;
    }
    out.push_str(&source[at..]);
    out
}

fn is_control(kind: Kind) -> bool {
    [
        PHP_IF_STATEMENT,
        PHP_ELSE_IF_CLAUSE,
        PHP_ELSE_CLAUSE,
        PHP_WHILE_STATEMENT,
        PHP_FOR_STATEMENT,
        PHP_FOREACH_STATEMENT,
        PHP_DO_WHILE_STATEMENT,
    ]
    .contains(&kind)
}

/// The body of a control structure is enclosed by braces, and `else if`
/// is `elseif`.
fn braces(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let kind = node.kind();
    let after_parentheses = doc
        .children(node)
        .find(|&at| doc.kind(at) == TK_OPEN_PAREN)
        .map(|open| doc.pair[open]);
    let body = node.children().find(|child| {
        let kind = child.kind();
        if kind == PHP_PARENTHESIZED || kind == PHP_ELSE_IF_CLAUSE || kind == PHP_ELSE_CLAUSE {
            return false;
        }
        match (after_parentheses, doc.first(child)) {
            (Some(close), Some(first)) => first > close,
            _ => true,
        }
    });
    let Some(body) = body else {
        return;
    };
    if body.kind() == PHP_BLOCK {
        return;
    }
    let (Some(first), Some(last)) = (doc.first(&body), doc.last(&body)) else {
        return;
    };
    if !matches!(doc.source_text(last), ";" | "}") {
        return;
    }
    if kind == PHP_ELSE_CLAUSE && body.kind() == PHP_IF_STATEMENT {
        // The two keywords with nothing but white space between them, and
        // no `if (…):` … `endif`, which `elseif` would not end.
        let alternative = body
            .children()
            .find(|child| child.kind() == PHP_BLOCK)
            .and_then(|block| doc.first(&block))
            .is_some_and(|open| doc.kind(open) == TK_COLON);
        if !alternative
            && first > 0
            && doc.is_keyword(first - 1, "else")
            && doc.is_keyword(first, "if")
        {
            edits.push(Edit::replace(
                doc.toks[first - 1].start,
                doc.toks[first].end,
                "elseif",
            ));
        }
        return;
    }
    // The brace follows the condition, before a comment about it.
    let Some(before) = doc.prev_sig(first) else {
        return;
    };
    edits.push(Edit::insert(doc.toks[before].end, " {"));
    edits.push(Edit::insert(doc.toks[last].end, " }"));
}

/// `new Foo` is `new Foo()`, and `new class()` is `new class`.
fn creation(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    if let Some(class) = node
        .children()
        .find(|child| child.kind() == PHP_ANONYMOUS_CLASS)
    {
        empty_arguments(doc, &class, edits);
        return;
    }
    if node
        .children()
        .any(|child| child.kind() == PHP_ARGUMENT_LIST)
    {
        return;
    }
    let Some(name) = node
        .children()
        .next()
        .filter(|child| child.kind() == PHP_NAME)
    else {
        return;
    };
    let (Some(first), Some(last)) = (doc.first(&name), doc.last(&name)) else {
        return;
    };
    if doc.last(node) != Some(last) {
        return;
    }
    // A class name, or one variable. What the parser does not know
    // (`new readonly class`) is not touched.
    let plain = (first..=last).all(|at| {
        let kind = doc.kind(at);
        kind == TK_IDENTIFIER
            || kind == TK_BACKSLASH
            || ["namespace", "parent", "self", "static"]
                .iter()
                .any(|word| doc.is_keyword(at, word))
    }) || first == last && doc.kind(first) == TK_VARIABLE;
    if !plain {
        return;
    }
    // `new $a->b` creates an object of the class named by `$a->b`, though
    // the parser reads it as `(new $a)->b`: the parentheses follow the
    // properties.
    let mut end = last;
    let mut outer = node.clone();
    while let Some(parent) = outer.parent().filter(|parent| {
        (parent.kind() == PHP_MEMBER_ACCESS || parent.kind() == PHP_SCOPED_ACCESS)
            && parent.children().next().as_ref() == Some(&outer)
    }) {
        let Some(parent_last) = doc.last(&parent) else {
            return;
        };
        end = parent_last;
        outer = parent;
    }
    if (last..=end).any(|at| doc.is_comment(at) || doc.is_opener(at)) {
        return;
    }
    let ends = doc.next_sig(end).is_none_or(|next| {
        matches!(
            doc.source_text(next),
            ";" | ")" | "," | "]" | "}" | ":" | "?"
        ) || doc.kind(next) == TK_OPERATOR
            || doc.parent_kind(next) == PHP_BINARY_EXPRESSION
    });
    if ends {
        edits.push(Edit::insert(doc.toks[end].end, "()"));
    }
}

/// An attribute, and an anonymous class, without arguments has no `()`.
fn empty_arguments(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let Some(list) = node
        .children()
        .find(|child| child.kind() == PHP_ARGUMENT_LIST)
    else {
        return;
    };
    if let Some(open) = doc.first(&list)
        && doc.pair[open] == open + 1
    {
        edits.push(Edit::replace(
            doc.toks[open].start,
            doc.toks[open + 1].end,
            "",
        ));
    }
}

/// Attributes that are split over several lines stand in a block each.
fn attribute_blocks(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let Some(open) = doc.first(node) else {
        return;
    };
    let close = doc.pair[open];
    if close == NONE || node.children().count() < 2 {
        return;
    }
    let split = (open + 1..=close).any(|at| doc.toks[at].nl > 0);
    if !split || (open..close).any(|at| doc.is_comment(at)) {
        return;
    }
    for comma in doc.children(node).filter(|&at| doc.kind(at) == TK_COMMA) {
        let text = if doc.next_sig(comma) == Some(close) {
            ""
        } else {
            "]\n#["
        };
        edits.push(Edit::replace(
            doc.toks[comma].start,
            doc.toks[comma].end,
            text,
        ));
    }
}

/// `array(…)` is `[…]`.
fn short_array(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let Some(keyword) = doc.first(node).filter(|&at| doc.is_keyword(at, "array")) else {
        return;
    };
    let open = keyword + 1;
    if open >= doc.toks.len() || doc.kind(open) != TK_OPEN_PAREN {
        return;
    }
    let close = doc.pair[open];
    edits.push(Edit::replace(
        doc.toks[keyword].start,
        doc.toks[open].end,
        "[",
    ));
    edits.push(Edit::replace(
        doc.toks[close].start,
        doc.toks[close].end,
        "]",
    ));
}

/// Each trait is imported by a `use` of its own.
fn one_trait(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    if doc.children(node).any(|at| doc.kind(at) == TK_OPEN_BRACE) {
        return;
    }
    for comma in doc.children(node).filter(|&at| doc.kind(at) == TK_COMMA) {
        edits.push(Edit::replace(
            doc.toks[comma].start,
            doc.toks[comma].end,
            "; use",
        ));
    }
}

/// An import does not begin with a backslash, and each import outside a
/// group has a `use` of its own.
fn import_names(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    if !doc.children(node).any(|at| doc.kind(at) == TK_OPEN_BRACE)
        && let Some(keyword) = doc.first(node)
    {
        let kind = doc
            .next_sig(keyword)
            .filter(|&at| doc.is_keyword(at, "function") || doc.is_keyword(at, "const"))
            .map_or(String::new(), |at| format!(" {}", doc.source_text(at)));
        for comma in doc.children(node).filter(|&at| doc.kind(at) == TK_COMMA) {
            edits.push(Edit::replace(
                doc.toks[comma].start,
                doc.toks[comma].end,
                format!(";\nuse{kind}"),
            ));
        }
    }
    for slash in doc
        .children(node)
        .filter(|&at| doc.kind(at) == TK_BACKSLASH)
    {
        // After `use`, `function`, `const` or a comma, and not inside a
        // name, whose parts may read like keywords (`App\Enum\Status`).
        let leads = doc.prev_sig(slash).is_some_and(|before| {
            doc.kind(before) == TK_COMMA
                || doc.kind(before) == TK_KEYWORD && doc.toks[before].end < doc.toks[slash].start
        });
        if leads {
            edits.push(Edit::replace(
                doc.toks[slash].start,
                doc.toks[slash].end,
                "",
            ));
        }
    }
}

/// One property is declared per statement.
fn one_property(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let commas: Vec<usize> = doc
        .children(node)
        .filter(|&at| doc.kind(at) == TK_COMMA)
        .collect();
    if commas.is_empty()
        || node.children().any(|child| {
            child.kind() == PHP_ATTRIBUTE_GROUP || child.kind() == PHP_PROPERTY_HOOK_LIST
        })
    {
        return;
    }
    let Some(start) = doc.first(node) else {
        return;
    };
    let Some(variable) = node
        .children()
        .find(|child| child.kind() == PHP_VARIABLE)
        .and_then(|variable| doc.first(&variable))
    else {
        return;
    };
    if (start..variable).any(|at| doc.is_comment(at)) {
        return;
    }
    let prefix = doc.source[doc.toks[start].start..doc.toks[variable].start].trim_end();
    for comma in commas {
        edits.push(Edit::replace(
            doc.toks[comma].start,
            doc.toks[comma].end,
            format!(";\n{prefix}"),
        ));
    }
}

/// Modifiers stand in the order of PER Coding Style. A member has a
/// visibility: `var` and none are `public`.
fn modifiers(doc: &Doc, node: &SyntaxNode, member: bool, edits: &mut Vec<Edit>) {
    let rank = |at: usize| {
        MODIFIERS
            .iter()
            .find(|(word, _)| doc.is_keyword(at, word))
            .map(|(_, rank)| *rank)
    };
    let found: Vec<usize> = doc
        .children(node)
        .skip_while(|&at| rank(at).is_none() && !is_declarator(doc, at))
        .take_while(|&at| rank(at).is_some())
        .collect();
    let Some(&first) = found.first() else {
        // A method or a constant without any modifier.
        if member && let Some(keyword) = doc.children(node).find(|&at| is_declarator(doc, at)) {
            edits.push(Edit::insert(doc.toks[keyword].start, "public "));
        }
        return;
    };
    let last = found[found.len() - 1];
    // Contiguous keywords only, and no `private(set)`.
    if last - first + 1 != found.len()
        || doc
            .next_sig(last)
            .is_some_and(|next| doc.kind(next) == TK_OPEN_PAREN)
    {
        return;
    }
    let mut words: Vec<(u8, String)> = found
        .iter()
        .map(|&at| {
            let word = doc.source_text(at).to_ascii_lowercase();
            let word = if member && word == "var" {
                "public".to_owned()
            } else {
                word
            };
            (rank(at).unwrap_or(0), word)
        })
        .collect();
    if member && !words.iter().any(|(rank, _)| *rank == 1) {
        words.push((1, "public".to_owned()));
    }
    words.sort_by_key(|(rank, _)| *rank);
    let ordered = words
        .into_iter()
        .map(|(_, word)| word)
        .collect::<Vec<_>>()
        .join(" ");
    let source = &doc.source[doc.toks[first].start..doc.toks[last].end];
    if !source.eq_ignore_ascii_case(&ordered) {
        edits.push(Edit::replace(
            doc.toks[first].start,
            doc.toks[last].end,
            ordered,
        ));
    }
}

/// The keyword a member declaration is made with.
fn is_declarator(doc: &Doc, at: usize) -> bool {
    doc.is_keyword(at, "function") || doc.is_keyword(at, "const")
}

/// The kind of an import: of classes, of functions, of constants.
fn import_kind(doc: &Doc, node: &SyntaxNode) -> Option<u8> {
    let first = doc.first(node)?;
    let second = doc.next_sig(first)?;
    Some(if doc.is_keyword(second, "function") {
        1
    } else if doc.is_keyword(second, "const") {
        2
    } else {
        0
    })
}

/// The imports of classes come first, then those of functions, then those
/// of constants.
fn import_order(doc: &Doc, node: &SyntaxNode, edits: &mut Vec<Edit>) {
    let mut run: Vec<SyntaxNode> = Vec::new();
    let children: Vec<SyntaxNode> = node.children().collect();
    for (index, child) in children.iter().enumerate() {
        if child.kind() == PHP_USE_DECLARATION {
            run.push(child.clone());
            if index + 1 < children.len() {
                continue;
            }
        }
        reorder(doc, &run, edits);
        run.clear();
    }
}

fn reorder(doc: &Doc, run: &[SyntaxNode], edits: &mut Vec<Edit>) {
    let mut imports: Vec<(u8, usize, usize)> = Vec::new();
    for node in run {
        let (Some(kind), Some(first), Some(last)) =
            (import_kind(doc, node), doc.first(node), doc.last(node))
        else {
            return;
        };
        imports.push((kind, first, last));
    }
    if imports.is_sorted_by_key(|(kind, _, _)| *kind) {
        return;
    }
    let (start, end) = (imports[0].1, imports[imports.len() - 1].2);
    if (start..=end).any(|at| doc.is_comment(at)) {
        return;
    }
    imports.sort_by_key(|(kind, _, _)| *kind);
    let text = imports
        .iter()
        .map(|(_, first, last)| &doc.source[doc.toks[*first].start..doc.toks[*last].end])
        .collect::<Vec<_>>()
        .join("\n");
    edits.push(Edit::replace(
        doc.toks[start].start,
        doc.toks[end].end,
        text,
    ));
}
