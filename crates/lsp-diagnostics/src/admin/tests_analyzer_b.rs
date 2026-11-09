//! Tests of the Administration analyzer (part 2).

use swlsp_admin::{
    VueComponentAssignment, VueComponentEvent, VueComponentMember, VueComponentMemberKind,
    VueComponentProp, VueComponentSlot, twig_dynamic_component_selector,
};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};

use super::test_fixture::*;

const VIEW_URI: &str = "file:///project/Resources/app/administration/src/view.html.twig";

#[test]
fn admin_analyzer_missing_required_props() {
    let fixture = Fixture::in_root();
    fixture.index(
        &fixture.path("src/Resources/app/administration/src/component/sw-button/index.js"),
        r#"
Component.register('sw-button', {
	props: {
		label: {
			type: String,
			required: true,
		},
		disabled: {
			type: Boolean,
			required: false,
		},
		variant: {
			type: String,
			required: true,
		},
	},
});
"#,
    );
    let components = fixture
        .indexer
        .get_component_with_definition("sw-button")
        .unwrap();
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].props.len(), 3);

    let analyzer = fixture.analyzer();
    let cases: &[(&str, &[&str])] = &[
        (
            "<{|admin.component.missing-required-prop:{|admin.component.missing-required-prop:sw-button|}|}></sw-button>",
            &["label", "variant"],
        ),
        (
            r#"<{|admin.component.missing-required-prop:sw-button|} label="Click me"></sw-button>"#,
            &["variant"],
        ),
        (
            r#"<sw-button label="Click me" variant="primary"></sw-button>"#,
            &[],
        ),
        (
            r#"<sw-button :label="buttonLabel" variant="primary"></sw-button>"#,
            &[],
        ),
        (
            r#"<sw-button v-bind:label="buttonLabel" variant="primary"></sw-button>"#,
            &[],
        ),
        (
            r#"<sw-button v-bind="{ label: buttonLabel, variant }"></sw-button>"#,
            &[],
        ),
        (
            r#"<{|admin.component.missing-required-prop:sw-button|} v-bind="{ label: buttonLabel }"></sw-button>"#,
            &["variant"],
        ),
        (
            r#"<{|admin.component.missing-required-prop:{|admin.component.missing-required-prop:sw-button|}|} v-bind="{}"></sw-button>"#,
            &["label", "variant"],
        ),
        (
            r#"<sw-button v-bind="filteredAttributes"></sw-button>"#,
            &[],
        ),
        (
            r#"<sw-button v-bind="{ label, ...forwarded }"></sw-button>"#,
            &[],
        ),
        (
            r#"<sw-button label="Click" variant="primary"></sw-button>"#,
            &[],
        ),
        (
            r#"<{|admin.component.not-found:sw-butotn|} required-prop="value"></sw-butotn>"#,
            &[],
        ),
        (
            r#"<plugin-unknown required-prop="value"></plugin-unknown>"#,
            &[],
        ),
        (r#"<div class="test"></div>"#, &[]),
    ];
    for (markup, props) in cases {
        let problems = check(
            &analyzer,
            "file:///project/src/Resources/app/administration/src/views/test.html.twig",
            markup,
        );
        for expected in *props {
            assert!(
                problems
                    .iter()
                    .any(|problem| payload_str(problem, "propName") == *expected),
                "{markup}: missing {expected}"
            );
        }
    }
}

#[test]
fn admin_analyzer_unknown_shopware_component() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/component/sw-button/index.js"),
        "Shopware.Component.register('sw-button', {});",
    );
    let problems = check(
        &fixture.analyzer(),
        &format!("file://{admin_root}/view.html.twig"),
        "<{|admin.component.not-found:sw-butotn|}></sw-butotn><plugin-widget></plugin-widget>",
    );
    assert!(problems[0].message.contains("sw-butotn"));
    assert!(suggestions(&problems[0]).contains(&"sw-button".to_owned()));
}

