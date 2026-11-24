//! The layout rules of PER Coding Style 2.0: where a line starts, how deep
//! it is indented, and which lists are written one item per line.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::{Kind, SyntaxNode};

use crate::Options;
use crate::tokens::{Blank, Doc, Gap, Indent, NONE};

/// What separates the items between two brackets.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Separator {
    /// Commas; a list over several lines ends with one when `trailing`.
    Comma { trailing: bool },
    /// The commas between the arms of a `match`.
    MatchArm,
    /// The semicolons of a `for`.
    Semicolon,
    /// One expression.
    None,
}

/// The keywords whose parentheses hold a list that may end with a comma.
const TRAILING_COMMA: &[&str] = &[
    "class", "isset", "list", "parent", "self", "static", "unset",
];

pub(crate) fn run(doc: &mut Doc, root: &SyntaxNode, options: Options) {
    doc.reset_layout();
    for node in root.descendants() {
        if node.kind() == PHP_PROGRAM {
            program(doc, &node, options);
        } else {
            layout(doc, &node);
        }
    }
    // What the parser does not read keeps the white space it has.
    for index in 1..doc.toks.len() {
        if doc.raw[index] {
            let adjacent = doc.toks[index - 1].end == doc.toks[index].start;
            doc.gaps[index] = if adjacent { Gap::Glue } else { Gap::Space };
            doc.flat[index] = false;
            doc.toks[index].comma = false;
            doc.toks[index].skip = false;
        } else if doc.raw[index - 1]
            && doc.toks[index].nl == 0
            && matches!(doc.gaps[index], Gap::Break(..))
        {
            // The statement goes on where the parser saw it end.
            doc.gaps[index] = Gap::Auto;
        }
    }
    // The line breaks that shorten a line.
    for index in 0..doc.toks.len() {
        if doc.breaks[index] && !matches!(doc.gaps[index], Gap::Break(..)) {
            doc.gaps[index] = Gap::Break(Blank::None, Indent::Continue);
        }
    }
}

fn layout(doc: &mut Doc, node: &SyntaxNode) {
    let kind = node.kind();
    if kind == PHP_BLOCK {
        block(doc, node);
    } else if is_class_like(kind) {
        class_like(doc, node);
    } else if kind == PHP_ANONYMOUS_CLASS {
        anonymous_class(doc, node);
    } else if kind == PHP_METHOD_DECLARATION || kind == PHP_FUNCTION_DECLARATION {
        function(doc, node);
    } else if kind == PHP_CLOSURE {
        closure(doc, node);
    } else if kind == PHP_ARROW_FUNCTION {
        arrow_function(doc, node);
    } else if kind == PHP_PARAMETER_LIST {
        bracketed(doc, node, Separator::Comma { trailing: true });
    } else if kind == PHP_ARGUMENT_LIST {
        arguments(doc, node);
    } else if kind == PHP_ARRAY {
        bracketed(doc, node, Separator::Comma { trailing: true });
    } else if kind == PHP_ATTRIBUTE_GROUP {
        attribute_group(doc, node);
    } else if kind == PHP_MATCH_EXPRESSION {
        match_expression(doc, node);
    } else if kind == PHP_SWITCH_STATEMENT {
        switch(doc, node);
    } else if kind == PHP_CASE_CLAUSE {
        case(doc, node);
    } else if kind == PHP_PARENTHESIZED {
        parenthesized(doc, node);
    } else if kind == PHP_FOR_STATEMENT {
        bracketed(doc, node, Separator::Semicolon);
    } else if kind == PHP_FOREACH_STATEMENT || kind == PHP_CATCH_CLAUSE {
        bracketed(doc, node, Separator::None);
    } else if kind == PHP_USE_DECLARATION {
        use_declaration(doc, node);
    } else if kind == PHP_TRAIT_USE_DECLARATION {
        trait_use(doc, node);
    } else if kind == PHP_PROPERTY_HOOK_LIST {
        property_hooks(doc, node);
    } else if kind == PHP_NAMESPACE {
        namespace(doc, node);
    }
    if [
        PHP_IF_STATEMENT,
        PHP_WHILE_STATEMENT,
        PHP_FOR_STATEMENT,
        PHP_FOREACH_STATEMENT,
    ]
    .contains(&kind)
    {
        alternative_syntax(doc, node);
    }
    if kind == PHP_MEMBER_CALL || kind == PHP_MEMBER_ACCESS {
        method_chain(doc, node);
    }
}

