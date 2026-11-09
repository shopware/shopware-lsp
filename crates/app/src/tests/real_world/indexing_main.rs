//! The main real-world indexing test (`shopware_trunk_indexing`).
//!
//! The test is split into ordered sections over one indexed fixture. Value comparisons are "soft":
//! a failure is recorded and the section continues,
//! while prerequisite failures (errors, missing values, lengths before
//! indexing) panic and end only their section. The test fails at the end
//! with every recorded failure, so one run reports every regression.

// Struct updates keep request literals robust to new optional fields;
// soft comparisons work on any ordered value.
#![allow(clippy::needless_update, clippy::neg_cmp_op_on_partial_ord)]

use std::cell::RefCell;
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fmt::Debug;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp::*;
use swlsp_lsp_protocol as protocol;

use super::RealWorldWorkspaceFixture;
use super::helpers_fixture::*;
use super::helpers_request::*;
use super::helpers_snapshot::*;
use super::helpers_workspace::*;

thread_local! {
    static FAILURES: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    static SECTION: RefCell<String> = const { RefCell::new(String::new()) };
}

pub(super) fn record_failure(message: String) {
    let section = SECTION.with(|section| section.borrow().clone());
    eprintln!("SOFTFAIL [{section}] {message}");
    FAILURES.with(|failures| failures.borrow_mut().push(format!("[{section}] {message}")));
}

/// Soft `require.True`.
macro_rules! check {
    ($cond:expr) => {
        if !$cond {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {}",
                line!(),
                stringify!($cond)
            ));
        }
    };
    ($cond:expr, $($arg:tt)+) => {
        if !$cond {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {}: {}",
                line!(),
                stringify!($cond),
                format!($($arg)+)
            ));
        }
    };
}
pub(super) use check;

