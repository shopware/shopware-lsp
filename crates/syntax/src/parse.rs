//! Language-agnostic event parser: a trivia-skipping token cursor, markers that record
//! start/finish events, forward parents for left-recursive wrapping, a
//! recovery loop, and a sink that replays events into a lossless green tree.

use std::cell::Cell;
use std::fmt;
use std::sync::Arc;

use rowan::{GreenNodeBuilder, TextRange, TextSize};

use crate::{Kind, Tree};

/// One lexeme. The text borrows the source being parsed.
#[derive(Clone, Copy, PartialEq, Eq)]
pub struct Token<'a> {
    pub kind: Kind,
    pub start: u32,
    pub text: &'a str,
}

impl<'a> Token<'a> {
    #[inline]
    pub fn new(kind: Kind, source: &'a str, start: u32, end: u32) -> Self {
        Self {
            kind,
            start,
            text: &source[start as usize..end as usize],
        }
    }

    #[inline]
    pub fn end(&self) -> u32 {
        self.start + self.text.len() as u32
    }

    #[inline]
    pub fn range(&self) -> TextRange {
        TextRange::new(TextSize::from(self.start), TextSize::from(self.end()))
    }
}

impl fmt::Debug for Token<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "{:?}@{}..{} {:?}",
            self.kind,
            self.start,
            self.end(),
            self.text
        )
    }
}

/// A parse diagnostic. `found == Kind::NONE` means end of file.
#[derive(Clone, PartialEq, Eq)]
pub struct ParseError {
    pub range: TextRange,
    pub found: Kind,
    pub expected: String,
}

impl ParseError {
    pub fn message(&self) -> String {
        if self.found != Kind::NONE {
            format!(
                "expected {} but found {}",
                self.expected,
                self.found.token_text()
            )
        } else {
            format!("expected {} but reached end of file", self.expected)
        }
    }
}

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "error at {}..{}: {}",
            u32::from(self.range.start()),
            u32::from(self.range.end()),
            self.message()
        )
    }
}

impl fmt::Debug for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Display::fmt(self, f)
    }
}

/// Builder for a [`ParseError`]; range and found kind default to the current
/// token (or the last token at EOF).
#[derive(Clone, Debug)]
pub struct ErrorBuilder {
    range: Option<TextRange>,
    found: Option<Kind>,
    expected: String,
}

impl ErrorBuilder {
    pub fn new(expected: impl Into<String>) -> Self {
        Self {
            range: None,
            found: None,
            expected: expected.into(),
        }
    }

    /// Pins range and found kind to an already-consumed token.
    pub fn at_token(mut self, token: &Token<'_>) -> Self {
        self.range = Some(token.range());
        self.found = Some(token.kind);
        self
    }

    /// Pins only the range.
    pub fn at_range(mut self, range: TextRange) -> Self {
        self.range = Some(range);
        self
    }
}

/// Language configuration for a [`Parser`].
#[derive(Clone, Copy)]
pub struct Config {
    /// Tokens that can begin a new top-level element; always a sync point.
    pub general_recovery_set: &'static [Kind],
    /// Node kind wrapping skipped garbage.
    pub error_kind: Kind,
}

/// Lookahead entry for [`Parser::at_following_content`].
#[derive(Clone, Copy)]
pub struct FollowingContent<'s> {
    pub kind: Kind,
    pub text: Option<&'s str>,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum EventKind {
    Placeholder,
    StartNode,
    AddToken,
    AddNextNTokensAs,
    FinishNode,
    ExplicitlyConsumeTrivia,
}

#[derive(Clone, Copy, Debug)]
struct Event {
    kind: EventKind,
    node_kind: Kind,
    /// Forward-parent offset for StartNode, token count for AddNextNTokensAs.
    data: u32,
}

impl Event {
    const PLACEHOLDER: Event = Event {
        kind: EventKind::Placeholder,
        node_kind: Kind::NONE,
        data: 0,
    };
}

/// An open node marker. Must be completed with [`Parser::complete`].
#[derive(Debug)]
#[must_use = "markers must be completed"]
pub struct Marker {
    pos: u32,
}

/// A completed node that may be wrapped later with [`Parser::precede`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct CompletedMarker {
    pos: u32,
}

/// The event-based parser engine.
pub struct Parser<'a> {
    source: &'a str,
    tokens: Vec<Token<'a>>,
    cursor: Cell<usize>,
    events: Vec<Event>,
    errors: Vec<ParseError>,
    config: Config,
    #[cfg(debug_assertions)]
    open_markers: Vec<u32>,
}

