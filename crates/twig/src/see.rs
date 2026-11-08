//! `@see` targets in Twig documentation comments (port of `see.go`).

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_COMMENT;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::DocumentMemo;
use crate::token_parser::{find_bytes, is_twig_tag_name_byte, is_twig_tag_space, range};

/// A target declared by a Twig documentation comment such as
/// `{# @see App\Controller\ProductController::show #}`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SeeReference {
    pub target: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SeeCompletionContext {
    pub prefix: String,
    pub range: TextRange,
}

/// Explicit `@see` targets and the reference plugin's legacy single-target
/// comment form.
pub fn see_references(root: &SyntaxNode) -> Vec<SeeReference> {
    let excluded = see_verbatim_ranges(root.text().to_string().as_bytes());
    let mut result = Vec::new();
    for comment in twigquery::iterate_nodes(root, &[TWIG_COMMENT]) {
        if see_range_excluded(comment.text_range(), &excluded) {
            continue;
        }
        result.extend(
            see_references_in_comment(&comment, false)
                .into_iter()
                .filter(|reference| !reference.target.is_empty()),
        );
    }
    result
}

/// The `@see` target at `offset`. The document scan is memoized per tree
/// snapshot, so repeated requests on one document version do not rescan it.
pub fn see_reference_at(root: &SyntaxNode, offset: u32) -> Option<SeeReference> {
    static MEMO: DocumentMemo<Vec<SeeReference>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, "", || see_references(root))
        .iter()
        .find(|reference| {
            offset >= u32::from(reference.range.start())
                && offset <= u32::from(reference.range.end())
        })
        .cloned()
}

/// An explicit `@see` target under construction. Legacy bare comments stay
/// navigation-only.
pub fn see_completion_at(root: &SyntaxNode, offset: u32) -> Option<SeeCompletionContext> {
    let excluded = see_verbatim_ranges(root.text().to_string().as_bytes());
    for comment in twigquery::iterate_nodes(root, &[TWIG_COMMENT]) {
        let comment_range = comment.text_range();
        if offset < u32::from(comment_range.start()) || offset > u32::from(comment_range.end()) {
            continue;
        }
        if see_range_excluded(comment_range, &excluded) {
            continue;
        }
        for reference in see_references_in_comment(&comment, true) {
            let start = u32::from(reference.range.start());
            if offset < start || offset > u32::from(reference.range.end()) {
                continue;
            }
            let length = (offset - start) as usize;
            if length > reference.target.len() {
                continue;
            }
            return Some(SeeCompletionContext {
                prefix: reference
                    .target
                    .get(..length)
                    .unwrap_or_default()
                    .to_owned(),
                range: reference.range,
            });
        }
    }
    None
}

/// Normalizes PHP class and optional method spellings used by Twig `@see`
/// comments (`Class::method` and the legacy `Class:method`).
pub fn see_php_class_and_member(target: &str) -> Option<(String, String)> {
    let target = target.replace("\\\\", "\\");
    let target = target.trim();
    let target = target.strip_suffix("[]").unwrap_or(target);
    if target.is_empty()
        || swlsp_php_types::go_to_lower(target).ends_with(".twig")
        || target.starts_with('@')
        || target.starts_with('.')
        || target.contains('/')
    {
        return None;
    }
    let (class_name, member) = if target.matches("::").count() == 1 {
        target.split_once("::").expect("one separator")
    } else if target.matches(':').count() == 1 {
        target.split_once(':').expect("one separator")
    } else if target.contains(':') {
        return None;
    } else {
        (target, "")
    };
    let class_name = class_name.trim().trim_start_matches('\\');
    let member = member.trim();
    if class_name.is_empty() || class_name.contains(['.', '@']) {
        return None;
    }
    Some((class_name.to_owned(), member.to_owned()))
}

