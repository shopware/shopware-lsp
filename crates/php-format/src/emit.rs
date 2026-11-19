//! Writes the tokens with the gaps the layout decided, and places the
//! comments, which no layout rule knows about.

use swlsp_parser_php::syntax::*;

use crate::spacing;
use crate::tokens::{Blank, Doc, Gap, Indent};

const INDENT: &str = "    ";

/// A gap with its line breaks and indentation worked out.
enum Written {
    Glue,
    Space,
    Break { blank: bool, level: usize },
}

struct Emitter<'a, 'doc> {
    doc: &'a Doc<'doc>,
    out: String,
    /// The indentation level of the line each written token is on.
    levels: Vec<usize>,
    level: usize,
    /// The tokens that start a line which continues an expression.
    continues: Vec<bool>,
}

/// A written document.
pub(crate) struct Text {
    pub text: String,
    /// The line each token starts on.
    pub lines: Vec<usize>,
    /// Where in the text each token starts.
    pub offsets: Vec<usize>,
}

pub(crate) fn emit(doc: &Doc) -> Text {
    let mut lines = vec![0; doc.toks.len()];
    let mut offsets = vec![0; doc.toks.len()];
    let mut emitter = Emitter {
        doc,
        out: String::with_capacity(doc.source.len() + doc.source.len() / 8),
        levels: vec![0; doc.toks.len()],
        level: 0,
        continues: vec![false; doc.toks.len()],
    };
    let mut line = 0;
    let mut previous = None;
    // A comment at the end of a line whose opening brace follows it stands
    // after the brace: `): void { // why`.
    let mut order: Vec<usize> = (0..doc.toks.len()).collect();
    for index in 1..doc.toks.len().saturating_sub(1) {
        let brace = index + 1;
        if doc.is_comment(index)
            && doc.toks[index].nl == 0
            && !doc.is_comment(index - 1)
            && doc.kind(brace) == TK_OPEN_BRACE
            && !doc.toks[brace].skip
            && emitter.base(brace) == Gap::Space
        {
            order.swap(index, brace);
        }
    }
    for index in order {
        let start = &mut lines[index];
        if doc.toks[index].skip {
            *start = line;
            continue;
        }
        let before = emitter.out.len();
        if let Some(previous) = previous {
            let gap = emitter.gap(previous, index);
            emitter.continues[index] = matches!(gap, Written::Break { .. })
                && matches!(emitter.base(index), Gap::Break(_, Indent::Continue));
            emitter.write_gap(gap);
        }
        line += line_breaks(&emitter.out[before..]);
        *start = line;
        let before = emitter.out.len();
        offsets[index] = before;
        emitter.write_token(index);
        line += line_breaks(&emitter.out[before..]);
        emitter.levels[index] = emitter.level;
        previous = Some(index);
    }
    let mut out = emitter.out;
    out.truncate(out.trim_end().len());
    out.push('\n');
    Text {
        text: out,
        lines,
        offsets,
    }
}

fn line_breaks(text: &str) -> usize {
    text.bytes().filter(|byte| *byte == b'\n').count()
}

impl Emitter<'_, '_> {
    fn write_gap(&mut self, gap: Written) {
        match gap {
            Written::Glue => {}
            Written::Space => self.out.push(' '),
            Written::Break { blank, level } => {
                self.out.push('\n');
                if blank {
                    self.out.push('\n');
                }
                for _ in 0..level {
                    self.out.push_str(INDENT);
                }
                self.level = level;
            }
        }
    }

    fn write_token(&mut self, index: usize) {
        let doc = self.doc;
        if doc.is_comment(index) {
            self.write_comment(index);
        } else if let Some(heredoc) = crate::heredoc::indented(doc.text(index), self.level + 1) {
            self.out.push_str(&heredoc);
        } else {
            self.out.push_str(doc.text(index));
        }
        if doc.toks[index].comma {
            self.out.push(',');
        }
    }

