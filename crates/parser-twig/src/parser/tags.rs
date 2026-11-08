//! `{% ... %}` tags.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, Kind, Marker, Parser};

use super::expression::parse_twig_expression;
use super::literal::{
    parse_twig_filter, parse_twig_function_argument, parse_twig_hash, parse_twig_name,
    parse_twig_string,
};
use super::shopware::parse_shopware_twig_block_statement;
use super::twig::{parse_twig_inline_comment, parse_twig_inline_comments};
use super::{ParseFn, TWIG_BLOCK_CLOSE_SET, TWIG_BLOCK_OPEN_SET, TwigParserExt, parse_many};
use crate::syntax::*;

const PC: Kind = TK_PERCENT_CURLY;
const MPC: Kind = TK_MINUS_PERCENT_CURLY;
const TPC: Kind = TK_TILDE_PERCENT_CURLY;
const LTS: Kind = TK_LESS_THAN_SLASH;

/// Recovery after the closing delimiter of a tag (`</` only).
const AFTER_CLOSE: &[Kind] = &[LTS];
/// The block close delimiters plus `</`.
const CLOSE_LTS: &[Kind] = &[PC, MPC, TPC, LTS];

/// Twig ending / delimiter keywords that make HTML body parsers stop early.
const TERMINATION_KEYWORDS: &[Kind] = &[
    TK_ENDBLOCK,
    TK_ENDIF,
    TK_ELSE_IF,
    TK_ELSE,
    TK_ENDSET,
    TK_ENDFOR,
    TK_ENDEMBED,
    TK_SW_END_EMBED,
    TK_ENDAPPLY,
    TK_ENDAUTOESCAPE,
    TK_ENDSANDBOX,
    TK_ENDVERBATIM,
    TK_ENDMACRO,
    TK_ENDWITH,
    TK_ENDCACHE,
    TK_ENDSW_SILENT_FEATURE_CALL,
    TK_ENDTRANS,
];

/// Whether the parser is at a Twig ending / delimiter tag like `endblock` or
/// `elseif`, which HTML body parsers treat as a stop signal.
pub(crate) fn at_twig_termination_tag(p: &Parser<'_>) -> bool {
    if !p.at_twig_block_open() {
        return false;
    }
    if p.at_twig_word_tag("endstylesheets") || p.at_twig_word_tag("endjavascripts") {
        return true;
    }
    p.peek_next_non_trivia_kind()
        .is_some_and(|kind| TERMINATION_KEYWORDS.contains(&kind))
}

