//! Twig block versioning hover (upstream hash and version-comment status).

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_BLOCK, TWIG_COMMENT};
use swlsp_syntax::rowan::Direction;
use swlsp_syntax::{SyntaxNode, SyntaxToken};
use swlsp_twig::{TwigFile, VersioningService};

use crate::hover::{markdown_hover, request_uri};

pub struct TwigVersioningHoverProvider {
    versioning: Option<Arc<VersioningService>>,
}

impl TwigVersioningHoverProvider {
    pub fn new(versioning: Option<Arc<VersioningService>>) -> Self {
        Self { versioning }
    }

    fn hover_block(
        &self,
        versioning: &VersioningService,
        file: &TwigFile,
        block_name: &str,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(block) = file.blocks.get(block_name) else {
            return Ok(None);
        };
        let resolution = versioning.resolve(file, block_name)?;
        let mut markdown = format!("**Block:** `{block_name}`\n\n");
        let Some(upstream) = resolution.candidates.first() else {
            if block.has_versioning_comment && resolution.parent_resolved {
                markdown.push_str("**Status:** Upstream block was removed\n\n");
            } else {
                markdown.push_str("**Status:** No resolvable upstream block\n\n");
            }
            return Ok(Some(markdown_hover(markdown, None)));
        };
        let _ = write!(markdown, "**Upstream:** `{}`\n\n", upstream.absolute_path);
        let _ = write!(markdown, "**Upstream hash:** `{}`\n\n", upstream.hash);
        let version = versioning.version_for_path(&upstream.absolute_path);
        if !version.is_empty() {
            let _ = write!(markdown, "**Upstream version:** `{version}`\n\n");
        }
        if resolution.candidates.len() > 1 {
            let _ = write!(
                markdown,
                "**Candidates:** {} compatible upstream blocks\n\n",
                resolution.candidates.len()
            );
        }
        let Some(comment) = block.version_comment.as_ref() else {
            if block.has_versioning_comment {
                markdown.push_str("**Status:** Version comment is malformed\n\n");
            } else {
                markdown.push_str("**Status:** No version comment\n\n");
            }
            return Ok(Some(markdown_hover(markdown, None)));
        };
        let _ = write!(markdown, "**Recorded hash:** `{}`\n\n", comment.hash);
        if !comment.version.is_empty() {
            let _ = write!(markdown, "**Recorded version:** `{}`\n\n", comment.version);
        }
        if resolution
            .candidates
            .iter()
            .any(|candidate| candidate.hash == comment.hash)
        {
            markdown.push_str("**Status:** Version comment is up to date\n\n");
        } else {
            markdown.push_str("**Status:** Upstream block has changed\n\n");
        }
        Ok(Some(markdown_hover(markdown, None)))
    }
}

fn block_at_identifier(node: &SyntaxNode, token: Option<&SyntaxToken>) -> Option<SyntaxNode> {
    let block = twigquery::block_at(node)?;
    let token = token?;
    let name = twigquery::block_name(&block).unwrap_or_default();
    (token.text() == name).then_some(block)
}

fn version_comment_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    let comment = twigquery::closest_node_of_kind(node, &[TWIG_COMMENT])?;
    comment
        .text()
        .to_string()
        .contains(swlsp_twig::VERSION_COMMENT_PREFIX)
        .then_some(comment)
}

fn find_block_name_after_comment(comment: &SyntaxNode) -> String {
    for sibling in comment.siblings(Direction::Next).skip(1) {
        if sibling.kind() == TWIG_BLOCK {
            return twigquery::block_name(&sibling).unwrap_or_default();
        }
    }
    String::new()
}

impl HoverProvider for TwigVersioningHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        if !request_uri(request).to_lowercase().ends_with(".twig") {
            return Ok(None);
        }
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let Some(versioning) = self.versioning.as_deref() else {
            return Ok(None);
        };
        let (Ok(path), Some(line_index)) = (
            swlsp_uriutil::path(request_uri(request)),
            request.line_index(),
        ) else {
            return Ok(None);
        };
        let file = swlsp_twig::parse_twig_tree(&path, request.tree(), line_index);
        if let Some(block) = block_at_identifier(node, request.token.as_ref()) {
            let name = twigquery::block_name(&block).unwrap_or_default();
            return self.hover_block(versioning, &file, &name);
        }
        let Some(comment) = version_comment_at(node) else {
            return Ok(None);
        };
        let block_name = find_block_name_after_comment(&comment);
        if block_name.is_empty() {
            return Ok(None);
        }
        self.hover_block(versioning, &file, &block_name)
    }
}
