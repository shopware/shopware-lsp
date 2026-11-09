//! The Shopware Administration inspections and their quick fixes (Go
//! `admin.go`, `admin_i18n.go`, `admin_slot.go`, `admin_twig_migration.go`).

mod fixes;

#[cfg(test)]
mod tests;
#[cfg(test)]
mod tests_fixes;

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_admin::AdminComponentIndexer;
use swlsp_admin_twigmigration as twigmigration;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
    QuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::{
    ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
    ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
    ADMIN_BLOCK_PARENT_REPEATED_CODE, AdminAnalyzer, AdminBlockOverrideAnalyzer,
    AdminI18nDeprecationAnalyzer, AdminSlotMigrationAnalyzer, AdminTwigMigrationAnalyzer,
    AdminTwigMigrationPayload, CodeGroup, ProblemAnalyzer, SharedAnalysis,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_shopware::ResolvedVersion;

use crate::{BindProblems, BoundInspection, SuggestionFix, suggestion_bound_fixes};

use fixes::{
    ADD_ADMIN_PROP_FIX_ID, AdminComponentTagFix, AdminComponentTagPayload, AdminI18nTcFix,
    AdminI18nTcReplacement, AdminPropFix, AdminPropPayload, AdminSlotMigrationFix,
    AdminSlotReplacement, AdminStaticPropFix, AdminStaticPropPayload,
    AdminTwigComponentMigrationFix, BIND_ADMIN_PROP_FIX_ID, MIGRATE_ADMIN_I18N_TC_FIX_ID,
    MIGRATE_ADMIN_SLOT_FIX_ID, MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID,
    REPLACE_ADMIN_COMPONENT_TAG_FIX_ID,
};

/// Go `mapString`: a string value of a decoded payload, or empty.
fn map_string(values: &Map<String, Value>, key: &str) -> String {
    match values.get(key) {
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    }
}

const SUGGESTION_CODES: &[&str] = &[
    "admin.application-container.unknown-member",
    "admin.shopware-context.unknown-member",
    "admin.shopware-utils.unknown-member",
    "admin.component.registry-not-found",
    "admin.component.block-not-found",
    "admin.cms-element.not-found",
    "admin.cms-block.not-found",
    "admin.directive.not-found",
    "admin.filter.not-found",
    "admin.component.invalid-prop-value",
    "admin.component.unknown-prop",
    "admin.component.unknown-event",
    "admin.component.unknown-model",
    "admin.component.unknown-slot",
    "admin.component.unknown-slot-prop",
    "admin.component.unknown-vue-member",
    "admin.component.unknown-instance-member",
    "admin.component.unknown-template-member",
    "admin.privilege.not-found",
    "admin.module.not-found",
    "admin.module-route.not-found",
];

/// Codes of [`AdminAnalyzer`], one list per inspection.
const COMPONENT_CODES: &[&str] = &[
    "admin.component.not-found",
    "admin.component.parent-not-found",
    "admin.component.registry-not-found",
    "admin.component.deprecated",
    "admin.component.deprecated-block",
    "admin.component.block-not-found",
    "admin.cms-block.not-found",
    "admin.cms-element.not-found",
    "admin.directive.not-found",
    "admin.filter.not-found",
];
const COMPONENT_API_CODES: &[&str] = &[
    "admin.component.missing-required-prop",
    "admin.component.unknown-prop",
    "admin.component.invalid-prop-value",
    "admin.component.static-prop-type",
    "admin.component.bound-prop-type",
    "admin.component.deprecated-prop",
    "admin.component.unknown-event",
    "admin.component.unknown-slot",
    "admin.component.unknown-slot-prop",
    "admin.component.unknown-model",
    "admin.component.model-type",
    "admin.component.model-not-assignable",
];
const MEMBER_CODES: &[&str] = &[
    "admin.component.unknown-instance-member",
    "admin.component.unknown-template-member",
    "admin.component.unknown-vue-member",
    "admin.component.deprecated-member",
    "admin.application-container.unknown-member",
    "admin.shopware-context.unknown-member",
    "admin.shopware-utils.unknown-member",
    "admin.event-bus.missing-payload",
    "admin.event-bus.missing-handler",
    "admin.event-bus.payload-type",
    "admin.event-bus.handler-type",
];
const MODULE_CODES: &[&str] = &[
    "admin.module.not-found",
    "admin.module-route.not-found",
    "admin.privilege.not-found",
];
const BLOCK_OVERRIDE_CODES: &[&str] = &[
    ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
    ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
    ADMIN_BLOCK_OVERRIDE_REPEATED_CODE,
    ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
    ADMIN_BLOCK_PARENT_REPEATED_CODE,
];

fn severity(code: &str) -> DiagnosticSeverity {
    if BLOCK_OVERRIDE_CODES.contains(&code) {
        DiagnosticSeverity::ERROR
    } else if code.starts_with("admin.component.deprecated") {
        DiagnosticSeverity::HINT
    } else {
        DiagnosticSeverity::WARNING
    }
}

/// The Administration inspections (Go `NewAdmin`, split by concern). The
/// component, component API, member and module inspections share one run of
/// [`AdminAnalyzer`] per document. From Shopware 6.7 the version-aware
/// `sw-*` → `mt-*` migrations own their component tags.
pub fn new_admin_inspections(
    index: Option<Arc<AdminComponentIndexer>>,
    version: ResolvedVersion,
) -> Vec<Arc<dyn Inspection>> {
    let mut admin_analyzer = AdminAnalyzer::new(index.clone());
    if version.at_least(6, 7, 0) {
        let names: Vec<&str> = twigmigration::rules()
            .iter()
            .map(|rule| rule.source_tag)
            .collect();
        admin_analyzer.suppress_component_diagnostics(&names);
    }
    let shared = SharedAnalysis::new(admin_analyzer);
    // Every inspection offers every Administration fix; `bind_admin_problem`
    // binds only the ones its codes use.
    let fixes = || -> Vec<Arc<dyn QuickFix>> {
        vec![
            Arc::new(AdminPropFix {
                index: index.clone(),
            }),
            Arc::new(AdminStaticPropFix),
            Arc::new(AdminSlotMigrationFix),
            Arc::new(AdminI18nTcFix),
            Arc::new(AdminComponentTagFix),
            Arc::new(AdminTwigComponentMigrationFix),
            Arc::new(SuggestionFix),
        ]
    };
    let inspection = |id: &str,
                      description: &'static str,
                      codes: Vec<String>,
                      analyzers: Vec<Arc<dyn ProblemAnalyzer>>|
     -> Arc<dyn Inspection> {
        let bind: BindProblems = Arc::new(bind_admin_problem);
        Arc::new(BoundInspection {
            definition: InspectionDefinition {
                id: id.to_owned(),
                description,
                domain: "administration",
                scope: InspectionScope::Framework,
                languages: vec![LanguageId::JavaScript, LanguageId::Twig, LanguageId::Vue],
                problems: codes
                    .iter()
                    .map(|code| ProblemDefinition {
                        id: DiagnosticId::from(code.as_str()),
                        source: "shopware-lsp".to_owned(),
                        default_severity: severity(code),
                        disabled_by_default: false,
                    })
                    .collect(),
            },
            analyzers,
            fixes: fixes(),
            bind: Some(bind),
        })
    };
    let group = |codes: &'static [&'static str]| -> (Vec<String>, Vec<Arc<dyn ProblemAnalyzer>>) {
        (
            codes.iter().map(|code| (*code).to_owned()).collect(),
            vec![Arc::new(CodeGroup::new(shared.clone(), codes))],
        )
    };
    let (codes, analyzers) = group(COMPONENT_CODES);
    let component = inspection(
        "shopware.admin.component",
        "Administration templates and scripts using components, CMS blocks and elements, directives or filters that are not registered, extending unregistered components, or using deprecated components and blocks.",
        codes,
        analyzers,
    );
    let (codes, analyzers) = group(COMPONENT_API_CODES);
    let component_api = inspection(
        "shopware.admin.component_api",
        "Administration component usages passing unknown, missing, deprecated or mistyped props, listening to unknown events, or using unknown slots, slot props and models.",
        codes,
        analyzers,
    );
    let (codes, analyzers) = group(MEMBER_CODES);
    let member = inspection(
        "shopware.admin.member",
        "Administration code accessing members a component, the application container, the Shopware context or Shopware utils do not have, deprecated members, and event-bus events with missing or mistyped payloads and handlers.",
        codes,
        analyzers,
    );
    let (codes, analyzers) = group(MODULE_CODES);
    let module = inspection(
        "shopware.admin.module",
        "Administration references to modules, module routes and privileges that are not registered.",
        codes,
        analyzers,
    );
    let block_override = inspection(
        "shopware.admin.block_override",
        "Administration template block overrides that are nested, conditional or repeated, and conditional or repeated parent calls.",
        BLOCK_OVERRIDE_CODES
            .iter()
            .map(|code| (*code).to_owned())
            .collect(),
        vec![Arc::new(AdminBlockOverrideAnalyzer::new())],
    );
    let mut migration_codes: Vec<String> = twigmigration::rules()
        .iter()
        .map(|rule| format!("admin.twig.migration.{}", rule.id))
        .collect();
    migration_codes.push("admin.slot-syntax-deprecated".to_owned());
    migration_codes.push("admin.vue-i18n.tc-deprecated".to_owned());
    let migration = inspection(
        "shopware.admin.migration",
        "Administration syntax to migrate: Twig components the Meteor component library replaces, deprecated slot syntax, and vue-i18n's `$tc`.",
        migration_codes,
        vec![
            Arc::new(AdminTwigMigrationAnalyzer::new(version)),
            Arc::new(AdminSlotMigrationAnalyzer::new()),
            Arc::new(AdminI18nDeprecationAnalyzer::new()),
        ],
    );
    vec![
        component,
        component_api,
        member,
        module,
        block_override,
        migration,
    ]
}

