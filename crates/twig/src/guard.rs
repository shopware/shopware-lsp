//! Twig's `{% guard %}` tag.

use swlsp_syntax::TextRange;

use crate::token_parser::{
    find_bytes, is_twig_tag_name_byte, is_twig_tag_space, lossy, range, rfind_bytes,
    twig_tag_usages,
};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum GuardKind {
    #[default]
    None,
    Function,
    Filter,
    Test,
}

impl GuardKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Function => "function",
            Self::Filter => "filter",
            Self::Test => "test",
            Self::None => "",
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct GuardCompletionContext {
    pub kind: GuardKind,
    pub callable: bool,
    pub prefix: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct GuardReference {
    pub kind: GuardKind,
    pub name: String,
    pub range: TextRange,
}

struct GuardToken {
    value: String,
    range: TextRange,
}

fn empty(offset: u32) -> TextRange {
    TextRange::empty(offset.into())
}

/// The two completion positions of Twig's guard tag:
/// `{% guard <type> %}` and `{% guard function <callable> %}`.
pub fn guard_completion_at(content: &[u8], offset: u32) -> Option<GuardCompletionContext> {
    let (tail_start, tail) = active_guard_tag(content, offset)?;
    let tokens = guard_tokens(tail, tail_start)?;
    let trailing_space = tail.last().is_some_and(|byte| is_twig_tag_space(*byte));
    match tokens.as_slice() {
        [] => Some(GuardCompletionContext {
            range: empty(offset),
            ..GuardCompletionContext::default()
        }),
        [first] => {
            let kind = parse_guard_kind(&first.value);
            if kind != GuardKind::None && trailing_space {
                return Some(GuardCompletionContext {
                    kind,
                    callable: true,
                    range: empty(offset),
                    ..GuardCompletionContext::default()
                });
            }
            Some(GuardCompletionContext {
                prefix: first.value.clone(),
                range: first.range,
                ..GuardCompletionContext::default()
            })
        }
        [first, second] => {
            let kind = parse_guard_kind(&first.value);
            if kind == GuardKind::None || trailing_space {
                return None;
            }
            Some(GuardCompletionContext {
                kind,
                callable: true,
                prefix: second.value.clone(),
                range: second.range,
            })
        }
        _ => None,
    }
}

pub fn guard_reference_at(content: &[u8], offset: u32) -> Option<GuardReference> {
    for usage in twig_tag_usages(content) {
        if !usage.name.eq_ignore_ascii_case("guard") {
            continue;
        }
        let tail_start = u32::from(usage.range.end()) as usize;
        let Some(close) = find_bytes(&content[tail_start..], b"%}") else {
            continue;
        };
        let mut tail_end = tail_start + close;
        while tail_end > tail_start && is_twig_tag_space(content[tail_end - 1]) {
            tail_end -= 1;
        }
        if tail_end > tail_start && (content[tail_end - 1] == b'-' || content[tail_end - 1] == b'~')
        {
            tail_end -= 1;
        }
        let Some(tokens) = guard_tokens(&content[tail_start..tail_end], tail_start) else {
            continue;
        };
        if tokens.len() != 2 {
            continue;
        }
        let kind = parse_guard_kind(&tokens[0].value);
        let name = &tokens[1];
        if kind == GuardKind::None
            || offset < u32::from(name.range.start())
            || offset > u32::from(name.range.end())
        {
            continue;
        }
        return Some(GuardReference {
            kind,
            name: name.value.clone(),
            range: name.range,
        });
    }
    None
}

fn active_guard_tag(content: &[u8], offset: u32) -> Option<(usize, &[u8])> {
    if offset as usize > content.len() {
        return None;
    }
    let before = &content[..offset as usize];
    let open = rfind_bytes(before, b"{%")?;
    if rfind_bytes(before, b"%}").is_some_and(|close| close > open) {
        return None;
    }
    let mut probe = before.to_vec();
    probe.extend_from_slice(b"%}");
    for usage in twig_tag_usages(&probe) {
        if (u32::from(usage.range.start()) as usize) < open
            || !usage.name.eq_ignore_ascii_case("guard")
            || u32::from(usage.range.end()) > offset
        {
            continue;
        }
        let tail_start = u32::from(usage.range.end()) as usize;
        return Some((tail_start, &before[tail_start..]));
    }
    None
}

fn guard_tokens(content: &[u8], base: usize) -> Option<Vec<GuardToken>> {
    let mut tokens = Vec::new();
    let mut cursor = 0;
    while cursor < content.len() {
        while cursor < content.len() && is_twig_tag_space(content[cursor]) {
            cursor += 1;
        }
        if cursor == content.len() {
            break;
        }
        let start = cursor;
        while cursor < content.len() && is_twig_tag_name_byte(content[cursor]) {
            cursor += 1;
        }
        if cursor == start {
            return None;
        }
        tokens.push(GuardToken {
            value: lossy(&content[start..cursor]),
            range: range(base + start, base + cursor),
        });
    }
    Some(tokens)
}

fn parse_guard_kind(value: &str) -> GuardKind {
    match value.to_ascii_lowercase().as_str() {
        "function" => GuardKind::Function,
        "filter" => GuardKind::Filter,
        "test" => GuardKind::Test,
        _ => GuardKind::None,
    }
}
