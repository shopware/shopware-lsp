//! Shopware-specific `{% %}` tags.

use swlsp_syntax::{CompletedMarker, ErrorBuilder, Kind, Marker, Parser};

use super::expression::parse_twig_expression;
use super::literal::parse_twig_string;
use super::{ParseFn, TWIG_BLOCK_CLOSE_SET, TWIG_BLOCK_OPEN_SET, TwigParserExt, parse_many};
use crate::syntax::*;

const CLOSE: &[Kind] = &[
    TK_PERCENT_CURLY,
    TK_MINUS_PERCENT_CURLY,
    TK_TILDE_PERCENT_CURLY,
];

/// Dispatches Shopware tags after `{%`. Hands the untouched marker back when
/// nothing matched so the caller can complete it as `ERROR`.
pub(crate) fn parse_shopware_twig_block_statement(
    p: &mut Parser<'_>,
    outer: Marker,
    child_parser: ParseFn,
) -> Result<CompletedMarker, Marker> {
    Ok(match p.peek() {
        Some(TK_SW_EXTENDS) => parse_twig_sw_extends(p, outer),
        Some(TK_SW_INCLUDE) => parse_twig_sw_include(p, outer),
        Some(TK_SW_SILENT_FEATURE_CALL) => {
            parse_twig_sw_silent_feature_call(p, outer, child_parser)
        }
        Some(TK_RETURN) => parse_twig_sw_return(p, outer),
        Some(TK_SW_ICON) => parse_twig_sw_icon(p, outer),
        Some(TK_SW_THUMBNAILS) => parse_twig_sw_thumbnails(p, outer),
        _ => return Err(outer),
    })
}

fn parse_twig_sw_thumbnails(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    let with_recovery = &[
        TK_WITH,
        TK_PERCENT_CURLY,
        TK_MINUS_PERCENT_CURLY,
        TK_TILDE_PERCENT_CURLY,
    ];
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as thumbnail name"));
        p.recover(with_recovery);
    }
    if p.at(TK_WITH) {
        let m = p.start();
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as thumbnail variables"));
            p.recover(with_recovery);
        }
        p.complete(m, SHOPWARE_THUMBNAILS_WITH);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, SHOPWARE_THUMBNAILS)
}

/// `{% sw_icon 'name' [style] {'pack': 'custom'} %}`
fn parse_twig_sw_icon(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as icon name"));
        p.recover(&[
            TK_STYLE,
            TK_OPEN_CURLY,
            TK_PERCENT_CURLY,
            TK_MINUS_PERCENT_CURLY,
            TK_TILDE_PERCENT_CURLY,
        ]);
    }
    if p.at(TK_STYLE) || p.at(TK_OPEN_CURLY) {
        let m = p.start();
        if p.at(TK_STYLE) {
            p.bump();
        }
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as icon style variables"));
            p.recover(CLOSE);
        }
        p.complete(m, SHOPWARE_ICON_STYLE);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, SHOPWARE_ICON)
}

fn parse_twig_sw_return(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    parse_twig_expression(p);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, SHOPWARE_RETURN)
}

fn parse_twig_sw_silent_feature_call(
    p: &mut Parser<'_>,
    outer: Marker,
    child_parser: ParseFn,
) -> CompletedMarker {
    p.bump();
    let end_recovery = &[
        TK_ENDSW_SILENT_FEATURE_CALL,
        TK_PERCENT_CURLY,
        TK_MINUS_PERCENT_CURLY,
        TK_TILDE_PERCENT_CURLY,
    ];
    if p.at_set(&[TK_DOUBLE_QUOTES, TK_SINGLE_QUOTES]) {
        parse_twig_string(p, false);
    } else {
        p.add_error(ErrorBuilder::new(
            "twig string as feature flag (shopware doesn't allow expressions here)",
        ));
        p.recover(end_recovery);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, end_recovery);
    let starting = p.complete(outer, SHOPWARE_SILENT_FEATURE_CALL_STARTING_BLOCK);
    let wrapper = p.precede(starting);

    let body = p.start();
    parse_many(
        p,
        |p| p.at_twig_tag(TK_ENDSW_SILENT_FEATURE_CALL),
        |p| {
            child_parser(p);
        },
    );
    p.complete(body, BODY);

    let ending = p.start();
    p.expect_any(TWIG_BLOCK_OPEN_SET, end_recovery);
    p.expect(TK_ENDSW_SILENT_FEATURE_CALL, CLOSE);
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(ending, SHOPWARE_SILENT_FEATURE_CALL_ENDING_BLOCK);

    p.complete(wrapper, SHOPWARE_SILENT_FEATURE_CALL)
}

/// `{% sw_extends 'x' %}` or `{% sw_extends { template: 'x', scopes: [...] } %}`.
fn parse_twig_sw_extends(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template"));
        p.recover(CLOSE);
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, SHOPWARE_TWIG_SW_EXTENDS)
}

fn parse_twig_sw_include(p: &mut Parser<'_>, outer: Marker) -> CompletedMarker {
    p.bump();
    if parse_twig_expression(p).is_none() {
        p.add_error(ErrorBuilder::new("twig expression as template name"));
        p.recover(&[
            TK_IGNORE_MISSING,
            TK_WITH,
            TK_ONLY,
            TK_PERCENT_CURLY,
            TK_MINUS_PERCENT_CURLY,
            TK_TILDE_PERCENT_CURLY,
        ]);
    }
    if p.at(TK_IGNORE_MISSING) {
        p.bump();
    }
    if p.at(TK_WITH) {
        let m = p.start();
        p.bump();
        if parse_twig_expression(p).is_none() {
            p.add_error(ErrorBuilder::new("twig expression as with value"));
            p.recover(&[
                TK_ONLY,
                TK_PERCENT_CURLY,
                TK_MINUS_PERCENT_CURLY,
                TK_TILDE_PERCENT_CURLY,
            ]);
        }
        p.complete(m, TWIG_INCLUDE_WITH);
    }
    if p.at(TK_ONLY) {
        p.bump();
    }
    p.expect_any(TWIG_BLOCK_CLOSE_SET, &[]);
    p.complete(outer, SHOPWARE_TWIG_SW_INCLUDE)
}
