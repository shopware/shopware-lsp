//! The `shopware.migration_step` inspection.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::PhpIndex;

use crate::diagnostics::{MIGRATION_STEP_CODES, ShopwareMigrationStepAnalyzer};
use crate::inspections::inspection::BoundInspection;
use crate::inspections::{RewriteNodeFix, rewrite_node_bound_fixes};

pub fn new_shopware_migration_step(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.migration_step".to_owned(),
            description: "Database migrations that fail when they run again (CREATE TABLE without IF NOT EXISTS, a column or key added without a check) or that break the previous version of the code during a deployment (drops and renames outside of updateDestructive()). Opt-in: inserts that add a row again.",
            domain: "shopware",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: MIGRATION_STEP_CODES
                .iter()
                .map(|(code, enabled)| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: "shopware-lsp".to_owned(),
                    default_severity: DiagnosticSeverity::WARNING,
                    disabled_by_default: !enabled,
                })
                .collect(),
        },
        analyzers: vec![Arc::new(ShopwareMigrationStepAnalyzer::new(php_index))],
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(Arc::new(|_, payload| rewrite_node_bound_fixes(payload))),
    })
}
