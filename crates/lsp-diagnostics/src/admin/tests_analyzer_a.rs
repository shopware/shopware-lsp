//! Ports of `admin_diagnostics_test.go` (part 1).

use swlsp_admin::{
    VueComponentMember, VueComponentMemberKind, VueComponentProp, VueLocalDirective,
};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_js::{query as jsquery, syntax as jssyntax};

use super::test_fixture::*;

#[test]
fn admin_analyzer_undefined_parent() {
    let fixture = Fixture::in_root();
    let analyzer = fixture.analyzer();
    let cases = [
        (
            "Component.extend('my-component', 'sw-undefined-parent', () => import('./index'));",
            "file:///project/src/Resources/app/administration/src/main.js",
            Some("Parent component 'sw-undefined-parent' is not registered"),
        ),
        (
            "Component.register('my-component', () => import('./index'));",
            "file:///project/src/Resources/app/administration/src/main.js",
            None,
        ),
        (
            "Shopware.Component.extend('my-component', 'sw-missing', () => import('./index'));",
            "file:///project/src/Resources/app/administration/src/main.js",
            Some("Parent component 'sw-missing' is not registered"),
        ),
        (
            "Component.extend('my-component', 'sw-undefined-parent', () => import('./index'));",
            "file:///project/src/other/main.js",
            None,
        ),
    ];
    for (code, uri, message) in cases {
        let problems = analyze(&analyzer, &document(uri, code));
        match message {
            Some(message) => {
                assert_eq!(problems.len(), 1, "{code}");
                assert_eq!(problems[0].message, message);
            }
            None => assert!(problems.is_empty(), "{code}"),
        }
    }
}

#[test]
fn admin_analyzer_defined_parent() {
    let fixture = Fixture::in_root();
    fixture.index(
        &fixture.path("src/Resources/app/administration/src/component/sw-button/index.js"),
        "Component.register('sw-button', () => import('./index'));",
    );
    let problems = analyze(
        &fixture.analyzer(),
        &document(
            "file:///project/src/Resources/app/administration/src/main.js",
            "Component.extend('my-button', 'sw-button', () => import('./index'));",
        ),
    );
    assert!(problems.is_empty(), "{problems:?}");
}

#[test]
fn admin_analyzer_validates_vue_sfc_template_against_indexed_contracts() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.save(vue_component! {
        name: "sw-button".into(),
        file_path: format!("{admin_root}/sw-button/index.ts"),
        props: vec![prop("label", "String", true)],
    });
    let component_path = format!("{admin_root}/sw-host/index.vue");
    let source = r#"<template><sw-button>{{ titel }}</sw-button></template>
<script setup lang="ts">
const props = defineProps({ title: { type: String, required: true } });
</script>"#;
    fixture.index(&component_path, source);
    fixture.index(
        &format!("{admin_root}/sw-host/index.ts"),
        "Shopware.Component.register('sw-host', () => import('./index.vue'));",
    );
    let codes = codes(&fixture.analyze_path(&component_path, source));
    assert!(codes.contains("admin.component.missing-required-prop"));
    assert!(codes.contains("admin.component.unknown-template-member"));
}

#[test]
fn admin_analyzer_uses_unsaved_legacy_definition_and_twig() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    let registration_path = format!("{admin_root}/component/index.js");
    let component_dir = format!("{admin_root}/component/sw-legacy-live");
    let definition_path = format!("{component_dir}/index.js");
    let template_path = format!("{component_dir}/sw-legacy-live.html.twig");
    let persisted_definition = r#"import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: { oldRequired: { type: String, required: true } },
    data() { return { oldMember: 'old' }; },
};"#;
    let files = [
        (
            registration_path.as_str(),
            "Shopware.Component.register(\n    'sw-legacy-live', () => import('./sw-legacy-live'),\n);",
        ),
        (definition_path.as_str(), persisted_definition),
        (template_path.as_str(), r#"<slot name="old-slot" />"#),
    ];
    for (path, source) in files {
        fixture.write(path, source);
    }
    for (path, source) in files {
        fixture.index(path, source);
    }

    fixture.update_live(
        &definition_path,
        r#"import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: { liveRequired: { type: Number, required: true } },
    data() { return { liveMember: { id: 'draft' } }; },
};"#,
    );
    let live_template = "<slot name=\"live-slot\" :record=\"liveMember\" />\n{{ liveMember.id }}";
    fixture.update_live(&template_path, live_template);

    let consumer_path = format!("{admin_root}/consumer.html.twig");
    let codes_found = codes(&fixture.analyze_path(
        &consumer_path,
        "<sw-legacy-live live-requird=\"1\">\n    <template #live-solt />\n</sw-legacy-live>",
    ));
    assert!(codes_found.contains("admin.component.missing-required-prop"));
    assert!(codes_found.contains("admin.component.unknown-prop"));
    assert!(codes_found.contains("admin.component.unknown-slot"));

    let owner_problems = fixture.analyze_path(&template_path, live_template);
    assert!(!codes(&owner_problems).contains("admin.component.unknown-template-member"));

    fixture.indexer.remove_live_document(&definition_path);
    fixture.indexer.remove_live_document(&template_path);
    let fallback = codes(&fixture.analyze_path(
        &consumer_path,
        "<sw-legacy-live old-required=\"saved\">\n    <template #old-slot />\n</sw-legacy-live>",
    ));
    assert!(!fallback.contains("admin.component.missing-required-prop"));
    assert!(!fallback.contains("admin.component.unknown-prop"));
    assert!(!fallback.contains("admin.component.unknown-slot"));
}