/// The object a member is taken of, through calls and array accesses.
fn chained_object(node: &SyntaxNode) -> Option<SyntaxNode> {
    let object = node.children().next()?;
    let kind = object.kind();
    if kind == PHP_MEMBER_CALL || kind == PHP_MEMBER_ACCESS {
        Some(object)
    } else if kind == PHP_ARRAY_ACCESS {
        chained_object(&object)
    } else {
        None
    }
}

fn member_operator(doc: &Doc, node: &SyntaxNode) -> Option<usize> {
    doc.children(node).find(|&at| {
        let kind = doc.kind(at);
        kind == TK_OBJECT_OPERATOR || kind == TK_NULLSAFE_OBJECT_OPERATOR
    })
}

/// A method chain over several lines starts its first method on a new
/// line too.
fn method_chain(doc: &mut Doc, node: &SyntaxNode) {
    // The whole chain, seen from its last member.
    let continues = node.parent().is_some_and(|parent| {
        let kind = parent.kind();
        (kind == PHP_MEMBER_CALL || kind == PHP_MEMBER_ACCESS || kind == PHP_ARRAY_ACCESS)
            && parent.children().next().as_ref() == Some(node)
    });
    if continues {
        return;
    }
    let mut links = Vec::new();
    let mut link = Some(node.clone());
    while let Some(member) = link {
        if let Some(operator) = member_operator(doc, &member) {
            links.push((operator, member.kind() == PHP_MEMBER_CALL));
        }
        link = chained_object(&member);
    }
    if !links
        .iter()
        .any(|(operator, _)| doc.toks[*operator].nl > 0 || doc.breaks[*operator])
    {
        return;
    }
    // The links were gathered from the last to the first.
    if let Some((operator, _)) = links.iter().rev().find(|(_, call)| *call) {
        doc.gaps[*operator] = Gap::Break(Blank::None, Indent::Continue);
    }
}

/// `if (…):` … `endif;`: the keywords that continue and end the statement
/// stand below its start.
fn alternative_syntax(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    let colon = node
        .children()
        .find(|child| child.kind() == PHP_BLOCK)
        .and_then(|body| doc.first(&body))
        .is_some_and(|open| doc.kind(open) == TK_COLON);
    if !colon {
        return;
    }
    for clause in node.children() {
        let kind = clause.kind();
        if (kind == PHP_ELSE_IF_CLAUSE || kind == PHP_ELSE_CLAUSE)
            && let Some(first) = doc.first(&clause)
        {
            doc.gaps[first] = Gap::Break(Blank::None, Indent::Like(start));
        }
    }
    let end = doc.last(node).and_then(|last| doc.prev_sig(last));
    if let Some(end) = end
        && ["endif", "endwhile", "endfor", "endforeach"]
            .iter()
            .any(|word| doc.is_keyword(end, word))
    {
        doc.gaps[end] = Gap::Break(Blank::None, Indent::Like(start));
    }
}

fn is_class_like(kind: Kind) -> bool {
    kind == PHP_CLASS_DECLARATION
        || kind == PHP_INTERFACE_DECLARATION
        || kind == PHP_TRAIT_DECLARATION
        || kind == PHP_ENUM_DECLARATION
}

/// The first bracket among the tokens of `node` itself.
fn opener(doc: &Doc, node: &SyntaxNode) -> Option<usize> {
    doc.children(node).find(|&at| doc.is_opener(at))
}

// --- statements ---------------------------------------------------------

