//! Port of Go `admin_markup_audit_test.go`: a development audit for typed
//! Administration markup diagnostics. It reports rather than fails on
//! findings so a changing local checkout stays usable as a discovery fixture.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_admin::{
    AdminCMSRegistrationKind, AdminComponentIndexer, VueComponent, is_component_tag,
    normalize_prop_name, static_component_name_for_tag, twig_directive_references,
    twig_dynamic_component_selector, vue_object_binding_fields, vue_prop_allowed_values,
};
use swlsp_lsp::{Context, TextDocument};
use swlsp_lsp_diagnostics::{AdminAnalyzer, ProblemAnalyzer};
use swlsp_parser_twig::ast::AstNode;
use swlsp_parser_twig::{ast as twigast, query as twigquery, syntax as twigsyntax};

use super::open_real_world_workspace_with_server;

/// Files below `root` whose extension equals `extension` ignoring ASCII
/// case, in lexical walk order.
pub(super) fn walk_files(root: &Path, extension: &str) -> Vec<PathBuf> {
    let mut result = Vec::new();
    let mut entries: Vec<PathBuf> = std::fs::read_dir(root)
        .unwrap_or_else(|error| panic!("{}: {error}", root.display()))
        .flatten()
        .map(|entry| entry.path())
        .collect();
    entries.sort();
    for path in entries {
        if path.is_dir() {
            result.extend(walk_files(&path, extension));
        } else if path
            .extension()
            .is_some_and(|value| value.to_string_lossy().eq_ignore_ascii_case(extension))
        {
            result.push(path);
        }
    }
    result
}

/// Go `topAdminAuditCounts`, formatted like Go's `%v` of a string slice.
fn top_admin_audit_counts(counts: &HashMap<String, usize>, limit: usize) -> String {
    let mut entries: Vec<(&String, &usize)> = counts.iter().collect();
    entries.sort_by(|left, right| right.1.cmp(left.1).then_with(|| left.0.cmp(right.0)));
    let entries = entries
        .into_iter()
        .take(limit)
        .map(|(name, count)| format!("{name}={count}"))
        .collect::<Vec<_>>();
    format!("[{}]", entries.join(" "))
}

#[derive(Default)]
struct Counters {
    templates: usize,
    findings: usize,
    typed_findings: usize,
    invalid_prop_value_findings: usize,
    missing_components: usize,
    files_with_findings: usize,
    dynamic_selectors: usize,
    complete_selectors: usize,
    static_candidates: usize,
    resolved_candidates: usize,
    single_contract_selectors: usize,
    inferred_selectors: usize,
    complete_inferred_selectors: usize,
    inferred_candidates: usize,
    inferred_contracts: usize,
    object_bindings: usize,
    complete_object_bindings: usize,
    object_fields: usize,
    contract_object_fields: usize,
    native_object_fields: usize,
    unresolved_object_contract_fields: usize,
    unknown_object_prop_fields: usize,
    partial_dynamic_object_fields: usize,
    unresolved_object_fields_by_tag: HashMap<String, usize>,
    unknown_object_props: HashMap<String, usize>,
    directive_usages: usize,
    resolved_directive_usages: usize,
    unknown_directive_usages: HashMap<String, usize>,
    directive_findings: usize,
}

