//! Go `serializer_diagnostics.go`.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_php_types::go_to_lower;
use swlsp_serializer::{Index, TargetKind, usages_in_document};

use super::symfony_warning;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_SERIALIZER_CLASS_CODE: &str = "symfony.serializer.class.missing";

pub struct SerializerAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
}

impl SerializerAnalyzer {
    pub fn new(index: Option<Arc<Index>>, php_index: Option<PhpIndex>) -> Self {
        Self { index, php_index }
    }
}

impl DeclaredAnalyzer for SerializerAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.serializer",
        description: "Serializer calls for classes that do not exist.",
        domain: "symfony.serializer",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[MISSING_SERIALIZER_CLASS_CODE],
    };
}

impl ProblemAnalyzer for SerializerAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.index) + crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(_), Some(php_index), Some(root)) =
            (&self.index, &self.php_index, document.root())
        else {
            return Ok(Vec::new());
        };
        if !go_to_lower(&document.uri).ends_with(".php") {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let missing: Vec<_> = usages_in_document(&path, &root)
            .into_iter()
            .filter(|usage| {
                usage.kind == TargetKind::String && php_index.find_class(&usage.class).is_none()
            })
            .collect();
        if missing.is_empty() {
            return Ok(Vec::new());
        }
        // The class catalog only feeds suggestions; building it materializes
        // every workspace class, so it is skipped for documents without a
        // missing serializer target (nearly all of them).
        let class_names = php_index.class_names_view();
        if class_names.is_empty() {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for usage in missing {
            result.push(symfony_warning(
                MISSING_SERIALIZER_CLASS_CODE,
                usage.range,
                format!("Serializer target class '{}' not found", usage.class),
                swlsp_suggestion::similar(&usage.class, &class_names),
            ));
        }
        Ok(result)
    }
}