/// Soft `require.Equal`.
macro_rules! check_eq {
    ($expected:expr, $actual:expr $(, $($arg:tt)+)?) => {{
        let expected = &$expected;
        let actual = &$actual;
        if expected != actual {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: expected {:?}, got {:?} ({}) {}",
                line!(),
                expected,
                actual,
                stringify!($actual),
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_eq;

macro_rules! check_contains {
    ($haystack:expr, $needle:expr $(, $($arg:tt)+)?) => {{
        let haystack = &$haystack;
        let needle = $needle;
        if !haystack.iter().any(|item| item == &needle) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {:?} not in {} = {:?} {}",
                line!(),
                needle,
                stringify!($haystack),
                haystack,
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_contains;

macro_rules! check_not_contains {
    ($haystack:expr, $needle:expr $(, $($arg:tt)+)?) => {{
        let haystack = &$haystack;
        let needle = $needle;
        if haystack.iter().any(|item| item == &needle) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {:?} unexpectedly in {} {}",
                line!(),
                needle,
                stringify!($haystack),
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_not_contains;

/// Soft `require.Contains` on a string.
macro_rules! check_str {
    ($haystack:expr, $needle:expr $(, $($arg:tt)+)?) => {{
        let haystack: &str = &$haystack;
        let needle: &str = &$needle;
        if !haystack.contains(needle) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {:?} not in {} = {:?} {}",
                line!(),
                needle,
                stringify!($haystack),
                haystack,
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_str;

macro_rules! check_not_str {
    ($haystack:expr, $needle:expr $(, $($arg:tt)+)?) => {{
        let haystack: &str = &$haystack;
        let needle: &str = &$needle;
        if haystack.contains(needle) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {:?} unexpectedly in {} = {:?} {}",
                line!(),
                needle,
                stringify!($haystack),
                haystack,
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_not_str;

/// Soft `require.GreaterOrEqual` / `Greater`.
macro_rules! check_ge {
    ($actual:expr, $min:expr) => {{
        let actual = $actual;
        let min = $min;
        if !(actual >= min) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {} = {:?} < {:?}",
                line!(),
                stringify!($actual),
                actual,
                min
            ));
        }
    }};
}
pub(super) use check_ge;

macro_rules! check_gt {
    ($actual:expr, $min:expr) => {{
        let actual = $actual;
        let min = $min;
        if !(actual > min) {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {} = {:?} <= {:?}",
                line!(),
                stringify!($actual),
                actual,
                min
            ));
        }
    }};
}
pub(super) use check_gt;

/// Soft `require.Empty`.
macro_rules! check_empty {
    ($value:expr $(, $($arg:tt)+)?) => {{
        let value = &$value;
        if !value.is_empty() {
            $crate::tests::real_world::indexing_main::record_failure(format!(
                "line {}: {} not empty: {:?} {}",
                line!(),
                stringify!($value),
                value,
                "" $(.to_owned() + &format!($($arg)+))?
            ));
        }
    }};
}
pub(super) use check_empty;

/// Hard `require.Len`.
macro_rules! require_len {
    ($value:expr, $len:expr $(, $($arg:tt)+)?) => {{
        let value = &$value;
        assert_eq!(
            $len,
            value.len(),
            "{} has wrong length: {:#?} {}",
            stringify!($value),
            value,
            "" $(.to_owned() + &format!($($arg)+))?
        );
    }};
}
pub(super) use require_len;

/// Runs one section, recording a panic as a hard failure.
fn section(name: &str, run: impl FnOnce()) {
    SECTION.with(|section| *section.borrow_mut() = name.to_owned());
    eprintln!("=== section {name}");
    let started = std::time::Instant::now();
    if let Err(panic) = catch_unwind(AssertUnwindSafe(run)) {
        let message = panic
            .downcast_ref::<String>()
            .cloned()
            .or_else(|| {
                panic
                    .downcast_ref::<&str>()
                    .map(|value| (*value).to_owned())
            })
            .unwrap_or_else(|| "panic".to_owned());
        record_failure(format!("HARD: {message}"));
    }
    eprintln!("=== section {name} done in {:?}", started.elapsed());
}

pub(super) fn ctx() -> Context {
    Context::background()
}

/// `path` with `/` separators and without the `root/` prefix (unchanged when
/// it lies outside `root`).
pub(super) fn relative(root: &str, path: &str) -> String {
    let prefix = format!("{}/", root.replace('\\', "/"));
    let path = path.replace('\\', "/");
    path.strip_prefix(&prefix)
        .map(str::to_owned)
        .unwrap_or(path)
}

pub(super) fn read(path: &str) -> String {
    std::fs::read_to_string(path).unwrap_or_else(|error| panic!("read {path}: {error}"))
}

pub(super) fn offset_of(source: &str, needle: &str) -> usize {
    source
        .find(needle)
        .unwrap_or_else(|| panic!("{needle:?} not found"))
}

pub(super) fn last_offset_of(source: &str, needle: &str) -> usize {
    source
        .rfind(needle)
        .unwrap_or_else(|| panic!("{needle:?} not found"))
}

pub(super) fn slash(path: &str) -> String {
    path.replace('\\', "/")
}

pub(super) fn payload_str(problem: &Problem, key: &str) -> String {
    problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_owned()
}

pub(super) fn payload_strings(problem: &Problem, key: &str) -> Vec<String> {
    problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
        .and_then(Value::as_array)
        .map(|values| {
            values
                .iter()
                .filter_map(|value| value.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

pub(super) fn token_text(document: &TextDocument, token: &SemanticToken) -> String {
    let start: usize = token.range.start().into();
    let end: usize = token.range.end().into();
    document.source()[start..end].to_owned()
}

#[test]
fn shopware_trunk_indexing() {
    let Some(fixture) = RealWorldWorkspaceFixture::new() else {
        return;
    };
    eprintln!("cold index: {:?}", fixture.cold_elapsed);
    let f = &fixture;
    section("admin/components", || admin::components(f));
    section("admin/contracts", || admin::contracts(f));
    section("admin/snippets-and-props", || admin::snippets_and_props(f));
    section("admin/data-grid", || admin::data_grid(f));
    section("admin/entities", || admin::entities(f));
    section("admin/media-and-entity-collections", || admin::media(f));
    section("admin/slots-and-events", || admin::slots_and_events(f));
    section("admin/registries", || admin::registries(f));
    section("dal-and-app-scripts", || domains::dal(f));
    section("php-and-twig-basics", || domains::php_and_twig(f));
    section("events-messenger-environment-services", || {
        domains::services(f)
    });
    section("translation-extraction-and-routes", || {
        domains::translation_and_routes(f)
    });
    section("assets/indexed", || twig_catalog::assets(f));
    section("twig-catalogs-and-snapshots", || twig_catalog::run(f));
    section("php-assistants", || restore::assistants(f));
    section("request-latency/strict", || {
        super::request_latency::measure_real_world_lsp_request_latency(&f.server, &f.root, true)
    });
    let cold = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        restore::probes(f, &f.workspace, &f.php)
    }));
    section("restore", || restore::restore(f, cold.as_ref().ok()));
    let failures = FAILURES.with(|failures| failures.borrow().clone());
    assert!(
        failures.is_empty(),
        "{} real-world failures:\n{}",
        failures.len(),
        failures.join("\n")
    );
}

mod admin {
    use std::collections::{BTreeMap, BTreeSet, HashMap};
    use std::sync::Arc;

    use swlsp_admin::*;
    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::{AdminAnalyzer, ProblemAnalyzer};
    use swlsp_lsp_protocol as protocol;
    use swlsp_syntax::{SyntaxNodeExt, TextSize};

    use super::*;

    fn exact(range: &AdminSourceRange) -> bool {
        range.declaration
            && (range.end_line > range.start_line
                || range.end_line == range.start_line
                    && range.end_character > range.start_character)
    }

    pub(super) fn analyzer(index: &Arc<AdminComponentIndexer>) -> AdminAnalyzer {
        AdminAnalyzer::new(Some(index.clone()))
    }

    pub(super) fn analyze(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
    ) -> Vec<Problem> {
        analyzer(index).analyze(&ctx(), document).expect("analyze")
    }

    pub(super) fn completions(
        index: &Arc<AdminComponentIndexer>,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        swlsp_lsp_completion::AdminCompletionProvider::new(index.clone())
            .get_completions(&ctx(), request)
    }

    pub(super) fn hover(
        root: &str,
        index: &Arc<AdminComponentIndexer>,
        request: &HoverRequest,
    ) -> Option<protocol::Hover> {
        swlsp_lsp_hover::AdminHoverProvider::new(root, index.clone())
            .get_hover(&ctx(), request)
            .expect("hover")
    }

    pub(super) fn definitions(
        index: &Arc<AdminComponentIndexer>,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        swlsp_lsp_definition::AdminDefinitionProvider::new(index.clone())
            .get_definition(&ctx(), request)
    }

    pub(super) fn references(
        index: &Arc<AdminComponentIndexer>,
        request: &ReferenceRequest,
    ) -> Vec<protocol::Location> {
        swlsp_lsp_reference::AdminReferenceProvider::new(Some(index.clone()))
            .get_references(&ctx(), request)
            .expect("references")
    }

    pub(super) fn markup_tokens(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
    ) -> Vec<SemanticToken> {
        swlsp_lsp_semantic::AdminMarkupProvider::new(index.clone())
            .get_semantic_tokens(
                &ctx(),
                &SemanticTokensRequest {
                    params: Default::default(),
                    document: document.clone(),
                },
            )
            .expect("semantic tokens")
    }

    pub(super) fn effective(index: &AdminComponentIndexer, name: &str) -> VueComponent {
        index
            .get_effective_component(name)
            .expect("effective component")
            .unwrap_or_else(|| panic!("component {name} not found"))
    }

    pub(super) fn template_document(path: &str, source: &str, version: i32) -> Arc<TextDocument> {
        new_document(file_uri(path), source, version)
    }

    pub(super) fn components(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let workspace = &f.workspace;
        let php = &f.php;
        let index = workspace_admin_index(workspace);
        let classes = php.class_symbols();
        check_gt!(classes.len(), 5_000);
        check!(
            php.find_class("Shopware\\Core\\Kernel").is_some(),
            "the production Shopware kernel must be indexed"
        );
        let admin_components = index.get_all_components().expect("components");
        assert!(!admin_components.is_empty());
        let vue_script_setup_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/adapter/_mocks_/example-extendable-script-setup-component.vue"
        );
        let vue_script_setup = index
            .get_component_definition(&vue_script_setup_path)
            .expect("component definition");
        match &vue_script_setup {
            None => record_failure(
                "real Administration Vue SFC must be indexed (fixture absent on this checkout)"
                    .into(),
            ),
            Some(definition) => {
                check_eq!(vue_script_setup_path, definition.template_path);
                check_contains!(admin_prop_names(&definition.props), "multiplier".to_owned());
                check_contains!(admin_prop_names(&definition.props), "added".to_owned());
                for member in [
                    "baseValue",
                    "multipliedValue",
                    "addedValue",
                    "reactiveValue",
                    "increment",
                    "privateStuff",
                    "message",
                ] {
                    check!(
                        component_definition_member_named(Some(definition), member).is_some(),
                        "script-setup member {member} must be indexed"
                    );
                }
            }
        }

        let mut template_paths = BTreeSet::new();
        let mut component_names = BTreeSet::new();
        let mut problem_counts: BTreeMap<String, usize> = BTreeMap::new();
        let mut problem_samples: BTreeMap<String, Vec<String>> = BTreeMap::new();
        let mut missing_required_counts: BTreeMap<String, usize> = BTreeMap::new();
        let mut missing_required_samples: HashMap<String, String> = HashMap::new();
        let mut unknown_template_member = Vec::new();
        let (mut effective_count, mut prop_count, mut typed, mut required, mut documented) =
            (0usize, 0usize, 0usize, 0usize, 0usize);
        let (mut exact_props, mut identifier_props, mut closed_values) = (0usize, 0usize, 0usize);
        let (mut events, mut exact_events, mut slots, mut exact_slots) =
            (0usize, 0usize, 0usize, 0usize);
        let (mut slot_members, mut exact_slot_members) = (0usize, 0usize);
        let mut missing_props = Vec::new();
        let mut missing_events = Vec::new();
        let mut missing_slots = Vec::new();
        let mut missing_slot_members = Vec::new();
        let analyzer = analyzer(&index);
        for registration in &admin_components {
            if !component_names.insert(registration.name.clone()) {
                continue;
            }
            let component = index
                .get_effective_component(&registration.name)
                .expect("effective component");
            if let Some(component) = &component {
                effective_count += 1;
                for prop in &component.props {
                    prop_count += 1;
                    if !prop.documentation.trim().is_empty() {
                        documented += 1;
                    }
                    if !prop.type_.trim().is_empty() {
                        typed += 1;
                    }
                    if prop.required {
                        required += 1;
                    }
                    let has_exact = exact(&prop.name_range);
                    if has_exact {
                        exact_props += 1;
                    } else {
                        missing_props.push(format!(
                            "{}.{} ({}:{}, range={:?})",
                            component.name,
                            prop.name,
                            relative(root, &prop.file_path),
                            prop.line,
                            prop.name_range
                        ));
                    }
                    if prop.name_range.identifier && has_exact {
                        identifier_props += 1;
                    }
                    if vue_prop_allowed_values(prop).1 {
                        closed_values += 1;
                    }
                }
                for event in component.component_events() {
                    events += 1;
                    if exact(&event.name_range) {
                        exact_events += 1;
                        continue;
                    }
                    missing_events.push(format!(
                        "{}.{} ({}:{})",
                        component.name,
                        canonical_event_name(&event.name),
                        relative(root, &event.file_path),
                        event.line
                    ));
                }
                for slot in &component.slots {
                    slots += 1;
                    if exact(&slot.name_range) {
                        exact_slots += 1;
                    } else {
                        missing_slots.push(format!(
                            "{}.{} ({}:{})",
                            component.name,
                            slot.display_name(),
                            relative(root, &slot.file_path),
                            slot.line
                        ));
                    }
                    for member in &slot.members {
                        slot_members += 1;
                        if exact(&member.name_range) {
                            exact_slot_members += 1;
                            continue;
                        }
                        missing_slot_members.push(format!(
                            "{}.{}.{} ({}:{})",
                            component.name,
                            slot.display_name(),
                            member.name,
                            relative(root, &member.file_path),
                            member.line
                        ));
                    }
                }
            }
            let Some(component) = &component else {
                continue;
            };
            if component.template_path.is_empty()
                || !template_paths.insert(component.template_path.clone())
            {
                continue;
            }
            let source = read(&component.template_path);
            let document = template_document(&component.template_path, &source, 1);
            let problems = analyzer.analyze(&ctx(), &document).expect("analyze");
            let relative_template = relative(root, &component.template_path);
            for problem in problems {
                let problem_id = problem.id.to_string();
                *problem_counts.entry(problem_id.clone()).or_default() += 1;
                if problem_id == "admin.component.missing-required-prop" {
                    let component_name = payload_str(&problem, "componentName");
                    let prop_name = payload_str(&problem, "propName");
                    if !component_name.is_empty() && !prop_name.is_empty() {
                        let key = format!("{component_name}.{prop_name}");
                        *missing_required_counts.entry(key.clone()).or_default() += 1;
                        missing_required_samples
                            .entry(key)
                            .or_insert_with(|| relative_template.clone());
                    }
                }
                let limit = if problem_id == "admin.component.unknown-slot-prop"
                    || problem_id == "admin.component.unknown-vue-member"
                {
                    20
                } else {
                    5
                };
                let samples = problem_samples.entry(problem_id.clone()).or_default();
                if samples.len() < limit {
                    let mut sample = format!("{relative_template}: {}", problem.message);
                    if problem_id == "admin.component.unknown-vue-member" {
                        let binding = payload_str(&problem, "bindingName");
                        if let Some(member) = component.template_member(&binding) {
                            sample += &format!(
                                " [type={:?}, open={}, source={:?}, context={}]",
                                member.type_,
                                member.open_runtime_shape,
                                member.source_expression,
                                relative(root, &member.type_context_path)
                            );
                        }
                    }
                    samples.push(sample);
                }
                if problem_id == "admin.component.unknown-template-member" {
                    unknown_template_member
                        .push(format!("{relative_template}: {}", problem.message));
                }
            }
        }
        check_ge!(effective_count, 1_000);
        check_ge!(prop_count, 3_000);
        check_ge!(typed, 2_900);
        check_ge!(required, 700);
        check_ge!(documented, 10);
        check_eq!(
            0,
            problem_counts
                .get("admin.module-route.not-found")
                .copied()
                .unwrap_or(0),
            "all static module routes in the real project must be indexed"
        );
        check_eq!(
            0,
            problem_counts
                .get("admin.privilege.not-found")
                .copied()
                .unwrap_or(0),
            "Shopware's built-in admin ACL privilege must be recognized"
        );
        check_eq!(prop_count, exact_props);
        check_ge!(closed_values, 50);
        check_gt!(events, 0);
        missing_props.sort();
        check_empty!(missing_props);
        missing_events.sort();
        check_empty!(missing_events);
        check_eq!(events, exact_events);
        check_gt!(slots, 0);
        missing_slots.sort();
        check_empty!(missing_slots);
        check_eq!(slots, exact_slots);
        check_gt!(slot_members, 0);
        missing_slot_members.sort();
        check_empty!(missing_slot_members);
        check_eq!(slot_members, exact_slot_members);
        eprintln!(
            "Administration component contracts: effective={effective_count}, props={prop_count}, typed={typed}, required={required}, documented={documented}, exact_prop_ranges={exact_props}, identifier_prop_ranges={identifier_props}, closed_values={closed_values}, events={events}, exact_event_ranges={exact_events}, slots={slots}, exact_slot_ranges={exact_slots}, slot_members={slot_members}, exact_slot_member_ranges={exact_slot_members}"
        );
        for (id, count) in &problem_counts {
            eprintln!(
                "Administration template diagnostics: {id}={count} samples={}",
                problem_samples
                    .get(id)
                    .map(|s| s.join(" | "))
                    .unwrap_or_default()
            );
        }
        for (key, count) in &missing_required_counts {
            eprintln!(
                "Administration missing required prop: {key}={count} sample={}",
                missing_required_samples[key]
            );
        }
        check_empty!(
            unknown_template_member,
            "indexed Administration template scope should not report typo false positives"
        );

        let cms_elements = index
            .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)
            .expect("cms");
        check_ge!(cms_elements.len(), 20);
        let cms_blocks = index
            .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Block)
            .expect("cms");
        check_ge!(cms_blocks.len(), 30);
        let image_elements = index
            .get_cms_registration(AdminCMSRegistrationKind::Element, "image")
            .expect("cms");
        assert!(!image_elements.is_empty());
        check_eq!("sw-cms-el-image", image_elements[0].component);
        check_eq!("sw-cms-el-config-image", image_elements[0].config_component);
        check_eq!(
            "sw-cms-el-preview-image",
            image_elements[0].preview_component
        );
        let usages = index
            .get_symbol_usages(&AdminSymbolTarget {
                kind: AdminSymbolKind::Component,
                name: "sw-cms-el-image".into(),
                ..Default::default()
            })
            .expect("usages");
        let element_path = swlsp_uriutil::filepath::clean(&image_elements[0].file_path);
        let linked = usages.iter().any(|set| {
            swlsp_uriutil::filepath::clean(&set.file_path) == element_path
                && set
                    .occurrences
                    .iter()
                    .any(|occurrence| !occurrence.declaration)
        });
        check!(
            linked,
            "CMS component link must be indexed as a component usage"
        );
        let image_blocks = index
            .get_cms_registration(AdminCMSRegistrationKind::Block, "image-two-column")
            .expect("cms");
        assert!(!image_blocks.is_empty());
        check_eq!(
            "sw-cms-preview-image-two-column",
            image_blocks[0].preview_component
        );
        check_eq!(2, image_blocks[0].slots.len());
        assert!(
            !index
                .get_component("sw-product-list")
                .expect("component")
                .is_empty()
        );
        let role_general = effective(&index, "sw-users-permissions-role-view-general");
        check_contains!(admin_prop_names(&role_general.props), "role".to_owned());
        check_contains!(
            admin_prop_names(&role_general.props),
            "isLoading".to_owned()
        );
        check_contains!(role_general.data, "mcpIntegrations".to_owned());
        check_contains!(role_general.data, "showMcpModal".to_owned());
        check_contains!(role_general.injected, "repositoryFactory".to_owned());
        check_contains!(role_general.computed, "roleId".to_owned());
        check_contains!(role_general.methods, "onOpenMcpModal".to_owned());
        check!(
            slash(&role_general.template_path)
                .ends_with("/sw-users-permissions-role-view-general.html.twig")
        );
        let typo = template_document(
            &role_general.template_path,
            r#"<sw-users-permissions-role-view-general :role="role" :is-laoding="isLoading" />"#,
            1,
        );
        let typo_problems = analyze(&index, &typo);
        require_len!(typo_problems, 1);
        check_eq!(
            "admin.component.unknown-prop",
            typo_problems[0].id.to_string()
        );
        check_contains!(
            payload_strings(&typo_problems[0], "suggestions"),
            "is-loading".to_owned()
        );
        let cms_slot = effective(&index, "sw-cms-slot");
        let element_config = cms_slot.template_member("elementConfig");
        check!(element_config.is_some());
        check_eq!(
            AdminCMSRegistrationKind::Element,
            element_config.unwrap_or_default().cms_registry_kind
        );
        let registry = cms_slot.template_member("cmsElements");
        check!(registry.is_some());
        check_eq!(
            AdminCMSRegistrationKind::Element,
            registry.unwrap_or_default().cms_registry_kind
        );
        let mixins = index.get_mixin("validation").expect("mixin");
        assert!(!mixins.is_empty());
        check_contains!(
            admin_prop_names(&mixins[0].definition.props),
            "validation".to_owned()
        );
        check_contains!(
            mixins[0].definition.injected,
            "validationService".to_owned()
        );
        check_contains!(mixins[0].definition.methods, "validate".to_owned());
        check_contains!(mixins[0].definition.methods, "validateRule".to_owned());
        let login = effective(&index, "sw-login-login");
        check_contains!(login.data, "loginConfig".to_owned());
        check_contains!(login.methods, "doSsoForwarding".to_owned());
        check_contains!(login.emits, "login-success".to_owned());
    }

    fn inlay_hints(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
    ) -> Vec<protocol::InlayHint> {
        let mut params = protocol::InlayHintParams::default();
        params.text_document.uri = document.uri.clone();
        params.range.end = position(document, document.source().len() as u32);
        swlsp_lsp_inlay::AdminParameterProvider::new(index.clone())
            .get_inlay_hints(
                &ctx(),
                &InlayHintRequest {
                    params,
                    document: document.clone(),
                },
            )
            .expect("inlay hints")
    }

    fn binding(
        index: &AdminComponentIndexer,
        document: &TextDocument,
        offset: usize,
        path: &str,
    ) -> Option<TwigVueBinding> {
        index
            .resolve_twig_vue_binding(
                &document.root().expect("tree"),
                document.source(),
                offset as u32,
                path,
            )
            .expect("binding")
    }

    pub(super) fn contracts(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let model_editor = effective(&index, "sw-model-editor");
        let change_position = model_editor.template_member("changeModelPosition");
        check!(change_position.is_some());
        check_eq!(
            "(position: { x: number; y: number; z: number }) => void",
            change_position.unwrap_or_default().type_
        );
        let model_source = r#"<button @click="changeModelPosition({ x: 1, y: 2, z: 3 })" />"#;
        let model_document = template_document(&model_editor.template_path, model_source, 1);
        let model_offset = (offset_of(model_source, "z: 3") + "z: ".len()) as u32;
        let signature = swlsp_lsp_signature::AdminSignatureProvider::new(index.clone())
            .get_signature_help(
                &ctx(),
                &real_world_signature_request(&model_document, model_offset),
            )
            .expect("signature")
            .expect("signature help");
        require_len!(signature.signatures, 1);
        check_eq!(
            "changeModelPosition(position: { x: number; y: number; z: number }): void",
            signature.signatures[0].label
        );
        let hints = inlay_hints(&index, &model_document);
        require_len!(hints, 1);
        check_eq!(protocol::InlayHintLabel::from("position:"), hints[0].label);
        check_str!(
            hints[0].tooltip,
            "changeModelPosition(position: { x: number; y: number; z: number }): void"
        );
        let slot_card = effective(&index, "sw-card");
        check_str!(slot_card.deprecated, "use mt-card instead");
        check!(slot_card.component_slot("default").is_some());
        let entity_listing = effective(&index, "sw-entity-listing");
        let items = entity_listing.component_prop("items");
        check!(items.is_some());
        check_str!(
            items.map(|p| p.deprecated.clone()).unwrap_or_default(),
            "Use `dataSource` prop instead"
        );
        let block_component = effective(&index, "sw-newsletter-recipient-filter-switch");
        let block = block_component
            .component_block("sw_newsletter_recipient_filter_switch_field")
            .cloned();
        check!(block.is_some());
        let block = block.unwrap_or_default();
        check_str!(
            block.deprecated,
            "Block will be removed without replacement"
        );
        check!(block.name_range.identifier);
        check!(
            slash(&block.file_path).ends_with("/sw-newsletter-recipient-filter-switch.html.twig")
        );
        let member_component = effective(&index, "sw-settings-snippet-set-list");
        let member = member_component.template_member("getNoPermissionsTooltip");
        check!(member.is_some());
        let member = member.unwrap_or_default();
        check_eq!(VueComponentMemberKind::Method, member.kind);
        check_str!(member.deprecated, "Will be removed without replacement");
        check!(member.name_range.identifier);
        check_gt!(member.line, 0);
        check!(slash(&member.file_path).ends_with("/sw-settings-snippet-set-list/index.js"));
        let checkbox = effective(&index, "sw-checkbox-field");
        check!(checkbox.component_model("v-model:model-value").is_some());
        let typo = template_document(
            &slot_card.template_path,
            r#"<sw-card><template #defualt>Content</template></sw-card><sw-checkbox-field v-model:modle-value="value" />"#,
            1,
        );
        let mut suggestions: HashMap<String, Vec<String>> = HashMap::new();
        for problem in analyze(&index, &typo) {
            let values = payload_strings(&problem, "suggestions");
            if problem
                .payload
                .as_ref()
                .and_then(|p| p.get("suggestions"))
                .is_some_and(|v| !v.is_null())
            {
                suggestions.insert(problem.id.to_string(), values);
            }
        }
        check_contains!(
            suggestions
                .get("admin.component.unknown-slot")
                .cloned()
                .unwrap_or_default(),
            "default".to_owned()
        );
        check_contains!(
            suggestions
                .get("admin.component.unknown-model")
                .cloned()
                .unwrap_or_default(),
            "model-value".to_owned()
        );
        let deprecation = template_document(
            &slot_card.template_path,
            r#"<sw-card></sw-card><sw-entity-listing :items="items" :repository="repository" />"#,
            2,
        );
        let mut deprecation_ids = BTreeSet::new();
        for problem in analyze(&index, &deprecation) {
            if problem.tags == vec![protocol::DiagnosticTag::DEPRECATED] {
                deprecation_ids.insert(problem.id.to_string());
                check_eq!(Some(protocol::DiagnosticSeverity::HINT), problem.severity);
            }
        }
        check!(deprecation_ids.contains("admin.component.deprecated"));
        check!(deprecation_ids.contains("admin.component.deprecated-prop"));
        let deprecated_member_source = "{{ getNoPermissionsTooltip(role) }}";
        let deprecated_member_document =
            template_document(&member_component.template_path, deprecated_member_source, 3);
        let mut found = false;
        for problem in analyze(&index, &deprecated_member_document) {
            if problem.id.to_string() != "admin.component.deprecated-member" {
                continue;
            }
            found = true;
            check_eq!(Some(protocol::DiagnosticSeverity::HINT), problem.severity);
            check_eq!(vec![protocol::DiagnosticTag::DEPRECATED], problem.tags);
            check_str!(problem.message, "Will be removed without replacement");
        }
        check!(found);
        let tooltip = offset_of(deprecated_member_source, "getNoPermissionsTooltip");
        let items = completions(
            &index,
            &real_world_completion_request(
                &deprecated_member_document,
                (tooltip + "getNoPermissionsTool".len()) as u32,
            ),
        );
        let completion = real_world_completion_by_label(&items, "getNoPermissionsTooltip");
        check!(completion.deprecated);
        check_str!(
            completion
                .documentation
                .map(|d| d.value)
                .unwrap_or_default(),
            "Will be removed without replacement"
        );
        let member_hover = hover(
            root,
            &index,
            &real_world_hover_request(
                &deprecated_member_document,
                (tooltip + "getNoPermissions".len()) as u32,
            ),
        )
        .expect("deprecated member hover");
        check_str!(member_hover.contents.value, "Deprecated");
        check_str!(
            member_hover.contents.value,
            "Will be removed without replacement"
        );
        let get_slots = slot_card.template_member("getSlots");
        check!(get_slots.is_some());
        check_eq!(
            "() => Record<string, Function | undefined>",
            get_slots.unwrap_or_default().type_
        );
        assert!(!slot_card.template_path.is_empty());
        let card_source = read(&slot_card.template_path);
        let card_document = template_document(&slot_card.template_path, &card_source, 1);
        let name_position = offset_of(&card_source, r#":name="name""#);
        let name_binding = binding(
            &index,
            &card_document,
            name_position + r#":name="na"#.len(),
            &slot_card.template_path,
        )
        .expect("slot card name binding");
        check_eq!("string", name_binding.type_);
        let slot_loop_source = r#"<div v-for="(slot, name, index) in getSlots()"><span :title="name.length" :data-name="name."></span><span :data-index="index."></span></div>"#;
        let slot_loop_document = template_document(&slot_card.template_path, slot_loop_source, 1);
        let slot_name_offset = (last_offset_of(slot_loop_source, "name.") + "name.".len()) as u32;
        let usage_offset = offset_of(slot_loop_source, "name.length") + 1;
        let slot_name_binding = binding(
            &index,
            &slot_loop_document,
            usage_offset,
            &slot_card.template_path,
        )
        .expect("slot name binding");
        check_eq!("string", slot_name_binding.type_);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&slot_loop_document, slot_name_offset),
        ));
        for member in ["length", "toLowerCase", "trim"] {
            check_contains!(labels, member.to_owned());
        }
        let index_offset = (offset_of(slot_loop_source, "index.") + "index.".len()) as u32;
        check_contains!(
            real_world_completion_labels(&completions(
                &index,
                &real_world_completion_request(&slot_loop_document, index_offset)
            )),
            "toFixed".to_owned()
        );
        let length_offset = (offset_of(slot_loop_source, "name.length") + "name.len".len()) as u32;
        let slot_name_hover = hover(
            root,
            &index,
            &real_world_hover_request(&slot_loop_document, length_offset),
        )
        .expect("slot name hover");
        check_str!(
            slot_name_hover.contents.value,
            "**property** `name.length`: `number`"
        );
        let keys_source = r#"<div v-for="slotName in Object.keys(getSlots())"><span :title="slotName.length" :data-name="slotName."></span></div>"#;
        let keys_document = template_document(&slot_card.template_path, keys_source, 1);
        let keys_offset = (last_offset_of(keys_source, "slotName.") + "slotName.".len()) as u32;
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&keys_document, keys_offset),
        ));
        for member in ["length", "toLowerCase", "trim"] {
            check_contains!(labels, member.to_owned());
        }
        let dashboard = effective(&index, "sw-dashboard-index");
        assert!(!dashboard.template_path.is_empty());
        let dashboard_source = read(&dashboard.template_path);
        let dashboard_document = template_document(&dashboard.template_path, &dashboard_source, 1);
        let key_position = offset_of(&dashboard_source, "${key}Link");
        let key_binding = binding(
            &index,
            &dashboard_document,
            key_position + "${k".len(),
            &dashboard.template_path,
        )
        .expect("dashboard key binding");
        check_eq!("string", key_binding.type_);
        let topbar = effective(&index, "sw-app-topbar-sidebar");
        let sidebars = topbar.template_member("sidebars");
        check!(sidebars.is_some());
        check_str!(sidebars.unwrap_or_default().type_, "SidebarItemEntry");
        assert!(!topbar.template_path.is_empty());
        let topbar_source = read(&topbar.template_path);
        let topbar_document = template_document(&topbar.template_path, &topbar_source, 1);
        let active_position = offset_of(&topbar_source, "sidebars[0].active");
        let active_offset = (active_position + "sidebars[0].ac".len()) as u32;
        let active = index
            .resolve_twig_vue_instance_member(
                &topbar_document.root().expect("tree"),
                topbar_document.source(),
                active_offset,
                &topbar.template_path,
            )
            .expect("instance member")
            .expect("topbar active member");
        check!(active.member_found);
        check_eq!("boolean", active.member.type_);
        check_eq!("sidebars[0].active", active.qualified_name());
        check!(slash(&active.member.definition_path).ends_with("/app/store/sidebar.store.ts"));
        let completion_source = r#"<div :class="sidebars[0]."></div>"#;
        let completion_document = template_document(&topbar.template_path, completion_source, 1);
        let completion_offset =
            (offset_of(completion_source, "sidebars[0].") + "sidebars[0].".len()) as u32;
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&completion_document, completion_offset),
        ));
        for member in ["active", "baseUrl"] {
            check_contains!(labels, member.to_owned());
        }
        let topbar_hover = hover(
            root,
            &index,
            &real_world_hover_request(&topbar_document, active_offset),
        )
        .expect("topbar hover");
        check_str!(
            topbar_hover.contents.value,
            "**property** `sidebars[0].active`: `boolean`"
        );
        let topbar_definitions = definitions(
            &index,
            &real_world_definition_request_at(&topbar_document, active_offset),
        );
        require_len!(topbar_definitions, 1);
        check!(slash(&topbar_definitions[0].uri).ends_with("/app/store/sidebar.store.ts"));
        for problem in analyze(&index, &topbar_document) {
            check!(
                problem.id.to_string() != "admin.component.unknown-vue-member",
                "unexpected indexed Administration markup problem: {}",
                problem.message
            );
        }
        let markup_source = "<sw-users-permissions-role-view-general : />";
        let markup_document = template_document(
            &format!(
                "{root}/src/Administration/Resources/app/administration/src/real-world.html.twig"
            ),
            markup_source,
            1,
        );
        let markup_offset = offset_of(markup_source, ":") as u32;
        let mut markup_params = protocol::CompletionParams::default();
        markup_params.text_document.uri = markup_document.uri.clone();
        let mut markup_syntax = syntax(
            &markup_document,
            node_at(&markup_document, markup_offset),
            Some(markup_offset),
        );
        markup_syntax.language = None;
        let labels = real_world_completion_labels(&completions(
            &index,
            &CompletionRequest {
                params: markup_params,
                syntax: markup_syntax,
            },
        ));
        for label in ["role", ":role", "is-loading", ":is-loading"] {
            check_contains!(
                labels,
                label.to_owned(),
                "expected effective component prop completion {label}"
            );
        }
        let dynamic_source = r#"<component :is="legacy ? 'sw-card' : 'sw-card-deprecated'"><template #def /></component>"#;
        let dynamic_document = new_document(markup_document.uri.clone(), dynamic_source, 2);
        let dynamic_offset = (offset_of(dynamic_source, "#def") + "#def".len()) as u32;
        check_contains!(
            real_world_completion_labels(&completions(
                &index,
                &real_world_completion_request(&dynamic_document, dynamic_offset - 1)
            )),
            "default".to_owned(),
            "expected the common real sw-card slot below a dynamic component"
        );
        let dynamic_source = r#"<component :is="legacy ? 'sw-card' : 'sw-card-deprecated'"><template #default /></component>"#;
        let dynamic_document = new_document(markup_document.uri.clone(), dynamic_source, 3);
        let dynamic_offset = (offset_of(dynamic_source, "default") + 2) as u32;
        let dynamic_hover = hover(
            root,
            &index,
            &real_world_hover_request(&dynamic_document, dynamic_offset),
        )
        .expect("dynamic slot hover");
        check_str!(dynamic_hover.contents.value, "`sw-card`");
        check_str!(dynamic_hover.contents.value, "`sw-card-deprecated`");
        let dynamic_definitions = definitions(
            &index,
            &real_world_definition_request_at(&dynamic_document, dynamic_offset),
        );
        check_ge!(dynamic_definitions.len(), 2);
        let highlighted = markup_tokens(&index, &dynamic_document)
            .iter()
            .any(|token| {
                token_text(&dynamic_document, token) == "#default"
                    && token.token_type == protocol::SEMANTIC_TOKEN_PROPERTY
            });
        check!(highlighted);
        for name in ["sw-data-grid", "sw-entity-listing"] {
            let component = effective(&index, name);
            let slot = component.component_slot("column-name");
            check!(slot.is_some(), "expected {name} dynamic column slot");
            check!(
                slot.and_then(|slot| slot.member("item")).is_some(),
                "expected {name} column item payload"
            );
        }
        let payload_completion_source = r#"<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="{ it }"></template></component>"#;
        let payload_completion_document =
            template_document(&slot_card.template_path, payload_completion_source, 4);
        let payload_completion_offset =
            (offset_of(payload_completion_source, "{ it") + "{ it".len()) as u32;
        check_contains!(
            real_world_completion_labels(&completions(
                &index,
                &real_world_completion_request(
                    &payload_completion_document,
                    payload_completion_offset - 1
                )
            )),
            "item".to_owned(),
            "expected the inherited real data-grid scoped-slot payload"
        );
        let payload_source = r#"<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="props">{{ props.item }}</template></component>"#;
        let payload_document = template_document(&slot_card.template_path, payload_source, 5);
        let payload_offset = (offset_of(payload_source, "props.item") + "props.it".len()) as u32;
        let payload_hover = hover(
            root,
            &index,
            &real_world_hover_request(&payload_document, payload_offset),
        )
        .expect("payload hover");
        check_str!(payload_hover.contents.value, "sw-data-grid");
        check_str!(payload_hover.contents.value, "sw-entity-listing");
        let payload_definitions = definitions(
            &index,
            &real_world_definition_request_at(&payload_document, payload_offset),
        );
        assert!(!payload_definitions.is_empty());
        check!(slash(&payload_definitions[0].uri).contains("/sw-data-grid/sw-data-grid.html.twig"));
        let payload_references = references(
            &index,
            &real_world_reference_request(&payload_document, payload_offset, true),
        );
        check_ge!(payload_references.len(), 2);
        let mut token_types: HashMap<String, Vec<u32>> = HashMap::new();
        for token in markup_tokens(&index, &payload_document) {
            token_types
                .entry(token_text(&payload_document, &token))
                .or_default()
                .push(token.token_type);
        }
        check_contains!(
            token_types.get("props").cloned().unwrap_or_default(),
            protocol::SEMANTIC_TOKEN_VARIABLE
        );
        check_contains!(
            token_types.get("item").cloned().unwrap_or_default(),
            protocol::SEMANTIC_TOKEN_PROPERTY
        );
        for problem in analyze(&index, &payload_document) {
            check!(
                problem.id.to_string() != "admin.component.unknown-slot-prop",
                "unexpected real scoped-slot payload problem: {}",
                problem.message
            );
        }
        let rename_source = r#"<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="{ item: row }">{{ row.id }}</template></component>"#;
        let rename_document = template_document(&slot_card.template_path, rename_source, 6);
        let rename_offset = (offset_of(rename_source, "row.id") + 1) as u32;
        let rename = swlsp_lsp_refactor::AdminRenameProvider::new(Some(index.clone()))
            .rename(
                &ctx(),
                &real_world_rename_request(&rename_document, rename_offset, "record"),
            )
            .expect("rename")
            .expect("rename edit");
        require_len!(
            rename
                .changes
                .get(&rename_document.uri)
                .cloned()
                .unwrap_or_default(),
            2
        );

        // Dynamic router-view components of the profile page.
        let _profile = effective(&index, "sw-profile-index");
        let profile_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-profile/page/sw-profile-index/sw-profile-index.html.twig"
        );
        let profile_source = read(&profile_path);
        let profile_document = template_document(&profile_path, &profile_source, 7);
        let profile_root = profile_document.root().expect("tree");
        let mut dynamic = None;
        for start_tag in swlsp_parser_twig::query::nodes(
            &profile_root,
            &[swlsp_parser_twig::syntax::HTML_STARTING_TAG],
        ) {
            if let Some(selector) = twig_dynamic_component_selector(&start_tag)
                && selector.expression == "Component"
            {
                dynamic = Some((start_tag, selector));
                break;
            }
        }
        let (tag, selector) = dynamic.expect("profile dynamic tag");
        let (resolved, route_components, complete) = index
            .resolve_dynamic_component_contracts(&profile_path, &selector, Some(&tag))
            .expect("dynamic contracts");
        check!(complete);
        check_eq!(
            vec![
                "frosh-profile-mfa".to_owned(),
                "sw-profile-index-general".to_owned(),
                "sw-profile-index-privacy-preferences".to_owned(),
                "sw-profile-index-search-preferences".to_owned(),
            ],
            resolved.names(),
            "(fails on checkouts without the FroshTools plugin)"
        );
        check_eq!(
            4,
            route_components.len(),
            "(fails on checkouts without the FroshTools plugin)"
        );
        let user_index = offset_of(&profile_source, "\n                            user,");
        let user_offset = (user_index + "\n                            us".len()) as u32;
        let user_hover = hover(
            root,
            &index,
            &real_world_hover_request(&profile_document, user_offset),
        )
        .expect("profile user hover");
        check_str!(user_hover.contents.value, "**prop** `user`");
        check_str!(
            user_hover.contents.value,
            "Component: `sw-profile-index-general`"
        );
        let general = effective(&index, "sw-profile-index-general");
        let user_source = general.symbol_source(AdminSymbolKind::ComponentProp, "user");
        check!(user_source.is_some());
        let usages = index
            .get_symbol_usages(&AdminSymbolTarget {
                kind: AdminSymbolKind::ComponentProp,
                owner: user_source.unwrap_or_default(),
                name: "user".into(),
            })
            .expect("usages");
        let clean_profile = swlsp_uriutil::filepath::clean(&profile_path);
        let dynamic_references = usages
            .iter()
            .filter(|set| swlsp_uriutil::filepath::clean(&set.file_path) == clean_profile)
            .flat_map(|set| &set.occurrences)
            .filter(|occurrence| {
                occurrence.dynamic_router_view
                    && occurrence.dynamic_component_selector == "Component"
            })
            .count();
        check_ge!(dynamic_references, 1);
    }

    fn snippet_analyzer(f: &RealWorldWorkspaceFixture) -> swlsp_lsp_diagnostics::SnippetAnalyzer {
        swlsp_lsp_diagnostics::SnippetAnalyzer::new(
            Some(f.workspace.services().snippets.clone()),
            None,
        )
    }

    fn markup_document(
        root: &str,
        relative_path: &str,
        source: &str,
        version: i32,
    ) -> Arc<TextDocument> {
        template_document(
            &format!("{root}/src/Administration/Resources/app/administration/src/{relative_path}"),
            source,
            version,
        )
    }

    fn allowed(prop: &VueComponentProp) -> (Vec<String>, bool) {
        let (mut values, complete) = vue_prop_allowed_values(prop);
        values.sort();
        (values, complete)
    }

    fn sorted(values: &[&str]) -> Vec<String> {
        let mut values: Vec<String> = values.iter().map(|v| (*v).to_owned()).collect();
        values.sort();
        values
    }

    fn colon_completion_labels(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: u32,
    ) -> Vec<String> {
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = document.uri.clone();
        let mut context = syntax(document, node_at(document, offset), Some(offset));
        context.language = None;
        real_world_completion_labels(&completions(
            index,
            &CompletionRequest {
                params,
                syntax: context,
            },
        ))
    }

    pub(super) fn snippets_and_props(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let snippets = f.workspace.services().snippets.clone();
        let markup_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/real-world.html.twig"
        ));
        assert!(
            !snippets
                .admin_snippet("global.default.save")
                .expect("snippet")
                .is_empty()
        );
        let snippet_completion = |document: &Arc<TextDocument>, offset: usize| {
            real_world_completion_labels(
                &swlsp_lsp_completion::SnippetCompletionProvider::new(Some(snippets.clone()))
                    .get_completions(
                        &ctx(),
                        &real_world_completion_request(document, offset as u32),
                    ),
            )
        };
        let source = r#"<mt-button :label="$t('global.default.sa')" />"#;
        let document = new_document(markup_uri.clone(), source, 2);
        check_contains!(
            snippet_completion(
                &document,
                offset_of(source, "global.default.sa") + "global.default.sa".len()
            ),
            "global.default.save".to_owned()
        );
        let source = r#"<mt-button :label="$t('global.default.save')" />"#;
        let document = new_document(markup_uri.clone(), source, 3);
        let offset = (offset_of(source, "global.default.save") + 3) as u32;
        let locations =
            swlsp_lsp_definition::SnippetDefinitionProvider::new(Some(snippets.clone()))
                .get_definition(&ctx(), &real_world_definition_request_at(&document, offset));
        assert!(!locations.is_empty());
        check!(
            locations
                .iter()
                .any(|l| slash(&l.uri).contains("/administration/src/app/snippet/en.json")),
            "expected an English Administration snippet definition in {locations:#?}"
        );
        let source = "Module.register('demo', {\n        title: 'global.default.sa',\n        navigation: [{ label: 'global.default.save' }],\n    });";
        let document = markup_document(root, "module-real-world.js", source, 4);
        check_contains!(
            snippet_completion(
                &document,
                offset_of(source, "global.default.sa") + "global.default.sa".len()
            ),
            "global.default.save".to_owned()
        );
        let module_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-product/index.js"
        );
        let module_document = template_document(&module_path, &read(&module_path), 1);
        check_ge!(
            swlsp_snippet::admin_javascript_string_references(
                &module_document.root().expect("tree")
            )
            .len(),
            3
        );
        for problem in snippet_analyzer(f)
            .analyze(&ctx(), &module_document)
            .expect("analyze")
        {
            check!(
                problem.id.to_string() != "admin.snippet.missing",
                "{}",
                problem.message
            );
        }
        let grid_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/data-grid/sw-data-grid/sw-data-grid.html.twig"
        );
        let grid_document = template_document(&grid_path, &read(&grid_path), 1);
        check_gt!(
            swlsp_snippet::admin_twig_references(&grid_document.root().expect("tree")).len(),
            5
        );
        for problem in snippet_analyzer(f)
            .analyze(&ctx(), &grid_document)
            .expect("analyze")
        {
            check!(
                problem.id.to_string() != "admin.snippet.missing",
                "{}",
                problem.message
            );
        }
        let meteor_button = effective(&index, "mt-button");
        for prop in ["disabled", "variant", "isLoading"] {
            check_contains!(admin_prop_names(&meteor_button.props), prop.to_owned());
        }
        check_contains!(admin_slot_names(&meteor_button.slots), "default".to_owned());
        let help = effective(&index, "sw-help-sidebar");
        check_str!(
            require_admin_prop(&help.props, "selector").documentation,
            "selector of the element where the sidebar should be appended"
        );
        let upload = effective(&index, "sw-upload-listener");
        for (name, type_) in [
            ("media-upload-add", "UploadTask[]"),
            ("media-upload-finish", "string"),
            ("media-upload-fail", "UploadTask"),
            ("media-upload-cancel", "UploadTask"),
        ] {
            let event = require_admin_event(&upload.component_events(), name);
            check_eq!(type_, event.type_, "{name}");
            check!(event.name_range.declaration, "{name}");
        }
        let address = effective(&index, "sw-address");
        check_str!(
            require_admin_prop(&address.props, "address").type_,
            "Object"
        );
        check_str!(
            require_admin_prop(&address.props, "editLink").type_,
            "PropType<RouteLocationNamedRaw | null>"
        );
        let media_item = effective(&index, "sw-media-media-item");
        check!(require_admin_prop(&media_item.props, "item").required);
        let legacy_array = effective(&index, "sw-cms-el-form-template-contact");
        let form_settings = require_admin_prop(&legacy_array.props, "formSettings");
        check!(form_settings.name_range.declaration);
        check!(!form_settings.name_range.identifier);
        check_gt!(
            form_settings.name_range.end_character,
            form_settings.name_range.start_character
        );
        let source = r#"<sw-cms-el-form-template-contact :form-settings="settings" />"#;
        let document = markup_document(root, "legacy-array-prop-real-world.html.twig", source, 1);
        let offset = (offset_of(source, "form-settings") + 3) as u32;
        let locations = definitions(&index, &real_world_definition_request_at(&document, offset));
        require_len!(locations, 1);
        check_eq!(file_uri(&form_settings.file_path), locations[0].uri);
        let range = &form_settings.name_range;
        check_eq!(
            protocol::Range {
                start: protocol::Position {
                    line: range.start_line,
                    character: range.start_character
                },
                end: protocol::Position {
                    line: range.end_line,
                    character: range.end_character
                },
            },
            locations[0].range
        );
        let slider = effective(&index, "sw-image-slider");
        let (values, complete) = allowed(&require_admin_prop(&slider.props, "navigationType"));
        check!(complete);
        check_eq!(sorted(&["arrow", "button", "all"]), values);
        let source = r#"<sw-image-slider navigation-type="a" />"#;
        let validator_document = markup_document(root, "validator-real-world.html.twig", source, 1);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(
                &validator_document,
                (offset_of(source, r#""a""#) + 2) as u32,
            ),
        ));
        for value in ["arrow", "button", "all"] {
            check_contains!(
                labels,
                value.to_owned(),
                "expected runtime-validator prop value completion {value}"
            );
        }
        let problems = analyze(
            &index,
            &new_document(
                validator_document.uri.clone(),
                r#"<sw-image-slider :images="[]" navigation-type="tabs" />"#,
                2,
            ),
        );
        require_len!(problems, 1);
        check_eq!(
            "admin.component.invalid-prop-value",
            problems[0].id.to_string()
        );
        let skeleton = effective(&index, "sw-skeleton");
        let (skeleton_values, complete) = allowed(&require_admin_prop(&skeleton.props, "variant"));
        check!(complete);
        check_eq!(
            sorted(&[
                "gallery",
                "detail",
                "detail-bold",
                "category",
                "listing",
                "tree-item",
                "tree-item-nested",
                "media",
                "extension-apps",
                "extension-themes",
            ]),
            skeleton_values
        );
        let source = r#"<sw-skeleton variant="g" />"#;
        let local_document =
            markup_document(root, "validator-local-real-world.html.twig", source, 1);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(
                &local_document,
                (offset_of(source, r#""g""#) + 2) as u32,
            ),
        ));
        for value in &skeleton_values {
            check_contains!(
                labels,
                value.clone(),
                "expected validator-local prop value completion {value}"
            );
        }
        let source = r#"<sw-skeleton v-bind="{ variant: 'g' }" />"#;
        let bound_document = new_document(local_document.uri.clone(), source, 2);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&bound_document, (offset_of(source, "'g'") + 2) as u32),
        ));
        for value in &skeleton_values {
            check_contains!(
                labels,
                value.clone(),
                "expected object-bound prop value completion {value}"
            );
        }
        let problems = analyze(
            &index,
            &new_document(
                local_document.uri.clone(),
                r#"<sw-skeleton variant="unknown" />"#,
                2,
            ),
        );
        require_len!(problems, 1);
        check_eq!(
            "admin.component.invalid-prop-value",
            problems[0].id.to_string()
        );
        let problems = analyze(
            &index,
            &new_document(
                local_document.uri.clone(),
                r#"<sw-skeleton v-bind="{ variant: 'unknown' }" />"#,
                3,
            ),
        );
        require_len!(problems, 1);
        check_eq!(
            "admin.component.invalid-prop-value",
            problems[0].id.to_string()
        );
        let access_key = effective(&index, "sw-user-sso-access-key-create-modal");
        let mode = require_admin_prop(&access_key.props, "mode");
        check_eq!("String", mode.type_);
        let (values, complete) = allowed(&mode);
        check!(complete);
        check_eq!(sorted(&["view", "edit", "create"]), values);
        let notifications = effective(&index, "sw-notifications");
        let (values, complete) = allowed(&require_admin_prop(&notifications.props, "position"));
        check!(complete);
        check_eq!(sorted(&["topRight", "bottomRight", ""]), values);
        check_contains!(
            admin_slot_names(&meteor_button.slots),
            "iconFront".to_owned()
        );
        let icon_front = require_admin_slot(&meteor_button.slots, "iconFront");
        check!(!icon_front.file_path.is_empty());
        check_gt!(icon_front.line, 0);
        let size = require_admin_slot_member(&icon_front.members, "size");
        check_eq!("number", size.type_);
        check_eq!(icon_front.file_path, size.file_path);
        check_gt!(size.line, 0);
        let tabs = effective(&index, "sw-tabs-deprecated");
        let tabs_default = require_admin_slot(&tabs.slots, "default");
        let active = require_admin_slot_member(&tabs_default.members, "active");
        check!(
            slash(&active.file_path)
                .ends_with("/app/component/base/sw-tabs-deprecated/sw-tabs-deprecated.html.twig")
        );
        check_gt!(active.line, 0);
        let source = "<mt-button : />";
        let document = markup_document(root, "meteor-real-world.html.twig", source, 1);
        let labels = colon_completion_labels(&index, &document, offset_of(source, ":") as u32);
        for label in ["disabled", ":disabled", "variant", ":is-loading"] {
            check_contains!(
                labels,
                label.to_owned(),
                "expected Meteor component prop completion {label}"
            );
        }
    }

    fn line_slice(source: &str, range: &AdminSourceRange) -> String {
        let line = source
            .split('\n')
            .nth(range.start_line as usize)
            .unwrap_or_default();
        line.get(range.start_character as usize..range.end_character as usize)
            .unwrap_or_default()
            .to_owned()
    }

    pub(super) fn data_grid(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let grid = effective(&index, "sw-data-grid");
        let member = |name: &str| {
            let member = grid.template_member(name);
            check!(member.is_some(), "member {name}");
            member.unwrap_or_default()
        };
        check_eq!("Array", member("currentColumns").type_);
        check_eq!("Array", member("currentVisibleColumns").type_);
        check_str!(member("getDefaultColumns").type_, "=> Array");
        let row_click = require_admin_event(&grid.component_events(), "row-click");
        let actions = require_admin_slot(&grid.slots, "actions");
        require_admin_slot_member(&actions.members, "item");
        require_admin_slot_member(&actions.members, "itemIndex");
        let selection = require_admin_slot(&grid.slots, "selection-content");
        for name in [
            "item",
            "isSelected",
            "isRecordSelectable",
            "selectItem",
            "itemIdentifierProperty",
        ] {
            require_admin_slot_member(&selection.members, name);
        }
        let family = grid.component_slot("column-name").cloned();
        check!(family.is_some());
        let family = family.unwrap_or_default();
        check!(family.is_dynamic_name());
        check_eq!("column-*", family.display_name());
        check!(family.name_range.declaration);
        check!(!family.name_range.identifier);
        check_gt!(
            family.name_range.end_character,
            family.name_range.start_character
        );
        for name in [
            "item",
            "itemIndex",
            "column",
            "columnIndex",
            "compact",
            "isInlineEdit",
            "selectItem",
        ] {
            require_admin_slot_member(&family.members, name);
        }
        let label_family = grid.component_slot("column-label-name").cloned();
        check!(label_family.is_some());
        let label_family = label_family.unwrap_or_default();
        check_eq!("column-label-*", label_family.display_name());
        for name in ["column", "columnIndex"] {
            require_admin_slot_member(&label_family.members, name);
        }
        let router_block = grid
            .component_block("sw_data_grid_columns_render_router_link")
            .cloned();
        check!(router_block.is_some());
        let router_block = router_block.unwrap_or_default();
        for name in ["item", "column"] {
            let member = router_block.scope_member(name).cloned();
            check!(member.is_some(), "expected inherited block input {name}");
            let member = member.unwrap_or_default();
            check!(
                slash(&member.file_path)
                    .ends_with("/app/component/data-grid/sw-data-grid/sw-data-grid.html.twig")
            );
            check_gt!(member.line, 0);
            check_gt!(
                member.name_range.end_character,
                member.name_range.start_character
            );
        }
        check!(
            slash(&row_click.file_path).ends_with("/app/component/data-grid/sw-data-grid/index.js")
        );
        check_gt!(row_click.line, 0);
        check!(row_click.name_range.declaration);
        check!(!row_click.name_range.identifier);
        check_gt!(
            row_click.name_range.end_character,
            row_click.name_range.start_character
        );
        check_eq!(
            "row-click",
            line_slice(&read(&row_click.file_path), &row_click.name_range)
        );
        let template_source = read(&grid.template_path);
        check_eq!(
            "`column-${column.property}`",
            line_slice(&template_source, &family.name_range)
        );
        let document = template_document(&grid.template_path, &template_source, 1);
        let usage = offset_of(&template_source, r#"v-show="column.visible""#);
        let usage_offset = (usage + r#"v-show="col"#.len()) as u32;
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&document, usage_offset),
        ));
        for label in ["column", "columnIndex", "currentColumns"] {
            check_contains!(labels, label.to_owned());
        }
        let member_offset = (usage + r#"v-show="column."#.len()) as u32;
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(&document, member_offset),
        ));
        for label in ["visible", "property", "width", "iconLabel", "sortable"] {
            check_contains!(
                labels,
                label.to_owned(),
                "expected observed sw-data-grid column member {label}"
            );
        }
        check_not_contains!(labels, "currentColumns".to_owned());
        let column_hover = hover(
            root,
            &index,
            &real_world_hover_request(&document, usage_offset),
        )
        .expect("column hover");
        check_str!(column_hover.contents.value, "**v-for local** `column`");
        check_str!(column_hover.contents.value, "Iterates `currentColumns`");
        let property_offset = (usage + r#"v-show="column.v"#.len()) as u32;
        let property_hover = hover(
            root,
            &index,
            &real_world_hover_request(&document, property_offset),
        )
        .expect("property hover");
        check_str!(
            property_hover.contents.value,
            "**property** `column.visible`"
        );
        let locations = definitions(
            &index,
            &real_world_definition_request_at(&document, usage_offset),
        );
        require_len!(locations, 1);
        check_eq!(document.uri, locations[0].uri);
        let declaration = offset_of(&template_source, "column, columnIndex") as u32;
        let (line, character) = document.line_index().position_utf16(declaration);
        check_eq!(line, locations[0].range.start.line);
        check_eq!(character, locations[0].range.start.character);
        let refs = references(
            &index,
            &real_world_reference_request(&document, usage_offset, true),
        );
        check_gt!(refs.len(), 10);
        for location in &refs {
            check_eq!(document.uri, location.uri);
        }
        let rename = swlsp_lsp_refactor::AdminRenameProvider::new(Some(index.clone()))
            .rename(
                &ctx(),
                &real_world_rename_request(&document, usage_offset, "headerColumn"),
            )
            .expect("rename")
            .expect("rename edit");
        require_len!(rename.changes, 1);
        check_gt!(
            rename
                .changes
                .get(&document.uri)
                .map(Vec::len)
                .unwrap_or_default(),
            10
        );
        let event = offset_of(&template_source, "onClickHeaderCell($event, column)");
        let event_offset = (event + "onClickHeaderCell($ev".len()) as u32;
        let event_hover = hover(
            root,
            &index,
            &real_world_hover_request(&document, event_offset),
        )
        .expect("event hover");
        check_str!(
            event_hover.contents.value,
            "**event payload** `$event`: `MouseEvent`"
        );
        let tokens = markup_tokens(&index, &document);
        require_semantic_token_text(
            &document,
            &tokens,
            "$event",
            protocol::SEMANTIC_TOKEN_VARIABLE,
        );
        require_semantic_token_text(
            &document,
            &tokens,
            "visible",
            protocol::SEMANTIC_TOKEN_PROPERTY,
        );
    }

    fn vue_member(
        index: &AdminComponentIndexer,
        document: &TextDocument,
        offset: u32,
        path: &str,
    ) -> Option<ResolvedTwigVueMember> {
        index
            .resolve_twig_vue_member(
                &document.root().expect("tree"),
                document.source(),
                offset,
                path,
            )
            .expect("resolve member")
    }

    fn check_no_unknown_vue_member(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        what: &str,
    ) {
        for problem in analyze(index, document) {
            check!(
                problem.id.to_string() != "admin.component.unknown-vue-member",
                "unexpected {what} markup problem: {}",
                problem.message
            );
        }
    }

    pub(super) fn entities(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let handling = effective(&index, "sw-settings-country-address-handling");
        let rows = handling.template_member("addressFormatRows");
        check!(rows.is_some());
        let rows = rows.unwrap_or_default();
        check_eq!("AddressFormatRow[]", rows.type_);
        let shape = index
            .resolve_vue_type("AddressFormatRow", &rows.file_path, &[])
            .expect("type");
        check!(
            shape.complete,
            "expected AddressFormatRow from {} to resolve: {shape:#?}",
            rows.file_path
        );
        check_eq!(5, shape.members.len());
        let address_source = read(&handling.template_path);
        let address_document = template_document(&handling.template_path, &address_source, 1);
        let by_template = index
            .get_component_by_template_path(&handling.template_path)
            .expect("by template")
            .expect("component by template");
        check!(
            by_template.template_member("addressFormatRows").is_some(),
            "component by template: {by_template:#?}"
        );
        let usage = offset_of(&address_source, r#"v-if="row.isPlaceholder""#);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(
                &address_document,
                (usage + r#"v-if="row."#.len()) as u32,
            ),
        ));
        for member in ["index", "isPlaceholder", "isSource", "key", "snippet"] {
            check_contains!(
                labels,
                member.to_owned(),
                "expected AddressFormatRow member {member}"
            );
        }
        let snippet_position = offset_of(&address_source, r#":value="row.snippet""#);
        let snippet_offset = (snippet_position + r#":value="row.sni"#.len()) as u32;
        let resolved = vue_member(
            &index,
            &address_document,
            snippet_offset,
            &handling.template_path,
        )
        .expect("row.snippet");
        check!(resolved.member_found);
        check_eq!(
            "string[]",
            resolved.member.type_,
            "binding={:#?} member={:#?}",
            resolved.binding,
            resolved.member
        );
        let snippet_hover = hover(
            root,
            &index,
            &real_world_hover_request(&address_document, snippet_offset),
        )
        .expect("hover");
        check_str!(
            snippet_hover.contents.value,
            "**property** `row.snippet`: `string[]`"
        );
        let locations = definitions(
            &index,
            &real_world_definition_request_at(&address_document, snippet_offset),
        );
        require_len!(locations, 1);
        check_eq!(file_uri(&rows.file_path), locations[0].uri);
        check_no_unknown_vue_member(&index, &address_document, "typed Administration");

        let grid = effective(&index, "sw-order-customer-grid");
        let customer = grid.template_member("customer");
        check!(customer.is_some());
        let customer = customer.unwrap_or_default();
        check_eq!("Entity<'customer'> | null", customer.type_);
        let shape = index
            .resolve_vue_type(&customer.type_, &customer.file_path, &[])
            .expect("type");
        check!(shape.complete);
        let names = real_world_admin_member_names(&shape.members);
        for member in [
            "id",
            "firstName",
            "lastName",
            "salesChannelId",
            "addresses",
            "getEntityName",
        ] {
            check_contains!(names, member.to_owned());
        }
        let grid_source = read(&grid.template_path);
        let grid_document = template_document(&grid.template_path, &grid_source, 1);
        let sales_channel = offset_of(&grid_source, "customer.salesChannelId");
        let sales_offset = (sales_channel + "customer.salesChannel".len()) as u32;
        let resolved = index
            .resolve_twig_vue_instance_member(
                &grid_document.root().expect("tree"),
                grid_document.source(),
                sales_offset,
                &grid.template_path,
            )
            .expect("instance member")
            .expect("customer.salesChannelId");
        check!(resolved.member_found);
        check_eq!("string", resolved.member.type_);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(
                &grid_document,
                (sales_channel + "customer.".len()) as u32,
            ),
        ));
        for member in ["firstName", "lastName", "salesChannelId", "addresses"] {
            check_contains!(labels, member.to_owned());
        }
        match hover(
            root,
            &index,
            &real_world_hover_request(&grid_document, sales_offset),
        ) {
            Some(value) => check_str!(
                value.contents.value,
                "**property** `customer.salesChannelId`: `string`"
            ),
            None => record_failure("customer.salesChannelId hover is nil".into()),
        }
        let locations = definitions(
            &index,
            &real_world_definition_request_at(&grid_document, sales_offset),
        );
        if locations.len() == 1 {
            check_eq!(file_uri(&resolved.member.definition_path), locations[0].uri);
        } else {
            record_failure(format!(
                "customer.salesChannelId definitions: {locations:?}"
            ));
        }
        check_no_unknown_vue_member(&index, &grid_document, "customer entity");

        let page_form = effective(&index, "sw-cms-page-form");
        let page = page_form.template_member("page");
        check!(page.is_some());
        check_str!(page.unwrap_or_default().type_, "Entity<'cms_page'>");
        let page_source = read(&page_form.template_path);
        let page_document = template_document(&page_form.template_path, &page_source, 1);
        let section_offset = (offset_of(&page_source, "section.id") + "section.i".len()) as u32;
        let resolved = vue_member(
            &index,
            &page_document,
            section_offset,
            &page_form.template_path,
        )
        .expect("section.id");
        check_eq!("Entity<'cms_section'>", resolved.binding.type_);
        check!(resolved.member_found);
        check_eq!("string", resolved.member.type_);
        let source = r#"<div :title="page.sections?.first()?.name"></div>"#;
        let call_document = template_document(&page_form.template_path, source, 2);
        let labels = real_world_completion_labels(&completions(
            &index,
            &real_world_completion_request(
                &call_document,
                (offset_of(source, "first()?.") + "first()?.".len()) as u32,
            ),
        ));
        for member in ["id", "name", "position", "blocks"] {
            check_contains!(labels, member.to_owned());
        }
        check_not_contains!(labels, "total".to_owned());
        let name_offset = offset_of(source, "name\"") + 1;
        hover_has(
            root,
            &index,
            &call_document,
            name_offset,
            "**property** `page.sections.first().name`: `string`",
        );
        if let Some(location) = single_definition(&index, &call_document, name_offset) {
            check_str!(location.uri, "entity-schema-definition.d.ts");
        }
        typo_suggests(
            &index,
            &template_document(
                &page_form.template_path,
                r#"<div :title="page.sections?.first()?.naem"></div>"#,
                3,
            ),
            "name",
        );
        check_no_unknown_vue_member(&index, &page_document, "CMS entity");
    }

    fn hover_has(
        root: &str,
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
        needle: &str,
    ) {
        match hover(
            root,
            index,
            &real_world_hover_request(document, offset as u32),
        ) {
            Some(value) => check_str!(value.contents.value, needle),
            None => record_failure(format!("hover is nil, expected {needle:?}")),
        }
    }

    fn single_definition(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
    ) -> Option<protocol::Location> {
        let locations = definitions(
            index,
            &real_world_definition_request_at(document, offset as u32),
        );
        if locations.len() != 1 {
            record_failure(format!(
                "expected one definition at {offset} in {}, got {locations:?}",
                document.uri
            ));
            return None;
        }
        locations.into_iter().next()
    }

    fn typo_suggests(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        suggestion: &str,
    ) {
        let problems = analyze(index, document);
        if problems.len() != 1 {
            record_failure(format!(
                "expected one problem in {:?}, got {:?}",
                document.source(),
                problems
                    .iter()
                    .map(|p| p.id.to_string())
                    .collect::<Vec<_>>()
            ));
            return;
        }
        check_eq!(
            "admin.component.unknown-vue-member",
            problems[0].id.to_string()
        );
        check_contains!(
            payload_strings(&problems[0], "suggestions"),
            suggestion.to_owned()
        );
    }

    fn labels_at(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
    ) -> Vec<String> {
        real_world_completion_labels(&completions(
            index,
            &real_world_completion_request(document, offset as u32),
        ))
    }

    fn member_type(component: &VueComponent, name: &str) -> String {
        let member = component.template_member(name);
        check!(member.is_some(), "member {name} of {}", component.name);
        member.unwrap_or_default().type_
    }

    pub(super) fn media(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let media = effective(&index, "sw-sidebar-media-item");
        check_eq!(
            "Repository<'media'>",
            member_type(&media, "mediaRepository")
        );
        check_eq!("number", member_type(&media, "itemsLoaded"));
        check_eq!(
            "EntityCollection<'media'>",
            member_type(&media, "mediaItems")
        );
        let item_source = r#"<div v-for="mediaItem in mediaItems" :title="mediaItem.fileName" :data-value="mediaItem."></div>"#;
        let item_document = template_document(&media.template_path, item_source, 4);
        let labels = labels_at(
            &index,
            &item_document,
            last_offset_of(item_source, "mediaItem.") + "mediaItem.".len(),
        );
        for member in ["id", "fileName", "mimeType", "url"] {
            check_contains!(labels, member.to_owned());
        }
        let filtered_source = r#"<div v-for="filteredMedia in mediaItems.filter((item) => item.fileName)" :title="filteredMedia.fileName" :data-value="filteredMedia."></div>"#;
        let filtered_document = template_document(&media.template_path, filtered_source, 5);
        let labels = labels_at(
            &index,
            &filtered_document,
            last_offset_of(filtered_source, "filteredMedia.") + "filteredMedia.".len(),
        );
        for member in ["id", "fileName", "mimeType", "url"] {
            check_contains!(labels, member.to_owned());
        }
        hover_has(
            root,
            &index,
            &filtered_document,
            offset_of(filtered_source, "filteredMedia.fileName") + "filteredMedia.fileNa".len(),
            "**property** `filteredMedia.fileName`: `string`",
        );
        typo_suggests(
            &index,
            &template_document(
                &media.template_path,
                r#"<div v-for="filteredMedia in mediaItems.filter((item) => item.fileName)">{{ filteredMedia.fileNmae }}</div>"#,
                6,
            ),
            "fileName",
        );
        let mapped_source = r#"<div v-for="mediaName in mediaItems?.map((item) => item.fileName) ?? []" :title="mediaName.length" :data-value="mediaName."></div>"#;
        let mapped_document = template_document(&media.template_path, mapped_source, 7);
        let labels = labels_at(
            &index,
            &mapped_document,
            last_offset_of(mapped_source, "mediaName.") + "mediaName.".len(),
        );
        for member in ["length", "toLowerCase", "trim"] {
            check_contains!(labels, member.to_owned());
        }
        hover_has(
            root,
            &index,
            &mapped_document,
            offset_of(mapped_source, "mediaName.length") + "mediaName.len".len(),
            "**property** `mediaName.length`: `number`",
        );
        typo_suggests(
            &index,
            &template_document(
                &media.template_path,
                r#"<div v-for="mediaCard in mediaItems?.map((item) => ({ label: item.fileName })) ?? []">{{ mediaCard.lable }}</div>"#,
                8,
            ),
            "label",
        );
        let file_name = offset_of(item_source, "fileName") + 2;
        hover_has(
            root,
            &index,
            &item_document,
            file_name,
            "**property** `mediaItem.fileName`: `string`",
        );
        if let Some(location) = single_definition(&index, &item_document, file_name) {
            check_str!(location.uri, "entity-schema-definition.d.ts");
        }
        typo_suggests(
            &index,
            &template_document(
                &media.template_path,
                r#"<div v-for="mediaItem in mediaItems">{{ mediaItem.fileNmae }}</div>"#,
                5,
            ),
            "fileName",
        );
        let repository_source = r#"<div :title="mediaRepository.create().fileName"></div>"#;
        let repository_document = template_document(&media.template_path, repository_source, 4);
        let labels = labels_at(
            &index,
            &repository_document,
            offset_of(repository_source, "create().") + "create().".len(),
        );
        for member in ["id", "fileName", "mimeType", "url"] {
            check_contains!(labels, member.to_owned());
        }
        let file_name = offset_of(repository_source, "fileName") + 2;
        hover_has(
            root,
            &index,
            &repository_document,
            file_name,
            "**property** `mediaRepository.create().fileName`: `string`",
        );
        if let Some(location) = single_definition(&index, &repository_document, file_name) {
            check_str!(location.uri, "entity-schema-definition.d.ts");
        }
        typo_suggests(
            &index,
            &template_document(
                &media.template_path,
                r#"<div :title="mediaRepository.create().fileNmae"></div>"#,
                5,
            ),
            "fileName",
        );

        let newsletter = effective(&index, "sw-newsletter-recipient-list");
        check_eq!(
            "EntityCollection<'newsletter_recipient'> | null",
            member_type(&newsletter, "items")
        );
        let source = r#"<div v-for="recipient in items" :title="recipient.email" :data-value="recipient."></div>"#;
        let document = template_document(&newsletter.template_path, source, 6);
        let labels = labels_at(
            &index,
            &document,
            last_offset_of(source, "recipient.") + "recipient.".len(),
        );
        for member in ["id", "email", "status", "salesChannel"] {
            check_contains!(labels, member.to_owned());
        }
        let email = offset_of(source, "email") + 2;
        hover_has(
            root,
            &index,
            &document,
            email,
            "**property** `recipient.email`: `string`",
        );
        if let Some(location) = single_definition(&index, &document, email) {
            check_str!(location.uri, "entity-schema-definition.d.ts");
        }
        typo_suggests(
            &index,
            &template_document(
                &newsletter.template_path,
                r#"<div v-for="recipient in items">{{ recipient.emial }}</div>"#,
                7,
            ),
            "email",
        );

        let groups = effective(&index, "sw-settings-customer-group-list");
        check_eq!(
            "EntityCollection<'customer_group'> | null",
            member_type(&groups, "customerGroups")
        );
        let source = r#"<div v-for="group in customerGroups" :title="group.name" :data-value="group."></div>"#;
        let document = template_document(&groups.template_path, source, 8);
        let labels = labels_at(
            &index,
            &document,
            last_offset_of(source, "group.") + "group.".len(),
        );
        for member in ["id", "name", "displayGross", "customers", "salesChannels"] {
            check_contains!(labels, member.to_owned());
        }
        hover_has(
            root,
            &index,
            &document,
            offset_of(source, "group.name") + "group.na".len(),
            "**property** `group.name`: `string`",
        );
        typo_suggests(
            &index,
            &template_document(
                &groups.template_path,
                r#"<div v-for="group in customerGroups">{{ group.naem }}</div>"#,
                9,
            ),
            "name",
        );

        let landing = effective(&index, "sw-landing-page-view");
        check_eq!(
            "Entity<'landing_page'> | null",
            member_type(&landing, "landingPage")
        );
        check_eq!(
            "null | Entity<'cms_page'>",
            member_type(&landing, "cmsPage")
        );
        let inherit = effective(&index, "sw-inherit-wrapper");
        let content = require_admin_slot(&inherit.slots, "content");
        check!(content.members_complete);
        let current_value = require_admin_slot_member(&content.members, "currentValue");
        for member in [
            "isInherited",
            "isInheritField",
            "updateCurrentValue",
            "restoreInheritance",
            "removeInheritance",
        ] {
            require_admin_slot_member(&content.members, member);
        }
        let seo_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-settings-seo/component/sw-seo-url/sw-seo-url.html.twig"
        );
        let seo_source = read(&seo_path);
        let seo_document = template_document(&seo_path, &seo_source, 1);
        let props = offset_of(&seo_source, "props.currentValue");
        let labels = labels_at(&index, &seo_document, props + "props.".len());
        for label in ["currentValue", "isInherited", "updateCurrentValue"] {
            check_contains!(labels, label.to_owned());
        }
        let current_offset = props + "props.cu".len();
        hover_has(
            root,
            &index,
            &seo_document,
            current_offset,
            "**slot prop** `props.currentValue`",
        );
        if let Some(location) = single_definition(&index, &seo_document, current_offset) {
            check_eq!(file_uri(&current_value.file_path), location.uri);
        }
        for problem in analyze(&index, &seo_document) {
            check!(
                problem.id.to_string() != "admin.component.unknown-slot-prop",
                "{}",
                problem.message
            );
        }
    }

    pub(super) fn slots_and_events(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let grid = effective(&index, "sw-data-grid");
        let row_click = require_admin_event(&grid.component_events(), "row-click");
        let family = grid
            .component_slot("column-name")
            .cloned()
            .expect("column-name slot");
        let meteor_button = effective(&index, "mt-button");
        let icon_front = require_admin_slot(&meteor_button.slots, "iconFront");
        let icon_size = require_admin_slot_member(&icon_front.members, "size");
        let event_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/event-real-world.html.twig"
        ));
        let source = "<sw-data-grid @ />";
        let document = new_document(event_uri.clone(), source, 1);
        check_contains!(
            labels_at(&index, &document, offset_of(source, "@") + 1),
            "@row-click".to_owned()
        );
        let source = r#"<sw-data-grid @row-click="onRowClick" />"#;
        let document = new_document(event_uri.clone(), source, 2);
        let offset = offset_of(source, "row-click") + 1;
        if let Some(location) = single_definition(&index, &document, offset) {
            check_eq!(file_uri(&row_click.file_path), location.uri);
            check_eq!(row_click.name_range.start_line, location.range.start.line);
            check_eq!(
                row_click.name_range.start_character,
                location.range.start.character
            );
            check_eq!(
                row_click.name_range.end_character,
                location.range.end.character
            );
        }
        hover_has(root, &index, &document, offset, "**event** `row-click`");
        let source = "<mt-button><template #iconFront></template></mt-button>";
        let document = new_document(event_uri.clone(), source, 3);
        let offset = offset_of(source, "iconFront") + 1;
        if let Some(location) = single_definition(&index, &document, offset) {
            check_eq!(file_uri(&icon_front.file_path), location.uri);
        }
        hover_has(root, &index, &document, offset, "**slot** `iconFront`");
        hover_has(root, &index, &document, offset, "`size`: `number`");
        let source =
            r#"<mt-button><template #iconFront="{ size }">{{ size }}</template></mt-button>"#;
        let document = new_document(event_uri.clone(), source, 4);
        let items = completions(
            &index,
            &real_world_completion_request(
                &document,
                (offset_of(source, "{ size") + "{ si".len()) as u32,
            ),
        );
        check_contains!(real_world_completion_labels(&items), "size".to_owned());
        let detail = items
            .iter()
            .find(|item| item.label == "size")
            .map(|item| item.detail.clone())
            .unwrap_or_default();
        check_str!(detail, "number");
        let value_offset = last_offset_of(source, "size") + 1;
        hover_has(
            root,
            &index,
            &document,
            value_offset,
            "**slot prop** `size`: `number`",
        );
        if let Some(location) = single_definition(&index, &document, value_offset) {
            check_eq!(file_uri(&icon_size.file_path), location.uri);
            check_eq!(icon_size.line - 1, location.range.start.line);
        }
        let source = r#"<sw-data-grid><template #column-name="{ item: row, isInlineEdit }">{{ row }}</template></sw-data-grid>"#;
        let document = new_document(event_uri.clone(), source, 5);
        let name_offset = offset_of(source, "column-name") + 1;
        if let Some(location) = single_definition(&index, &document, name_offset) {
            check_eq!(file_uri(&family.file_path), location.uri);
            check_eq!(family.name_range.start_line, location.range.start.line);
            check_eq!(
                family.name_range.start_character,
                location.range.start.character
            );
            check_eq!(family.name_range.end_line, location.range.end.line);
            check_eq!(
                family.name_range.end_character,
                location.range.end.character
            );
        }
        let labels = labels_at(
            &index,
            &document,
            offset_of(source, "{ item") + "{ it".len(),
        );
        for member in [
            "item",
            "itemIndex",
            "column",
            "columnIndex",
            "compact",
            "isInlineEdit",
            "selectItem",
        ] {
            check_contains!(labels, member.to_owned());
        }
        let value_offset = last_offset_of(source, "row") + 1;
        hover_has(
            root,
            &index,
            &document,
            value_offset,
            "Dynamic slot family: `column-*`",
        );
        let item_member = require_admin_slot_member(&family.members, "item");
        check!(item_member.name_range.declaration);
        check_gt!(
            item_member.name_range.end_character,
            item_member.name_range.start_character
        );
        if let Some(location) = single_definition(&index, &document, value_offset) {
            check_eq!(file_uri(&item_member.file_path), location.uri);
            check_eq!(item_member.name_range.start_line, location.range.start.line);
            check_eq!(
                item_member.name_range.start_character,
                location.range.start.character
            );
            check_eq!(item_member.name_range.end_line, location.range.end.line);
            check_eq!(
                item_member.name_range.end_character,
                location.range.end.character
            );
        }
        let list_item = effective(&index, "sw-cms-list-item");
        let item_click = require_admin_event(&list_item.component_events(), "item-click");
        let cms_list_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-cms/page/sw-cms-list/sw-cms-list.html.twig"
        );
        let cms_list_source = read(&cms_list_path);
        let cms_list_document = template_document(&cms_list_path, &cms_list_source, 1);
        let click_offset = (offset_of(&cms_list_source, "@item-click") + 2) as u32;
        let refs = references(
            &index,
            &real_world_reference_request(&cms_list_document, click_offset, true),
        );
        check_ge!(refs.len(), 4);
        require_location_uri(&refs, &item_click.file_path);
        require_location_uri(&refs, &cms_list_path);
        let renamer = swlsp_lsp_refactor::AdminRenameProvider::new(Some(index.clone()));
        let rename = renamer
            .rename(
                &ctx(),
                &real_world_rename_request(&cms_list_document, click_offset, "item-selected"),
            )
            .expect("rename")
            .expect("rename edit");
        check!(
            rename
                .changes
                .contains_key(&file_uri(&item_click.file_path))
        );
        check!(rename.changes.contains_key(&file_uri(&cms_list_path)));
        let mut count = 0;
        for (uri, edits) in &rename.changes {
            count += edits.len();
            for edit in edits {
                check_eq!("item-selected", edit.new_text);
            }
            check!(!slash(uri).contains("/app/component/sidebar/"), "{uri}");
        }
        check_ge!(count, 4);
        let login_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-login/view/sw-login-login/sw-login-login.html.twig"
        );
        let login_source = read(&login_path);
        let login_document = template_document(&login_path, &login_source, 1);
        let slot_offset = (offset_of(&login_source, "#iconFront") + 2) as u32;
        let refs = references(
            &index,
            &real_world_reference_request(&login_document, slot_offset, true),
        );
        check_gt!(refs.len(), 5);
        require_location_uri(&refs, &icon_front.file_path);
        require_location_uri(&refs, &login_path);
        let error = renamer
            .rename(
                &ctx(),
                &real_world_rename_request(&login_document, slot_offset, "leadingIcon"),
            )
            .expect_err("renaming an external Meteor slot must fail");
        check_str!(error.to_string(), "external Meteor");
        let definition_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-newsletter-recipient/component/sw-newsletter-recipient-filter-switch/index.js"
        );
        let problems = analyze(
            &index,
            &template_document(&definition_path, &read(&definition_path), 1),
        );
        check_empty!(
            problems
                .iter()
                .map(|p| format!("{}: {}", p.id, p.message))
                .collect::<Vec<_>>()
        );
        let source = "export default { methods: { probe() { return this.; } } };";
        let document = template_document(&definition_path, source, 1);
        let offset = (offset_of(source, "this.") + "this.".len()) as u32;
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = document.uri.clone();
        let mut context = syntax(&document, node_at(&document, offset - 1), Some(offset - 1));
        context.language = None;
        let labels = real_world_completion_labels(&completions(
            &index,
            &CompletionRequest {
                params,
                syntax: context,
            },
        ));
        for label in ["id", "group", "onChange", "$emit"] {
            check_contains!(labels, label.to_owned());
        }
    }

    fn shape_members(shape: &VueTypeShape) -> BTreeMap<String, TwigVueMember> {
        shape
            .members
            .iter()
            .map(|member| (member.name.clone(), member.clone()))
            .collect()
    }

    pub(super) fn registries(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let index = workspace_admin_index(&f.workspace);
        let filters = index.get_all_filters().expect("filters");
        check_ge!(filters.len(), 12);
        let asset = index.get_filter("asset").expect("filter");
        assert!(!asset.is_empty());
        check_str!(
            slash(&asset[0].file_path),
            "/administration/src/app/filter/asset.filter.ts"
        );
        let services = index.get_all_services().expect("services");
        check_gt!(services.len(), 25);
        let acl = index.get_service("acl").expect("service");
        assert!(!acl.is_empty());
        check_str!(slash(&acl[0].file_path), "/administration/src/app/main.ts");
        check!(
            slash(&acl[0].implementation_path)
                .ends_with("/administration/src/app/service/acl.service.ts")
        );
        check_gt!(index.get_all_stores().expect("stores").len(), 10);
        let profile = index.get_store("swProfile").expect("store");
        assert!(!profile.is_empty());
        check!(profile[0].member("setMinSearchTermLength").is_some());
        let session = index.get_store("session").expect("store");
        assert!(!session.is_empty());
        for member in [
            "currentUser",
            "userPending",
            "setAdminLocale",
            "setCurrentUser",
        ] {
            check!(
                session[0].member(member).is_some(),
                "expected setup-store member {member}"
            );
        }
        let context_store = index.get_store("context").expect("store");
        assert!(!context_store.is_empty());
        for member in ["app", "api", "setApiLanguageId", "resetLanguageToDefault"] {
            check!(
                context_store[0].member(member).is_some(),
                "expected spread setup-store member {member}"
            );
        }
        check_gt!(index.get_all_privileges().expect("privileges").len(), 100);
        let viewer = index.get_privilege("product.viewer").expect("privilege");
        assert!(!viewer.is_empty());
        check_eq!(AdminPrivilegeKind::Role, viewer[0].kind);
        let read_privilege = index.get_privilege("product:read").expect("privilege");
        assert!(!read_privilege.is_empty());
        check_eq!(AdminPrivilegeKind::Permission, read_privilege[0].kind);
        let (product_module, detail) = index
            .get_module_route("sw.product.detail")
            .expect("route")
            .expect("sw.product.detail");
        check_eq!("sw-product", product_module.name);
        check_eq!("sw-product-detail", detail.component);
        let (order_module, general) = index
            .get_module_route("sw.order.detail.general")
            .expect("route")
            .expect("sw.order.detail.general");
        check_eq!("sw-order", order_module.name);
        check_eq!("sw-order-detail-general", general.component);
        for (route_name, component) in [
            ("sw.settings.security.index", "sw-settings-security-view"),
            ("sw.profile.index.mfa", "frosh-profile-mfa"),
            (
                "sw.sales.channel.detail.theme",
                "sw-sales-channel-detail-theme",
            ),
        ] {
            match index.get_module_route(route_name).expect("route") {
                Some((_, route)) => check_eq!(component, route.component, "{route_name}"),
                None => record_failure(format!("module route {route_name} not found")),
            }
        }
        check_gt!(index.get_all_modules().expect("modules").len(), 40);
        let global_types =
            format!("{root}/src/Administration/Resources/app/administration/src/global.types.ts");
        let factory = shape_members(
            &index
                .resolve_application_container("factory", &global_types)
                .expect("container"),
        );
        for name in [
            "$list",
            "apiService",
            "entityDefinition",
            "locale",
            "module",
            "workerNotification",
        ] {
            check!(
                factory.contains_key(name),
                "real FactoryContainer must expose {name}"
            );
        }
        check_eq!(
            "typeof LocaleFactory",
            factory
                .get("locale")
                .map(|m| m.type_.clone())
                .unwrap_or_default()
        );
        let service = shape_members(
            &index
                .resolve_application_container("service", &global_types)
                .expect("container"),
        );
        for name in [
            "acl",
            "repositoryFactory",
            "languageAutoFetchingService",
            "extensionSdkService",
        ] {
            check!(
                service.contains_key(name),
                "merged real ServiceContainer must expose {name}"
            );
        }
        let container_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/application-container-real-world.ts"
        );
        let source = "Application.getContainer('factory').";
        let document = template_document(&container_path, source, 1);
        let labels = labels_at(&index, &document, source.len() - 1);
        for name in ["locale", "module", "entityDefinition"] {
            check_contains!(labels, name.to_owned());
        }
        let source = "function resolveService() {\n    const services = Shopware.Application.getContainer('service');\n    return services.;\n}";
        let document = template_document(&container_path, source, 2);
        let labels = labels_at(&index, &document, last_offset_of(source, ".;"));
        for name in ["acl", "repositoryFactory", "languageAutoFetchingService"] {
            check_contains!(labels, name.to_owned());
        }
        let source = "Application.getContainer('factory').locale";
        let document = template_document(&container_path, source, 3);
        if let Some(location) =
            single_definition(&index, &document, last_offset_of(source, "locale") + 1)
        {
            check_eq!(file_uri(&global_types), location.uri);
            check_gt!(location.range.start.character, 0);
        }
        let usages = index
            .get_usages(AdminSymbolKind::Service, "", "languageAutoFetchingService")
            .expect("usages");
        let language_init = swlsp_uriutil::filepath::clean(&format!(
            "{root}/src/Administration/Resources/app/administration/src/app/init-post/language.init.ts"
        ));
        check!(
            usages
                .iter()
                .any(
                    |usage| swlsp_uriutil::filepath::clean(&usage.file_path) == language_init
                        && !usage.occurrences.is_empty()
                ),
            "real getContainer('service') member must be indexed as a service reference"
        );
        super::admin_utils::run(f, &index, &container_path);
    }
}