#[test]
fn admin_analyzer_uses_imported_script_setup_prop_contract() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    let component_dir = format!("{admin_root}/component/sw-imported-card");
    let component_path = format!("{component_dir}/index.vue");
    let component_source = r#"<template>{{ heading }}<slot name="header" :item="heading" /></template>
<script setup lang="ts">
import type { CardProps, CardSlots } from './contracts';
const { optionalTitle: heading = 'fallback' } = defineProps<CardProps>();
defineSlots<CardSlots>();
</script>"#;
    fixture.index(&component_path, component_source);
    fixture.index(
        &format!("{component_dir}/index.ts"),
        "Shopware.Component.register('sw-imported-card', () => import('./index.vue'));",
    );
    fixture.index(
        &format!("{component_dir}/contracts.ts"),
        "export interface CardProps { mode: string; optionalTitle?: string }\nexport interface CardSlots { header(props: { item: string }): unknown }",
    );
    fixture.index(
        &format!("{component_dir}/draft-contracts.ts"),
        "export interface DraftProfile { city: string; zip: string }\nexport interface DraftProps { profile: DraftProfile }",
    );
    fixture.index(
        &format!("{component_dir}/DraftPanel.vue"),
        r#"<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
defineSlots<{ default(props: { item: string; active: boolean }): unknown }>();
</script>"#,
    );

    let consumer = format!("{admin_root}/consumer.html.twig");
    let found = codes(&fixture.analyze_path(
        &consumer,
        "<sw-imported-card mdoe=\"wrong\">\n    <template #header=\"{ itme }\" />\n    <template #haeder />\n</sw-imported-card>",
    ));
    for code in [
        "admin.component.missing-required-prop",
        "admin.component.unknown-prop",
        "admin.component.unknown-slot",
        "admin.component.unknown-slot-prop",
    ] {
        assert!(found.contains(code), "{code}");
    }

    let live_component_source = component_source.replace("heading", "headline");
    let component_problems = fixture.analyze_path(&component_path, &live_component_source);
    assert!(!codes(&component_problems).contains("admin.component.unknown-template-member"));

    let live_imported = fixture.analyze_path(
        &component_path,
        r#"<template>{{ profile.ctiy }}</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>"#,
    );
    let nested = with_code(&live_imported, "admin.component.unknown-vue-member");
    assert!(!nested.is_empty());
    for problem in nested {
        assert!(
            problem.message.contains("profile.ctiy"),
            "{}",
            problem.message
        );
    }

    let live_local = fixture.analyze_path(
        &component_path,
        r#"<template>
    <DraftPanel lable="wrong">
        <template #default="{ itme }" />
        <template #defualt />
    </DraftPanel>
</template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
</script>"#,
    );
    let live_local_codes = codes(&live_local);
    assert!(!live_local_codes.contains("admin.component.not-found"));
    for code in [
        "admin.component.missing-required-prop",
        "admin.component.unknown-prop",
        "admin.component.unknown-slot",
        "admin.component.unknown-slot-prop",
    ] {
        assert!(live_local_codes.contains(code), "{code}");
    }

    let draft_panel_path = format!("{component_dir}/DraftPanel.vue");
    fixture.update_live(
        &draft_panel_path,
        r#"<template><slot name="preview" /></template>
<script setup lang="ts">
defineProps<{ draftLabel: string }>();
defineSlots<{ preview(props: { record: { id: string } }): unknown }>();
</script>"#,
    );
    let live_child = codes(&fixture.analyze_path(
        &component_path,
        "<template>\n\t    <DraftPanel draft-lable=\"value\"><template #preveiw /></DraftPanel>\n</template>\n<script setup lang=\"ts\">import DraftPanel from './DraftPanel.vue';</script>",
    ));
    assert!(!live_child.contains("admin.component.not-found"));
    for code in [
        "admin.component.missing-required-prop",
        "admin.component.unknown-prop",
        "admin.component.unknown-slot",
    ] {
        assert!(live_child.contains(code), "{code}");
    }

    fixture.indexer.remove_live_document(&draft_panel_path);
    let fallback = codes(&fixture.analyze_path(
        &component_path,
        "<template>\n\t    <DraftPanel label=\"persisted\"><template #default /></DraftPanel>\n</template>\n<script setup lang=\"ts\">import DraftPanel from './DraftPanel.vue';</script>",
    ));
    assert!(!fallback.contains("admin.component.unknown-prop"));
    assert!(!fallback.contains("admin.component.unknown-slot"));
}

