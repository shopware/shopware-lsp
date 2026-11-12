//! PHP grammar. Tree shapes and error messages are a contract with persisted
//! caches and CST dumps, so changes to them must be deliberate.

use std::sync::Arc;

use swlsp_syntax::{CompletedMarker, Config, ErrorBuilder, Kind, Parse, Parser, Token};

use crate::syntax::*;
use crate::{eq_ignore_case_keyword, lexer};

type P<'a> = Parser<'a>;

/// A set of token kinds stored as a bit mask relative to the PHP base.
#[derive(Clone, Copy, PartialEq, Eq)]
struct Stop(u64);

impl Stop {
    const NONE: Stop = Stop(0);

    const fn of(kinds: &[Kind]) -> Stop {
        let mut mask = 0u64;
        let mut index = 0;
        while index < kinds.len() {
            mask |= 1 << (kinds[index].0 - KIND_BASE);
            index += 1;
        }
        Stop(mask)
    }

    #[inline]
    fn contains(self, kind: Kind) -> bool {
        let offset = kind.0.wrapping_sub(KIND_BASE);
        offset < 64 && self.0 & (1 << offset) != 0
    }
}

const STOP_COMMA_CLOSE_PAREN: Stop = Stop::of(&[TK_COMMA, TK_CLOSE_PAREN]);
const STOP_COMMA_SEMICOLON: Stop = Stop::of(&[TK_COMMA, TK_SEMICOLON]);
const STOP_SEMICOLON_CLOSE_BRACE: Stop = Stop::of(&[TK_SEMICOLON, TK_CLOSE_BRACE]);
const STOP_COMMA_CLOSE_BRACE: Stop = Stop::of(&[TK_COMMA, TK_CLOSE_BRACE]);

const GENERAL_RECOVERY: &[Kind] = &[TK_SEMICOLON, TK_CLOSE_BRACE];

pub(crate) fn parse(source: &str) -> Parse {
    parse_shared(Arc::from(source))
}

/// Like [`parse`], but the tree shares `shared` instead of copying it.
pub(crate) fn parse_shared(shared: Arc<str>) -> Parse {
    let source: &str = &shared;
    let tokens = lexer::lex(source);
    let mut p = Parser::new(
        source,
        tokens,
        Config {
            general_recovery_set: GENERAL_RECOVERY,
            error_kind: ERROR,
        },
    );
    let root = p.start();
    while !p.at_end() {
        let position = p.get_pos();
        parse_statement(&mut p, false);
        if p.get_pos() == position {
            p.add_error(ErrorBuilder::new("PHP statement"));
            p.bump();
        }
    }
    p.complete(root, PHP_PROGRAM);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

// -- token helpers ------------------------------------------------------------

#[inline]
fn is_trivia(kind: Kind) -> bool {
    (TK_WHITESPACE.0..=TK_BLOCK_COMMENT.0).contains(&kind.0)
}

/// Raw tokens starting at the current non-trivia token.
#[inline]
fn upcoming<'p, 'a>(p: &'p P<'a>) -> &'p [Token<'a>] {
    p.peek();
    &p.tokens()[p.get_pos()..]
}

/// Index of the first non-trivia token at or after `index`.
#[inline]
fn skip_trivia(tokens: &[Token<'_>], mut index: usize) -> Option<usize> {
    while index < tokens.len() {
        if !is_trivia(tokens[index].kind) {
            return Some(index);
        }
        index += 1;
    }
    None
}

/// The `ordinal`-th non-trivia token from the current one.
fn nth<'a>(p: &P<'a>, ordinal: usize) -> Option<Token<'a>> {
    upcoming(p)
        .iter()
        .filter(|token| !is_trivia(token.kind))
        .nth(ordinal)
        .copied()
}

fn next_kind(p: &P<'_>) -> Kind {
    nth(p, 1).map_or(Kind::NONE, |token| token.kind)
}

fn next_text_is(p: &P<'_>, word: &str) -> bool {
    nth(p, 1).is_some_and(|token| eq_ignore_case_keyword(token.text, word))
}

/// Skips an attribute group starting at `index` (a `#[` token); returns the
/// index of its closing bracket.
fn skip_attribute(tokens: &[Token<'_>], mut index: usize) -> Option<usize> {
    let mut depth = 1;
    while depth > 0 {
        index = skip_trivia(tokens, index + 1)?;
        match tokens[index].kind {
            TK_OPEN_BRACKET | TK_ATTRIBUTE_OPEN => depth += 1,
            TK_CLOSE_BRACKET => depth -= 1,
            _ => {}
        }
    }
    Some(index)
}

/// Lowercased text of the current keyword token, or "" when the current
/// token is not a keyword. Keyword tokens are ASCII and at most 12 bytes.
fn current_keyword<'b>(p: &P<'_>, buffer: &'b mut [u8; 12]) -> &'b str {
    match p.peek_token() {
        Some(token) if token.kind == TK_KEYWORD && token.text.len() <= 12 => {
            let bytes = token.text.as_bytes();
            for (slot, byte) in buffer.iter_mut().zip(bytes) {
                *slot = byte.to_ascii_lowercase();
            }
            std::str::from_utf8(&buffer[..bytes.len()]).unwrap_or("")
        }
        _ => "",
    }
}

fn at_keyword(p: &P<'_>, words: &[&str]) -> bool {
    match p.peek_token() {
        Some(token) if token.kind == TK_KEYWORD => words
            .iter()
            .any(|word| token.text.eq_ignore_ascii_case(word)),
        _ => false,
    }
}

#[inline]
fn at_kw(p: &P<'_>, word: &str) -> bool {
    at_keyword(p, &[word])
}

fn at_word(p: &P<'_>, words: &[&str]) -> bool {
    match p.peek_token() {
        Some(token) if token.kind == TK_KEYWORD || token.kind == TK_IDENTIFIER => words
            .iter()
            .any(|word| eq_ignore_case_keyword(token.text, word)),
        _ => false,
    }
}

fn is_modifier(text: &str) -> bool {
    [
        "abstract",
        "final",
        "public",
        "protected",
        "private",
        "static",
        "readonly",
        "var",
    ]
    .iter()
    .any(|word| text.eq_ignore_ascii_case(word))
}

fn is_visibility(text: &str) -> bool {
    ["public", "protected", "private"]
        .iter()
        .any(|word| eq_ignore_case_keyword(text, word))
}

#[inline]
fn is_name_start(p: &P<'_>) -> bool {
    matches!(
        p.peek(),
        Some(TK_IDENTIFIER | TK_KEYWORD | TK_BACKSLASH | TK_VARIABLE)
    )
}

#[inline]
fn at_stop(p: &P<'_>, stop: Stop) -> bool {
    match p.peek() {
        None => true,
        Some(kind) => stop.contains(kind),
    }
}

#[inline]
fn bump_if(p: &mut P<'_>, kind: Kind) -> bool {
    if p.at(kind) {
        p.bump();
        true
    } else {
        false
    }
}

// -- statements -----------------------------------------------------------------

fn parse_statement(p: &mut P<'_>, in_class: bool) {
    let mut buffer = [0u8; 12];
    let keyword = current_keyword(p, &mut buffer);
    if p.at(TK_OPEN_TAG) || p.at(TK_CLOSE_TAG) {
        p.bump();
    } else if keyword == "namespace" {
        parse_namespace(p);
    } else if keyword == "use" && !in_class {
        parse_use(p);
    } else if starts_class_declaration(p) {
        parse_class_declaration(p);
    } else if in_class && starts_method_declaration(p) {
        parse_function_like(p, PHP_METHOD_DECLARATION, true);
    } else if !in_class && starts_function_declaration(p) {
        parse_function_like(p, PHP_FUNCTION_DECLARATION, false);
    } else if in_class && keyword == "use" {
        parse_trait_use(p);
    } else if in_class && keyword == "case" {
        parse_enum_case(p);
    } else if keyword == "const" || in_class && starts_class_const_declaration(p) {
        parse_const(p, in_class);
    } else if in_class && starts_property_declaration(p) {
        parse_property(p);
    } else {
        match keyword {
            "return" => parse_keyword_expression_statement(p, PHP_RETURN_STATEMENT),
            "if" => parse_if(p),
            "switch" => parse_switch(p),
            "while" => parse_while(p),
            "do" => parse_do_while(p),
            "for" => parse_for(p),
            "foreach" => parse_foreach(p),
            "try" => parse_try(p),
            "throw" => parse_keyword_expression_statement(p, PHP_THROW_STATEMENT),
            "break" => parse_keyword_expression_statement(p, PHP_BREAK_STATEMENT),
            "continue" => parse_keyword_expression_statement(p, PHP_CONTINUE_STATEMENT),
            "echo" => parse_comma_expression_statement(p, PHP_ECHO_STATEMENT),
            "global" => parse_comma_expression_statement(p, PHP_GLOBAL_STATEMENT),
            "static" if !static_starts_expression(p) => {
                parse_comma_expression_statement(p, PHP_STATIC_STATEMENT)
            }
            _ if p.at(TK_OPEN_BRACE) => {
                parse_block(p);
            }
            _ if p.at(TK_CLOSE_BRACE) => {
                p.bump();
            }
            _ => parse_expression_statement(p),
        }
    }
}

fn parse_namespace(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    if is_name_start(p) {
        parse_name(p);
    }
    if p.at(TK_SEMICOLON) {
        p.bump();
    } else if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    }
    p.complete(node, PHP_NAMESPACE);
}