mod admin_utils {
    use std::collections::BTreeMap;
    use std::sync::Arc;

    use swlsp_admin::*;
    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;

    use super::admin::{analyze, completions, definitions, effective, hover, template_document};
    use super::*;

    fn shape_members(shape: &VueTypeShape) -> BTreeMap<String, TwigVueMember> {
        shape
            .members
            .iter()
            .map(|member| (member.name.clone(), member.clone()))
            .collect()
    }

    fn labels_at(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
    ) -> Vec<String> {
        real_world_completion_labels(&completions(
            index,
            &real_world_completion_request(document, offset as u32),
        ))
    }

    fn signature(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
    ) -> protocol::SignatureHelp {
        swlsp_lsp_signature::AdminSignatureProvider::new(index.clone())
            .get_signature_help(
                &ctx(),
                &real_world_signature_request(document, offset as u32),
            )
            .expect("signature")
            .expect("signature help")
    }

    fn one_definition(
        index: &Arc<AdminComponentIndexer>,
        document: &Arc<TextDocument>,
        offset: usize,
    ) -> protocol::Location {
        let locations = definitions(
            index,
            &real_world_definition_request_at(document, offset as u32),
        );
        require_len!(locations, 1);
        locations.into_iter().next().unwrap()
    }

    pub(super) fn run(
        f: &RealWorldWorkspaceFixture,
        index: &Arc<AdminComponentIndexer>,
        container_path: &str,
    ) {
        let root = f.root.as_str();
        let context_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/composables/use-context.ts"
        );
        let api = index
            .resolve_shopware_context("api", container_path)
            .expect("context");
        check!(api.complete);
        let members = shape_members(&api);
        for name in ["languageId", "versionId", "currencyId", "systemLanguageId"] {
            check!(members.contains_key(name), "{name}");
        }
        check_eq!(
            "null | string",
            members
                .get("languageId")
                .map(|m| m.type_.clone())
                .unwrap_or_default()
        );
        let source = "Shopware.Context.api.";
        let document = template_document(container_path, source, 4);
        let labels = labels_at(index, &document, source.len() - 1);
        for name in ["languageId", "versionId", "currencyId"] {
            check_contains!(labels, name.to_owned());
        }
        let source = "Shopware.Context.api.languageId";
        let document = template_document(container_path, source, 5);
        let location = one_definition(index, &document, last_offset_of(source, "languageId") + 1);
        check_eq!(file_uri(&context_path), location.uri);
        check_gt!(location.range.start.character, 0);
        let utils_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/core/service/util.service.ts"
        );
        let utils = index
            .resolve_shopware_utils("", container_path)
            .expect("utils");
        check!(utils.complete);
        let members = shape_members(&utils);
        for name in [
            "EventBus", "array", "createId", "debug", "debounce", "format", "object", "string",
            "types",
        ] {
            check!(members.contains_key(name), "{name}");
        }
        check_eq!(
            "() => string",
            members
                .get("createId")
                .map(|m| m.type_.clone())
                .unwrap_or_default()
        );
        let format = index
            .resolve_shopware_utils("format", container_path)
            .expect("utils");
        check!(format.complete);
        let members = shape_members(&format);
        for name in [
            "currency",
            "date",
            "dateWithUserTimezone",
            "fileSize",
            "toISODate",
        ] {
            check!(members.contains_key(name), "{name}");
        }
        check_str!(
            members
                .get("date")
                .map(|m| m.type_.clone())
                .unwrap_or_default(),
            "=> string"
        );
        let bus = index
            .resolve_shopware_utils("EventBus", container_path)
            .expect("utils");
        for name in ["all", "emit", "off", "on"] {
            check_contains!(real_world_admin_member_names(&bus.members), name.to_owned());
        }
        let bus_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/core/service/utils/eventBus.utils.ts"
        );
        let events = index
            .resolve_shopware_event_bus_events(container_path)
            .expect("events");
        check!(!events.complete);
        for name in [
            "consent",
            "sw-media-library-item-updated",
            "sw-product-detail-save-finish",
            "telemetry",
        ] {
            check_contains!(
                real_world_admin_member_names(&events.members),
                name.to_owned()
            );
        }
        let source = "const { EventBus } = Shopware.Utils;\nEventBus.on('sw-media', handler)";
        let document = template_document(container_path, source, 6);
        check_contains!(
            labels_at(
                index,
                &document,
                last_offset_of(source, "sw-media") + "sw-media".len()
            ),
            "sw-media-library-item-updated".to_owned()
        );
        let source = "const bus = Shopware.Utils.EventBus;\nbus.emit('sw-media-library-item-updated', mediaId)";
        let bus_document = template_document(container_path, source, 7);
        let bus_offset = last_offset_of(source, "sw-media-library-item-updated") + 1;
        let location = one_definition(index, &bus_document, bus_offset);
        check_eq!(file_uri(&bus_path), location.uri);
        check_gt!(location.range.start.character, 0);
        let refs = swlsp_lsp_reference::AdminReferenceProvider::new(Some(index.clone()))
            .get_references(
                &ctx(),
                &real_world_reference_request(&bus_document, bus_offset as u32, true),
            )
            .expect("references");
        check_gt!(refs.len(), 5);
        check!(
            refs.iter()
                .any(|location| location.uri == file_uri(&bus_path))
        );
        let rename_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/media/sw-model-editor/index.ts"
        );
        let rename_source = read(&rename_path);
        let rename_document = template_document(&rename_path, &rename_source, 1);
        let rename_offset = (offset_of(&rename_source, "sw-media-library-item-updated") + 2) as u32;
        let rename = swlsp_lsp_refactor::AdminRenameProvider::new(Some(index.clone()))
            .rename(
                &ctx(),
                &real_world_rename_request(
                    &rename_document,
                    rename_offset,
                    "sw-media-library-item-refreshed",
                ),
            )
            .expect("rename")
            .expect("rename edit");
        check_gt!(admin_workspace_edit_count(Some(&rename)), 5);
        let renamed = apply_real_world_text_edits(
            &read(&bus_path),
            rename
                .changes
                .get(&file_uri(&bus_path))
                .map(Vec::as_slice)
                .unwrap_or_default(),
        );
        check_str!(renamed, "'sw-media-library-item-refreshed': string");
        let renamed = apply_real_world_text_edits(
            &rename_source,
            rename
                .changes
                .get(&file_uri(&rename_path))
                .map(Vec::as_slice)
                .unwrap_or_default(),
        );
        check_str!(renamed, "EventBus.on('sw-media-library-item-refreshed'");
        let help = signature(index, &bus_document, last_offset_of(source, "mediaId") + 1);
        require_len!(help.signatures, 1);
        check_eq!(
            r#"emit(event: "sw-media-library-item-updated", payload: string): void"#,
            help.signatures[0].label
        );
        check_eq!(1, help.active_parameter);
        let diagnostic_source = "\nShopware.Utils.EventBus.emit('sw-media-library-item-updated', 42);\nShopware.Utils.EventBus.emit('extension-owned-event', 42);\n";
        let problems = analyze(
            index,
            &template_document(container_path, diagnostic_source, 8),
        );
        check_eq!(1, problems.len(), "event bus payload problems {problems:?}");
        if let Some(problem) = problems.first() {
            check_eq!("admin.event-bus.payload-type", problem.id.to_string());
            let start: usize = problem.range.start().into();
            let end: usize = problem.range.end().into();
            check_eq!("42", &diagnostic_source[start..end]);
        }
        let source = "const format = Shopware.Utils.format;\nformat.";
        let document = template_document(container_path, source, 6);
        let labels = labels_at(index, &document, source.len() - 1);
        for name in ["currency", "date", "fileSize"] {
            check_contains!(labels, name.to_owned());
        }
        let source =
            "const { date: formatDate } = Shopware.Utils.format;\nformatDate('2026-01-01')";
        let document = template_document(container_path, source, 7);
        let location = one_definition(index, &document, last_offset_of(source, "formatDate") + 1);
        check_eq!(file_uri(&utils_path), location.uri);
        check_gt!(location.range.start.character, 0);
        let help = signature(
            index,
            &document,
            last_offset_of(source, "2026") + "2026".len(),
        );
        require_len!(help.signatures, 1);
        check_str!(help.signatures[0].label, "date(val: string");
        check_str!(help.signatures[0].label, "): string");

        let registry_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/registry-real-world.js"
        ));
        let source = "Module.getModuleRegistry().get('sw-pro')";
        let document = new_document(registry_uri.clone(), source, 1);
        check_contains!(
            labels_at(
                index,
                &document,
                offset_of(source, "sw-pro") + "sw-pro".len()
            ),
            "sw-product".to_owned()
        );
        let source = "Module.getModuleRegistry().get('sw-product')";
        let document = new_document(registry_uri.clone(), source, 2);
        let offset = offset_of(source, "sw-product") + 2;
        let locations = definitions(
            index,
            &real_world_definition_request_at(&document, offset as u32),
        );
        assert!(!locations.is_empty());
        let (product_module, _) = index
            .get_module_route("sw.product.detail")
            .expect("route")
            .expect("route");
        check_eq!(file_uri(&product_module.file_path), locations[0].uri);
        let registry_hover = hover(
            root,
            index,
            &real_world_hover_request(&document, offset as u32),
        )
        .expect("registry hover");
        check_str!(registry_hover.contents.value, "Administration module");
        check_str!(registry_hover.contents.value, "`sw-product`");
        let source = "Shopware.Component.getComponentRegistry().has('sw-product-list')";
        let document = new_document(registry_uri.clone(), source, 3);
        check!(
            !definitions(
                index,
                &real_world_definition_request_at(
                    &document,
                    (offset_of(source, "sw-product-list") + 2) as u32
                )
            )
            .is_empty()
        );
        let spec_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-settings-message-stats/index.spec.js"
        );
        for problem in analyze(index, &template_document(&spec_path, &read(&spec_path), 1)) {
            check!(
                problem.id.to_string() != "admin.component.registry-not-found",
                "{}",
                problem.message
            );
            check!(
                problem.id.to_string() != "admin.module.not-found",
                "{}",
                problem.message
            );
        }
        let privilege_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/privilege-real-world.html.twig"
        ));
        let source = r#"<mt-button :disabled="acl.can('product.v')" />"#;
        let document = new_document(privilege_uri.clone(), source, 1);
        check_contains!(
            labels_at(
                index,
                &document,
                offset_of(source, "product.v") + "product.v".len()
            ),
            "product.viewer".to_owned()
        );
        let source = r#"<mt-button :disabled="acl.can('product.viewer')" />"#;
        let document = new_document(privilege_uri.clone(), source, 2);
        let locations = definitions(
            index,
            &real_world_definition_request_at(
                &document,
                (offset_of(source, "product.viewer") + 2) as u32,
            ),
        );
        assert!(!locations.is_empty());
        let viewer = index.get_privilege("product.viewer").expect("privilege");
        check_eq!(file_uri(&viewer[0].file_path), locations[0].uri);
        let properties_path = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-product/component/sw-product-properties/sw-product-properties.html.twig"
        );
        for problem in analyze(
            index,
            &template_document(&properties_path, &read(&properties_path), 1),
        ) {
            check!(
                problem.id.to_string() != "admin.privilege.not-found",
                "{}",
                problem.message
            );
        }
        let symbols = swlsp_lsp_symbol::AdminWorkspaceSymbolProvider::new(Some(index.clone()));
        for (name, container) in [
            ("acl", "Administration service"),
            ("session", "Administration store"),
            ("setCurrentUser", "store · session"),
            ("product.viewer", "ACL role"),
        ] {
            let found = symbols.workspace_symbols(&ctx(), name).expect("symbols");
            require_workspace_symbol(&found, name, container);
        }
        for (query, name, container) in [
            ("deprecated", "deprecated", "sw-card · component prop"),
            ("#default", "default", "sw-card · component slot"),
        ] {
            let found = symbols.workspace_symbols(&ctx(), query).expect("symbols");
            require_workspace_symbol(&found, name, container);
        }
        let card_template = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/base/sw-card/sw-card.html.twig"
        );
        let card_definition = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/base/sw-card/index.ts"
        );
        let lenses = real_world_code_lenses(
            &swlsp_lsp_codelens::AdminComponentCodeLensProvider::new(Some(index.clone())),
            &card_template,
        );
        require_related_lens_file_targets(
            &lenses,
            "component definition",
            &file_uri(&card_definition),
            1,
        );
        let document_symbols =
            swlsp_lsp_symbol::AdminDocumentSymbolProvider::new(Some(index.clone()));
        let definition_symbols = real_world_document_symbols(&document_symbols, &card_definition);
        require_len!(definition_symbols, 1);
        check_eq!("sw-card", definition_symbols[0].name);
        require_document_symbol_named(&definition_symbols[0].children, "deprecated");
        require_document_symbol_named(&definition_symbols[0].children, "getSlots");
        let template_symbols = real_world_document_symbols(&document_symbols, &card_template);
        require_len!(template_symbols, 1);
        check_eq!("sw-card", template_symbols[0].name);
        require_document_symbol_named(&template_symbols[0].children, "sw_card");
        require_document_symbol_named_recursive(&template_symbols[0].children, "default");
        let selection = swlsp_lsp_selection::AdminSelectionRangeProvider::new();
        let texts = real_world_selection_texts(&selection, &card_definition, "getSlots");
        check_ge!(texts.len(), 5);
        check_eq!("getSlots", texts[0]);
        require_real_world_selection_containing(&texts, "return this.$slots");
        let texts = real_world_selection_texts(&selection, &card_template, "getSlots");
        check_ge!(texts.len(), 7);
        check_eq!("getSlots", texts[0]);
        require_real_world_selection_containing(&texts, r#"v-for="(index, name) in getSlots()""#);
        let folding = swlsp_lsp_folding::AdminFoldingProvider::new();
        let folds = real_world_folding_ranges(&folding, &card_definition);
        require_real_world_folding_range(&folds, 2, 10, protocol::FOLDING_RANGE_KIND_COMMENT);
        require_real_world_folding_range(&folds, 11, 26, "");
        require_real_world_folding_range(&folds, 14, 19, "");
        require_real_world_folding_range(&folds, 22, 25, "");
        require_real_world_folding_range(&folds, 23, 24, "");
        let folds = real_world_folding_ranges(&folding, &card_template);
        require_real_world_folding_range(&folds, 1, 17, "");
        require_real_world_folding_range(&folds, 2, 16, "");
        require_real_world_folding_range(&folds, 21, 35, "");
        let colors = swlsp_lsp_color::AdminScssColorProvider::new();
        let variables = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/assets/scss/variables.scss"
        );
        let (found, document) = real_world_document_colors(&colors, &variables);
        let gray = found
            .iter()
            .find(|color| real_world_range_text(&document, &color.range) == "#f9fafb")
            .copied()
            .expect("real Administration variables must expose #f9fafb");
        check!((gray.color.red - 249.0 / 255.0).abs() < 0.00001);
        check!((gray.color.green - 250.0 / 255.0).abs() < 0.00001);
        check!((gray.color.blue - 251.0 / 255.0).abs() < 0.00001);
        check_eq!(1.0, gray.color.alpha);
        let mut params = protocol::ColorPresentationParams::default();
        params.text_document.uri = document.uri.clone();
        params.color = gray.color;
        params.range = gray.range;
        let presentations = colors
            .get_color_presentations(
                &ctx(),
                &ColorPresentationRequest {
                    params,
                    document: document.clone(),
                },
            )
            .expect("presentations");
        require_len!(presentations, 3);
        check_eq!("#f9fafb", presentations[0].label);
        check_eq!("rgb(249, 250, 251)", presentations[1].label);
        let mixins = format!(
            "{root}/src/Administration/Resources/app/administration/src/app/assets/scss/mixins.scss"
        );
        let (found, document) = real_world_document_colors(&colors, &mixins);
        let shadow = found
            .iter()
            .find(|color| real_world_range_text(&document, &color.range) == "rgba(0, 0, 0, 8%)")
            .copied()
            .expect("real Administration mixins must expose rgba percentage alpha");
        check!((shadow.color.alpha - 0.08).abs() < 0.00001);
        let highlights = real_world_document_highlights(
            &swlsp_lsp_highlight::AdminDocumentHighlightProvider::new(Some(index.clone())),
            &card_template,
            "getSlots",
        );
        require_len!(highlights, 2);
        for item in &highlights {
            check_eq!(Some(protocol::DocumentHighlightKind::TEXT), item.kind);
        }
        let linked = real_world_linked_editing_ranges(
            &swlsp_lsp_linkedediting::AdminLinkedEditingProvider::new(),
            &card_template,
            "sw-card-deprecated",
        )
        .expect("linked editing ranges");
        require_len!(linked.ranges, 2);
        let calls = swlsp_lsp_callhierarchy::AdminCallHierarchyProvider::new(index.clone());
        let items = real_world_call_hierarchy_items(&calls, &card_definition, "getSlots");
        require_len!(items, 1);
        check_eq!("getSlots", items[0].name);
        let incoming = calls
            .incoming_calls(
                &ctx(),
                &CallHierarchyCallsRequest {
                    item: items[0].clone(),
                    documents: Vec::new(),
                },
            )
            .expect("incoming");
        let caller = incoming
            .iter()
            .find(|call| call.from.uri == file_uri(&card_template))
            .cloned()
            .expect("real sw-card template must call its indexed getSlots method");
        check_eq!("sw-card template", caller.from.name);
        require_len!(caller.from_ranges, 2);
        let outgoing = calls
            .outgoing_calls(
                &ctx(),
                &CallHierarchyCallsRequest {
                    item: caller.from.clone(),
                    documents: Vec::new(),
                },
            )
            .expect("outgoing");
        let get_slots = outgoing
            .iter()
            .find(|call| call.to.name == "getSlots" && call.to.uri == file_uri(&card_definition))
            .expect("getSlots outgoing call");
        require_len!(get_slots.from_ranges, 2);
        let count = |kind, name: &str| {
            let usages = index.get_usages(kind, "", name).expect("usages");
            (admin_usage_occurrence_count(&usages), usages)
        };
        let (sw_page, _) = count(AdminSymbolKind::Component, "sw-page");
        check_gt!(sw_page, 150);
        let (acl, _) = count(AdminSymbolKind::Service, "acl");
        check_gt!(acl, 20);
        let (session, _) = count(AdminSymbolKind::Store, "session");
        check_gt!(session, 15);
        let (viewer_count, viewer_usages) = count(AdminSymbolKind::Privilege, "product.viewer");
        check_gt!(viewer_count, 10);
        check!(
            viewer_usages
                .iter()
                .any(|usage| usage.file_path.ends_with(".twig"))
        );
        let (route_count, route_usages) = count(AdminSymbolKind::ModuleRoute, "sw.product.detail");
        check_ge!(route_count, 6);
        check!(
            route_usages
                .iter()
                .any(|usage| usage.file_path.ends_with(".twig"))
        );
        let (module_count, _) = count(AdminSymbolKind::Module, "sw-settings-message-stats");
        check_ge!(module_count, 3);
        eprintln!(
            "administration usages: sw-page={sw_page}, acl={acl}, session={session}, product.viewer={viewer_count}"
        );
    }
}

