//! Go `admin_twig_migration.go`: Shopware 6.7 `sw-*` → `mt-*` component
//! migrations.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_admin_twigmigration as twigmigration;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_twig::ast::{AstNode, HtmlTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_php_types::go_to_lower;
use swlsp_shopware::ResolvedVersion;
use swlsp_syntax::SyntaxElement;

use super::go_ext;
use crate::ProblemAnalyzer;

/// The payload of an `admin.twig.migration.*` problem.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AdminTwigMigrationPayload {
    #[serde(default)]
    pub rule: String,
    #[serde(default)]
    pub source_tag: String,
    #[serde(default)]
    pub target_tag: String,
    #[serde(default)]
    pub safe: bool,
}

/// Go `AdminTwigMigrationAnalyzer`.
#[derive(Clone, Debug, Default)]
pub struct AdminTwigMigrationAnalyzer {
    version: ResolvedVersion,
}

impl AdminTwigMigrationAnalyzer {
    pub fn new(version: ResolvedVersion) -> Self {
        Self { version }
    }
}

impl ProblemAnalyzer for AdminTwigMigrationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !self.version.at_least(6, 7, 0) || document.language != Some(LanguageId::Twig) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !document.uri.contains("Resources/app/administration")
            || go_to_lower(go_ext(&document.uri)) != ".twig"
        {
            return Ok(Vec::new());
        }
        let mut problems = Vec::new();
        for node in twigquery::iterate_nodes(&root, &[twigsyntax::HTML_TAG]) {
            ctx.check()?;
            let Some(name) = HtmlTag::cast(node.clone()).and_then(|tag| tag.name()) else {
                continue;
            };
            let Some(rule) = twigmigration::rule_for_tag(name.text()) else {
                continue;
            };
            let compiled = twigmigration::compile(document.source(), &node, rule);
            if let Err(error) = &compiled
                && !error.is_unsafe()
            {
                return Err(Box::new(error.clone()));
            }
            let value = AdminTwigMigrationPayload {
                rule: rule.id.to_owned(),
                source_tag: rule.source_tag.to_owned(),
                target_tag: rule.target_tag.to_owned(),
                safe: compiled.is_ok(),
            };
            problems.push(Problem {
                id: format!("admin.twig.migration.{}", rule.id).as_str().into(),
                range: name.text_range(),
                element: Some(SyntaxElement::Node(node.clone())),
                message: rule.message.to_owned(),
                severity: Some(DiagnosticSeverity::WARNING),
                source: "shopware-lsp".into(),
                tags: vec![DiagnosticTag::DEPRECATED],
                payload: Some(serde_json::to_value(&value)?),
                ..Problem::default()
            });
        }
        Ok(problems)
    }
}
