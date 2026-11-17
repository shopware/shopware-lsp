//! The `shopware.php.route` inspection.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_php::PhpIndex;

use crate::diagnostics::{ROUTE_CODES, ShopwareRouteAnalyzer};
use crate::inspections::inspection::BoundInspection;
use crate::inspections::{ClassConstantFix, class_constant_bound_fixes};

pub fn new_shopware_routes(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.php.route".to_owned(),
            description: "Route attributes that break Shopware's conventions: no route scope, an API route outside of the path of its scope, a storefront route the storefront router does not prefix, no HTTP methods, and route defaults written as strings where Shopware declares constants.",
            domain: "shopware",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: ROUTE_CODES
                .iter()
                .map(|(code, severity)| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: "shopware-lsp".to_owned(),
                    default_severity: *severity,
                    disabled_by_default: false,
                })
                .collect(),
        },
        analyzers: vec![Arc::new(ShopwareRouteAnalyzer::new(php_index))],
        fixes: vec![Arc::new(ClassConstantFix)],
        bind: Some(Arc::new(|_, payload| class_constant_bound_fixes(payload))),
    })
}
