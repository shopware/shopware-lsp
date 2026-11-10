//! Tests of the i18n, Twig migration, static-prop, missing-prop and slot fixes.

use std::sync::Arc;

use swlsp_lsp::{Context, Inspection, TextDocument};
use swlsp_shopware::ResolvedVersion;
use swlsp_uriutil::file_uri as uri;

use super::fixes::{
    ADD_ADMIN_PROP_FIX_ID, BIND_ADMIN_PROP_FIX_ID, MIGRATE_ADMIN_I18N_TC_FIX_ID,
    MIGRATE_ADMIN_SLOT_FIX_ID, MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID,
};
use super::new_admin_inspections;
use super::tests::{Fixture, vue_component};
use crate::inspections::test_support::{check_diagnostics, check_fix, fix_context};

#[allow(clippy::field_reassign_with_default)]
fn known_version(major: i64, minor: i64, patch: i64) -> ResolvedVersion {
    let mut version = ResolvedVersion::default();
    version.known = true;
    version.version.major = major;
    version.version.minor = minor;
    version.version.patch = patch;
    version
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
    for (file, before, expected) in [
        (
            "component.js",
            "this.{|admin.vue-i18n.tc-deprecated:$tc|}('translation.key', count);",
            "this.$t('translation.key', count);",
        ),
        (
            "component.html.twig",
            "{{ {|admin.vue-i18n.tc-deprecated:$tc|}( 'translation.key' ) }}",
            "{{ $t( 'translation.key' ) }}",
        ),
    ] {
        let uri = uri(&format!("{admin_root}/{file}"));
        let (document, problems) = check_diagnostics(inspection.as_ref(), &uri, before);
        assert_eq!(problems[0].fixes.len(), 1);
        assert_eq!(problems[0].fixes[0].id.0, MIGRATE_ADMIN_I18N_TC_FIX_ID);
        check_fix(
            inspection.as_ref(),
            &uri,
            before,
            MIGRATE_ADMIN_I18N_TC_FIX_ID,
            expected,
        );
        assert_eq!(reparse_errors(&document, expected), 0);
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
    let uri = uri(&fixture.path("src/Resources/app/administration/src/component.html.twig"));
    // No `admin.component.deprecated` problem: the migration owns the tag.
    let before = r#"<{|admin.twig.migration.text-field:sw-text-field|} :value="computedName"><template #label>{{ $t('name') }}</template></sw-text-field>"#;
    let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
        new_admin_inspections(Some(fixture.index.clone()), known_version(6, 7, 0)),
    ));
    let (document, problems) = check_diagnostics(inspection.as_ref(), &uri, before);
    assert_eq!(problems[0].fixes.len(), 1);
    assert_eq!(
        problems[0].fixes[0].id.0,
        MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID
    );
    let expected =
        r#"<mt-text-field :model-value="computedName" :label="$t('name')"></mt-text-field>"#;
    check_fix(
        inspection.as_ref(),
        &uri,
        before,
        MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID,
        expected,
    );
    assert_eq!(reparse_errors(&document, expected), 0);
}

#[test]
fn admin_twig_migration_inspection_reports_unsafe_without_fix() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: fixture.path("sw-card.ts"),
        deprecated: "tag:v6.8.0 - Will be removed, use mt-card instead.".into(),
    });
    let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
        new_admin_inspections(Some(fixture.index.clone()), known_version(6, 7, 0)),
    ));
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        &uri(&fixture.path("src/Resources/app/administration/src/component.html.twig")),
        "<{|admin.twig.migration.card:sw-card|} aiBadge>Content</sw-card>",
    );
    assert!(problems[0].fixes.is_empty());
}

#[test]
fn admin_twig_migration_inspection_owns_missing_legacy_component() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "unrelated-component".into(),
        file_path: fixture.path("component.ts"),
    });
    let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
        new_admin_inspections(Some(fixture.index.clone()), known_version(6, 8, 0)),
    ));
    // No `admin.component.not-found` problem: the migration owns the tag.
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        &uri(&fixture.path("src/Resources/app/administration/src/component.html.twig")),
        "<{|admin.twig.migration.loader:sw-loader|} />",
    );
    assert_eq!(problems[0].message, "sw-loader is removed; use mt-loader");
}

