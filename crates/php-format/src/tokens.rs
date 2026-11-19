//! The token stream a document is laid out from: every token that is not
//! whitespace, with the line breaks the source had before it.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxToken};

/// No token: the partner of a bracket without one.
pub(crate) const NONE: usize = usize::MAX;

/// What is written between a token and the one before it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Gap {
    /// Decided by the two tokens: see `spacing`.
    Auto,
    Glue,
    Space,
    Break(Blank, Indent),
}

/// The empty lines before a token that starts a line.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Blank {
    None,
    /// At most one, when the source had one.
    Keep,
    One,
}

/// The indentation of a token that starts a line, relative to a line that
/// is already written.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Indent {
    Zero,
    /// One level deeper than the line of this token.
    Inside(usize),
    /// The level of the line of this token.
    Like(usize),
    /// A continuation line: one level deeper than the line its expression
    /// starts on.
    Continue,
}

pub(crate) struct Tok {
    pub kind: Kind,
    pub start: usize,
    pub end: usize,
    /// Line breaks between the previous token and this one.
    pub nl: u32,
    pub parent: SyntaxNode,
    /// The text to write when it is not the source text.
    pub text: Option<String>,
    /// Not written at all.
    pub skip: bool,
    /// A comma is written right after it.
    pub comma: bool,
}

pub(crate) struct Doc<'a> {
    pub source: &'a str,
    pub toks: Vec<Tok>,
    /// The partner of each bracket, [`NONE`] for other tokens.
    pub pair: Vec<usize>,
    pub gaps: Vec<Gap>,
    /// Opening brackets of lists that are on one line, which a line that
    /// is too long may split.
    pub flat: Vec<bool>,
    /// Opening brackets of lists split to shorten a line.
    pub split: Vec<bool>,
    /// Tokens a line is broken before to shorten it.
    pub breaks: Vec<bool>,
    /// The tokens the document was built without.
    dropped: Vec<bool>,
    /// Tokens of `$$name` and `${…}`, which the parser does not read as one
    /// variable: they keep the white space of the source.
    pub raw: Vec<bool>,
    /// Tokens that keep an empty line between them and a comment before.
    pub after_comment: Vec<bool>,
}

impl<'a> Doc<'a> {
    /// The tokens of `root`. `None` for a document this formatter does not
    /// handle: one that does not start with `<?php`, has markup outside PHP
    /// tags, or has brackets that do not match.
    pub fn new(source: &'a str, root: &SyntaxNode) -> Option<Self> {
        Self::build(source, root, false)
    }

    /// The tokens of a document with markup outside PHP tags: the markup
    /// is a token like the others, and every tag is kept.
    pub fn template(source: &'a str, root: &SyntaxNode) -> Option<Self> {
        Self::build(source, root, true)
    }

    fn build(source: &'a str, root: &SyntaxNode, template: bool) -> Option<Self> {
        let mut toks = Vec::new();
        let mut nl = 0;
        for element in root.descendants_with_tokens() {
            let NodeOrToken::Token(token) = element else {
                continue;
            };
            let kind = token.kind();
            if kind == TK_WHITESPACE || kind == TK_LINE_BREAK {
                nl += token.text().bytes().filter(|byte| *byte == b'\n').count() as u32;
                continue;
            }
            // White space after the closing tag that ends a file.
            if kind == TK_INLINE_HTML && !template && token.text().trim().is_empty() {
                continue;
            }
            // What the parser does not know is not laid out.
            // The parser reads the `&` of `$a = &$b` and the `:` of a label
            // as errors of their own, and nothing else about the statement
            // wrong.
            let unknown = token.parent().is_some_and(|node| {
                node.kind() == ERROR
                    && (kind != TK_AMPERSAND && kind != TK_COLON
                        || node
                            .children_with_tokens()
                            .filter(|child| !child.kind().is_trivia())
                            .count()
                            > 1)
            });
            if kind == TK_UNKNOWN || unknown {
                return None;
            }
            toks.push(tok(&token, nl)?);
            nl = 0;
        }
        if !template {
            let first = toks.first()?;
            if first.kind != TK_OPEN_TAG
                || !source[first.start..first.end].eq_ignore_ascii_case("<?php")
            {
                return None;
            }
            if toks.iter().skip(1).any(|tok| tok.kind == TK_OPEN_TAG) {
                return None;
            }
            // A closing tag ends the file, and is then left out.
            if let Some(position) = toks.iter().position(|tok| tok.kind == TK_CLOSE_TAG) {
                if position + 1 != toks.len() || !source[toks[position].end..].trim().is_empty() {
                    return None;
                }
                toks[position].skip = true;
            }
        }
        let pair = pairs(source, &toks)?;
        let count = toks.len();
        let dropped = toks.iter().map(|tok| tok.skip).collect();
        let raw = raw(source, &toks, &pair);
        Some(Self {
            raw,
            flat: vec![false; count],
            split: vec![false; count],
            breaks: vec![false; count],
            dropped,
            source,
            toks,
            pair,
            gaps: vec![Gap::Auto; count],
            after_comment: vec![false; count],
        })
    }