/// The kind of a statement in the header of a file.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Header {
    Declare,
    Namespace,
    Use(u8),
    Code,
}

fn header(doc: &Doc, node: &SyntaxNode) -> Header {
    let kind = node.kind();
    if kind == PHP_NAMESPACE {
        return Header::Namespace;
    }
    let Some(first) = doc.first(node) else {
        return Header::Code;
    };
    if kind == PHP_USE_DECLARATION {
        let import = doc.next_sig(first).map_or(0, |second| {
            if doc.is_keyword(second, "function") {
                1
            } else if doc.is_keyword(second, "const") {
                2
            } else {
                0
            }
        });
        return Header::Use(import);
    }
    if kind == PHP_EXPRESSION_STATEMENT && doc.is_keyword(first, "declare") {
        return Header::Declare;
    }
    Header::Code
}

/// Whether the statement is complete, so that the next one starts a line.
fn ends_statement(doc: &Doc, node: &SyntaxNode) -> bool {
    doc.last(node)
        .is_some_and(|last| matches!(doc.source_text(last), ";" | "}" | ":"))
}

/// The header blocks of a file are set apart by one empty line each and
/// hold none.
fn program(doc: &mut Doc, node: &SyntaxNode, options: Options) {
    let mut previous: Option<(Header, bool)> = None;
    for child in node.children() {
        let Some(first) = doc.first(&child) else {
            continue;
        };
        // An empty statement stays with what it follows, but not with a
        // statement that has its own semicolon.
        if doc.kind(first) == TK_SEMICOLON && previous.is_some() {
            if follows_semicolon(doc, first) {
                doc.gaps[first] = Gap::Break(Blank::None, Indent::Zero);
            }
            continue;
        }
        let now = header(doc, &child);
        match previous {
            None if options.declare_on_opening_tag
                && now == Header::Declare
                && doc.toks[first].nl == 0 =>
            {
                doc.gaps[first] = Gap::Space;
            }
            None => {
                doc.gaps[first] = Gap::Break(Blank::One, Indent::Zero);
                doc.after_comment[first] = now != Header::Code;
            }
            Some((_, false)) => {}
            Some((before, true)) => {
                let blank = if before == now && !matches!(now, Header::Code | Header::Namespace) {
                    Blank::None
                } else if before == Header::Code && now == Header::Code {
                    Blank::Keep
                } else {
                    Blank::One
                };
                doc.gaps[first] = Gap::Break(blank, Indent::Zero);
                // A docblock of the file stands apart from the header.
                doc.after_comment[first] = matches!(now, Header::Declare | Header::Namespace);
            }
        }
        previous = Some((now, ends_statement(doc, &child)));
    }
}

/// Whether the token before this one, comments aside, is a semicolon.
fn follows_semicolon(doc: &Doc, index: usize) -> bool {
    doc.prev_sig(index)
        .is_some_and(|before| doc.kind(before) == TK_SEMICOLON)
}

/// Every statement among `children` starts a line at `indent`.
fn statements(
    doc: &mut Doc,
    children: impl Iterator<Item = SyntaxNode>,
    indent: Indent,
    blank_after: impl Fn(&SyntaxNode, &SyntaxNode) -> Blank,
) {
    let mut previous: Option<(SyntaxNode, bool)> = None;
    for child in children {
        let Some(first) = doc.first(&child) else {
            continue;
        };
        // An empty statement stays with what it follows, but not with a
        // statement that has its own semicolon.
        if doc.kind(first) == TK_SEMICOLON && previous.is_some() {
            if follows_semicolon(doc, first) {
                doc.gaps[first] = Gap::Break(Blank::None, indent);
            }
            continue;
        }
        match &previous {
            None => doc.gaps[first] = Gap::Break(Blank::None, indent),
            Some((_, false)) => {}
            Some((before, true)) => {
                doc.gaps[first] = Gap::Break(blank_after(before, &child), indent);
            }
        }
        let complete = ends_statement(doc, &child);
        previous = Some((child, complete));
    }
}

