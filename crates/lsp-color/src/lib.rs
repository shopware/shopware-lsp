//! Document color provider for SCSS.
//!
//! Supplies editor swatches and color-picker edits for statically known SCSS
//! colors. Dynamic Sass expressions are intentionally omitted because reporting
//! a guessed color is worse than reporting none.

use swlsp_language::LanguageId;
use swlsp_lsp::{
    ColorPresentationRequest, Context, DocumentColorProvider, DocumentColorRequest, ProviderResult,
    protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_scss::{query, syntax as scss};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxToken, TextRange};

#[derive(Debug, Default, Clone, Copy)]
pub struct AdminScssColorProvider;

impl AdminScssColorProvider {
    pub fn new() -> Self {
        Self
    }
}

fn is_scss_document(language: Option<LanguageId>) -> bool {
    matches!(language, Some(LanguageId::Scss | LanguageId::Vue))
}

impl DocumentColorProvider for AdminScssColorProvider {
    fn get_document_colors(
        &self,
        ctx: &Context,
        request: &DocumentColorRequest,
    ) -> ProviderResult<Vec<protocol::ColorInformation>> {
        ctx.check()?;
        let document = &request.document;
        if !is_scss_document(document.language) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let lines = document.line_index();
        let mut result = Vec::new();
        for element in root.descendants_with_tokens() {
            ctx.check()?;
            let NodeOrToken::Token(token) = element else {
                continue;
            };
            if token.kind() != scss::TK_HASH || !in_color_value(token.parent()) {
                continue;
            }
            let Some(next) = token
                .next_sibling_or_token()
                .and_then(NodeOrToken::into_token)
            else {
                continue;
            };
            if next.text_range().start() != token.text_range().end()
                || !matches!(next.kind(), scss::TK_IDENTIFIER | scss::TK_NUMBER)
            {
                continue;
            }
            let Some(color) = parse_hex_color(next.text()) else {
                continue;
            };
            result.push(protocol::ColorInformation {
                range: protocol_range(
                    lines,
                    TextRange::new(token.text_range().start(), next.text_range().end()),
                ),
                color,
            });
        }
        for call in query::nodes(&root, &[scss::SCSS_FUNCTION_CALL]) {
            ctx.check()?;
            if !in_color_value(call.parent()) {
                continue;
            }
            let Some((range, text)) = color_function_text(&call, document.source()) else {
                continue;
            };
            let Some(color) = parse_color_function(text) else {
                continue;
            };
            result.push(protocol::ColorInformation {
                range: protocol_range(lines, range),
                color,
            });
        }
        Ok(result)
    }

    fn get_color_presentations(
        &self,
        ctx: &Context,
        request: &ColorPresentationRequest,
    ) -> ProviderResult<Vec<protocol::ColorPresentation>> {
        ctx.check()?;
        if !is_scss_document(request.document.language) {
            return Ok(Vec::new());
        }
        let requested = request.params.color;
        let color = protocol::Color {
            red: clamp(requested.red),
            green: clamp(requested.green),
            blue: clamp(requested.blue),
            alpha: clamp(requested.alpha),
        };
        Ok(presentation_labels(color)
            .into_iter()
            .map(|label| protocol::ColorPresentation {
                text_edit: Some(protocol::TextEdit::new(request.params.range, label.clone())),
                label,
                ..Default::default()
            })
            .collect())
    }
}

/// Hex, `rgb()` and `hsl()` spellings (with alpha variants when the color is
/// not opaque).
fn presentation_labels(color: protocol::Color) -> [String; 3] {
    let red = channel_byte(color.red);
    let green = channel_byte(color.green);
    let blue = channel_byte(color.blue);
    let opaque = (color.alpha - 1.0).abs() < 0.0005;
    let mut hex = format!("#{red:02x}{green:02x}{blue:02x}");
    if !opaque {
        hex.push_str(&format!("{:02x}", channel_byte(color.alpha)));
    }
    let alpha = format_number(color.alpha * 100.0);
    let rgb = if opaque {
        format!("rgb({red}, {green}, {blue})")
    } else {
        format!("rgba({red}, {green}, {blue}, {alpha}%)")
    };
    let (hue, saturation, lightness) = rgb_to_hsl(color.red, color.green, color.blue);
    let (hue, saturation, lightness) = (
        format_number(hue),
        format_number(saturation * 100.0),
        format_number(lightness * 100.0),
    );
    let hsl = if opaque {
        format!("hsl({hue}, {saturation}%, {lightness}%)")
    } else {
        format!("hsla({hue}, {saturation}%, {lightness}%, {alpha}%)")
    };
    [hex, rgb, hsl]
}