fn see_references_in_comment(comment: &SyntaxNode, include_empty: bool) -> Vec<SeeReference> {
    let text = comment.text().to_string();
    let bytes = text.as_bytes();
    let Some(inner_start) = text.find("{#") else {
        return Vec::new();
    };
    let inner_start = inner_start + 2;
    let mut inner_end = text.rfind("#}").unwrap_or(0);
    if inner_end < inner_start {
        inner_end = text.len();
    }
    let base = u32::from(comment.text_range().start()) as usize;
    let mut result = Vec::new();
    let mut cursor = inner_start;
    while cursor < inner_end {
        let Some(index) = find_bytes(&bytes[cursor..inner_end], b"@see") else {
            break;
        };
        let index = index + cursor;
        let mut after = index + "@see".len();
        if index > inner_start && is_see_word_byte(bytes[index - 1])
            || after < inner_end && is_see_word_byte(bytes[after])
        {
            cursor = after;
            continue;
        }
        if after >= inner_end || !is_see_space(bytes[after]) {
            cursor = after;
            continue;
        }
        while after < inner_end && is_see_space(bytes[after]) {
            after += 1;
        }
        let mut end = after;
        while end < inner_end && is_see_target_byte(bytes[end]) {
            end += 1;
        }
        if end > after || include_empty {
            result.push(SeeReference {
                target: String::from_utf8_lossy(&bytes[after..end]).into_owned(),
                range: range(base + after, base + end),
            });
        }
        cursor = end;
        if cursor == after {
            cursor += 1;
        }
    }
    if !result.is_empty() {
        return result;
    }

    let (mut start, mut end) = (inner_start, inner_end);
    while start < end && is_see_space(bytes[start]) {
        start += 1;
    }
    while end > start && is_see_space(bytes[end - 1]) {
        end -= 1;
    }
    let target = String::from_utf8_lossy(&bytes[start..end]);
    if target.is_empty()
        || target.contains(char::is_whitespace)
        || !looks_like_legacy_see_target(&target)
    {
        return Vec::new();
    }
    vec![SeeReference {
        target: target.into_owned(),
        range: range(base + start, base + end),
    }]
}

fn looks_like_legacy_see_target(target: &str) -> bool {
    if target.starts_with('@')
        || target.starts_with('\\')
        || target.starts_with('.')
        || target.contains(['\\', '/', ':'])
        || swlsp_php_types::go_to_lower(target).ends_with(".twig")
    {
        return true;
    }
    target
        .strip_suffix("[]")
        .unwrap_or(target)
        .chars()
        .next()
        .is_some_and(char::is_uppercase)
}

fn is_see_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}

fn is_see_word_byte(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphanumeric()
}

fn is_see_target_byte(value: u8) -> bool {
    !is_see_space(value) && value != b'#'
}

fn see_verbatim_ranges(content: &[u8]) -> Vec<(u32, u32)> {
    let mut result = Vec::new();
    let mut end_tag = String::new();
    let mut body_start = 0;
    let mut offset = 0;
    while offset + 2 <= content.len() {
        if end_tag.is_empty() && content[offset..].starts_with(b"{#") {
            let Some(close) = find_bytes(&content[offset + 2..], b"#}") else {
                break;
            };
            offset += close + 4;
            continue;
        }
        let Some(relative) = find_bytes(&content[offset..], b"{%") else {
            break;
        };
        let open = offset + relative;
        let mut cursor = open + 2;
        if cursor < content.len() && (content[cursor] == b'-' || content[cursor] == b'~') {
            cursor += 1;
        }
        while cursor < content.len() && is_twig_tag_space(content[cursor]) {
            cursor += 1;
        }
        let start = cursor;
        while cursor < content.len() && is_twig_tag_name_byte(content[cursor]) {
            cursor += 1;
        }
        let name = String::from_utf8_lossy(&content[start..cursor]).to_lowercase();
        let Some(close) = find_bytes(&content[cursor..], b"%}") else {
            break;
        };
        let close = close + cursor + 2;
        if end_tag.is_empty() && (name == "raw" || name == "verbatim") {
            end_tag = format!("end{name}");
            body_start = close;
        } else if !end_tag.is_empty() && name == end_tag {
            result.push((body_start as u32, open as u32));
            end_tag.clear();
        }
        offset = close;
    }
    if !end_tag.is_empty() {
        result.push((body_start as u32, content.len() as u32));
    }
    result
}

fn see_range_excluded(range: TextRange, excluded: &[(u32, u32)]) -> bool {
    let start = u32::from(range.start());
    excluded
        .iter()
        .any(|(candidate_start, candidate_end)| start >= *candidate_start && start < *candidate_end)
}