fn block(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = doc.children(node).next() else {
        return;
    };
    // The body of a control structure is indented once from the line the
    // structure starts on, wherever its condition ends.
    let anchor = node
        .parent()
        .filter(|owner| {
            [
                PHP_IF_STATEMENT,
                PHP_ELSE_IF_CLAUSE,
                PHP_ELSE_CLAUSE,
                PHP_WHILE_STATEMENT,
                PHP_FOR_STATEMENT,
                PHP_FOREACH_STATEMENT,
                PHP_DO_WHILE_STATEMENT,
                PHP_TRY_STATEMENT,
                PHP_CATCH_CLAUSE,
                PHP_FINALLY_CLAUSE,
            ]
            .contains(&owner.kind())
        })
        .and_then(|owner| doc.first(&owner))
        .unwrap_or(open);
    let close = doc.pair[open];
    if close != NONE {
        let empty = close == open + 1;
        let owner = node.parent().map(|parent| parent.kind());
        let abbreviated = empty
            && owner.is_some_and(|kind| {
                kind == PHP_FUNCTION_DECLARATION
                    || kind == PHP_METHOD_DECLARATION
                    || kind == PHP_CLOSURE
            });
        doc.gaps[close] = if abbreviated {
            Gap::Glue
        } else {
            Gap::Break(Blank::None, Indent::Like(anchor))
        };
    }
    statements(doc, node.children(), Indent::Inside(anchor), |_, _| {
        Blank::Keep
    });
}

fn namespace(doc: &mut Doc, node: &SyntaxNode) {
    if let Some(open) = opener(doc, node) {
        doc.gaps[open] = Gap::Space;
        let close = doc.pair[open];
        doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
        statements(
            doc,
            node.children().skip(1),
            Indent::Inside(open),
            |_, _| Blank::Keep,
        );
    }
}

fn switch(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = opener(doc, node) else {
        return;
    };
    doc.gaps[open] = Gap::Space;
    let close = doc.pair[open];
    doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
    let mut first = true;
    for clause in node
        .children()
        .filter(|child| child.kind() == PHP_CASE_CLAUSE)
    {
        if let Some(start) = doc.first(&clause) {
            let blank = if first { Blank::None } else { Blank::Keep };
            doc.gaps[start] = Gap::Break(blank, Indent::Inside(open));
            first = false;
        }
    }
}

fn case(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    let Some(colon) = doc
        .children(node)
        .find(|&at| matches!(doc.source_text(at), ":" | ";"))
    else {
        return;
    };
    let after = doc.toks[colon].start;
    let body = node
        .children()
        .filter(|child| usize::from(child.text_range().start()) > after);
    statements(doc, body, Indent::Inside(start), |_, _| Blank::Keep);
}

// --- classes ------------------------------------------------------------

fn class_like(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    inheritance_lists(doc, node, start);
    let Some(body) = node.children().find(|child| child.kind() == PHP_CLASS_BODY) else {
        return;
    };
    let Some(open) = opener(doc, &body) else {
        return;
    };
    let close = doc.pair[open];
    if close == open + 1 {
        // A class without declarations is abbreviated as `{}`.
        doc.gaps[open] = Gap::Space;
        doc.gaps[close] = Gap::Glue;
        return;
    }
    doc.gaps[open] = Gap::Break(Blank::None, Indent::Like(start));
    class_body(doc, &body, open);
}

fn class_body(doc: &mut Doc, body: &SyntaxNode, open: usize) {
    let close = doc.pair[open];
    doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
    statements(doc, body.children(), Indent::Inside(open), |before, now| {
        // The trait imports are set apart from what follows them.
        if before.kind() == PHP_TRAIT_USE_DECLARATION && now.kind() != PHP_TRAIT_USE_DECLARATION {
            Blank::One
        } else {
            Blank::Keep
        }
    });
}

