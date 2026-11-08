//! Twig template files, blocks and block version comments (port of
//! `parser.go`).

use std::collections::BTreeMap;
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use swlsp_parser_twig::ast::{AstNode, TwigBlock as TwigBlockNode};
use swlsp_parser_twig::syntax::{TWIG_BLOCK, TWIG_COMMENT};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, Tree};

use crate::documentation::documentation_before;
use crate::path::{VERSION_COMMENT_PREFIX, bundle_name_by_path, convert_to_relative_path};
use crate::reference::{TemplateReferenceKind, twig_template_references};

static SHOPWARE_BLOCK_COMMENT: LazyLock<Regex> = LazyLock::new(|| {
    // Go's `\s` and `\w` are ASCII-only.
    Regex::new(&format!(
        r"\{{#[\t\n\f\r ]*{VERSION_COMMENT_PREFIX}[\t\n\f\r ]*([a-fA-F0-9]+)(?:@([0-9A-Za-z_.\-+]+))?[\t\n\f\r ]*#\}}"
    ))
    .expect("valid block comment pattern")
});

/// One parsed Twig template file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigFile {
    /// Name of the bundle.
    pub bundle_name: String,
    pub path: String,
    /// Relative path, used inside of Twig.
    pub rel_path: String,
    pub blocks: BTreeMap<String, TwigBlock>,
    pub extends_file: String,
    pub extends_tag_line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigVersionComment {
    pub hash: String,
    pub version: String,
    pub line: i64,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigBlockHash {
    pub name: String,
    pub relative_path: String,
    pub absolute_path: String,
    pub bundle_name: String,
    pub hash: String,
    pub text: String,
    pub line: i64,
    pub deprecation: String,
    pub has_versioning_comment: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigBlock {
    pub name: String,
    pub documentation: String,
    pub range: TextRange,
    pub name_range: TextRange,
    pub line: i64,
    pub hash: String,
    pub text: String,
    pub has_versioning_comment: bool,
    pub version_comment_range: Option<TextRange>,
    pub version_comment: Option<TwigVersionComment>,
    pub deprecation: String,
}

pub(crate) fn calculate_block_hash(content: &str) -> String {
    let digest = Sha256::digest(content.as_bytes());
    let mut out = String::with_capacity(64);
    for byte in digest {
        use std::fmt::Write;
        let _ = write!(out, "{byte:02x}");
    }
    out
}

pub fn parse_version_comment(comment: &str, line: i64) -> Option<TwigVersionComment> {
    let captures = SHOPWARE_BLOCK_COMMENT.captures(comment)?;
    Some(TwigVersionComment {
        hash: captures.get(1).map_or("", |m| m.as_str()).to_owned(),
        version: captures.get(2).map_or("", |m| m.as_str()).to_owned(),
        line,
        range: TextRange::default(),
    })
}

fn slice(source: &str, range: TextRange) -> &str {
    source
        .get(usize::from(range.start())..usize::from(range.end()))
        .unwrap_or_default()
}

fn line_of(line_index: &LineIndex, offset: rowan::TextSize) -> i64 {
    i64::from(line_index.position(u32::from(offset)).0) + 1
}

fn find_blocks(root: &SyntaxNode, source: &str, line_index: &LineIndex, file: &mut TwigFile) {
    for node in root.descendants() {
        let Some(block) = TwigBlockNode::cast(node.clone()) else {
            continue;
        };
        let Some(name) = block.name() else {
            continue;
        };
        let block_range = node.range_trimmed_trivia();
        let block_text = slice(source, block_range);
        let mut version_comment = None;
        let mut version_comment_range = None;
        let mut has_versioning_comment = false;
        let deprecation = block_deprecation(&node, source);
        for previous in find_previous_block_comments(&node) {
            let comment_range = previous.range_trimmed_trivia();
            let comment = slice(source, comment_range);
            if !comment.contains(VERSION_COMMENT_PREFIX) {
                continue;
            }
            has_versioning_comment = true;
            version_comment_range = Some(comment_range);
            version_comment =
                parse_version_comment(comment, line_of(line_index, comment_range.start())).map(
                    |mut parsed| {
                        parsed.range = comment_range;
                        parsed
                    },
                );
            break;
        }
        let name_text = name.text().to_owned();
        file.blocks.insert(
            name_text.clone(),
            TwigBlock {
                name: name_text,
                documentation: documentation_before(&node),
                range: block_range,
                name_range: name.text_range(),
                line: line_of(line_index, name.text_range().start()),
                hash: calculate_block_hash(block_text),
                text: block_text.to_owned(),
                has_versioning_comment,
                version_comment_range,
                version_comment,
                deprecation,
            },
        );
    }
}

/// The normalized `@deprecated` documentation from the Twig comment
/// immediately preceding a block declaration. Administration component
/// templates and Storefront block versioning share this source convention.
pub fn block_deprecation(block_node: &SyntaxNode, source: &str) -> String {
    for previous in find_previous_block_comments(block_node) {
        let range = previous.range_trimmed_trivia();
        let Some(comment) = source.get(usize::from(range.start())..usize::from(range.end())) else {
            continue;
        };
        let deprecation = parse_block_deprecation(comment);
        if !deprecation.is_empty() {
            return deprecation;
        }
    }
    String::new()
}

fn find_previous_block_comments(block_node: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    let mut sibling = block_node.prev_sibling_or_token();
    while let Some(current) = sibling {
        match current {
            NodeOrToken::Token(token) => sibling = token.prev_sibling_or_token(),
            NodeOrToken::Node(node) => {
                if node.kind() == TWIG_BLOCK {
                    return result;
                }
                if node.kind() == TWIG_COMMENT {
                    sibling = node.prev_sibling_or_token();
                    result.push(node);
                    continue;
                }
                return result;
            }
        }
    }
    result
}

fn parse_block_deprecation(comment: &str) -> String {
    // Go lowercases a copy and reuses the byte index; ASCII lowercasing keeps
    // byte offsets identical for the ASCII marker.
    let lower = comment.to_ascii_lowercase();
    let Some(index) = lower.find("@deprecated") else {
        return String::new();
    };
    let message = comment[index + "@deprecated".len()..].trim();
    let message = message.strip_suffix("#}").unwrap_or(message).trim();
    if message.is_empty() {
        return "This Twig block is deprecated".to_owned();
    }
    message.to_owned()
}

/// Parses a Twig source that is not part of the index.
pub fn parse_twig(file_path: &str, content: &str) -> TwigFile {
    if !content.contains("{%") {
        return new_twig_file(file_path);
    }
    let parse = swlsp_parser_twig::parse(content);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_twig_tree(file_path, Some(&parse.tree), &line_index)
}

pub(crate) fn new_twig_file(file_path: &str) -> TwigFile {
    TwigFile {
        path: file_path.to_owned(),
        bundle_name: bundle_name_by_path(file_path),
        rel_path: convert_to_relative_path(file_path),
        ..TwigFile::default()
    }
}

pub fn parse_twig_tree(file_path: &str, tree: Option<&Tree>, line_index: &LineIndex) -> TwigFile {
    let mut file = new_twig_file(file_path);
    let Some(tree) = tree else {
        return file;
    };
    let source = tree.source();
    if !source.contains("{%") {
        return file;
    }
    let root = tree.root();
    if source.contains("block") {
        find_blocks(&root, source, line_index, &mut file);
    }
    if !source.contains("extends") && !source.contains("sw_extends") {
        return file;
    }
    for reference in twig_template_references(file_path, &root) {
        if reference.kind != TemplateReferenceKind::Extends {
            continue;
        }
        file.extends_file = reference.template;
        file.extends_tag_line = line_of(line_index, reference.range.start());
        return file;
    }
    file
}