fn channel_byte(value: f64) -> i64 {
    (value * 255.0).round() as i64
}

fn in_color_value(node: Option<SyntaxNode>) -> bool {
    for current in node.iter().flat_map(SyntaxNode::ancestors) {
        match current.kind() {
            scss::SCSS_DECLARATION | scss::SCSS_VARIABLE_DECLARATION => return true,
            scss::SCSS_RULE | scss::SCSS_STYLESHEET => return false,
            _ => {}
        }
    }
    false
}

/// The source text from the function name through the last closing paren.
fn color_function_text<'s>(node: &SyntaxNode, source: &'s str) -> Option<(TextRange, &'s str)> {
    let mut name: Option<SyntaxToken> = None;
    let mut close_paren: Option<SyntaxToken> = None;
    for element in node.descendants_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        if name.is_none() && token.kind() == scss::TK_IDENTIFIER {
            name = Some(token.clone());
        }
        if token.kind() == scss::TK_CLOSE_PAREN {
            close_paren = Some(token);
        }
    }
    let (name, close_paren) = (name?, close_paren?);
    let start = name.text_range().start();
    let end = close_paren.text_range().end();
    if end <= start || usize::from(end) > source.len() {
        return None;
    }
    let range = TextRange::new(start, end);
    Some((range, source.get(range.start().into()..range.end().into())?))
}

fn parse_hex_color(value: &str) -> Option<protocol::Color> {
    if !matches!(value.len(), 3 | 4 | 6 | 8) || !value.bytes().all(|byte| byte.is_ascii_hexdigit())
    {
        return None;
    }
    let expanded: String = if value.len() <= 4 {
        value
            .chars()
            .flat_map(|character| [character, character])
            .collect()
    } else {
        value.to_owned()
    };
    let mut channels = [0u8, 0, 0, 255];
    for (index, channel) in channels.iter_mut().enumerate().take(expanded.len() / 2) {
        *channel = u8::from_str_radix(&expanded[index * 2..index * 2 + 2], 16).ok()?;
    }
    Some(protocol::Color {
        red: f64::from(channels[0]) / 255.0,
        green: f64::from(channels[1]) / 255.0,
        blue: f64::from(channels[2]) / 255.0,
        alpha: f64::from(channels[3]) / 255.0,
    })
}

fn parse_color_function(text: &str) -> Option<protocol::Color> {
    let open = text.find('(')?;
    if open == 0 || !text.trim().ends_with(')') {
        return None;
    }
    let name = text[..open].trim().to_lowercase();
    let close = text.rfind(')')?;
    let body = text.get(open + 1..close)?.trim();
    if body.is_empty() || body.contains(['(', ')', '$', '{', '}']) {
        return None;
    }
    let (parts, alpha) = color_function_parts(body)?;
    let [first, second, third] = parts.as_slice() else {
        return None;
    };
    match name.as_str() {
        "rgb" | "rgba" => Some(protocol::Color {
            red: parse_rgb_channel(first)?,
            green: parse_rgb_channel(second)?,
            blue: parse_rgb_channel(third)?,
            alpha,
        }),
        "hsl" | "hsla" => {
            let hue = parse_hue(first)?;
            let saturation = parse_percentage(second)?;
            let lightness = parse_percentage(third)?;
            let (red, green, blue) = hsl_to_rgb(hue, saturation, lightness);
            Some(protocol::Color {
                red,
                green,
                blue,
                alpha,
            })
        }
        _ => None,
    }
}

/// Splits the legacy comma syntax or the modern space syntax with an
/// optional `/ alpha` into three channels plus alpha.
fn color_function_parts(body: &str) -> Option<(Vec<&str>, f64)> {
    let mut alpha = 1.0;
    if body.contains(',') {
        let mut parts: Vec<&str> = body.split(',').map(str::trim).collect();
        if parts.len() == 4 {
            alpha = parse_alpha(parts[3])?;
            parts.truncate(3);
        }
        return (parts.len() == 3).then_some((parts, alpha));
    }
    let mut fields: Vec<&str> = Vec::new();
    for field in body.split_whitespace() {
        // Split on whitespace and around every `/`, keeping `/` as a field.
        let mut rest = field;
        while let Some(index) = rest.find('/') {
            if index > 0 {
                fields.push(&rest[..index]);
            }
            fields.push("/");
            rest = &rest[index + 1..];
        }
        if !rest.is_empty() {
            fields.push(rest);
        }
    }
    if fields.len() == 5 && fields[3] == "/" {
        alpha = parse_alpha(fields[4])?;
        fields.truncate(3);
    }
    (fields.len() == 3).then_some((fields, alpha))
}

