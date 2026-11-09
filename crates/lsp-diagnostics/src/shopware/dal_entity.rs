//! Misspelled DAL entity names.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::JS_STRING;
use swlsp_shopware_dal::{self as dal, JsEntityReferenceKind};

use super::util::warning;
use crate::common::{admin_nearby_suggestions, javascript_string_content_range};
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

/// Only close misspellings of statically named DAL entities are reported.
/// Runtime custom entities are valid, so an unknown name without a strong
/// indexed suggestion stays untouched.
#[derive(Clone)]
pub struct DalEntityAnalyzer {
    index: Option<Arc<dal::Index>>,
}

impl DalEntityAnalyzer {
    pub fn new(index: Option<Arc<dal::Index>>) -> Self {
        Self { index }
    }
}

impl DeclaredAnalyzer for DalEntityAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.dal.entity",
        description: "Administration repository calls for entity names no entity definition declares.",
        domain: "shopware",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::JavaScript, LanguageId::Vue],
        source: "shopware-lsp",
        codes: &["shopware.dal.entity-not-found"],
    };
}

impl ProblemAnalyzer for DalEntityAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        // Definitions are loaded on the first entity reference: most
        // JavaScript files have none, and listing them decodes every
        // indexed definition.
        let mut catalog: Option<(HashSet<String>, Vec<String>)> = None;
        let mut result = Vec::new();
        for literal in jsquery::nodes(&root, &[JS_STRING]) {
            ctx.check()?;
            let Some(reference) = dal::js_entity_reference_at(&literal) else {
                continue;
            };
            if reference.kind == JsEntityReferenceKind::DefinitionHas {
                continue;
            }
            if catalog.is_none() {
                let definitions = index.definitions()?;
                if definitions.is_empty() {
                    return Ok(Vec::new());
                }
                let mut known = HashSet::with_capacity(definitions.len());
                let mut names = Vec::with_capacity(definitions.len());
                for definition in definitions {
                    if definition.name.is_empty() || known.contains(&definition.name) {
                        continue;
                    }
                    known.insert(definition.name.clone());
                    names.push(definition.name);
                }
                names.sort();
                catalog = Some((known, names));
            }
            let (known, names) = catalog.as_ref().expect("catalog loaded");
            if known.contains(reference.name.as_str()) {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&reference.name, names);
            if suggestions.is_empty() {
                continue;
            }
            let mut problem = warning(
                "shopware.dal.entity-not-found",
                javascript_string_content_range(&literal, document.source()),
                format!("Shopware DAL entity '{}' is not indexed", reference.name),
            );
            problem.element = Some(literal.clone().into());
            problem.payload = Some(json!({
                "entityName": reference.name,
                "suggestions": suggestions,
            }));
            result.push(problem);
        }
        Ok(result)
    }
}
