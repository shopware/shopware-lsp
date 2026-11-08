//! Lossless syntax tree on top of rowan's green/red trees.
//!
//! Green nodes are immutable, `Send + Sync`, and deduplicate identical tokens
//! (whitespace, punctuation, keywords) within one parse. Red nodes
//! ([`SyntaxNode`]) add parent pointers and absolute offsets on demand and are
//! cheap, thread-local views.

use std::sync::Arc;

use rowan::{GreenNode, NodeOrToken, WalkEvent};

use crate::Kind;

pub use rowan::{TextRange, TextSize};

/// The single rowan language shared by every grammar. Kinds are globally
/// unique, so trees of different languages can coexist without separate
/// `Language` types.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Lang {}

impl rowan::Language for Lang {
    type Kind = Kind;

    #[inline]
    fn kind_from_raw(raw: rowan::SyntaxKind) -> Kind {
        Kind(raw.0)
    }

    #[inline]
    fn kind_to_raw(kind: Kind) -> rowan::SyntaxKind {
        rowan::SyntaxKind(kind.0)
    }
}

pub type SyntaxNode = rowan::SyntaxNode<Lang>;
pub type SyntaxToken = rowan::SyntaxToken<Lang>;
pub type SyntaxElement = rowan::SyntaxElement<Lang>;

/// A parsed source snapshot: the exact source text plus its green tree.
#[derive(Clone)]
pub struct Tree {
    source: Arc<str>,
    green: GreenNode,
}

impl std::fmt::Debug for Tree {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&crate::debug_tree(&self.root()))
    }
}

impl Tree {
    pub fn new(source: Arc<str>, green: GreenNode) -> Self {
        Self { source, green }
    }

    #[inline]
    pub fn source(&self) -> &str {
        &self.source
    }

    #[inline]
    pub fn source_arc(&self) -> &Arc<str> {
        &self.source
    }

    #[inline]
    pub fn green(&self) -> &GreenNode {
        &self.green
    }

    /// A fresh red root for traversal.
    #[inline]
    pub fn root(&self) -> SyntaxNode {
        SyntaxNode::new_root(self.green.clone())
    }

    /// The exact source slice covered by `range`.
    #[inline]
    pub fn text(&self, range: TextRange) -> &str {
        &self.source[range]
    }

    /// The exact source slice covered by a node of this tree.
    #[inline]
    pub fn node_text(&self, node: &SyntaxNode) -> &str {
        self.text(node.text_range())
    }
}

/// Convenience queries mirroring the Go `cst.Node` API.
pub trait SyntaxNodeExt {
    /// First direct child element of the given kind.
    fn child_of_kind(&self, kind: Kind) -> Option<SyntaxElement>;
    /// First direct child node of the given kind.
    fn child_node_of_kind(&self, kind: Kind) -> Option<SyntaxNode>;
    /// First direct child token of the given kind.
    fn child_token_of_kind(&self, kind: Kind) -> Option<SyntaxToken>;
    /// Nearest strict ancestor of the given kind.
    fn ancestor_of_kind(&self, kind: Kind) -> Option<SyntaxNode>;
    /// This node if it has the given kind, otherwise its nearest ancestor of
    /// that kind.
    fn ancestor_or_self_of_kind(&self, kind: Kind) -> Option<SyntaxNode>;
    /// First direct child token whose kind is in `kinds`.
    fn child_token_of_kinds(&self, kinds: &[Kind]) -> Option<SyntaxToken>;
    /// All nodes in the subtree (preorder, including `self`) whose kind is in
    /// `kinds`.
    fn descendants_of_kinds(&self, kinds: &[Kind]) -> Vec<SyntaxNode>;
    /// The node range with leading trivia tokens removed.
    fn range_trimmed_trivia(&self) -> TextRange;
    /// Token at `offset`, preferring the right token on a boundary. `None`
    /// when `offset` lies outside `[start, end)`.
    fn token_at(&self, offset: TextSize) -> Option<SyntaxToken>;
    /// Smallest element whose range fully contains `range` (right-biased for
    /// empty ranges on boundaries; zero-width children are skipped).
    fn descendant_for_range(&self, range: TextRange) -> Option<SyntaxElement>;
    /// Smallest node containing `offset`.
    fn node_at(&self, offset: TextSize) -> Option<SyntaxNode>;
}