fn parse_rgb_channel(value: &str) -> Option<f64> {
    if value.ends_with('%') {
        return parse_percentage(value);
    }
    Some(parse_bounded(value.trim(), 0.0, 255.0)? / 255.0)
}

fn parse_percentage(value: &str) -> Option<f64> {
    let number = value.trim().strip_suffix('%')?;
    Some(parse_bounded(number.trim(), 0.0, 100.0)? / 100.0)
}

fn parse_alpha(value: &str) -> Option<f64> {
    if value.trim().ends_with('%') {
        return parse_percentage(value);
    }
    parse_bounded(value.trim(), 0.0, 1.0)
}

fn parse_hue(value: &str) -> Option<f64> {
    let value = value.trim().to_lowercase();
    let (number, multiplier) = if let Some(number) = value.strip_suffix("turn") {
        (number, 360.0)
    } else if let Some(number) = value.strip_suffix("grad") {
        (number, 0.9)
    } else if let Some(number) = value.strip_suffix("rad") {
        (number, 180.0 / std::f64::consts::PI)
    } else if let Some(number) = value.strip_suffix("deg") {
        (number, 1.0)
    } else {
        (value.as_str(), 1.0)
    };
    let parsed = parse_float(number.trim())?;
    let mut hue = (parsed * multiplier) % 360.0;
    if hue < 0.0 {
        hue += 360.0;
    }
    Some(hue)
}

fn parse_bounded(value: &str, minimum: f64, maximum: f64) -> Option<f64> {
    parse_float(value).filter(|parsed| (minimum..=maximum).contains(parsed))
}

/// A finite decimal float (no NaN or infinity).
fn parse_float(value: &str) -> Option<f64> {
    value
        .parse::<f64>()
        .ok()
        .filter(|parsed| parsed.is_finite())
}

fn hsl_to_rgb(hue: f64, saturation: f64, lightness: f64) -> (f64, f64, f64) {
    let chroma = (1.0 - (2.0 * lightness - 1.0).abs()) * saturation;
    let x = chroma * (1.0 - ((hue / 60.0) % 2.0 - 1.0).abs());
    let (red, green, blue) = if hue < 60.0 {
        (chroma, x, 0.0)
    } else if hue < 120.0 {
        (x, chroma, 0.0)
    } else if hue < 180.0 {
        (0.0, chroma, x)
    } else if hue < 240.0 {
        (0.0, x, chroma)
    } else if hue < 300.0 {
        (x, 0.0, chroma)
    } else {
        (chroma, 0.0, x)
    };
    let shift = lightness - chroma / 2.0;
    (red + shift, green + shift, blue + shift)
}

#[allow(clippy::float_cmp)]
fn rgb_to_hsl(red: f64, green: f64, blue: f64) -> (f64, f64, f64) {
    let maximum = red.max(green.max(blue));
    let minimum = red.min(green.min(blue));
    let delta = maximum - minimum;
    let lightness = (maximum + minimum) / 2.0;
    if delta == 0.0 {
        return (0.0, 0.0, lightness);
    }
    let saturation = delta / (1.0 - (2.0 * lightness - 1.0).abs());
    let mut hue = if maximum == red {
        60.0 * (((green - blue) / delta) % 6.0)
    } else if maximum == green {
        60.0 * ((blue - red) / delta + 2.0)
    } else {
        60.0 * ((red - green) / delta + 4.0)
    };
    if hue < 0.0 {
        hue += 360.0;
    }
    (hue, saturation, lightness)
}

/// One decimal place without a trailing `.0`.
fn format_number(value: f64) -> String {
    let value = if value.abs() < 0.0005 { 0.0 } else { value };
    let formatted = format!("{value:.1}");
    match formatted.strip_suffix(".0") {
        Some(trimmed) => trimmed.to_owned(),
        None => formatted,
    }
}

fn clamp(value: f64) -> f64 {
    value.clamp(0.0, 1.0)
}

#[cfg(test)]
mod tests;