pub(crate) fn parse_twig_block_statement(
    p: &mut Parser<'_>,
    child_parser: ParseFn,
) -> Option<CompletedMarker> {
    let m = p.start();
    p.bump();

    let completed = match p.peek() {
        Some(TK_BLOCK) => parse_twig_block(p, m, child_parser),
        Some(TK_IF) => parse_twig_if(p, m, child_parser),
        Some(TK_SET) => parse_twig_set(p, m, child_parser),
        Some(TK_FOR) => parse_twig_for(p, m, child_parser),
        Some(TK_EXTENDS) => parse_twig_extends(p, m),
        Some(TK_INCLUDE) => parse_twig_include(p, m),
        Some(TK_EMBED | TK_SW_EMBED) => parse_twig_embed(p, m, child_parser),
        Some(TK_USE | TK_SW_USE) => parse_twig_use(p, m),
        Some(TK_FROM | TK_SW_FROM) => parse_twig_from(p, m),
        Some(TK_IMPORT | TK_SW_IMPORT) => parse_twig_import(p, m),
        Some(TK_APPLY) => parse_twig_apply(p, m, child_parser),
        Some(TK_AUTOESCAPE) => parse_twig_autoescape(p, m, child_parser),
        Some(TK_DEPRECATED) => parse_twig_deprecated(p, m),
        Some(TK_DO) => parse_twig_do(p, m),
        Some(TK_FLUSH) => parse_twig_flush(p, m),
        Some(TK_SANDBOX) => simple_body_tag(
            p,
            m,
            child_parser,
            TK_ENDSANDBOX,
            TWIG_SANDBOX_STARTING_BLOCK,
            TWIG_SANDBOX_ENDING_BLOCK,
            TWIG_SANDBOX,
        ),
        Some(TK_VERBATIM) => simple_body_tag(
            p,
            m,
            child_parser,
            TK_ENDVERBATIM,
            TWIG_VERBATIM_STARTING_BLOCK,
            TWIG_VERBATIM_ENDING_BLOCK,
            TWIG_VERBATIM,
        ),
        Some(TK_MACRO) => parse_twig_macro(p, m, child_parser),
        Some(TK_WITH) => parse_twig_with(p, m, child_parser),
        Some(TK_CACHE) => parse_twig_cache(p, m, child_parser),
        Some(TK_TRANS) => simple_body_tag(
            p,
            m,
            child_parser,
            TK_ENDTRANS,
            TWIG_TRANS_STARTING_BLOCK,
            TWIG_TRANS_ENDING_BLOCK,
            TWIG_TRANS,
        ),
        Some(TK_COMPONENT) => parse_twig_component(p, m, child_parser),
        Some(TK_PROPS) => parse_twig_props(p, m),
        _ if p.at_word("types") => parse_twig_types(p, m),
        _ if p.at_word("form_theme") => parse_twig_form_theme(p, m),
        _ if p.at_word("stylesheets") || p.at_word("javascripts") => {
            parse_twig_assetic(p, m, child_parser)
        }
        _ => match parse_shopware_twig_block_statement(p, m, child_parser) {
            Ok(completed) => completed,
            Err(m) => {
                p.add_error(ErrorBuilder::new("twig tag"));
                p.complete(m, ERROR);
                return None;
            }
        },
    };
    Some(completed)
}

/// Body until `{% end_kind`, then the ending block `{% end_kind %}`. Returns
/// after completing `ending_kind`; the caller completes the wrapper.
fn parse_body_and_ending(
    p: &mut Parser<'_>,
    child_parser: ParseFn,
    end_kind: Kind,
    ending_kind: Kind,
) {
    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_tag(end_kind),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, &[end_kind, PC, MPC, TPC, LTS]);
    p.expect(end_kind, CLOSE_LTS);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, ending_kind);
}

/// `{% keyword %}body{% endkeyword %}` without arguments (sandbox, verbatim,
/// trans).
#[allow(clippy::too_many_arguments)]
fn simple_body_tag(
    p: &mut Parser<'_>,
    outer: Marker,
    child_parser: ParseFn,
    end_kind: Kind,
    starting_kind: Kind,
    ending_kind: Kind,
    kind: Kind,
) -> CompletedMarker {
    p.bump();
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[end_kind, PC, MPC, TPC, LTS]);
    let starting = p.complete(outer, starting_kind);
    let wrapper = p.precede(starting);
    parse_body_and_ending(p, child_parser, end_kind, ending_kind);
    p.complete(wrapper, kind)
}

/// Static-analysis `{% types ... %}` tag kept as a tolerant raw boundary.
fn parse_twig_types(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    while !p.at_end() && !p.at_twig_block_close() {
        p.bump();
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, TWIG_TYPES)
}

