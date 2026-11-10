//! Unknown SCSS variable inspection.

use std::sync::Arc;

use crate::diagnostics::{
    SCSS_VARIABLE_UNKNOWN_CODE, ScssVariableAnalyzer, ScssVariableDeclarationProvider,
    ScssVariableIndexReadiness,
};
use swlsp_indexer::WorkspaceSymbolCatalog;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol::DiagnosticSeverity;

use crate::inspections::BoundInspection;

/// Unknown SCSS variables, checked only once the workspace symbol catalog
/// reports a complete index.
pub fn new_scss_variable(
    index: Option<Arc<swlsp_style::Index>>,
    readiness: Option<Arc<WorkspaceSymbolCatalog>>,
) -> Arc<dyn Inspection> {
    let variables = index.map(|index| index as Arc<dyn ScssVariableDeclarationProvider>);
    let readiness = readiness.map(|catalog| catalog as Arc<dyn ScssVariableIndexReadiness>);
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "scss.variable".to_owned(),
            description: "Reports SCSS variables that are neither defined in the workspace nor provided by Shopware themes.",
            domain: "scss",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Scss],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from(SCSS_VARIABLE_UNKNOWN_CODE),
                source: "scss".to_owned(),
                default_severity: DiagnosticSeverity::ERROR,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(ScssVariableAnalyzer::new(variables, readiness))],
        fixes: Vec::new(),
        bind: None,
    })
}

#[cfg(test)]
mod tests {
    use swlsp_language::LanguageId;
    use swlsp_lsp_protocol::DiagnosticSeverity;

    use super::new_scss_variable;

    #[test]
    fn scss_variable_inspection_contract() {
        let definition = new_scss_variable(None, None).definition();
        assert_eq!(definition.id, "scss.variable");
        assert_eq!(definition.languages, vec![LanguageId::Scss]);
        assert_eq!(definition.problems.len(), 1);
        assert_eq!(definition.problems[0].id.0, "scss.variable.unknown");
        assert_eq!(
            definition.problems[0].default_severity,
            DiagnosticSeverity::ERROR
        );
    }
}
