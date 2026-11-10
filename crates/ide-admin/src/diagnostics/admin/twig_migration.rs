//! Shopware 6.7 `sw-*` → `mt-*` component migrations.
//!
//! Shopware keeps the replaced `sw-*` components for a while as compatibility
//! wrappers, so whether a tag is a problem depends on the project's own
//! Administration, not on the version alone: a component that is no longer
//! registered is removed, one that is registered with a `@deprecated` note is
//! deprecated, and one that is registered without a note is fine.

use std::collections::HashMap;
use std::collections::hash_map::Entry;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_admin::AdminComponentIndexer;
use swlsp_admin_twigmigration as twigmigration;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_twig::ast::{AstNode, HtmlTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_shopware::ResolvedVersion;
use swlsp_syntax::SyntaxElement;
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

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

#[derive(Clone, Default)]
pub struct AdminTwigMigrationAnalyzer {
    index: Option<Arc<AdminComponentIndexer>>,
    version: ResolvedVersion,
}

/// What the project's Administration says about a replaced component.
enum Legacy {
    /// No longer registered.
    Removed,
    /// Still registered, with this deprecation note.
    Deprecated(String),
    /// Still registered and not deprecated, or not known yet.
    Supported,
}

impl AdminTwigMigrationAnalyzer {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>, version: ResolvedVersion) -> Self {
        Self { index, version }
    }

    fn legacy(&self, index: &AdminComponentIndexer, tag: &str) -> Result<Legacy, BoxError> {
        let components = index.get_component_with_definition(tag)?;
        if let Some(note) = components
            .iter()
            .map(|component| component.deprecated.trim())
            .find(|note| !note.is_empty())
        {
            return Ok(Legacy::Deprecated(note.to_owned()));
        }
        if !components.is_empty() {
            return Ok(Legacy::Supported);
        }
        // During an initial or partial index there is not enough evidence
        // to claim that a component is gone.
        match index.get_all_components_view() {
            Ok(all) if !all.is_empty() => Ok(Legacy::Removed),
            _ => Ok(Legacy::Supported),
        }
    }
}

impl ProblemAnalyzer for AdminTwigMigrationAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only Administration templates are analyzed, against the
        // Administration index and its open-document overlays.
        if !document.uri.contains("Resources/app/administration") {
            Some(0)
        } else {
            Some(crate::diagnostics::inputs(&self.index))
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !self.version.at_least(6, 7, 0) || document.language != Some(LanguageId::Twig) {
            return Ok(Vec::new());
        }
        let Some(index) = self.index.as_deref() else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !document.uri.contains("Resources/app/administration")
            || lowercase(filepath::ext(&document.uri)) != ".twig"
        {
            return Ok(Vec::new());
        }
        let mut problems = Vec::new();
        let mut states: HashMap<&str, Legacy> = HashMap::new();
        for node in twigquery::iterate_nodes(&root, &[twigsyntax::HTML_TAG]) {
            ctx.check()?;
            let Some(name) = HtmlTag::cast(node.clone()).and_then(|tag| tag.name()) else {
                continue;
            };
            let Some(rule) = twigmigration::rule_for_tag(name.text()) else {
                continue;
            };
            // One lookup per component, however often a template uses it.
            let state = match states.entry(rule.id) {
                Entry::Occupied(entry) => entry.into_mut(),
                Entry::Vacant(entry) => entry.insert(self.legacy(index, rule.source_tag)?),
            };
            let message = match state {
                Legacy::Removed => rule.removed_message(),
                Legacy::Deprecated(note) => rule.deprecated_message(note),
                Legacy::Supported => continue,
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
                message,
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
