//! Ports of `admin_diagnostics_test.go` (part 2).

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
    let cases: &[(&str, usize, &[&str])] = &[
        ("<sw-button></sw-button>", 2, &["label", "variant"]),
        (
            r#"<sw-button label="Click me"></sw-button>"#,
            1,
            &["variant"],
        ),
        (
            r#"<sw-button label="Click me" variant="primary"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button :label="buttonLabel" variant="primary"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button v-bind:label="buttonLabel" variant="primary"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button v-bind="{ label: buttonLabel, variant }"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button v-bind="{ label: buttonLabel }"></sw-button>"#,
            1,
            &["variant"],
        ),
        (
            r#"<sw-button v-bind="{}"></sw-button>"#,
            2,
            &["label", "variant"],
        ),
        (
            r#"<sw-button v-bind="filteredAttributes"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button v-bind="{ label, ...forwarded }"></sw-button>"#,
            0,
            &[],
        ),
        (
            r#"<sw-button label="Click" variant="primary"></sw-button>"#,
            0,
            &[],
        ),
        (r#"<sw-butotn required-prop="value"></sw-butotn>"#, 1, &[]),
        (
            r#"<plugin-unknown required-prop="value"></plugin-unknown>"#,
            0,
            &[],
        ),
        (r#"<div class="test"></div>"#, 0, &[]),
    ];
    for (markup, count, props) in cases {
        let problems = analyze(
            &analyzer,
            &document(
                "file:///project/src/Resources/app/administration/src/views/test.html.twig",
                markup,
            ),
        );
        assert_eq!(problems.len(), *count, "{markup}: {problems:?}");
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
    let problems = analyze(
        &fixture.analyzer(),
        &document(
            &format!("file://{admin_root}/view.html.twig"),
            "<sw-butotn></sw-butotn><plugin-widget></plugin-widget>",
        ),
    );
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.0, "admin.component.not-found");
    assert!(problems[0].message.contains("sw-butotn"));
    assert!(suggestions(&problems[0]).contains(&"sw-button".to_owned()));
}

#[test]
fn admin_analyzer_unknown_shopware_component_needs_indexed_catalog() {
    let fixture = Fixture::in_root();
    let problems = analyze(
        &fixture.analyzer(),
        &document(
            "file:///project/Resources/app/administration/src/view.html.twig",
            "<sw-unknown></sw-unknown>",
        ),
    );
    assert!(problems.is_empty());
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
    let problems = analyze(
        &fixture.analyzer(),
        &document(
            &format!("file://{template_path}"),
            "<mt-card-original></mt-card-original><mt-other-original></mt-other-original>",
        ),
    );
    assert_eq!(problems.len(), 1, "{problems:?}");
    assert_eq!(problems[0].id.0, "admin.component.not-found");
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
    for (markup, count) in [
        (r#"<mt-card positionIdentifier="test"></mt-card>"#, 0),
        (r#"<mt-card position-identifier="test"></mt-card>"#, 0),
        (r#"<mt-card :position-identifier="myVar"></mt-card>"#, 0),
        ("<mt-card></mt-card>", 1),
    ] {
        let problems = analyze(
            &analyzer,
            &document(
                "file:///project/src/Resources/app/administration/src/views/test.html.twig",
                markup,
            ),
        );
        assert_eq!(problems.len(), count, "{markup}");
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
    let problems = analyze(
        &fixture.analyzer(),
        &document(VIEW_URI, r#"<sw-child child-label="Child"></sw-child>"#),
    );
    assert_eq!(problems.len(), 1, "{problems:?}");
    assert_eq!(problems[0].id.0, "admin.component.missing-required-prop");
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
    for (markup, count) in [
        (r#"<sw-field count="5"></sw-field>"#, 1),
        (r#"<sw-field :count="5"></sw-field>"#, 0),
        ("<sw-field active></sw-field>", 0),
        (r#"<sw-field active=""></sw-field>"#, 0),
        (r#"<sw-field active="false"></sw-field>"#, 1),
        (r#"<sw-field label="Name"></sw-field>"#, 0),
    ] {
        let problems = analyze(&analyzer, &document(VIEW_URI, markup));
        assert_eq!(problems.len(), count, "{markup}");
        if count != 0 {
            assert_eq!(problems[0].id.0, "admin.component.static-prop-type");
        }
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
    for (name, markup, count) in [
        ("allowed value", r#"<sw-label variant="primary" />"#, 0),
        ("allowed empty value", r#"<sw-label variant="" />"#, 0),
        (
            "invalid options value",
            r#"<sw-label variant="primry" />"#,
            1,
        ),
        (
            "invalid literal union value",
            r#"<sw-label size="medium" />"#,
            1,
        ),
        (
            "invalid empty literal union value",
            r#"<sw-label size="" />"#,
            1,
        ),
        (
            "invalid bound literal union value",
            r#"<sw-label :size="'medium'" />"#,
            1,
        ),
        (
            "valid bound literal union value",
            r#"<sw-label :size="'small'" />"#,
            0,
        ),
        (
            "invalid object-bound literal union value",
            r#"<sw-label v-bind="{ size: 'medium' }" />"#,
            1,
        ),
        (
            "bound value is runtime",
            r#"<sw-label :variant="selectedVariant" />"#,
            0,
        ),
        (
            "partial union stays conservative",
            r#"<sw-label legacy="other" />"#,
            0,
        ),
    ] {
        let problems = analyze(&analyzer, &document(VIEW_URI, markup));
        assert_eq!(problems.len(), count, "{name}: {problems:?}");
        if count > 0 {
            assert_eq!(problems[0].id.0, "admin.component.invalid-prop-value");
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
    for (markup, id) in [
        (
            r#"<component :is="'sw-field'" />"#,
            "admin.component.missing-required-prop",
        ),
        (
            r#"<component :is="'sw-field'" title="Field" count="5" />"#,
            "admin.component.static-prop-type",
        ),
        (
            r#"<component :is="active ? 'sw-field' : 'sw-other'" />"#,
            "admin.component.missing-required-prop",
        ),
        (
            r#"<component :is="'sw-fiel'" />"#,
            "admin.component.not-found",
        ),
        (
            r#"<component :is="dynamicField" />"#,
            "admin.component.missing-required-prop",
        ),
        (
            r#"<router-view v-slot="{ Component }"><component :is="Component" /></router-view>"#,
            "admin.component.missing-required-prop",
        ),
    ] {
        let problems = analyze(
            &analyzer,
            &document(&swlsp_uriutil::file_uri(template_path), markup),
        );
        assert_eq!(problems.len(), 1, "{markup}: {problems:?}");
        assert_eq!(problems[0].id.0, id, "{markup}");
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
    let analyzer = fixture.analyzer();
    let cases: &[(&str, Option<(&str, &str)>)] = &[
        (
            r#"<sw-field :count="'wrong'" />"#,
            Some(("number", "string")),
        ),
        (
            r#"<sw-field :active="title" />"#,
            Some(("boolean", "string")),
        ),
        (r#"<sw-field :rows="title" />"#, Some(("Row[]", "string"))),
        (r#"<sw-field :config="rows" />"#, Some(("Config", "Row[]"))),
        (
            r#"<sw-field :callback="count" />"#,
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
            r#"<sw-field v-bind="{ count: title }" />"#,
            Some(("number", "string")),
        ),
        (r#"<sw-field v-bind="{ ...runtimeProps, count }" />"#, None),
        (r#"<sw-field :title="maybeTitle" />"#, None),
        (r#"<sw-field :count="runtimeValue" />"#, None),
        (r#"<sw-field :count="mutableValue" />"#, None),
    ];
    for (markup, expected) in cases {
        let problems = analyze(
            &analyzer,
            &document(&format!("file://{template_path}"), markup),
        );
        match expected {
            None => assert!(problems.is_empty(), "{markup}: {problems:?}"),
            Some((expected, actual)) => {
                assert_eq!(problems.len(), 1, "{markup}: {problems:?}");
                assert_eq!(problems[0].id.0, "admin.component.bound-prop-type");
                assert_eq!(
                    payload_str(&problems[0], "expectedType"),
                    *expected,
                    "{markup}"
                );
                assert_eq!(payload_str(&problems[0], "actualType"), *actual, "{markup}");
            }
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
    let analyzer = fixture.analyzer();
    type Expected<'a> = Option<(&'a str, &'a str, &'a str)>;
    let cases: &[(&str, Expected)] = &[
        (
            r#"<sw-field lable="Title" />"#,
            Some(("admin.component.unknown-prop", "lable", "label")),
        ),
        (
            r#"<sw-field :is-laoding.sync="ready" />"#,
            Some(("admin.component.unknown-prop", "is-laoding", "is-loading")),
        ),
        (
            r#"<sw-field v-bind:lable.prop="title" />"#,
            Some(("admin.component.unknown-prop", "lable", "label")),
        ),
        (
            r#"<sw-field v-bind="{ lable: title }" />"#,
            Some(("admin.component.unknown-prop", "lable", "label")),
        ),
        (
            r#"<sw-field v-bind="{ 'is-laoding': ready }" />"#,
            Some(("admin.component.unknown-prop", "is-laoding", "is-loading")),
        ),
        (
            r#"<sw-field @item-clik.stop="select" />"#,
            Some(("admin.component.unknown-event", "item-clik", "item-click")),
        ),
        (
            r#"<sw-field v-on:itemClik.once="select" />"#,
            Some(("admin.component.unknown-event", "itemClik", "item-click")),
        ),
        (
            r#"<sw-field v-model:cheked.trim="checked" />"#,
            Some(("admin.component.unknown-model", "cheked", "checked")),
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
            r#"<component :is="condition ? 'sw-field' : 'sw-secondary'" :lable="title" />"#,
            Some(("admin.component.unknown-prop", "lable", "label")),
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
        let problems = analyze(
            &analyzer,
            &document(&format!("file://{template_path}"), markup),
        );
        match expected {
            None => assert!(problems.is_empty(), "{markup}: {problems:?}"),
            Some((code, range_text, suggestion)) => {
                assert_eq!(problems.len(), 1, "{markup}: {problems:?}");
                assert_eq!(problems[0].id.0, *code, "{markup}");
                assert_eq!(slice(markup, problems[0].range), *range_text, "{markup}");
                assert!(
                    suggestions(&problems[0]).contains(&suggestion.to_string()),
                    "{markup}"
                );
            }
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
    let analyzer = fixture.analyzer();
    let cases: &[(&str, Option<(&str, &str)>)] = &[
        (
            "<sw-card><template #heder>Title</template></sw-card>",
            Some(("heder", "header")),
        ),
        (
            "<sw-card><template v-slot:actons.stop>Actions</template></sw-card>",
            Some(("actons", "actions")),
        ),
        ("<sw-card #heder>Title</sw-card>", Some(("heder", "header"))),
        (
            "<sw-card><template #header>Title</template><template #column-name>Cell</template></sw-card>",
            None,
        ),
        (
            "<sw-card><template #plugin-content>Content</template></sw-card>",
            None,
        ),
        (
            r#"<component :is="condition ? 'sw-card' : 'sw-secondary'"><template #heder>Title</template></component>"#,
            Some(("heder", "header")),
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
        let problems = analyze(
            &analyzer,
            &document(&format!("file://{template_path}"), markup),
        );
        match expected {
            None => assert!(problems.is_empty(), "{markup}: {problems:?}"),
            Some((range_text, suggestion)) => {
                assert_eq!(problems.len(), 1, "{markup}: {problems:?}");
                assert_eq!(problems[0].id.0, "admin.component.unknown-slot");
                assert_eq!(slice(markup, problems[0].range), *range_text);
                assert!(suggestions(&problems[0]).contains(&suggestion.to_string()));
            }
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
    let analyzer = fixture.analyzer();
    for (markup, id) in [
        (
            r#"<sw-field v-model="enabled" v-model:value="title" />"#,
            "",
        ),
        (
            r#"<component :is="'sw-partial-model'" v-model:element="title" />"#,
            "",
        ),
        (
            r#"<sw-field v-model="title" v-model:value="title" />"#,
            "admin.component.model-type",
        ),
        (
            r#"<sw-field v-model="enabled" v-model:value="enabled" />"#,
            "admin.component.model-type",
        ),
        (
            r#"<sw-field v-model="true" v-model:value="title" />"#,
            "admin.component.model-not-assignable",
        ),
        (
            r#"<sw-field v-model="getValue()" v-model:value="title" />"#,
            "admin.component.model-not-assignable",
        ),
    ] {
        let problems = analyze(
            &analyzer,
            &document(&format!("file://{template_path}"), markup),
        );
        if id.is_empty() {
            assert!(problems.is_empty(), "{markup}: {problems:?}");
        } else {
            assert_eq!(problems.len(), 1, "{markup}: {problems:?}");
            assert_eq!(problems[0].id.0, id, "{markup}");
        }
    }
}
