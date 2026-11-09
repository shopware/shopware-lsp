//! Go `validation_diagnostics.go`.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::PhpIndex;
use swlsp_php_types::go_to_lower;
use swlsp_validation::{
    constraint_properties_in_context, find_constraint_property, option_reference_at,
};

use super::symfony_warning;
use crate::common::value_node_text_range;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_CONSTRAINT_OPTION_CODE: &str = "symfony.validation.option.missing";

pub struct ValidationAnalyzer {
    php_index: Option<PhpIndex>,
}

impl ValidationAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for ValidationAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.validation",
        description: "Validation constraints using options the constraint does not accept.",
        domain: "symfony.validation",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[MISSING_CONSTRAINT_OPTION_CODE],
    };
}

impl ProblemAnalyzer for ValidationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(php_index), Some(root)) = (&self.php_index, document.root()) else {
            return Ok(Vec::new());
        };
        if !go_to_lower(&document.uri).ends_with(".php") {
            return Ok(Vec::new());
        }
        let context = swlsp_lsp_phpanalysis::context_for_document(php_index, document)?;
        let mut result = Vec::new();
        for literal in phpquery::nodes(&root, &[PHP_STRING]) {
            let Some(reference) = option_reference_at(context.as_ref(), &root, &literal) else {
                continue;
            };
            if reference.name.is_empty() {
                continue;
            }
            let properties =
                constraint_properties_in_context(context.as_ref(), &reference.constraint);
            if find_constraint_property(&properties, &reference.name).is_some() {
                continue;
            }
            let names: Vec<String> = properties
                .iter()
                .map(|property| property.name.to_string())
                .collect();
            result.push(symfony_warning(
                MISSING_CONSTRAINT_OPTION_CODE,
                value_node_text_range(Some(&reference.node), &reference.name),
                format!(
                    "Option '{}' is not defined by constraint '{}'",
                    reference.name, reference.constraint
                ),
                swlsp_suggestion::similar(&reference.name, &names),
            ));
        }
        Ok(result)
    }
}