/// Legacy Assetic `{% stylesheets ... %}...{% endstylesheets %}` blocks.
fn parse_twig_assetic(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    let tag = p.bump().text;
    let end_tag = format!("end{tag}");
    parse_many(
        p,
        |p| p.at_twig_block_close(),
        |p| {
            if p.at(TK_WORD) && p.peek_next_non_trivia_kind() == Some(TK_EQUAL) {
                let option = p.start();
                p.bump();
                p.bump();
                if parse_twig_expression(p).is_none() {
                    p.add_error(ErrorBuilder::new("Assetic option value"));
                    p.recover(TWIG_BLOCK_CLOSE_SET);
                }
                p.complete(option, TWIG_NAMED_ARGUMENT);
                return;
            }
            if parse_twig_expression(p).is_none() {
                p.add_error(ErrorBuilder::new("Assetic asset or option"));
                p.recover(TWIG_BLOCK_CLOSE_SET);
            }
        },
    );
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);

    let starting = p.complete(outer, TWIG_ASSETIC_STARTING_BLOCK);
    let wrapper = p.precede(starting);

    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_word_tag(&end_tag),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, &[TK_WORD, PC, MPC, TPC]);
    if p.at_word(&end_tag) {
        p.bump();
    } else {
        p.add_error(ErrorBuilder::new(end_tag));
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, TWIG_ASSETIC_ENDING_BLOCK);

    p.complete(wrapper, TWIG_ASSETIC)
}

/// Symfony's `{% form_theme form 'a' %}` / `{% form_theme form with [...] %}`.
fn parse_twig_form_theme(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("form expression"));
    }
    if p.at(TK_WITH) {
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("form theme expression"));
        }
    } else {
        parse_many(
            p,
            |p| p.at_twig_block_close(),
            |p| {
                if parse_twig_expression(p).is_none() {
                    p.add_error(ErrorBuilder::new("form theme expression"));
                    p.recover(TWIG_BLOCK_CLOSE_SET);
                }
            },
        );
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_FORM_THEME)
}

fn parse_twig_cache(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as cache key"));
        p.recover(&[
            TK_TTL,
            TK_TAGS,
            TK_ENDCACHE,
            TK_OPEN_PARENTHESIS,
            TK_CLOSE_PARENTHESIS,
            PC,
            MPC,
            TPC,
            LTS,
        ]);
    }

    if p.at(TK_TTL) {
        let ttl = p.start();
        p.bump();
        p.expect(
            TK_OPEN_PARENTHESIS,
            &[
                TK_CLOSE_PARENTHESIS,
                TK_TAGS,
                TK_ENDCACHE,
                PC,
                MPC,
                TPC,
                LTS,
            ],
        );
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as cache time to live"));
            p.recover(&[
                TK_CLOSE_PARENTHESIS,
                TK_TAGS,
                TK_ENDCACHE,
                PC,
                MPC,
                TPC,
                LTS,
            ]);
        }
        p.expect(
            TK_CLOSE_PARENTHESIS,
            &[TK_TAGS, TK_ENDCACHE, PC, MPC, TPC, LTS],
        );
        p.complete(ttl, TWIG_CACHE_TTL);
    }
    if p.at(TK_TAGS) {
        let tags = p.start();
        p.bump();
        p.expect(
            TK_OPEN_PARENTHESIS,
            &[TK_CLOSE_PARENTHESIS, TK_ENDCACHE, PC, MPC, TPC, LTS],
        );
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as cache tags"));
            p.recover(&[TK_CLOSE_PARENTHESIS, TK_ENDCACHE, PC, MPC, TPC, LTS]);
        }
        p.expect(TK_CLOSE_PARENTHESIS, &[TK_ENDCACHE, PC, MPC, TPC, LTS]);
        p.complete(tags, TWIG_CACHE_TAGS);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDCACHE, PC, MPC, TPC, LTS]);

    let starting = p.complete(outer, TWIG_CACHE_STARTING_BLOCK);
    let wrapper = p.precede(starting);
    parse_body_and_ending(p, child_parser, TK_ENDCACHE, TWIG_CACHE_ENDING_BLOCK);
    p.complete(wrapper, TWIG_CACHE)
}

fn parse_twig_with(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    parse_twig_expression(p);
    if p.at(TK_ONLY) {
        p.bump();
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDWITH, PC, MPC, TPC, LTS]);
    let starting = p.complete(outer, TWIG_WITH_STARTING_BLOCK);
    let wrapper = p.precede(starting);
    parse_body_and_ending(p, child_parser, TK_ENDWITH, TWIG_WITH_ENDING_BLOCK);
    p.complete(wrapper, TWIG_WITH)
}

