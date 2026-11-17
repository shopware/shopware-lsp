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
use crate::diagnostics::common::{admin_nearby_suggestions, javascript_string_content_range};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

/// An entity every Shopware has: where it is indexed, the entities of the
/// project are known, and an unknown name is unknown.
const CORE_ENTITY: &str = "product";

/// Custom entities are created at runtime, from an app's `entities.xml` or
/// in the Administration, and named with one of these prefixes.
fn is_runtime_entity(name: &str) -> bool {
    name.starts_with("custom_entity_") || name.starts_with("ce_")
}

/// Statically named DAL entities no definition declares: a close
/// misspelling with the name it resembles, and, where Shopware's own
/// entities are indexed, any other unknown name that is no runtime custom
/// entity.
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
        Some(crate::diagnostics::inputs(&self.index))
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
            // Without a close name the entity may be one nothing in the
            // workspace declares: a custom entity created at runtime, or
            // any entity at all where Shopware itself is not indexed.
            if suggestions.is_empty()
                && (is_runtime_entity(&reference.name) || !known.contains(CORE_ENTITY))
            {
                continue;
            }
            let mut problem = warning(
                "shopware.dal.entity-not-found",
                javascript_string_content_range(&literal, document.source()),
                if suggestions.is_empty() {
                    format!(
                        "No entity definition declares '{}'; the repository cannot be created",
                        reference.name
                    )
                } else {
                    format!("Shopware DAL entity '{}' is not indexed", reference.name)
                },
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