#[test]
fn admin_analyzer_unknown_shopware_component_needs_indexed_catalog() {
    let fixture = Fixture::in_root();
    check(&fixture.analyzer(), VIEW_URI, "<sw-unknown></sw-unknown>");
}

#[test]
fn admin_analyzer_options_api_local_component_is_template_scoped() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let template_path = format!("{admin_root}/component/mt-card/mt-card.html.twig");
    fixture.index(
        &format!("{admin_root}/component/mt-card/index.ts"),
        r#"
import { MtCard } from '@shopware-ag/meteor-component-library';
import template from './mt-card.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});
"#,
    );
    let problems = fixture.check_path(
        &template_path,
        "<mt-card-original></mt-card-original><{|admin.component.not-found:mt-other-original|}></mt-other-original>",
    );
    assert!(problems[0].message.contains("mt-other-original"));
}

#[test]
fn admin_analyzer_kebab_case_props() {
    let fixture = Fixture::in_root();
    fixture.index(
        &fixture.path("src/Resources/app/administration/src/component/mt-card/index.js"),
        r#"
Component.register('mt-card', {
	props: {
		positionIdentifier: {
			type: String,
			required: true,
		},
	},
});
"#,
    );
    let analyzer = fixture.analyzer();
    for markup in [
        r#"<mt-card positionIdentifier="test"></mt-card>"#,
        r#"<mt-card position-identifier="test"></mt-card>"#,
        r#"<mt-card :position-identifier="myVar"></mt-card>"#,
        "<{|admin.component.missing-required-prop:mt-card|}></mt-card>",
    ] {
        check(
            &analyzer,
            "file:///project/src/Resources/app/administration/src/views/test.html.twig",
            markup,
        );
    }
}

#[test]
fn admin_analyzer_inherited_required_prop() {
    let fixture = Fixture::in_root();
    let admin_root = fixture.path("src/Resources/app/administration/src/component");
    fixture.index(
        &format!("{admin_root}/sw-parent/index.js"),
        "Component.register('sw-parent', {\n    props: { parentLabel: { type: String, required: true } },\n});",
    );
    fixture.index(
        &format!("{admin_root}/sw-child/index.js"),
        "Component.extend('sw-child', 'sw-parent', {\n    props: { childLabel: String },\n});",
    );
    let problems = check(
        &fixture.analyzer(),
        VIEW_URI,
        r#"<{|admin.component.missing-required-prop:sw-child|} child-label="Child"></sw-child>"#,
    );
    assert_eq!(payload_str(&problems[0], "propName"), "parentLabel");
}

#[test]
fn admin_analyzer_static_prop_types() {
    let fixture = Fixture::in_root();
    fixture.index(
        &fixture.path("src/Resources/app/administration/src/component/sw-field/index.js"),
        "Component.register('sw-field', {\n    props: { count: Number, active: Boolean, label: String },\n\t\t});",
    );
    let analyzer = fixture.analyzer();
    for markup in [
        r#"<sw-field {|admin.component.static-prop-type:count="5"|}></sw-field>"#,
        r#"<sw-field :count="5"></sw-field>"#,
        "<sw-field active></sw-field>",
        r#"<sw-field active=""></sw-field>"#,
        r#"<sw-field {|admin.component.static-prop-type:active="false"|}></sw-field>"#,
        r#"<sw-field label="Name"></sw-field>"#,
    ] {
        check(&analyzer, VIEW_URI, markup);
    }
}

