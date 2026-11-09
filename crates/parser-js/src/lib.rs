//! Lossless, error-tolerant JavaScript/TypeScript parser.
//!
//! The grammar is intentionally lightweight: it recognises the statement and
//! expression shapes used by Shopware Administration code (imports, default
//! exports, component registrations, object literals with methods) while
//! keeping every other token in the tree.

pub mod lexer;
pub mod query;
pub mod syntax;

use std::sync::Arc;

use swlsp_syntax::{CompletedMarker, Config, ErrorBuilder, Kind, Parse, Parser};

use syntax::*;

type P<'a> = Parser<'a>;

pub fn parse(source: &str) -> Parse {
    parse_shared(Arc::from(source))
}

/// Like [`parse`], but the tree shares `shared` instead of copying it.
pub fn parse_shared(shared: Arc<str>) -> Parse {
    let source: &str = &shared;
    let tokens = lexer::lex(source);
    let mut p = Parser::new(
        source,
        tokens,
        Config {
            general_recovery_set: &[TK_SEMICOLON, TK_CLOSE_BRACE],
            error_kind: ERROR,
        },
    );
    let root = p.start();
    while !p.at_end() {
        let position = p.get_pos();
        parse_statement(&mut p);
        if p.get_pos() == position {
            p.add_error(ErrorBuilder::new("JavaScript statement"));
            p.bump();
        }
    }
    p.complete(root, JS_PROGRAM);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}

fn parse_statement(p: &mut P<'_>) {
    if at_keyword(p, &["import"]) {
        parse_import(p);
    } else if at_keyword(p, &["export"]) && next_non_trivia_text(p) == "default" {
        parse_export_default(p);
    } else if at_keyword(p, &["const", "let", "var"]) {
        parse_variable_declaration(p);
    } else if at_keyword(p, &["if"]) {
        parse_if_statement(p);
    } else if at_keyword(p, &["for", "while", "switch", "with"]) {
        parse_conditional_block(p);
    } else if at_keyword(p, &["try"]) {
        parse_try_statement(p);
    } else if at_keyword(p, &["do"]) {
        parse_do_statement(p);
    } else if at_keyword(p, &["return"]) {
        parse_return_statement(p);
    } else if p.at(TK_CLOSE_BRACE) {
        p.add_error(ErrorBuilder::new("JavaScript statement"));
        p.bump();
    } else {
        let statement = p.start();
        parse_expression(p, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
        bump_if(p, TK_SEMICOLON);
        p.complete(statement, JS_EXPRESSION_STATEMENT);
    }
}

fn bump_if(p: &mut P<'_>, kind: Kind) {
    if p.at(kind) {
        p.bump();
    }
}

fn parse_return_statement(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    if !p.at_end() && !p.at(TK_SEMICOLON) && !p.at(TK_CLOSE_BRACE) {
        parse_expression(p, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(statement, JS_RETURN_STATEMENT);
}

fn parse_if_statement(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    parse_control_condition(p);
    parse_statement_body(p);
    if at_keyword(p, &["else"]) {
        p.bump();
        if at_keyword(p, &["if"]) {
            parse_if_statement(p);
        } else {
            parse_statement_body(p);
        }
    }
    p.complete(statement, JS_EXPRESSION_STATEMENT);
}

fn parse_conditional_block(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    parse_control_condition(p);
    parse_statement_body(p);
    p.complete(statement, JS_EXPRESSION_STATEMENT);
}

fn parse_try_statement(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    parse_statement_body(p);
    if at_keyword(p, &["catch"]) {
        p.bump();
        parse_control_condition(p);
        parse_statement_body(p);
    }
    if at_keyword(p, &["finally"]) {
        p.bump();
        parse_statement_body(p);
    }
    p.complete(statement, JS_EXPRESSION_STATEMENT);
}

fn parse_do_statement(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    parse_statement_body(p);
    if at_keyword(p, &["while"]) {
        p.bump();
        parse_control_condition(p);
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(statement, JS_EXPRESSION_STATEMENT);
}

fn parse_control_condition(p: &mut P<'_>) {
    if p.at(TK_OPEN_PAREN) {
        parse_parenthesized(p);
    }
}

fn parse_statement_body(p: &mut P<'_>) {
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
        return;
    }
    if !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        parse_statement(p);
    }
}

fn parse_import(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_SEMICOLON) {
        if p.at(TK_STRING) || p.at(TK_TEMPLATE) {
            parse_string(p);
        } else {
            p.bump();
        }
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(statement, JS_IMPORT_STATEMENT);
}

fn parse_export_default(p: &mut P<'_>) {
    let statement = p.start();
    p.bump();
    if at_keyword(p, &["default"]) {
        p.bump();
    }
    parse_expression(p, &[TK_SEMICOLON]);
    bump_if(p, TK_SEMICOLON);
    p.complete(statement, JS_EXPORT_DEFAULT);
}

fn parse_variable_declaration(p: &mut P<'_>) {
    let declaration = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_SEMICOLON) && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        parse_expression(p, &[TK_COMMA, TK_SEMICOLON, TK_CLOSE_BRACE]);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    bump_if(p, TK_SEMICOLON);
    p.complete(declaration, JS_VARIABLE_DECLARATION);
}

/// Parses the body after `=>` (block or expression) and completes the arrow.
fn finish_arrow(p: &mut P<'_>, arrow: swlsp_syntax::Marker, stop: &[Kind]) -> CompletedMarker {
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    } else {
        parse_expression(p, stop);
    }
    p.complete(arrow, JS_ARROW_FUNCTION)
}

