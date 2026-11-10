//! Missing Twig macros.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_textutil::lowercase;
use swlsp_twig::{Macro, MacroReference, MacroReferenceRole, TwigIndexer};

use super::component::suggestions_payload;
use super::{lower_ext, uri_path};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_TWIG_MACRO_CODE: &str = "twig.macro.missing";

pub struct TwigMacroAnalyzer {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigMacroAnalyzer {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }
}

impl DeclaredAnalyzer for TwigMacroAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.macro",
        description: "Macro calls naming macros the imported template does not define.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "twig",
        codes: &[MISSING_TWIG_MACRO_CODE],
    };
}

impl ProblemAnalyzer for TwigMacroAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.index, document.root()) else {
            return Ok(Vec::new());
        };
        if lower_ext(&document.uri) != ".twig" {
            return Ok(Vec::new());
        }
        let path = uri_path(&document.uri);
        let current_macros = swlsp_twig::macros_in_document(&path, &root);
        let mut result = Vec::new();
        for reference in swlsp_twig::macro_references_in_document(&path, &root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if reference.role != MacroReferenceRole::Usage {
                continue;
            }
            let (found, candidates) = resolve(index, &path, &current_macros, &reference)?;
            if found {
                continue;
            }
            result.push(Problem {
                id: MISSING_TWIG_MACRO_CODE.into(),
                range: reference.range,
                message: format!("Twig macro '{}' not found", reference.name),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(swlsp_suggestion::similar(
                    &reference.name,
                    &candidates,
                )),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

fn resolve(
    index: &TwigIndexer,
    path: &str,
    current_macros: &[Macro],
    reference: &MacroReference,
) -> Result<(bool, Vec<String>), BoxError> {
    let current_templates = swlsp_twig::template_names(path);
    let mut names: HashMap<String, String> = HashMap::new();
    for template in &reference.templates {
        let is_current = current_templates.contains(template);
        for item in index.get_macros(template)? {
            if item.file_path == path && is_current {
                continue;
            }
            names.insert(lowercase(&item.name), item.name);
        }
        if is_current {
            for item in current_macros {
                names.insert(lowercase(&item.name), item.name.clone());
            }
        }
    }
    let found = names.contains_key(&lowercase(&reference.name));
    let mut candidates: Vec<(String, String)> = names.into_iter().collect();
    candidates.sort_by(|left, right| left.0.cmp(&right.0));
    Ok((
        found,
        candidates.into_iter().map(|(_, name)| name).collect(),
    ))
}
