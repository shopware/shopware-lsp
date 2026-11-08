//! Go `twig_versioning.go`: Shopware Twig block versioning diagnostics.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php_types::go_quote;
use swlsp_twig::{TwigBlockHash, VersioningService};

use super::go_ext;
use crate::ProblemAnalyzer;

pub const TWIG_VERSIONING_ORIGINAL_MISSING_CODE: &str = "twig.versioning.original_missing";
pub const TWIG_VERSIONING_OUTDATED_CODE: &str = "twig.versioning.outdated";
pub const TWIG_VERSIONING_COMMENT_MISSING_CODE: &str = "twig.versioning.comment_missing";
pub const TWIG_BLOCK_DEPRECATED_CODE: &str = "twig.block.deprecated";
pub const TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE: &str = "twig.block.redundant_override";

/// Go `TwigVersioningPayload`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigVersioningPayload {
    #[serde(default)]
    pub block_name: String,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub has_comment: bool,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub core_diff: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub recorded_version: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub upstream_path: String,
}

/// Go `TwigVersioningAnalyzer`.
pub struct TwigVersioningAnalyzer {
    versioning: Option<Arc<VersioningService>>,
}

impl TwigVersioningAnalyzer {
    pub fn new(versioning: Option<Arc<VersioningService>>) -> Self {
        Self { versioning }
    }
}

fn payload_value(payload: &TwigVersioningPayload) -> Option<serde_json::Value> {
    serde_json::to_value(payload).ok()
}

impl ProblemAnalyzer for TwigVersioningAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(versioning) = &self.versioning else {
            return Ok(Vec::new());
        };
        if go_ext(&document.uri) != ".twig" {
            return Ok(Vec::new());
        }
        let file_path = swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone());
        if swlsp_twig::is_upstream_template(&file_path) {
            return Ok(Vec::new());
        }
        let current =
            swlsp_twig::parse_twig_tree(&file_path, document.tree(), document.line_index());
        if current.extends_file.is_empty() {
            return Ok(Vec::new());
        }
        let resolutions = versioning.resolve_blocks(&current)?;
        let bodies = match document.root() {
            Some(root) => swlsp_twig::block_bodies(&root, document.source()),
            None => Default::default(),
        };

        let mut problems = Vec::with_capacity(current.blocks.len());
        let empty = swlsp_twig::UpstreamResolution::default();
        for block in current.blocks.values() {
            ctx.check()?;
            let resolution = resolutions.get(&block.name).unwrap_or(&empty);
            let mut payload = TwigVersioningPayload {
                block_name: block.name.clone(),
                ..TwigVersioningPayload::default()
            };
            if let Some(first) = resolution.candidates.first() {
                payload.upstream_path = first.absolute_path.clone();
            }

            if let Some(candidate) = resolution
                .candidates
                .iter()
                .find(|candidate| !candidate.deprecation.is_empty())
            {
                problems.push(Problem {
                    id: TWIG_BLOCK_DEPRECATED_CODE.into(),
                    range: block.name_range,
                    message: candidate.deprecation.clone(),
                    tags: vec![protocol::DiagnosticTag::DEPRECATED],
                    payload: payload_value(&payload),
                    related_information: vec![block_related_information(
                        candidate,
                        "Deprecated upstream block",
                    )],
                    ..Problem::default()
                });
            }

            let body = bodies.get(&block.range);
            if body.is_some_and(|body| swlsp_twig::is_parent_delegation(&body.text)) {
                continue;
            }
            let parent = swlsp_twig::redundant_block_override(block, resolution);
            if let (Some(parent), Some(body)) = (&parent, body)
                && !body.text.trim().is_empty()
            {
                payload.upstream_path = parent.absolute_path.clone();
                problems.push(Problem {
                    id: TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE.into(),
                    range: block.name_range,
                    message: format!(
                        "The block {} duplicates its resolved parent; delegate to parent() instead",
                        go_quote(&block.name)
                    ),
                    payload: payload_value(&payload),
                    related_information: vec![block_related_information(
                        parent,
                        "Identical parent block",
                    )],
                    ..Problem::default()
                });
                continue;
            }

            if resolution.candidates.is_empty() {
                if block.has_versioning_comment && resolution.parent_resolved {
                    let locations = versioning.other_locations(&block.name, &file_path)?;
                    let related = locations
                        .iter()
                        .map(|location| {
                            block_related_information(
                                location,
                                "A block with the same name still exists here",
                            )
                        })
                        .collect();
                    let message = if locations.is_empty() {
                        format!(
                            "The upstream block {} has been removed; check whether this override is still needed",
                            go_quote(&block.name)
                        )
                    } else {
                        format!(
                            "The upstream block {} was removed from this template but still exists elsewhere",
                            go_quote(&block.name)
                        )
                    };
                    problems.push(Problem {
                        id: TWIG_VERSIONING_ORIGINAL_MISSING_CODE.into(),
                        range: block.name_range,
                        message,
                        payload: payload_value(&payload),
                        related_information: related,
                        ..Problem::default()
                    });
                }
                continue;
            }

            if !block.has_versioning_comment {
                problems.push(Problem {
                    id: TWIG_VERSIONING_COMMENT_MISSING_CODE.into(),
                    range: block.name_range,
                    message: format!(
                        "The block {} does not have a versioning comment",
                        go_quote(&block.name)
                    ),
                    payload: payload_value(&payload),
                    ..Problem::default()
                });
                continue;
            }
            payload.has_comment = true;
            let Some(comment) = &block.version_comment else {
                problems.push(Problem {
                    id: TWIG_VERSIONING_COMMENT_MISSING_CODE.into(),
                    range: block.version_comment_range.unwrap_or(block.name_range),
                    message: "The Twig block versioning comment is malformed".into(),
                    payload: payload_value(&payload),
                    ..Problem::default()
                });
                continue;
            };
            payload.recorded_version = comment.version.clone();
            if resolution
                .candidates
                .iter()
                .any(|candidate| candidate.hash == comment.hash)
            {
                continue;
            }
            let selected = &resolution.candidates[0];
            payload.core_diff = swlsp_twig::is_storefront_template(&selected.absolute_path)
                && !comment.version.is_empty();
            problems.push(Problem {
                id: TWIG_VERSIONING_OUTDATED_CODE.into(),
                range: comment.range,
                message: format!(
                    "The upstream block has changed; review {} and update the versioning comment",
                    selected.relative_path
                ),
                payload: payload_value(&payload),
                related_information: vec![block_related_information(
                    selected,
                    "Current upstream block",
                )],
                ..Problem::default()
            });
        }
        Ok(problems)
    }
}

/// Go `blockRelatedInformation`.
fn block_related_information(
    block: &TwigBlockHash,
    message: &str,
) -> protocol::DiagnosticRelatedInformation {
    let line = u32::try_from((block.line - 1).max(0)).unwrap_or(u32::MAX);
    let position = protocol::Position { line, character: 0 };
    protocol::DiagnosticRelatedInformation {
        location: protocol::Location {
            uri: swlsp_uriutil::file_uri(&block.absolute_path),
            range: protocol::Range {
                start: position,
                end: position,
            },
        },
        message: message.to_owned(),
    }
}
