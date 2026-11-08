//! Ports of `admin_i18n_test.go` and `admin_twig_migration_test.go`, plus
//! the static-prop, missing-prop and slot fixes.

use std::sync::Arc;

use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_shopware::ResolvedVersion;

use super::fixes::{
    ADD_ADMIN_PROP_FIX_ID, BIND_ADMIN_PROP_FIX_ID, MIGRATE_ADMIN_I18N_TC_FIX_ID,
    MIGRATE_ADMIN_SLOT_FIX_ID, MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID,
};
use super::new_admin;
use super::tests::{Fixture, open, vue_component};
use crate::test_support::{apply_single, fix_context, inspect, quick_fix_with_id};

#[allow(clippy::field_reassign_with_default)]
fn known_version(major: i64, minor: i64, patch: i64) -> ResolvedVersion {
    let mut version = ResolvedVersion::default();
    version.known = true;
    version.version.major = major;
    version.version.minor = minor;
    version.version.patch = patch;
    version
}

fn problem_with_id<'a>(problems: &'a [Problem], id: &str) -> &'a Problem {
    problems
        .iter()
        .find(|problem| problem.id.0 == id)
        .unwrap_or_else(|| panic!("problem {id} not found in {problems:?}"))
}

fn reparse_errors(document: &TextDocument, updated: &str) -> usize {
    TextDocument::new(document.uri.clone(), updated, 2)
        .parse_errors()
        .len()
}

#[test]
fn admin_i18n_tc_inspection_builds_lossless_callee_rewrite() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    let inspection = fixture.inspection();
    for (file, source, expected) in [
        (
            "component.js",
            "this.$tc('translation.key', count);",
            "this.$t('translation.key', count);",
        ),
        (
            "component.html.twig",
            "{{ $tc( 'translation.key' ) }}",
            "{{ $t( 'translation.key' ) }}",
        ),
    ] {
        let document = open(&format!("{admin_root}/{file}"), source);
        let problems = inspect(inspection.as_ref(), &document);
        let problem = problem_with_id(&problems, "admin.vue-i18n.tc-deprecated");
        assert_eq!(problem.fixes.len(), 1);
        assert_eq!(problem.fixes[0].id.0, MIGRATE_ADMIN_I18N_TC_FIX_ID);
        let fix = quick_fix_with_id(inspection.as_ref(), MIGRATE_ADMIN_I18N_TC_FIX_ID);
        let updated = apply_single(
            &fix,
            &fix_context(&document, problem, &problem.fixes[0], None),
        );
        assert_eq!(updated, expected);
        assert_eq!(reparse_errors(&document, &updated), 0);
    }
}

#[test]
fn admin_twig_migration_inspection_builds_versioned_fix() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "sw-text-field".into(),
        file_path: fixture.path("sw-text-field.ts"),
        deprecated: "Use mt-text-field instead.".into(),
    });
    let document = open(
        &fixture.path("src/Resources/app/administration/src/component.html.twig"),
        r#"<sw-text-field :value="computedName"><template #label>{{ $t('name') }}</template></sw-text-field>"#,
    );
    let inspection = new_admin(Some(fixture.index.clone()), known_version(6, 7, 0));
    let problems = inspect(inspection.as_ref(), &document);
    let problem = problem_with_id(&problems, "admin.twig.migration.text-field");
    assert!(
        problems
            .iter()
            .all(|candidate| candidate.id.0 != "admin.component.deprecated")
    );
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.0, MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID);
    let fix = quick_fix_with_id(inspection.as_ref(), MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID);
    let updated = apply_single(
        &fix,
        &fix_context(&document, problem, &problem.fixes[0], None),
    );
    assert_eq!(
        updated,
        r#"<mt-text-field :model-value="computedName" :label="$t('name')"></mt-text-field>"#
    );
    assert_eq!(reparse_errors(&document, &updated), 0);
}

#[test]
fn admin_twig_migration_inspection_reports_unsafe_without_fix() {
    let fixture = Fixture::new();
    let document = open(
        &fixture.path("src/Resources/app/administration/src/component.html.twig"),
        "<sw-card aiBadge>Content</sw-card>",
    );
    let inspection = new_admin(Some(fixture.index.clone()), known_version(6, 7, 0));
    let problems = inspect(inspection.as_ref(), &document);
    assert!(
        problem_with_id(&problems, "admin.twig.migration.card")
            .fixes
            .is_empty()
    );
}

#[test]
fn admin_twig_migration_inspection_owns_missing_legacy_component() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "unrelated-component".into(),
        file_path: fixture.path("component.ts"),
    });
    let document = open(
        &fixture.path("src/Resources/app/administration/src/component.html.twig"),
        "<sw-loader />",
    );
    let inspection = new_admin(Some(fixture.index.clone()), known_version(6, 8, 0));
    let problems = inspect(inspection.as_ref(), &document);
    problem_with_id(&problems, "admin.twig.migration.loader");
    assert!(
        problems
            .iter()
            .all(|problem| problem.id.0 != "admin.component.not-found")
    );
}