    /// A comment over several lines moves with its first line: the lines of
    /// a docblock are aligned on their `*`, other lines keep their distance
    /// to each other and, below a comment on a line of its own, to its
    /// first line.
    fn write_comment(&mut self, index: usize) {
        let doc = self.doc;
        let text = doc.source_text(index);
        let mut lines = text.lines();
        self.out
            .push_str(lines.next().unwrap_or_default().trim_end());
        let indent_of = |line: &str| line.len() - line.trim_start().len();
        let plain = |line: &&str| !line.trim().is_empty() && !line.trim().starts_with('*');
        let shallowest = lines
            .clone()
            .filter(plain)
            .map(indent_of)
            .min()
            .unwrap_or(0);
        let below_first = if doc.stands_alone(index) {
            shallowest.saturating_sub(column(doc.source, doc.toks[index].start))
        } else {
            0
        };
        for line in lines {
            self.out.push('\n');
            let trimmed = line.trim();
            if trimmed.is_empty() {
                continue;
            }
            for _ in 0..self.level {
                self.out.push_str(INDENT);
            }
            if trimmed.starts_with('*') {
                self.out.push(' ');
            } else {
                for _ in shallowest..indent_of(line) + below_first {
                    self.out.push(' ');
                }
            }
            self.out.push_str(trimmed);
        }
    }

    fn base(&self, index: usize) -> Gap {
        match self.doc.gaps[index] {
            Gap::Auto => match self.doc.prev_sig(index) {
                Some(previous) => spacing::auto(self.doc, previous, index),
                None => Gap::Glue,
            },
            gap => gap,
        }
    }

    fn level_of(&self, indent: Indent, index: usize) -> usize {
        match indent {
            Indent::Zero => 0,
            Indent::Inside(anchor) => self.levels[anchor] + 1,
            Indent::Like(anchor) => self.levels[anchor],
            Indent::Continue => self.continuation(index),
        }
    }

    /// The level of a line that continues an expression: one deeper than
    /// the line the expression starts on. The operators of a condition
    /// split over lines stay at the level of the condition.
    fn continuation(&self, index: usize) -> usize {
        let doc = self.doc;
        let mut owner = doc.toks[index].parent.clone();
        while doc.first(&owner) == Some(index) {
            match owner.parent() {
                Some(parent) => owner = parent,
                None => return self.level,
            }
        }
        if owner.kind() == PHP_BINARY_EXPRESSION {
            // Every operator of a chain of them continues the first operand.
            while let Some(parent) = owner
                .parent()
                .filter(|node| node.kind() == PHP_BINARY_EXPRESSION)
            {
                owner = parent;
            }
            // An expression that starts a line inside parentheses or
            // brackets, as a condition or an argument does, has its
            // operators below its start.
            // So has one that starts a continuation line, as a value on
            // the line after its `=` does.
            if let Some(first) = doc.first(&owner)
                && (self.continues[first]
                    || matches!(
                        doc.gaps[first],
                        Gap::Break(_, Indent::Inside(open))
                            if matches!(doc.source_text(open), "(" | "[")
                    ))
            {
                return self.levels[first];
            }
        }
        match doc.first(&owner) {
            Some(first) if first < index => self.levels[first] + 1,
            _ => self.level,
        }
    }

    fn keep(&self, index: usize) -> bool {
        self.doc.toks[index].nl > 1
    }

    fn gap(&self, previous: usize, index: usize) -> Written {
        if self.doc.is_comment(index) {
            self.gap_before_comment(previous, index)
        } else if self.doc.is_comment(previous) {
            self.gap_after_comment(previous, index)
        } else {
            match self.base(index) {
                Gap::Glue | Gap::Auto => Written::Glue,
                Gap::Space => Written::Space,
                Gap::Break(blank, indent) => Written::Break {
                    blank: match blank {
                        Blank::None => false,
                        Blank::Keep => self.keep(index),
                        Blank::One => true,
                    },
                    level: self.level_of(indent, index),
                },
            }
        }
    }

    /// A comment stays on the line it was on. On a line of its own it
    /// stands at the level of what follows it, or inside the brackets it
    /// is the last thing of.
    fn gap_before_comment(&self, previous: usize, index: usize) -> Written {
        let doc = self.doc;
        if !doc.stands_alone(index) {
            // The closing brace of a class or a function has its line to
            // itself.
            if self.closes_declaration(previous) {
                return Written::Break {
                    blank: false,
                    level: self.levels[previous],
                };
            }
            let inline = doc.kind(index) == TK_BLOCK_COMMENT
                && matches!(doc.source_text(previous), "(" | "[");
            return if inline {
                Written::Glue
            } else {
                Written::Space
            };
        }
        let Some(next) = doc.next_sig(index) else {
            // A file of comments only: they stand apart from its tag.
            return Written::Break {
                blank: self.keep(index) || doc.kind(previous) == TK_OPEN_TAG,
                level: 0,
            };
        };
        let after_opener = doc.is_opener(previous);
        let (blank, level) = match self.base(next) {
            Gap::Break(blank, indent) => {
                let closes = doc.is_closer(next);
                let indent = match indent {
                    Indent::Like(open) if closes => Indent::Inside(open),
                    indent => indent,
                };
                if self.ends_case(index, next) {
                    return Written::Break {
                        blank: self.keep(index),
                        level: self.level_of(indent, next) + 1,
                    };
                }
                let blank = if doc.is_comment(previous) {
                    self.keep(index)
                } else {
                    match blank {
                        Blank::One => true,
                        Blank::Keep => self.keep(index),
                        Blank::None => closes && !after_opener && self.keep(index),
                    }
                };
                (blank, self.level_of(indent, next))
            }
            _ => (
                false,
                self.clause_level(next)
                    .unwrap_or_else(|| self.continuation(next)),
            ),
        };
        Written::Break { blank, level }
    }