fn parse_use(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    let mut depth = 0usize;
    while let Some(kind) = p.peek() {
        if kind == TK_SEMICOLON && depth == 0 {
            p.bump();
            break;
        }
        match kind {
            TK_OPEN_BRACE => depth += 1,
            TK_CLOSE_BRACE => depth = depth.saturating_sub(1),
            _ => {}
        }
        p.bump();
    }
    p.complete(node, PHP_USE_DECLARATION);
}

fn parse_class_declaration(p: &mut P<'_>) {
    let declaration = p.start();
    parse_attribute_groups(p);
    consume_modifiers(p);

    let kind = if at_kw(p, "interface") {
        PHP_INTERFACE_DECLARATION
    } else if at_kw(p, "trait") {
        PHP_TRAIT_DECLARATION
    } else if at_kw(p, "enum") {
        PHP_ENUM_DECLARATION
    } else {
        PHP_CLASS_DECLARATION
    };
    if at_keyword(p, &["class", "interface", "trait", "enum"]) {
        p.bump();
    }
    if is_name_start(p) {
        parse_name(p);
    }
    parse_class_header_clauses(p, true);
    if p.at(TK_OPEN_BRACE) {
        parse_class_body(p);
    } else if p.at(TK_SEMICOLON) {
        p.bump();
    }
    p.complete(declaration, kind);
}

/// Consumes `extends`/`implements` clauses and other header tokens up to the
/// class body (and, for declarations, a semicolon).
fn parse_class_header_clauses(p: &mut P<'_>, stop_at_semicolon: bool) {
    while !p.at_end() && !p.at(TK_OPEN_BRACE) && !(stop_at_semicolon && p.at(TK_SEMICOLON)) {
        if at_kw(p, "extends") {
            parse_name_clause(p, PHP_EXTENDS_CLAUSE);
        } else if at_kw(p, "implements") {
            parse_name_clause(p, PHP_IMPLEMENTS_CLAUSE);
        } else {
            p.bump();
        }
    }
}

fn parse_name_clause(p: &mut P<'_>, kind: Kind) {
    let clause = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_OPEN_BRACE) && !at_keyword(p, &["extends", "implements"]) {
        if is_name_start(p) {
            parse_name(p);
        } else if p.at(TK_COMMA) {
            p.bump();
        } else {
            break;
        }
    }
    p.complete(clause, kind);
}

fn parse_class_body(p: &mut P<'_>) {
    let body = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        parse_statement(p, true);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(body, PHP_CLASS_BODY);
}

fn parse_function_like(p: &mut P<'_>, kind: Kind, allow_modifiers: bool) {
    let method = p.start();
    parse_attribute_groups(p);
    if allow_modifiers {
        consume_modifiers(p);
    }
    if at_kw(p, "function") {
        p.bump();
    }
    bump_if(p, TK_AMPERSAND);
    if is_name_start(p) {
        parse_name(p);
    }
    if p.at(TK_OPEN_PAREN) {
        parse_parameters(p);
    }
    if bump_if(p, TK_COLON) {
        parse_type_expression(p, Stop::of(&[TK_OPEN_BRACE, TK_SEMICOLON]));
    }
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    } else if p.at(TK_SEMICOLON) {
        p.bump();
    }
    p.complete(method, kind);
}

fn parse_parameters(p: &mut P<'_>) {
    let list = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
        let position = p.get_pos();
        let parameter = p.start();
        parse_attribute_groups(p);
        consume_modifiers(p);
        if !p.at(TK_VARIABLE) && !p.at(TK_ELLIPSIS) && !starts_by_reference_parameter(p) {
            parse_type_expression(p, Stop::of(&[TK_VARIABLE]));
        }
        bump_if(p, TK_AMPERSAND);
        bump_if(p, TK_ELLIPSIS);
        if p.at(TK_VARIABLE) {
            parse_leaf(p, PHP_VARIABLE);
        }
        if bump_if(p, TK_EQUALS) {
            parse_expression(p, STOP_COMMA_CLOSE_PAREN);
        }
        p.complete(parameter, PHP_PARAMETER);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_PAREN, &[TK_COLON, TK_OPEN_BRACE, TK_SEMICOLON]);
    p.complete(list, PHP_PARAMETER_LIST);
}

