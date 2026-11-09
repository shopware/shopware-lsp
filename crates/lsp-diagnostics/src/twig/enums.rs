//! Go `twig_enum_diagnostics.go`.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;

use super::{lower_ext, strings_value};
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_TWIG_ENUM_CODE: &str = "twig.enum.missing";
const INVALID_TWIG_ENUM_CODE: &str = "twig.enum.invalid";

/// Go `TwigEnumAnalyzer`.
pub struct TwigEnumAnalyzer {
    php_index: Option<PhpIndex>,
}

impl TwigEnumAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for TwigEnumAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.enum",
        description: "Twig `enum()` calls naming classes that are not enums, or cases that do not exist.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "twig",
        codes: &[INVALID_TWIG_ENUM_CODE, MISSING_TWIG_ENUM_CODE],
    };
}

impl ProblemAnalyzer for TwigEnumAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(php_index), Some(root)) = (&self.php_index, document.root()) else {
            return Ok(Vec::new());
        };
        if lower_ext(&document.uri) != ".twig" {
            return Ok(Vec::new());
        }
        let mut enum_names: Option<Vec<String>> = None;
        let mut result = Vec::new();
        for reference in swlsp_twig::enum_references(&root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            match php_index.find_class(&reference.name) {
                None => {
                    let names = enum_names.get_or_insert_with(|| enum_names_of(php_index));
                    let suggestions: Vec<String> =
                        swlsp_suggestion::similar(&reference.name, names)
                            .iter()
                            .map(|value| swlsp_twig::escape_twig_class_name(value))
                            .collect();
                    let mut payload = Map::new();
                    payload.insert("suggestions".into(), strings_value(suggestions));
                    result.push(Problem {
                        id: MISSING_TWIG_ENUM_CODE.into(),
                        range: reference.range,
                        message: format!("PHP enum '{}' not found", reference.name),
                        severity: Some(protocol::DiagnosticSeverity::WARNING),
                        source: "twig".into(),
                        payload: Some(Value::Object(payload)),
                        ..Problem::default()
                    });
                }
                Some(symbol) if symbol.kind != SymbolKind::Enum => {
                    result.push(Problem {
                        id: INVALID_TWIG_ENUM_CODE.into(),
                        range: reference.range,
                        message: format!("PHP class '{}' is not an enum", reference.name),
                        severity: Some(protocol::DiagnosticSeverity::WARNING),
                        source: "twig".into(),
                        ..Problem::default()
                    });
                }
                Some(_) => {}
            }
        }
        Ok(result)
    }
}

fn enum_names_of(index: &PhpIndex) -> Vec<String> {
    index
        .class_symbols_view()
        .iter()
        .filter(|symbol| symbol.kind == SymbolKind::Enum)
        .map(|symbol| {
            let name: &str = &symbol.fully_qualified;
            name.strip_prefix('\\').unwrap_or(name).to_owned()
        })
        .collect()
}