fn bind_admin_problem(code: &DiagnosticId, payload: &Map<String, Value>) -> Vec<BoundFix> {
    let code = code.as_str();
    if code.starts_with("admin.twig.migration.") {
        let value = AdminTwigMigrationPayload {
            rule: map_string(payload, "rule"),
            source_tag: map_string(payload, "sourceTag"),
            target_tag: map_string(payload, "targetTag"),
            safe: matches!(payload.get("safe"), Some(Value::Bool(true))),
        };
        if !value.safe
            || value.rule.is_empty()
            || value.source_tag.is_empty()
            || value.target_tag.is_empty()
        {
            return Vec::new();
        }
        return vec![bind_fix(MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID, value)];
    }
    if code == "admin.component.not-found" {
        return admin_component_tag_bound_fixes(payload);
    }
    if SUGGESTION_CODES.contains(&code) {
        return suggestion_bound_fixes(payload);
    }
    match code {
        "admin.slot-syntax-deprecated" => {
            let replacement = map_string(payload, "replacement");
            if replacement.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(
                MIGRATE_ADMIN_SLOT_FIX_ID,
                AdminSlotReplacement { replacement },
            )]
        }
        "admin.vue-i18n.tc-deprecated" => {
            let replacement = map_string(payload, "replacement");
            if replacement != "$t" {
                return Vec::new();
            }
            vec![bind_fix(
                MIGRATE_ADMIN_I18N_TC_FIX_ID,
                AdminI18nTcReplacement { replacement },
            )]
        }
        "admin.component.static-prop-type" => {
            let attribute = map_string(payload, "attributeName");
            if attribute.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(
                BIND_ADMIN_PROP_FIX_ID,
                AdminStaticPropPayload { attribute },
            )]
        }
        "admin.component.missing-required-prop" => {
            let value = AdminPropPayload {
                component: map_string(payload, "componentName"),
                prop: map_string(payload, "propName"),
            };
            if value.component.is_empty() || value.prop.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(ADD_ADMIN_PROP_FIX_ID, value)]
        }
        _ => Vec::new(),
    }
}

/// Go `adminComponentTagBoundFixes`: one tag replacement per distinct
/// suggestion other than the component itself.
fn admin_component_tag_bound_fixes(payload: &Map<String, Value>) -> Vec<BoundFix> {
    let component = map_string(payload, "componentName");
    if component.is_empty() {
        return Vec::new();
    }
    let Some(Value::Array(suggestions)) = payload.get("suggestions") else {
        return Vec::new();
    };
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for suggestion in suggestions {
        let Value::String(replacement) = suggestion else {
            continue;
        };
        let replacement = replacement.trim();
        if replacement.is_empty() || replacement == component || !seen.insert(replacement) {
            continue;
        }
        result.push(bind_fix(
            REPLACE_ADMIN_COMPONENT_TAG_FIX_ID,
            AdminComponentTagPayload {
                component: component.clone(),
                replacement: replacement.to_owned(),
            },
        ));
    }
    result
}