fn starts_by_reference_parameter(p: &P<'_>) -> bool {
    p.at(TK_AMPERSAND) && matches!(next_kind(p), TK_VARIABLE | TK_ELLIPSIS)
}

fn parse_property(p: &mut P<'_>) {
    let property = p.start();
    parse_attribute_groups(p);
    consume_modifiers(p);
    if !p.at(TK_VARIABLE) {
        parse_type_expression(p, Stop::of(&[TK_VARIABLE]));
    }
    while let Some(kind) = p.peek() {
        match kind {
            TK_SEMICOLON | TK_CLOSE_BRACE => break,
            TK_VARIABLE => {
                parse_leaf(p, PHP_VARIABLE);
            }
            TK_EQUALS => {
                p.bump();
                parse_expression(p, STOP_COMMA_SEMICOLON);
            }
            TK_STRING => {
                parse_string(p);
            }
            TK_OPEN_BRACKET => {
                parse_array(p);
            }
            TK_OPEN_BRACE => {
                parse_property_hooks(p);
                p.complete(property, PHP_PROPERTY_DECLARATION);
                return;
            }
            _ => {
                p.bump();
            }
        }
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(property, PHP_PROPERTY_DECLARATION);
}

fn parse_property_hooks(p: &mut P<'_>) {
    let list = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        let hook = p.start();
        parse_attribute_groups(p);
        consume_modifiers(p);
        bump_if(p, TK_AMPERSAND);
        if at_word(p, &["get", "set"]) {
            p.bump();
        }
        if p.at(TK_OPEN_PAREN) {
            parse_parameters(p);
        }
        match p.peek() {
            Some(TK_ARROW) => {
                p.bump();
                parse_expression(p, STOP_SEMICOLON_CLOSE_BRACE);
                bump_if(p, TK_SEMICOLON);
            }
            Some(TK_OPEN_BRACE) => {
                parse_block(p);
            }
            Some(TK_SEMICOLON) => {
                p.bump();
            }
            _ => {}
        }
        p.complete(hook, PHP_PROPERTY_HOOK);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(list, PHP_PROPERTY_HOOK_LIST);
}

fn parse_const(p: &mut P<'_>, in_class: bool) {
    let node = p.start();
    parse_attribute_groups(p);
    if in_class {
        consume_modifiers(p);
    }
    if at_kw(p, "const") {
        p.bump();
    }
    if typed_const_starts(p) {
        parse_type_expression(p, Stop::NONE);
    }
    while !p.at_end() && !p.at(TK_SEMICOLON) && !p.at(TK_CLOSE_BRACE) {
        if is_name_start(p) {
            parse_name(p);
            if bump_if(p, TK_EQUALS) {
                parse_expression(p, STOP_COMMA_SEMICOLON);
            }
        } else {
            p.bump();
        }
        bump_if(p, TK_COMMA);
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(
        node,
        if in_class {
            PHP_CLASS_CONST_DECLARATION
        } else {
            PHP_CONST_DECLARATION
        },
    );
}

fn parse_enum_case(p: &mut P<'_>) {
    let node = p.start();
    parse_attribute_groups(p);
    p.bump();
    if is_name_start(p) {
        parse_name(p);
    }
    if bump_if(p, TK_EQUALS) {
        parse_expression(p, Stop::of(&[TK_SEMICOLON]));
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(node, PHP_ENUM_CASE_DECLARATION);
}

fn parse_trait_use(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_SEMICOLON) && !p.at(TK_OPEN_BRACE) && !p.at(TK_CLOSE_BRACE) {
        if is_name_start(p) {
            parse_name(p);
        } else {
            p.bump();
        }
    }
    if p.at(TK_OPEN_BRACE) {
        let mut depth = 0i64;
        while let Some(kind) = p.peek() {
            match kind {
                TK_OPEN_BRACE => depth += 1,
                TK_CLOSE_BRACE => depth -= 1,
                _ => {}
            }
            p.bump();
            if depth == 0 {
                break;
            }
        }
    } else if p.at(TK_SEMICOLON) {
        p.bump();
    }
    p.complete(node, PHP_TRAIT_USE_DECLARATION);
}

fn parse_if(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    parse_condition(p);
    if p.at(TK_COLON) {
        parse_alternative_body(p, &["elseif", "else", "endif"]);
        while at_kw(p, "elseif") {
            let clause = p.start();
            p.bump();
            parse_condition(p);
            parse_alternative_body(p, &["elseif", "else", "endif"]);
            p.complete(clause, PHP_ELSE_IF_CLAUSE);
        }
        if at_kw(p, "else") {
            let clause = p.start();
            p.bump();
            parse_alternative_body(p, &["endif"]);
            p.complete(clause, PHP_ELSE_CLAUSE);
        }
        consume_alternative_end(p, "endif");
        p.complete(node, PHP_IF_STATEMENT);
        return;
    }
    parse_controlled_statement(p);
    while at_kw(p, "elseif") {
        let clause = p.start();
        p.bump();
        parse_condition(p);
        parse_controlled_statement(p);
        p.complete(clause, PHP_ELSE_IF_CLAUSE);
    }
    if at_kw(p, "else") {
        let clause = p.start();
        p.bump();
        parse_controlled_statement(p);
        p.complete(clause, PHP_ELSE_CLAUSE);
    }
    p.complete(node, PHP_IF_STATEMENT);
}

fn parse_switch(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    parse_condition(p);
    let alternative = p.at(TK_COLON);
    if !p.at(TK_OPEN_BRACE) && !alternative {
        p.complete(node, PHP_SWITCH_STATEMENT);
        return;
    }
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) && !at_kw(p, "endswitch") {
        if at_keyword(p, &["case", "default"]) {
            parse_case(p);
            continue;
        }
        parse_statement(p, false);
    }
    if alternative {
        consume_alternative_end(p, "endswitch");
    } else {
        p.expect(TK_CLOSE_BRACE, &[]);
    }
    p.complete(node, PHP_SWITCH_STATEMENT);
}

fn parse_case(p: &mut P<'_>) {
    let clause = p.start();
    let is_default = at_kw(p, "default");
    p.bump();
    if !is_default {
        parse_expression(p, Stop::of(&[TK_COLON, TK_SEMICOLON]));
    }
    if p.at(TK_COLON) || p.at(TK_SEMICOLON) {
        p.bump();
    }
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) && !at_keyword(p, &["case", "default", "endswitch"])
    {
        parse_statement(p, false);
    }
    p.complete(clause, PHP_CASE_CLAUSE);
}