mod domains {
    use std::collections::{BTreeMap, BTreeSet, HashMap};
    use std::sync::Arc;

    use serde_json::Value;
    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;

    use super::*;

    pub(super) fn document_at(root: &str, relative_path: &str, version: i32) -> Arc<TextDocument> {
        let path = format!("{root}/{relative_path}");
        new_document(file_uri(&path), &read(&path), version)
    }

    pub(super) fn full_range_params(document: &TextDocument) -> protocol::InlayHintParams {
        let mut params = protocol::InlayHintParams::default();
        params.text_document.uri = document.uri.clone();
        params.range.end = position(document, document.source().len() as u32);
        params
    }

    pub(super) fn label_parts(
        hint: &protocol::InlayHint,
    ) -> Option<Vec<protocol::InlayHintLabelPart>> {
        match &hint.label {
            protocol::InlayHintLabel::Parts(parts) => Some(parts.clone()),
            protocol::InlayHintLabel::String(_) => None,
        }
    }

    pub(super) fn dal(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let dal = f.workspace.services().dal.clone();
        let definitions = dal.definitions().expect("definitions");
        assert!(!definitions.is_empty());
        let product = dal.definition("product").expect("definition");
        assert!(!product.is_empty());
        check!(!product[0].fields.is_empty());
        let entity_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/dal-entity-real-world.ts"
        ));
        let completion = |document: &Arc<TextDocument>, offset: usize| {
            real_world_completion_labels(
                &swlsp_lsp_completion::DalCompletionProvider::new(dal.clone()).get_completions(
                    &ctx(),
                    &real_world_completion_request(document, offset as u32),
                ),
            )
        };
        let source = "Shopware.EntityDefinition.get('prod')";
        let document = new_document(entity_uri.clone(), source, 1);
        check_contains!(
            completion(&document, offset_of(source, "prod") + 2),
            "product".to_owned()
        );
        let source = "Shopware.EntityDefinition.get('product')";
        let document = new_document(entity_uri.clone(), source, 2);
        let offset = (offset_of(source, "product") + 2) as u32;
        let locations = swlsp_lsp_definition::DalDefinitionProvider::new(Some(dal.clone()))
            .get_definition(&ctx(), &real_world_definition_request_at(&document, offset));
        assert!(!locations.is_empty());
        check_str!(slash(&locations[0].uri), "/ProductDefinition.php");
        let hover = swlsp_lsp_hover::DalHoverProvider::new(Some(dal.clone()))
            .get_hover(&ctx(), &real_world_hover_request(&document, offset))
            .expect("hover")
            .expect("DAL entity hover");
        check_str!(hover.contents.value, "Shopware DAL entity");
        check_str!(hover.contents.value, "ProductDefinition");
        let problems = swlsp_lsp_diagnostics::DalEntityAnalyzer::new(Some(dal.clone()))
            .analyze(
                &ctx(),
                &new_document(
                    entity_uri.clone(),
                    "Shopware.EntityDefinition.get('prodcut')",
                    3,
                ),
            )
            .expect("analyze");
        require_len!(problems, 1);
        check_eq!("shopware.dal.entity-not-found", problems[0].id.to_string());
        let symbols = swlsp_lsp_symbol::DalWorkspaceSymbolProvider::new(Some(dal.clone()))
            .workspace_symbols(&ctx(), "ProductDefinition")
            .expect("symbols");
        check!(symbols.iter().any(|symbol| symbol.name == "product"));
        let criteria_uri = file_uri(&format!(
            "{root}/src/Administration/Resources/app/administration/src/dal-real-world.js"
        ));
        let source = "Criteria.equals('manu', value)";
        let document = new_document(criteria_uri.clone(), source, 1);
        check_contains!(
            completion(&document, offset_of(source, "manu") + "manu".len()),
            "manufacturer".to_owned()
        );
        let source = "Criteria.equals('manufacturer.name', value)";
        let document = new_document(criteria_uri.clone(), source, 2);
        let offset = (offset_of(source, "manufacturer") + 2) as u32;
        let locations = swlsp_lsp_definition::DalDefinitionProvider::new(Some(dal.clone()))
            .get_definition(&ctx(), &real_world_definition_request_at(&document, offset));
        assert!(!locations.is_empty());
        check!(
            locations
                .iter()
                .any(|location| slash(&location.uri).contains("/ProductDefinition.php"))
        );
        let name_offset = (offset_of(source, ".name") + ".n".len()) as u32;
        let hover = swlsp_lsp_hover::DalHoverProvider::new(Some(dal.clone()))
            .get_hover(&ctx(), &real_world_hover_request(&document, name_offset))
            .expect("hover")
            .expect("DAL field hover");
        check_str!(hover.contents.value, "Shopware DAL field");
        check_str!(hover.contents.value, "StringField");
        let scripts = f.workspace.services().app_scripts.clone();
        check!(!scripts.hooks().expect("hooks").is_empty());
        check!(!scripts.facades().expect("facades").is_empty());
    }

    pub(super) fn php_and_twig(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let php = &f.php;
        let services = f.workspace.services();
        let semantic = swlsp_lsp_phpsemantic::Provider::new(Some(php.clone()));
        for relative_path in [
            "src/Core/Framework/Demodata/DemodataContext.php",
            "src/Core/Framework/Api/Controller/ApiController.php",
            "src/Core/Framework/Gateway/Context/Command/Handler/ChangeShippingLocationCommandHandler.php",
            "src/Core/DevOps/StaticAnalyze/PHPStan/Rules/NoUpdatesInExecuteQueryRule.php",
            "src/Core/Framework/Adapter/Kernel/KernelFactory.php",
            "src/Core/Content/Product/Cms/ManufacturerLogoCmsElementResolver.php",
        ] {
            let document = document_at(root, relative_path, 1);
            let diagnostics = semantic.analyze(&ctx(), &document).expect("analyze");
            check_empty!(
                diagnostics
                    .iter()
                    .map(|d| format!("{}: {}", d.id, d.message))
                    .collect::<Vec<_>>(),
                "{relative_path} must remain free of PHP semantic false positives"
            );
        }
        let variables = php
            .twig_template_variables(&[
                "@Storefront/storefront/page/content/product-detail.html.twig",
            ])
            .expect("variables");
        require_twig_variable(&variables, "page");
        require_twig_variable(&variables, "redirectTo");
        let labels = real_world_completion_labels(&real_world_twig_completions(
            root,
            &services.twig,
            php,
            "{% for entry in entries %}{{ loop. }}{% endfor %}",
            "loop.",
        ));
        check_contains!(labels, "index".to_owned());
        if php
            .find_class(swlsp_twig::TWIG_LOOP_CONTEXT_CLASS)
            .is_some()
        {
            check_contains!(labels, "previous".to_owned());
            check_contains!(labels, "depth0".to_owned());
        } else {
            check_contains!(labels, "revindex".to_owned());
            check_not_contains!(labels, "previous".to_owned());
        }
        let product_template = new_document(
            file_uri(&format!(
                "{root}/src/Storefront/Resources/views/storefront/page/content/product-detail.html.twig"
            )),
            "",
            1,
        );
        let hints = swlsp_lsp_inlay::TwigVariableProvider::new(Some(php.clone()))
            .get_inlay_hints(
                &ctx(),
                &InlayHintRequest {
                    params: protocol::InlayHintParams::default(),
                    document: product_template,
                },
            )
            .expect("inlay hints");
        require_len!(hints, 1);
        let parts = label_parts(&hints[0]).expect("label parts");
        require_len!(parts, 1);
        check_eq!(format!("Variables ({})", variables.len()), parts[0].value);
        check_eq!(
            Some(swlsp_lsp_inlay::BROWSE_TWIG_VARIABLES_COMMAND.to_owned()),
            parts[0]
                .command
                .as_ref()
                .map(|command| command.command.clone())
        );
        let administration = document_at(
            root,
            "src/Administration/Resources/views/administration/index.html.twig",
            1,
        );
        let mut params = protocol::DocumentLinkParams::default();
        params.text_document.uri = administration.uri.clone();
        let links = swlsp_lsp_documentlink::RelatedProvider::new(
            Some(services.twig.clone()),
            Some(services.configuration.clone()),
            Some(php.clone()),
        )
        .get_document_links(
            &ctx(),
            &DocumentLinkRequest {
                params,
                document: administration.clone(),
            },
        )
        .expect("links");
        check_contains!(
            real_world_document_link_targets(&links),
            file_uri(&format!(
                "{root}/src/Administration/Resources/views/administration/layout/base.html.twig"
            ))
        );
        let navigation_path = format!("{root}/src/Storefront/Controller/NavigationController.php");
        let navigation = document_at(
            root,
            "src/Storefront/Controller/NavigationController.php",
            1,
        );
        let hints = swlsp_lsp_inlay::RouteControllerProvider::new(
            Some(services.services.clone()),
            Some(php.clone()),
        )
        .get_inlay_hints(
            &ctx(),
            &InlayHintRequest {
                params: full_range_params(&navigation),
                document: navigation.clone(),
            },
        )
        .expect("inlay hints");
        let home = hints.iter().find_map(|hint| {
            let parts = label_parts(hint)?;
            if parts.len() != 1 || parts[0].value != "→ NavigationController::home" {
                return None;
            }
            parts[0]
                .location
                .as_ref()
                .map(|location| location.uri.clone())
        });
        check_eq!(Some(file_uri(&navigation_path)), home);
        let declaration = new_document(
            "file:///real-world-template-declaration.php",
            "<?php\nnamespace App\\Controller;\nuse Symfony\\Bridge\\Twig\\Attribute\\Template;\nclass RealWorldController\n{\n    #[Template('@Storefront/storefront/page/content/product-detail.html.twig')]\n    public function existing(): array { return []; }\n\n    #[Template]\n    public function missingAction(): array { return []; }\n}\n",
            1,
        );
        let problems = swlsp_lsp_diagnostics::TemplateAnalyzer::new(
            Some(services.twig.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &declaration)
        .expect("analyze");
        require_len!(problems, 1);
        check_eq!("twig.template.missing", problems[0].id.to_string());
        check_eq!(
            "real_world/missing.html.twig",
            payload_str(&problems[0], "templateName")
        );
        let console = services.console.clone();
        let commands = console.get_command("system:config:get").expect("command");
        assert!(!commands.is_empty());
        for command in &commands {
            require_console_input(&command.arguments, "key");
            require_console_input(&command.options, "format");
            require_console_input(&command.options, "salesChannelId");
        }
        let request = swlsp_console::CatalogRequest {
            query: "system:config:get".into(),
            file_glob: "src/**/Command/*.php".into(),
            ..Default::default()
        };
        let catalog = swlsp_console::CatalogProvider::new(console.clone(), Some(root))
            .catalog_with_request(&ctx(), &request)
            .expect("catalog");
        require_len!(catalog, 1);
        check_eq!("system:config:get", catalog[0].name);
        check_eq!(
            "Shopware\\Core\\System\\SystemConfig\\Command\\ConfigGet",
            catalog[0].class
        );
        check!(!catalog[0].file_path.is_empty());
        check!(swlsp_lsp_analytics::ant_path_match(
            &request.file_glob,
            &catalog[0].file_path
        ));
        require_console_catalog_input(&catalog[0].arguments, "key");
        require_console_catalog_input(&catalog[0].options, "format");
        require_console_catalog_input(&catalog[0].options, "salesChannelId");
        let aliases = console.get_command("snippets:validate").expect("command");
        assert!(!aliases.is_empty());
        check_eq!("translation:validate", aliases[0].canonical);
        let invokable = new_document(
            "file:///real-world-invokable-command.php",
            "<?php\nuse Symfony\\Component\\Console\\Attribute\\AsCommand;\nuse Symfony\\Component\\Console\\Command\\Command;\n\n#[AsCommand(name: 'app:real-world')]\nclass RealWorldCommand\n{\n    public function __invoke()\n    {\n        if (random_int(0, 1)) {\n            return Command::SUCCESS;\n        }\n        return 'invalid';\n    }\n}\n",
            1,
        );
        let problems = swlsp_lsp_diagnostics::InvokableCommandAnalyzer::new(Some(php.clone()))
            .analyze(&ctx(), &invokable)
            .expect("analyze");
        let types = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.console.invoke.return_type")
            .count();
        let values = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.console.invoke.return_value")
            .count();
        check_eq!(1, types);
        check_eq!(1, values);
    }

    fn find_event(f: &RealWorldWorkspaceFixture, name: &str) -> Option<swlsp_event::Event> {
        f.workspace.services().events.event(name).expect("event")
    }

    pub(super) fn services(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let php = &f.php;
        let s = f.workspace.services();
        let cart = find_event(f, "Shopware\\Core\\Checkout\\Cart\\Event\\CartMergedEvent");
        check!(cart.is_some());
        if let Some(cart) = cart {
            check!(
                cart.listeners().iter().any(|listener| listener.class
                    == "Shopware\\Storefront\\Event\\CartMergedSubscriber"
                    && listener.method == "addCartMergedNoticeFlash"),
                "event listener not found in {:#?}",
                cart.listeners()
            );
        }
        let kernel = find_event(f, "kernel.request");
        check!(kernel.is_some());
        check!(!kernel.map(|e| e.listeners()).unwrap_or_default().is_empty());
        assert!(!s.messenger.messages().expect("messages").is_empty());
        let collect = s
            .messenger
            .message("Shopware\\Core\\System\\UsageData\\EntitySync\\CollectEntityDataMessage")
            .expect("message");
        check!(collect.is_some());
        let collect = collect.unwrap_or_default();
        check!(!collect.handlers().is_empty());
        check!(!collect.dispatches().is_empty());
        assert!(!s.environment.variables().expect("variables").is_empty());
        let app_env = s.environment.variable("APP_ENV").expect("variable");
        check!(app_env.is_some());
        let app_env = app_env.unwrap_or_default();
        check!(!app_env.declarations.is_empty(), "APP_ENV declarations");
        check_ge!(app_env.references.len(), 2);
        let deprecated = s
            .services
            .get_all_service_definitions()
            .expect("services")
            .iter()
            .filter(|service| service.deprecated)
            .count();
        check_ge!(deprecated, 5);
        let parameter_name = "shopware.messenger.enforce_message_size";
        let parameter = s
            .services
            .get_parameter_by_name(parameter_name)
            .expect("parameter");
        check!(parameter.is_some());
        let parameter = parameter.unwrap_or_default();
        let source = format!(
            "<?php\nuse Symfony\\Component\\DependencyInjection\\ParameterBag\\ParameterBagInterface;\nfunction inspect(ParameterBagInterface $bag): void {{\n    $bag->get('{parameter_name}');\n    $bag->has('real_world.missing_parameter');\n}}"
        );
        let document = new_document("file:///real-world-parameter-bag.php", &source, 1);
        let offset = (offset_of(&source, parameter_name) + 3) as u32;
        let node = node_at(&document, offset);
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = position(&document, offset);
        let labels = real_world_completion_labels(
            &swlsp_lsp_completion::ServiceCompletionProvider::new(
                Some(s.services.clone()),
                Some(php.clone()),
            )
            .get_completions(
                &ctx(),
                &CompletionRequest {
                    params,
                    syntax: syntax(&document, node.clone(), None),
                },
            ),
        );
        check_contains!(labels, parameter_name.to_owned());
        let locations = swlsp_lsp_definition::ServiceDefinitionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, node, offset),
        );
        require_len!(locations, 1);
        check_eq!(file_uri(&parameter.path), locations[0].uri);
        let problems = swlsp_lsp_diagnostics::ServiceAnalyzer::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_len!(problems, 1);
        check_eq!("symfony.parameter.missing", problems[0].id.to_string());
        let fixtures = [
            (
                format!("{root}/config/services_test.xml"),
                format!(
                    "{root}/src/Core/Framework/Telemetry/Metrics/Transport/TransportCollection.php"
                ),
                "create",
            ),
            (
                format!(
                    "{root}/tests/integration/Core/Framework/Plugin/_fixtures/plugins/SwagTestPlugin/src/Resources/config/services.xml"
                ),
                format!(
                    "{root}/tests/integration/Core/Framework/Plugin/_fixtures/plugins/SwagTestPlugin/src/SwagTestPlugin.php"
                ),
                "manualSetter",
            ),
            (
                format!("{root}/src/Core/System/DependencyInjection/snippet.xml"),
                format!("{root}/src/Core/System/Snippet/Service/TranslationConfigLoader.php"),
                "load",
            ),
        ];
        let fixture = fixtures.iter().find(|(path, target, _)| {
            std::path::Path::new(path).exists() && std::path::Path::new(target).exists()
        });
        match fixture {
            None => eprintln!(
                "configured XML service-method fixture is not present in this Shopware checkout"
            ),
            Some((path, target, method)) => {
                let source = read(path);
                let document = new_document(file_uri(path), &source, 1);
                let marker = format!("method=\"{method}\"");
                let prefix = format!("method=\"{}", &method[..std::cmp::max(1, method.len() / 2)]);
                let offset = (offset_of(&source, &marker) + prefix.len()) as u32;
                check_gt!(offset, prefix.len() as u32);
                let node = node_at(&document, offset);
                let mut params = protocol::CompletionParams::default();
                params.text_document.uri = document.uri.clone();
                params.position = position(&document, offset);
                let items = swlsp_lsp_completion::ServiceCompletionProvider::new(
                    Some(s.services.clone()),
                    Some(php.clone()),
                )
                .get_completions(
                    &ctx(),
                    &CompletionRequest {
                        params,
                        syntax: syntax(&document, node.clone(), None),
                    },
                );
                let completion = real_world_completion_by_label(&items, method);
                check_eq!(
                    Some(method.to_string()),
                    completion.text_edit.map(|edit| edit.new_text)
                );
                let locations = swlsp_lsp_definition::ServiceDefinitionProvider::new(
                    Some(s.services.clone()),
                    Some(php.clone()),
                )
                .get_definition(
                    &ctx(),
                    &real_world_definition_request(&document, node, offset),
                );
                require_len!(locations, 1);
                check_eq!(file_uri(target), locations[0].uri);
            }
        }
        let services_yaml = file_uri(&format!("{root}/config/services.yaml"));
        let authoring =
            swlsp_lsp_completion::YamlServiceAuthoringCompletionProvider::new(php.project());
        let source = "services:\n  app.real_world:\n    aut";
        let document = new_document(services_yaml.clone(), source, 1);
        let items = authoring.get_completions(
            &ctx(),
            &real_world_completion_request(&document, source.len() as u32),
        );
        let autowire = real_world_completion_by_label(&items, "autowire");
        check_eq!(
            Some("autowire: ".to_owned()),
            autowire.text_edit.map(|edit| edit.new_text)
        );
        check!(real_world_completion_by_label(&items, "factory_class").deprecated);
        let source = "services:\n  app.real_world:\n    arguments: [!tag";
        let document = new_document(services_yaml.clone(), source, 1);
        let labels = real_world_completion_labels(&authoring.get_completions(
            &ctx(),
            &real_world_completion_request(&document, source.len() as u32),
        ));
        check_contains!(labels, "!tagged_iterator".to_owned());
        check_contains!(labels, "!tagged_locator".to_owned());
        check_not_contains!(labels, "!tagged".to_owned());
        let routes_yaml = file_uri(&format!("{root}/config/routes.yaml"));
        let route_authoring =
            swlsp_lsp_completion::YamlRouteAuthoringCompletionProvider::new(Some(s.routes.clone()));
        let source = "real_world.catalog:\n  path: /real-world/{id}\n  meth";
        let document = new_document(routes_yaml.clone(), source, 1);
        let items = route_authoring.get_completions(
            &ctx(),
            &real_world_completion_request(&document, source.len() as u32),
        );
        check_eq!(
            Some("methods: ".to_owned()),
            real_world_completion_by_label(&items, "methods")
                .text_edit
                .map(|edit| edit.new_text)
        );
        check!(real_world_completion_by_label(&items, "pattern").deprecated);
        let source =
            "real_world.catalog:\n  path: /real-world/{id}/{slug}\n  requirements:\n    sl";
        let document = new_document(routes_yaml.clone(), source, 1);
        let items = route_authoring.get_completions(
            &ctx(),
            &real_world_completion_request(&document, source.len() as u32),
        );
        check_eq!(
            Some("slug: ".to_owned()),
            real_world_completion_by_label(&items, "slug")
                .text_edit
                .map(|edit| edit.new_text)
        );
        let notification = s
            .services
            .get_service_by_id("Shopware\\Administration\\Notification\\NotificationDefinition")
            .expect("service")
            .expect("NotificationDefinition service");
        check!(notification.deprecated);
        let started = std::time::Instant::now();
        let located = swlsp_lsp_analytics::ServiceLocatorProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .locate(
            &ctx(),
            &swlsp_lsp_analytics::ServiceLocatorRequest {
                identifier: notification.id.clone(),
                ..Default::default()
            },
        )
        .expect("locate");
        eprintln!(
            "service locator analytics: {:?} ({} matches)",
            started.elapsed(),
            located.len()
        );
        let entry = located
            .iter()
            .find(|entry| entry.id == notification.id)
            .unwrap_or_else(|| panic!("service locator entry {:?} not found", notification.id));
        check_eq!(notification.class, entry.resolved_class);
        check!(entry.deprecated);
        check!(!entry.definitions.is_empty());
        let collection = swlsp_lsp_codeaction::SymfonyGeneratorProvider::new(
            Some(php.clone()),
            Some(s.services.clone()),
        )
        .collect_service_definitions(
            &ctx(),
            &swlsp_lsp_codeaction::SymfonyServiceDefinitionCollectionRequest {
                class_names: notification.class.clone(),
                output: "yaml".into(),
                class_as_id: true,
                ..Default::default()
            },
        )
        .unwrap_or_else(|error| {
            record_failure(format!("collect service definitions: {error}"));
            Default::default()
        });
        check_eq!(1, collection.definitions.len());
        if let Some(definition) = collection.definitions.first() {
            check_empty!(definition.error);
            check_str!(definition.content, format!("{}:", notification.class));
        }
        let names = s.configuration.names().expect("names");
        check_contains!(names, "shopware".to_owned());
        let roots = s.configuration.roots("shopware").expect("roots");
        assert!(!roots.is_empty());
        check_eq!(
            "Shopware\\Core\\Framework\\DependencyInjection\\Configuration",
            roots[0].class
        );
        s.serializer.classes().expect("serializer classes");
    }

    /// The extraction file outside the
    /// workspace resolves from disk; everything else through the server.
    struct ExtractionHost {
        server: Server,
        file: String,
    }

    impl DocumentResolver for ExtractionHost {
        fn resolve_document(&self, ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
            if uri == file_uri(&self.file) {
                let source = std::fs::read_to_string(&self.file)?;
                return Ok(DocumentSnapshot {
                    document: new_document(uri, &source, 0),
                    version: None,
                });
            }
            self.server.resolve_document(ctx, uri)
        }
    }

    impl WorkspaceEditHost for ExtractionHost {
        fn workspace_edit(
            &self,
            ctx: &Context,
            plan: &swlsp_rewrite::WorkspacePlan,
        ) -> Result<protocol::WorkspaceEdit, BoxError> {
            self.server.workspace_edit(ctx, plan)
        }
    }

    pub(super) fn translation_and_routes(f: &RealWorldWorkspaceFixture) {
        #[allow(unused_variables)]
        let root = f.root.as_str();
        let s = f.workspace.services();
        let translations = s.translations.clone();
        let domains = translations.domains().expect("domains");
        let mut assistant = None;
        for domain in &domains {
            let keys = translations.keys(domain).expect("keys");
            if let Some(key) = keys.first() {
                assistant = Some((domain.clone(), key.clone()));
                break;
            }
        }
        check!(assistant.is_some());
        let text = "Real world extraction";
        let key = "shopware_lsp.real_world.extraction";
        let extraction_domain = domains
            .iter()
            .find(|domain| {
                !translations
                    .insertions_with_value(domain, key, text)
                    .expect("insertions")
                    .is_empty()
            })
            .cloned()
            .expect("the real project must expose a writable YAML/XLIFF translation domain");
        let source = format!(
            "{{% trans_default_domain '{}' %}}<p>{text}</p>",
            extraction_domain.replace('\'', "\\'")
        );
        let line_index = swlsp_syntax::LineIndex::new(source.as_str());
        let start = offset_of(&source, text);
        let end = start + text.len();
        let (start_line, start_character) = line_index.position_utf16(start as u32);
        let (end_line, end_character) = line_index.position_utf16(end as u32);
        let range = protocol::Range {
            start: protocol::Position {
                line: start_line,
                character: start_character,
            },
            end: protocol::Position {
                line: end_line,
                character: end_character,
            },
        };
        {
            let temp = tempfile::tempdir().expect("temp dir");
            let file = temp
                .path()
                .join("real-world-extraction.html.twig")
                .to_string_lossy()
                .into_owned();
            std::fs::write(&file, &source).expect("write extraction file");
            let host = Arc::new(ExtractionHost {
                server: Server::new(None, root, "test"),
                file: file.clone(),
            });
            let provider = swlsp_lsp_codeaction::TwigTranslationExtractProvider::new(
                Some(translations.clone()),
                Some(host.clone()),
            );
            let commands = provider.commands(&ctx());
            let mut request =
                serde_json::json!({"fileUri": file_uri(&file), "source": source, "range": range});
            let prepared =
                commands["shopware/symfony/translation/extract/prepare"](&ctx(), Some(&request))
                    .expect("prepare");
            check_eq!(Some(text), prepared["text"].as_str());
            check_eq!(
                Some(extraction_domain.as_str()),
                prepared["defaultDomain"].as_str()
            );
            check!(
                prepared["domains"].as_array().is_some_and(|domains| domains
                    .iter()
                    .any(|d| d.as_str() == Some(extraction_domain.as_str()))),
                "{prepared}"
            );
            request["key"] = Value::from(key);
            request["domain"] = Value::from(extraction_domain.clone());
            request["range"] = prepared["range"].clone();
            let generated =
                commands["shopware/symfony/translation/extract/generate"](&ctx(), Some(&request))
                    .expect("generate");
            check_eq!(
                Some(format!("{{{{ '{key}'|trans }}}}")),
                generated["replacement"].as_str().map(str::to_owned)
            );
            let targets = generated["targets"].as_array().cloned().unwrap_or_default();
            check!(!targets.is_empty());
            for target in &targets {
                check!(!target["fileUri"].as_str().unwrap_or_default().is_empty());
                check_str!(target["newText"].as_str().unwrap_or_default(), text);
            }
            let _ = host.server.close_all();
        }
        let routes = s
            .routes
            .find_routes_by_path("/sitemap/shop.xml.gz")
            .expect("routes");
        require_route(&routes, "frontend.sitemap.proxy");
        super::analytics::run(f);
    }
}

