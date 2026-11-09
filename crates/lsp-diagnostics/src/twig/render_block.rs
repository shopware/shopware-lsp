//! Go `twig_render_block_diagnostics.go`.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_twig::TwigIndexer;

use super::component::suggestions_payload;
use super::go_ext;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_TWIG_RENDER_BLOCK_CODE: &str = "twig.template.block.missing";

/// Go `TwigRenderBlockAnalyzer`.
pub struct TwigRenderBlockAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigRenderBlockAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl DeclaredAnalyzer for TwigRenderBlockAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.render_block",
        description: "Rendering a block the template does not define.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "twig",
        codes: &[MISSING_TWIG_RENDER_BLOCK_CODE],
    };
}

impl ProblemAnalyzer for TwigRenderBlockAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.twig_index) + crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(twig_index), Some(php_index), Some(root)) =
            (&self.twig_index, &self.php_index, document.root())
        else {
            return Ok(Vec::new());
        };
        if !equal_fold(go_ext(&document.uri), ".php") {
            return Ok(Vec::new());
        }
        let references = swlsp_twig::render_block_references_in_php(&root);
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let context = swlsp_lsp_phpanalysis::context_for_document(php_index, document)?;
        let mut result = Vec::new();
        for reference in references {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if reference.template.is_empty()
                || reference.block.is_empty()
                || !swlsp_twig::validate_render_block_reference(
                    &reference,
                    Some(php_index),
                    context.as_ref(),
                )
            {
                continue;
            }
            let blocks = twig_index.get_template_blocks(&[reference.template.as_str()])?;
            let mut found = false;
            let mut names: HashMap<String, String> = HashMap::new();
            for block in &blocks {
                names.insert(go_to_lower(&block.name), block.name.clone());
                if equal_fold(&block.name, &reference.block) {
                    found = true;
                }
            }
            if found || blocks.is_empty() {
                continue;
            }
            let mut candidates: Vec<String> = names.into_values().collect();
            candidates.sort();
            result.push(Problem {
                id: MISSING_TWIG_RENDER_BLOCK_CODE.into(),
                range: reference.block_range,
                message: format!(
                    "Twig block '{}' not found in template '{}'",
                    reference.block, reference.template
                ),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(swlsp_suggestion::similar(
                    &reference.block,
                    &candidates,
                )),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}
