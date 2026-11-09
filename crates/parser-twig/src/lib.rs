//! Lossless, error-tolerant Twig + HTML parser, plus typed AST wrappers,
//! semantic queries and a formatter built on the resulting
//! [`swlsp_syntax::SyntaxNode`] tree.

pub mod ast;
pub mod formatter;
pub mod lexer;
mod parser;
pub mod query;
pub mod syntax;

pub use parser::{parse, parse_shared};
