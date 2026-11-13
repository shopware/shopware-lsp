//! The `scss.import` inspection.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol::DiagnosticSeverity;

use crate::diagnostics::{SCSS_IMPORT_EXTENSION_CODE, ScssImportExtensionAnalyzer};
use crate::inspections::{BindProblems, BoundInspection, RewriteNodeFix, rewrite_node_bound_fixes};

pub fn new_scss_import() -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
            rewrite_node_bound_fixes(payload)
        },
    );
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "scss.import".to_owned(),
            description: "Administration SCSS importing a shared `~scss` resource with its file extension.",
            domain: "scss",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Scss],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from(SCSS_IMPORT_EXTENSION_CODE),
                source: "scss".to_owned(),
                default_severity: DiagnosticSeverity::WARNING,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(ScssImportExtensionAnalyzer)],
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(bind),
    })
}

#[cfg(test)]
mod tests {
    use super::new_scss_import;
    use crate::inspections::REWRITE_NODE_FIX_ID;
    use crate::inspections::test_support::{check_diagnostics, check_fix};

    const URI: &str = "file:///project/src/Resources/app/administration/src/module/a/a.scss";

    #[test]
    fn shared_resources_are_imported_without_their_extension() {
        let inspection = new_scss_import();
        check_diagnostics(
            inspection.as_ref(),
            URI,
            "@import {|scss.import.extension:\"~scss/variables.scss\"|};\n@import '~scss/mixins', {|scss.import.extension:'~scss/global.scss'|};\n@import \"./local.scss\";\n@import \"~scss/variables\";\n@use \"~scss/other.scss\";\n.a { background: url(\"~scss/image.scss\"); }\n",
        );
        for (before, after) in [
            (
                "@import {|scss.import.extension:\"~scss/variables.scss\"|};\n",
                "@import \"~scss/variables\";\n",
            ),
            (
                "@import {|scss.import.extension:'~scss/a.scss/b.scss'|};\n",
                "@import '~scss/a.scss/b';\n",
            ),
        ] {
            let presentation =
                check_fix(inspection.as_ref(), URI, before, REWRITE_NODE_FIX_ID, after);
            assert!(presentation.preferred);
            assert_eq!(presentation.title, "Remove the .scss extension");
        }
        // The alias only exists in the Administration.
        check_diagnostics(
            inspection.as_ref(),
            "file:///project/src/Resources/app/storefront/src/scss/base.scss",
            "@import \"~scss/variables.scss\";\n",
        );
    }
}
