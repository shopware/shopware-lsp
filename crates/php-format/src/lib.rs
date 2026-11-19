//! A PHP formatter for PER Coding Style 2.0.
//!
//! The formatter works on the token stream of the native PHP tree: it
//! decides what stands between each two tokens (nothing, a space, or a line
//! break with an indentation) and writes the tokens back in their order.
//! No token is moved, so what it cannot lay out it leaves as it found it.

mod casing;
mod emit;
mod heredoc;
mod layout;
mod normalize;
mod spacing;
mod template;
mod tokens;
mod verify;
mod wrap;

use swlsp_syntax::SyntaxNode;
use tokens::Doc;

#[cfg(test)]
mod tests;

/// Why a document was not formatted.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Skipped {
    /// The document has syntax errors.
    SyntaxError,
    /// The document holds something the formatter does not lay out: markup
    /// outside PHP tags, or syntax the parser does not know.
    Unsupported,
    /// The formatted document would not hold the same code. This is a bug
    /// of the formatter, which the check keeps from reaching a file.
    Unsafe,
}

/// The choices PER Coding Style leaves open, and the one place a project
/// may want to differ from it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Options {
    /// Keep `<?php declare(strict_types=1);` on one line where the source
    /// has it so. PER Coding Style puts the opening tag on a line of its
    /// own; Shopware does not.
    pub declare_on_opening_tag: bool,
    /// The length lines are split at. PER Coding Style asks for lines of
    /// no more than 80 characters where possible, and sets a soft limit
    /// of 120.
    pub line_width: usize,
}

/// The line length PER Coding Style recommends.
pub const RECOMMENDED_LINE_WIDTH: usize = 80;
/// The soft limit of PER Coding Style on the length of a line.
pub const SOFT_LINE_WIDTH: usize = 120;

impl Default for Options {
    fn default() -> Self {
        Self {
            declare_on_opening_tag: false,
            line_width: RECOMMENDED_LINE_WIDTH,
        }
    }
}

/// `source` formatted as a whole document. `None` when the document is not
/// formatted: it is then left as it is.
pub fn format(source: &str, options: Options) -> Option<String> {
    try_format(source, options).ok()
}

/// Like [`format`], telling why a document is not formatted.
pub fn try_format(source: &str, options: Options) -> Result<String, Skipped> {
    let parse = swlsp_parser_php::parse(source);
    try_format_tree(source, &parse.root(), !parse.errors.is_empty(), options)
}

/// The line a script starts with to name its interpreter (`#!/usr/bin/env
/// php`), and the PHP after it.
fn shebang(source: &str) -> Option<(&str, &str)> {
    if !source.starts_with("#!") {
        return None;
    }
    let end = source.find('\n')? + 1;
    Some((&source[..end], &source[end..]))
}

/// Like [`try_format`], for a document that is parsed already: `root` is
/// the tree of `source`, and `has_errors` tells whether parsing it found
/// syntax errors.
pub fn try_format_tree(
    source: &str,
    root: &SyntaxNode,
    has_errors: bool,
    options: Options,
) -> Result<String, Skipped> {
    // An empty file has nothing to format.
    if source.trim().is_empty() {
        return Ok(String::new());
    }
    // The interpreter line of a script is not PHP: what follows it is
    // formatted as a document of its own.
    if let Some((line, script)) = shebang(source) {
        let formatted = try_format(script, options)?;
        return Ok(format!("{}\n{formatted}", line.trim_end()));
    }
    if tokens::has_markup(source, root) {
        return format_template(source, root, has_errors);
    }
    let normalized = normalized(source, root, has_errors)?;
    let parse = swlsp_parser_php::parse(&normalized);
    let root = parse.root();
    let mut doc = Doc::new(&normalized, &root).ok_or(Skipped::Unsafe)?;
    casing::run(&mut doc);
    let formatted = wrap::run(&mut doc, &root, options);
    let check = swlsp_parser_php::parse(&formatted);
    if !check.errors.is_empty() || !verify::same_code(&root, &check.root()) {
        return Err(Skipped::Unsafe);
    }
    Ok(formatted)
}

/// A document with markup outside PHP tags: its lines stay, the spaces
/// inside them are set.
fn format_template(source: &str, root: &SyntaxNode, has_errors: bool) -> Result<String, Skipped> {
    if has_errors {
        return Err(Skipped::SyntaxError);
    }
    let mut doc = Doc::template(source, root).ok_or(Skipped::Unsupported)?;
    casing::run(&mut doc);
    let formatted = template::emit(&doc);
    let check = swlsp_parser_php::parse(&formatted);
    if !check.errors.is_empty() || !verify::same_code(root, &check.root()) {
        return Err(Skipped::Unsafe);
    }
    Ok(formatted)
}

/// `source` with the changes made that are more than white space.
fn normalized(source: &str, original: &SyntaxNode, has_errors: bool) -> Result<String, Skipped> {
    if has_errors {
        return Err(Skipped::SyntaxError);
    }
    let doc = Doc::new(source, original).ok_or(Skipped::Unsupported)?;
    let edits = normalize::edits(&doc, original);
    if edits.is_empty() {
        return Ok(source.to_owned());
    }
    let mut text = normalize::apply(source, edits);
    // An edit may make room for another: a few rounds settle them.
    for _ in 0..3 {
        let parse = swlsp_parser_php::parse(&text);
        if !parse.errors.is_empty() {
            return Err(Skipped::Unsafe);
        }
        let root = parse.root();
        let doc = Doc::new(&text, &root).ok_or(Skipped::Unsafe)?;
        let edits = normalize::edits(&doc, &root);
        if edits.is_empty() {
            break;
        }
        text = normalize::apply(&text, edits);
    }
    let parse = swlsp_parser_php::parse(&text);
    if !parse.errors.is_empty() || !verify::same_content(original, &parse.root()) {
        return Err(Skipped::Unsafe);
    }
    Ok(text)
}