/// `extends` and `implements` lists over several lines hold one name per
/// line, indented once. Returns whether a list is split.
fn inheritance_lists(doc: &mut Doc, node: &SyntaxNode, anchor: usize) -> bool {
    let mut split = false;
    for clause in node.children() {
        let kind = clause.kind();
        if kind != PHP_IMPLEMENTS_CLAUSE && kind != PHP_EXTENDS_CLAUSE {
            continue;
        }
        let names: Vec<usize> = clause
            .children()
            .filter_map(|name| doc.first(&name))
            .collect();
        // One name stays on the line of the class, unless the line is
        // too long with it.
        let forced = names.iter().any(|&name| doc.breaks[name]);
        let written = names.len() >= 2 && names.iter().any(|&name| doc.toks[name].nl > 0);
        if !forced && !written {
            continue;
        }
        split = true;
        for name in names {
            doc.gaps[name] = Gap::Break(Blank::None, Indent::Inside(anchor));
        }
    }
    split
}

fn anonymous_class(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    // The line of `new`, which the class is written on.
    let anchor = doc
        .prev_sig(start)
        .filter(|&at| doc.is_keyword(at, "new"))
        .unwrap_or(start);
    let split = inheritance_lists(doc, node, anchor);
    let Some(body) = node.children().find(|child| child.kind() == PHP_CLASS_BODY) else {
        return;
    };
    let Some(open) = opener(doc, &body) else {
        return;
    };
    doc.gaps[open] = if split {
        Gap::Break(Blank::None, Indent::Like(anchor))
    } else {
        Gap::Space
    };
    if doc.pair[open] == open + 1 {
        doc.gaps[doc.pair[open]] = Gap::Glue;
        return;
    }
    class_body(doc, &body, open);
}

fn trait_use(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = opener(doc, node) else {
        return;
    };
    let close = doc.pair[open];
    doc.gaps[open] = Gap::Space;
    if close == open + 1 {
        doc.gaps[close] = Gap::Glue;
        return;
    }
    doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
    let mut starts = true;
    for at in open + 1..close {
        if doc.is_comment(at) {
            continue;
        }
        if starts {
            doc.gaps[at] = Gap::Break(Blank::None, Indent::Inside(open));
        }
        starts = doc.source_text(at) == ";";
    }
}

fn property_hooks(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = opener(doc, node) else {
        return;
    };
    let close = doc.pair[open];
    doc.gaps[open] = Gap::Space;
    doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
    statements(doc, node.children(), Indent::Inside(open), |_, _| {
        Blank::Keep
    });
}

// --- functions ----------------------------------------------------------

fn function(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    let Some(body) = node.children().find(|child| child.kind() == PHP_BLOCK) else {
        return;
    };
    let Some(open) = doc.first(&body) else {
        return;
    };
    let split = node
        .children()
        .find(|child| child.kind() == PHP_PARAMETER_LIST)
        .and_then(|list| opener(doc, &list))
        .is_some_and(|list| is_split(doc, list));
    let empty = doc.pair[open] == open + 1;
    // The brace stands on its own line, but with the closing parenthesis
    // of a parameter list over several lines and in an empty body.
    doc.gaps[open] = if split || empty {
        Gap::Space
    } else {
        Gap::Break(Blank::None, Indent::Like(start))
    };
}

fn closure(doc: &mut Doc, node: &SyntaxNode) {
    if let Some(body) = node.children().find(|child| child.kind() == PHP_BLOCK)
        && let Some(open) = doc.first(&body)
    {
        doc.gaps[open] = Gap::Space;
    }
    // The variables after `use`.
    if let Some(keyword) = doc.child(node, "use")
        && let Some(open) = doc.next_sig(keyword)
        && doc.is_opener(open)
    {
        list(doc, open, Separator::Comma { trailing: true });
    }
}

