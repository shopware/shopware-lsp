//! Ports of `admin_block_override_test.go`, `admin_i18n_deprecation_test.go`
//! and `admin_slot_migration_test.go`.

use std::sync::Arc;

use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_syntax::{SyntaxElement, TextRange};

use super::*;
use crate::ProblemAnalyzer;
use crate::test_support::diagnostics_document;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

fn block_override_document(name: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&format!(
            "/project/src/Resources/app/administration/src/{name}"
        )),
        source,
        1,
    ))
}

fn analyze(analyzer: &dyn ProblemAnalyzer, document: &Arc<TextDocument>) -> Vec<Problem> {
    analyzer
        .analyze(&Context::background(), document)
        .expect("analyze")
}

#[test]
fn admin_block_override_analyzer_reports_unsupported_structures() {
    let source = r#"<template>
    <sw-block extends="outer">
        <div><sw-block extends="nested" /></div>
        <sw-block-parent v-if="showParent" />
        <template v-for="item in items"><sw-block-parent /></template>
    </sw-block>
    <sw-block v-if="enabled" extends="conditional" />
    <section v-for="item in items"><sw-block extends="repeated" /></section>
</template>"#;
    let problems = analyze(
        &AdminBlockOverrideAnalyzer::new(),
        &block_override_document("component.vue", source),
    );
    assert_eq!(problems.len(), 5);
    let mut expected = std::collections::HashMap::from([
        (ADMIN_BLOCK_OVERRIDE_NESTED_CODE, "sw-block"),
        (ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, "sw-block"),
        (ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, "sw-block"),
        (ADMIN_BLOCK_PARENT_CONDITIONAL_CODE, "sw-block-parent"),
        (ADMIN_BLOCK_PARENT_REPEATED_CODE, "sw-block-parent"),
    ]);
    for problem in &problems {
        let want = expected
            .remove(problem.id.as_str())
            .unwrap_or_else(|| panic!("unexpected problem {}", problem.id));
        assert_eq!(slice(source, problem.range), want);
        assert!(!problem.message.is_empty());
    }
    assert!(expected.is_empty());
}

#[test]
fn admin_block_override_analyzer_allows_static_sibling_declarations() {
    let source = r#"<template>
    <sw-block extends="first">
        <div><section><sw-block-parent /></section></div>
    </sw-block>
    <div v-show="visible"><sw-block extends="second" /></div>
    <sw-block name="ordinary"><sw-block-parent /></sw-block>
</template>"#;
    let problems = analyze(
        &AdminBlockOverrideAnalyzer::new(),
        &block_override_document("component.vue", source),
    );
    assert!(problems.is_empty());
}

#[test]
fn admin_block_override_analyzer_reports_ancestor_and_twig_control_flow() {
    let cases = [
        (
            "component.vue",
            r#"<template>
    <section v-if="enabled"><div><sw-block extends="conditional" /></div></section>
    <template v-for="item in items"><div><sw-block-parent /></div></template>
</template>"#,
        ),
        (
            "component.html.twig",
            r#"{% if enabled %}<sw-block extends="conditional" />{% endif %}
{% for item in items %}<sw-block-parent />{% endfor %}"#,
        ),
    ];
    for (file, source) in cases {
        let problems = analyze(
            &AdminBlockOverrideAnalyzer::new(),
            &block_override_document(file, source),
        );
        let ids: Vec<&str> = problems.iter().map(|problem| problem.id.as_str()).collect();
        assert_eq!(
            ids,
            [
                ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
                ADMIN_BLOCK_PARENT_REPEATED_CODE
            ],
            "{file}"
        );
    }
}

