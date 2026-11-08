//! The Shopware Administration inspection and its quick fixes (Go
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
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, ProblemDefinition, bind_fix,
};
use swlsp_lsp_diagnostics::{
    ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
    ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
    ADMIN_BLOCK_PARENT_REPEATED_CODE, AdminAnalyzer, AdminBlockOverrideAnalyzer,
    AdminI18nDeprecationAnalyzer, AdminSlotMigrationAnalyzer, AdminTwigMigrationAnalyzer,
    AdminTwigMigrationPayload,
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

/// Go `NewAdmin`: the `shopware.admin` inspection. From Shopware 6.7 the
/// version-aware `sw-*` → `mt-*` migrations own their component tags.
pub fn new_admin(
    index: Option<Arc<AdminComponentIndexer>>,
    version: ResolvedVersion,
) -> Arc<dyn Inspection> {
    let mut admin_analyzer = AdminAnalyzer::new(index.clone());
    if version.at_least(6, 7, 0) {
        let names: Vec<&str> = twigmigration::rules()
            .iter()
            .map(|rule| rule.source_tag)
            .collect();
        admin_analyzer.suppress_component_diagnostics(&names);
    }
    let problem = |id: &str, severity: DiagnosticSeverity| ProblemDefinition {
        id: DiagnosticId::from(id),
        source: "shopware-lsp".to_owned(),
        default_severity: severity,
        disabled_by_default: false,
    };
    let warning = DiagnosticSeverity::WARNING;
    let error = DiagnosticSeverity::ERROR;
    let hint = DiagnosticSeverity::HINT;
    let mut problems = vec![
        problem("admin.application-container.unknown-member", warning),
        problem(ADMIN_BLOCK_OVERRIDE_NESTED_CODE, error),
        problem(ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, error),
        problem(ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, error),
        problem(ADMIN_BLOCK_PARENT_CONDITIONAL_CODE, error),
        problem(ADMIN_BLOCK_PARENT_REPEATED_CODE, error),
        problem("admin.shopware-context.unknown-member", warning),
        problem("admin.shopware-utils.unknown-member", warning),
        problem("admin.component.deprecated", hint),
        problem("admin.component.deprecated-block", hint),
        problem("admin.component.deprecated-member", hint),
        problem("admin.component.deprecated-prop", hint),
        problem("admin.component.block-not-found", warning),
        problem("admin.component.bound-prop-type", warning),
        problem("admin.component.invalid-prop-value", warning),
        problem("admin.component.model-not-assignable", warning),
        problem("admin.component.model-type", warning),
        problem("admin.component.not-found", warning),
        problem("admin.component.missing-required-prop", warning),
        problem("admin.component.static-prop-type", warning),
        problem("admin.component.unknown-slot-prop", warning),
        problem("admin.component.unknown-prop", warning),
        problem("admin.component.unknown-event", warning),
        problem("admin.component.unknown-model", warning),
        problem("admin.component.unknown-slot", warning),
        problem("admin.component.unknown-vue-member", warning),
        problem("admin.component.unknown-instance-member", warning),
        problem("admin.component.unknown-template-member", warning),
        problem("admin.component.parent-not-found", warning),
        problem("admin.component.registry-not-found", warning),
        problem("admin.cms-element.not-found", warning),
        problem("admin.cms-block.not-found", warning),
        problem("admin.directive.not-found", warning),
        problem("admin.filter.not-found", warning),
        problem("admin.privilege.not-found", warning),
        problem("admin.module.not-found", warning),
        problem("admin.module-route.not-found", warning),
        problem("admin.slot-syntax-deprecated", warning),
        problem("admin.vue-i18n.tc-deprecated", warning),
        problem("admin.event-bus.missing-payload", warning),
        problem("admin.event-bus.missing-handler", warning),
        problem("admin.event-bus.payload-type", warning),
        problem("admin.event-bus.handler-type", warning),
    ];
    for rule in twigmigration::rules() {
        problems.push(problem(
            &format!("admin.twig.migration.{}", rule.id),
            warning,
        ));
    }
    let bind: BindProblems = Arc::new(bind_admin_problem);
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.admin".to_owned(),
            languages: vec![LanguageId::JavaScript, LanguageId::Twig, LanguageId::Vue],
            problems,
        },
        analyzers: vec![
            Arc::new(admin_analyzer),
            Arc::new(AdminBlockOverrideAnalyzer::new()),
            Arc::new(AdminTwigMigrationAnalyzer::new(version)),
            Arc::new(AdminSlotMigrationAnalyzer::new()),
            Arc::new(AdminI18nDeprecationAnalyzer::new()),
        ],
        fixes: vec![
            Arc::new(AdminPropFix { index }),
            Arc::new(AdminStaticPropFix),
            Arc::new(AdminSlotMigrationFix),
            Arc::new(AdminI18nTcFix),
            Arc::new(AdminComponentTagFix),
            Arc::new(AdminTwigComponentMigrationFix),
            Arc::new(SuggestionFix),
        ],
        bind: Some(bind),
    })
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