impl SyntaxNodeExt for SyntaxNode {
    fn child_of_kind(&self, kind: Kind) -> Option<SyntaxElement> {
        self.children_with_tokens()
            .find(|child| child.kind() == kind)
    }

    fn child_node_of_kind(&self, kind: Kind) -> Option<SyntaxNode> {
        self.children().find(|child| child.kind() == kind)
    }

    fn child_token_of_kind(&self, kind: Kind) -> Option<SyntaxToken> {
        self.children_with_tokens()
            .filter_map(NodeOrToken::into_token)
            .find(|child| child.kind() == kind)
    }

    fn ancestor_of_kind(&self, kind: Kind) -> Option<SyntaxNode> {
        self.ancestors().skip(1).find(|node| node.kind() == kind)
    }

    fn ancestor_or_self_of_kind(&self, kind: Kind) -> Option<SyntaxNode> {
        self.ancestors().find(|node| node.kind() == kind)
    }

    fn child_token_of_kinds(&self, kinds: &[Kind]) -> Option<SyntaxToken> {
        self.children_with_tokens()
            .filter_map(NodeOrToken::into_token)
            .find(|child| kinds.contains(&child.kind()))
    }

    fn descendants_of_kinds(&self, kinds: &[Kind]) -> Vec<SyntaxNode> {
        self.descendants()
            .filter(|node| kinds.contains(&node.kind()))
            .collect()
    }

    fn range_trimmed_trivia(&self) -> TextRange {
        let range = self.text_range();
        let mut start = range.start();
        for event in self.preorder_with_tokens() {
            let WalkEvent::Enter(NodeOrToken::Token(token)) = event else {
                continue;
            };
            if !token.kind().is_trivia() {
                break;
            }
            let end = token.text_range().end();
            if end < range.end() {
                start = end;
            }
        }
        TextRange::new(start, range.end())
    }

    fn token_at(&self, offset: TextSize) -> Option<SyntaxToken> {
        let range = self.text_range();
        if offset < range.start() || offset >= range.end() {
            return None;
        }
        let mut current = self.clone();
        loop {
            let child = current.children_with_tokens().find(|child| {
                let r = child.text_range();
                offset >= r.start() && offset < r.end()
            })?;
            match child {
                NodeOrToken::Token(token) => return Some(token),
                NodeOrToken::Node(node) => current = node,
            }
        }
    }

    fn descendant_for_range(&self, range: TextRange) -> Option<SyntaxElement> {
        let node_range = self.text_range();
        if range.start() < node_range.start() || range.end() > node_range.end() {
            return None;
        }
        let mut result = NodeOrToken::Node(self.clone());
        let mut current = self.clone();
        'descend: loop {
            for child in current.children_with_tokens() {
                let r = child.text_range();
                if r.start() <= range.start() && range.start() < r.end() && range.end() <= r.end() {
                    match child {
                        NodeOrToken::Node(node) => {
                            result = NodeOrToken::Node(node.clone());
                            current = node;
                            continue 'descend;
                        }
                        token => return Some(token),
                    }
                }
            }
            return Some(result);
        }
    }

    fn node_at(&self, offset: TextSize) -> Option<SyntaxNode> {
        match self.descendant_for_range(TextRange::empty(offset))? {
            NodeOrToken::Node(node) => Some(node),
            NodeOrToken::Token(token) => token.parent(),
        }
    }
}

/// Convenience queries on tokens.
pub trait SyntaxTokenExt {
    fn ancestor_of_kind(&self, kind: Kind) -> Option<SyntaxNode>;
}

impl SyntaxTokenExt for SyntaxToken {
    fn ancestor_of_kind(&self, kind: Kind) -> Option<SyntaxNode> {
        self.parent_ancestors().find(|node| node.kind() == kind)
    }
}