    /// Forgets the layout, for another pass over the same tokens.
    pub fn reset_layout(&mut self) {
        self.gaps.fill(Gap::Auto);
        self.flat.fill(false);
        self.after_comment.fill(false);
        for (tok, dropped) in self.toks.iter_mut().zip(&self.dropped) {
            tok.skip = *dropped;
            tok.comma = false;
        }
    }

    pub fn text(&self, index: usize) -> &str {
        let tok = &self.toks[index];
        match &tok.text {
            Some(text) => text,
            None => &self.source[tok.start..tok.end],
        }
    }

    /// The text as it is in the source.
    pub fn source_text(&self, index: usize) -> &'a str {
        let tok = &self.toks[index];
        &self.source[tok.start..tok.end]
    }

    pub fn kind(&self, index: usize) -> Kind {
        self.toks[index].kind
    }

    pub fn is_comment(&self, index: usize) -> bool {
        let kind = self.toks[index].kind;
        kind == TK_LINE_COMMENT || kind == TK_BLOCK_COMMENT
    }

    /// Whether the token is the keyword `word`, in any case.
    pub fn is_keyword(&self, index: usize, word: &str) -> bool {
        self.toks[index].kind == TK_KEYWORD && self.source_text(index).eq_ignore_ascii_case(word)
    }

    pub fn parent_kind(&self, index: usize) -> Kind {
        self.toks[index].parent.kind()
    }

    /// The token before `index` that is written and is not a comment.
    pub fn prev_sig(&self, index: usize) -> Option<usize> {
        (0..index)
            .rev()
            .find(|&at| !self.toks[at].skip && !self.is_comment(at))
    }

    /// The token after `index` that is written and is not a comment.
    pub fn next_sig(&self, index: usize) -> Option<usize> {
        (index + 1..self.toks.len()).find(|&at| !self.toks[at].skip && !self.is_comment(at))
    }

    /// The index of `token`.
    pub fn index(&self, token: &SyntaxToken) -> Option<usize> {
        let start = usize::from(token.text_range().start());
        self.toks.binary_search_by_key(&start, |tok| tok.start).ok()
    }

    /// The first token of `node` that is not a comment.
    pub fn first(&self, node: &SyntaxNode) -> Option<usize> {
        let range = node.text_range();
        let (start, end) = (usize::from(range.start()), usize::from(range.end()));
        let from = self.toks.partition_point(|tok| tok.start < start);
        (from..self.toks.len())
            .take_while(|&at| self.toks[at].start < end)
            .find(|&at| !self.is_comment(at))
    }

    /// The last token of `node` that is not a comment.
    pub fn last(&self, node: &SyntaxNode) -> Option<usize> {
        let range = node.text_range();
        let (start, end) = (usize::from(range.start()), usize::from(range.end()));
        let to = self.toks.partition_point(|tok| tok.start < end);
        (0..to)
            .rev()
            .take_while(|&at| self.toks[at].start >= start)
            .find(|&at| !self.is_comment(at))
    }

    /// The token of `node` itself (not of a child node) with this text.
    pub fn child(&self, node: &SyntaxNode, text: &str) -> Option<usize> {
        self.children(node)
            .find(|&at| self.source_text(at).eq_ignore_ascii_case(text))
    }

    /// The tokens of `node` itself, comments left out.
    pub fn children<'b>(&'b self, node: &'b SyntaxNode) -> impl Iterator<Item = usize> + 'b {
        node.children_with_tokens()
            .filter_map(|element| element.into_token())
            .filter_map(|token| self.index(&token))
            .filter(|&at| !self.is_comment(at))
    }

    /// Whether the comment stands on a line of its own: the source has it
    /// so, or it is a docblock after a statement or a comment, which
    /// describes what follows it.
    pub fn stands_alone(&self, comment: usize) -> bool {
        self.toks[comment].nl > 0
            || self.source_text(comment).starts_with("/**")
                && (0..comment)
                    .rev()
                    .find(|&at| !self.toks[at].skip)
                    .is_some_and(|before| {
                        self.is_comment(before)
                            || matches!(self.source_text(before), ";" | "{" | "}")
                    })
    }

    pub fn is_opener(&self, index: usize) -> bool {
        self.pair[index] != NONE && self.pair[index] > index
    }

    pub fn is_closer(&self, index: usize) -> bool {
        self.pair[index] != NONE && self.pair[index] < index
    }
}