#[test]
fn admin_block_override_analyzer_reports_vue_control_directives() {
    let cases = [
        (
            r#"<sw-block v-if="enabled" extends="example" />"#,
            ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block v-else-if="enabled" extends="example" />"#,
            ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block v-else extends="example" />"#,
            ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block v-for="item in items" extends="example" />"#,
            ADMIN_BLOCK_OVERRIDE_REPEATED_CODE,
        ),
        (
            r#"<sw-block-parent v-if="enabled" />"#,
            ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block-parent v-else-if="enabled" />"#,
            ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block-parent v-else />"#,
            ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
        ),
        (
            r#"<sw-block-parent v-for="item in items" />"#,
            ADMIN_BLOCK_PARENT_REPEATED_CODE,
        ),
    ];
    for (tag, id) in cases {
        let source = format!("<template>{tag}</template>");
        let problems = analyze(
            &AdminBlockOverrideAnalyzer::new(),
            &block_override_document("component.vue", &source),
        );
        assert_eq!(problems.len(), 1, "{tag}");
        assert_eq!(problems[0].id.as_str(), id, "{tag}");
    }
}

#[test]
fn admin_block_override_analyzer_handles_incomplete_nested_declaration() {
    let source = r#"<template><sw-block extends="outer"><div><sw-block extends="inner""#;
    let document = block_override_document("component.vue", source);
    assert!(!document.parse_errors().is_empty());
    let problems = analyze(&AdminBlockOverrideAnalyzer::new(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.as_str(), ADMIN_BLOCK_OVERRIDE_NESTED_CODE);
}

#[test]
fn admin_block_override_analyzer_ignores_files_outside_administration() {
    let document = Arc::new(TextDocument::new(
        "file:///project/templates/component.vue",
        r#"<template><sw-block v-if="enabled" extends="conditional" /></template>"#,
        1,
    ));
    assert!(analyze(&AdminBlockOverrideAnalyzer::new(), &document).is_empty());
}

#[test]
fn admin_i18n_deprecation_analyzer_finds_calls_across_admin_languages() {
    let analyzer = AdminI18nDeprecationAnalyzer::new();
    let cases: [(&str, &str, usize); 7] = [
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "this.$tc('translation.key');",
            1,
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.ts",
            "$tc('translation.key', count);",
            1,
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.html.twig",
            "{{ $tc('translation.key') }}",
            1,
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.vue",
            "<template>{{ $tc('template.key') }}</template>\n<script>this.$tc('script.key', count);</script>",
            2,
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "const $tc = translator; this.$tc;",
            0,
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "this.$t('translation.key');",
            0,
        ),
        (
            "file:///project/src/storefront/component.js",
            "this.$tc('translation.key');",
            0,
        ),
    ];
    for (uri, source, count) in cases {
        let document = diagnostics_document(uri, source);
        let problems = analyze(&analyzer, &document);
        assert_eq!(problems.len(), count, "{uri}: {source}");
        for problem in &problems {
            assert_eq!(problem.id.as_str(), "admin.vue-i18n.tc-deprecated");
            let element_text = match problem.element.as_ref().expect("element") {
                SyntaxElement::Node(node) => node.text().to_string(),
                SyntaxElement::Token(token) => token.text().to_owned(),
            };
            assert_eq!(element_text, "$tc");
            assert_eq!(slice(source, problem.range), "$tc");
        }
    }
}

#[test]
fn admin_slot_migration_analyzer_builds_combined_replacement() {
    let document = diagnostics_document(
        "file:///project/Resources/app/administration/component.html.twig",
        r#"<template slot="actions" slot-scope="{ item }"><button/></template>"#,
    );
    let problems = analyze(&AdminSlotMigrationAnalyzer::new(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(
        problems[0].payload.as_ref().unwrap()["replacement"],
        r#"<template #actions="{ item }">"#
    );
}

#[test]
fn admin_slot_migration_analyzer_only_scans_vue_template_section() {
    let document = diagnostics_document(
        "file:///project/Resources/app/administration/component.vue",
        "<script>\nconst example = '<template slot=\"ignored\">';\n</script>\n<template>\n    <template slot=\"actions\" slot-scope=\"{ item }\"><button/></template>\n</template>",
    );
    let problems = analyze(&AdminSlotMigrationAnalyzer::new(), &document);
    assert_eq!(problems.len(), 1);
    assert!(
        problems[0].payload.as_ref().unwrap()["replacement"]
            .as_str()
            .unwrap()
            .contains("#actions")
    );
}