/// The macro / block end tag may repeat the opening name.
fn parse_optional_end_name(p: &mut Parser<'_>, opening_name: Option<&str>, description: &str) {
    if !p.at(TK_WORD) {
        return;
    }
    let end_name = p.bump();
    if let Some(name) = opening_name
        && end_name.text != name
    {
        p.add_error(
            ErrorBuilder::new(format!(
                "nothing or same twig {description} name as opening ({name})"
            ))
            .at_token(&end_name),
        );
        p.recover(CLOSE_LTS);
    }
}

fn parse_twig_macro(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    let macro_name = p
        .expect(
            TK_WORD,
            &[
                TK_OPEN_PARENTHESIS,
                TK_CLOSE_PARENTHESIS,
                TK_ENDMACRO,
                PC,
                MPC,
                TPC,
                LTS,
            ],
        )
        .map(|token| token.text);

    let arguments = p.start();
    p.expect(
        TK_OPEN_PARENTHESIS,
        &[TK_CLOSE_PARENTHESIS, TK_ENDMACRO, PC, MPC, TPC, LTS],
    );
    const ARGUMENTS_END: &[Kind] = &[PC, MPC, TPC, TK_CLOSE_PARENTHESIS];
    parse_many(
        p,
        |p| p.at_set(ARGUMENTS_END),
        |p| {
            if parse_twig_inline_comment(p).is_some() {
                return;
            }
            parse_twig_function_argument(p);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at_set(ARGUMENTS_END) {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    p.expect(TK_CLOSE_PARENTHESIS, &[TK_ENDMACRO, PC, MPC, TPC, LTS]);
    p.complete(arguments, TWIG_ARGUMENTS);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDMACRO, PC, MPC, TPC, LTS]);

    let starting = p.complete(outer, TWIG_MACRO_STARTING_BLOCK);
    let wrapper = p.precede(starting);

    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_tag(TK_ENDMACRO),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, &[TK_ENDMACRO, PC, MPC, TPC, LTS]);
    p.expect(TK_ENDMACRO, CLOSE_LTS);
    parse_optional_end_name(p, macro_name, "macro");
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, TWIG_MACRO_ENDING_BLOCK);

    p.complete(wrapper, TWIG_MACRO)
}

fn parse_twig_flush(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_FLUSH)
}

fn parse_twig_do(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(CLOSE_LTS);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_DO)
}

fn parse_twig_deprecated(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        parse_twig_string(p, false);
    } else {
        p.add_error(ErrorBuilder::new("twig deprecation message as string"));
        p.recover(CLOSE_LTS);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_DEPRECATED)
}

fn parse_twig_autoescape(
    p: &mut Parser<'_>,
    outer: Marker,
    child_parser: ParseFn,
) -> CompletedMarker {
    p.bump();
    if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        parse_twig_string(p, false);
    } else if p.at(TK_FALSE) {
        p.bump();
    } else if !p.at_twig_block_close() {
        p.add_error(ErrorBuilder::new(
            "twig escape strategy as string or 'false'",
        ));
        p.recover(&[PC, MPC, TPC, TK_ENDAUTOESCAPE, LTS]);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDAUTOESCAPE, LTS]);
    let starting = p.complete(outer, TWIG_AUTOESCAPE_STARTING_BLOCK);
    let wrapper = p.precede(starting);
    parse_body_and_ending(
        p,
        child_parser,
        TK_ENDAUTOESCAPE,
        TWIG_AUTOESCAPE_ENDING_BLOCK,
    );
    p.complete(wrapper, TWIG_AUTOESCAPE)
}

