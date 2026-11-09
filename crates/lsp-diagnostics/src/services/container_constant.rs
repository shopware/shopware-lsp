//! Unresolved `!php/const` / `!php/enum` and XML constant arguments (Go
//! `container_constant_diagnostics.go`).

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;
use swlsp_symfony::{ContainerConstantReference, ContainerValueKind};

use super::{lower_ext, problem};
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_CONTAINER_CONSTANT_CODE: &str = "symfony.constant.missing";

/// Go `ContainerConstantAnalyzer`.
pub struct ContainerConstantAnalyzer {
    php_index: Option<PhpIndex>,
}

impl ContainerConstantAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for ContainerConstantAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.container_constant",
        description: "Service configuration referencing PHP constants that do not exist.",
        domain: "symfony.services",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[MISSING_CONTAINER_CONSTANT_CODE],
    };
}

impl ProblemAnalyzer for ContainerConstantAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        if document.tree().is_none() {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for reference in container_constant_references(document) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if !swlsp_symfony::resolve_container_value(Some(php), &reference).is_empty() {
                continue;
            }
            let message = if reference.kind == ContainerValueKind::Enum {
                "Symfony: enum or case not found"
            } else {
                "Symfony: constant not found"
            };
            result.push(problem(
                MISSING_CONTAINER_CONSTANT_CODE,
                reference.range,
                message.to_owned(),
                DiagnosticSeverity::ERROR,
            ));
        }
        Ok(result)
    }
}

fn container_constant_references(document: &TextDocument) -> Vec<ContainerConstantReference> {
    match lower_ext(&document.uri).as_str() {
        ".yaml" | ".yml" => swlsp_symfony::yaml_container_constant_references(document.source()),
        ".xml" => document
            .root()
            .map(|root| swlsp_symfony::xml_container_constant_references(&root))
            .unwrap_or_default(),
        _ => Vec::new(),
    }
}
