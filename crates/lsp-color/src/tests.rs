use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{
    ColorPresentationRequest, Context, DocumentColorProvider, DocumentColorRequest, TextDocument,
};
use swlsp_lsp_protocol as protocol;

use super::{AdminScssColorProvider, parse_color_function, parse_hex_color};

fn document_colors(
    uri: &str,
    source: &str,
) -> (Arc<TextDocument>, Vec<protocol::ColorInformation>) {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let request = DocumentColorRequest {
        params: protocol::DocumentColorParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: document.clone(),
    };
    let colors = AdminScssColorProvider::new()
        .get_document_colors(&Context::background(), &request)
        .expect("document colors");
    (document, colors)
}

fn text_at(document: &TextDocument, range: protocol::Range) -> &str {
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character) as usize;
    let end = lines.offset_utf16(range.end.line, range.end.character) as usize;
    &document.source()[start..end]
}

fn color(red: f64, green: f64, blue: f64, alpha: f64) -> protocol::Color {
    protocol::Color {
        red,
        green,
        blue,
        alpha,
    }
}

#[track_caller]
fn assert_color_near(expected: protocol::Color, actual: protocol::Color, delta: f64, label: &str) {
    for (want, got) in [
        (expected.red, actual.red),
        (expected.green, actual.green),
        (expected.blue, actual.blue),
        (expected.alpha, actual.alpha),
    ] {
        assert!(
            (want - got).abs() <= delta,
            "{label}: {expected:?} != {actual:?}"
        );
    }
}

#[test]
fn admin_scss_document_colors() {
    let source = r##"// #badbad
$short: #0f8;
$short-alpha: #0f8c;
$long: #112233;
$long-alpha: #11223380;
$quoted: "#abcdef";

#fff {
    color: rgb(255, 0, 128);
    border-color: rgb(100% 0% 50% / 25%);
    background: hsl(120deg, 100%, 25%);
    box-shadow: 0 0 rgba(0, 0, 0, 8%);
    outline-color: hsla(-120, 100%, 50%, .5);
    dynamic: rgb(from var(--color) r g b / 10%);
    invalid: #12;
}
"##;
    let (document, colors) = document_colors("file:///workspace/component.scss", source);
    assert_eq!(colors.len(), 9, "{colors:#?}");

    let mut want: HashMap<&str, protocol::Color> = HashMap::from([
        ("#0f8", color(0.0, 1.0, 136.0 / 255.0, 1.0)),
        ("#0f8c", color(0.0, 1.0, 136.0 / 255.0, 204.0 / 255.0)),
        (
            "#112233",
            color(17.0 / 255.0, 34.0 / 255.0, 51.0 / 255.0, 1.0),
        ),
        (
            "#11223380",
            color(17.0 / 255.0, 34.0 / 255.0, 51.0 / 255.0, 128.0 / 255.0),
        ),
        ("rgb(255, 0, 128)", color(1.0, 0.0, 128.0 / 255.0, 1.0)),
        ("rgb(100% 0% 50% / 25%)", color(1.0, 0.0, 0.5, 0.25)),
        ("hsl(120deg, 100%, 25%)", color(0.0, 0.5, 0.0, 1.0)),
        ("rgba(0, 0, 0, 8%)", color(0.0, 0.0, 0.0, 0.08)),
        ("hsla(-120, 100%, 50%, .5)", color(0.0, 0.0, 1.0, 0.5)),
    ]);
    for information in &colors {
        let text = text_at(&document, information.range);
        let expected = want
            .remove(text)
            .unwrap_or_else(|| panic!("unexpected color range {text:?}"));
        assert_color_near(expected, information.color, 0.00001, text);
    }
    assert!(want.is_empty(), "{want:?}");
}

#[test]
fn admin_scss_color_ranges_use_utf16_positions() {
    let source = ".😀 { color: #abcdef; }";
    let (document, colors) = document_colors("file:///workspace/unicode.scss", source);
    assert_eq!(colors.len(), 1);
    assert_eq!(
        colors[0].range.start,
        protocol::Position {
            line: 0,
            character: 13
        }
    );
    assert_eq!(text_at(&document, colors[0].range), "#abcdef");
}

#[test]
fn admin_scss_color_presentations_preserve_alpha() {
    let uri = "file:///workspace/component.scss";
    let replacement = protocol::Range {
        start: protocol::Position {
            line: 0,
            character: 12,
        },
        end: protocol::Position {
            line: 0,
            character: 16,
        },
    };
    let request = ColorPresentationRequest {
        params: protocol::ColorPresentationParams {
            color: color(1.0, 0.0, 0.5, 0.25),
            range: replacement,
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, ".x { color: #000; }", 1)),
    };
    let presentations = AdminScssColorProvider::new()
        .get_color_presentations(&Context::background(), &request)
        .expect("presentations");
    let labels: Vec<&str> = presentations
        .iter()
        .map(|value| value.label.as_str())
        .collect();
    assert_eq!(
        labels,
        [
            "#ff008040",
            "rgba(255, 0, 128, 25%)",
            "hsla(330, 100%, 50%, 25%)"
        ]
    );
    for presentation in &presentations {
        let edit = presentation.text_edit.as_ref().expect("text edit");
        assert_eq!(edit.range, replacement);
        assert_eq!(edit.new_text, presentation.label);

        let parsed = match presentation.label.strip_prefix('#') {
            Some(hex) => parse_hex_color(hex),
            None => parse_color_function(&presentation.label),
        }
        .unwrap_or_else(|| panic!("unparsable {}", presentation.label));
        assert_color_near(
            color(1.0, 0.0, 0.5, 0.25),
            parsed,
            0.002,
            &presentation.label,
        );
    }
}

#[test]
fn admin_scss_color_presentations_for_opaque_colors() {
    let request = ColorPresentationRequest {
        params: protocol::ColorPresentationParams {
            color: color(0.0, 0.5, 0.0, 1.0),
            ..Default::default()
        },
        document: Arc::new(TextDocument::new("file:///workspace/a.scss", "", 1)),
    };
    let labels: Vec<String> = AdminScssColorProvider::new()
        .get_color_presentations(&Context::background(), &request)
        .expect("presentations")
        .into_iter()
        .map(|value| value.label)
        .collect();
    assert_eq!(labels, ["#008000", "rgb(0, 128, 0)", "hsl(120, 100%, 25%)"]);
}

#[test]
fn admin_scss_colors_ignore_other_languages_and_report_cancellation() {
    let (_, colors) = document_colors("file:///workspace/a.php", "<?php $a = '#fff';");
    assert!(colors.is_empty());

    let ctx = Context::background();
    ctx.token().cancel();
    let request = DocumentColorRequest {
        params: protocol::DocumentColorParams::default(),
        document: Arc::new(TextDocument::new("file:///a.scss", "a { color: #fff; }", 1)),
    };
    let error = AdminScssColorProvider::new()
        .get_document_colors(&ctx, &request)
        .expect_err("cancelled");
    assert!(swlsp_lsp::is_cancellation(error.as_ref()));
}
