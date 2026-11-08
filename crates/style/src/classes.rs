use std::collections::HashSet;

use swlsp_parser_scss::syntax::{
    SCSS_BLOCK, SCSS_RULE, TK_CLOSE_BRACE, TK_CLOSE_BRACKET, TK_CLOSE_PAREN, TK_COMMA,
    TK_IDENTIFIER, TK_INTERPOLATION_OPEN, TK_OPEN_BRACKET, TK_OPEN_PAREN,
};
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute};
use swlsp_parser_twig::query::html_attribute_name;
use swlsp_parser_twig::syntax::HTML_ATTRIBUTE;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxToken};

use crate::model::{ByteRange, ClassOccurrence, ClassOccurrenceKind};

fn lower_extension(path: &str) -> String {
    swlsp_uriutil::filepath::ext(path).to_lowercase()
}

/// Statically named class selectors of SCSS trees. Vue SFC trees are
/// supported because their style sections embed SCSS nodes.
pub fn class_declarations(path: &str, root: &SyntaxNode) -> Vec<ClassOccurrence> {
    let extension = lower_extension(path);
    if extension != ".scss" && extension != ".vue" {
        return Vec::new();
    }
    let mut result = Vec::new();
    walk_scss_rules(path, root, &[], &mut result);
    sort_occurrences(&mut result);
    result
}

fn walk_scss_rules(
    path: &str,
    node: &SyntaxNode,
    parent_classes: &[String],
    result: &mut Vec<ClassOccurrence>,
) {
    for child in node.children() {
        if child.kind() != SCSS_RULE {
            walk_scss_rules(path, &child, parent_classes, result);
            continue;
        }
        let Some((declarations, contexts, block)) = classes_in_rule(path, &child, parent_classes)
        else {
            continue;
        };
        result.extend(declarations);
        walk_scss_rules(path, &block, &contexts, result);
    }
}

fn classes_in_rule(
    path: &str,
    rule: &SyntaxNode,
    parent_classes: &[String],
) -> Option<(Vec<ClassOccurrence>, Vec<String>, SyntaxNode)> {
    let block = rule.children().find(|child| child.kind() == SCSS_BLOCK)?;
    let block_start = block.text_range().start();
    let tokens: Vec<SyntaxToken> = rule
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .filter(|token| token.text_range().start() < block_start && !token.kind().is_trivia())
        .collect();

    let mut declarations = Vec::new();
    let mut contexts = Vec::new();
    let mut segment_start = 0;
    let (mut paren_depth, mut bracket_depth, mut interpolation_depth) = (0usize, 0usize, 0usize);
    for (position, token) in tokens.iter().enumerate() {
        match token.kind() {
            TK_OPEN_PAREN => paren_depth += 1,
            TK_CLOSE_PAREN => paren_depth = paren_depth.saturating_sub(1),
            TK_OPEN_BRACKET => bracket_depth += 1,
            TK_CLOSE_BRACKET => bracket_depth = bracket_depth.saturating_sub(1),
            TK_INTERPOLATION_OPEN => interpolation_depth += 1,
            TK_CLOSE_BRACE => interpolation_depth = interpolation_depth.saturating_sub(1),
            TK_COMMA if paren_depth == 0 && bracket_depth == 0 && interpolation_depth == 0 => {
                let (current, context) = classes_in_selector_segment(
                    path,
                    &tokens[segment_start..position],
                    parent_classes,
                );
                declarations.extend(current);
                contexts.extend(context);
                segment_start = position + 1;
            }
            _ => {}
        }
    }
    let (current, context) =
        classes_in_selector_segment(path, &tokens[segment_start..], parent_classes);
    declarations.extend(current);
    contexts.extend(context);
    Some((
        unique_occurrences(declarations),
        unique_strings(contexts),
        block,
    ))
}

fn start(token: &SyntaxToken) -> u32 {
    token.text_range().start().into()
}

fn end(token: &SyntaxToken) -> u32 {
    token.text_range().end().into()
}