/// The expression of a short closure may go to the next line, which then
/// starts with the `=>`.
fn arrow_function(doc: &mut Doc, node: &SyntaxNode) {
    let Some(start) = doc.first(node) else {
        return;
    };
    let Some(arrow) = doc.children(node).find(|&at| doc.kind(at) == TK_ARROW) else {
        return;
    };
    let Some(value) = doc.next_sig(arrow) else {
        return;
    };
    if doc.toks[arrow].nl > 0 || doc.toks[value].nl > 0 {
        doc.gaps[arrow] = Gap::Break(Blank::None, Indent::Inside(start));
        doc.gaps[value] = Gap::Space;
    }
}

fn attribute_group(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = doc.first(node) else {
        return;
    };
    let close = doc.pair[open];
    let Some(next) = doc.next_sig(close) else {
        return;
    };
    let Some(owner) = node.parent() else {
        return;
    };
    let Some(start) = doc.first(&owner) else {
        return;
    };
    let kind = owner.kind();
    let own_line = if kind == PHP_PARAMETER {
        // Inline in a parameter list on one line, above the parameter in
        // one over several lines.
        matches!(doc.gaps[start], Gap::Break(..))
    } else {
        is_class_like(kind)
            || [
                PHP_METHOD_DECLARATION,
                PHP_FUNCTION_DECLARATION,
                PHP_PROPERTY_DECLARATION,
                PHP_CLASS_CONST_DECLARATION,
                PHP_CONST_DECLARATION,
                PHP_ENUM_CASE_DECLARATION,
            ]
            .contains(&kind)
    };
    doc.gaps[next] = if own_line {
        Gap::Break(Blank::None, Indent::Like(start))
    } else {
        Gap::Space
    };
}

// --- lists --------------------------------------------------------------

fn bracketed(doc: &mut Doc, node: &SyntaxNode, separator: Separator) {
    if let Some(open) = opener(doc, node) {
        list(doc, open, separator);
    }
}

fn arguments(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = opener(doc, node) else {
        return;
    };
    let close = doc.pair[open];
    // `foo(...)` and `foo(?, ...)` end in the placeholder for every other
    // argument, which nothing follows.
    let reference = doc
        .prev_sig(close)
        .map(|last| match doc.kind(last) {
            TK_COMMA => doc.prev_sig(last).unwrap_or(last),
            _ => last,
        })
        .is_some_and(|last| doc.kind(last) == TK_ELLIPSIS);
    // The parentheses after a keyword are a call only for the constructs
    // known to take a list (`isset`, `unset`, `list`, `new static`) and for a
    // method of that name: `include(…)` and `new (…)` take one expression.
    let construct = doc.prev_sig(open).is_some_and(|callee| {
        doc.kind(callee) == TK_KEYWORD
            && !TRAILING_COMMA
                .iter()
                .any(|word| doc.is_keyword(callee, word))
            && !doc
                .prev_sig(callee)
                .is_some_and(|before| matches!(doc.source_text(before), "->" | "?->" | "::"))
    });
    list(
        doc,
        open,
        Separator::Comma {
            trailing: !reference && !construct,
        },
    );
}

fn match_expression(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = doc.children(node).find(|&at| doc.kind(at) == TK_OPEN_BRACE) else {
        return;
    };
    doc.gaps[open] = Gap::Space;
    list(doc, open, Separator::MatchArm);
    // The conditions of one arm over several lines stand below each other.
    for arm in node
        .children()
        .filter(|child| child.kind() == PHP_MATCH_ARM)
    {
        let Some(start) = doc.first(&arm) else {
            continue;
        };
        let commas: Vec<usize> = doc
            .children(&arm)
            .filter(|&at| doc.kind(at) == TK_COMMA)
            .collect();
        for comma in commas {
            if let Some(condition) = doc.next_sig(comma)
                && (doc.toks[condition].nl > 0 || doc.breaks[condition])
            {
                doc.gaps[condition] = Gap::Break(Blank::None, Indent::Like(start));
            }
        }
    }
}

fn use_declaration(doc: &mut Doc, node: &SyntaxNode) {
    if let Some(open) = opener(doc, node) {
        list(doc, open, Separator::Comma { trailing: true });
    }
}

