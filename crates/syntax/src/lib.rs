//! Language-neutral lossless syntax infrastructure shared by every parser.

mod debug;
pub mod dump;
mod kind;
mod line_index;
pub mod parse;
mod tree;

pub use debug::debug_tree;
pub use kind::{Kind, LanguageSpec};
pub use line_index::{LineIndex, utf16_len};
pub use parse::{
    CompletedMarker, Config, ErrorBuilder, FollowingContent, Marker, ParseError, Parser, Token,
};
pub use tree::{
    Lang, SyntaxElement, SyntaxNode, SyntaxNodeExt, SyntaxToken, SyntaxTokenExt, TextRange,
    TextSize, Tree,
};

#[doc(hidden)]
pub use inventory;
pub use rowan;

/// Result of parsing one source snapshot.
#[derive(Clone, Debug)]
pub struct Parse {
    pub tree: Tree,
    pub errors: Vec<ParseError>,
}

impl Parse {
    pub fn root(&self) -> SyntaxNode {
        self.tree.root()
    }

    /// Debug tree followed by one line per error, the format of parser
    /// snapshot tests and CST dumps.
    pub fn debug_dump(&self) -> String {
        let mut out = debug_tree(&self.tree.root());
        for error in &self.errors {
            out.push('\n');
            out.push_str(&error.to_string());
        }
        out
    }
}