fn parse_expression(p: &mut P<'_>, stop: &[Kind]) -> Option<CompletedMarker> {
    if p.at_end() || at_stop(p, stop) {
        return None;
    }
    let mut left = match parse_primary(p) {
        Some(left) => left,
        None => {
            let error = p.start();
            p.bump();
            p.complete(error, ERROR)
        }
    };
    left = parse_suffixes(p, left);

    while !p.at_end() && !at_stop(p, stop) {
        if p.at(TK_ARROW) {
            let arrow = p.precede(left);
            p.bump();
            return Some(finish_arrow(p, arrow, stop));
        } else if p.at(TK_COLON) {
            // TypeScript annotations: `(): State => ...` or a parameter type.
            let arrow = p.precede(left);
            p.bump();
            parse_type_annotation(p);
            if !p.at(TK_ARROW) {
                left = p.complete(arrow, JS_IDENTIFIER);
                continue;
            }
            p.bump();
            return Some(finish_arrow(p, arrow, stop));
        } else if p.at(TK_QUESTION) {
            p.bump();
            parse_expression(p, &[TK_COLON]);
            if p.at(TK_COLON) {
                p.bump();
                parse_expression(p, stop);
            }
        } else if p.at(TK_OPERATOR) {
            p.bump();
            if let Some(right) = parse_primary(p) {
                left = parse_suffixes(p, right);
            }
        } else if at_keyword(p, &["as", "satisfies"]) {
            p.bump();
            parse_type_expression(p, stop);
        } else {
            return Some(left);
        }
    }
    Some(left)
}

#[derive(Default)]
struct Depth {
    round: u32,
    square: u32,
    curly: u32,
    angle: i64,
}

impl Depth {
    fn at_top(&self) -> bool {
        self.round == 0 && self.square == 0 && self.curly == 0 && self.angle == 0
    }

    /// Tracks nesting for the next token; returns false when the token closes
    /// a bracket that was opened outside the type.
    fn track(&mut self, kind: Kind, text: &str) -> bool {
        let close = |depth: &mut u32| {
            if *depth == 0 {
                return false;
            }
            *depth -= 1;
            true
        };
        match kind {
            TK_OPEN_PAREN => self.round += 1,
            TK_CLOSE_PAREN => return close(&mut self.round),
            TK_OPEN_BRACKET => self.square += 1,
            TK_CLOSE_BRACKET => return close(&mut self.square),
            TK_OPEN_BRACE => self.curly += 1,
            TK_CLOSE_BRACE => return close(&mut self.curly),
            TK_OPERATOR => {
                self.angle += text.bytes().filter(|byte| *byte == b'<').count() as i64;
                self.angle -= text.bytes().filter(|byte| *byte == b'>').count() as i64;
                self.angle = self.angle.max(0);
            }
            _ => {}
        }
        true
    }
}

