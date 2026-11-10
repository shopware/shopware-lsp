//! ACL privileges, block overrides and the start-tag walk.

use std::collections::{HashMap, HashSet};

use swlsp_admin::{AdminSymbolKind, ComponentRegistrationKind, TwigBlock};
use swlsp_lsp::{BoxError, Context, Problem};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_twig::ast::{AstNode, TwigBlock as TwigBlockNode};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_suggestion::similar;
use swlsp_syntax::SyntaxNode;

use super::analyzer::AdminAnalyzer;
use super::twig::AdminTwigDiagnosticDocument;
use super::{payload, string_array, text};
use crate::diagnostics::common::admin_nearby_suggestions;

impl AdminAnalyzer {
    pub(super) fn twig_privilege_diagnostics(
        &self,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let references: Vec<_> = analysis
            .registry_references
            .iter()
            .filter(|reference| {
                reference.kind == AdminSymbolKind::Privilege && !reference.name.is_empty()
            })
            .collect();
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let privileges = indexer.get_all_privileges()?;
        if privileges.is_empty() {
            return Ok(Vec::new());
        }
        let mut known = HashSet::new();
        let mut names = Vec::new();
        let mut has_project_privileges = false;
        for privilege in &privileges {
            if privilege.name.is_empty() {
                continue;
            }
            if !privilege.is_builtin() {
                has_project_privileges = true;
            }
            if !known.insert(privilege.name.clone()) {
                continue;
            }
            names.push(privilege.name.clone());
        }
        // Preserve the analyzer's fail-open behavior for an empty or
        // not-yet-built project index. The built-in administrator key remains
        // available to completion and hover without making it look like a
        // complete ACL registry.
        if !has_project_privileges {
            return Ok(Vec::new());
        }
        let mut diagnostics = Vec::new();
        for reference in references {
            if known.contains(&reference.name) {
                continue;
            }
            diagnostics.push(Problem {
                range: reference.range,
                message: format!(
                    "Administration privilege '{}' is not registered",
                    reference.name
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.privilege.not-found".into(),
                payload: Some(payload(vec![
                    ("privilegeName", text(&reference.name)),
                    (
                        "suggestions",
                        string_array(similar(&reference.name, &names)),
                    ),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }

    /// Checks block overrides against the source registration's parent
    /// contract.
    pub(super) fn check_block_references(
        &self,
        root: &SyntaxNode,
        analysis: &AdminTwigDiagnosticDocument,
        diagnostics: &mut Vec<Problem>,
    ) {
        let Some(owner) = &analysis.live_owner else {
            return;
        };
        if owner.kind != ComponentRegistrationKind::Extend
            && owner.kind != ComponentRegistrationKind::Override
            && owner.extends_component.is_empty()
        {
            return;
        }
        let Some(indexer) = self.indexer() else {
            return;
        };
        let Ok(Some(parent)) = indexer.get_parent_component_for_template(&analysis.template_path)
        else {
            return;
        };
        let mut blocks: HashMap<String, TwigBlock> = HashMap::new();
        let mut names = Vec::new();
        for block in &parent.blocks {
            if block.name.is_empty() {
                continue;
            }
            blocks.insert(block.name.clone(), block.clone());
            names.push(block.name.clone());
        }
        find_block_tags(root, &parent.name, &blocks, &names, diagnostics);
    }

    pub(super) fn find_html_start_tags(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        source: &str,
        analysis: &mut AdminTwigDiagnosticDocument,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
            if ctx.is_cancelled() {
                return Ok(());
            }
            self.check_component_slot_names(
                &start_tag,
                &analysis.template_path,
                analysis.live_owner.as_ref(),
                diagnostics,
            )?;
            self.check_component_props(root, source, &start_tag, analysis, diagnostics)?;
        }
        Ok(())
    }
}

fn find_block_tags(
    node: &SyntaxNode,
    parent_name: &str,
    valid_blocks: &HashMap<String, TwigBlock>,
    block_names: &[String],
    diagnostics: &mut Vec<Problem>,
) {
    for block_node in twigquery::iterate_nodes(node, &[twigsyntax::TWIG_BLOCK]) {
        let block_name = twigquery::block_name(&block_node).unwrap_or_default();
        let Some(name_token) =
            TwigBlockNode::cast(block_node.clone()).and_then(|block| block.name())
        else {
            continue;
        };
        if block_name.is_empty() {
            continue;
        }
        let Some(parent_block) = valid_blocks.get(&block_name) else {
            let suggestions = admin_nearby_suggestions(&block_name, block_names);
            if suggestions.is_empty() {
                // Component.extend/override templates may introduce their
                // own extensibility blocks; absence from the parent is not
                // itself an error. Only a close parent-block spelling gives
                // enough evidence to report a likely typo.
                continue;
            }
            diagnostics.push(Problem {
                range: name_token.text_range(),
                message: format!(
                    "Block '{block_name}' does not exist in parent component '{parent_name}'"
                ),
                source: "shopware-lsp".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.block-not-found".into(),
                payload: Some(payload(vec![
                    ("blockName", text(&block_name)),
                    ("suggestions", string_array(suggestions)),
                ])),
                ..Problem::default()
            });
            continue;
        };
        if !parent_block.deprecated.is_empty() {
            diagnostics.push(Problem {
                range: name_token.text_range(),
                message: format!(
                    "Administration Twig block '{block_name}' is deprecated: {}",
                    parent_block.deprecated
                ),
                source: "shopware-lsp".into(),
                severity: Some(DiagnosticSeverity::HINT),
                id: "admin.component.deprecated-block".into(),
                tags: vec![DiagnosticTag::DEPRECATED],
                ..Problem::default()
            });
        }
    }
}