impl<'a> Parser<'a> {
    pub fn new(source: &'a str, tokens: Vec<Token<'a>>, config: Config) -> Self {
        let events = Vec::with_capacity(tokens.len() * 2);
        Self {
            source,
            tokens,
            cursor: Cell::new(0),
            events,
            errors: Vec::new(),
            config,
            #[cfg(debug_assertions)]
            open_markers: Vec::new(),
        }
    }

    #[inline]
    pub fn source(&self) -> &'a str {
        self.source
    }

    #[inline]
    fn eat_trivia(&self) -> usize {
        let mut cursor = self.cursor.get();
        while cursor < self.tokens.len() && self.tokens[cursor].kind.is_trivia() {
            cursor += 1;
        }
        self.cursor.set(cursor);
        cursor
    }

    // -- peeking --------------------------------------------------------------

    /// Kind of the current non-trivia token, `None` at EOF.
    #[inline]
    pub fn peek(&self) -> Option<Kind> {
        let cursor = self.eat_trivia();
        self.tokens.get(cursor).map(|token| token.kind)
    }

    /// The current non-trivia token, `None` at EOF.
    #[inline]
    pub fn peek_token(&self) -> Option<&Token<'a>> {
        let cursor = self.eat_trivia();
        self.tokens.get(cursor)
    }

    /// Raw lookahead without skipping trivia between tokens.
    #[inline]
    pub fn peek_nth_token(&self, n: usize) -> Option<&Token<'a>> {
        let cursor = self.eat_trivia();
        self.tokens.get(cursor + n)
    }

    /// Raw cursor position for progress checks.
    #[inline]
    pub fn get_pos(&self) -> usize {
        self.cursor.get()
    }

    /// Kind of the next non-trivia token after the current one.
    pub fn peek_next_non_trivia_kind(&self) -> Option<Kind> {
        let cursor = self.eat_trivia();
        self.tokens
            .get(cursor + 1..)?
            .iter()
            .map(|token| token.kind)
            .find(|kind| !kind.is_trivia())
    }

    /// All lexer tokens (including trivia).
    pub fn tokens(&self) -> &[Token<'a>] {
        &self.tokens
    }

    // -- predicates -----------------------------------------------------------

    #[inline]
    pub fn at(&self, kind: Kind) -> bool {
        self.peek() == Some(kind)
    }

    #[inline]
    pub fn at_set(&self, set: &[Kind]) -> bool {
        self.peek().is_some_and(|kind| set.contains(&kind))
    }

    #[inline]
    pub fn at_end(&self) -> bool {
        self.peek().is_none()
    }

    /// Whether the next `set.len()` non-trivia kinds match `set` in order.
    /// Returns false when already at EOF.
    pub fn at_following(&self, set: &[Kind]) -> bool {
        let cursor = self.eat_trivia();
        if cursor == self.tokens.len() {
            return false;
        }
        let mut upcoming = self.tokens[cursor..]
            .iter()
            .filter(|token| !token.kind.is_trivia());
        set.iter()
            .all(|kind| upcoming.next().is_some_and(|token| token.kind == *kind))
    }

    /// Like [`Parser::at_following`] but optionally matching token text.
    pub fn at_following_content(&self, set: &[FollowingContent<'_>]) -> bool {
        let cursor = self.eat_trivia();
        if cursor == self.tokens.len() {
            return false;
        }
        let mut upcoming = self.tokens[cursor..]
            .iter()
            .filter(|token| !token.kind.is_trivia());
        set.iter().all(|want| {
            upcoming.next().is_some_and(|token| {
                token.kind == want.kind && want.text.is_none_or(|text| token.text == text)
            })
        })
    }

    // -- consumption ----------------------------------------------------------

    fn next_token(&self) -> Option<Token<'a>> {
        let cursor = self.eat_trivia();
        let token = *self.tokens.get(cursor)?;
        self.cursor.set(cursor + 1);
        Some(token)
    }

    /// Consumes the next non-trivia token under its lexer kind.
    ///
    /// # Panics
    /// At EOF; the grammar must check [`Parser::at_end`] first.
    pub fn bump(&mut self) -> Token<'a> {
        let token = self
            .next_token()
            .expect("parser: bump called, but there are no more tokens");
        self.push(Event {
            kind: EventKind::AddToken,
            node_kind: token.kind,
            data: 0,
        });
        token
    }

    /// Consumes one token, recording it under a different kind.
    pub fn bump_as(&mut self, kind: Kind) -> Token<'a> {
        let mut token = self
            .next_token()
            .expect("parser: bump_as called, but there are no more tokens");
        self.push(Event {
            kind: EventKind::AddToken,
            node_kind: kind,
            data: 0,
        });
        token.kind = kind;
        token
    }

    /// Consumes `n` raw tokens and fuses them into one tree token of `kind`.
    pub fn bump_next_n_as(&mut self, n: usize, kind: Kind) -> &[Token<'a>] {
        let cursor = self.eat_trivia();
        let end = cursor + n;
        assert!(
            end <= self.tokens.len(),
            "parser: bump_next_n_as called, but there are not enough tokens"
        );
        self.cursor.set(end);
        self.push(Event {
            kind: EventKind::AddNextNTokensAs,
            node_kind: kind,
            data: n as u32,
        });
        &self.tokens[cursor..end]
    }

    /// Forces pending trivia inside the currently open node.
    pub fn explicitly_consume_trivia(&mut self) {
        self.push(Event {
            kind: EventKind::ExplicitlyConsumeTrivia,
            node_kind: Kind::NONE,
            data: 0,
        });
    }

    #[inline]
    fn push(&mut self, event: Event) {
        self.events.push(event);
    }

    // -- markers --------------------------------------------------------------

    pub fn start(&mut self) -> Marker {
        let pos = self.events.len() as u32;
        self.push(Event::PLACEHOLDER);
        #[cfg(debug_assertions)]
        self.open_markers.push(pos);
        Marker { pos }
    }

    pub fn complete(&mut self, marker: Marker, kind: Kind) -> CompletedMarker {
        #[cfg(debug_assertions)]
        {
            let open = self.open_markers.pop();
            assert_eq!(
                open,
                Some(marker.pos),
                "parser: inner markers must be closed before outer markers"
            );
        }
        let event = &mut self.events[marker.pos as usize];
        assert_eq!(
            event.kind,
            EventKind::Placeholder,
            "parser: complete called on a non-placeholder event"
        );
        *event = Event {
            kind: EventKind::StartNode,
            node_kind: kind,
            data: 0,
        };
        self.push(Event {
            kind: EventKind::FinishNode,
            node_kind: Kind::NONE,
            data: 0,
        });
        CompletedMarker { pos: marker.pos }
    }

    /// Opens a new marker that will wrap the already-completed node.
    pub fn precede(&mut self, completed: CompletedMarker) -> Marker {
        let marker = self.start();
        let event = &mut self.events[completed.pos as usize];
        assert_eq!(
            event.kind,
            EventKind::StartNode,
            "parser: precede called on a non-StartNode event"
        );
        event.data = marker.pos - completed.pos;
        marker
    }

    /// Discards an open marker without creating a node. Valid only when the
    /// marker is the most recently started open marker.
    pub fn abandon(&mut self, marker: Marker) {
        #[cfg(debug_assertions)]
        {
            let open = self.open_markers.pop();
            assert_eq!(
                open,
                Some(marker.pos),
                "parser: abandoned marker is not the innermost"
            );
        }
        // The placeholder stays in place and is skipped by the sink.
        let _ = marker;
    }

    // -- expect / recovery ----------------------------------------------------

    pub fn expect(&mut self, kind: Kind, recovery_set: &[Kind]) -> Option<Token<'a>> {
        if self.at(kind) {
            return Some(self.bump());
        }
        self.add_error(ErrorBuilder::new(kind.token_text()));
        self.recover_expect(&[kind], recovery_set)
    }

    pub fn expect_any(&mut self, kinds: &[Kind], recovery_set: &[Kind]) -> Option<Token<'a>> {
        if self.at_set(kinds) {
            return Some(self.bump());
        }
        let expected = kinds
            .iter()
            .map(|kind| kind.token_text())
            .collect::<Vec<_>>()
            .join(" or ");
        self.add_error(ErrorBuilder::new(expected));
        self.recover_expect(kinds, recovery_set)
    }

    pub fn recover(&mut self, recovery_set: &[Kind]) {
        self.recover_expect(&[], recovery_set);
    }

    fn at_sync_point(&self, recovery_set: &[Kind]) -> bool {
        self.at_end() || self.at_set(recovery_set) || self.at_set(self.config.general_recovery_set)
    }

    /// Skips garbage up to a sync point (wrapped in the error node), resyncing
    /// on one of `expected` if it appears.
    pub fn recover_expect(
        &mut self,
        expected: &[Kind],
        recovery_set: &[Kind],
    ) -> Option<Token<'a>> {
        if self.at_sync_point(recovery_set) {
            return None;
        }
        let marker = self.start();
        loop {
            self.bump();
            if !expected.is_empty() && self.at_set(expected) {
                self.complete(marker, self.config.error_kind);
                return Some(self.bump());
            }
            if self.at_sync_point(recovery_set) {
                self.complete(marker, self.config.error_kind);
                return None;
            }
        }
    }

    /// Records an error without consuming tokens.
    pub fn add_error(&mut self, builder: ErrorBuilder) {
        let ErrorBuilder {
            mut range,
            mut found,
            expected,
        } = builder;
        if range.is_none() || found.is_none() {
            match self.peek_token() {
                Some(current) => {
                    range = range.or(Some(current.range()));
                    found = found.or(Some(current.kind));
                }
                None => {
                    if range.is_none() {
                        range = Some(self.tokens.last().map(Token::range).unwrap_or_default());
                    }
                }
            }
        }
        self.errors.push(ParseError {
            range: range.unwrap_or_default(),
            found: found.unwrap_or(Kind::NONE),
            expected,
        });
    }

    pub fn errors(&self) -> &[ParseError] {
        &self.errors
    }

    /// Replays the events into a lossless tree.
    pub fn finish(self) -> (Tree, Vec<ParseError>) {
        let source: Arc<str> = Arc::from(self.source);
        self.finish_with_source(source)
    }

    /// Like [`Parser::finish`] but reuses an existing shared source.
    pub fn finish_with_source(self, source: Arc<str>) -> (Tree, Vec<ParseError>) {
        debug_assert_eq!(&*source, self.source);
        #[cfg(debug_assertions)]
        assert!(
            self.open_markers.is_empty(),
            "parser: markers need to be completed"
        );
        let green = Sink::new(self.source, &self.tokens, self.events).finish();
        (Tree::new(source, green), self.errors)
    }
}

