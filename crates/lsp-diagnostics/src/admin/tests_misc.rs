//! Ports of `admin_block_override_test.go`, `admin_i18n_deprecation_test.go`
//! and `admin_slot_migration_test.go`.

use swlsp_syntax::SyntaxElement;

use super::*;
use crate::test_support::check_problems;

fn block_override_uri(name: &str) -> String {
    swlsp_uriutil::file_uri(&format!(
        "/project/src/Resources/app/administration/src/{name}"
    ))
}

#[test]
fn admin_block_override_analyzer_reports_unsupported_structures() {
    let (_, problems) = check_problems(
        &AdminBlockOverrideAnalyzer::new(),
        &block_override_uri("component.vue"),
        r#"<template>
    <sw-block extends="outer">
        <div><{|admin.block-override.nested:sw-block|} extends="nested" /></div>
        <{|admin.block-parent.conditional:sw-block-parent|} v-if="showParent" />
        <template v-for="item in items"><{|admin.block-parent.repeated:sw-block-parent|} /></template>
    </sw-block>
    <{|admin.block-override.conditional:sw-block|} v-if="enabled" extends="conditional" />
    <section v-for="item in items"><{|admin.block-override.repeated:sw-block|} extends="repeated" /></section>
</template>"#,
    );
    for problem in &problems {
        assert!(!problem.message.is_empty());
    }
}

#[test]
fn admin_block_override_analyzer_allows_static_sibling_declarations() {
    check_problems(
        &AdminBlockOverrideAnalyzer::new(),
        &block_override_uri("component.vue"),
        r#"<template>
    <sw-block extends="first">
        <div><section><sw-block-parent /></section></div>
    </sw-block>
    <div v-show="visible"><sw-block extends="second" /></div>
    <sw-block name="ordinary"><sw-block-parent /></sw-block>
</template>"#,
    );
}

#[test]
fn admin_block_override_analyzer_reports_ancestor_and_twig_control_flow() {
    let cases = [
        (
            "component.vue",
            r#"<template>
    <section v-if="enabled"><div><{|admin.block-override.conditional:sw-block|} extends="conditional" /></div></section>
    <template v-for="item in items"><div><{|admin.block-parent.repeated:sw-block-parent|} /></div></template>
</template>"#,
        ),
        (
            "component.html.twig",
            r#"{% if enabled %}<{|admin.block-override.conditional:sw-block|} extends="conditional" />{% endif %}
{% for item in items %}<{|admin.block-parent.repeated:sw-block-parent|} />{% endfor %}"#,
        ),
    ];
    for (file, fixture) in cases {
        check_problems(
            &AdminBlockOverrideAnalyzer::new(),
            &block_override_uri(file),
            fixture,
        );
    }
}

#[test]
fn admin_block_override_analyzer_reports_vue_control_directives() {
    for tag in [
        r#"<{|admin.block-override.conditional:sw-block|} v-if="enabled" extends="example" />"#,
        r#"<{|admin.block-override.conditional:sw-block|} v-else-if="enabled" extends="example" />"#,
        r#"<{|admin.block-override.conditional:sw-block|} v-else extends="example" />"#,
        r#"<{|admin.block-override.repeated:sw-block|} v-for="item in items" extends="example" />"#,
        r#"<{|admin.block-parent.conditional:sw-block-parent|} v-if="enabled" />"#,
        r#"<{|admin.block-parent.conditional:sw-block-parent|} v-else-if="enabled" />"#,
        r#"<{|admin.block-parent.conditional:sw-block-parent|} v-else />"#,
        r#"<{|admin.block-parent.repeated:sw-block-parent|} v-for="item in items" />"#,
    ] {
        check_problems(
            &AdminBlockOverrideAnalyzer::new(),
            &block_override_uri("component.vue"),
            &format!("<template>{tag}</template>"),
        );
    }
}

#[test]
fn admin_block_override_analyzer_handles_incomplete_nested_declaration() {
    let (document, _) = check_problems(
        &AdminBlockOverrideAnalyzer::new(),
        &block_override_uri("component.vue"),
        r#"<template><sw-block extends="outer"><div><{|admin.block-override.nested:sw-block|} extends="inner""#,
    );
    assert!(!document.parse_errors().is_empty());
}

#[test]
fn admin_block_override_analyzer_ignores_files_outside_administration() {
    check_problems(
        &AdminBlockOverrideAnalyzer::new(),
        "file:///project/templates/component.vue",
        r#"<template><sw-block v-if="enabled" extends="conditional" /></template>"#,
    );
}

#[test]
fn admin_i18n_deprecation_analyzer_finds_calls_across_admin_languages() {
    let analyzer = AdminI18nDeprecationAnalyzer::new();
    let cases: [(&str, &str); 7] = [
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "this.{|admin.vue-i18n.tc-deprecated:$tc|}('translation.key');",
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.ts",
            "{|admin.vue-i18n.tc-deprecated:$tc|}('translation.key', count);",
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.html.twig",
            "{{ {|admin.vue-i18n.tc-deprecated:$tc|}('translation.key') }}",
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.vue",
            "<template>{{ {|admin.vue-i18n.tc-deprecated:$tc|}('template.key') }}</template>\n<script>this.{|admin.vue-i18n.tc-deprecated:$tc|}('script.key', count);</script>",
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "const $tc = translator; this.$tc;",
        ),
        (
            "file:///project/src/Resources/app/administration/src/component.js",
            "this.$t('translation.key');",
        ),
        (
            "file:///project/src/storefront/component.js",
            "this.$tc('translation.key');",
        ),
    ];
    for (uri, fixture) in cases {
        let (_, problems) = check_problems(&analyzer, uri, fixture);
        for problem in &problems {
            let element_text = match problem.element.as_ref().expect("element") {
                SyntaxElement::Node(node) => node.text().to_string(),
                SyntaxElement::Token(token) => token.text().to_owned(),
            };
            assert_eq!(element_text, "$tc");
        }
    }
}

#[test]
fn admin_slot_migration_analyzer_builds_combined_replacement() {
    let (_, problems) = check_problems(
        &AdminSlotMigrationAnalyzer::new(),
        "file:///project/Resources/app/administration/component.html.twig",
        r#"{|admin.slot-syntax-deprecated:<template slot="actions" slot-scope="{ item }">|}<button/></template>"#,
    );
    assert_eq!(
        problems[0].payload.as_ref().unwrap()["replacement"],
        r#"<template #actions="{ item }">"#
    );
}

#[test]
fn admin_slot_migration_analyzer_only_scans_vue_template_section() {
    let (_, problems) = check_problems(
        &AdminSlotMigrationAnalyzer::new(),
        "file:///project/Resources/app/administration/component.vue",
        "<script>\nconst example = '<template slot=\"ignored\">';\n</script>\n<template>\n    {|admin.slot-syntax-deprecated:<template slot=\"actions\" slot-scope=\"{ item }\">|}<button/></template>\n</template>",
    );
    assert!(
        problems[0].payload.as_ref().unwrap()["replacement"]
            .as_str()
            .unwrap()
            .contains("#actions")
    );
}