#[test]
fn admin_twig_migration_inspection_requires_known_target() {
    let fixture = Fixture::new();
    let document = open(
        &fixture.path("src/Resources/app/administration/src/component.html.twig"),
        "<sw-loader />",
    );
    for version in [ResolvedVersion::default(), known_version(6, 6, 9)] {
        let inspection = new_admin(Some(fixture.index.clone()), version);
        let problems = inspect(inspection.as_ref(), &document);
        assert!(
            problems
                .iter()
                .all(|problem| problem.id.0 != "admin.twig.migration.loader")
        );
    }
}

fn single_problem(
    inspection: &dyn swlsp_lsp::Inspection,
    document: &Arc<TextDocument>,
    id: &str,
) -> Problem {
    let problems = inspect(inspection, document);
    problem_with_id(&problems, id).clone()
}

#[test]
fn admin_static_prop_fix_binds_the_attribute() {
    let fixture = Fixture::new();
    fixture.index_file(
        &fixture.path("src/Resources/app/administration/src/component/sw-field/index.js"),
        "Component.register('sw-field', {\n    props: { count: Number },\n});",
    );
    let inspection = fixture.inspection();
    let document = open(
        &fixture.path("src/Resources/app/administration/src/view.html.twig"),
        r#"<sw-field count="5"></sw-field>"#,
    );
    let problem = single_problem(
        inspection.as_ref(),
        &document,
        "admin.component.static-prop-type",
    );
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.0, BIND_ADMIN_PROP_FIX_ID);
    let fix = quick_fix_with_id(inspection.as_ref(), BIND_ADMIN_PROP_FIX_ID);
    let context = fix_context(&document, &problem, &problem.fixes[0], None);
    let presentation = fix
        .present(&Context::background(), &context)
        .unwrap()
        .unwrap();
    assert_eq!(presentation.title, "Bind 'count' as a Vue expression");
    assert_eq!(
        apply_single(&fix, &context),
        r#"<sw-field :count="5"></sw-field>"#
    );
}

#[test]
fn admin_missing_prop_fix_is_a_snippet_command() {
    let fixture = Fixture::new();
    fixture.index_file(
        &fixture.path("src/Resources/app/administration/src/component/sw-button/index.js"),
        "Component.register('sw-button', {\n    props: { isActive: { type: Boolean, required: true } },\n});",
    );
    let inspection = fixture.inspection();
    let document = open(
        &fixture.path("src/Resources/app/administration/src/view.html.twig"),
        "<sw-button></sw-button>",
    );
    let problem = single_problem(
        inspection.as_ref(),
        &document,
        "admin.component.missing-required-prop",
    );
    assert_eq!(problem.fixes[0].id.0, ADD_ADMIN_PROP_FIX_ID);
    let fix = inspection
        .quick_fixes()
        .into_iter()
        .find(|fix| fix.id().0 == ADD_ADMIN_PROP_FIX_ID)
        .unwrap();
    let context = fix_context(&document, &problem, &problem.fixes[0], None);
    let presentation = fix
        .present(&Context::background(), &context)
        .unwrap()
        .unwrap();
    assert_eq!(presentation.title, "Add missing prop 'isActive'");
    let command = fix
        .as_command()
        .unwrap()
        .build_command(&Context::background(), &context)
        .unwrap()
        .unwrap();
    assert_eq!(command.command, "shopware.editor.insertSnippetAtPosition");
    assert_eq!(
        command.arguments,
        vec![
            serde_json::json!(document.uri),
            serde_json::json!(0),
            serde_json::json!(10),
            serde_json::json!(" :is-active=\"false$0\""),
        ]
    );
}

#[test]
fn admin_slot_migration_fix_replaces_the_template_tag() {
    let fixture = Fixture::new();
    let inspection = fixture.inspection();
    let document = open(
        &fixture.path("Resources/app/administration/component.html.twig"),
        r#"<template slot="actions" slot-scope="{ item }"><button/></template>"#,
    );
    let problem = single_problem(
        inspection.as_ref(),
        &document,
        "admin.slot-syntax-deprecated",
    );
    assert_eq!(problem.fixes[0].id.0, MIGRATE_ADMIN_SLOT_FIX_ID);
    let fix = quick_fix_with_id(inspection.as_ref(), MIGRATE_ADMIN_SLOT_FIX_ID);
    assert_eq!(
        apply_single(
            &fix,
            &fix_context(&document, &problem, &problem.fixes[0], None)
        ),
        r#"<template #actions="{ item }"><button/></template>"#
    );
}
