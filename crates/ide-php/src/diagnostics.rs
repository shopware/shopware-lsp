//! PHP inspections.

use std::sync::Arc;

use swlsp_ide::inspections::analyzer_inspection;
use swlsp_ide::{Registrar, Services};
use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

/// Adapts the PHP semantic provider to the analyzer interface.
struct PhpSemanticAnalyzer(Arc<PhpFeatures>);

impl swlsp_ide::diagnostics::ProblemAnalyzer for PhpSemanticAnalyzer {
    fn analyze(
        &self,
        ctx: &swlsp_lsp::Context,
        document: &Arc<swlsp_lsp::TextDocument>,
    ) -> Result<Vec<swlsp_lsp::Problem>, swlsp_lsp::BoxError> {
        self.0.analyze(ctx, document)
    }

    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(swlsp_ide::diagnostics::inputs(&self.0.index()))
    }
}

impl swlsp_ide::diagnostics::DeclaredAnalyzer for PhpSemanticAnalyzer {
    const INSPECTION: swlsp_lsp::InspectionSpec = swlsp_lsp_phpsemantic::INSPECTION;
}

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    server.inspection(analyzer_inspection(PhpSemanticAnalyzer(
        s.php_features.clone(),
    )));
    server.inspection(crate::inspections::new_php_imports(s.php()));
}