fn parse_twig_apply(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    if let Some(mut node) = parse_twig_name(p) {
        const ARGUMENTS_END: &[Kind] = &[PC, MPC, TPC, TK_CLOSE_PARENTHESIS];
        if p.at(TK_OPEN_PARENTHESIS) {
            let arguments = p.start();
            p.bump();
            parse_many(
                p,
                |p| p.at_set(ARGUMENTS_END),
                |p| {
                    parse_twig_function_argument(p);
                    if p.at(TK_COMMA) {
                        p.bump();
                    } else if !p.at_set(ARGUMENTS_END) {
                        p.add_error(ErrorBuilder::new(","));
                    }
                },
            );
            p.expect(TK_CLOSE_PARENTHESIS, &[TK_ENDAPPLY, PC, MPC, TPC, LTS]);
            p.complete(arguments, TWIG_ARGUMENTS);
        }
        parse_many(
            p,
            |p| p.at_twig_block_close(),
            |p| {
                if p.at(TK_SINGLE_PIPE) {
                    node = parse_twig_filter(p, node);
                }
            },
        );
    } else {
        p.add_error(ErrorBuilder::new("twig filter"));
        p.recover(&[PC, MPC, TPC, TK_ENDAPPLY, LTS]);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDAPPLY, LTS]);
    let starting = p.complete(outer, TWIG_APPLY_STARTING_BLOCK);
    let wrapper = p.precede(starting);
    parse_body_and_ending(p, child_parser, TK_ENDAPPLY, TWIG_APPLY_ENDING_BLOCK);
    p.complete(wrapper, TWIG_APPLY)
}

fn parse_twig_import(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template"));
        p.recover(&[TK_AS, PC, MPC, TPC, TK_WORD, LTS]);
    }
    p.expect(TK_AS, &[PC, MPC, TPC, TK_WORD, LTS]);
    if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new("name for twig macro"));
        p.recover(CLOSE_LTS);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_IMPORT)
}

/// Comma-separated `name [as alias]` overrides up to the block close.
fn parse_overrides(p: &mut Parser<'_>, description: &str) -> usize {
    let mut count = 0;
    parse_many(
        p,
        |p| p.at_twig_block_close(),
        |p| {
            count += 1;
            parse_name_as_name_override(p, description);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at_twig_block_close() {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    count
}

fn parse_twig_from(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template"));
        p.recover(&[TK_IMPORT, TK_SW_IMPORT, PC, MPC, TPC, LTS]);
    }
    if p.at_set(&[TK_IMPORT, TK_SW_IMPORT]) {
        p.bump();
    } else {
        p.add_error(ErrorBuilder::new("import or sw_import"));
        p.recover(CLOSE_LTS);
    }
    if parse_overrides(p, "macro name") < 1 {
        p.add_error(ErrorBuilder::new("at least one macro name as macro name"));
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_FROM)
}

fn parse_name_as_name_override(p: &mut Parser<'_>, description: &str) -> CompletedMarker {
    let m = p.start();
    if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new(description));
        p.recover(&[TK_AS, PC, MPC, TPC, LTS]);
    }
    if p.at(TK_AS) {
        p.bump();
        if parse_twig_name(p).is_none() {
            p.add_error(ErrorBuilder::new(description));
            p.recover(CLOSE_LTS);
        }
    }
    p.complete(m, TWIG_OVERRIDE)
}

fn parse_twig_use(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        parse_twig_string(p, false);
    } else {
        p.add_error(ErrorBuilder::new("twig string as template"));
        p.recover(&[TK_WITH, TK_WORD, PC, MPC, TPC, LTS]);
    }
    if p.at(TK_WITH) {
        p.bump();
        if parse_overrides(p, "block name") < 1 {
            p.add_error(ErrorBuilder::new("at least one block name as block name"));
        }
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_USE)
}