/// Consumes a TypeScript value/parameter/arrow return annotation, stopping
/// before `=`, the enclosing delimiter or the arrow.
fn parse_type_annotation(p: &mut P<'_>) {
    let mut depth = Depth::default();
    while let Some(token) = p.peek_token() {
        let (kind, text) = (token.kind, token.text);
        if depth.at_top()
            && (matches!(
                kind,
                TK_ARROW | TK_COMMA | TK_CLOSE_PAREN | TK_SEMICOLON | TK_CLOSE_BRACE
            ) || (kind == TK_OPERATOR && text == "="))
        {
            return;
        }
        if !depth.track(kind, text) {
            return;
        }
        p.bump();
    }
}

fn parse_type_expression(p: &mut P<'_>, stop: &[Kind]) {
    let mut depth = Depth::default();
    while let Some(token) = p.peek_token() {
        let (kind, text) = (token.kind, token.text);
        if depth.at_top() && at_stop(p, stop) {
            return;
        }
        if !depth.track(kind, text) {
            return;
        }
        p.bump();
    }
}

fn parse_primary(p: &mut P<'_>) -> Option<CompletedMarker> {
    let kind = p.peek()?;
    Some(match kind {
        TK_STRING | TK_TEMPLATE => parse_string(p),
        TK_NUMBER => parse_leaf(p, JS_NUMBER),
        TK_IDENTIFIER => parse_leaf(p, JS_IDENTIFIER),
        TK_KEYWORD => match peek_text(p) {
            "true" | "false" => parse_leaf(p, JS_BOOLEAN),
            "null" | "undefined" => parse_leaf(p, JS_NULL),
            "function" => parse_function(p),
            "class" => parse_class_like(p),
            "await" | "new" | "typeof" | "void" | "delete" | "throw" | "yield" => parse_unary(p),
            "async" | "return" | "this" | "super" | "import" => parse_leaf(p, JS_IDENTIFIER),
            _ => return None,
        },
        TK_OPERATOR if is_prefix_operator(peek_text(p)) => parse_unary(p),
        TK_OPEN_BRACE => parse_object(p),
        TK_OPEN_BRACKET => parse_array(p),
        TK_OPEN_PAREN => parse_parenthesized(p),
        _ => return None,
    })
}

fn parse_unary(p: &mut P<'_>) -> CompletedMarker {
    let unary = p.start();
    p.bump();
    match parse_primary(p) {
        Some(operand) => {
            parse_suffixes(p, operand);
        }
        None => p.add_error(ErrorBuilder::new("unary expression operand")),
    }
    p.complete(unary, JS_UNARY_EXPRESSION)
}

fn is_prefix_operator(value: &str) -> bool {
    matches!(value, "!" | "~" | "+" | "-" | "++" | "--")
}

fn parse_suffixes(p: &mut P<'_>, mut expression: CompletedMarker) -> CompletedMarker {
    loop {
        match p.peek() {
            Some(TK_DOT | TK_OPTIONAL_CHAIN) => {
                let member = p.precede(expression);
                p.bump();
                if p.at(TK_IDENTIFIER) || p.at(TK_KEYWORD) {
                    parse_leaf(p, JS_IDENTIFIER);
                } else {
                    p.add_error(ErrorBuilder::new("property name"));
                }
                expression = p.complete(member, JS_MEMBER_EXPRESSION);
            }
            Some(TK_OPEN_BRACKET) => {
                let member = p.precede(expression);
                parse_computed(p);
                expression = p.complete(member, JS_MEMBER_EXPRESSION);
            }
            Some(TK_OPEN_PAREN) => {
                let call = p.precede(expression);
                parse_arguments(p);
                expression = p.complete(call, JS_CALL_EXPRESSION);
            }
            _ => return expression,
        }
    }
}

