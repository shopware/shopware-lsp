//! The strict Twig inspections: findings that are real under
//! `strict_variables` but common in templates written without it. And the
//! `dump_type` debugging aid.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Inspection, InspectionDefinition, InspectionScope, ProblemDefinition};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;

use crate::diagnostics::{
    TWIG_MEMBER_NULL_CODE, TWIG_TYPE_DUMPED_CODE, TWIG_VARIABLE_UNDEFINED_CODE,
    TwigDumpTypeAnalyzer, TwigNullAccessAnalyzer, TwigUndefinedVariableAnalyzer,
};
use crate::inspections::BoundInspection;

pub fn new_twig_null_access(
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "twig.member.nullable".into(),
            description: "Twig member access on a value that can be null, where no condition around it rules null out: an error under strict_variables. Opt-in.",
            domain: "twig",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Twig],
            problems: vec![ProblemDefinition {
                id: TWIG_MEMBER_NULL_CODE.into(),
                source: "twig".into(),
                default_severity: protocol::DiagnosticSeverity::WARNING,
                // Twig yields null for an attribute of null unless
                // `strict_variables` is on, which Shopware never sets.
                disabled_by_default: true,
            }],
        },
        analyzers: vec![Arc::new(TwigNullAccessAnalyzer::new(twig, php))],
        fixes: Vec::new(),
        bind: None,
    })
}

pub fn new_twig_undefined_variable(
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "twig.variable".into(),
            description: "Twig variables no render call, including or extending template, assignment or global provides. Opt-in.",
            domain: "twig",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Twig],
            problems: vec![ProblemDefinition {
                id: TWIG_VARIABLE_UNDEFINED_CODE.into(),
                source: "twig".into(),
                default_severity: protocol::DiagnosticSeverity::WARNING,
                // Variables added by event listeners and extensions at
                // render time are invisible to the analysis.
                disabled_by_default: true,
            }],
        },
        analyzers: vec![Arc::new(TwigUndefinedVariableAnalyzer::new(twig, php))],
        fixes: Vec::new(),
        bind: None,
    })
}

pub fn new_twig_dump_type(
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "twig.dump_type".into(),
            description: "The type the analysis has for the variable a `{% dump_type %}` tag names, or for the whole context.",
            domain: "twig",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Twig],
            problems: vec![ProblemDefinition {
                id: TWIG_TYPE_DUMPED_CODE.into(),
                source: "twig".into(),
                default_severity: protocol::DiagnosticSeverity::INFORMATION,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(TwigDumpTypeAnalyzer::new(twig, php))],
        fixes: Vec::new(),
        bind: None,
    })
}