fn parse_twig_embed(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    let is_sw_embed = p.at(TK_SW_EMBED);
    p.bump();
    let expected_end = if is_sw_embed {
        TK_SW_END_EMBED
    } else {
        TK_ENDEMBED
    };

    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template name"));
        p.recover(&[
            TK_IGNORE_MISSING,
            TK_WITH,
            TK_ONLY,
            TK_ENDEMBED,
            TK_SW_END_EMBED,
            PC,
            MPC,
            TPC,
            LTS,
        ]);
    }
    if p.at(TK_IGNORE_MISSING) {
        p.bump();
    }
    if p.at(TK_WITH) {
        let with = p.start();
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as with value"));
            p.recover(&[TK_ONLY, TK_ENDEMBED, TK_SW_END_EMBED, PC, MPC, TPC, LTS]);
        }
        p.complete(with, TWIG_INCLUDE_WITH);
    }
    if p.at(TK_ONLY) {
        p.bump();
    }
    p.expect_any(
        TWIG_BLOCK_CLOSE_SET,
        &[TK_ENDEMBED, TK_SW_END_EMBED, PC, MPC, TPC, LTS],
    );

    let starting = p.complete(outer, TWIG_EMBED_STARTING_BLOCK);
    let wrapper = p.precede(starting);

    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_tag(TK_ENDEMBED) || p.at_twig_tag(TK_SW_END_EMBED),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    let ending = p.start();
    p.expect_any(
        TWIG_BLOCK_OPEN_SET,
        &[TK_ENDEMBED, TK_SW_END_EMBED, PC, MPC, TPC, LTS],
    );
    p.expect(expected_end, CLOSE_LTS);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, TWIG_EMBED_ENDING_BLOCK);

    p.complete(wrapper, TWIG_EMBED)
}

fn parse_twig_include(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template name"));
        p.recover(&[TK_IGNORE_MISSING, TK_WITH, TK_ONLY, PC, MPC, TPC, LTS]);
    }
    if p.at(TK_IGNORE_MISSING) {
        p.bump();
    }
    if p.at(TK_WITH) {
        let with = p.start();
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as with value"));
            p.recover(&[TK_ONLY, PC, MPC, TPC, LTS]);
        }
        p.complete(with, TWIG_INCLUDE_WITH);
    }
    if p.at(TK_ONLY) {
        p.bump();
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_INCLUDE)
}

fn parse_twig_extends(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(CLOSE_LTS);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_EXTENDS)
}

fn parse_twig_for(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();

    parse_twig_inline_comments(p);
    if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new("variable name"));
        p.recover(&[TK_COMMA, TK_IN, TK_ELSE, TK_ENDFOR, PC, MPC, TPC, LTS]);
    }
    if p.at(TK_COMMA) {
        p.bump();
        parse_twig_inline_comments(p);
        if parse_twig_name(p).is_none() {
            p.add_error(ErrorBuilder::new("variable name"));
            p.recover(&[TK_IN, TK_ELSE, TK_ENDFOR, PC, MPC, TPC, LTS]);
        }
    }
    p.expect(TK_IN, &[TK_ELSE, TK_ENDFOR, PC, MPC, TPC, LTS]);

    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(&[PC, MPC, TPC, TK_ELSE, TK_ENDFOR, LTS]);
    }
    p.expect_any(
        TWIG_BLOCK_CLOSE_SET,
        &[TK_ELSE, TK_ENDFOR, PC, MPC, TPC, LTS],
    );

    let starting = p.complete(outer, TWIG_FOR_BLOCK);
    let wrapper = p.precede(starting);

    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_tag(TK_ENDFOR) || p.at_twig_tag(TK_ELSE),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    if p.at_twig_tag(TK_ELSE) {
        let else_block = p.start();
        p.bump();
        p.bump();
        p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDFOR, PC, MPC, TPC, LTS]);
        p.complete(else_block, TWIG_FOR_ELSE_BLOCK);

        let body = p.start();
        parse_many(
            p,
            |p| p.at_twig_tag(TK_ENDFOR),
            |p| {
                child_parser(p);
            },
        );
        p.complete(body, BODY);
    }

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, &[TK_ENDFOR, PC, MPC, TPC, LTS]);
    p.expect(TK_ENDFOR, CLOSE_LTS);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, TWIG_ENDFOR_BLOCK);

    p.complete(wrapper, TWIG_FOR)
}

