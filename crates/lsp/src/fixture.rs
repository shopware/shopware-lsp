//! Problem markup for inspection tests.
//!
//! A fixture is source text with each expected problem marked inline as
//! `{|code:text|}`, where `text` is the problem's range and may itself contain
//! markers; `{|code:|}` marks an empty range. [`parse`] splits a fixture into
//! the plain source and the marked problems, and [`render`] marks problems
//! into plain source again, so a test can compare the rendering of what an
//! inspection reported with the rendering of what the fixture expects and
//! fail with a readable diff instead of byte offsets.
//!
//! The delimiters `{|` and `|}` do not occur in the languages the server
//! inspects except inside string literals; a fixture that needs them
//! literally cannot be expressed in this format.

use std::sync::Arc;

use swlsp_syntax::{TextRange, TextSize};

use crate::{Problem, TextDocument};

const OPEN: &str = "{|";
const CLOSE: &str = "|}";

/// A problem marked in a fixture: its code and byte range in the plain
/// source.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Marked {
    pub code: String,
    pub range: TextRange,
}

impl Marked {
    fn key(&self) -> (TextSize, std::cmp::Reverse<TextSize>, &str) {
        (
            self.range.start(),
            std::cmp::Reverse(self.range.end()),
            &self.code,
        )
    }

    pub fn new(code: impl Into<String>, range: TextRange) -> Self {
        Marked {
            code: code.into(),
            range,
        }
    }
}

/// Splits `fixture` into the plain source and the problems it marks.
///
/// # Panics
///
/// On malformed markup (an unclosed or unopened marker, or an opening marker
/// without `code:`): fixtures are test input.
pub fn parse(fixture: &str) -> (String, Vec<Marked>) {
    let mut source = String::with_capacity(fixture.len());
    let mut open: Vec<(String, TextSize)> = Vec::new();
    let mut marked = Vec::new();
    let mut rest = fixture;
    while !rest.is_empty() {
        let next_open = rest.find(OPEN);
        let next_close = rest.find(CLOSE).filter(|_| !open.is_empty());
        let at = match (next_open, next_close) {
            (Some(o), Some(c)) => o.min(c),
            (Some(o), None) => o,
            (None, Some(c)) => c,
            (None, None) => rest.len(),
        };
        source.push_str(&rest[..at]);
        rest = &rest[at..];
        let offset = TextSize::of(source.as_str());
        if let Some(after) = rest.strip_prefix(CLOSE).filter(|_| next_close == Some(at)) {
            let (code, start) = open.pop().expect("open marker");
            marked.push(Marked::new(code, TextRange::new(start, offset)));
            rest = after;
        } else if let Some(after) = rest.strip_prefix(OPEN) {
            let colon = after
                .find(':')
                .unwrap_or_else(|| panic!("marker without `code:` at byte {offset:?}"));
            open.push((after[..colon].to_owned(), offset));
            rest = &after[colon + 1..];
        }
    }
    assert!(open.is_empty(), "unclosed markers: {open:?}");
    marked.sort_by(|a, b| a.key().cmp(&b.key()));
    (source, marked)
}

/// Marks `problems` into `source`. Nested ranges render nested; ranges that
/// overlap without nesting render in start order, which [`parse`] cannot
/// read back but still compares deterministically.
pub fn render(source: &str, problems: &[Marked]) -> String {
    let mut problems = problems.to_vec();
    // Outer ranges open first and close last.
    problems.sort_by(|a, b| a.key().cmp(&b.key()));
    let mut writer = Writer {
        source,
        out: String::with_capacity(source.len() + problems.len() * 16),
        cursor: 0,
        // Ends of the open ranges, innermost (smallest) last.
        open: Vec::new(),
    };
    for problem in &problems {
        writer.close_until(problem.range.start());
        writer.copy_until(problem.range.start());
        writer.out.push_str(OPEN);
        writer.out.push_str(&problem.code);
        writer.out.push(':');
        let at = writer
            .open
            .iter()
            .rposition(|&end| end >= problem.range.end())
            .map_or(0, |index| index + 1);
        writer.open.insert(at, problem.range.end());
    }
    writer.close_until(TextSize::of(source));
    writer.copy_until(TextSize::of(source));
    writer.out
}

struct Writer<'a> {
    source: &'a str,
    out: String,
    cursor: usize,
    open: Vec<TextSize>,
}

impl Writer<'_> {
    fn copy_until(&mut self, offset: TextSize) {
        let offset = usize::from(offset);
        if offset > self.cursor {
            self.out.push_str(&self.source[self.cursor..offset]);
            self.cursor = offset;
        }
    }

    /// Closes every open range ending at or before `limit`.
    fn close_until(&mut self, limit: TextSize) {
        while let Some(&end) = self.open.last() {
            if end > limit {
                break;
            }
            self.copy_until(end);
            self.out.push_str(CLOSE);
            self.open.pop();
        }
    }
}

/// The canonical rendering of `fixture`: equal for fixtures marking the same
/// problems in the same source.
pub fn normalize(fixture: &str) -> String {
    let (source, marked) = parse(fixture);
    render(&source, &marked)
}

/// Runs `inspect` on the plain source of `fixture`, opened as `uri`, and
/// asserts that it reports exactly the marked problems. Returns the problems
/// sorted like the markup, for further assertions on messages or fixes.
///
/// # Panics
///
/// When the reported problems differ from the marked ones; the message shows
/// both renderings.
pub fn check(
    uri: &str,
    fixture: &str,
    inspect: impl FnOnce(&Arc<TextDocument>) -> Vec<Problem>,
) -> (Arc<TextDocument>, Vec<Problem>) {
    let (source, expected) = parse(fixture);
    let document = Arc::new(TextDocument::new(uri, &source, 1));
    let mut problems = inspect(&document);
    problems.sort_by(|a, b| marked(a).key().cmp(&marked(b).key()));
    let actual: Vec<Marked> = problems.iter().map(marked).collect();
    let actual = render(&source, &actual);
    let expected = render(&source, &expected);
    assert!(
        actual == expected,
        "reported problems differ from the fixture\n{}",
        line_diff(&expected, &actual)
    );
    (document, problems)
}

/// The lines of `expected` and `actual`, marking those that differ with
/// `-` (fixture) and `+` (reported). Problems never add or remove lines, so
/// the lines correspond one to one.
fn line_diff(expected: &str, actual: &str) -> String {
    let mut out = String::new();
    for (expected, actual) in expected.lines().zip(actual.lines()) {
        if expected == actual {
            out.push_str(&format!("  {expected}\n"));
        } else {
            out.push_str(&format!("- {expected}\n+ {actual}\n"));
        }
    }
    out
}

fn marked(problem: &Problem) -> Marked {
    Marked::new(problem.id.as_str(), problem.range)
}