#[test]
fn shopware_trunk_admin_markup_audit() {
    let root = crate::real_world_root_or_skip!();
    let cache = tempfile::tempdir().unwrap();
    let ctx = Context::background();
    let (workspace, _php, _server) = open_real_world_workspace_with_server(&root, cache.path());
    workspace
        .file_scanner()
        .index_all(&ctx.token().indexer_token())
        .unwrap();
    let admin: Arc<AdminComponentIndexer> = workspace
        .services()
        .admin
        .clone()
        .expect("administration index");
    let analyzer = AdminAnalyzer::new(Some(admin.clone()));

    let mut allowed_value_props = 0;
    let mut complete_allowed_value_props = 0;
    let mut allowed_value_candidates = 0;
    let mut effective_components = 0;
    let mut indexed_props = 0;
    let mut typed_props = 0;
    let mut required_props = 0;
    let mut source_owned_props = 0;
    let mut exact_prop_ranges = 0;
    for name in admin.get_all_component_names().unwrap() {
        let Some(component) = admin.get_effective_component(&name).unwrap() else {
            continue;
        };
        effective_components += 1;
        for prop in &component.props {
            indexed_props += 1;
            if !prop.type_.trim().is_empty() {
                typed_props += 1;
            }
            if prop.required {
                required_props += 1;
            }
            if !prop.file_path.is_empty() {
                source_owned_props += 1;
            }
            if prop.name_range.declaration {
                exact_prop_ranges += 1;
            }
            let (values, complete) = vue_prop_allowed_values(prop);
            if values.is_empty() {
                continue;
            }
            allowed_value_props += 1;
            allowed_value_candidates += values.len();
            if complete {
                complete_allowed_value_props += 1;
            }
        }
    }
    for (component_name, prop_name, expected) in [
        ("sw-label", "variant", &["primary", "neutral-reversed"][..]),
        ("mt-button", "variant", &["primary", "action"][..]),
        ("mt-button", "size", &["x-small", "large"][..]),
    ] {
        let component = admin
            .get_effective_component(component_name)
            .unwrap()
            .unwrap_or_else(|| panic!("{component_name}"));
        let prop = component
            .component_prop(prop_name)
            .unwrap_or_else(|| panic!("{component_name}.{prop_name}"));
        assert!(!prop.file_path.is_empty(), "{component_name}.{prop_name}");
        assert!(
            prop.name_range.declaration,
            "{component_name}.{prop_name} must retain its exact declaration range"
        );
        let (values, complete) = vue_prop_allowed_values(prop);
        assert!(complete, "{component_name}.{prop_name}");
        for value in expected {
            assert!(
                values.iter().any(|candidate| candidate == value),
                "{component_name}.{prop_name}: {value} not in {values:?}"
            );
        }
    }
    let directives = admin.get_all_directives().unwrap();
    let known_directives: HashSet<String> = directives
        .iter()
        .map(|directive| directive.name.clone())
        .collect();
    for name in ["tooltip", "draggable", "droppable", "autofocus"] {
        assert!(known_directives.contains(name), "directive {name}");
    }
    let cms_elements = admin
        .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)
        .unwrap();
    assert!(cms_elements.len() >= 20, "{}", cms_elements.len());
    let cms_blocks = admin
        .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Block)
        .unwrap();
    assert!(cms_blocks.len() >= 30, "{}", cms_blocks.len());
    let cms_component_links = cms_elements
        .iter()
        .chain(&cms_blocks)
        .flat_map(|registration| {
            [
                &registration.component,
                &registration.config_component,
                &registration.preview_component,
            ]
        })
        .filter(|name| !name.is_empty())
        .count();

    let administration_root =
        Path::new(&root).join("src/Administration/Resources/app/administration/src");
    let mut c = Counters::default();
    for path in walk_files(&administration_root, "twig") {
        audit_template(&ctx, &root, &path, &admin, &analyzer, &mut c);
    }

    eprintln!(
        "Administration CMS registries: elements={} blocks={} component_links={cms_component_links}",
        cms_elements.len(),
        cms_blocks.len()
    );
    eprintln!(
        "Administration markup audit: templates={} files_with_findings={} typed_findings={} missing_components={}",
        c.templates, c.files_with_findings, c.typed_findings, c.missing_components
    );
    eprintln!(
        "Administration prop index: components={effective_components} props={indexed_props} typed={typed_props} required={required_props} source_owned={source_owned_props} exact_ranges={exact_prop_ranges}"
    );
    eprintln!(
        "Administration prop value contracts: props={allowed_value_props} complete={complete_allowed_value_props} candidates={allowed_value_candidates} invalid_usages={}",
        c.invalid_prop_value_findings
    );
    eprintln!(
        "Administration dynamic components: selectors={} complete={} static_candidates={} resolved_candidates={} single_contracts={}",
        c.dynamic_selectors,
        c.complete_selectors,
        c.static_candidates,
        c.resolved_candidates,
        c.single_contract_selectors
    );
    eprintln!(
        "Administration inferred dynamic components: selectors={} complete={} candidates={} resolved_contracts={}",
        c.inferred_selectors,
        c.complete_inferred_selectors,
        c.inferred_candidates,
        c.inferred_contracts
    );
    eprintln!(
        "Administration object v-bind: bindings={} complete={} fields={} contract_fields={}",
        c.object_bindings, c.complete_object_bindings, c.object_fields, c.contract_object_fields
    );
    eprintln!(
        "Administration object v-bind classification: native_fields={} unresolved_contract_fields={} unknown_prop_fields={} partial_dynamic_fields={}",
        c.native_object_fields,
        c.unresolved_object_contract_fields,
        c.unknown_object_prop_fields,
        c.partial_dynamic_object_fields
    );
    eprintln!(
        "Administration unresolved object-binding tags: {}",
        top_admin_audit_counts(&c.unresolved_object_fields_by_tag, 20)
    );
    eprintln!(
        "Administration unknown object-binding props: {}",
        top_admin_audit_counts(&c.unknown_object_props, 30)
    );
    eprintln!(
        "Administration custom directives: declarations={} usages={} resolved={} typo_findings={} unknown={}",
        known_directives.len(),
        c.directive_usages,
        c.resolved_directive_usages,
        c.directive_findings,
        top_admin_audit_counts(&c.unknown_directive_usages, 30)
    );
    assert!(c.dynamic_selectors > 0);
    assert!(c.complete_selectors > 0);
    assert!(c.resolved_candidates > 0);
    assert!(c.inferred_selectors > 0);
    assert!(c.complete_inferred_selectors > 0);
    assert!(c.inferred_contracts > 0);
    assert!(c.object_bindings > 0);
    assert!(c.object_fields > 0);
    assert!(allowed_value_props > 0);
    assert!(complete_allowed_value_props > 0);
    assert!(!known_directives.is_empty());
    assert!(c.directive_usages > 0);
    assert!(c.resolved_directive_usages > 0);
    assert!(cms_component_links > 0);
    workspace.close_workspace().unwrap();
}