mod analytics {
    use std::sync::Arc;

    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;

    use super::*;

    fn edits_for(action: &protocol::CodeAction, uri: &str) -> Vec<protocol::TextEdit> {
        action
            .edit
            .as_ref()
            .and_then(|edit| edit.changes.get(uri))
            .cloned()
            .unwrap_or_default()
    }

    pub(super) fn run(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let php = &f.php;
        let s = f.workspace.services();
        let html_routes = s
            .routes
            .find_routes_by_path("/sitemap/shop.xml.gz")
            .expect("routes");
        assert!(!html_routes.is_empty());
        let provider = swlsp_lsp_analytics::RouteCatalogProvider::new(
            root,
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(php.clone()),
            Some(s.twig.clone()),
        );
        let started = std::time::Instant::now();
        let all = provider
            .catalog(&ctx(), &swlsp_lsp_analytics::RouteCatalogRequest::default())
            .expect("catalog");
        check_gt!(all.len(), 100);
        eprintln!(
            "route analytics catalog: {:?} ({} routes)",
            started.elapsed(),
            all.len()
        );
        match swlsp_lsp_analytics::ProfilerCatalogProvider::new(
            root,
            Some(php.clone()),
            Some(s.twig.clone()),
        )
        .catalog(
            &ctx(),
            &swlsp_lsp_analytics::ProfilerRequestCatalogRequest {
                limit: 3,
                ..Default::default()
            },
        ) {
            Err(error) => {
                let message = error.to_string();
                check!(
                    message.contains("no local Symfony profiler index")
                        || message.contains("no profiler requests were found"),
                    "unexpected optional profiler analytics error: {message}"
                );
            }
            Ok(catalog) => {
                check!(!catalog.is_empty());
                check!(catalog.len() <= 3);
                for request in &catalog {
                    check!(!request.hash.is_empty());
                    check!(!request.index_file_uri.is_empty());
                }
            }
        }
        let request = swlsp_lsp_analytics::RouteCatalogRequest {
            route_name: "frontend.home.page".into(),
            file_glob: "src/**/NavigationController.php".into(),
            ..Default::default()
        };
        let catalog = provider.catalog(&ctx(), &request).expect("catalog");
        require_len!(catalog, 1);
        let navigation = format!("{root}/src/Storefront/Controller/NavigationController.php");
        check_eq!("frontend.home.page", catalog[0].name);
        check_eq!("/", catalog[0].path);
        check_eq!(vec!["GET".to_owned()], catalog[0].methods);
        check_eq!(
            "Shopware\\Storefront\\Controller\\NavigationController::home",
            catalog[0].resolved_controller
        );
        check_eq!(file_uri(&navigation), catalog[0].controller_uri);
        check_eq!(56, catalog[0].controller_line);
        check_contains!(
            catalog[0].templates,
            "@Storefront/storefront/page/content/index.html.twig".to_owned()
        );
        let routes_yaml = file_uri(&format!("{root}/config/routes.yaml"));
        let authoring =
            swlsp_lsp_completion::YamlRouteAuthoringCompletionProvider::new(Some(s.routes.clone()));
        let source = "real_world.draft:\n  path: /site";
        let document = new_document(routes_yaml.clone(), source, 1);
        let items = authoring.get_completions(
            &ctx(),
            &real_world_completion_request(&document, source.len() as u32),
        );
        let sitemap = real_world_completion_by_label(&items, &html_routes[0].path);
        check_eq!(Some(protocol::CompletionItemKind::REFERENCE), sitemap.kind);
        check_eq!(
            Some(html_routes[0].path.clone()),
            sitemap.text_edit.map(|edit| edit.new_text)
        );
        let php_provider =
            swlsp_lsp_completion::RouteCompletionProvider::new(s.routes.clone(), Some(php.clone()));
        let source = "<?php\nuse Symfony\\Component\\Routing\\Attribute\\Route;\n\n#[Route('/site')]\nfunction draft(): void {}\n";
        let document = new_document(
            file_uri(&format!("{root}/src/DraftController.php")),
            source,
            1,
        );
        let items = php_provider.get_completions(
            &ctx(),
            &real_world_completion_request(
                &document,
                (offset_of(source, "/site") + "/site".len()) as u32,
            ),
        );
        let completion = real_world_completion_by_label(&items, &html_routes[0].path);
        check_eq!(
            Some(html_routes[0].path.clone()),
            completion.text_edit.map(|edit| edit.new_text)
        );
        let source = "<?php\n/** @Route(path=\"/site\") */\nfunction draft(): void {}\n";
        let document = new_document(
            file_uri(&format!("{root}/src/LegacyController.php")),
            source,
            1,
        );
        let items = php_provider.get_completions(
            &ctx(),
            &real_world_completion_request(
                &document,
                (offset_of(source, "/site") + "/site".len()) as u32,
            ),
        );
        real_world_completion_by_label(&items, &html_routes[0].path);
        let source = "<?php\nnamespace Shopware\\Core\\Controller;\n\nclass CatalogController\n{\n    #[Route(path: '/catalog', name: 'dr')]\n    public function detailAction(): void {}\n}\n";
        let document = new_document(
            file_uri(&format!("{root}/src/CatalogController.php")),
            source,
            1,
        );
        let items = php_provider.get_completions(
            &ctx(),
            &real_world_completion_request(
                &document,
                (offset_of(source, "'dr'") + "'dr".len()) as u32,
            ),
        );
        let generated = real_world_completion_by_label(&items, "shopware_core_catalog_detail");
        check_eq!(
            Some("shopware_core_catalog_detail".to_owned()),
            generated.text_edit.map(|edit| edit.new_text)
        );
        let source = "<?php\nnamespace Shopware\\Core\\Controller;\n\nclass CatalogController\n{\n    /** @Route(path=\"/catalog\", name=\"dr\") */\n    public function detailAction(): void {}\n}\n";
        let document = new_document(
            file_uri(&format!("{root}/src/LegacyCatalogController.php")),
            source,
            1,
        );
        let items = php_provider.get_completions(
            &ctx(),
            &real_world_completion_request(
                &document,
                (offset_of(source, "\"dr\"") + "\"dr".len()) as u32,
            ),
        );
        real_world_completion_by_label(&items, "shopware_core_catalog_detail");
        let absolute = s
            .routes
            .find_routes_by_path("https://shopware.test/sitemap/shop.xml.gz?preview=1#details")
            .expect("routes");
        check_eq!(html_routes, absolute);
        let prefix = s
            .routes
            .find_routes_by_path_prefix(
                "/store-api/checkout/cart/line-item",
                "/store-api/checkout/cart/line-item/delete",
            )
            .expect("routes");
        require_route(&prefix, "store-api.checkout.cart.add");
        require_route(&prefix, "store-api.checkout.cart.update-lineitem");
        require_route(&prefix, "store-api.checkout.cart.remove-item");
        for route in &prefix {
            check!(route.name != "store-api.checkout.cart.remove-item-v2");
        }
        let import_path = format!("{root}/src/Core/Framework/Resources/config/routes_dev.php");
        let target_path =
            format!("{root}/vendor/symfony/framework-bundle/Resources/config/routing/errors.php");
        let import_source = read(&import_path);
        let import_document = new_document(file_uri(&import_path), &import_source, 1);
        let bundle_offset = (offset_of(&import_source, "@FrameworkBundle") + 3) as u32;
        let locations = swlsp_lsp_definition::RouteDefinitionProvider::new(
            Some(s.routes.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request_at(&import_document, bundle_offset),
        );
        require_len!(locations, 1);
        check_eq!(file_uri(&target_path), locations[0].uri);
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = import_document.uri.clone();
        params.position = position(&import_document, bundle_offset);
        let started = std::time::Instant::now();
        let items =
            swlsp_lsp_completion::BundleResourceCompletionProvider::new(Some(php.clone()), None)
                .get_completions(
                    &ctx(),
                    &CompletionRequest {
                        params,
                        syntax: syntax(
                            &import_document,
                            node_at(&import_document, bundle_offset),
                            None,
                        ),
                    },
                );
        eprintln!(
            "bundle resource completion: {:?} ({} candidates)",
            started.elapsed(),
            items.len()
        );
        let framework = real_world_completion_by_label(
            &items,
            "FrameworkBundle/Resources/config/routing/errors.php",
        );
        check_eq!(
            Some("@FrameworkBundle/Resources/config/routing/errors.php".to_owned()),
            framework.text_edit.map(|edit| edit.new_text)
        );
        let has_compiled = std::fs::read_dir(format!("{root}/var/cache"))
            .map(|entries| {
                entries
                    .flatten()
                    .any(|entry| entry.path().join("url_generating_routes.php").is_file())
            })
            .unwrap_or(false);
        if has_compiled {
            let compiled = s.routes.get_compiled_routes();
            assert!(
                !compiled.is_empty(),
                "the generated route catalog should be loaded"
            );
            check_eq!(
                "url_generating_routes.php",
                swlsp_uriutil::filepath::base(&compiled[0].file_path)
            );
        }
        let analyzer = swlsp_lsp_diagnostics::RouteAnalyzer::new(
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(php.clone()),
        );
        let source = "{% if app.request.attributes.get('_route') in ['frontend.sitemap.proxy'] %}{% endif %}";
        let document = new_document("file:///real-world-route-comparison.twig", source, 1);
        check_empty!(
            analyzer
                .analyze(&ctx(), &document)
                .expect("analyze")
                .iter()
                .map(|p| p.message.clone())
                .collect::<Vec<_>>()
        );
        let offset = (offset_of(source, "frontend.sitemap.proxy") + 3) as u32;
        let locations =
            swlsp_lsp_definition::RouteDefinitionProvider::new(Some(s.routes.clone()), None)
                .get_definition(&ctx(), &real_world_definition_request_at(&document, offset));
        assert!(!locations.is_empty());
        require_route_definition_path(&locations, &html_routes[0].file_path);
        let document = new_document(
            "file:///real-world-deprecated-route.twig",
            "{{ path('widgets.account.order.detail') }}",
            1,
        );
        let problems = analyzer.analyze(&ctx(), &document).expect("analyze");
        require_len!(problems, 1);
        check_eq!("symfony.controller.deprecated", problems[0].id.to_string());
        check_eq!(vec![protocol::DiagnosticTag::DEPRECATED], problems[0].tags);
        code_actions(f);
    }

    fn code_actions(f: &RealWorldWorkspaceFixture) {
        let php = &f.php;
        let s = f.workspace.services();
        let document = new_document(
            "file:///real-world-route-attribute.php",
            "<?php\nnamespace App\\Controller\\Admin;\nuse Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController;\nclass ProductController extends AbstractController\n{\n    public function editAction(): void {}\n}",
            1,
        );
        let actions =
            swlsp_lsp_codeaction::RouteAttributeCodeActionProvider::new(Some(php.clone()))
                .get_code_actions(
                    &ctx(),
                    &real_world_code_action_request(&document, "editAction"),
                );
        require_len!(actions, 1);
        let edits = edits_for(&actions[0], &document.uri);
        require_len!(edits, 2);
        check_str!(
            edits[0].new_text,
            "#[Route('/admin/product/edit', name: 'app_admin_product_edit')]"
        );
        let document = new_document(
            "file:///real-world-route-parameter.php",
            "<?php\nnamespace App\\Controller;\nuse Symfony\\Component\\Routing\\Attribute\\Route;\nclass ProductController\n{\n    #[Route('/product/{id}')]\n    public function detail(?string $format = null): void {}\n}",
            1,
        );
        let actions = swlsp_lsp_codeaction::RouteActionParameterCodeActionProvider::new()
            .get_code_actions(&ctx(), &real_world_code_action_request(&document, "detail"));
        require_len!(actions, 2);
        check_eq!(
            "Symfony: Add Request parameter to route action",
            actions[0].title
        );
        let edits = edits_for(&actions[0], &document.uri);
        require_len!(edits, 2);
        check_eq!("Request $request, ", edits[0].new_text);
        check_str!(
            edits[1].new_text,
            "use Symfony\\Component\\HttpFoundation\\Request;"
        );
        check!(
            php.find_class("Symfony\\Component\\Console\\Command\\InvokableCommand")
                .is_some()
        );
        let source = "<?php\nnamespace App\\Command;\nuse Symfony\\Component\\Console\\Attribute\\AsCommand;\nuse Symfony\\Component\\Console\\Command\\Command;\nuse Symfony\\Component\\Console\\Input\\InputArgument;\nuse Symfony\\Component\\Console\\Input\\InputInterface;\nuse Symfony\\Component\\Console\\Input\\InputOption;\nuse Symfony\\Component\\Console\\Output\\OutputInterface;\n\n#[AsCommand(name: 'app:index-migrate')]\nclass IndexMigrateCommand extends Command\n{\n    protected function configure(): void\n    {\n        $this->addArgument(\n            'project',\n            InputArgument::REQUIRED,\n            'Project'\n        );\n        $this->addOption(\n            'dry-run',\n            'd',\n            InputOption::VALUE_NONE,\n            'Dry run'\n        );\n    }\n\n    protected function execute(\n        InputInterface $input,\n        OutputInterface $output,\n    ): int {\n        $project = $input->getArgument('project');\n        $dryRun = $input->getOption('dry-run');\n        $output->writeln($project);\n\n        return $dryRun ? self::SUCCESS : Command::SUCCESS;\n    }\n}";
        let document = new_document("file:///real-world-command-migration.php", source, 1);
        let actions = swlsp_lsp_codeaction::InvokableCommandMigrationCodeActionProvider::new(Some(
            php.clone(),
        ))
        .get_code_actions(
            &ctx(),
            &real_world_code_action_request(&document, "IndexMigrateCommand"),
        );
        require_len!(actions, 1);
        let edits = edits_for(&actions[0], &document.uri);
        require_len!(edits, 1);
        let migrated = edits[0].new_text.clone();
        check_str!(migrated, "public function __invoke(");
        check_str!(
            migrated,
            "#[Argument(description: 'Project')] string $project"
        );
        check_str!(
            migrated,
            "#[Option(name: 'dry-run', shortcut: 'd', description: 'Dry run')] bool $dryRun = false"
        );
        check_not_str!(migrated, "extends Command");
        check_not_str!(migrated, "function configure");
        check_not_str!(migrated, "$input->getArgument");
        check!(
            new_document(document.uri.clone(), &migrated, 2)
                .parse_errors()
                .is_empty()
        );
        let temp = tempfile::tempdir().expect("temp dir");
        let index = swlsp_console::Index::new(temp.path(), None).expect("console index");
        swlsp_indexer::Indexer::index(
            &index,
            &swlsp_indexer::ParsedFile::new(
                "/real-world-command-migration.php",
                migrated.clone().into_bytes(),
            ),
        )
        .expect("index migrated command");
        let commands = index.get_command("app:index-migrate").expect("command");
        require_len!(commands, 1);
        require_console_input(&commands[0].arguments, "project");
        require_console_input(&commands[0].options, "dry-run");
        let _ = swlsp_indexer::Indexer::close(&index);
        check!(
            php.find_class("Symfony\\Component\\Console\\Style\\SymfonyStyle")
                .is_some()
        );
        let document = new_document(
            "file:///real-world-command-parameter.php",
            "<?php\nnamespace App\\Command;\nuse Symfony\\Component\\Console\\Attribute\\AsCommand;\n#[AsCommand(name: 'app:index-test')]\nclass IndexTestCommand\n{\n    public function __invoke(?string $format = null): int\n    {\n        return 0;\n    }\n}",
            1,
        );
        let actions =
            swlsp_lsp_codeaction::CommandInvokeParameterCodeActionProvider::new(Some(php.clone()))
                .get_code_actions(
                    &ctx(),
                    &real_world_code_action_request(&document, "IndexTestCommand"),
                );
        require_len!(actions, 5);
        let style = actions
            .iter()
            .find(|action| action.title.contains("Add SymfonyStyle parameter"))
            .expect("SymfonyStyle action");
        let edits = edits_for(style, &document.uri);
        require_len!(edits, 2);
        check_eq!("SymfonyStyle $io, ", edits[0].new_text);
        check_str!(
            edits[1].new_text,
            "use Symfony\\Component\\Console\\Style\\SymfonyStyle;"
        );
        check!(php.find_class("Twig\\Attribute\\AsTwigFunction").is_some());
        let document = new_document(
            "file:///real-world-twig-migration.php",
            "<?php\nnamespace App\\Twig;\nuse Twig\\Extension\\AbstractExtension;\nuse Twig\\TwigFunction;\nclass IndexTestExtension extends AbstractExtension\n{\n    public function getFunctions(): array\n    {\n        return [\n            new TwigFunction('index_test', [$this, 'indexTest']),\n        ];\n    }\n\n    public function indexTest(string $value): string\n    {\n        return $value;\n    }\n}",
            1,
        );
        let actions =
            swlsp_lsp_codeaction::TwigExtensionAttributeCodeActionProvider::new(Some(php.clone()))
                .get_code_actions(
                    &ctx(),
                    &real_world_code_action_request(&document, "IndexTestExtension"),
                );
        require_len!(actions, 1);
        let edits = edits_for(&actions[0], &document.uri);
        require_len!(edits, 1);
        check_str!(edits[0].new_text, "#[AsTwigFunction('index_test')]");
        check_not_str!(edits[0].new_text, "extends AbstractExtension");
        let migrated = new_document(document.uri.clone(), &edits[0].new_text, 2);
        check!(migrated.parse_errors().is_empty());
        let callables = swlsp_twig::parse_twig_extension_tree(
            "/real-world-twig-migration.php",
            &migrated.root().expect("tree"),
            migrated.source(),
            migrated.line_index(),
        );
        require_len!(callables.functions, 1);
        check_eq!("index_test", callables.functions[0].name);
        check_eq!(
            "App\\Twig\\IndexTestExtension::indexTest",
            callables.functions[0].method
        );
        check!(callables.filters.is_empty());
        let document = new_document(
            "file:///real-world-deprecated-member.twig",
            "{# @var header \\Shopware\\Storefront\\Pagelet\\Header\\HeaderPagelet #}\n{{ header.activeLanguage }}",
            1,
        );
        let problems = swlsp_lsp_diagnostics::TwigMemberDeprecationAnalyzer::new(
            Some(s.twig.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_len!(problems, 1);
        check_eq!("twig.member.deprecated", problems[0].id.to_string());
        check_str!(problems[0].message, "HeaderPagelet::getActiveLanguage");
        check_eq!(vec![protocol::DiagnosticTag::DEPRECATED], problems[0].tags);
        let source = "{# @var context \\Shopware\\Core\\Framework\\Context #}\n{{ context.versionId }}\n{{ context.SYSTEM_SCOPE }}\n{{ context.definitelyMissing }}";
        let document = new_document("file:///real-world-missing-member.twig", source, 1);
        let problems = swlsp_lsp_diagnostics::TwigMemberMissingAnalyzer::new(
            Some(s.twig.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_len!(problems, 1);
        check_eq!("twig.member.missing", problems[0].id.to_string());
        let start: usize = problems[0].range.start().into();
        let end: usize = problems[0].range.end().into();
        check_eq!("definitelyMissing", &source[start..end]);
        check!(
            php.find_class("Twig\\Extension\\ExtensionInterface")
                .is_some()
        );
        let document = new_document(
            "file:///real-world-service-tag.yaml",
            "services:\n  app.cart_merged_subscriber:\n    class: Shopware\\Storefront\\Event\\CartMergedSubscriber\n",
            1,
        );
        let actions = swlsp_lsp_codeaction::ServiceTagCodeActionProvider::new(Some(php.clone()))
            .get_code_actions(
                &ctx(),
                &real_world_code_action_request(&document, "CartMergedSubscriber"),
            );
        require_len!(actions, 1);
        check_eq!(
            "Symfony: Add service tag 'kernel.event_subscriber'",
            actions[0].title
        );
        let edits = edits_for(&actions[0], &document.uri);
        require_len!(edits, 1);
        check_str!(edits[0].new_text, "kernel.event_subscriber");
        let document = new_document(
            "file:///real-world-service-suggestion.xml",
            "<container>\n  <services>\n    <service id=\"app.cart_merged\" class=\"Shopware\\Storefront\\Event\\CartMergedSubscriber\">\n      <argument/>\n    </service>\n  </services>\n</container>",
            1,
        );
        let actions = swlsp_lsp_codeaction::XmlServiceSuggestionCodeActionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_code_actions(
            &ctx(),
            &real_world_code_action_request(&document, "<argument"),
        );
        assert!(!actions.is_empty());
        let translator = actions
            .iter()
            .find(|action| action.title.ends_with("for $translator"))
            .expect("translator service action");
        let edits = edits_for(translator, &document.uri);
        require_len!(edits, 1);
        check_str!(edits[0].new_text, r#"type="service""#);
        check_str!(edits[0].new_text, r#"id=""#);
        super::inlays::run(f);
    }
}

mod inlays {
    use std::sync::Arc;

    use serde_json::Value;
    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;

    use super::domains::full_range_params;
    use super::*;

    fn edits_for(action: &protocol::CodeAction, uri: &str) -> Vec<protocol::TextEdit> {
        action
            .edit
            .as_ref()
            .and_then(|edit| edit.changes.get(uri))
            .cloned()
            .unwrap_or_default()
    }

    /// HTTP client option checks: at least 20 declared options
    /// and `timeout` typed and defaulted `null`.
    fn check_http_client_options(php: &swlsp_php::PhpIndex) {
        let options = swlsp_httpclient::options(php);
        check_ge!(options.len(), 20);
        let timeout = options.iter().find(|option| option.name == "timeout");
        check!(timeout.is_some());
        if let Some(timeout) = timeout {
            check_eq!("null", timeout.ty.to_string());
            check_eq!("null", timeout.default.as_str());
        }
    }

    fn php_request(document: &Arc<TextDocument>, offset: u32) -> CompletionRequest {
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = position(document, offset);
        CompletionRequest {
            params,
            syntax: syntax(document, node_at(document, offset), None),
        }
    }

    pub(super) fn run(f: &RealWorldWorkspaceFixture) {
        let php = &f.php;
        let s = f.workspace.services();
        let document = new_document(
            "file:///real-world-service-inlay.yaml",
            "services:\n  app.cart_merged:\n    class: Shopware\\Storefront\\Event\\CartMergedSubscriber\n    arguments: ['@translator', '@request_stack']\n",
            1,
        );
        let hints = swlsp_lsp_inlay::ServiceArgumentProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_inlay_hints(
            &ctx(),
            &InlayHintRequest {
                params: full_range_params(&document),
                document: document.clone(),
            },
        )
        .expect("inlay hints");
        require_len!(hints, 2);
        check_eq!(
            protocol::InlayHintLabel::from("TranslatorInterface"),
            hints[0].label
        );
        check_eq!(
            protocol::InlayHintLabel::from("RequestStack"),
            hints[1].label
        );
        let document = new_document(
            "file:///real-world-property-service.php",
            "<?php\nnamespace Shopware\\Storefront\\Event;\nclass CartMergedSubscriber\n{\n    public function indexTest(): void\n    {\n        $this->indexTestLogger->info('indexed');\n    }\n}",
            1,
        );
        let actions = swlsp_lsp_codeaction::PropertyServiceCodeActionProvider::new(
            Some(php.clone()),
            Some(s.services.clone()),
        )
        .get_code_actions(
            &ctx(),
            &real_world_code_action_request(&document, "indexTestLogger"),
        );
        let logger = actions
            .iter()
            .find(|action| action.title == "Symfony: Inject LoggerInterface as $indexTestLogger")
            .expect("logger property action");
        let edits = edits_for(logger, &document.uri);
        require_len!(edits, 2);
        check_str!(
            edits[0].new_text,
            "private readonly LoggerInterface $indexTestLogger"
        );
        check_str!(edits[1].new_text, "use Psr\\Log\\LoggerInterface;");
        let bundle =
            super::domains::document_at(&f.root, "src/Core/Framework/Test/TestBundle.php", 1);
        let actions = swlsp_lsp_codeaction::SymfonyGeneratorProvider::new(
            Some(php.clone()),
            Some(s.services.clone()),
        )
        .get_code_actions(
            &ctx(),
            &real_world_code_action_request(&bundle, "function build"),
        );
        require_len!(actions, 2);
        check_eq!("Generate Symfony service", actions[0].title);
        check_eq!(
            Some("shopware.symfony.generateService".to_owned()),
            actions[0].command.as_ref().map(|c| c.command.clone())
        );
        check_eq!("Symfony: Create CompilerPass", actions[1].title);
        check_eq!(
            Some("shopware.symfony.createCompilerPass".to_owned()),
            actions[1].command.as_ref().map(|c| c.command.clone())
        );
        let document = new_document(
            "file:///real-world-tagged-service.yaml",
            "services:\n  invalid_extension:\n    class: Shopware\\Core\\Kernel\n    tags:\n      - { name: twig.extension }\n",
            1,
        );
        let problems = swlsp_lsp_diagnostics::TaggedServiceAnalyzer::new(Some(php.clone()))
            .analyze(&ctx(), &document)
            .expect("analyze");
        require_len!(problems, 1);
        check_eq!("symfony.service.tag_type", problems[0].id.to_string());
        check_str!(problems[0].message, "twig.extension");
        check_gt!(php.constant_symbols().len(), 500);
        check_http_client_options(php);
        let source = "<?php\nuse Symfony\\Contracts\\HttpClient\\HttpClientInterface;\nfunction fetch(HttpClientInterface $client): void\n{\n    $client->request('GET', '/', [\n        'headers' => [],\n        'ti' => null,\n    ]);\n}\n";
        let document = new_document("file:///real-world-http-client.php", source, 1);
        let items = swlsp_lsp_completion::HttpClientCompletionProvider::new(Some(php.clone()))
            .get_completions(
                &ctx(),
                &php_request(&document, (offset_of(source, "'ti'") + 2) as u32),
            );
        let mut timeout = false;
        for item in &items {
            check!(item.label != "headers");
            if item.label == "timeout" {
                timeout = true;
                check_str!(item.detail, "null");
            }
        }
        check!(timeout);
        let started = std::time::Instant::now();
        let helpers = swlsp_console::helpers(php);
        let elapsed = started.elapsed();
        check_ge!(helpers.len(), 5);
        let question = helpers.iter().find(|helper| helper.name == "question");
        check!(question.is_some());
        check_eq!(
            Some("Symfony\\Component\\Console\\Helper\\QuestionHelper"),
            question.map(|h| h.class.as_str())
        );
        eprintln!(
            "console helper discovery: {elapsed:?} ({} helpers)",
            helpers.len()
        );
        let source = "<?php\nuse Symfony\\Component\\Console\\Helper\\HelperSet;\nfunction helper(HelperSet $helpers): void\n{\n    $helpers->get('que');\n}\n";
        let document = new_document("file:///real-world-console-helper.php", source, 1);
        let offset = (offset_of(source, "'que'") + 2) as u32;
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = document.uri.clone();
        let items = swlsp_lsp_completion::ConsoleHelperCompletionProvider::new(Some(php.clone()))
            .get_completions(
                &ctx(),
                &CompletionRequest {
                    params,
                    syntax: syntax(&document, node_at(&document, offset), None),
                },
            );
        let question = items.iter().find(|item| item.label == "question");
        check!(question.is_some());
        check_eq!(
            Some("Symfony\\Component\\Console\\Helper\\QuestionHelper"),
            question.map(|item| item.detail.as_str())
        );
        let attribute_source = "<?php\nnamespace App\\Controller;\nclass RealWorldController\n{\n    #[Rou]\n    public function index(): void {}\n}\n";
        let items = real_world_php_attribute_completions(php, attribute_source, "Rou");
        let labels = real_world_completion_labels(&items);
        check_contains!(labels, "Route".to_owned());
        check_contains!(labels, "Cache".to_owned());
        if php
            .find_class("Symfony\\Component\\Security\\Http\\Attribute\\IsGranted")
            .is_some()
        {
            check_contains!(labels, "IsGranted".to_owned());
        } else {
            check_not_contains!(labels, "IsGranted".to_owned());
        }
        let route = real_world_completion_by_label(&items, "Route");
        require_len!(route.additional_text_edits, 1);
        check_eq!(
            Some("Route('${1}')$0".to_owned()),
            route.text_edit.map(|edit| edit.new_text)
        );
        check!(
            !swlsp_symfony::resolve_container_constant(
                Some(php),
                "Shopware\\Core\\Defaults::LANGUAGE_SYSTEM"
            )
            .is_empty()
        );
        let document = new_document(
            "file:///real-world-container-constant.yaml",
            "parameters:\n  language: !php/const Shopware\\Core\\Defaults::LANGUAGE_SYSTEM\n  invalid: !php/const Shopware\\Core\\Defaults::MISSING\n",
            1,
        );
        let problems = swlsp_lsp_diagnostics::ContainerConstantAnalyzer::new(Some(php.clone()))
            .analyze(&ctx(), &document)
            .expect("analyze");
        require_len!(problems, 1);
        check_eq!("symfony.constant.missing", problems[0].id.to_string());
        let constructors = php.find_methods("Shopware\\Core\\Kernel", "__construct");
        assert!(!constructors.is_empty());
        check_ge!(constructors[0].parameters.len(), 7);
        let analyzer = swlsp_lsp_diagnostics::ServiceArgumentAnalyzer::new(
            Some(s.services.clone()),
            Some(php.clone()),
        );
        let source = "services:\n  app.wrong_loader:\n    class: Shopware\\Core\\Kernel\n  app.kernel:\n    class: Shopware\\Core\\Kernel\n    arguments:\n      $environment: test\n      $debug: false\n      $pluginLoader: '@app.wrong_loader'\n      $cacheId: test\n      $version: test\n      $connection: '@connection'\n      $projectDire: /project\n";
        let document = new_document("file:///real-world-named-argument.yaml", source, 1);
        let problems = analyzer.analyze(&ctx(), &document).expect("analyze");
        let unknown: Vec<&Problem> = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.service.named_argument.unknown")
            .collect();
        let invalid: Vec<&Problem> = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.service.argument.type")
            .collect();
        require_len!(unknown, 1);
        require_len!(invalid, 1);
        check_str!(invalid[0].message, "KernelPluginLoader");
        let start: usize = unknown[0].range.start().into();
        let end: usize = unknown[0].range.end().into();
        check_eq!("$projectDire", &source[start..end]);
        let document = new_document(
            "file:///real-world-missing-service-method.yaml",
            "services:\n  app.kernel:\n    class: Shopware\\Core\\Kernel\n    calls:\n      - [definitelyMissingMethod, []]\n",
            1,
        );
        let problems = analyzer.analyze(&ctx(), &document).expect("analyze");
        let missing: Vec<&Problem> = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.service.method.missing")
            .collect();
        require_len!(missing, 1);
        check_eq!("Missing Method", missing[0].message);
        check_eq!(
            "definitelyMissingMethod",
            payload_str(missing[0], "methodName")
        );
        check!(
            missing[0]
                .payload
                .as_ref()
                .and_then(|p| p.get("classURI"))
                .is_none()
        );
        let document = new_document(
            "file:///real-world-service-argument.php",
            "<?php\nuse Shopware\\Core\\Kernel;\nuse Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ContainerConfigurator;\n\nreturn static function (ContainerConfigurator $container): void {\n    $services = $container->services();\n    $services->set('app.wrong_loader', Kernel::class);\n    $services->set('app.kernel', Kernel::class)\n        ->arg('$pluginLoader', service('app.wrong_loader'));\n};\n",
            1,
        );
        let problems = analyzer.analyze(&ctx(), &document).expect("analyze");
        let invalid: Vec<&Problem> = problems
            .iter()
            .filter(|p| p.id.to_string() == "symfony.service.argument.type")
            .collect();
        require_len!(invalid, 1);
        check_str!(invalid[0].message, "KernelPluginLoader");
    }
}

mod twig_catalog {
    use std::collections::BTreeSet;
    use std::sync::Arc;

    use serde_json::Value;
    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;

    use super::*;

    pub(super) fn assets(f: &RealWorldWorkspaceFixture) {
        let assets = capture_workspace_assets(&f.workspace);
        check_gt!(assets.names.len(), 1000);
        check_not_contains!(assets.importmap, "twig.runtime.importmap".to_owned());
        check_contains!(assets.vite, "app".to_owned());
        check_eq!(1, assets.vite_usages.len());
        check_eq!(1, assets.vite_administration_usages.len());
        check_contains!(assets.packages, "@Administration".to_owned());
        check_contains!(assets.packages, "asset".to_owned());
        check_contains!(assets.packages, "theme".to_owned());
        check!(!assets.administration.is_empty());
        check!(!assets.theme.is_empty());
        check_ge!(assets.administration_usages.len(), 5);
        check!(!assets.html_usages.is_empty());
    }

    pub(super) fn run(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let php = &f.php;
        let s = f.workspace.services();
        let twig = s.twig.clone();
        let macros = twig.get_all_macros().expect("macros");
        assert!(!macros.is_empty());
        let functions = twig.get_all_twig_functions().expect("functions");
        let tests = twig.get_all_twig_tests().expect("tests");
        let operators = twig.get_all_twig_operators().expect("operators");
        check_ge!(operators.len(), 4);
        require_twig_operator(
            &operators,
            "===",
            "src/Core/Framework/Adapter/Twig/Extension/PhpSyntaxExtension.php",
        );
        let items = real_world_twig_completions(root, &twig, php, "{% if value  %}", "value ");
        check_contains!(real_world_completion_labels(&items), "===".to_owned());
        check_eq!(
            "Custom Twig binary operator",
            real_world_completion_by_label(&items, "===").detail
        );
        let names: BTreeSet<String> = functions
            .iter()
            .map(|function| function.name.clone())
            .collect();
        let mut deprecated = Vec::new();
        for name in &names {
            if twig
                .twig_function_deprecation(name)
                .expect("deprecation")
                .is_some()
            {
                deprecated.push(name.clone());
            }
        }
        check_ge!(deprecated.len(), 4);
        eprintln!("deprecated Twig functions: {}", deprecated.join(", "));
        check!(
            !twig
                .get_twig_function("category_url")
                .expect("function")
                .is_empty()
        );
        let category = twig
            .twig_function_deprecation("category_url")
            .expect("deprecation");
        check!(category.is_some());
        check_str!(category.unwrap_or_default(), "deprecated");
        let tags = twig.get_all_twig_tags().expect("tags");
        check_ge!(tags.len(), 10);
        require_twig_tag(&tags, "sw_extends");
        require_twig_tag(&tags, "sw_icon");
        require_twig_tag(&tags, "macro");
        let catalog = swlsp_lsp_analytics::TwigExtensionCatalogProvider::new(
            Some(twig.clone()),
            Some(php.clone()),
        )
        .catalog(
            &ctx(),
            &swlsp_lsp_analytics::TwigExtensionCatalogRequest::default(),
        )
        .expect("catalog");
        check_gt!(catalog.len(), functions.len() + tests.len() + tags.len());
        let category_entry = catalog
            .iter()
            .find(|entry| entry.ty == "function" && entry.name == "category_url");
        check!(category_entry.is_some());
        if let Some(entry) = category_entry {
            check!(entry.deprecated);
            check!(!entry.file_uri.is_empty());
            check_gt!(entry.source_line, 0);
        }
        let globals = twig.get_all_globals().expect("globals");
        if std::path::Path::new(&format!("{root}/var/cache")).exists() {
            require_twig_global(
                &globals,
                "app",
                "Shopware\\Storefront\\Framework\\Twig\\TwigAppVariable",
            );
        } else {
            eprintln!("Symfony cache absent; skipping compiled-container Twig global");
        }
        require_twig_global(&globals, "shopware", "");
        let base_references = twig
            .get_template_references(&["@Storefront/storefront/base.html.twig"])
            .expect("references");
        check_ge!(base_references.len(), 10);
        let usage_catalog = swlsp_lsp_analytics::TwigTemplateUsageCatalogProvider::new(
            root,
            Some(twig.clone()),
            Some(php.clone()),
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(s.twig_components.clone()),
        )
        .catalog(
            &ctx(),
            &swlsp_lsp_analytics::TwigTemplateUsageCatalogRequest {
                template: "@Storefront/storefront/base.html.twig".into(),
                file_glob: "src/Storefront/Resources/views/storefront/base.html.twig".into(),
                ..Default::default()
            },
        )
        .expect("usage catalog");
        if usage_catalog.len() == 1 {
            check_eq!(
                "@Storefront/storefront/base.html.twig",
                usage_catalog[0].template
            );
            check_eq!(
                1,
                usage_catalog[0].files.len(),
                "{:?}",
                usage_catalog[0].files
            );
            check_ge!(usage_catalog[0].extends.len(), 10);
        } else {
            record_failure(format!(
                "expected one usage catalog entry, got {}",
                usage_catalog.len()
            ));
        }
        let product_references = twig
            .get_template_references(&[
                "@Storefront/storefront/page/content/product-detail.html.twig",
            ])
            .expect("references");
        require_template_reference_path(
            &product_references,
            "src/Storefront/Controller/ProductController.php",
        );
        check!(
            !twig
                .find_macro("Collector/db.html.twig", "render_simple_table")
                .expect("macro")
                .is_empty()
        );
        check_ge!(
            twig.get_macro_usages("Collector/db.html.twig", "render_simple_table")
                .expect("usages")
                .len(),
            5
        );
        let inputs = twig
            .get_template_variables(&[
                "@Storefront/storefront/page/content/product-detail.html.twig",
            ])
            .expect("variables");
        require_twig_template_input(&inputs, "page");
        let variable_catalog = swlsp_lsp_analytics::TwigTemplateVariableCatalogProvider::new(
            root,
            Some(twig.clone()),
            Some(php.clone()),
            Some(s.twig_components.clone()),
        )
        .catalog(
            &ctx(),
            &swlsp_lsp_analytics::TwigTemplateVariableCatalogRequest {
                template: "@Storefront/storefront/page/content/product-detail.html.twig".into(),
                file_glob: "src/Storefront/Resources/views/storefront/page/content/product-detail.html.twig".into(),
                ..Default::default()
            },
        )
        .expect("variable catalog");
        assert!(!variable_catalog.is_empty());
        let page = variable_catalog
            .iter()
            .filter(|entry| {
                entry.template == "@Storefront/storefront/page/content/product-detail.html.twig"
            })
            .find_map(|entry| {
                entry
                    .variables
                    .iter()
                    .find(|variable| variable.name == "page")
            });
        check!(page.is_some());
        if let Some(page) = page {
            check!(page.ty != "unknown");
            check!(!page.properties.is_empty());
        }
        let blocks = twig
            .get_template_blocks(&["@Storefront/storefront/page/content/product-detail.html.twig"])
            .expect("blocks");
        require_twig_block(&blocks, "page_content");
        require_twig_block(&blocks, "base_body");
        require_twig_block(&blocks, "base_main_inner");
        let source =
            "{% sw_extends '@Storefront/storefront/page/content/product-detail.html.twig' %}\n";
        let document = new_document("file:///real-world-template-generator.html.twig", source, 1);
        let generator =
            swlsp_lsp_codeaction::TwigTemplateGeneratorProvider::new(Some(twig.clone()));
        let actions = generator.get_code_actions(
            &ctx(),
            &real_world_code_action_request(&document, "sw_extends"),
        );
        require_len!(actions, 1);
        check_eq!(
            Some("shopware.symfony.generateTwigBlocks".to_owned()),
            actions[0].command.as_ref().map(|c| c.command.clone())
        );
        let commands = CommandProvider::commands(&generator, &ctx());
        let response = commands["shopware/symfony/twig/blocks/candidates"](
            &ctx(),
            Some(&serde_json::json!({"fileUri": document.uri, "source": source})),
        )
        .expect("block candidates");
        check_eq!(
            Some("@Storefront/storefront/page/content/product-detail.html.twig"),
            response["parent"].as_str()
        );
        check!(
            response["blocks"]
                .as_array()
                .is_some_and(|b| b.iter().any(|v| v.as_str() == Some("page_content")))
        );
        let scaffolds = swlsp_lsp_scaffold::Provider::new(
            root,
            Some(php.clone()),
            Some(s.console.clone()),
            None,
        );
        let commands = CommandProvider::commands(&scaffolds, &ctx());
        let scaffold = commands
            .get(swlsp_lsp_scaffold::CREATE_SYMFONY_SCAFFOLD_COMMAND)
            .expect("scaffold command");
        let directory = format!("{root}/src/Core/Framework/Feature/Command");
        let value = scaffold(
            &ctx(),
            Some(
                &serde_json::to_value(swlsp_lsp_scaffold::Request {
                    kind: "command".into(),
                    directory_uri: file_uri(&directory),
                    name: "LspScaffoldProbe".into(),
                    ..Default::default()
                })
                .unwrap(),
            ),
        )
        .expect("scaffold");
        let response: swlsp_lsp_scaffold::Response =
            serde_json::from_value(value).expect("scaffold response");
        check_eq!(
            "Shopware\\Core\\Framework\\Feature\\Command",
            response.namespace
        );
        check_eq!("LspScaffoldProbeCommand", response.class_name);
        check_str!(response.content, "#[AsCommand(");
        check_str!(response.content, "name: 'feature:lsp_scaffold_probe'");
        check_str!(response.content, "protected function execute(");
        check!(!std::path::Path::new(&format!("{directory}/LspScaffoldProbeCommand.php")).exists());
        let value = scaffold(
            &ctx(),
            Some(
                &serde_json::to_value(swlsp_lsp_scaffold::Request {
                    kind: "services-yaml".into(),
                    directory_uri: file_uri(&format!("{root}/config")),
                    name: "lsp-scaffold-probe".into(),
                    ..Default::default()
                })
                .unwrap(),
            ),
        )
        .expect("scaffold");
        let response: swlsp_lsp_scaffold::Response =
            serde_json::from_value(value).expect("scaffold response");
        check_str!(response.content, "  Shopware\\:");
        check_str!(response.content, "resource: '../src/'");
        check!(!std::path::Path::new(&format!("{root}/config/lsp-scaffold-probe.yaml")).exists());
        // Snapshot helpers assert their navigation targets; the restore
        // section compares them with the reopened workspace.
        real_world_symbol_snapshot(&f.workspace);
        real_world_related_navigation_snapshot(root, &f.workspace, php);
        real_world_route_endpoint_snapshot(root, &f.workspace, php);
        real_world_console_command_lens_snapshot(root);
        real_world_service_navigation_snapshot(root, &f.workspace, php);
        real_world_controller_usage_snapshot(root, &f.workspace, php);
        let document = new_document(
            "file:///real-world-component-self-import.html.twig",
            "<twig:Alert>\n  {% from _self import message %}\n</twig:Alert>",
            1,
        );
        let problems =
            swlsp_lsp_diagnostics::TwigComponentAnalyzer::new(Some(s.twig_components.clone()))
                .analyze(&ctx(), &document)
                .expect("analyze");
        check_eq!(
            1,
            problems
                .iter()
                .filter(|p| p.id.to_string() == "twig.component.self_macro_import")
                .count()
        );
        real_world_twig_ux_semantic_token_snapshot(&ctx());
        let component_names = s.twig_components.names().expect("names");
        let component_catalog =
            swlsp_lsp_analytics::TwigComponentCatalogProvider::new(Some(s.twig_components.clone()))
                .catalog(
                    &ctx(),
                    &swlsp_lsp_analytics::TwigComponentCatalogRequest::default(),
                )
                .expect("component catalog");
        check_eq!(component_names.len(), component_catalog.len());
        let constant = swlsp_twig::ConstantReference {
            class: "Shopware\\Core\\Content\\Product\\ProductDefinition".into(),
            name: "TYPE_DIGITAL".into(),
            ..Default::default()
        };
        check_ge!(
            twig.get_constant_references(&constant)
                .expect("references")
                .len(),
            5
        );
        check!(
            !php.find_constants(&constant.class, &constant.name)
                .is_empty()
        );
        let class_target = swlsp_twig::PhpUsageReference {
            class: "Shopware\\Core\\Content\\Product\\SalesChannel\\SalesChannelProductEntity"
                .into(),
            ..Default::default()
        };
        check_ge!(
            twig.get_php_usage_references(&class_target)
                .expect("references")
                .len(),
            3
        );
        check_gt!(
            twig.get_extension_usages(swlsp_twig::ExtensionUsageKind::Filter, "trans")
                .expect("usages")
                .len(),
            100
        );
        check_gt!(
            twig.get_extension_usages(swlsp_twig::ExtensionUsageKind::Test, "defined")
                .expect("usages")
                .len(),
            100
        );
        let assets = capture_workspace_assets(&f.workspace);
        let app_env = s
            .environment
            .variable("APP_ENV")
            .ok()
            .flatten()
            .unwrap_or_default();
        let collect = s
            .messenger
            .message("Shopware\\Core\\System\\UsageData\\EntitySync\\CollectEntityDataMessage")
            .ok()
            .flatten()
            .unwrap_or_default();
        eprintln!(
            "cold index: {:?}, classes={}, php_constants={}, messenger_messages={}, collect_message_handlers={}, collect_message_dispatches={}, environment_variables={}, app_env_declarations={}, app_env_references={}, deprecated_services={}, assets={}, asset_packages={}, administration_package_usages={}, html_asset_usages={}, encore_entries={}, importmap_entries={}, vite_entries={}, vite_usages={}, twig_macros={}, twig_tests={}, twig_operators={}, deprecated_twig_functions={}, twig_tags={}, twig_globals={}, base_template_references={}, product_template_inputs={}, product_template_blocks={}, twig_components={}",
            f.cold_elapsed,
            php.class_symbols().len(),
            php.constant_symbols().len(),
            s.messenger.messages().map(|v| v.len()).unwrap_or_default(),
            collect.handlers().len(),
            collect.dispatches().len(),
            s.environment
                .variables()
                .map(|v| v.len())
                .unwrap_or_default(),
            app_env.declarations.len(),
            app_env.references.len(),
            s.services
                .get_all_service_definitions()
                .map(|v| v.iter().filter(|x| x.deprecated).count())
                .unwrap_or_default(),
            assets.names.len(),
            assets.packages.len(),
            assets.administration_usages.len(),
            assets.html_usages.len(),
            assets.encore.len(),
            assets.importmap.len(),
            assets.vite.len(),
            assets.vite_usages.len() + assets.vite_administration_usages.len(),
            macros.len(),
            tests.len(),
            operators.len(),
            deprecated.len(),
            tags.len(),
            globals.len(),
            base_references.len(),
            inputs.len(),
            blocks.len(),
            component_names.len(),
        );
        let revision = php.revision();
        let started = std::time::Instant::now();
        f.workspace
            .file_scanner()
            .index_all(&ctx().token().indexer_token())
            .expect("warm scan");
        eprintln!("warm no-op scan: {:?}", started.elapsed());
        check_eq!(
            revision,
            php.revision(),
            "warm scan must not republish unchanged PHP documents"
        );
    }
}

mod restore {
    use std::sync::Arc;

    use swlsp_lsp::*;
    use swlsp_lsp_diagnostics::ProblemAnalyzer;
    use swlsp_lsp_protocol as protocol;
    use swlsp_php::PhpIndex;

    use super::*;
    use crate::Workspace;

    const ASSISTANT_SOURCE: &str = "<?php\n/** @param string $route #Route */\nfunction real_world_route_assistant(string $route): void {}\n\n/** @param string $service #Service */\nfunction real_world_service_assistant(string $service): void {}\n\n/** @param string $name #Parameter */\nfunction real_world_parameter_assistant(string $name): void {}\n\n/** @param string $name #ClassInterface */\nfunction real_world_class_assistant(string $name): void {}\n\n/** @param string $entity #Entity */\nfunction real_world_entity_assistant(string $entity): void {}\n\n/** @param string $template #Template */\nfunction real_world_template_assistant(string $template): void {}\n\n/**\n * @param string $key #TranslationKey\n * @param string $domain #TranslationDomain\n */\nfunction real_world_translation_assistant(string $key, string $domain): void {}\n";

    const ROUTE: &str = "frontend.sitemap.proxy";
    const NOTIFICATION: &str = "Shopware\\Administration\\Notification\\NotificationDefinition";
    const PARAMETER: &str = "shopware.messenger.enforce_message_size";
    const CLASS: &str = "Shopware\\Core\\Kernel";
    const INTERFACE: &str = "Shopware\\Core\\Framework\\Struct\\ExtendableInterface";
    const TEMPLATE: &str = "@Storefront/storefront/base.html.twig";

    fn translation_target(workspace: &Workspace) -> Option<(String, String)> {
        let translations = &workspace.services().translations;
        for domain in translations.domains().ok()? {
            if let Some(key) = translations.keys(&domain).ok()?.first() {
                return Some((domain, key.clone()));
            }
        }
        None
    }

    /// One `assistant('<value>')` probe: the document and the offset after
    /// the value.
    fn probe(
        root: &str,
        file: &str,
        source: String,
        value: &str,
        last: bool,
    ) -> (Arc<TextDocument>, u32) {
        let document = new_document(file_uri(&format!("{root}/src/{file}")), &source, 1);
        let start = if last {
            last_offset_of(&source, value)
        } else {
            offset_of(&source, value)
        };
        (document, (start + value.len()) as u32)
    }

    fn edit_text(item: &protocol::CompletionItem) -> Option<String> {
        item.text_edit.as_ref().map(|edit| edit.new_text.clone())
    }

    pub(super) fn assistants(f: &RealWorldWorkspaceFixture) {
        let root = f.root.as_str();
        let php = &f.php;
        let s = f.workspace.services();
        swlsp_indexer::Indexer::index(
            php,
            &swlsp_indexer::ParsedFile::new(
                format!("{root}/.shopware-lsp/virtual_route_assistant.php"),
                ASSISTANT_SOURCE.as_bytes().to_vec(),
            ),
        )
        .expect("index virtual assistant definitions");
        let (document, offset) = probe(
            root,
            "RouteAssistantUsage.php",
            format!("<?php real_world_route_assistant('{ROUTE}');"),
            ROUTE,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let items =
            swlsp_lsp_completion::RouteCompletionProvider::new(s.routes.clone(), Some(php.clone()))
                .get_completions(&ctx(), &request);
        check_eq!(
            Some(ROUTE.to_owned()),
            edit_text(&real_world_completion_by_label(&items, ROUTE))
        );
        // The PHP index is passed through the request context.
        let locations = swlsp_lsp_definition::RouteDefinitionProvider::new(
            Some(s.routes.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        check_eq!(
            1,
            locations.len(),
            "route assistant definitions {locations:?}"
        );
        let notification = s
            .services
            .get_service_by_id(NOTIFICATION)
            .expect("service")
            .expect("notification service");
        let (document, offset) = probe(
            root,
            "ServiceAssistantUsage.php",
            format!("<?php real_world_service_assistant('{NOTIFICATION}');"),
            NOTIFICATION,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let items = swlsp_lsp_completion::ServiceCompletionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_completions(&ctx(), &request);
        let item = real_world_completion_by_label(&items, NOTIFICATION);
        check_eq!(Some(NOTIFICATION.to_owned()), edit_text(&item));
        check!(item.deprecated);
        let locations = swlsp_lsp_definition::ServiceDefinitionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        require_len!(locations, 1);
        check_eq!(file_uri(&notification.path), locations[0].uri);
        let parameter = s
            .services
            .get_parameter_by_name(PARAMETER)
            .expect("parameter")
            .unwrap_or_default();
        let (document, offset) = probe(
            root,
            "ParameterAssistantUsage.php",
            format!("<?php real_world_parameter_assistant('{PARAMETER}');"),
            PARAMETER,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let items = swlsp_lsp_completion::ServiceCompletionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_completions(&ctx(), &request);
        check_eq!(
            Some(PARAMETER.to_owned()),
            edit_text(&real_world_completion_by_label(&items, PARAMETER))
        );
        let locations = swlsp_lsp_definition::ServiceDefinitionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        require_len!(locations, 1);
        check_eq!(file_uri(&parameter.path), locations[0].uri);
        let (document, offset) = probe(
            root,
            "ClassAssistantUsage.php",
            format!("<?php real_world_class_assistant('{CLASS}');"),
            CLASS,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let semantic = swlsp_lsp_phpsemantic::Provider::new(Some(php.clone()));
        let items = semantic.get_completions(&ctx(), &request);
        check_eq!(
            Some(CLASS.to_owned()),
            edit_text(&real_world_completion_by_label(&items, CLASS))
        );
        real_world_completion_by_label(&items, INTERFACE);
        let locations = semantic.get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        require_len!(locations, 1);
        check_eq!(
            file_uri(&format!("{root}/src/Core/Kernel.php")),
            locations[0].uri
        );
        let (document, offset) = probe(
            root,
            "TemplateAssistantUsage.php",
            format!("<?php real_world_template_assistant('{TEMPLATE}');"),
            TEMPLATE,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let items = swlsp_lsp_completion::TwigCompletionProvider::new(
            root,
            Some(s.twig.clone()),
            None,
            Some(php.clone()),
        )
        .get_completions(&ctx(), &request);
        check_eq!(
            Some(TEMPLATE.to_owned()),
            edit_text(&real_world_completion_by_label(&items, TEMPLATE))
        );
        let locations = swlsp_lsp_definition::TwigDefinitionProvider::new(
            root,
            Some(s.twig.clone()),
            None,
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        check!(!locations.is_empty());
        let (domain, key) = translation_target(&f.workspace).expect("translation domain and key");
        let (document, offset) = probe(
            root,
            "TranslationAssistantUsage.php",
            format!("<?php real_world_translation_assistant(domain: '{domain}', key: '{key}');"),
            &key,
            true,
        );
        let request = real_world_completion_request(&document, offset);
        let items = swlsp_lsp_completion::TranslationCompletionProvider::new(
            Some(s.translations.clone()),
            Some(php.clone()),
        )
        .get_completions(&ctx(), &request);
        check_eq!(
            Some(key.clone()),
            edit_text(&real_world_completion_by_label(&items, &key))
        );
        let locations = swlsp_lsp_definition::TranslationDefinitionProvider::new(
            Some(s.translations.clone()),
            Some(php.clone()),
        )
        .get_definition(
            &ctx(),
            &real_world_definition_request(&document, request.syntax.node.clone(), offset),
        );
        check!(!locations.is_empty());
        let typo = format!("{ROUTE}x");
        let document = new_document(
            file_uri(&format!("{root}/src/RouteAssistantDiagnostic.php")),
            &format!("<?php real_world_route_assistant('{typo}');"),
            1,
        );
        let problems = swlsp_lsp_diagnostics::RouteAnalyzer::new(
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_real_world_suggestion_diagnostic(
            &document,
            &problems,
            "symfony.route.missing",
            &typo,
            ROUTE,
        );
        let typo = format!("{NOTIFICATION}x");
        let document = new_document(
            file_uri(&format!("{root}/src/ServiceAssistantDiagnostic.php")),
            &format!("<?php real_world_service_assistant('{typo}');"),
            1,
        );
        let problems = swlsp_lsp_diagnostics::ServiceAnalyzer::new(
            Some(s.services.clone()),
            Some(php.clone()),
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_real_world_suggestion_diagnostic(
            &document,
            &problems,
            "symfony.service.missing",
            &typo,
            NOTIFICATION,
        );
        let typo = format!("{key}x");
        let document = new_document(
            file_uri(&format!("{root}/src/TranslationAssistantDiagnostic.php")),
            &format!("<?php real_world_translation_assistant(domain: '{domain}', key: '{typo}');"),
            1,
        );
        let problems = swlsp_lsp_diagnostics::TranslationAnalyzer::new(
            Some(s.translations.clone()),
            Some(php.clone()),
            None,
        )
        .analyze(&ctx(), &document)
        .expect("analyze");
        require_real_world_suggestion_diagnostic(
            &document,
            &problems,
            "symfony.translation.key.missing",
            &typo,
            &key,
        );
    }

    type Probes = Vec<(String, String)>;

    fn push<T: std::fmt::Debug>(probes: &mut Probes, label: &str, value: T) {
        probes.push((label.to_owned(), format!("{value:#?}")));
    }

    /// Every value compared between the cold and the restored workspace,
    /// computed against `workspace` (Debug-rendered for comparison).
    pub(super) fn probes(
        f: &RealWorldWorkspaceFixture,
        workspace: &Workspace,
        php: &PhpIndex,
    ) -> Probes {
        let root = f.root.as_str();
        let s = workspace.services();
        let admin = workspace_admin_index(workspace);
        let mut p = Probes::new();
        push(&mut p, "php.class_symbols.len", php.class_symbols().len());
        // The restored HTTP client options are compared with the cold count
        // and re-checks `timeout`.
        push(
            &mut p,
            "httpclient.options",
            swlsp_httpclient::options(php)
                .into_iter()
                .map(|option| (option.name, option.ty.to_string(), option.default))
                .collect::<Vec<_>>(),
        );
        push(
            &mut p,
            "admin.components.len",
            admin.get_all_components().map(|c| c.len()),
        );
        let data_grid = admin
            .get_effective_component("sw-data-grid")
            .ok()
            .flatten()
            .unwrap_or_default();
        push(
            &mut p,
            "admin.data_grid.row_click",
            data_grid
                .component_events()
                .into_iter()
                .find(|e| swlsp_admin::canonical_event_name(&e.name) == "row-click"),
        );
        push(
            &mut p,
            "admin.data_grid.column_name",
            data_grid.component_slot("column-name").cloned(),
        );
        push(
            &mut p,
            "admin.data_grid.router_block",
            data_grid
                .component_block("sw_data_grid_columns_render_router_link")
                .cloned(),
        );
        let effective = |name: &str| {
            admin
                .get_effective_component(name)
                .ok()
                .flatten()
                .unwrap_or_default()
        };
        push(
            &mut p,
            "admin.sw_card.deprecated",
            effective("sw-card").deprecated,
        );
        push(
            &mut p,
            "admin.entity_listing.items",
            effective("sw-entity-listing")
                .component_prop("items")
                .map(|prop| prop.deprecated.clone()),
        );
        push(
            &mut p,
            "admin.deprecated_block",
            effective("sw-newsletter-recipient-filter-switch")
                .component_block("sw_newsletter_recipient_filter_switch_field")
                .cloned(),
        );
        push(
            &mut p,
            "admin.deprecated_member",
            effective("sw-settings-snippet-set-list").template_member("getNoPermissionsTooltip"),
        );
        push(
            &mut p,
            "admin.mt_button.icon_front",
            effective("mt-button")
                .slots
                .iter()
                .find(|slot| slot.name == "iconFront")
                .cloned(),
        );
        let references = swlsp_lsp_reference::AdminReferenceProvider::new(Some(admin.clone()));
        let cms_list = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-cms/page/sw-cms-list/sw-cms-list.html.twig"
        );
        let cms_source = read(&cms_list);
        let cms_document = new_document(file_uri(&cms_list), &cms_source, 1);
        let click = (offset_of(&cms_source, "@item-click") + 2) as u32;
        push(
            &mut p,
            "admin.event_references",
            references
                .get_references(
                    &ctx(),
                    &real_world_reference_request(&cms_document, click, true),
                )
                .map_err(|e| e.to_string()),
        );
        let login = format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-login/view/sw-login-login/sw-login-login.html.twig"
        );
        let login_source = read(&login);
        let login_document = new_document(file_uri(&login), &login_source, 1);
        let slot = (offset_of(&login_source, "#iconFront") + 2) as u32;
        push(
            &mut p,
            "admin.slot_references",
            references
                .get_references(
                    &ctx(),
                    &real_world_reference_request(&login_document, slot, true),
                )
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "admin.event_rename",
            swlsp_lsp_refactor::AdminRenameProvider::new(Some(admin.clone()))
                .rename(
                    &ctx(),
                    &real_world_rename_request(&cms_document, click, "item-selected"),
                )
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "admin.services.len",
            admin.get_all_services().map(|v| v.len()),
        );
        push(
            &mut p,
            "admin.filters.len",
            admin.get_all_filters().map(|v| v.len()),
        );
        push(
            &mut p,
            "admin.stores.len",
            admin.get_all_stores().map(|v| v.len()),
        );
        push(
            &mut p,
            "admin.privileges.len",
            admin.get_all_privileges().map(|v| v.len()),
        );
        push(
            &mut p,
            "admin.modules.len",
            admin.get_all_modules().map(|v| v.len()),
        );
        for (kind, name) in [
            (swlsp_admin::AdminSymbolKind::Component, "sw-page"),
            (swlsp_admin::AdminSymbolKind::Service, "acl"),
            (swlsp_admin::AdminSymbolKind::Store, "session"),
            (swlsp_admin::AdminSymbolKind::Privilege, "product.viewer"),
            (
                swlsp_admin::AdminSymbolKind::ModuleRoute,
                "sw.product.detail",
            ),
            (
                swlsp_admin::AdminSymbolKind::Module,
                "sw-settings-message-stats",
            ),
        ] {
            push(
                &mut p,
                &format!("admin.usages.{name}"),
                admin
                    .get_usages(kind, "", name)
                    .map(|sets| admin_usage_occurrence_count(&sets)),
            );
        }
        let container = format!(
            "{root}/src/Administration/Resources/app/administration/src/application-container-real-world.ts"
        );
        let api = admin
            .resolve_shopware_context("api", &container)
            .unwrap_or_default();
        push(
            &mut p,
            "admin.context.api.languageId",
            api.members
                .iter()
                .find(|m| m.name == "languageId")
                .map(|m| (m.definition_path.clone(), m.definition_range.identifier)),
        );
        let format = admin
            .resolve_shopware_utils("format", &container)
            .unwrap_or_default();
        push(
            &mut p,
            "admin.utils.format.date",
            format.members.iter().find(|m| m.name == "date").map(|m| {
                (
                    m.type_.clone(),
                    m.definition_path.clone(),
                    m.definition_range.identifier,
                )
            }),
        );
        push(
            &mut p,
            "dal.definitions.len",
            s.dal.definitions().map(|v| v.len()),
        );
        push(
            &mut p,
            "app_script.hooks.len",
            s.app_scripts.hooks().map(|v| v.len()),
        );
        push(
            &mut p,
            "app_script.facades.len",
            s.app_scripts.facades().map(|v| v.len()),
        );
        let attribute = "<?php\nnamespace App\\Controller;\nclass RealWorldController\n{\n    #[Rou]\n    public function index(): void {}\n}\n";
        push(
            &mut p,
            "php.attribute_completions",
            real_world_completion_labels(&real_world_php_attribute_completions(
                php, attribute, "Rou",
            )),
        );
        push(
            &mut p,
            "twig.loop_completions",
            real_world_completion_labels(&real_world_twig_completions(
                root,
                &s.twig,
                php,
                "{% for entry in entries %}{{ loop. }}{% endfor %}",
                "loop.",
            )),
        );
        push(
            &mut p,
            "twig.operator_completions",
            real_world_completion_labels(&real_world_twig_completions(
                root,
                &s.twig,
                php,
                "{% if value  %}",
                "value ",
            )),
        );
        push(
            &mut p,
            "symfony.container_constant",
            swlsp_symfony::resolve_container_constant(
                Some(php),
                "Shopware\\Core\\Defaults::LANGUAGE_SYSTEM",
            )
            .len(),
        );
        let helpers = swlsp_console::helpers(php);
        push(
            &mut p,
            "console.helpers",
            helpers
                .iter()
                .map(|h| (h.name.clone(), h.class.clone()))
                .collect::<Vec<_>>(),
        );
        push(
            &mut p,
            "php.twig_variables",
            php.twig_template_variables(&[
                "@Storefront/storefront/page/content/product-detail.html.twig",
            ])
            .map_err(|e| e.to_string()),
        );
        let missing = new_document(
            "file:///real-world-missing-member.twig",
            "{# @var context \\Shopware\\Core\\Framework\\Context #}\n{{ context.versionId }}\n{{ context.SYSTEM_SCOPE }}\n{{ context.definitelyMissing }}",
            1,
        );
        push(
            &mut p,
            "twig.missing_member",
            swlsp_lsp_diagnostics::TwigMemberMissingAnalyzer::new(
                Some(s.twig.clone()),
                Some(php.clone()),
            )
            .analyze(&ctx(), &missing)
            .map(|problems| {
                problems
                    .iter()
                    .map(|pr| (pr.id.to_string(), pr.range, pr.message.clone()))
                    .collect::<Vec<_>>()
            })
            .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "console.system_config_get",
            s.console
                .get_command("system:config:get")
                .map_err(|e| e.to_string()),
        );
        let request = swlsp_console::CatalogRequest {
            query: "system:config:get".into(),
            file_glob: "src/**/Command/*.php".into(),
            ..Default::default()
        };
        push(
            &mut p,
            "console.catalog",
            swlsp_console::CatalogProvider::new(s.console.clone(), Some(root))
                .catalog_with_request(&ctx(), &request)
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "console.aliases",
            s.console
                .get_command("snippets:validate")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "event.cart_merged",
            s.events
                .event("Shopware\\Core\\Checkout\\Cart\\Event\\CartMergedEvent")
                .map(|e| e.map(|e| e.listeners()))
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "messenger.messages.len",
            s.messenger.messages().map(|v| v.len()),
        );
        push(
            &mut p,
            "messenger.collect",
            s.messenger
                .message("Shopware\\Core\\System\\UsageData\\EntitySync\\CollectEntityDataMessage")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "environment.variables.len",
            s.environment.variables().map(|v| v.len()),
        );
        push(
            &mut p,
            "environment.app_env",
            s.environment.variable("APP_ENV").map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "services.len",
            s.services.get_all_service_definitions().map(|v| v.len()),
        );
        let authoring =
            swlsp_lsp_completion::YamlServiceAuthoringCompletionProvider::new(php.project());
        let services_yaml = file_uri(&format!("{root}/config/services.yaml"));
        for source in [
            "services:\n  app.real_world:\n    aut",
            "services:\n  app.real_world:\n    arguments: [!tag",
        ] {
            let document = new_document(services_yaml.clone(), source, 1);
            push(
                &mut p,
                &format!("yaml.services {source:?}"),
                authoring.get_completions(
                    &ctx(),
                    &real_world_completion_request(&document, source.len() as u32),
                ),
            );
        }
        let routes_yaml = file_uri(&format!("{root}/config/routes.yaml"));
        let route_authoring =
            swlsp_lsp_completion::YamlRouteAuthoringCompletionProvider::new(Some(s.routes.clone()));
        for source in [
            "real_world.catalog:\n  path: /real-world/{id}\n  meth",
            "real_world.catalog:\n  path: /real-world/{id}/{slug}\n  requirements:\n    sl",
            "real_world.draft:\n  path: /site",
        ] {
            let document = new_document(routes_yaml.clone(), source, 1);
            push(
                &mut p,
                &format!("yaml.routes {source:?}"),
                route_authoring.get_completions(
                    &ctx(),
                    &real_world_completion_request(&document, source.len() as u32),
                ),
            );
        }
        let notification = s.services.get_service_by_id(NOTIFICATION).ok().flatten();
        push(&mut p, "services.notification", &notification);
        let identifier = notification
            .as_ref()
            .map(|n| n.id.clone())
            .unwrap_or_default();
        push(
            &mut p,
            "analytics.service_locator",
            swlsp_lsp_analytics::ServiceLocatorProvider::new(
                Some(s.services.clone()),
                Some(php.clone()),
            )
            .locate(
                &ctx(),
                &swlsp_lsp_analytics::ServiceLocatorRequest {
                    identifier,
                    ..Default::default()
                },
            )
            .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "codeaction.collect",
            swlsp_lsp_codeaction::SymfonyGeneratorProvider::new(
                Some(php.clone()),
                Some(s.services.clone()),
            )
            .collect_service_definitions(
                &ctx(),
                &swlsp_lsp_codeaction::SymfonyServiceDefinitionCollectionRequest {
                    class_names: notification
                        .as_ref()
                        .map(|n| n.class.clone())
                        .unwrap_or_default(),
                    output: "yaml".into(),
                    class_as_id: true,
                    ..Default::default()
                },
            )
            .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "symfonyconfig.names",
            s.configuration.names().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "symfonyconfig.roots",
            s.configuration.roots("shopware").map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "serializer.classes",
            s.serializer.classes().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "routes.sitemap",
            s.routes
                .find_routes_by_path("/sitemap/shop.xml.gz")
                .map_err(|e| e.to_string()),
        );
        let route_catalog = swlsp_lsp_analytics::RouteCatalogProvider::new(
            root,
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(php.clone()),
            Some(s.twig.clone()),
        );
        push(
            &mut p,
            "analytics.routes.all",
            route_catalog
                .catalog(&ctx(), &Default::default())
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "analytics.routes.home",
            route_catalog
                .catalog(
                    &ctx(),
                    &swlsp_lsp_analytics::RouteCatalogRequest {
                        route_name: "frontend.home.page".into(),
                        file_glob: "src/**/NavigationController.php".into(),
                        ..Default::default()
                    },
                )
                .map_err(|e| e.to_string()),
        );
        let route_provider =
            swlsp_lsp_completion::RouteCompletionProvider::new(s.routes.clone(), Some(php.clone()));
        for (file, source, needle) in [
            (
                "DraftController.php",
                "<?php\nuse Symfony\\Component\\Routing\\Attribute\\Route;\n\n#[Route('/site')]\nfunction draft(): void {}\n",
                "/site",
            ),
            (
                "LegacyController.php",
                "<?php\n/** @Route(path=\"/site\") */\nfunction draft(): void {}\n",
                "/site",
            ),
            (
                "CatalogController.php",
                "<?php\nnamespace Shopware\\Core\\Controller;\n\nclass CatalogController\n{\n    #[Route(path: '/catalog', name: 'dr')]\n    public function detailAction(): void {}\n}\n",
                "'dr",
            ),
            (
                "LegacyCatalogController.php",
                "<?php\nnamespace Shopware\\Core\\Controller;\n\nclass CatalogController\n{\n    /** @Route(path=\"/catalog\", name=\"dr\") */\n    public function detailAction(): void {}\n}\n",
                "\"dr",
            ),
        ] {
            let document = new_document(file_uri(&format!("{root}/src/{file}")), source, 1);
            push(
                &mut p,
                &format!("routes.php_completion {file}"),
                route_provider.get_completions(
                    &ctx(),
                    &real_world_completion_request(
                        &document,
                        (offset_of(source, needle) + needle.len()) as u32,
                    ),
                ),
            );
        }
        // Assistants (the virtual definitions were persisted by the cold run).
        let (document, offset) = probe(
            root,
            "RouteAssistantUsage.php",
            format!("<?php real_world_route_assistant('{ROUTE}');"),
            ROUTE,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        push(
            &mut p,
            "assistant.route.completions",
            route_provider.get_completions(&ctx(), &request),
        );
        push(
            &mut p,
            "assistant.route.definition",
            swlsp_lsp_definition::RouteDefinitionProvider::new(
                Some(s.routes.clone()),
                Some(php.clone()),
            )
            .get_definition(
                &ctx(),
                &real_world_definition_request(&document, request.syntax.node.clone(), offset),
            ),
        );
        let service_completion = swlsp_lsp_completion::ServiceCompletionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        );
        let service_definition = swlsp_lsp_definition::ServiceDefinitionProvider::new(
            Some(s.services.clone()),
            Some(php.clone()),
        );
        for (file, function, value) in [
            (
                "ServiceAssistantUsage.php",
                "real_world_service_assistant",
                NOTIFICATION,
            ),
            (
                "ParameterAssistantUsage.php",
                "real_world_parameter_assistant",
                PARAMETER,
            ),
        ] {
            let (document, offset) = probe(
                root,
                file,
                format!("<?php {function}('{value}');"),
                value,
                false,
            );
            let request = real_world_completion_request(&document, offset);
            push(
                &mut p,
                &format!("assistant.{function}.completions"),
                service_completion.get_completions(&ctx(), &request),
            );
            push(
                &mut p,
                &format!("assistant.{function}.definition"),
                service_definition.get_definition(
                    &ctx(),
                    &real_world_definition_request(&document, request.syntax.node.clone(), offset),
                ),
            );
        }
        let (document, offset) = probe(
            root,
            "ClassAssistantUsage.php",
            format!("<?php real_world_class_assistant('{CLASS}');"),
            CLASS,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        let semantic = swlsp_lsp_phpsemantic::Provider::new(Some(php.clone()));
        push(
            &mut p,
            "assistant.class.completions",
            semantic.get_completions(&ctx(), &request),
        );
        push(
            &mut p,
            "assistant.class.definition",
            semantic.get_definition(
                &ctx(),
                &real_world_definition_request(&document, request.syntax.node.clone(), offset),
            ),
        );
        let (document, offset) = probe(
            root,
            "TemplateAssistantUsage.php",
            format!("<?php real_world_template_assistant('{TEMPLATE}');"),
            TEMPLATE,
            false,
        );
        let request = real_world_completion_request(&document, offset);
        push(
            &mut p,
            "assistant.template.completions",
            swlsp_lsp_completion::TwigCompletionProvider::new(
                root,
                Some(s.twig.clone()),
                None,
                Some(php.clone()),
            )
            .get_completions(&ctx(), &request),
        );
        push(
            &mut p,
            "assistant.template.definition",
            swlsp_lsp_definition::TwigDefinitionProvider::new(
                root,
                Some(s.twig.clone()),
                None,
                Some(php.clone()),
            )
            .get_definition(
                &ctx(),
                &real_world_definition_request(&document, request.syntax.node.clone(), offset),
            ),
        );
        if let Some((domain, key)) = translation_target(workspace) {
            let (document, offset) = probe(
                root,
                "TranslationAssistantUsage.php",
                format!(
                    "<?php real_world_translation_assistant(domain: '{domain}', key: '{key}');"
                ),
                &key,
                true,
            );
            let request = real_world_completion_request(&document, offset);
            push(
                &mut p,
                "assistant.translation.completions",
                swlsp_lsp_completion::TranslationCompletionProvider::new(
                    Some(s.translations.clone()),
                    Some(php.clone()),
                )
                .get_completions(&ctx(), &request),
            );
            push(
                &mut p,
                "assistant.translation.definition",
                swlsp_lsp_definition::TranslationDefinitionProvider::new(
                    Some(s.translations.clone()),
                    Some(php.clone()),
                )
                .get_definition(
                    &ctx(),
                    &real_world_definition_request(&document, request.syntax.node.clone(), offset),
                ),
            );
            let typo = format!("{key}x");
            let document = new_document(
                file_uri(&format!("{root}/src/TranslationAssistantDiagnostic.php")),
                &format!(
                    "<?php real_world_translation_assistant(domain: '{domain}', key: '{typo}');"
                ),
                1,
            );
            push(
                &mut p,
                "assistant.translation.diagnostics",
                swlsp_lsp_diagnostics::TranslationAnalyzer::new(
                    Some(s.translations.clone()),
                    Some(php.clone()),
                    None,
                )
                .analyze(&ctx(), &document)
                .map(|v| {
                    v.iter()
                        .map(|pr| {
                            (
                                pr.id.to_string(),
                                pr.range,
                                pr.message.clone(),
                                pr.payload.clone(),
                            )
                        })
                        .collect::<Vec<_>>()
                })
                .map_err(|e| e.to_string()),
            );
        }
        let document = new_document(
            file_uri(&format!("{root}/src/RouteAssistantDiagnostic.php")),
            &format!("<?php real_world_route_assistant('{ROUTE}x');"),
            1,
        );
        let route_analyzer = swlsp_lsp_diagnostics::RouteAnalyzer::new(
            Some(s.routes.clone()),
            Some(s.services.clone()),
            Some(php.clone()),
        );
        push(
            &mut p,
            "assistant.route.diagnostics",
            route_analyzer
                .analyze(&ctx(), &document)
                .map(|v| {
                    v.iter()
                        .map(|pr| {
                            (
                                pr.id.to_string(),
                                pr.range,
                                pr.message.clone(),
                                pr.payload.clone(),
                            )
                        })
                        .collect::<Vec<_>>()
                })
                .map_err(|e| e.to_string()),
        );
        let document = new_document(
            file_uri(&format!("{root}/src/ServiceAssistantDiagnostic.php")),
            &format!("<?php real_world_service_assistant('{NOTIFICATION}x');"),
            1,
        );
        push(
            &mut p,
            "assistant.service.diagnostics",
            swlsp_lsp_diagnostics::ServiceAnalyzer::new(
                Some(s.services.clone()),
                Some(php.clone()),
            )
            .analyze(&ctx(), &document)
            .map(|v| {
                v.iter()
                    .map(|pr| {
                        (
                            pr.id.to_string(),
                            pr.range,
                            pr.message.clone(),
                            pr.payload.clone(),
                        )
                    })
                    .collect::<Vec<_>>()
            })
            .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "routes.absolute",
            s.routes
                .find_routes_by_path("https://shopware.test/sitemap/shop.xml.gz?preview=1#details")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "routes.prefix",
            s.routes
                .find_routes_by_path_prefix(
                    "/store-api/checkout/cart/line-item",
                    "/store-api/checkout/cart/line-item/delete",
                )
                .map_err(|e| e.to_string()),
        );
        let import_path = format!("{root}/src/Core/Framework/Resources/config/routes_dev.php");
        let import_source = read(&import_path);
        let import_document = new_document(file_uri(&import_path), &import_source, 1);
        let bundle_offset = (offset_of(&import_source, "@FrameworkBundle") + 3) as u32;
        push(
            &mut p,
            "routes.bundle_definition",
            swlsp_lsp_definition::RouteDefinitionProvider::new(
                Some(s.routes.clone()),
                Some(php.clone()),
            )
            .get_definition(
                &ctx(),
                &real_world_definition_request_at(&import_document, bundle_offset),
            ),
        );
        let mut params = protocol::CompletionParams::default();
        params.text_document.uri = import_document.uri.clone();
        params.position = position(&import_document, bundle_offset);
        push(
            &mut p,
            "routes.bundle_completions",
            swlsp_lsp_completion::BundleResourceCompletionProvider::new(Some(php.clone()), None)
                .get_completions(
                    &ctx(),
                    &CompletionRequest {
                        params,
                        syntax: syntax(
                            &import_document,
                            node_at(&import_document, bundle_offset),
                            None,
                        ),
                    },
                ),
        );
        let source = "{% if app.request.attributes.get('_route') in ['frontend.sitemap.proxy'] %}{% endif %}";
        let document = new_document("file:///real-world-route-comparison.twig", source, 1);
        push(
            &mut p,
            "routes.comparison_diagnostics",
            route_analyzer
                .analyze(&ctx(), &document)
                .map(|v| v.len())
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "routes.comparison_definition",
            swlsp_lsp_definition::RouteDefinitionProvider::new(Some(s.routes.clone()), None)
                .get_definition(
                    &ctx(),
                    &real_world_definition_request_at(
                        &document,
                        (offset_of(source, "frontend.sitemap.proxy") + 3) as u32,
                    ),
                ),
        );
        push(&mut p, "assets", capture_workspace_assets(workspace));
        let twig = &s.twig;
        push(
            &mut p,
            "twig.macros",
            twig.get_all_macros().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.functions",
            twig.get_all_twig_functions().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.tests",
            twig.get_all_twig_tests().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.operators",
            twig.get_all_twig_operators().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.category_url",
            twig.get_twig_function("category_url")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.tags",
            twig.get_all_twig_tags().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.extension_catalog",
            swlsp_lsp_analytics::TwigExtensionCatalogProvider::new(
                Some(twig.clone()),
                Some(php.clone()),
            )
            .catalog(&ctx(), &Default::default())
            .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.globals",
            twig.get_all_globals().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.base_references",
            twig.get_template_references(&["@Storefront/storefront/base.html.twig"])
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.base_usage_catalog",
            swlsp_lsp_analytics::TwigTemplateUsageCatalogProvider::new(
                root,
                Some(twig.clone()),
                Some(php.clone()),
                Some(s.routes.clone()),
                Some(s.services.clone()),
                Some(s.twig_components.clone()),
            )
            .catalog(
                &ctx(),
                &swlsp_lsp_analytics::TwigTemplateUsageCatalogRequest {
                    template: "@Storefront/storefront/base.html.twig".into(),
                    file_glob: "src/Storefront/Resources/views/storefront/base.html.twig".into(),
                    ..Default::default()
                },
            )
            .map_err(|e| e.to_string()),
        );
        let product = "@Storefront/storefront/page/content/product-detail.html.twig";
        push(
            &mut p,
            "twig.product_references",
            twig.get_template_references(&[product])
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.profiler_macro_usages",
            twig.get_macro_usages("Collector/db.html.twig", "render_simple_table")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.product_inputs",
            twig.get_template_variables(&[product])
                .map_err(|e| e.to_string()),
        );
        push(&mut p, "twig.product_variable_catalog", swlsp_lsp_analytics::TwigTemplateVariableCatalogProvider::new(root, Some(twig.clone()), Some(php.clone()), Some(s.twig_components.clone())).catalog(&ctx(), &swlsp_lsp_analytics::TwigTemplateVariableCatalogRequest { template: product.into(), file_glob: "src/Storefront/Resources/views/storefront/page/content/product-detail.html.twig".into(), ..Default::default() }).map_err(|e| e.to_string()));
        push(
            &mut p,
            "twig.product_blocks",
            twig.get_template_blocks(&[product])
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "snapshot.workspace_symbols",
            real_world_symbol_snapshot(workspace),
        );
        push(
            &mut p,
            "snapshot.related_navigation",
            real_world_related_navigation_snapshot(root, workspace, php),
        );
        push(
            &mut p,
            "snapshot.route_endpoints",
            real_world_route_endpoint_snapshot(root, workspace, php),
        );
        push(
            &mut p,
            "snapshot.console_lenses",
            real_world_console_command_lens_snapshot(root),
        );
        push(
            &mut p,
            "snapshot.service_navigation",
            real_world_service_navigation_snapshot(root, workspace, php),
        );
        push(
            &mut p,
            "snapshot.controller_usages",
            real_world_controller_usage_snapshot(root, workspace, php),
        );
        push(
            &mut p,
            "twigcomponent.names",
            s.twig_components.names().map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twigcomponent.catalog",
            swlsp_lsp_analytics::TwigComponentCatalogProvider::new(Some(s.twig_components.clone()))
                .catalog(&ctx(), &Default::default())
                .map_err(|e| e.to_string()),
        );
        let constant = swlsp_twig::ConstantReference {
            class: "Shopware\\Core\\Content\\Product\\ProductDefinition".into(),
            name: "TYPE_DIGITAL".into(),
            ..Default::default()
        };
        push(
            &mut p,
            "twig.constant_references",
            twig.get_constant_references(&constant)
                .map_err(|e| e.to_string()),
        );
        let class_target = swlsp_twig::PhpUsageReference {
            class: "Shopware\\Core\\Content\\Product\\SalesChannel\\SalesChannelProductEntity"
                .into(),
            ..Default::default()
        };
        push(
            &mut p,
            "twig.php_usage_references",
            twig.get_php_usage_references(&class_target)
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.trans_usages",
            twig.get_extension_usages(swlsp_twig::ExtensionUsageKind::Filter, "trans")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.defined_usages",
            twig.get_extension_usages(swlsp_twig::ExtensionUsageKind::Test, "defined")
                .map_err(|e| e.to_string()),
        );
        push(
            &mut p,
            "twig.ux_tokens",
            real_world_twig_ux_semantic_token_snapshot(&ctx()),
        );
        p
    }

    pub(super) fn restore(f: &RealWorldWorkspaceFixture, cold: Option<&Probes>) {
        let root = f.root.as_str();
        let cold = cold.expect("cold probes failed");
        f.workspace.close_workspace().expect("close cold workspace");
        let started = std::time::Instant::now();
        let (reopened, restored_php, _server) =
            super::super::open_real_world_workspace_with_server(root, f.cache.path());
        let elapsed = started.elapsed();
        let admin = workspace_admin_index(&reopened);
        check!(restored_php.find_class("Shopware\\Core\\Kernel").is_some());
        let role = super::admin::effective(&admin, "sw-users-permissions-role-view-general");
        check_contains!(role.data, "mcpIntegrations".to_owned());
        check_contains!(role.injected, "repositoryFactory".to_owned());
        let mixins = admin.get_mixin("validation").expect("mixin");
        assert!(!mixins.is_empty());
        check_contains!(mixins[0].definition.methods, "validateRule".to_owned());
        check!(
            !admin
                .get_privilege("product.viewer")
                .expect("privilege")
                .is_empty()
        );
        for (store, member) in [
            ("swProfile", "setMinSearchTermLength"),
            ("session", "setCurrentUser"),
            ("context", "app"),
        ] {
            let stores = admin.get_store(store).expect("store");
            assert!(!stores.is_empty());
            check!(stores[0].member(member).is_some(), "{store}.{member}");
        }
        let container = format!(
            "{root}/src/Administration/Resources/app/administration/src/application-container-real-world.ts"
        );
        check!(
            admin
                .resolve_shopware_context("api", &container)
                .expect("context")
                .complete
        );
        check!(
            admin
                .resolve_shopware_utils("format", &container)
                .expect("utils")
                .complete
        );
        let variables = restored_php
            .twig_template_variables(&[
                "@Storefront/storefront/page/content/product-detail.html.twig",
            ])
            .expect("variables");
        require_twig_variable(&variables, "page");
        let restored = probes(f, &reopened, &restored_php);
        check_eq!(cold.len(), restored.len());
        for ((label, before), (_, after)) in cold.iter().zip(restored.iter()) {
            if before != after {
                record_failure(format!(
                    "restored {label} differs:\ncold: {}\nrestored: {}",
                    truncate(before),
                    truncate(after)
                ));
            }
        }
        eprintln!("cache restore: {elapsed:?}");
        reopened
            .close_workspace()
            .expect("close restored workspace");
    }

    fn truncate(value: &str) -> String {
        if value.len() <= 2000 {
            value.to_owned()
        } else {
            format!(
                "{}… ({} bytes)",
                &value[..value
                    .char_indices()
                    .nth(2000)
                    .map(|(i, _)| i)
                    .unwrap_or(value.len())],
                value.len()
            )
        }
    }
}