#[test]
fn admin_analyzer_static_prop_allowed_values() {
    let fixture = Fixture::in_root();
    fixture.save(vue_component! {
        name: "sw-label".into(),
        file_path: fixture.path("sw-label/index.js"),
        props: vec![
            VueComponentProp {
                allowed_values: vec!["".into(), "primary".into(), "secondary".into()],
                allowed_values_complete: true,
                ..prop("variant", "String", false)
            },
            prop("size", r#""small" | "large""#, false),
            prop("legacy", r#""known" | LegacyValue"#, false),
        ],
    });
    let analyzer = fixture.analyzer();
    for (name, markup) in [
        ("allowed value", r#"<sw-label variant="primary" />"#),
        ("allowed empty value", r#"<sw-label variant="" />"#),
        (
            "invalid options value",
            r#"<sw-label variant="{|admin.component.invalid-prop-value:primry|}" />"#,
        ),
        (
            "invalid literal union value",
            r#"<sw-label size="{|admin.component.invalid-prop-value:medium|}" />"#,
        ),
        (
            "invalid empty literal union value",
            r#"<sw-label size="{|admin.component.invalid-prop-value:|}" />"#,
        ),
        (
            "invalid bound literal union value",
            r#"<sw-label :size="'{|admin.component.invalid-prop-value:medium|}'" />"#,
        ),
        (
            "valid bound literal union value",
            r#"<sw-label :size="'small'" />"#,
        ),
        (
            "invalid object-bound literal union value",
            r#"<sw-label v-bind="{ size: '{|admin.component.invalid-prop-value:medium|}' }" />"#,
        ),
        (
            "bound value is runtime",
            r#"<sw-label :variant="selectedVariant" />"#,
        ),
        (
            "partial union stays conservative",
            r#"<sw-label legacy="other" />"#,
        ),
    ] {
        let problems = check(&analyzer, VIEW_URI, markup);
        if !problems.is_empty() {
            let payload = problems[0].payload.as_ref().unwrap();
            assert!(
                payload["allowedValues"]
                    .as_array()
                    .is_some_and(|values| !values.is_empty())
            );
            if name != "invalid empty literal union value" {
                assert!(!suggestions(&problems[0]).is_empty(), "{name}");
            }
        }
    }
}

fn member(name: &str, kind: VueComponentMemberKind, type_: &str) -> VueComponentMember {
    VueComponentMember {
        name: name.into(),
        kind,
        type_: type_.into(),
        ..VueComponentMember::default()
    }
}

fn event(name: &str, type_: &str) -> VueComponentEvent {
    VueComponentEvent {
        name: name.into(),
        type_: type_.into(),
        ..VueComponentEvent::default()
    }
}

fn slot(name: &str, prefix: &str) -> VueComponentSlot {
    VueComponentSlot {
        name: name.into(),
        name_prefix: prefix.into(),
        ..VueComponentSlot::default()
    }
}

#[test]
fn admin_analyzer_dynamic_component_contracts() {
    let fixture = Fixture::in_root();
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-field/index.js");
    fixture.save(vue_component! {
        name: "sw-field".into(),
        file_path: definition_path.clone(),
        props: vec![
            prop("title", "String", true),
            prop("count", "Number", false),
            prop("account", "Object", false),
        ],
    });
    fixture.save(vue_component! {
        name: "sw-other".into(),
        file_path: fixture.path("sw-other/index.js"),
        props: vec![prop("account", "Object", false)],
    });
    let template_path = "/project/Resources/app/administration/src/view.html.twig";
    fixture.save(vue_component! {
        name: "sw-host".into(),
        file_path: fixture.path("sw-host/index.js"),
        template_path: template_path.to_owned(),
        members: vec![VueComponentMember {
            return_expressions: vec!["'sw-field'".into(), "'sw-other'".into()],
            returns_complete: true,
            ..member("dynamicField", VueComponentMemberKind::Computed, "")
        }],
    });
    fixture.index(
        &fixture.path("src/Resources/app/administration/src/module/sw-host/routes.js"),
        r#"Shopware.Module.register('sw-host', {
    routes: {
        index: {
            component: 'sw-host',
            children: {
                field: { component: 'sw-field' },
                other: { component: 'sw-other' },
            },
        },
    },
		});"#,
    );
    assert_eq!(fixture.indexer.get_all_module_routes().unwrap().len(), 3);
    let owner = fixture
        .indexer
        .get_component_by_template_path(template_path)
        .unwrap()
        .expect("owner");
    assert_eq!(owner.name, "sw-host");
    let route_document = document(
        &swlsp_uriutil::file_uri(template_path),
        r#"<router-view v-slot="{ Component }"><component :is="Component" /></router-view>"#,
    );
    let root = route_document.root().unwrap();
    let (start_tag, selector) = twigquery::nodes(&root, &[twigsyntax::HTML_STARTING_TAG])
        .into_iter()
        .find_map(|tag| twig_dynamic_component_selector(&tag).map(|selector| (tag, selector)))
        .expect("dynamic tag");
    let (resolved, components, complete) = fixture
        .indexer
        .resolve_dynamic_component_contracts(template_path, &selector, Some(&start_tag))
        .unwrap();
    assert!(complete);
    assert_eq!(resolved.names(), ["sw-field", "sw-other"]);
    assert_eq!(components.len(), 2);

    let analyzer = fixture.analyzer();
    for markup in [
        r#"<component :is="'{|admin.component.missing-required-prop:sw-field|}'" />"#,
        r#"<component :is="'sw-field'" title="Field" {|admin.component.static-prop-type:count="5"|} />"#,
        r#"<component :is="active ? '{|admin.component.missing-required-prop:sw-field|}' : 'sw-other'" />"#,
        r#"<component :is="'{|admin.component.not-found:sw-fiel|}'" />"#,
        r#"<component :is="{|admin.component.missing-required-prop:dynamicField|}" />"#,
        r#"<router-view v-slot="{ Component }"><component :is="{|admin.component.missing-required-prop:Component|}" /></router-view>"#,
    ] {
        check(&analyzer, &swlsp_uriutil::file_uri(template_path), markup);
    }
}

#[test]
fn admin_analyzer_bound_prop_types() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("Resources/app/administration/src/component/sw-host/view.html.twig");
    let definition_path =
        fixture.path("Resources/app/administration/src/component/sw-host/index.ts");
    let field_path = fixture.path("Resources/app/administration/src/component/sw-field/index.ts");
    fixture.save(vue_component! {
        name: "sw-field".into(),
        file_path: field_path.clone(),
        definition_path: field_path.clone(),
        props: vec![
            prop("count", "Number", false),
            prop("title", "String", false),
            prop("active", "Boolean", false),
            prop("rows", "Array as PropType<Row[]>", false),
            prop("config", "Object as PropType<Config>", false),
            prop("callback", "Function", false),
        ],
    });
    use VueComponentMemberKind::{Data, Method};
    fixture.save(vue_component! {
        name: "sw-host".into(),
        definition_path: definition_path.clone(),
        file_path: definition_path.clone(),
        template_path: template_path.clone(),
        members: vec![
            member("count", Data, "number"),
            member("title", Data, "string"),
            member("rows", Data, "Row[]"),
            member("config", Data, "{ enabled: boolean }"),
            member("callback", Method, "() => void"),
            member("maybeTitle", Data, "string | null"),
            member("mutableValue", Data, "boolean"),
        ],
        assignments: vec![VueComponentAssignment {
            target: "mutableValue".into(),
            expression: "nextValue".into(),
            ..VueComponentAssignment::default()
        }],
    });
    let cases: &[(&str, Option<(&str, &str)>)] = &[
        (
            r#"<sw-field :count="{|admin.component.bound-prop-type:'wrong'|}" />"#,
            Some(("number", "string")),
        ),
        (
            r#"<sw-field :active="{|admin.component.bound-prop-type:title|}" />"#,
            Some(("boolean", "string")),
        ),
        (
            r#"<sw-field :rows="{|admin.component.bound-prop-type:title|}" />"#,
            Some(("Row[]", "string")),
        ),
        (
            r#"<sw-field :config="{|admin.component.bound-prop-type:rows|}" />"#,
            Some(("Config", "Row[]")),
        ),
        (
            r#"<sw-field :callback="{|admin.component.bound-prop-type:count|}" />"#,
            Some(("Function", "number")),
        ),
        (
            r#"<sw-field :count="count" :title="title" :rows="rows" :config="config" :callback="callback" />"#,
            None,
        ),
        (
            r#"<sw-field v-bind="{ count, title, rows, config, callback }" />"#,
            None,
        ),
        (
            r#"<sw-field v-bind="{ count: {|admin.component.bound-prop-type:title|} }" />"#,
            Some(("number", "string")),
        ),
        (r#"<sw-field v-bind="{ ...runtimeProps, count }" />"#, None),
        (r#"<sw-field :title="maybeTitle" />"#, None),
        (r#"<sw-field :count="runtimeValue" />"#, None),
        (r#"<sw-field :count="mutableValue" />"#, None),
    ];
    for (markup, expected) in cases {
        let problems = fixture.check_path(&template_path, markup);
        if let Some((expected, actual)) = expected {
            assert_eq!(
                payload_str(&problems[0], "expectedType"),
                *expected,
                "{markup}"
            );
            assert_eq!(payload_str(&problems[0], "actualType"), *actual, "{markup}");
        }
    }
}

#[test]
fn admin_analyzer_unknown_component_contract_attributes() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("Resources/app/administration/src/component/sw-host/view.html.twig");
    fixture.save(vue_component! {
        name: "sw-field".into(),
        file_path: fixture.path("sw-field.ts"),
        props: vec![
            prop("label", "String", false),
            prop("isLoading", "Boolean", false),
            prop("count", "Number", false),
            prop("checked", "Boolean", false),
        ],
        events: vec![event("itemClick", ""), event("update:checked", "")],
    });
    fixture.save(vue_component! {
        name: "sw-secondary".into(),
        file_path: fixture.path("sw-secondary.ts"),
        props: vec![prop("secondary", "String", false)],
    });
    let cases: &[(&str, Option<&str>)] = &[
        (
            r#"<sw-field {|admin.component.unknown-prop:lable|}="Title" />"#,
            Some("label"),
        ),
        (
            r#"<sw-field :{|admin.component.unknown-prop:is-laoding|}.sync="ready" />"#,
            Some("is-loading"),
        ),
        (
            r#"<sw-field v-bind:{|admin.component.unknown-prop:lable|}.prop="title" />"#,
            Some("label"),
        ),
        (
            r#"<sw-field v-bind="{ {|admin.component.unknown-prop:lable|}: title }" />"#,
            Some("label"),
        ),
        (
            r#"<sw-field v-bind="{ '{|admin.component.unknown-prop:is-laoding|}': ready }" />"#,
            Some("is-loading"),
        ),
        (
            r#"<sw-field @{|admin.component.unknown-event:item-clik|}.stop="select" />"#,
            Some("item-click"),
        ),
        (
            r#"<sw-field v-on:{|admin.component.unknown-event:itemClik|}.once="select" />"#,
            Some("item-click"),
        ),
        (
            r#"<sw-field v-model:{|admin.component.unknown-model:cheked|}.trim="checked" />"#,
            Some("checked"),
        ),
        (r#"<sw-field v-model:checked.trim="checked" />"#, None),
        (
            r#"<sw-field label="Title" @item-click="select" @click="open" class="field" :data-test="id" aria-label="Field" v-if="visible" />"#,
            None,
        ),
        (
            r#"<sw-field mystery-attribute="value" @runtime-event="run" />"#,
            None,
        ),
        (
            r#"<component :is="condition ? 'sw-field' : 'sw-secondary'" :{|admin.component.unknown-prop:lable|}="title" />"#,
            Some("label"),
        ),
        (
            r#"<component :is="condition ? 'sw-field' : 'sw-secondary'" :label="title" />"#,
            None,
        ),
        (
            r#"<component :is="runtimeComponent" :lable="title" @item-clik="select" />"#,
            None,
        ),
    ];
    for (markup, expected) in cases {
        let problems = fixture.check_path(&template_path, markup);
        if let Some(suggestion) = expected {
            assert!(
                suggestions(&problems[0]).contains(&suggestion.to_string()),
                "{markup}"
            );
        }
    }
}

#[test]
fn admin_analyzer_unknown_component_slot_names() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("Resources/app/administration/src/component/sw-host/view.html.twig");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: fixture.path("sw-card.ts"),
        slots: vec![slot("header", ""), slot("actions", ""), slot("", "column-")],
    });
    fixture.save(vue_component! {
        name: "sw-secondary".into(),
        file_path: fixture.path("sw-secondary.ts"),
        slots: vec![slot("actions", ""), slot("footer", "")],
    });
    let cases: &[(&str, Option<&str>)] = &[
        (
            "<sw-card><template #{|admin.component.unknown-slot:heder|}>Title</template></sw-card>",
            Some("header"),
        ),
        (
            "<sw-card><template v-slot:{|admin.component.unknown-slot:actons|}.stop>Actions</template></sw-card>",
            Some("actions"),
        ),
        (
            "<sw-card #{|admin.component.unknown-slot:heder|}>Title</sw-card>",
            Some("header"),
        ),
        (
            "<sw-card><template #header>Title</template><template #column-name>Cell</template></sw-card>",
            None,
        ),
        (
            "<sw-card><template #plugin-content>Content</template></sw-card>",
            None,
        ),
        (
            r#"<component :is="condition ? 'sw-card' : 'sw-secondary'"><template #{|admin.component.unknown-slot:heder|}>Title</template></component>"#,
            Some("header"),
        ),
        (
            r#"<component :is="condition ? 'sw-card' : 'sw-secondary'"><template #header>Title</template></component>"#,
            None,
        ),
        (
            r#"<component :is="runtimeComponent"><template #heder>Title</template></component>"#,
            None,
        ),
    ];
    for (markup, expected) in cases {
        let problems = fixture.check_path(&template_path, markup);
        if let Some(suggestion) = expected {
            assert!(suggestions(&problems[0]).contains(&suggestion.to_string()));
        }
    }
}

#[test]
fn admin_analyzer_model_bindings() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("Resources/app/administration/src/component/sw-host/view.html.twig");
    let host_path = fixture.path("Resources/app/administration/src/component/sw-host/index.ts");
    let field_path = fixture.path("Resources/app/administration/src/component/sw-field/index.ts");
    fixture.save(vue_component! {
        name: "sw-field".into(),
        file_path: field_path.clone(),
        definition_path: field_path.clone(),
        props: vec![prop("modelValue", "Boolean", true), prop("value", "String", true)],
        events: vec![
            event("update:modelValue", "(value: boolean) => void"),
            event("update:value", "(value: string) => void"),
        ],
    });
    use VueComponentMemberKind::{Data, Method};
    fixture.save(vue_component! {
        name: "sw-host".into(),
        file_path: host_path.clone(),
        definition_path: host_path.clone(),
        template_path: template_path.clone(),
        members: vec![
            member("enabled", Data, "boolean"),
            member("title", Data, "string"),
            member("getValue", Method, "() => boolean"),
        ],
    });
    fixture.save(vue_component! {
        name: "sw-partial-model".into(),
        file_path: format!("{field_path}.partial"),
        props: vec![prop("element", "Object", true)],
    });
    for markup in [
        r#"<sw-field v-model="enabled" v-model:value="title" />"#,
        r#"<component :is="'sw-partial-model'" v-model:element="title" />"#,
        r#"<sw-field v-model="{|admin.component.model-type:title|}" v-model:value="title" />"#,
        r#"<sw-field v-model="enabled" v-model:value="{|admin.component.model-type:enabled|}" />"#,
        r#"<sw-field v-model="{|admin.component.model-not-assignable:true|}" v-model:value="title" />"#,
        r#"<sw-field v-model="{|admin.component.model-not-assignable:getValue()|}" v-model:value="title" />"#,
    ] {
        fixture.check_path(&template_path, markup);
    }
}
