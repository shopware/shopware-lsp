//! Tagged services whose class misses the tag's required interface.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;

use super::{lower_ext, problem};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const TAGGED_SERVICE_TYPE_CODE: &str = "symfony.service.tag_type";

pub struct TaggedServiceAnalyzer {
    php_index: Option<PhpIndex>,
}

impl TaggedServiceAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for TaggedServiceAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.service.tag_type",
        description: "Tagged services that do not implement the interface their tag requires.",
        domain: "symfony.services",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[TAGGED_SERVICE_TYPE_CODE],
    };
}

impl ProblemAnalyzer for TaggedServiceAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
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
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let services = match lower_ext(&document.uri).as_str() {
            ".xml" => {
                swlsp_symfony::parse_xml_services_tree(
                    &path,
                    document.tree(),
                    document.line_index(),
                )
                .0
            }
            ".yaml" | ".yml" => {
                swlsp_symfony::parse_yaml_services_tree(
                    &path,
                    document.tree(),
                    document.line_index(),
                )
                .0
            }
            _ => return Ok(Vec::new()),
        };
        let snapshot = php.semantic_snapshot();
        let mut result = Vec::new();
        for service in services {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let class_name = service.class.trim();
            let class_name = class_name.strip_prefix('\\').unwrap_or(class_name);
            if class_name.is_empty() || class_name.contains('%') {
                continue;
            }
            if php.find_class(class_name).is_none() {
                continue;
            }
            // TagMap is a BTreeMap, so tags iterate in sorted order.
            for tag in service.tags.keys() {
                let expected = swlsp_symfony::required_service_tag_types(tag);
                if expected.is_empty() {
                    continue;
                }
                let mut missing = "";
                for target in &expected {
                    if php.find_class(target).is_none() {
                        continue;
                    }
                    missing = target;
                    if snapshot.is_subtype_of(class_name, target) {
                        missing = "";
                        break;
                    }
                }
                if missing.is_empty() {
                    continue;
                }
                let range = if service.class_range.is_empty() {
                    service.id_range
                } else {
                    service.class_range
                };
                result.push(problem(
                    TAGGED_SERVICE_TYPE_CODE,
                    range,
                    format!(
                        "Class needs to implement '{}' for tag '{tag}'",
                        missing.strip_prefix('\\').unwrap_or(missing)
                    ),
                    DiagnosticSeverity::WARNING,
                ));
            }
        }
        Ok(result)
    }
}