fn classes_in_selector_segment(
    path: &str,
    tokens: &[SyntaxToken],
    parent_classes: &[String],
) -> (Vec<ClassOccurrence>, Vec<String>) {
    let mut declarations = Vec::new();
    let mut direct_contexts = Vec::new();
    let mut expanded_contexts = Vec::new();
    let mut uses_parent = false;
    let mut position = 0;
    while position < tokens.len() {
        let token = &tokens[position];
        if token.text() == "." && position + 1 < tokens.len() {
            let identifier = &tokens[position + 1];
            if identifier.kind() != TK_IDENTIFIER
                || end(token) != start(identifier)
                || has_adjacent_interpolation(tokens, position + 1)
            {
                position += 1;
                continue;
            }
            let name = decode_css_identifier(identifier.text());
            if name.is_empty() {
                position += 1;
                continue;
            }
            declarations.push(ClassOccurrence {
                name: name.clone(),
                file: path.to_owned(),
                range: identifier.text_range().into(),
                kind: ClassOccurrenceKind::Declaration,
                ..ClassOccurrence::default()
            });
            direct_contexts = vec![name];
            position += 2;
            continue;
        }
        if token.text() != "&" {
            position += 1;
            continue;
        }
        uses_parent = true;
        if position + 1 >= tokens.len() {
            position += 1;
            continue;
        }
        let suffix = &tokens[position + 1];
        if suffix.kind() != TK_IDENTIFIER
            || end(token) != start(suffix)
            || has_adjacent_interpolation(tokens, position + 1)
        {
            position += 1;
            continue;
        }
        let decoded_suffix = decode_css_identifier(suffix.text());
        if decoded_suffix.is_empty() {
            position += 1;
            continue;
        }
        for parent in parent_classes {
            let name = format!("{parent}{decoded_suffix}");
            expanded_contexts.push(name.clone());
            declarations.push(ClassOccurrence {
                name,
                file: path.to_owned(),
                range: ByteRange::new(start(token), end(suffix)),
                kind: ClassOccurrenceKind::Declaration,
                ..ClassOccurrence::default()
            });
        }
        position += 2;
    }
    if !direct_contexts.is_empty() {
        return (declarations, direct_contexts);
    }
    if !expanded_contexts.is_empty() {
        return (declarations, expanded_contexts);
    }
    if uses_parent {
        return (declarations, parent_classes.to_vec());
    }
    (declarations, Vec::new())
}

fn has_adjacent_interpolation(tokens: &[SyntaxToken], identifier_position: usize) -> bool {
    identifier_position + 1 < tokens.len()
        && end(&tokens[identifier_position]) == start(&tokens[identifier_position + 1])
        && tokens[identifier_position + 1].kind() == TK_INTERPOLATION_OPEN
}

/// Static class attribute values of Twig-compatible markup. Vue SFC template
/// sections embed the same Twig nodes.
pub fn class_usages(path: &str, root: &SyntaxNode) -> Vec<ClassOccurrence> {
    let extension = lower_extension(path);
    if extension != ".twig" && extension != ".html" && extension != ".vue" {
        return Vec::new();
    }
    let mut result = Vec::new();
    for node in root.descendants() {
        if node.kind() != HTML_ATTRIBUTE
            || !html_attribute_name(&node).is_some_and(|name| equal_fold_class(&name))
        {
            continue;
        }
        let Some(inner) = HtmlAttribute::cast(node)
            .and_then(|attribute| attribute.value())
            .and_then(|value| value.inner())
        else {
            continue;
        };
        result.extend(static_class_segments(path, inner.syntax()));
    }
    sort_occurrences(&mut result);
    unique_occurrences(result)
}

/// Go `strings.EqualFold(name, "class")`: `ſ` (U+017F) folds to `s`.
fn equal_fold_class(name: &str) -> bool {
    let mut chars = name.chars();
    for expected in "class".chars() {
        let Some(actual) = chars.next() else {
            return false;
        };
        if actual.to_ascii_lowercase() != expected && !(actual == '\u{17f}' && expected == 's') {
            return false;
        }
    }
    chars.next().is_none()
}