fn parse_computed(p: &mut P<'_>) {
    let computed = p.start();
    p.bump();
    parse_expression(p, &[TK_CLOSE_BRACKET]);
    p.expect(TK_CLOSE_BRACKET, &[]);
    p.complete(computed, JS_COMPUTED);
}

fn parse_arguments(p: &mut P<'_>) -> CompletedMarker {
    let arguments = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
        let position = p.get_pos();
        let argument = p.start();
        parse_expression(p, &[TK_COMMA, TK_CLOSE_PAREN]);
        p.complete(argument, JS_ARGUMENT);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_PAREN, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
    p.complete(arguments, JS_ARGUMENT_LIST)
}

fn parse_object(p: &mut P<'_>) -> CompletedMarker {
    let object = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        if p.at(TK_COMMA) {
            p.bump();
            continue;
        }
        let position = p.get_pos();
        parse_object_member(p);
        if p.at(TK_COMMA) || p.at(TK_SEMICOLON) {
            p.bump();
        }
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[TK_SEMICOLON]);
    p.complete(object, JS_OBJECT)
}

fn parse_object_member(p: &mut P<'_>) {
    let member = p.start();

    if p.at(TK_OPERATOR) && peek_text(p) == "..." {
        p.bump();
        parse_expression(p, &[TK_COMMA, TK_CLOSE_BRACE]);
        p.complete(member, JS_PROPERTY);
        return;
    }

    if at_keyword(p, &["async", "get", "set", "static"]) && object_modifier_has_name(p) {
        // Modifiers stay plain tokens so the property name is the real name.
        p.bump();
        if p.at(TK_OPERATOR) && peek_text(p) == "*" {
            p.bump();
        }
    }

    match p.peek() {
        Some(TK_IDENTIFIER | TK_KEYWORD) => {
            parse_leaf(p, JS_IDENTIFIER);
        }
        Some(TK_STRING | TK_TEMPLATE) => {
            parse_string(p);
        }
        Some(TK_OPEN_BRACKET) => parse_computed(p),
        _ => {
            p.add_error(ErrorBuilder::new("object property"));
            p.bump();
            p.complete(member, ERROR);
            return;
        }
    }

    match p.peek() {
        Some(TK_COLON) => {
            p.bump();
            parse_expression(p, &[TK_COMMA, TK_CLOSE_BRACE]);
            p.complete(member, JS_PROPERTY);
        }
        Some(TK_OPEN_PAREN) => {
            parse_arguments(p);
            parse_return_type(p);
            if p.at(TK_OPEN_BRACE) {
                parse_block(p);
            }
            p.complete(member, JS_METHOD);
        }
        _ => {
            p.complete(member, JS_PROPERTY);
        }
    }
}

fn object_modifier_has_name(p: &P<'_>) -> bool {
    matches!(
        p.peek_next_non_trivia_kind(),
        Some(TK_IDENTIFIER | TK_KEYWORD | TK_STRING | TK_TEMPLATE | TK_OPEN_BRACKET | TK_OPERATOR)
    )
}

fn parse_return_type(p: &mut P<'_>) {
    if !p.at(TK_COLON) {
        return;
    }
    p.bump();
    // A method return annotation ends at its body; a leading object literal,
    // an intersection/union branch or an object nested in a generic belongs
    // to the type.
    let mut saw_type_token = false;
    let mut angle_depth = 0u32;
    let mut previous_operator = false;
    while !p.at_end() && !p.at(TK_COMMA) && !p.at(TK_CLOSE_BRACE) {
        if p.at(TK_OPEN_BRACE) {
            if !saw_type_token || angle_depth > 0 || previous_operator {
                consume_type_object(p);
                saw_type_token = true;
                previous_operator = false;
                continue;
            }
            return;
        }
        let token = p.bump();
        saw_type_token = true;
        previous_operator = false;
        if token.kind == TK_OPERATOR {
            match token.text {
                "<" => angle_depth += 1,
                ">" => angle_depth = angle_depth.saturating_sub(1),
                "|" | "&" => previous_operator = true,
                _ => {}
            }
        }
    }
}

