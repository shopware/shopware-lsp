//! Constructor parameters typed with a concrete decorator.

use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, SymbolKind};

use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

/// Constructor parameters typed with a concrete decorator instead of its
/// decoration abstraction.
#[derive(Clone)]
pub struct ShopwareDecorationAnalyzer {
    php: Option<PhpIndex>,
}

impl ShopwareDecorationAnalyzer {
    pub fn new(index: Option<PhpIndex>) -> Self {
        Self { php: index }
    }
}

impl DeclaredAnalyzer for ShopwareDecorationAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.decoration",
        description: "Decorators type-hinting the concrete service instead of its abstract class.",
        domain: "shopware",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "shopware-lsp",
        codes: &["shopware.decoration.abstraction"],
    };
}

impl ProblemAnalyzer for ShopwareDecorationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php else {
            return Ok(Vec::new());
        };
        if document.tree().is_none() || !document.uri.to_lowercase().ends_with(".php") {
            return Ok(Vec::new());
        }
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        for symbol in &state.document.symbols {
            ctx.check()?;
            if symbol.kind != SymbolKind::Method || &*symbol.name != "__construct" {
                continue;
            }
            for parameter in &symbol.parameters {
                let type_name = parameter.ty.name();
                let concrete_name = type_name.strip_prefix('\\').unwrap_or(type_name);
                if concrete_name.is_empty() {
                    continue;
                }
                let Some(concrete) = php.find_class(concrete_name) else {
                    continue;
                };
                if concrete.flags.has(Flags::ABSTRACT) {
                    continue;
                }
                for parent_name in concrete.extends() {
                    let Some(parent) = php.find_class(parent_name) else {
                        continue;
                    };
                    if !parent.flags.has(Flags::ABSTRACT)
                        || php
                            .find_methods(&parent.fully_qualified, "getDecorated")
                            .is_empty()
                    {
                        continue;
                    }
                    let abstract_class = parent
                        .fully_qualified
                        .strip_prefix('\\')
                        .unwrap_or(&parent.fully_qualified)
                        .to_owned();
                    let mut problem = warning(
                        "shopware.decoration.abstraction",
                        parameter.range,
                        format!(
                            "Depend on decoration abstraction {abstract_class} instead of concrete class {concrete_name}"
                        ),
                    );
                    problem.payload = Some(json!({
                        "abstractClass": abstract_class,
                        "concreteClass": concrete_name,
                    }));
                    result.push(problem);
                    break;
                }
            }
        }
        Ok(result)
    }
}