struct Sink<'t, 'a> {
    source: &'a str,
    tokens: &'t [Token<'a>],
    cursor: usize,
    events: Vec<Event>,
    builder: GreenNodeBuilder<'static>,
}

impl<'t, 'a> Sink<'t, 'a> {
    fn new(source: &'a str, tokens: &'t [Token<'a>], events: Vec<Event>) -> Self {
        Self {
            source,
            tokens,
            cursor: 0,
            events,
            builder: GreenNodeBuilder::new(),
        }
    }

    fn finish(mut self) -> rowan::GreenNode {
        let mut forward_kinds: Vec<Kind> = Vec::new();
        let last = self.events.len().saturating_sub(1);
        for index in 0..self.events.len() {
            let event = std::mem::replace(&mut self.events[index], Event::PLACEHOLDER);
            if matches!(
                event.kind,
                EventKind::AddToken | EventKind::AddNextNTokensAs
            ) || index == last
            {
                self.consume_trivia();
            }
            match event.kind {
                EventKind::StartNode => {
                    forward_kinds.push(event.node_kind);
                    let mut walk = index;
                    let mut forward = event.data;
                    while forward != 0 {
                        walk += forward as usize;
                        let parent = std::mem::replace(&mut self.events[walk], Event::PLACEHOLDER);
                        assert_eq!(
                            parent.kind,
                            EventKind::StartNode,
                            "sink: forward parent is not a StartNode"
                        );
                        forward_kinds.push(parent.node_kind);
                        forward = parent.data;
                    }
                    for kind in forward_kinds.drain(..).rev() {
                        self.builder.start_node(rowan::SyntaxKind(kind.0));
                    }
                }
                EventKind::AddToken => self.token_as(event.node_kind, 1),
                EventKind::AddNextNTokensAs => self.token_as(event.node_kind, event.data as usize),
                EventKind::ExplicitlyConsumeTrivia => self.consume_trivia(),
                EventKind::FinishNode => self.builder.finish_node(),
                EventKind::Placeholder => {}
            }
        }
        assert_eq!(
            self.cursor,
            self.tokens.len(),
            "sink: parser did not consume all tokens; this is a bug in the grammar"
        );
        self.builder.finish()
    }

    fn consume_trivia(&mut self) {
        while let Some(token) = self.tokens.get(self.cursor) {
            if !token.kind.is_trivia() {
                break;
            }
            self.builder
                .token(rowan::SyntaxKind(token.kind.0), token.text);
            self.cursor += 1;
        }
    }

    fn token_as(&mut self, kind: Kind, count: usize) {
        let first = self.tokens[self.cursor];
        let last = self.tokens[self.cursor + count - 1];
        let text = &self.source[first.start as usize..last.end() as usize];
        self.builder.token(rowan::SyntaxKind(kind.0), text);
        self.cursor += count;
    }
}