fn parse_twig_set(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();

    const DECLARATIONS_END: &[Kind] = &[TK_EQUAL, PC, MPC, TPC];
    let assignment = p.start();
    let mut declaration_count = 0usize;
    parse_many(
        p,
        |p| p.at_set(DECLARATIONS_END),
        |p| {
            if parse_twig_inline_comment(p).is_some() {
                return;
            }
            if parse_twig_name(p).is_some() {
                declaration_count += 1;
            } else {
                p.add_error(ErrorBuilder::new("twig variable name"));
            }
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at_set(DECLARATIONS_END) {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    if declaration_count == 0 {
        p.add_error(ErrorBuilder::new("twig variable name"));
    }

    let mut assigned_by_equal = false;
    if p.at(TK_EQUAL) {
        p.bump();
        assigned_by_equal = true;
        let mut assignment_count = 0usize;
        parse_many(
            p,
            |p| p.at_twig_block_close(),
            |p| {
                if parse_twig_expression(p).is_some() {
                    assignment_count += 1;
                } else {
                    p.add_error(ErrorBuilder::new("twig expression"));
                }
                if p.at(TK_COMMA) {
                    p.bump();
                } else if !p.at_twig_block_close() {
                    p.add_error(ErrorBuilder::new(","));
                }
            },
        );
        if declaration_count != assignment_count {
            p.add_error(ErrorBuilder::new(format!(
                "a total of {declaration_count} twig expressions (same amount as declarations) instead of {assignment_count}"
            )));
        }
    } else if declaration_count > 1 {
        p.add_error(ErrorBuilder::new(format!(
            "= followed by {declaration_count} twig expressions"
        )));
    }

    p.complete(assignment, TWIG_ASSIGNMENT);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDSET, PC, MPC, TPC, LTS]);

    let starting = p.complete(outer, TWIG_SET_BLOCK);
    let wrapper = p.precede(starting);
    if !assigned_by_equal {
        parse_body_and_ending(p, child_parser, TK_ENDSET, TWIG_ENDSET_BLOCK);
    }
    p.complete(wrapper, TWIG_SET)
}

fn parse_twig_block(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    let block_name = p
        .expect(TK_WORD, &[LTS, PC, MPC, TPC, TK_ENDBLOCK])
        .map(|token| token.text);

    let mut found_shortcut = false;
    if !p.at_twig_block_close() {
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression or '%}'"));
            p.recover(&[PC, MPC, TPC, TK_ENDBLOCK, LTS]);
        } else {
            found_shortcut = true;
        }
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[LTS, TK_ENDBLOCK, PC, MPC, TPC]);

    let starting = p.complete(outer, TWIG_STARTING_BLOCK);
    let wrapper = p.precede(starting);

    if !found_shortcut {
        let body = p.start();
        parse_many(
            p,
            |p| p.at_twig_tag(TK_ENDBLOCK),
            |p| {
                child_parser(p);
            },
        );
        p.complete(body, BODY);

        let ending = p.start();
        p.expect_any(TWIG_BLOCK_OPEN_SET, &[LTS, TK_ENDBLOCK, PC, MPC, TPC]);
        p.expect(TK_ENDBLOCK, &[LTS, PC, MPC, TPC]);
        parse_optional_end_name(p, block_name, "block");
        p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
        p.complete(ending, TWIG_ENDING_BLOCK);
    }

    p.complete(wrapper, TWIG_BLOCK)
}