#[test]
fn admin_analyzer_reports_deprecated_components_and_props() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.save(vue_component! {
        name: "sw-legacy".into(),
        file_path: format!("{admin_root}/sw-legacy.ts"),
        deprecated: "tag:v6.8.0 - Use mt-modern instead.".into(),
        props: vec![
            VueComponentProp {
                deprecated: "Use modernValue instead.".into(),
                ..prop("oldValue", "String", false)
            },
            prop("active", "Boolean", false),
        ],
    });
    let source = r#"<sw-legacy :old-value="value" :active="true" v-bind="{ oldValue: fallback }"></sw-legacy>"#;
    let problems = fixture.analyze_path(&format!("{admin_root}/consumer.html.twig"), source);
    let deprecated: Vec<_> = problems
        .iter()
        .filter(|problem| {
            problem.id.0 == "admin.component.deprecated"
                || problem.id.0 == "admin.component.deprecated-prop"
        })
        .collect();
    assert_eq!(deprecated.len(), 3);
    let mut by_text = std::collections::HashMap::new();
    for problem in deprecated {
        assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
        by_text.insert(slice(source, problem.range), problem.id.0.clone());
    }
    assert_eq!(by_text["sw-legacy"], "admin.component.deprecated");
    assert_eq!(by_text["old-value"], "admin.component.deprecated-prop");
    assert_eq!(by_text["oldValue"], "admin.component.deprecated-prop");
    assert!(!by_text.contains_key("active"));
}

#[test]
fn admin_analyzer_requires_all_dynamic_contracts_to_deprecate_prop() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.save(vue_component! {
        name: "sw-a".into(),
        file_path: format!("{admin_root}/a.ts"),
        props: vec![VueComponentProp {
            deprecated: "Use heading instead.".into(),
            ..prop("title", "String", false)
        }],
    });
    fixture.save(vue_component! {
        name: "sw-b".into(),
        file_path: format!("{admin_root}/b.ts"),
        props: vec![prop("title", "String", false)],
    });
    let problems = fixture.analyze_path(
        &format!("{admin_root}/consumer.html.twig"),
        r#"<component :is="active ? 'sw-a' : 'sw-b'" :title="title"></component>"#,
    );
    assert!(!codes(&problems).contains("admin.component.deprecated-prop"));
}

#[test]
fn admin_analyzer_reports_deprecated_members_in_script_and_markup() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    let definition_path = format!("{admin_root}/sw-card.ts");
    let template_path = format!("{admin_root}/sw-card.html.twig");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        methods: vec!["legacySave".into()],
        members: vec![VueComponentMember {
            name: "legacySave".into(),
            kind: VueComponentMemberKind::Method,
            file_path: definition_path.clone(),
            line: 8,
            deprecated: "Use save instead.".into(),
            ..VueComponentMember::default()
        }],
    });

    let markup = r#"{{ legacySave() }}<button @click="legacySave()"></button>"#;
    let markup_problems = fixture.analyze_path(&template_path, markup);
    let markup_deprecated = with_code(&markup_problems, "admin.component.deprecated-member");
    assert_eq!(markup_deprecated.len(), 2);
    for problem in markup_deprecated {
        assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
        assert_eq!(slice(markup, problem.range), "legacySave");
    }

    let script = "export default {\n    methods: { current() { this.legacySave(); } },\n};";
    let script_problems = fixture.analyze_path(&definition_path, script);
    let script_deprecated = with_code(&script_problems, "admin.component.deprecated-member");
    assert_eq!(script_deprecated.len(), 1);
    assert_eq!(slice(script, script_deprecated[0].range), "legacySave");
}