/// The parentheses of a condition are split when the condition is; other
/// parentheses when a line break follows the opening one.
fn parenthesized(doc: &mut Doc, node: &SyntaxNode) {
    let Some(open) = opener(doc, node) else {
        return;
    };
    let condition = node.parent().is_some_and(|parent| {
        [
            PHP_IF_STATEMENT,
            PHP_ELSE_IF_CLAUSE,
            PHP_WHILE_STATEMENT,
            PHP_DO_WHILE_STATEMENT,
            PHP_SWITCH_STATEMENT,
            PHP_MATCH_EXPRESSION,
        ]
        .contains(&parent.kind())
    });
    if condition {
        // A condition is split when the expression is, also where the
        // line break stands inside parentheses of the expression.
        let close = doc.pair[open];
        let nested = (open + 1..close).any(|at| {
            doc.toks[at].nl > 0
                && doc.kind(at) != TK_CLOSE_PAREN
                && enclosing_parenthesis(doc, at, open)
        });
        if nested {
            doc.split[open] = true;
        }
        list(doc, open, Separator::None);
        boolean_operators(doc, node);
        return;
    }
    let close = doc.pair[open];
    let Some(first) = doc.next_sig(open) else {
        return;
    };
    if first != close && doc.toks[first].nl > 0 {
        doc.gaps[first] = Gap::Break(Blank::None, Indent::Inside(open));
        doc.gaps[close] = Gap::Break(Blank::None, Indent::Like(open));
    }
}

/// The boolean operators between the conditions of a control structure
/// stand at the beginning of their lines or at the end, not both: a mix
/// is moved to the beginning.
fn boolean_operators(doc: &mut Doc, node: &SyntaxNode) {
    let Some(mut top) = node
        .children()
        .find(|child| child.kind() == PHP_BINARY_EXPRESSION)
    else {
        return;
    };
    let mut operators = Vec::new();
    loop {
        if let Some(operator) = doc.children(&top).next()
            && matches!(
                doc.source_text(operator).to_ascii_lowercase().as_str(),
                "&&" | "||" | "and" | "or" | "xor"
            )
            && let Some(operand) = doc.next_sig(operator)
        {
            operators.push((operator, operand));
        }
        match top
            .children()
            .next()
            .filter(|left| left.kind() == PHP_BINARY_EXPRESSION)
        {
            Some(left) => top = left,
            None => break,
        }
    }
    let leading = operators
        .iter()
        .any(|(operator, _)| doc.toks[*operator].nl > 0 || doc.breaks[*operator]);
    let trailing: Vec<(usize, usize)> = operators
        .iter()
        .copied()
        .filter(|(operator, operand)| doc.toks[*operator].nl == 0 && doc.toks[*operand].nl > 0)
        .collect();
    if !leading {
        return;
    }
    for (operator, operand) in trailing {
        // A comment after the operator keeps it where it is.
        if operand != operator + 1 {
            continue;
        }
        doc.gaps[operator] = Gap::Break(Blank::None, Indent::Continue);
        doc.gaps[operand] = Gap::Space;
    }
}

/// Whether every bracket around the token inside `open` is a parenthesis
/// around an expression: not a list, an array or a block.
fn enclosing_parenthesis(doc: &Doc, at: usize, open: usize) -> bool {
    let mut index = at;
    while index > open + 1 {
        index -= 1;
        if doc.is_closer(index) {
            index = doc.pair[index];
        } else if doc.is_opener(index) && doc.parent_kind(index) != PHP_PARENTHESIZED {
            return false;
        }
    }
    true
}

fn is_separator(doc: &Doc, at: usize, separator: Separator) -> bool {
    match separator {
        Separator::Comma { .. } => doc.kind(at) == TK_COMMA,
        Separator::MatchArm => {
            doc.kind(at) == TK_COMMA && doc.parent_kind(at) == PHP_MATCH_EXPRESSION
        }
        Separator::Semicolon => doc.kind(at) == TK_SEMICOLON,
        Separator::None => false,
    }
}