fn static_class_segments(path: &str, inner: &SyntaxNode) -> Vec<ClassOccurrence> {
    struct Segment {
        text: String,
        start: u32,
        end: u32,
        open: bool,
        leading_dynamic: bool,
    }
    let mut result = Vec::new();
    let mut segment = Segment {
        text: String::new(),
        start: 0,
        end: 0,
        open: false,
        leading_dynamic: false,
    };
    let mut previous_dynamic_end = 0u32;
    let mut previous_dynamic_touches_right = false;

    let flush =
        |segment: &mut Segment, result: &mut Vec<ClassOccurrence>, trailing_dynamic: bool| {
            if !segment.open {
                return;
            }
            result.extend(class_words(
                path,
                &segment.text,
                segment.start,
                segment.leading_dynamic,
                trailing_dynamic,
            ));
            segment.text.clear();
            segment.open = false;
            segment.leading_dynamic = false;
        };

    for child in inner.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) => {
                if !segment.open {
                    segment.start = start(&token);
                    segment.leading_dynamic =
                        previous_dynamic_end == segment.start && previous_dynamic_touches_right;
                    segment.open = true;
                }
                segment.text.push_str(token.text());
                segment.end = end(&token);
            }
            NodeOrToken::Node(node) => {
                let dynamic_text = node.text().to_string();
                let range = node.text_range();
                let touches_left = dynamic_text
                    .bytes()
                    .next()
                    .is_some_and(|byte| !is_class_space(byte));
                let trailing =
                    segment.open && segment.end == u32::from(range.start()) && touches_left;
                flush(&mut segment, &mut result, trailing);
                previous_dynamic_end = range.end().into();
                previous_dynamic_touches_right = dynamic_text
                    .bytes()
                    .last()
                    .is_some_and(|byte| !is_class_space(byte));
            }
        }
    }
    flush(&mut segment, &mut result, false);
    result
}

fn class_words(
    path: &str,
    value: &str,
    base: u32,
    leading_dynamic: bool,
    trailing_dynamic: bool,
) -> Vec<ClassOccurrence> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut position = 0;
    while position < bytes.len() {
        while position < bytes.len() && is_class_space(bytes[position]) {
            position += 1;
        }
        let word_start = position;
        while position < bytes.len() && !is_class_space(bytes[position]) {
            position += 1;
        }
        if word_start == position
            || (leading_dynamic && word_start == 0)
            || (trailing_dynamic && position == bytes.len())
        {
            continue;
        }
        let name = &value[word_start..position];
        if name.contains(['{', '}']) {
            continue;
        }
        result.push(ClassOccurrence {
            name: name.to_owned(),
            file: path.to_owned(),
            range: ByteRange::new(base + word_start as u32, base + position as u32),
            kind: ClassOccurrenceKind::Usage,
            ..ClassOccurrence::default()
        });
    }
    result
}

/// The class declaration or usage at `offset` (inclusive of the end).
pub fn class_at(path: &str, root: &SyntaxNode, offset: u32) -> Option<ClassOccurrence> {
    class_declarations(path, root)
        .into_iter()
        .chain(class_usages(path, root))
        .find(|occurrence| occurrence.range.contains(offset) || offset == occurrence.range.end)
}

fn is_class_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n' | 0x0c)
}