fn parse_while(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    parse_condition(p);
    if p.at(TK_COLON) {
        parse_alternative_body(p, &["endwhile"]);
        consume_alternative_end(p, "endwhile");
    } else {
        parse_controlled_statement(p);
    }
    p.complete(node, PHP_WHILE_STATEMENT);
}

fn parse_do_while(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    parse_controlled_statement(p);
    if at_kw(p, "while") {
        p.bump();
        parse_condition(p);
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(node, PHP_DO_WHILE_STATEMENT);
}

fn parse_for(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    if bump_if(p, TK_OPEN_PAREN) {
        parse_expression_list(p, TK_SEMICOLON);
        p.expect(TK_SEMICOLON, &[TK_CLOSE_PAREN]);
        parse_expression_list(p, TK_SEMICOLON);
        p.expect(TK_SEMICOLON, &[TK_CLOSE_PAREN]);
        parse_expression_list(p, TK_CLOSE_PAREN);
        p.expect(TK_CLOSE_PAREN, &[TK_OPEN_BRACE, TK_SEMICOLON]);
    }
    if p.at(TK_COLON) {
        parse_alternative_body(p, &["endfor"]);
        consume_alternative_end(p, "endfor");
    } else {
        parse_controlled_statement(p);
    }
    p.complete(node, PHP_FOR_STATEMENT);
}

fn parse_foreach(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    if bump_if(p, TK_OPEN_PAREN) {
        parse_expression(p, Stop::NONE);
        if at_kw(p, "as") {
            p.bump();
        }
        bump_if(p, TK_AMPERSAND);
        parse_expression(p, Stop::of(&[TK_ARROW, TK_CLOSE_PAREN]));
        if bump_if(p, TK_ARROW) {
            bump_if(p, TK_AMPERSAND);
            parse_expression(p, Stop::of(&[TK_CLOSE_PAREN]));
        }
        p.expect(TK_CLOSE_PAREN, &[TK_OPEN_BRACE, TK_SEMICOLON]);
    }
    if p.at(TK_COLON) {
        parse_alternative_body(p, &["endforeach"]);
        consume_alternative_end(p, "endforeach");
    } else {
        parse_controlled_statement(p);
    }
    p.complete(node, PHP_FOREACH_STATEMENT);
}

fn parse_try(p: &mut P<'_>) {
    let node = p.start();
    p.bump();
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    }
    while at_kw(p, "catch") {
        let clause = p.start();
        p.bump();
        if bump_if(p, TK_OPEN_PAREN) {
            parse_type_expression(p, Stop::of(&[TK_VARIABLE, TK_CLOSE_PAREN]));
            if p.at(TK_VARIABLE) {
                parse_leaf(p, PHP_VARIABLE);
            }
            p.expect(TK_CLOSE_PAREN, &[TK_OPEN_BRACE]);
        }
        if p.at(TK_OPEN_BRACE) {
            parse_block(p);
        }
        p.complete(clause, PHP_CATCH_CLAUSE);
    }
    if at_kw(p, "finally") {
        let clause = p.start();
        p.bump();
        if p.at(TK_OPEN_BRACE) {
            parse_block(p);
        }
        p.complete(clause, PHP_FINALLY_CLAUSE);
    }
    p.complete(node, PHP_TRY_STATEMENT);
}

fn parse_keyword_expression_statement(p: &mut P<'_>, kind: Kind) {
    let node = p.start();
    p.bump();
    parse_expression(p, STOP_SEMICOLON_CLOSE_BRACE);
    bump_if(p, TK_SEMICOLON);
    p.complete(node, kind);
}

fn parse_comma_expression_statement(p: &mut P<'_>, kind: Kind) {
    let node = p.start();
    p.bump();
    parse_expression_list(p, TK_SEMICOLON);
    bump_if(p, TK_SEMICOLON);
    p.complete(node, kind);
}

fn parse_expression_list(p: &mut P<'_>, end: Kind) {
    let stop = Stop::of(&[TK_COMMA, end]);
    while !p.at_end() && !p.at(end) {
        let position = p.get_pos();
        parse_expression(p, stop);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
}

fn parse_condition(p: &mut P<'_>) {
    if p.at(TK_OPEN_PAREN) {
        parse_parenthesized(p);
    }
}

fn parse_controlled_statement(p: &mut P<'_>) {
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
        return;
    }
    parse_statement(p, false);
}