/// The tokens between a bracket and its partner that are not inside
/// further brackets.
fn level(doc: &Doc, open: usize) -> Vec<usize> {
    let close = doc.pair[open];
    let mut tokens = Vec::new();
    let mut at = open + 1;
    while at < close {
        tokens.push(at);
        at = if doc.is_opener(at) {
            doc.pair[at] + 1
        } else {
            at + 1
        };
    }
    tokens
}

/// Whether what the bracket encloses is split over several lines: a line
/// break stands between two of its items or parts of them. Line breaks
/// inside further brackets do not count, and neither does one before the
/// closing bracket alone.
pub(crate) fn is_split(doc: &Doc, open: usize) -> bool {
    doc.split[open]
        || level(doc, open).into_iter().any(|at| {
            doc.toks[at].nl > 0
                || doc.is_comment(at) && (doc.kind(at) == TK_LINE_COMMENT || doc.stands_alone(at))
        })
}

/// Lays out what stands between a bracket and its partner: on one line
/// without spaces inside the brackets, or one item per line.
fn list(doc: &mut Doc, open: usize, separator: Separator) {
    let close = doc.pair[open];
    if close == NONE {
        return;
    }
    if close == open + 1 {
        doc.gaps[close] = Gap::Glue;
        return;
    }
    // Separators alone, as in `for (;;)`, are not split.
    let empty = level(doc, open)
        .into_iter()
        .all(|at| is_separator(doc, at, separator));
    // The arms of a `match` always stand one per line.
    let split = !empty && (separator == Separator::MatchArm || is_split(doc, open));
    let inside = Gap::Glue;
    let items: Vec<usize> = level(doc, open)
        .into_iter()
        .filter(|&at| !doc.is_comment(at))
        .collect();
    // A list of comments only has nothing to put on lines of their own.
    doc.flat[open] = !split && items.iter().any(|&at| !is_separator(doc, at, separator));
    // Arguments and parameters follow each other without empty lines, but
    // where attributes set them apart.
    let tight = [PHP_ARGUMENT_LIST, PHP_PARAMETER_LIST].contains(&doc.parent_kind(open))
        && !items.iter().any(|&at| doc.kind(at) == TK_ATTRIBUTE_OPEN);
    let mut starts = true;
    let mut first = true;
    for &at in &items {
        if is_separator(doc, at, separator) {
            // A comma after a comma skips an element: `[$a, , $b]`.
            let skips = matches!(separator, Separator::Comma { .. });
            doc.gaps[at] = if starts && !first && skips {
                Gap::Space
            } else {
                Gap::Glue
            };
            starts = true;
            first = false;
            continue;
        }
        if starts {
            doc.gaps[at] = match (split, first) {
                (true, true) => Gap::Break(Blank::None, Indent::Inside(open)),
                (true, false) if tight => Gap::Break(Blank::None, Indent::Inside(open)),
                (true, false) => Gap::Break(Blank::Keep, Indent::Inside(open)),
                (false, true) => inside,
                (false, false) => Gap::Space,
            };
            starts = false;
            first = false;
        }
    }
    doc.gaps[close] = if split {
        Gap::Break(Blank::None, Indent::Like(open))
    } else {
        inside
    };
    if separator == Separator::None {
        return;
    }
    let Some(last) = doc.prev_sig(close).filter(|&last| last != open) else {
        return;
    };
    let ends_with_comma = doc.kind(last) == TK_COMMA && is_separator(doc, last, separator);
    match separator {
        Separator::Comma { trailing: true } | Separator::MatchArm if split => {
            if !ends_with_comma {
                doc.toks[last].comma = true;
            }
        }
        Separator::Comma { .. } | Separator::MatchArm if !split && ends_with_comma => {
            // A list on one line has no trailing comma. A comma after
            // nothing skips an element and stays.
            let before = doc.prev_sig(last);
            if before.is_some_and(|before| before != open && doc.kind(before) != TK_COMMA) {
                doc.toks[last].skip = true;
            }
        }
        _ => {}
    }
}