/// Decodes CSS escapes (`\:`, `\31 `) in an identifier.
fn decode_css_identifier(value: &str) -> String {
    if !value.contains('\\') {
        return value.to_owned();
    }
    let bytes = value.as_bytes();
    let mut result: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut position = 0;
    while position < bytes.len() {
        if bytes[position] != b'\\' {
            result.push(bytes[position]);
            position += 1;
            continue;
        }
        position += 1;
        if position >= bytes.len() {
            break;
        }
        let digits_start = position;
        let mut codepoint = 0u32;
        while position < bytes.len() && position - digits_start < 6 {
            let Some(digit) = (bytes[position] as char).to_digit(16) else {
                break;
            };
            codepoint = codepoint * 16 + digit;
            position += 1;
        }
        if position != digits_start {
            if position < bytes.len() && is_class_space(bytes[position]) {
                position += 1;
            }
            let character = char::from_u32(codepoint)
                .filter(|_| codepoint != 0)
                .unwrap_or('\u{fffd}');
            result.extend_from_slice(character.encode_utf8(&mut [0; 4]).as_bytes());
            continue;
        }
        result.push(bytes[position]);
        position += 1;
    }
    String::from_utf8_lossy(&result).into_owned()
}

fn unique_occurrences(values: Vec<ClassOccurrence>) -> Vec<ClassOccurrence> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| seen.insert((value.name.clone(), value.range)))
        .collect()
}

fn unique_strings(values: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| !value.is_empty() && seen.insert(value.clone()))
        .collect()
}

fn sort_occurrences(values: &mut [ClassOccurrence]) {
    values.sort_by(|left, right| {
        (left.range.start, left.range.end, &left.name).cmp(&(
            right.range.start,
            right.range.end,
            &right.name,
        ))
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn class_declarations_include_nested_bem_and_escaped_names() {
        let source = r".sw-card, .utility\:visible {
    &__title, &--active {
        color: red;
    }
    @media (min-width: 40rem) {
        &__wide { display: block; }
    }
    .child { display: block; }
    .button-#{$variant} { color: blue; }
}";
        let parsed = swlsp_parser_scss::parse(source);
        assert!(parsed.errors.is_empty());
        let declarations = class_declarations("component.scss", &parsed.root());
        let mut names: Vec<&str> = declarations
            .iter()
            .map(|declaration| declaration.name.as_str())
            .collect();
        for declaration in &declarations {
            assert_eq!(declaration.kind, ClassOccurrenceKind::Declaration);
            assert!(!declaration.range.is_empty());
        }
        names.sort_unstable();
        let mut expected = vec![
            "sw-card",
            "utility:visible",
            "sw-card__title",
            "utility:visible__title",
            "sw-card--active",
            "utility:visible--active",
            "sw-card__wide",
            "utility:visible__wide",
            "child",
        ];
        expected.sort_unstable();
        assert_eq!(names, expected);
    }

    #[test]
    fn class_usages_keep_static_twig_attribute_segments() {
        let source = r#"<div class="sw-card {{ active ? 'is-active' : '' }} sw-card__title foo-{{ suffix }} {{ prefix }}-bar"></div>"#;
        let parsed = swlsp_parser_twig::parse(source);
        assert!(parsed.errors.is_empty());
        let usages = class_usages("page.html.twig", &parsed.root());
        assert_eq!(usages.len(), 2);
        assert_eq!(usages[0].name, "sw-card");
        assert_eq!(
            &source[usages[0].range.start as usize..usages[0].range.end as usize],
            "sw-card"
        );
        assert_eq!(usages[1].name, "sw-card__title");
        assert_eq!(
            &source[usages[1].range.start as usize..usages[1].range.end as usize],
            "sw-card__title"
        );
    }

    #[test]
    fn decodes_css_escapes() {
        assert_eq!(decode_css_identifier(r"a\:b"), "a:b");
        assert_eq!(decode_css_identifier(r"\31 0"), "10");
        assert_eq!(decode_css_identifier(r"\0"), "\u{fffd}");
        assert_eq!(decode_css_identifier("x\\"), "x");
    }

    #[test]
    fn class_at_includes_range_end() {
        let source = ".card { color: red; }";
        let parsed = swlsp_parser_scss::parse(source);
        let found = class_at("a.scss", &parsed.root(), 5).unwrap();
        assert_eq!(found.name, "card");
        assert!(class_at("a.scss", &parsed.root(), 7).is_none());
    }
}