fn parse_alternative_body(p: &mut P<'_>, terminators: &[&str]) {
    let block = p.start();
    p.expect(TK_COLON, &[]);
    while !p.at_end() && !at_keyword(p, terminators) {
        let position = p.get_pos();
        parse_statement(p, false);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.complete(block, PHP_BLOCK);
}

fn consume_alternative_end(p: &mut P<'_>, keyword: &'static str) {
    if at_kw(p, keyword) {
        p.bump();
    } else {
        p.add_error(ErrorBuilder::new(keyword));
    }
    bump_if(p, TK_SEMICOLON);
}

fn parse_expression_statement(p: &mut P<'_>) {
    let statement = p.start();
    parse_expression(p, STOP_SEMICOLON_CLOSE_BRACE);
    bump_if(p, TK_SEMICOLON);
    p.complete(statement, PHP_EXPRESSION_STATEMENT);
}

// -- expressions ----------------------------------------------------------------

// PHP's operator table is intentionally not a simple C-style precedence
// ladder. In particular, instanceof binds more tightly than !, while the word
// operators bind less tightly than assignment.
const THROW: i32 = 1;
const LOGICAL_OR_WORD: i32 = 2;
const LOGICAL_XOR_WORD: i32 = 3;
const LOGICAL_AND_WORD: i32 = 4;
const PRINT: i32 = 5;
const YIELD_FROM: i32 = 7;
const ASSIGNMENT: i32 = 8;
const TERNARY: i32 = 9;
const NULL_COALESCING: i32 = 10;
const LOGICAL_OR: i32 = 11;
const LOGICAL_AND: i32 = 12;
const BITWISE_OR: i32 = 13;
const BITWISE_XOR: i32 = 14;
const BITWISE_AND: i32 = 15;
const EQUALITY: i32 = 16;
const COMPARISON: i32 = 17;
const CONCATENATION: i32 = 18;
const SHIFT: i32 = 19;
const ADDITIVE: i32 = 20;
const MULTIPLICATIVE: i32 = 21;
const LOGICAL_NOT: i32 = 22;
const INSTANCEOF: i32 = 23;
const TIGHT_PREFIX: i32 = 24;
const EXPONENTIATION: i32 = 25;

#[inline]
fn parse_expression(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    parse_expression_precedence(p, stop, 0)
}

/// Wraps `operand` in an assignment when the next token is an assignment
/// operator (PHP groups `!$a = b()` as `!($a = b())`).
fn wrap_trailing_assignment(p: &mut P<'_>, operand: CompletedMarker, stop: Stop) {
    let (precedence, _, assignment) = binary_operator(p);
    if assignment && precedence == ASSIGNMENT {
        let node = p.precede(operand);
        p.bump();
        if parse_expression_precedence(p, stop, ASSIGNMENT).is_none() {
            p.add_error(ErrorBuilder::new("expression"));
        }
        p.complete(node, PHP_ASSIGNMENT_EXPRESSION);
    }
}

fn parse_expression_precedence(p: &mut P<'_>, stop: Stop, minimum: i32) -> Option<CompletedMarker> {
    if at_stop(p, stop) {
        return None;
    }
    let mut left = match parse_prefix(p, stop) {
        Some(left) => left,
        None => {
            let node = p.start();
            p.bump();
            p.complete(node, ERROR)
        }
    };
    left = parse_suffixes(p, left);

    while !at_stop(p, stop) {
        if p.at(TK_QUESTION) {
            if TERNARY < minimum {
                return Some(left);
            }
            let ternary = p.precede(left);
            p.bump();
            if !p.at(TK_COLON) {
                parse_expression_precedence(p, Stop::of(&[TK_COLON]), 0);
            }
            p.expect(TK_COLON, &[]);
            parse_expression_precedence(p, stop, TERNARY);
            left = p.complete(ternary, PHP_TERNARY_EXPRESSION);
            continue;
        }

        let (precedence, right_associative, assignment) = binary_operator(p);
        if precedence < minimum {
            return Some(left);
        }
        let expression = p.precede(left);
        p.bump();
        let next = if right_associative {
            precedence
        } else {
            precedence + 1
        };
        match parse_expression_precedence(p, stop, next) {
            None => p.add_error(ErrorBuilder::new("expression")),
            // Assignment is syntactically permitted as the right operand of a
            // tighter binary operator: `$ready && $value = load()`.
            Some(right) if precedence > ASSIGNMENT => wrap_trailing_assignment(p, right, stop),
            Some(_) => {}
        }
        left = p.complete(
            expression,
            if assignment {
                PHP_ASSIGNMENT_EXPRESSION
            } else {
                PHP_BINARY_EXPRESSION
            },
        );
    }
    Some(left)
}

fn parse_prefix(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    if let Some(minimum) = prefix_operator_precedence(p) {
        let node = p.start();
        p.bump();
        match parse_expression_precedence(p, stop, minimum) {
            None => p.add_error(ErrorBuilder::new("expression")),
            Some(operand) if minimum > ASSIGNMENT => wrap_trailing_assignment(p, operand, stop),
            Some(_) => {}
        }
        return Some(p.complete(node, PHP_UNARY_EXPRESSION));
    }
    if at_kw(p, "throw") {
        let node = p.start();
        p.bump();
        parse_expression_precedence(p, stop, THROW);
        return Some(p.complete(node, PHP_THROW_EXPRESSION));
    }
    if at_kw(p, "yield") {
        return Some(parse_yield(p, stop));
    }
    if at_kw(p, "clone") {
        let node = p.start();
        p.bump();
        if p.at(TK_OPEN_PAREN) {
            // PHP 8.5: `clone($object, ['property' => $value])` is a call,
            // and a member access after it applies to the clone.
            parse_arguments(p);
        } else {
            parse_expression_precedence(p, stop, 19);
        }
        return Some(p.complete(node, PHP_CLONE_EXPRESSION));
    }
    if is_cast(p) {
        return Some(parse_cast(p, stop));
    }
    parse_primary(p, stop)
}

/// The operand precedence of a prefix operator at the current token.
fn prefix_operator_precedence(p: &P<'_>) -> Option<i32> {
    let token = p.peek_token()?;
    match token.text {
        "!" => Some(LOGICAL_NOT),
        "~" | "+" | "-" | "@" | "++" | "--" => Some(TIGHT_PREFIX),
        text if token.kind == TK_KEYWORD => {
            if text.eq_ignore_ascii_case("print") {
                Some(PRINT)
            } else if ["include", "include_once", "require", "require_once"]
                .iter()
                .any(|word| text.eq_ignore_ascii_case(word))
            {
                Some(THROW)
            } else {
                None
            }
        }
        _ => None,
    }
}

fn parse_primary(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    let kind = p.peek()?;
    let marker = match kind {
        TK_STRING => parse_string(p),
        TK_NUMBER => parse_leaf(p, PHP_NUMBER),
        TK_VARIABLE => parse_leaf(p, PHP_VARIABLE),
        TK_KEYWORD => {
            let mut buffer = [0u8; 12];
            let keyword = current_keyword(p, &mut buffer);
            match keyword {
                "true" | "false" => parse_leaf(p, PHP_BOOLEAN),
                "null" => parse_leaf(p, PHP_NULL),
                "static" if next_text_is(p, "function") => parse_closure(p, true),
                "static" if next_text_is(p, "fn") => parse_arrow_function(p, true, stop),
                "function" => parse_closure(p, false),
                "fn" => parse_arrow_function(p, false, stop),
                "match" => parse_match(p),
                "new" => parse_object_creation(p),
                "array" if next_kind(p) == TK_OPEN_PAREN => parse_old_array(p),
                _ => parse_name(p),
            }
        }
        TK_IDENTIFIER | TK_BACKSLASH => parse_name(p),
        TK_OPEN_BRACKET => parse_array(p),
        TK_OPEN_PAREN => parse_parenthesized(p),
        TK_OPEN_BRACE => parse_block(p),
        _ => return None,
    };
    Some(marker)
}

fn binary_operator(p: &P<'_>) -> (i32, bool, bool) {
    let Some(token) = p.peek_token() else {
        return (-1, false, false);
    };
    let text = token.text;
    if token.kind == TK_EQUALS
        || token.kind == TK_OPERATOR
            && text.ends_with('=')
            && !matches!(text, "==" | "===" | "!=" | "!==" | "<=" | ">=" | "<=>")
    {
        return (ASSIGNMENT, true, true);
    }
    let precedence = match text {
        "??" => return (NULL_COALESCING, true, false),
        "**" => return (EXPONENTIATION, true, false),
        "||" => LOGICAL_OR,
        "&&" => LOGICAL_AND,
        "|" => BITWISE_OR,
        "^" => BITWISE_XOR,
        "&" => BITWISE_AND,
        "==" | "!=" | "===" | "!==" | "<>" | "<=>" => EQUALITY,
        "<" | ">" | "<=" | ">=" => COMPARISON,
        "<<" | ">>" => SHIFT,
        "." => CONCATENATION,
        "+" | "-" => ADDITIVE,
        "*" | "/" | "%" => MULTIPLICATIVE,
        _ if text.eq_ignore_ascii_case("or") => LOGICAL_OR_WORD,
        _ if text.eq_ignore_ascii_case("xor") => LOGICAL_XOR_WORD,
        _ if text.eq_ignore_ascii_case("and") => LOGICAL_AND_WORD,
        _ if text.eq_ignore_ascii_case("instanceof") => INSTANCEOF,
        _ => -1,
    };
    (precedence, false, false)
}

fn is_cast(p: &P<'_>) -> bool {
    if !p.at(TK_OPEN_PAREN) {
        return false;
    }
    let mut ahead = upcoming(p)
        .iter()
        .filter(|token| !is_trivia(token.kind))
        .skip(1);
    let (Some(type_token), Some(close)) = (ahead.next(), ahead.next()) else {
        return false;
    };
    close.kind == TK_CLOSE_PAREN
        && [
            "array", "bool", "boolean", "double", "float", "int", "integer", "object", "real",
            "string", "unset", "binary",
        ]
        .iter()
        .any(|word| type_token.text.eq_ignore_ascii_case(word))
}

fn parse_cast(p: &mut P<'_>, stop: Stop) -> CompletedMarker {
    let node = p.start();
    p.bump();
    p.bump();
    p.expect(TK_CLOSE_PAREN, &[]);
    parse_expression_precedence(p, stop, TIGHT_PREFIX);
    p.complete(node, PHP_CAST_EXPRESSION)
}

fn parse_yield(p: &mut P<'_>, stop: Stop) -> CompletedMarker {
    let node = p.start();
    p.bump();
    if at_kw(p, "from") {
        p.bump();
        parse_expression_precedence(p, stop, YIELD_FROM);
        return p.complete(node, PHP_YIELD_EXPRESSION);
    }
    if at_stop(p, stop) || p.at(TK_SEMICOLON) {
        return p.complete(node, PHP_YIELD_EXPRESSION);
    }
    parse_expression(p, Stop::of(&[TK_ARROW, TK_SEMICOLON]));
    if bump_if(p, TK_ARROW) {
        parse_expression(p, stop);
    }
    p.complete(node, PHP_YIELD_EXPRESSION)
}

fn parse_suffixes(p: &mut P<'_>, mut expression: CompletedMarker) -> CompletedMarker {
    loop {
        let Some(token) = p.peek_token() else {
            return expression;
        };
        match token.kind {
            TK_OBJECT_OPERATOR | TK_NULLSAFE_OBJECT_OPERATOR => {
                let member = p.precede(expression);
                p.bump();
                parse_member_name(p);
                expression = if p.at(TK_OPEN_PAREN) {
                    parse_arguments(p);
                    p.complete(member, PHP_MEMBER_CALL)
                } else {
                    p.complete(member, PHP_MEMBER_ACCESS)
                };
            }
            TK_SCOPE_RESOLUTION => {
                let call = p.precede(expression);
                p.bump();
                parse_member_name(p);
                // Static access keeps the member-access kind for compatibility;
                // the scope-resolution token distinguishes it.
                expression = if p.at(TK_OPEN_PAREN) {
                    parse_arguments(p);
                    p.complete(call, PHP_SCOPED_CALL)
                } else {
                    p.complete(call, PHP_MEMBER_ACCESS)
                };
            }
            TK_OPEN_PAREN => {
                let call = p.precede(expression);
                parse_arguments(p);
                expression = p.complete(call, PHP_FUNCTION_CALL);
            }
            TK_OPEN_BRACKET => {
                let member = p.precede(expression);
                parse_array_access(p);
                expression = p.complete(member, PHP_ARRAY_ACCESS);
            }
            TK_OPERATOR if token.text == "++" || token.text == "--" => {
                let postfix = p.precede(expression);
                p.bump();
                expression = p.complete(postfix, PHP_UNARY_EXPRESSION);
            }
            _ => return expression,
        }
    }
}

fn parse_member_name(p: &mut P<'_>) {
    if matches!(p.peek(), Some(TK_IDENTIFIER | TK_KEYWORD | TK_VARIABLE)) {
        parse_name(p);
        return;
    }
    if !p.at(TK_OPEN_BRACE) {
        return;
    }
    p.bump();
    parse_expression(p, Stop::of(&[TK_CLOSE_BRACE]));
    p.expect(TK_CLOSE_BRACE, &[]);
}

fn parse_array_access(p: &mut P<'_>) {
    p.bump();
    if !p.at(TK_CLOSE_BRACKET) {
        parse_expression(p, Stop::of(&[TK_CLOSE_BRACKET]));
    }
    p.expect(TK_CLOSE_BRACKET, &[]);
}

fn parse_arguments(p: &mut P<'_>) -> CompletedMarker {
    let arguments = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
        let position = p.get_pos();
        let argument = p.start();
        let mut kind = PHP_ARGUMENT;
        if matches!(p.peek(), Some(TK_IDENTIFIER | TK_KEYWORD)) && next_kind(p) == TK_COLON {
            parse_name(p);
            p.expect(TK_COLON, &[]);
            kind = PHP_NAMED_ARGUMENT;
        }
        if p.at(TK_ELLIPSIS) || p.at(TK_AMPERSAND) {
            p.bump();
        }
        parse_expression(p, STOP_COMMA_CLOSE_PAREN);
        p.complete(argument, kind);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_PAREN, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
    p.complete(arguments, PHP_ARGUMENT_LIST)
}

fn parse_array_items(p: &mut P<'_>, close: Kind) {
    let item_stop = Stop::of(&[TK_COMMA, TK_ARROW, close]);
    let value_stop = Stop::of(&[TK_COMMA, close]);
    while !p.at_end() && !p.at(close) {
        let position = p.get_pos();
        let item = p.start();
        bump_if(p, TK_ELLIPSIS);
        parse_expression(p, item_stop);
        if bump_if(p, TK_ARROW) {
            parse_expression(p, value_stop);
        }
        p.complete(item, PHP_ARRAY_ITEM);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(close, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
}

fn parse_array(p: &mut P<'_>) -> CompletedMarker {
    let array = p.start();
    p.bump();
    parse_array_items(p, TK_CLOSE_BRACKET);
    p.complete(array, PHP_ARRAY)
}

fn parse_old_array(p: &mut P<'_>) -> CompletedMarker {
    let array = p.start();
    p.bump();
    bump_if(p, TK_OPEN_PAREN);
    parse_array_items(p, TK_CLOSE_PAREN);
    p.complete(array, PHP_ARRAY)
}

fn parse_object_creation(p: &mut P<'_>) -> CompletedMarker {
    let object = p.start();
    p.bump();
    if at_kw(p, "class") {
        let anonymous = p.start();
        p.bump();
        if p.at(TK_OPEN_PAREN) {
            parse_arguments(p);
        }
        parse_class_header_clauses(p, false);
        if p.at(TK_OPEN_BRACE) {
            parse_class_body(p);
        }
        p.complete(anonymous, PHP_ANONYMOUS_CLASS);
        return p.complete(object, PHP_OBJECT_CREATION);
    }
    if is_name_start(p) {
        parse_name(p);
    }
    if p.at(TK_OPEN_PAREN) {
        parse_arguments(p);
    }
    p.complete(object, PHP_OBJECT_CREATION)
}

fn parse_closure(p: &mut P<'_>, has_static: bool) -> CompletedMarker {
    let node = p.start();
    if has_static {
        p.bump();
    }
    p.bump();
    bump_if(p, TK_AMPERSAND);
    if p.at(TK_OPEN_PAREN) {
        parse_parameters(p);
    }
    if at_kw(p, "use") {
        p.bump();
        if bump_if(p, TK_OPEN_PAREN) {
            while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
                bump_if(p, TK_AMPERSAND);
                if p.at(TK_VARIABLE) {
                    parse_leaf(p, PHP_VARIABLE);
                } else {
                    p.bump();
                }
                bump_if(p, TK_COMMA);
            }
            p.expect(TK_CLOSE_PAREN, &[TK_COLON, TK_OPEN_BRACE]);
        }
    }
    if bump_if(p, TK_COLON) {
        parse_type_expression(p, Stop::of(&[TK_OPEN_BRACE]));
    }
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    }
    p.complete(node, PHP_CLOSURE)
}

fn parse_arrow_function(p: &mut P<'_>, has_static: bool, stop: Stop) -> CompletedMarker {
    let node = p.start();
    if has_static {
        p.bump();
    }
    p.bump();
    bump_if(p, TK_AMPERSAND);
    if p.at(TK_OPEN_PAREN) {
        parse_parameters(p);
    }
    if bump_if(p, TK_COLON) {
        parse_type_expression(p, Stop::of(&[TK_ARROW]));
    }
    p.expect(TK_ARROW, &[]);
    parse_expression(p, stop);
    p.complete(node, PHP_ARROW_FUNCTION)
}

fn parse_match(p: &mut P<'_>) -> CompletedMarker {
    let node = p.start();
    p.bump();
    if p.at(TK_OPEN_PAREN) {
        parse_parenthesized(p);
    }
    if !p.at(TK_OPEN_BRACE) {
        return p.complete(node, PHP_MATCH_EXPRESSION);
    }
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        let arm = p.start();
        if at_kw(p, "default") {
            p.bump();
        } else {
            parse_expression_list(p, TK_ARROW);
        }
        p.expect(TK_ARROW, &[TK_COMMA, TK_CLOSE_BRACE]);
        parse_expression(p, STOP_COMMA_CLOSE_BRACE);
        p.complete(arm, PHP_MATCH_ARM);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(node, PHP_MATCH_EXPRESSION)
}

fn parse_parenthesized(p: &mut P<'_>) -> CompletedMarker {
    let node = p.start();
    p.bump();
    parse_expression(p, Stop::of(&[TK_CLOSE_PAREN]));
    p.expect(TK_CLOSE_PAREN, &[TK_SEMICOLON, TK_OPEN_BRACE]);
    p.complete(node, PHP_PARENTHESIZED)
}

fn parse_block(p: &mut P<'_>) -> CompletedMarker {
    let block = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        parse_statement(p, false);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(block, PHP_BLOCK)
}

fn parse_attribute_groups(p: &mut P<'_>) {
    while p.at(TK_ATTRIBUTE_OPEN) {
        let group = p.start();
        p.bump();
        while !p.at_end() && !p.at(TK_CLOSE_BRACKET) {
            if bump_if(p, TK_COMMA) {
                continue;
            }
            let attribute = p.start();
            if is_name_start(p) {
                parse_name(p);
            }
            if p.at(TK_OPEN_PAREN) {
                parse_arguments(p);
            }
            p.complete(attribute, PHP_ATTRIBUTE);
            bump_if(p, TK_COMMA);
        }
        p.expect(TK_CLOSE_BRACKET, &[]);
        p.complete(group, PHP_ATTRIBUTE_GROUP);
    }
}

fn parse_name(p: &mut P<'_>) -> CompletedMarker {
    let name = p.start();
    bump_if(p, TK_BACKSLASH);
    while matches!(p.peek(), Some(TK_IDENTIFIER | TK_KEYWORD | TK_VARIABLE)) {
        p.bump();
        if !bump_if(p, TK_BACKSLASH) {
            break;
        }
    }
    p.complete(name, PHP_NAME)
}

fn parse_string(p: &mut P<'_>) -> CompletedMarker {
    let node = p.start();
    let token = p.bump();
    if !closed_string(token.text) {
        p.add_error(ErrorBuilder::new("closing quote").at_token(&token));
    }
    p.complete(node, PHP_STRING)
}

fn parse_leaf(p: &mut P<'_>, kind: Kind) -> CompletedMarker {
    let node = p.start();
    p.bump();
    p.complete(node, kind)
}

// -- types ----------------------------------------------------------------------

fn parse_type_expression(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    let mut left = parse_intersection_type(p, stop)?;
    while p.at(TK_PIPE) && !at_stop(p, stop) {
        let union = p.precede(left);
        p.bump();
        if parse_intersection_type(p, stop).is_none() {
            p.add_error(ErrorBuilder::new("type"));
        }
        left = p.complete(union, PHP_UNION_TYPE);
    }
    Some(left)
}

fn parse_intersection_type(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    let mut left = parse_type_primary(p, stop)?;
    while p.at(TK_AMPERSAND)
        && !matches!(next_kind(p), TK_VARIABLE | TK_ELLIPSIS)
        && !at_stop(p, stop)
    {
        let intersection = p.precede(left);
        p.bump();
        if parse_type_primary(p, stop).is_none() {
            p.add_error(ErrorBuilder::new("type"));
        }
        left = p.complete(intersection, PHP_INTERSECTION_TYPE);
    }
    Some(left)
}

fn parse_type_primary(p: &mut P<'_>, stop: Stop) -> Option<CompletedMarker> {
    if at_stop(p, stop) {
        return None;
    }
    match p.peek() {
        Some(TK_QUESTION) => {
            let nullable = p.start();
            p.bump();
            if parse_type_primary(p, stop).is_none() {
                p.add_error(ErrorBuilder::new("type"));
            }
            Some(p.complete(nullable, PHP_NULLABLE_TYPE))
        }
        Some(TK_OPEN_PAREN) => {
            let group = p.start();
            p.bump();
            parse_type_expression(p, Stop::of(&[TK_CLOSE_PAREN]));
            p.expect(TK_CLOSE_PAREN, &[]);
            Some(p.complete(group, PHP_TYPE))
        }
        Some(TK_IDENTIFIER | TK_KEYWORD | TK_BACKSLASH) => {
            let node = p.start();
            parse_name(p);
            Some(p.complete(node, PHP_TYPE))
        }
        _ => None,
    }
}

// -- declaration lookahead ------------------------------------------------------

fn consume_modifiers(p: &mut P<'_>) {
    while at_keyword(
        p,
        &[
            "abstract",
            "final",
            "public",
            "protected",
            "private",
            "static",
            "readonly",
            "var",
        ],
    ) {
        let modifier = p.bump();
        if is_visibility(modifier.text) && p.at(TK_OPEN_PAREN) {
            let mut ahead = upcoming(p)
                .iter()
                .filter(|token| !is_trivia(token.kind))
                .skip(1);
            let set = ahead
                .next()
                .is_some_and(|token| eq_ignore_case_keyword(token.text, "set"));
            let close = ahead
                .next()
                .is_some_and(|token| token.kind == TK_CLOSE_PAREN);
            if set && close {
                p.bump();
                p.bump();
                p.bump();
            }
        }
    }
}

fn starts_class_declaration(p: &P<'_>) -> bool {
    token_after_prefix(p, &["class", "interface", "trait", "enum"])
}

fn starts_method_declaration(p: &P<'_>) -> bool {
    token_after_prefix(p, &["function"])
}

fn starts_class_const_declaration(p: &P<'_>) -> bool {
    token_after_prefix(p, &["const"])
}

fn starts_function_declaration(p: &P<'_>) -> bool {
    let tokens = upcoming(p);
    let mut index = 0;
    loop {
        let Some(at) = skip_trivia(tokens, index) else {
            return false;
        };
        let token = tokens[at];
        if token.kind == TK_ATTRIBUTE_OPEN {
            let Some(close) = skip_attribute(tokens, at) else {
                return false;
            };
            index = close + 1;
            continue;
        }
        if !eq_ignore_case_keyword(token.text, "function") {
            return false;
        }
        let mut next = skip_trivia(tokens, at + 1);
        if let Some(ampersand) = next.filter(|&index| tokens[index].kind == TK_AMPERSAND) {
            next = skip_trivia(tokens, ampersand + 1);
        }
        return next.is_some_and(|index| matches!(tokens[index].kind, TK_IDENTIFIER | TK_KEYWORD));
    }
}

fn typed_const_starts(p: &P<'_>) -> bool {
    let tokens = upcoming(p);
    let mut previous_name = false;
    let mut index = 0;
    loop {
        let Some(at) = skip_trivia(tokens, index) else {
            return false;
        };
        let token = tokens[at];
        index = at + 1;
        match token.kind {
            TK_ATTRIBUTE_OPEN => {
                let Some(close) = skip_attribute(tokens, at) else {
                    return false;
                };
                index = close + 1;
            }
            TK_EQUALS | TK_COMMA | TK_SEMICOLON | TK_CLOSE_BRACE => return false,
            TK_IDENTIFIER | TK_KEYWORD => {
                if previous_name {
                    return true;
                }
                previous_name = true;
            }
            _ => previous_name = false,
        }
    }
}

fn starts_property_declaration(p: &P<'_>) -> bool {
    if starts_method_declaration(p)
        || starts_class_declaration(p)
        || starts_class_const_declaration(p)
    {
        return false;
    }
    // Class-level declarations with a variable before the next statement
    // delimiter are properties, including typed and multi-property forms.
    let tokens = upcoming(p);
    let mut previous: Option<usize> = None;
    let mut index = 0;
    loop {
        let Some(at) = skip_trivia(tokens, index) else {
            return false;
        };
        let token = tokens[at];
        index = at + 1;
        match token.kind {
            TK_ATTRIBUTE_OPEN => {
                let Some(close) = skip_attribute(tokens, at) else {
                    return false;
                };
                index = close + 1;
                previous = Some(close);
                continue;
            }
            TK_VARIABLE => return true,
            TK_SEMICOLON | TK_OPEN_BRACE | TK_CLOSE_BRACE => return false,
            TK_OPEN_PAREN => {
                let next = skip_trivia(tokens, at + 1);
                let close = next.and_then(|next| skip_trivia(tokens, next + 1));
                let asymmetric = previous
                    .is_some_and(|previous| is_visibility(tokens[previous].text))
                    && next.is_some_and(|next| eq_ignore_case_keyword(tokens[next].text, "set"))
                    && close.is_some_and(|close| tokens[close].kind == TK_CLOSE_PAREN);
                if let (true, Some(close)) = (asymmetric, close) {
                    index = close + 1;
                    previous = Some(close);
                    continue;
                }
                let mut depth = 1;
                let mut cursor = at;
                while depth > 0 {
                    let Some(next) = skip_trivia(tokens, cursor + 1) else {
                        return false;
                    };
                    cursor = next;
                    match tokens[cursor].kind {
                        TK_OPEN_PAREN => depth += 1,
                        TK_CLOSE_PAREN => depth -= 1,
                        TK_SEMICOLON | TK_OPEN_BRACE | TK_CLOSE_BRACE => return false,
                        _ => {}
                    }
                }
                index = cursor + 1;
                previous = Some(cursor);
                continue;
            }
            _ => {}
        }
        previous = Some(at);
    }
}

fn token_after_prefix(p: &P<'_>, words: &[&str]) -> bool {
    let tokens = upcoming(p);
    let mut index = 0;
    loop {
        let Some(at) = skip_trivia(tokens, index) else {
            return false;
        };
        let token = tokens[at];
        index = at + 1;
        if token.kind == TK_ATTRIBUTE_OPEN {
            let Some(close) = skip_attribute(tokens, at) else {
                return false;
            };
            index = close + 1;
            continue;
        }
        if token.kind == TK_KEYWORD && is_modifier(token.text) {
            continue;
        }
        return words
            .iter()
            .any(|word| eq_ignore_case_keyword(token.text, word));
    }
}

fn static_starts_expression(p: &P<'_>) -> bool {
    at_kw(p, "static")
        && (next_kind(p) == TK_SCOPE_RESOLUTION
            || next_text_is(p, "function")
            || next_text_is(p, "fn"))
}

fn closed_string(text: &str) -> bool {
    if let Some(rest) = text.strip_prefix("<<<") {
        let Some(header_end) = rest.find(['\r', '\n']) else {
            return false;
        };
        let mut label = rest[..header_end].trim();
        let bytes = label.as_bytes();
        if bytes.len() >= 2
            && (bytes[0] == b'\'' || bytes[0] == b'"')
            && bytes[bytes.len() - 1] == bytes[0]
        {
            label = &label[1..label.len() - 1];
        }
        return !label.is_empty() && text.trim().ends_with(label);
    }
    let bytes = text.as_bytes();
    bytes.len() >= 2
        && matches!(bytes[0], b'\'' | b'"' | b'`')
        && bytes[bytes.len() - 1] == bytes[0]
}