/// Whether the document holds markup outside PHP tags, which the parser
/// reads as broken PHP.
pub(crate) fn has_markup(source: &str, root: &SyntaxNode) -> bool {
    let mut tokens = root
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| !token.kind().is_trivia());
    let opens = tokens.next().is_some_and(|first| {
        first.kind() == TK_OPEN_TAG
            && first.text().eq_ignore_ascii_case("<?php")
            && usize::from(first.text_range().start()) == 0
    });
    let markup = root
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| token.kind() == TK_INLINE_HTML && !token.text().trim().is_empty());
    markup
        || !opens
        || tokens.any(|token| {
            token.kind() == TK_OPEN_TAG
                || token.kind() == TK_CLOSE_TAG
                    && !source[usize::from(token.text_range().end())..]
                        .trim()
                        .is_empty()
        })
}

/// The tokens after a lone `$`: the name it is put before, or what its
/// braces hold.
fn raw(source: &str, toks: &[Tok], pair: &[usize]) -> Vec<bool> {
    let mut raw = vec![false; toks.len()];
    for (index, tok) in toks.iter().enumerate() {
        if tok.kind != TK_VARIABLE || &source[tok.start..tok.end] != "$" {
            continue;
        }
        let Some(next) = toks.get(index + 1) else {
            continue;
        };
        let end = if next.kind == TK_OPEN_BRACE && pair[index + 1] != NONE {
            pair[index + 1]
        } else {
            index + 1
        };
        for at in raw.iter_mut().take(end + 1).skip(index + 1) {
            *at = true;
        }
    }
    raw
}

fn tok(token: &SyntaxToken, nl: u32) -> Option<Tok> {
    let range = token.text_range();
    Some(Tok {
        kind: token.kind(),
        start: usize::from(range.start()),
        end: usize::from(range.end()),
        nl,
        parent: token.parent()?,
        text: None,
        skip: false,
        comma: false,
    })
}

fn pairs(source: &str, toks: &[Tok]) -> Option<Vec<usize>> {
    let mut pair = vec![NONE; toks.len()];
    let mut open: Vec<(usize, &str)> = Vec::new();
    for (index, tok) in toks.iter().enumerate() {
        let kind = tok.kind;
        let closes = if kind == TK_OPEN_PAREN {
            ")"
        } else if kind == TK_OPEN_BRACE {
            "}"
        } else if kind == TK_OPEN_BRACKET || kind == TK_ATTRIBUTE_OPEN {
            "]"
        } else if kind == TK_CLOSE_PAREN || kind == TK_CLOSE_BRACE || kind == TK_CLOSE_BRACKET {
            let (partner, expected) = open.pop()?;
            if expected != &source[tok.start..tok.end] {
                return None;
            }
            pair[partner] = index;
            pair[index] = partner;
            continue;
        } else {
            continue;
        };
        open.push((index, closes));
    }
    open.is_empty().then_some(pair)
}
