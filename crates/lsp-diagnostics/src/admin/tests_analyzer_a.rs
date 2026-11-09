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
            "Component.extend('my-component', {|admin.component.parent-not-found:'sw-undefined-parent'|}, () => import('./index'));",
            "file:///project/src/Resources/app/administration/src/main.js",
            Some("Parent component 'sw-undefined-parent' is not registered"),
        ),
        (
            "Component.register('my-component', () => import('./index'));",
            "file:///project/src/Resources/app/administration/src/main.js",
            None,
        ),
        (
            "Shopware.Component.extend('my-component', {|admin.component.parent-not-found:'sw-missing'|}, () => import('./index'));",
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
        let problems = check(&analyzer, uri, code);
        if let Some(message) = message {
            assert_eq!(problems[0].message, message);
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
    check(
        &fixture.analyzer(),
        "file:///project/src/Resources/app/administration/src/main.js",
        "Component.extend('my-button', 'sw-button', () => import('./index'));",
    );
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
    let marked = r#"<template><{|admin.component.missing-required-prop:sw-button|}>{{ {|admin.component.unknown-template-member:titel|} }}</sw-button></template>
<script setup lang="ts">
const props = defineProps({ title: { type: String, required: true } });
</script>"#;
    let (source, _) = swlsp_lsp::fixture::parse(marked);
    fixture.index(&component_path, &source);
    fixture.index(
        &format!("{admin_root}/sw-host/index.ts"),
        "Shopware.Component.register('sw-host', () => import('./index.vue'));",
    );
    fixture.check_path(&component_path, marked);
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
    fixture.check_path(
        &consumer_path,
        "<{|admin.component.missing-required-prop:sw-legacy-live|} {|admin.component.unknown-prop:live-requird|}=\"1\">\n    <template #{|admin.component.unknown-slot:live-solt|} />\n</sw-legacy-live>",
    );

    fixture.check_path(&template_path, live_template);

    fixture.indexer.remove_live_document(&definition_path);
    fixture.indexer.remove_live_document(&template_path);
    fixture.check_path(
        &consumer_path,
        "<sw-legacy-live old-required=\"saved\">\n    <template #old-slot />\n</sw-legacy-live>",
    );
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
    fixture.check_path(
        &consumer,
        "<{|admin.component.missing-required-prop:sw-imported-card|} {|admin.component.unknown-prop:mdoe|}=\"wrong\">\n    <template #header=\"{ {|admin.component.unknown-slot-prop:itme|} }\" />\n    <template #{|admin.component.unknown-slot:haeder|} />\n</sw-imported-card>",
    );

    let live_component_source = component_source.replace("heading", "headline");
    fixture.check_path(&component_path, &live_component_source);

    let live_imported = fixture.check_path(
        &component_path,
        r#"<template>{{ profile.{|admin.component.unknown-vue-member:ctiy|} }}</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>"#,
    );
    for problem in &live_imported {
        assert!(
            problem.message.contains("profile.ctiy"),
            "{}",
            problem.message
        );
    }

    fixture.check_path(
        &component_path,
        r#"<template>
    <{|admin.component.missing-required-prop:DraftPanel|} {|admin.component.unknown-prop:lable|}="wrong">
        <template #default="{ {|admin.component.unknown-slot-prop:itme|} }" />
        <template #{|admin.component.unknown-slot:defualt|} />
    </DraftPanel>
</template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
</script>"#,
    );

    let draft_panel_path = format!("{component_dir}/DraftPanel.vue");
    fixture.update_live(
        &draft_panel_path,
        r#"<template><slot name="preview" /></template>
<script setup lang="ts">
defineProps<{ draftLabel: string }>();
defineSlots<{ preview(props: { record: { id: string } }): unknown }>();
</script>"#,
    );
    fixture.check_path(
        &component_path,
        "<template>\n\t    <{|admin.component.missing-required-prop:DraftPanel|} {|admin.component.unknown-prop:draft-lable|}=\"value\"><template #{|admin.component.unknown-slot:preveiw|} /></DraftPanel>\n</template>\n<script setup lang=\"ts\">import DraftPanel from './DraftPanel.vue';</script>",
    );

    fixture.indexer.remove_live_document(&draft_panel_path);
    fixture.check_path(
        &component_path,
        "<template>\n\t    <DraftPanel label=\"persisted\"><template #default /></DraftPanel>\n</template>\n<script setup lang=\"ts\">import DraftPanel from './DraftPanel.vue';</script>",
    );
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
    let problems = fixture.check_path(
        &format!("{admin_root}/consumer.html.twig"),
        r#"<{|admin.component.deprecated:sw-legacy|} :{|admin.component.deprecated-prop:old-value|}="value" :active="true" v-bind="{ {|admin.component.deprecated-prop:oldValue|}: fallback }"></sw-legacy>"#,
    );
    for problem in &problems {
        assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
    }
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
    fixture.check_path(
        &format!("{admin_root}/consumer.html.twig"),
        r#"<component :is="active ? 'sw-a' : 'sw-b'" :title="title"></component>"#,
    );
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

    let markup_problems = fixture.check_path(
        &template_path,
        r#"{{ {|admin.component.deprecated-member:legacySave|}() }}<button @click="{|admin.component.deprecated-member:legacySave|}()"></button>"#,
    );
    for problem in &markup_problems {
        assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
    }

    fixture.check_path(
        &definition_path,
        "export default {\n    methods: { current() { this.{|admin.component.deprecated-member:legacySave|}(); } },\n};",
    );
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
    let problems = fixture.check_path(
        &template_path,
        r#"<div v-tooltip.bottom="message" v-if="visible"></div>
<div v-completely-custom="value"></div>
<div v-hide="value"></div>
<div v-{|admin.directive.not-found:hied|}="value"></div>
<div v-{|admin.directive.not-found:tooltpi|}="message"></div>"#,
    );
    let [hied, tooltpi] = problems.as_slice() else {
        unreachable!()
    };
    assert!(tooltpi.message.contains("v-tooltpi"));
    assert_eq!(suggestions(tooltpi), ["tooltip"]);
    assert_eq!(suggestions(hied), ["hide"]);

    let problems = fixture.check_path(
        &format!("{admin_root}/consumer.ts"),
        "Shopware.Directive.getByName('{|admin.directive.not-found:tooltpi|}');",
    );
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
    let fixture_source = r#"
Shopware.Component.getComponentRegistry().get('sw-card');
Shopware.Module.getModuleRegistry().get('sw-product');
Shopware.Component.getComponentRegistry().has('sw-optional');
Shopware.Module.getModuleRegistry().has('sw-optional');
Shopware.Component.getComponentRegistry().get('{|admin.component.registry-not-found:sw-cadr|}');
Shopware.Module.getModuleRegistry().get('{|admin.module.not-found:sw-prduct|}');
Shopware.Filter.getByName('{|admin.filter.not-found:currncy|}');"#;
    let problems = check(
        &fixture.analyzer(),
        &format!("file://{admin_root}/consumer.ts"),
        fixture_source,
    );
    for (problem, (typo, expected)) in problems.iter().zip([
        ("sw-cadr", "sw-card"),
        ("sw-prduct", "sw-product"),
        ("currncy", "currency"),
    ]) {
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
cmsService.getCmsElementConfigByName('{|admin.cms-element.not-found:herp|}');
cmsService.getCmsBlockConfigByName('{|admin.cms-block.not-found:hero-gird|}');
cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: '{|admin.cms-element.not-found:herp|}' } } });
cmsService.registerCmsElement({ name: 'other', component: '{|admin.component.registry-not-found:sw-cms-el-herp|}' });"#;
    let problems = fixture.check_path(&format!("{admin_root}/consumer.ts"), source);
    for problem in &problems {
        assert!(!suggestions(problem).is_empty());
    }
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