#[test]
fn admin_twig_migration_inspection_follows_the_registered_component() {
    let fixture = Fixture::new();
    // Shopware keeps replaced components as compatibility wrappers: some
    // carry a deprecation note, some do not.
    fixture.save(vue_component! {
        name: "sw-switch-field".into(),
        file_path: fixture.path("sw-switch-field.ts"),
        deprecated: "tag:v6.8.0 - Will be removed, use mt-switch instead.".into(),
    });
    fixture.save(vue_component! {
        name: "sw-loader".into(),
        file_path: fixture.path("sw-loader.ts"),
    });
    let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
        new_admin_inspections(Some(fixture.index.clone()), known_version(6, 7, 0)),
    ));
    let uri = uri(&fixture.path("src/Resources/app/administration/src/component.html.twig"));

    // Deprecated and still registered: a warning that says so, and no
    // second `admin.component.deprecated` problem.
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        &uri,
        "<{|admin.twig.migration.switch-field:sw-switch-field|} />",
    );
    assert_eq!(
        problems[0].message,
        "sw-switch-field is deprecated (tag:v6.8.0 - Will be removed, use mt-switch instead); \
         use mt-switch and review props, events, and slots"
    );
    assert_eq!(
        problems[0].severity,
        Some(swlsp_lsp_protocol::DiagnosticSeverity::WARNING)
    );

    // Registered without a deprecation note: nothing to report.
    check_diagnostics(inspection.as_ref(), &uri, "<sw-loader />");
}

#[test]
fn admin_twig_migration_inspection_waits_for_the_index() {
    // An empty index is no evidence that a component is gone.
    let fixture = Fixture::new();
    let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
        new_admin_inspections(Some(fixture.index.clone()), known_version(6, 7, 0)),
    ));
    check_diagnostics(
        inspection.as_ref(),
        &uri(&fixture.path("src/Resources/app/administration/src/component.html.twig")),
        "<sw-loader /><sw-switch-field />",
    );
}

#[test]
fn admin_twig_migration_inspection_requires_known_target() {
    let fixture = Fixture::new();
    let uri = uri(&fixture.path("src/Resources/app/administration/src/component.html.twig"));
    for version in [ResolvedVersion::default(), known_version(6, 6, 9)] {
        let inspection: Arc<dyn Inspection> = Arc::new(crate::inspections::test_support::Combined(
            new_admin_inspections(Some(fixture.index.clone()), version),
        ));
        check_diagnostics(inspection.as_ref(), &uri, "<sw-loader />");
    }
}

#[test]
fn admin_static_prop_fix_binds_the_attribute() {
    let fixture = Fixture::new();
    fixture.index_file(
        &fixture.path("src/Resources/app/administration/src/component/sw-field/index.js"),
        "Component.register('sw-field', {\n    props: { count: Number },\n});",
    );
    let inspection = fixture.inspection();
    let uri = uri(&fixture.path("src/Resources/app/administration/src/view.html.twig"));
    let before = r#"<sw-field {|admin.component.static-prop-type:count="5"|}></sw-field>"#;
    let (_, problems) = check_diagnostics(inspection.as_ref(), &uri, before);
    assert_eq!(problems[0].fixes.len(), 1);
    assert_eq!(problems[0].fixes[0].id.0, BIND_ADMIN_PROP_FIX_ID);
    let presentation = check_fix(
        inspection.as_ref(),
        &uri,
        before,
        BIND_ADMIN_PROP_FIX_ID,
        r#"<sw-field :count="5"></sw-field>"#,
    );
    assert_eq!(presentation.title, "Bind 'count' as a Vue expression");
}

#[test]
fn admin_missing_prop_fix_is_a_snippet_command() {
    let fixture = Fixture::new();
    fixture.index_file(
        &fixture.path("src/Resources/app/administration/src/component/sw-button/index.js"),
        "Component.register('sw-button', {\n    props: { isActive: { type: Boolean, required: true } },\n});",
    );
    let inspection = fixture.inspection();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &uri(&fixture.path("src/Resources/app/administration/src/view.html.twig")),
        "<{|admin.component.missing-required-prop:sw-button|}></sw-button>",
    );
    let problem = &problems[0];
    assert_eq!(problem.fixes[0].id.0, ADD_ADMIN_PROP_FIX_ID);
    let fix = inspection
        .quick_fixes()
        .into_iter()
        .find(|fix| fix.id().0 == ADD_ADMIN_PROP_FIX_ID)
        .unwrap();
    let context = fix_context(&document, problem, &problem.fixes[0], None);
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
    let uri = uri(&fixture.path("Resources/app/administration/component.html.twig"));
    let before = r#"{|admin.slot-syntax-deprecated:<template slot="actions" slot-scope="{ item }">|}<button/></template>"#;
    let (_, problems) = check_diagnostics(inspection.as_ref(), &uri, before);
    assert_eq!(problems[0].fixes[0].id.0, MIGRATE_ADMIN_SLOT_FIX_ID);
    check_fix(
        inspection.as_ref(),
        &uri,
        before,
        MIGRATE_ADMIN_SLOT_FIX_ID,
        r#"<template #actions="{ item }"><button/></template>"#,
    );
}
