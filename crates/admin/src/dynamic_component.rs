//! Static `<component :is>` selectors.

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueDynamicComponentCandidate {
    pub name: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueDynamicComponentSelector {
    pub attribute_name: String,
    pub expression: String,
    pub expression_range: TextRange,
    pub candidates: Vec<VueDynamicComponentCandidate>,
    pub complete: bool,
}

use twigast::AstNode;

/// Recognizes static `is` attributes and bound `:is` / `v-bind:is`
/// selectors on Vue's built-in `<component>` element.
pub fn twig_dynamic_component_selector(
    start_tag: &SyntaxNode,
) -> Option<VueDynamicComponentSelector> {
    if twigquery::html_tag_name(start_tag).as_deref() != Some("component") {
        return None;
    }
    let tag = twigast::HtmlStartingTag::cast(start_tag.clone())?;
    for attribute in tag.attributes() {
        let attribute_name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        if attribute_name != "is" && attribute_name != ":is" && attribute_name != "v-bind:is" {
            continue;
        }
        let Some(value) = attribute.value() else {
            return Some(VueDynamicComponentSelector {
                attribute_name,
                ..Default::default()
            });
        };
        let Some(inner) = value.inner() else {
            return Some(VueDynamicComponentSelector {
                attribute_name,
                ..Default::default()
            });
        };
        let range_value = inner.syntax().range_trimmed_trivia();
        let expression = inner.syntax().text().to_string().trim().to_owned();
        let mut selector = VueDynamicComponentSelector {
            attribute_name: attribute_name.clone(),
            expression: expression.clone(),
            expression_range: range_value,
            ..Default::default()
        };
        if expression.is_empty() {
            return Some(selector);
        }
        if attribute_name == "is" {
            selector.candidates = vec![VueDynamicComponentCandidate {
                name: expression,
                range: range_value,
            }];
            selector.complete = true;
            return Some(selector);
        }
        let (candidates, complete) =
            static_vue_dynamic_component_candidates(&expression, u32::from(range_value.start()));
        selector.candidates = candidates;
        selector.complete = complete;
        return Some(selector);
    }
    None
}

/// Resolves a normal component tag or a dynamic component selector whose
/// complete candidate set contains one name.
pub fn static_component_name_for_tag(start_tag: &SyntaxNode) -> Option<String> {
    let name = twigquery::html_tag_name(start_tag).unwrap_or_default();
    if is_component_tag(&name) {
        return Some(name);
    }
    let selector = twig_dynamic_component_selector(start_tag)?;
    if !selector.complete {
        return None;
    }
    let mut names = selector.names();
    if names.len() != 1 || !is_component_tag(&names[0]) {
        return None;
    }
    names.pop()
}

impl VueDynamicComponentSelector {
    /// The static selector candidate under `offset`.
    pub fn candidate_at(&self, offset: u32) -> Option<&VueDynamicComponentCandidate> {
        self.candidates.iter().find(|candidate| {
            offset >= u32::from(candidate.range.start())
                && offset < u32::from(candidate.range.end())
        })
    }

    /// Selector candidates once each in source order.
    pub fn names(&self) -> Vec<String> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for candidate in &self.candidates {
            if candidate.name.is_empty() || !seen.insert(candidate.name.as_str()) {
                continue;
            }
            result.push(candidate.name.clone());
        }
        result
    }
}

pub(crate) fn static_vue_dynamic_component_candidates(
    expression: &str,
    base: u32,
) -> (Vec<VueDynamicComponentCandidate>, bool) {
    let (mut expression, mut base) = trim_dynamic_component_expression(expression, base);
    if expression.is_empty() {
        return (Vec::new(), false);
    }
    while expression.len() >= 2
        && expression.as_bytes()[0] == b'('
        && matching_slot_delimiter(expression, 0, b'(', b')') == Some(expression.len() - 1)
    {
        (expression, base) =
            trim_dynamic_component_expression(&expression[1..expression.len() - 1], base + 1);
    }
    if let Some((name, start, end)) = static_dynamic_component_string(expression) {
        return (
            vec![VueDynamicComponentCandidate {
                name,
                range: TextRange::new(
                    TextSize::from(base + start as u32),
                    TextSize::from(base + end as u32),
                ),
            }],
            true,
        );
    }
    let Some((true_start, true_end, false_start, false_end)) =
        split_dynamic_component_conditional(expression)
    else {
        return (Vec::new(), false);
    };
    let (mut when_true, true_complete) = static_vue_dynamic_component_candidates(
        &expression[true_start..true_end],
        base + true_start as u32,
    );
    let (when_false, false_complete) = static_vue_dynamic_component_candidates(
        &expression[false_start..false_end],
        base + false_start as u32,
    );
    when_true.extend(when_false);
    (when_true, true_complete && false_complete)
}

const DYNAMIC_SPACE: &[char] = &[' ', '\t', '\r', '\n'];

pub(crate) fn trim_dynamic_component_expression(value: &str, base: u32) -> (&str, u32) {
    let left = value.len() - value.trim_start_matches(DYNAMIC_SPACE).len();
    (value.trim(), base + left as u32)
}

/// A complete static quoted string: `(name, start, end)` where the range
/// excludes the quotes.
pub(crate) fn static_dynamic_component_string(value: &str) -> Option<(String, usize, usize)> {
    let bytes = value.as_bytes();
    if bytes.len() < 2
        || !matches!(bytes[0], b'\'' | b'"' | b'`')
        || bytes[bytes.len() - 1] != bytes[0]
    {
        return None;
    }
    let quote = bytes[0];
    let mut escaped = false;
    let mut index = 1;
    while index < bytes.len() - 1 {
        let current = bytes[index];
        index += 1;
        if escaped {
            escaped = false;
            continue;
        }
        if current == b'\\' {
            escaped = true;
            continue;
        }
        if current == quote
            || quote == b'`' && current == b'$' && index < bytes.len() - 1 && bytes[index] == b'{'
        {
            return None;
        }
    }
    let name = &value[1..value.len() - 1];
    if name.is_empty() || name.contains('\\') {
        return None;
    }
    Some((name.to_owned(), 1, value.len() - 1))
}

/// Splits a top-level `a ? b : c`: `(true_start, true_end, false_start,
/// false_end)`.
pub(crate) fn split_dynamic_component_conditional(
    value: &str,
) -> Option<(usize, usize, usize, usize)> {
    let bytes = value.as_bytes();
    let mut state = SlotScanState::default();
    let mut question: Option<usize> = None;
    let mut nested = 0usize;
    for index in 0..bytes.len() {
        if state.top_level() {
            match bytes[index] {
                b'?' => {
                    let skip = index + 1 < bytes.len()
                        && (bytes[index + 1] == b'?' || bytes[index + 1] == b'.')
                        || index > 0 && bytes[index - 1] == b'?';
                    if !skip {
                        if question.is_none() {
                            question = Some(index);
                        } else {
                            nested += 1;
                        }
                    }
                }
                b':' => {
                    if let Some(question) = question {
                        if nested > 0 {
                            nested -= 1;
                        } else {
                            return Some((question + 1, index, index + 1, bytes.len()));
                        }
                    }
                }
                _ => {}
            }
        }
        state.consume(bytes[index]);
    }
    None
}