#[test]
fn admin_analyzer_custom_directive_diagnostics() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.index(
        &format!("{admin_root}/app/directive/tooltip.directive.ts"),
        "Shopware.Directive.register('tooltip', {});",
    );
    let template_path = format!("{admin_root}/consumer.html.twig");
    fixture.save(vue_component! {
        name: "sw-consumer".into(),
        file_path: format!("{admin_root}/consumer.ts"),
        template_path: template_path.clone(),
        local_directives: vec![VueLocalDirective {
            name: "hide".into(),
            ..VueLocalDirective::default()
        }],
    });
    let source = r#"<div v-tooltip.bottom="message" v-if="visible"></div>
<div v-completely-custom="value"></div>
<div v-hide="value"></div>
<div v-hied="value"></div>
<div v-tooltpi="message"></div>"#;
    let problems = fixture.analyze_path(&template_path, source);
    assert_eq!(problems.len(), 2, "{problems:?}");
    let by_name: std::collections::HashMap<_, _> = problems
        .iter()
        .map(|problem| (slice(source, problem.range), problem))
        .collect();
    assert!(by_name["tooltpi"].message.contains("v-tooltpi"));
    assert_eq!(suggestions(by_name["tooltpi"]), ["tooltip"]);
    assert_eq!(suggestions(by_name["hied"]), ["hide"]);

    let problems = fixture.analyze_path(
        &format!("{admin_root}/consumer.ts"),
        "Shopware.Directive.getByName('tooltpi');",
    );
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.0, "admin.directive.not-found");
    assert_eq!(suggestions(&problems[0]), ["tooltip"]);
}

#[test]
fn admin_analyzer_registry_lookup_diagnostics() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.index(
        &format!("{admin_root}/main.ts"),
        "\nShopware.Component.register('sw-card', {});\nShopware.Module.register('sw-product', { routes: {} });\nShopware.Filter.register('currency', value => value);",
    );
    let source = r#"
Shopware.Component.getComponentRegistry().get('sw-card');
Shopware.Module.getModuleRegistry().get('sw-product');
Shopware.Component.getComponentRegistry().has('sw-optional');
Shopware.Module.getModuleRegistry().has('sw-optional');
Shopware.Component.getComponentRegistry().get('sw-cadr');
Shopware.Module.getModuleRegistry().get('sw-prduct');
Shopware.Filter.getByName('currncy');"#;
    let problems = analyze(
        &fixture.analyzer(),
        &document(&format!("file://{admin_root}/consumer.ts"), source),
    );
    assert_eq!(problems.len(), 3, "{problems:?}");
    let by_id: std::collections::HashMap<_, _> = problems
        .iter()
        .map(|problem| (problem.id.0.as_str(), problem))
        .collect();
    for (code, typo, expected) in [
        ("admin.component.registry-not-found", "sw-cadr", "sw-card"),
        ("admin.module.not-found", "sw-prduct", "sw-product"),
        ("admin.filter.not-found", "currncy", "currency"),
    ] {
        let problem = by_id[code];
        assert!(problem.message.contains(typo));
        assert!(suggestions(problem).contains(&expected.to_owned()));
    }
}

#[test]
fn admin_analyzer_cms_registry_diagnostics() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.index(
        &format!("{admin_root}/cms.ts"),
        "\nShopware.Component.register('sw-cms-el-hero', {});\ncmsService.registerCmsElement({ name: 'hero', component: 'sw-cms-el-hero' });\ncmsService.registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });",
    );
    let source = r#"
cmsService.getCmsElementConfigByName('hero');
cmsService.getCmsBlockConfigByName('hero-grid');
cmsService.getCmsElementConfigByName('herp');
cmsService.getCmsBlockConfigByName('hero-gird');
cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'herp' } } });
cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-herp' });"#;
    let problems = fixture.analyze_path(&format!("{admin_root}/consumer.ts"), source);
    assert_eq!(problems.len(), 4, "{problems:?}");
    let mut counts = std::collections::HashMap::new();
    for problem in &problems {
        *counts.entry(problem.id.0.as_str()).or_insert(0) += 1;
        assert!(!suggestions(problem).is_empty());
    }
    assert_eq!(counts["admin.cms-element.not-found"], 2);
    assert_eq!(counts["admin.cms-block.not-found"], 1);
    assert_eq!(counts["admin.component.registry-not-found"], 1);
}

#[test]
fn extract_string_content() {
    for (code, expected) in [("'hello'", "hello"), ("\"world\"", "world")] {
        let root = swlsp_parser_js::parse(code).tree.root();
        let nodes = jsquery::nodes(&root, &[jssyntax::JS_STRING]);
        assert!(!nodes.is_empty());
        assert_eq!(jsquery::string_value(&nodes[0]), expected);
    }
}
