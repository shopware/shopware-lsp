//! `@noinspection` suppression comments for any language with a syntax tree.
//!
//! PHP has its own reader (`swlsp-php-suppression`, which also understands
//! PHPStan's forms). Other languages register
//! [`comment_suppression_reader`] with their comment kinds. A comment
//! containing `@noinspection` followed by diagnostic codes suppresses those
//! codes, or every code when none follows (`*` and `all` work too), on
//!
//! - its own line when code shares the line (`{{ x }} {# @noinspection a #}`);
//! - otherwise the next line containing code, skipping blank and comment-only
//!   lines.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_syntax::{Kind, LineIndex, TextRange, TextSize};

use crate::document::TextDocument;
use crate::inspection::{DiagnosticId, SuppressionReader, Suppressions};

const MARKER: &str = "@noinspection";
/// Comment terminators that end a directive's code list.
const TERMINATORS: [&str; 3] = ["#}", "-->", "*/"];

/// A reader for languages whose comments are the elements of `comment_kinds`
/// (tokens or nodes).
pub fn comment_suppression_reader(comment_kinds: &'static [Kind]) -> SuppressionReader {
    Arc::new(move |document: &TextDocument| {
        Box::new(parse(document, comment_kinds)) as Box<dyn Suppressions>
    })
}

#[derive(Debug, Default)]
struct Directive {
    codes: Vec<String>,
}

impl Directive {
    fn accepts(&self, code: &str) -> bool {
        self.codes.is_empty()
            || self.codes.iter().any(|candidate| {
                candidate == "*" || candidate == "all" || candidate.eq_ignore_ascii_case(code)
            })
    }
}

struct LineSuppressions {
    line_index: Arc<LineIndex>,
    lines: HashMap<u32, Vec<Directive>>,
}

impl Suppressions for LineSuppressions {
    fn suppresses(&self, offset: TextSize, code: &DiagnosticId) -> bool {
        if self.lines.is_empty() {
            return false;
        }
        let (line, _) = self.line_index.position(offset.into());
        self.lines
            .get(&line)
            .is_some_and(|directives| directives.iter().any(|d| d.accepts(code.as_str())))
    }
}

fn parse(document: &TextDocument, comment_kinds: &[Kind]) -> LineSuppressions {
    let line_index = document.line_index_arc().clone();
    let mut result = LineSuppressions {
        line_index: line_index.clone(),
        lines: HashMap::new(),
    };
    let source = document.source();
    if !source.to_ascii_lowercase().contains(MARKER) {
        return result;
    }
    let Some(root) = document.root() else {
        return result;
    };
    let line = |offset: TextSize| line_index.position(offset.into()).0;
    let mut comments: Vec<TextRange> = Vec::new();
    let mut code_lines: Vec<bool> = vec![false; line_index.line_count()];
    let mut comment_end = TextSize::from(0);
    for element in root.descendants_with_tokens() {
        let range = element.text_range();
        if comment_kinds.contains(&element.kind()) {
            comment_end = comment_end.max(range.end());
            // Comment nodes can carry the preceding line break as leading
            // trivia; their line is that of the comment text.
            let text = &source[range];
            let start = range.start() + TextSize::of(&text[..text.len() - text.trim_start().len()]);
            let end = range.end() - TextSize::of(&text[text.trim_end().len()..]);
            if start < end {
                comments.push(TextRange::new(start, end));
            }
            continue;
        }
        let Some(token) = element.into_token() else {
            continue;
        };
        // Elements arrive in source order, so a token starting before the
        // end of the last comment belongs to that comment node.
        if range.start() < comment_end {
            continue;
        }
        if token.text().trim().is_empty() {
            continue;
        }
        let (first, last) = (line(range.start()), line(range.end()));
        for code_line in first..=last {
            if let Some(slot) = code_lines.get_mut(code_line as usize) {
                *slot = true;
            }
        }
    }
    for comment in comments {
        let text = &source[comment];
        let Some(directive) = directive(text) else {
            continue;
        };
        let start_line = line(comment.start());
        let target = if code_lines.get(start_line as usize) == Some(&true) {
            start_line
        } else {
            let after = line(comment.end());
            match (after + 1..line_index.line_count() as u32)
                .find(|candidate| code_lines.get(*candidate as usize) == Some(&true))
            {
                Some(next) => next,
                None => continue,
            }
        };
        result.lines.entry(target).or_default().push(directive);
    }
    result
}

/// The directive in a comment's text, if it has one.
fn directive(text: &str) -> Option<Directive> {
    let position = text.to_ascii_lowercase().find(MARKER)?;
    let mut rest = &text[position + MARKER.len()..];
    for terminator in TERMINATORS {
        if let Some(end) = rest.find(terminator) {
            rest = &rest[..end];
        }
    }
    let codes = rest
        .split(|c: char| c.is_whitespace() || c == ',')
        .filter(|code| !code.is_empty())
        .map(str::to_owned)
        .collect();
    Some(Directive { codes })
}