fn audit_template(
    ctx: &Context,
    root: &str,
    path: &Path,
    admin: &AdminComponentIndexer,
    analyzer: &AdminAnalyzer,
    c: &mut Counters,
) {
    c.templates += 1;
    let path_text = path.to_string_lossy().into_owned();
    let source = std::fs::read_to_string(path).unwrap();
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&path_text),
        &source,
        1,
    ));
    let tree_root = document.root().expect("Twig syntax tree");
    let template_directives: HashSet<String> = admin
        .get_all_directives_for_template(&path_text)
        .unwrap()
        .into_iter()
        .map(|directive| directive.name)
        .collect();
    for directive in twig_directive_references(&tree_root) {
        c.directive_usages += 1;
        if template_directives.contains(&directive.name) {
            c.resolved_directive_usages += 1;
        } else {
            *c.unknown_directive_usages
                .entry(directive.name)
                .or_default() += 1;
        }
    }
    for start_tag in twigquery::nodes(&tree_root, &[twigsyntax::HTML_STARTING_TAG]) {
        let selector = twig_dynamic_component_selector(&start_tag);
        let dynamic = selector.is_some();
        let selector = selector.unwrap_or_default();
        let mut bound_fields = Vec::new();
        if let Some(tag) = twigast::HtmlStartingTag::cast(start_tag.clone()) {
            for attribute in tag.attributes() {
                if twigquery::html_attribute_name(attribute.syntax()).as_deref() != Some("v-bind") {
                    continue;
                }
                let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                    continue;
                };
                let (fields, complete) = vue_object_binding_fields(
                    &inner.syntax().text().to_string(),
                    u32::from(inner.syntax().text_range().start()),
                );
                if fields.is_empty() {
                    continue;
                }
                c.object_bindings += 1;
                c.object_fields += fields.len();
                if complete {
                    c.complete_object_bindings += 1;
                }
                bound_fields.extend(fields);
            }
        }
        if !bound_fields.is_empty() {
            let mut contracts: Vec<VueComponent> = Vec::new();
            let tag_name = twigquery::html_tag_name(&start_tag).unwrap_or_default();
            if dynamic {
                let (_, resolved, complete) = admin
                    .resolve_dynamic_component_contracts(&path_text, &selector, Some(&start_tag))
                    .unwrap();
                if complete {
                    contracts = resolved;
                }
            } else if let Some(name) = static_component_name_for_tag(&start_tag)
                && let Some(component) = admin
                    .get_component_for_template_tag(&path_text, &name, None)
                    .unwrap()
            {
                contracts = vec![component];
            }
            if contracts.is_empty() {
                if dynamic || is_component_tag(&tag_name) {
                    c.unresolved_object_contract_fields += bound_fields.len();
                    *c.unresolved_object_fields_by_tag
                        .entry(tag_name.clone())
                        .or_default() += bound_fields.len();
                    if dynamic {
                        let names: Vec<&str> = bound_fields
                            .iter()
                            .map(|field| field.name.as_str())
                            .collect();
                        eprintln!(
                            "unresolved dynamic object binding: {} selector={:?} fields={}",
                            path_text
                                .strip_prefix(&format!("{root}/"))
                                .unwrap_or(&path_text),
                            selector.expression,
                            names.join(",")
                        );
                    }
                } else {
                    c.native_object_fields += bound_fields.len();
                }
            }
            for field in &bound_fields {
                let known_contracts = contracts
                    .iter()
                    .filter(|contract| {
                        contract.props.iter().any(|prop| {
                            normalize_prop_name(&prop.name) == normalize_prop_name(&field.name)
                        })
                    })
                    .count();
                if !contracts.is_empty() && known_contracts == contracts.len() {
                    c.contract_object_fields += 1;
                } else if known_contracts > 0 {
                    c.partial_dynamic_object_fields += 1;
                } else if !contracts.is_empty() {
                    c.unknown_object_prop_fields += 1;
                    *c.unknown_object_props
                        .entry(format!("{tag_name}.{}", field.name))
                        .or_default() += 1;
                }
            }
        }
        if !dynamic {
            continue;
        }
        c.dynamic_selectors += 1;
        if selector.complete {
            c.complete_selectors += 1;
        }
        c.static_candidates += selector.candidates.len();
        let mut resolved_in_selector = 0;
        for candidate in &selector.candidates {
            if admin
                .get_component_for_template_tag(&path_text, &candidate.name, None)
                .unwrap()
                .is_some()
            {
                c.resolved_candidates += 1;
                resolved_in_selector += 1;
            }
        }
        if selector.complete && selector.names().len() == 1 && resolved_in_selector > 0 {
            c.single_contract_selectors += 1;
        }
        let (resolved_selector, inferred) = admin
            .resolve_dynamic_component_selector(&path_text, &selector, Some(&start_tag))
            .unwrap();
        if inferred && !selector.complete && !resolved_selector.names().is_empty() {
            c.inferred_selectors += 1;
            c.inferred_candidates += resolved_selector.names().len();
            if resolved_selector.complete {
                c.complete_inferred_selectors += 1;
            }
            let (_, components, contract_complete) = admin
                .resolve_dynamic_component_contracts(&path_text, &selector, Some(&start_tag))
                .unwrap();
            if contract_complete {
                c.inferred_contracts += components.len();
            }
        }
    }
    let problems = analyzer.analyze(ctx, &document).unwrap();
    let mut file_has_finding = false;
    for problem in problems {
        match problem.id.as_str() {
            "admin.component.bound-prop-type"
            | "admin.component.model-not-assignable"
            | "admin.component.model-type"
            | "admin.component.unknown-vue-member"
            | "admin.component.unknown-slot-prop" => c.typed_findings += 1,
            "admin.component.invalid-prop-value" => {
                c.typed_findings += 1;
                c.invalid_prop_value_findings += 1;
            }
            "admin.component.not-found" => c.missing_components += 1,
            "admin.directive.not-found" => c.directive_findings += 1,
            _ => continue,
        }
        c.findings += 1;
        file_has_finding = true;
        if c.findings > 100 {
            continue;
        }
        let relative = path_text
            .strip_prefix(&format!("{root}/"))
            .unwrap_or(&path_text);
        let start = u32::from(problem.range.start());
        let (line, _) = document.line_index().position_utf16(start);
        let mut root_type = String::new();
        let mut receiver_type = String::new();
        if let Some(resolved) = admin
            .resolve_twig_vue_member(&tree_root, document.source(), start, &path_text)
            .unwrap()
        {
            root_type = resolved.binding.type_;
            receiver_type = resolved.receiver_type;
        } else if let Some(resolved) = admin
            .resolve_twig_vue_instance_member(&tree_root, document.source(), start, &path_text)
            .unwrap()
        {
            root_type = resolved.root_member.type_;
            receiver_type = resolved.receiver_type;
        }
        eprintln!(
            "{relative}:{}: {} ({}; root={root_type:?} receiver={receiver_type:?})",
            line + 1,
            problem.message,
            problem.id,
        );
    }
    if file_has_finding {
        c.files_with_findings += 1;
    }
}