fn consume_type_object(p: &mut P<'_>) {
    let mut depth = 0i64;
    while let Some(kind) = p.peek() {
        p.bump();
        match kind {
            TK_OPEN_BRACE => depth += 1,
            TK_CLOSE_BRACE => {
                depth -= 1;
                if depth == 0 {
                    return;
                }
            }
            _ => {}
        }
    }
}

fn parse_array(p: &mut P<'_>) -> CompletedMarker {
    let array = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACKET) {
        let position = p.get_pos();
        parse_expression(p, &[TK_COMMA, TK_CLOSE_BRACKET]);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACKET, &[TK_SEMICOLON, TK_CLOSE_BRACE]);
    p.complete(array, JS_ARRAY)
}

fn parse_parenthesized(p: &mut P<'_>) -> CompletedMarker {
    let parenthesized = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_PAREN) {
        let position = p.get_pos();
        parse_expression(p, &[TK_COMMA, TK_CLOSE_PAREN]);
        bump_if(p, TK_COMMA);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_PAREN, &[TK_ARROW, TK_OPEN_BRACE]);
    p.complete(parenthesized, JS_PARENTHESIZED)
}

fn parse_function(p: &mut P<'_>) -> CompletedMarker {
    let function = p.start();
    p.bump();
    if p.at(TK_IDENTIFIER) {
        parse_leaf(p, JS_IDENTIFIER);
    }
    if p.at(TK_OPEN_PAREN) {
        parse_arguments(p);
    }
    parse_return_type(p);
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    }
    p.complete(function, JS_FUNCTION)
}

fn parse_class_like(p: &mut P<'_>) -> CompletedMarker {
    let class = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_OPEN_BRACE) && !p.at(TK_SEMICOLON) {
        p.bump();
    }
    if p.at(TK_OPEN_BRACE) {
        parse_block(p);
    }
    p.complete(class, JS_FUNCTION)
}

fn parse_block(p: &mut P<'_>) -> CompletedMarker {
    let block = p.start();
    p.bump();
    while !p.at_end() && !p.at(TK_CLOSE_BRACE) {
        let position = p.get_pos();
        parse_statement(p);
        if p.get_pos() == position {
            p.bump();
        }
    }
    p.expect(TK_CLOSE_BRACE, &[]);
    p.complete(block, JS_BLOCK)
}

fn parse_string(p: &mut P<'_>) -> CompletedMarker {
    let node = p.start();
    let token = p.bump();
    if !closed_string(token.text) {
        p.add_error(ErrorBuilder::new("closing quote").at_token(&token));
    }
    p.complete(node, JS_STRING)
}

fn parse_leaf(p: &mut P<'_>, kind: Kind) -> CompletedMarker {
    let node = p.start();
    p.bump();
    p.complete(node, kind)
}

fn peek_text<'a>(p: &P<'a>) -> &'a str {
    p.peek_token().map_or("", |token| token.text)
}

fn at_keyword(p: &P<'_>, words: &[&str]) -> bool {
    p.at(TK_KEYWORD) && words.contains(&peek_text(p))
}

fn next_non_trivia_text<'a>(p: &P<'a>) -> &'a str {
    let mut current_seen = false;
    let mut offset = 0;
    while let Some(token) = p.peek_nth_token(offset) {
        offset += 1;
        if token.kind.is_trivia() {
            continue;
        }
        if !current_seen {
            current_seen = true;
            continue;
        }
        return token.text;
    }
    ""
}

fn at_stop(p: &P<'_>, stop: &[Kind]) -> bool {
    p.peek().is_none_or(|kind| stop.contains(&kind))
}

fn closed_string(text: &str) -> bool {
    let bytes = text.as_bytes();
    bytes.len() >= 2
        && matches!(bytes[0], b'\'' | b'"' | b'`')
        && bytes[bytes.len() - 1] == bytes[0]
}

#[cfg(test)]
mod tests;