    /// Whether the token is the closing brace of a class or of a function.
    fn closes_declaration(&self, index: usize) -> bool {
        let doc = self.doc;
        if doc.kind(index) != TK_CLOSE_BRACE || doc.pair[index] + 1 == index {
            return false;
        }
        let body = &doc.toks[index].parent;
        match body.kind() {
            kind if kind == PHP_CLASS_BODY => body
                .parent()
                .is_some_and(|owner| owner.kind() != PHP_ANONYMOUS_CLASS),
            kind if kind == PHP_BLOCK => body.parent().is_some_and(|owner| {
                owner.kind() == PHP_METHOD_DECLARATION || owner.kind() == PHP_FUNCTION_DECLARATION
            }),
            _ => false,
        }
    }

    /// Whether the comment stands between the body of a `case` and the
    /// next `case`: it belongs to the body, as `// no break` does.
    fn ends_case(&self, comment: usize, next: usize) -> bool {
        let doc = self.doc;
        doc.parent_kind(next) == PHP_CASE_CLAUSE
            && doc.first(&doc.toks[next].parent) == Some(next)
            && doc
                .prev_sig(comment)
                .is_some_and(|before| !doc.is_opener(before))
    }

    fn gap_after_comment(&self, comment: usize, index: usize) -> Written {
        let doc = self.doc;
        let base = self.base(index);
        let own_line = doc.stands_alone(comment);
        let ends_line = own_line || doc.kind(comment) == TK_LINE_COMMENT || doc.toks[index].nl > 0;
        if !ends_line {
            return match base {
                Gap::Break(blank, indent) => Written::Break {
                    blank: matches!(blank, Blank::One),
                    level: self.level_of(indent, index),
                },
                _ if matches!(doc.source_text(index), ")" | "]" | "," | ";") => Written::Glue,
                _ => Written::Space,
            };
        }
        match base {
            Gap::Break(blank, indent) => {
                // No empty line stands between a docblock and the attributes
                // of what it describes.
                let attribute = doc.kind(index) == TK_ATTRIBUTE_OPEN
                    && doc.source_text(comment).starts_with("/**");
                let blank = if attribute {
                    false
                } else if own_line {
                    // The empty line a rule asks for stands before the
                    // comment.
                    doc.after_comment[index]
                        || !(blank == Blank::None && doc.is_closer(index)) && self.keep(index)
                } else {
                    match blank {
                        Blank::None => false,
                        Blank::Keep => self.keep(index),
                        Blank::One => true,
                    }
                };
                Written::Break {
                    blank,
                    level: self.level_of(indent, index),
                }
            }
            _ => Written::Break {
                blank: false,
                level: self
                    .clause_level(index)
                    .unwrap_or_else(|| self.continuation(index)),
            },
        }
    }

    /// The level of an `else`, `catch` or `finally` that a comment sets
    /// apart from its closing brace: that of the brace.
    fn clause_level(&self, index: usize) -> Option<usize> {
        let doc = self.doc;
        let clause = ["else", "elseif", "catch", "finally"]
            .iter()
            .any(|word| doc.is_keyword(index, word));
        // A `;` that a comment sets apart ends its statement at the level
        // the statement starts on.
        if doc.kind(index) == TK_SEMICOLON {
            return doc
                .first(&doc.toks[index].parent)
                .filter(|&first| first < index)
                .map(|first| self.levels[first]);
        }
        let brace = doc.prev_sig(index)?;
        (clause && doc.kind(brace) == TK_CLOSE_BRACE).then(|| self.levels[brace])
    }
}

/// The column of `offset` in its line.
fn column(source: &str, offset: usize) -> usize {
    offset - source[..offset].rfind('\n').map_or(0, |line| line + 1)
}