fn parse_twig_if(p: &mut Parser<'_>, outer: Marker, child_parser: ParseFn) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression"));
        p.recover(&[PC, MPC, TPC, TK_ELSE, TK_ELSE_IF, TK_ENDIF, LTS]);
    }
    p.expect_any(
        TWIG_BLOCK_CLOSE_SET,
        &[TK_ELSE, TK_ELSE_IF, TK_ENDIF, PC, MPC, TPC, LTS],
    );

    let starting = p.complete(outer, TWIG_IF_BLOCK);
    let wrapper = p.precede(starting);

    loop {
        let body = p.start();
        parse_many(
            p,
            |p| p.at_twig_tag(TK_ENDIF) || p.at_twig_tag(TK_ELSE_IF) || p.at_twig_tag(TK_ELSE),
            |p| {
                child_parser(p);
            },
        );
        p.complete(body, BODY);

        if p.at_twig_tag(TK_ENDIF) {
            break;
        }
        if p.at_twig_tag(TK_ELSE_IF) {
            let branch = p.start();
            p.bump();
            p.bump();
            if parse_twig_expression(p).is_none() {
                p.add_error(ErrorBuilder::new("twig expression"));
                p.recover(&[PC, MPC, TPC, TK_ENDIF, LTS]);
            }
            p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDIF, PC, MPC, TPC, LTS]);
            p.complete(branch, TWIG_ELSE_IF_BLOCK);
        } else if p.at_twig_tag(TK_ELSE) {
            let branch = p.start();
            p.bump();
            p.bump();
            p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDIF, PC, MPC, TPC, LTS]);
            p.complete(branch, TWIG_ELSE_BLOCK);
        } else {
            break;
        }
    }

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, &[TK_ENDIF, PC, MPC, TPC, LTS]);
    p.expect(TK_ENDIF, CLOSE_LTS);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(ending, TWIG_ENDIF_BLOCK);

    p.complete(wrapper, TWIG_IF)
}

/// `{% props icon, type = 'primary' %}`
fn parse_twig_props(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    parse_many(
        p,
        |p| p.at_set(&[PC, MPC, TPC, TK_COMMA, TK_EQUAL, LTS]),
        |p| {
            let m = p.start();
            if parse_twig_name(p).is_none() {
                p.add_error(ErrorBuilder::new("twig variable name"));
            }
            if p.at(TK_EQUAL) {
                p.bump();
                if parse_twig_expression(p).is_none() {
                    p.add_error(ErrorBuilder::new("twig expression"));
                }
            }
            p.complete(m, TWIG_PROP_DECLARATION);
            if p.at(TK_COMMA) {
                p.bump();
            } else if !p.at_twig_block_close() {
                p.add_error(ErrorBuilder::new(","));
            }
        },
    );
    p.expect_any(TWIG_BLOCK_CLOSE_SET, AFTER_CLOSE);
    p.complete(outer, TWIG_PROPS)
}

/// `{% component Alert with {type: 'success'} %}...{% endcomponent %}`
fn parse_twig_component(
    p: &mut Parser<'_>,
    outer: Marker,
    child_parser: ParseFn,
) -> CompletedMarker {
    p.bump();
    if p.at_set(&[TK_SINGLE_QUOTES, TK_DOUBLE_QUOTES]) {
        parse_twig_string(p, false);
    } else if parse_twig_name(p).is_none() {
        p.add_error(ErrorBuilder::new("component name"));
    }
    if p.at(TK_WITH) {
        p.bump();
        if p.at(TK_OPEN_CURLY) {
            parse_twig_hash(p);
        } else {
            p.add_error(ErrorBuilder::new("twig hash/object"));
        }
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[TK_ENDCOMPONENT, PC, MPC, TPC, LTS]);
    let starting = p.complete(outer, TWIG_COMPONENT_STARTING_BLOCK);
    let wrapper = p.precede(starting);
    parse_body_and_ending(
        p,
        child_parser,
        TK_ENDCOMPONENT,
        TWIG_COMPONENT_ENDING_BLOCK,
    );
    p.complete(wrapper, TWIG_COMPONENT)
}
