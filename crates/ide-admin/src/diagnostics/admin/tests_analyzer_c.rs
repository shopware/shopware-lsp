//! Tests of the Administration analyzer (part 3).

use std::collections::HashMap;

use swlsp_admin::{
    ComponentRegistrationKind, TwigBlock, TwigBlockScopeMember, VueComponentMember,
    VueComponentMemberKind, VueComponentSlot, VueComponentSlotMember, camel_to_kebab,
};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};

use super::test_fixture::*;

fn member(name: &str, kind: VueComponentMemberKind, type_: &str) -> VueComponentMember {
    VueComponentMember {
        name: name.into(),
        kind,
        type_: type_.into(),
        ..VueComponentMember::default()
    }
}

fn slot_member(name: &str, type_: &str) -> VueComponentSlotMember {
    VueComponentSlotMember {
        name: name.into(),
        type_: type_.into(),
        ..VueComponentSlotMember::default()
    }
}

fn content_slot(complete: bool, members: Vec<VueComponentSlotMember>) -> VueComponentSlot {
    VueComponentSlot {
        name: "content".into(),
        members_complete: complete,
        members,
        ..VueComponentSlot::default()
    }
}

fn block(name: &str, line: u32) -> TwigBlock {
    TwigBlock {
        name: name.into(),
        line,
        ..TwigBlock::default()
    }
}

fn string_list(problem: &swlsp_lsp::Problem, key: &str) -> Vec<String> {
    match problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
    {
        Some(serde_json::Value::Array(values)) => values
            .iter()
            .filter_map(|value| value.as_str().map(str::to_owned))
            .collect(),
        _ => Vec::new(),
    }
}

#[test]
fn admin_analyzer_unknown_whole_slot_object_member() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "sw-inherit-wrapper".into(),
        file_path: fixture.path("wrapper.js"),
        slots: vec![content_slot(
            true,
            vec![slot_member("currentValue", "string"), slot_member("isInherited", "boolean")],
        )],
    });
    fixture.save(vue_component! {
        name: "sw-forwarding-wrapper".into(),
        file_path: fixture.path("forward.js"),
        slots: vec![content_slot(false, vec![slot_member("known", "")])],
    });
    fixture.save(vue_component! {
        name: "sw-card-a".into(),
        file_path: fixture.path("a.js"),
        slots: vec![content_slot(
            true,
            vec![slot_member("currentValue", "string"), slot_member("onlyA", "boolean")],
        )],
    });
    fixture.save(vue_component! {
        name: "sw-card-b".into(),
        file_path: fixture.path("b.js"),
        slots: vec![content_slot(true, vec![slot_member("currentValue", "number")])],
    });
    let analyzer = fixture.analyzer();
    let cases: &[(&str, bool)] = &[
        (
            r#"<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>"#,
            false,
        ),
        (
            r#"<sw-inherit-wrapper><template #content="props">{{ props.{|admin.component.unknown-slot-prop:curentValue|} }}</template></sw-inherit-wrapper>"#,
            true,
        ),
        (
            r#"<sw-inherit-wrapper><template #content="{ {|admin.component.unknown-slot-prop:curentValue|} }">{{ curentValue }}</template></sw-inherit-wrapper>"#,
            true,
        ),
        (
            r#"<sw-inherit-wrapper><template #content="props"><div v-for="props in rows">{{ props.anything }}</div></template></sw-inherit-wrapper>"#,
            false,
        ),
        (
            r#"<sw-forwarding-wrapper><template #content="props">{{ props.anything }}</template></sw-forwarding-wrapper>"#,
            false,
        ),
        (
            r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #content="props">{{ props.currentValue }}</template></component>"#,
            false,
        ),
        (
            r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #content="props">{{ props.{|admin.component.unknown-slot-prop:onlyA|} }}</template></component>"#,
            false,
        ),
        (
            r#"<component :is="runtimeComponent"><template #content="props">{{ props.anything }}</template></component>"#,
            false,
        ),
    ];
    for (source, unknown) in cases {
        let problems = check(
            &analyzer,
            "file:///project/Resources/app/administration/src/view.html.twig",
            source,
        );
        if problems.is_empty() {
            continue;
        }
        if *unknown {
            assert_eq!(payload_str(&problems[0], "memberName"), "curentValue");
            assert!(suggestions(&problems[0]).contains(&"currentValue".to_owned()));
        } else {
            assert_eq!(payload_str(&problems[0], "memberName"), "onlyA");
            let mut names = string_list(&problems[0], "componentNames");
            names.sort();
            assert_eq!(names, ["sw-card-a", "sw-card-b"]);
        }
    }
}

#[test]
fn admin_analyzer_unknown_typed_vue_binding_member() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src/component/sw-view");
    let definition_path = format!("{admin_root}/index.ts");
    let template_path = format!("{admin_root}/view.html.twig");
    fixture.index(
        &definition_path,
        "\ninterface Manufacturer { name: string; }\ninterface Product { id: string; name: string; manufacturer: Manufacturer; getManufacturer(): Manufacturer; }\n",
    );
    use VueComponentMemberKind::{Computed, Prop};
    let with_path = |mut value: VueComponentMember| {
        value.file_path = definition_path.clone();
        value
    };
    fixture.save(vue_component! {
        name: "sw-view".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        members: vec![
            with_path(member("products", Computed, "Product[]")),
            with_path(member("productsById", Computed, "Record<string, Product>")),
            with_path(member("unknownRows", Computed, "Array")),
            with_path(member("selectedProduct", Prop, "Product")),
            with_path(member("cards", Computed, "Array<{ id: string; label: string }>")),
        ],
    });
    let analyzer = fixture.analyzer();
    let cases: &[(&str, &str)] = &[
        (
            r#"<div v-for="product in products">{{ product.manufacturer.name }}</div>"#,
            "",
        ),
        (
            r#"<div v-for="name in products?.map((product) => product.name) ?? []">{{ name.length }}</div>"#,
            "",
        ),
        (
            r#"<div v-for="card in products.map((product) => ({ label: product.name }))">{{ card.{|admin.component.unknown-vue-member:lable|} }}</div>"#,
            "label",
        ),
        (
            r#"<div v-for="product in products">{{ product.{|admin.component.unknown-vue-member:manufaturer|}.name }}</div>"#,
            "manufacturer",
        ),
        (
            r#"<div v-for="product in products">{{ product.manufacturer.{|admin.component.unknown-vue-member:naem|} }}</div>"#,
            "name",
        ),
        (
            r#"<div v-for="(product, productId, index) in productsById">{{ product.{|admin.component.unknown-vue-member:naem|} }}</div>"#,
            "name",
        ),
        (
            r#"<div v-for="(product, productId, index) in productsById">{{ productId.length }} {{ index.toFixed() }}</div>"#,
            "",
        ),
        (
            r#"<div v-for="product in Object.values(productsById)">{{ product.{|admin.component.unknown-vue-member:naem|} }}</div>"#,
            "name",
        ),
        (
            r#"<div v-for="productId in Object.keys(productsById)">{{ productId.length }}</div><div v-for="label in ['first']">{{ label.toUpperCase() }}</div>"#,
            "",
        ),
        (
            r#"<div v-for="(product, index) in products">{{ products[0].manufacturer.name }} {{ products[index].name }} {{ products[index - 1].name }} {{ productsById[selectedProduct.name].name }}</div>"#,
            "",
        ),
        (
            r#"<div v-for="(product, index) in products">{{ products[index].{|admin.component.unknown-vue-member:naem|} }}</div>"#,
            "name",
        ),
        (
            r#"<div>{{ productsById[selectedProduct.name].{|admin.component.unknown-vue-member:naem|} }}</div>"#,
            "name",
        ),
        (
            r#"<div :title="selectedProduct.manufacturer.name"></div>"#,
            "",
        ),
        (r#"<div>{{ cards.length }} {{ cards[0].id }}</div>"#, ""),
        (
            r#"<div :title="selectedProduct.{|admin.component.unknown-vue-member:naem|}"></div>"#,
            "name",
        ),
        (
            r#"<div :title="selectedProduct.manufacturer.{|admin.component.unknown-vue-member:naem|}"></div>"#,
            "name",
        ),
        (
            r#"<div :title="selectedProduct.getManufacturer().name"></div>"#,
            "",
        ),
        (
            r#"<div :title="selectedProduct.{|admin.component.unknown-vue-member:getManufacturr|}().name"></div>"#,
            "getManufacturer",
        ),
        (
            r#"<div :title="selectedProduct.getManufacturer().{|admin.component.unknown-vue-member:naem|}"></div>"#,
            "name",
        ),
        (
            r#"<div v-for="row in unknownRows">{{ row.anything }}</div>"#,
            "",
        ),
    ];
    let uri = swlsp_uriutil::file_uri(&template_path);
    for (source, suggestion) in cases {
        let problems = check(&analyzer, &uri, source);
        if suggestion.is_empty() {
            continue;
        }
        assert!(
            suggestions(&problems[0]).contains(&suggestion.to_string()),
            "{source}"
        );
    }
}

#[test]
fn admin_analyzer_keeps_inferred_runtime_object_shapes_open() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("Resources/app/administration/src/component/sw-view/view.html.twig");
    let definition_path =
        fixture.path("Resources/app/administration/src/component/sw-view/index.ts");
    let mut page = member("page", VueComponentMemberKind::Data, "{ sections: Array }");
    page.file_path = definition_path.clone();
    page.open_runtime_shape = true;
    let mut declared = member(
        "declared",
        VueComponentMemberKind::Prop,
        "{ title: string }",
    );
    declared.file_path = definition_path.clone();
    fixture.save(vue_component! {
        name: "sw-view".into(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        members: vec![page, declared],
    });
    let problems = fixture.check_path(
        &template_path,
        "<div>{{ page.locked }} {{ declared.{|admin.component.unknown-vue-member:titel|} }}</div>",
    );
    assert!(problems[0].message.contains("titel"));
    assert!(!problems[0].message.contains("locked"));
}

#[test]
fn admin_analyzer_unknown_this_member() {
    let fixture = Fixture::in_root();
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/index.js");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        props: vec![prop("title", "", false)],
        injected: vec!["repositoryFactory".to_owned()],
    });
    let source = "export default {
    props: { title: String },
    methods: {
        freshMethod() {},
        run() {
            this.title;
            this.repositoryFactory.create('product');
            this.freshMethod();
            this.$emit('save');
\t\t\tthis.runtimeValue = 1;
\t\t\tthis.runtimeValue;
\t\t\tthis.{|admin.component.unknown-instance-member:freshMethd|};
        },
    },
};";
    let problems = fixture.check_path(&definition_path, source);
    assert!(problems[0].message.contains("freshMethd"));
    assert!(suggestions(&problems[0]).contains(&"freshMethod".to_owned()));
}

#[test]
fn admin_analyzer_treats_inherited_assignments_as_instance_members() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/component/sw-card/index.js"),
        "Component.register('sw-card', {\n    mounted() { this.runtimeHandle = window.setInterval(() => {}, 1000); },\n});",
    );
    let override_path = format!("{admin_root}/extension/sw-card/index.js");
    let source = "Component.override('sw-card', {
    methods: {
        stop() {
            window.clearInterval(this.runtimeHandle);
            this.{|admin.component.unknown-instance-member:runtimeHandel|};
        },
    },
});";
    let (plain, _) = swlsp_lsp::fixture::parse(source);
    fixture.index(&override_path, &plain);
    let problems = fixture.check_path(&override_path, source);
    assert!(problems[0].message.contains("runtimeHandel"));
    assert!(suggestions(&problems[0]).contains(&"runtimeHandle".to_owned()));
}

#[test]
fn admin_analyzer_scopes_unknown_instance_members_per_component() {
    let fixture = Fixture::new();
    let definition_path = fixture.path("src/Resources/app/administration/src/component/index.js");
    let source = "Component.register('sw-first', {
    methods: {
        firstOnly() {},
        run() { this.{|admin.component.unknown-instance-member:secondOnly|}(); },
    },
});
Component.register('sw-second', {
    methods: {
        secondOnly() {},
        run() { this.{|admin.component.unknown-instance-member:firstOnly|}(); },
    },
});";
    let (plain, _) = swlsp_lsp::fixture::parse(source);
    fixture.index(&definition_path, &plain);
    let problems = fixture.check_path(&definition_path, source);
    assert!(problems[0].message.contains("secondOnly"));
    assert!(problems[1].message.contains("firstOnly"));
}

#[test]
fn admin_analyzer_suppresses_unknown_members_for_open_runtime_component() {
    let fixture = Fixture::new();
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/index.js");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        open_runtime_members: true,
    });
    fixture.check_path(
        &definition_path,
        "export default {\n    methods: { run() { this.runtimePluginMember(); } },\n};",
    );
}

#[test]
fn admin_analyzer_unknown_template_root_member_with_suggestion() {
    let fixture = Fixture::new();
    let template_path = fixture.path(
        "src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
    );
    use VueComponentMemberKind::{Data, Method};
    fixture.save(vue_component! {
        name: "sw-card".into(),
        template_path: template_path.clone(),
        file_path: fixture.path(
            "src/Administration/Resources/app/administration/src/component/sw-card/index.ts",
        ),
        members: vec![
            member("rows", Data, "Array<Row>"),
            member("getSlots", Method, "() => Array<string>"),
        ],
        blocks: vec![TwigBlock {
            scope_members: vec![TwigBlockScopeMember {
                name: "item".into(),
                ..TwigBlockScopeMember::default()
            }],
            ..block("sw_card_row", 0)
        }],
    });
    let analyzer = fixture.analyzer();
    for (source, suggestion) in [
        (
            "{{ {|admin.component.unknown-template-member:getSlos|}() }}",
            "getSlots",
        ),
        (
            "{{ {|admin.component.unknown-template-member:Objcet|}.keys(rows) }}",
            "Object",
        ),
        (
            "{{ getSlots() }} {{ Object.keys(rows) }} {{ $t('key') }}",
            "",
        ),
        (
            r#"<div v-for="row in rows" @click="getSlots($event)">{{ row.name }}</div>"#,
            "",
        ),
        ("{{ runtimePluginValue() }}", ""),
        ("{% block sw_card_row %}{{ item.name }}{% endblock %}", ""),
    ] {
        let problems = check(&analyzer, &swlsp_uriutil::file_uri(&template_path), source);
        if suggestion.is_empty() {
            continue;
        }
        assert!(suggestions(&problems[0]).contains(&suggestion.to_owned()));
    }
}

#[test]
fn admin_analyzer_keeps_runtime_open_template_scope_conservative() {
    let fixture = Fixture::new();
    let template_path = fixture.path("Resources/app/administration/src/sw-flow.html.twig");
    fixture.save(vue_component! {
        name: "sw-flow".into(),
        template_path: template_path.clone(),
        file_path: fixture.path("Resources/app/administration/src/index.ts"),
        open_runtime_members: true,
        members: vec![member("flowId", VueComponentMemberKind::Prop, "")],
    });
    let problems = fixture.check_path(
        &template_path,
        "{{ flow.name }} {{ {|admin.component.unknown-template-member:Objcet|}.keys(flow) }}",
    );
    assert!(suggestions(&problems[0]).contains(&"Object".to_owned()));
}

#[test]
fn admin_analyzer_treats_setup_returns_as_instance_members() {
    let fixture = Fixture::new();
    let definition_path = fixture
        .path("src/Administration/Resources/app/administration/src/component/sw-setup/index.ts");
    let source = "Shopware.Component.register('sw-setup', Shopware.Component.wrapComponentConfig({
    setup() {
        const parent = computed(() => null);
        const open = () => true;
        return { parent, open };
    },
    render() {
        this.parent;
        this.open();
        this.{|admin.component.unknown-instance-member:typoMember|};
    },
}));";
    let (plain, _) = swlsp_lsp::fixture::parse(source);
    fixture.index(&definition_path, &plain);
    let problems = fixture.check_path(&definition_path, source);
    assert!(problems[0].message.contains("typoMember"));
}

#[test]
fn admin_analyzer_reports_unknown_twig_privilege_with_suggestion() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.index(
        &format!("{admin_root}/privileges.ts"),
        "Shopware.Service('privileges').addPrivilegeMappingEntry({\n    key: 'product', roles: { viewer: { privileges: ['product:read'] } },\n});",
    );
    let source = "<div>\n    <mt-button :disabled=\"acl.can('admin')\" />\n    <mt-button :disabled=\"acl.can('{|admin.privilege.not-found:product.viwer|}')\" />\n</div>";
    let problems = fixture.check_path(&format!("{admin_root}/view.html.twig"), source);
    assert!(suggestions(&problems[0]).contains(&"product.viewer".to_owned()));
}

#[test]
fn admin_analyzer_reports_unknown_administration_module_routes() {
    let fixture = Fixture::new();
    let admin_root = fixture.path(ADMIN_SRC);
    fixture.index(
        &format!("{admin_root}/module/sw-product/index.js"),
        "Module.register('sw-product', {\n    routes: { detail: { path: 'detail/:id', component: 'sw-product-detail' } },\n});",
    );
    fixture.index(
        &format!("{admin_root}/module/sw-profile/index.js"),
        "const options = {
    routeMiddleware(next, currentRoute) {
        const name = 'sw.profile.index.mfa';
        currentRoute.children.push({ name, path: 'mfa' });
        next(currentRoute);
    },
};
Module.register('sw-profile-extension', options);",
    );
    for (path, source) in [
        (
            "component.js",
            "this.$router.push({ name: '{|admin.module-route.not-found:sw.product.detial|}' });",
        ),
        (
            "component.html.twig",
            r#"<router-link :to="{ name: '{|admin.module-route.not-found:sw.product.detial|}' }" />"#,
        ),
    ] {
        let problems = fixture.check_path(&format!("{admin_root}/{path}"), source);
        assert!(suggestions(&problems[0]).contains(&"sw.product.detail".to_owned()));
    }
    for source in [
        "this.$router.push({ name: 'sw.product.detail' });",
        r#"<router-link :to="{ name: 'sw.profile.index.mfa' }" />"#,
    ] {
        fixture.check_path(&format!("{admin_root}/known-route.html.twig"), source);
    }
    fixture.check_path(
        &format!("{admin_root}/module/sw-other/index.js"),
        "Module.register('sw-other', {\n    name: 'Not a route',\n    routes: { detail: { path: 'detail/:id' } },\n});",
    );
}

#[test]
fn admin_analyzer_unknown_this_member_suppresses_spread_generated_scope() {
    let fixture = Fixture::in_root();
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/index.js");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
    });
    fixture.check_path(
        &definition_path,
        "export default {\n    computed: { ...mapPropertyErrors('product', ['name']) },\n    methods: { run() { return this.productNameError; } },\n};",
    );
}

#[test]
fn camel_to_kebab_conversion() {
    for (input, expected) in [
        ("positionIdentifier", "position-identifier"),
        ("myPropName", "my-prop-name"),
        ("simple", "simple"),
        ("ABC", "a-b-c"),
        ("camelCase", "camel-case"),
        ("", ""),
    ] {
        assert_eq!(camel_to_kebab(input), expected);
    }
}

#[test]
fn admin_analyzer_block_references() {
    let fixture = Fixture::in_root();
    let base = fixture.path("src/Resources/app/administration/src");
    let extend_template_path = format!("{base}/extension/my-custom-page/my-custom-page.html.twig");
    fixture.save(vue_component! {
        name: "sw-page".into(),
        file_path: format!("{base}/component/sw-page/index.js"),
        template_path: format!("{base}/component/sw-page/sw-page.html.twig"),
        blocks: vec![
            block("sw_page_content", 10),
            block("sw_page_smart_bar", 20),
            block("sw_page_actions", 30),
        ],
    });
    fixture.save(vue_component! {
        name: "my-custom-page".into(),
        file_path: format!("{base}/extension/my-custom-page/index.js"),
        template_path: extend_template_path.clone(),
        extends_component: "sw-page".into(),
    });
    let owner = fixture
        .indexer
        .get_component_by_template_path(&extend_template_path)
        .unwrap()
        .expect("owner");
    assert_eq!(owner.name, "my-custom-page");
    assert_eq!(owner.extends_component, "sw-page");
    let parents = fixture.indexer.get_component("sw-page").unwrap();
    assert_eq!(parents.len(), 1);
    assert_eq!(parents[0].blocks.len(), 3);

    let analyzer = fixture.analyzer();
    for (code, blocks) in [
        (
            "{% block sw_page_content %}<div>Custom content</div>{% endblock %}",
            vec![],
        ),
        (
            "{% block {|admin.component.block-not-found:sw_page_contnet|} %}<div>Custom content</div>{% endblock %}",
            vec!["sw_page_contnet"],
        ),
        (
            "{% block sw_page_content %}content{% endblock %}{% block sw_page_smart_bar %}bar{% endblock %}",
            vec![],
        ),
        (
            "{% block sw_page_content %}content{% endblock %}{% block invalid_block %}invalid{% endblock %}",
            vec![],
        ),
        (
            "{% block invalid_one %}one{% endblock %}{% block invalid_two %}two{% endblock %}",
            vec![],
        ),
    ] {
        let problems = check(
            &analyzer,
            &swlsp_uriutil::file_uri(&extend_template_path),
            code,
        );
        let names: Vec<String> = problems
            .iter()
            .map(|problem| payload_str(problem, "blockName"))
            .collect();
        assert_eq!(names, blocks, "{code}");
    }
}

#[test]
fn admin_analyzer_block_references_no_override() {
    let fixture = Fixture::in_root();
    let base = fixture.path("src/Resources/app/administration/src/component/my-component");
    let template_path = format!("{base}/my-component.html.twig");
    fixture.save(vue_component! {
        name: "my-component".into(),
        file_path: format!("{base}/index.js"),
        template_path: template_path.clone(),
    });
    fixture.check_path(
        &template_path,
        "{% block any_block_name %}content{% endblock %}",
    );
}

#[test]
fn admin_analyzer_override_blocks_report_deprecation_and_typo() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    let base_template = format!("{admin_root}/sw-card.html.twig");
    let override_template = format!("{admin_root}/acme-card.html.twig");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        kind: ComponentRegistrationKind::Register,
        file_path: format!("{admin_root}/sw-card.js"),
        template_path: base_template.clone(),
        blocks: vec![
            TwigBlock {
                file_path: base_template.clone(),
                deprecated: "Use sw_card_content instead.".into(),
                ..block("sw_card_legacy", 4)
            },
            TwigBlock {
                file_path: base_template.clone(),
                ..block("sw_card_content", 8)
            },
        ],
    });
    fixture.save(vue_component! {
        name: "sw-card".into(),
        kind: ComponentRegistrationKind::Override,
        target_component: "sw-card".into(),
        file_path: format!("{admin_root}/zz-acme-card.js"),
        template_path: override_template.clone(),
    });
    let problems = fixture.check_path(
        &override_template,
        "{% block {|admin.component.deprecated-block:sw_card_legacy|} %}{% endblock %}\n{% block {|admin.component.block-not-found:sw_card_contnet|} %}{% endblock %}",
    );
    let [deprecated, missing] = problems.as_slice() else {
        unreachable!()
    };
    assert_eq!(deprecated.severity, Some(DiagnosticSeverity::HINT));
    assert_eq!(deprecated.tags, vec![DiagnosticTag::DEPRECATED]);
    assert!(deprecated.message.contains("Use sw_card_content instead"));
    assert!(suggestions(missing).contains(&"sw_card_content".to_owned()));
}

#[test]
fn admin_analyzer_reports_closed_application_container_typos() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/global.types.ts"),
        "
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
        module: ModuleFactory;
    }
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
}",
    );
    let source = "
Application.getContainer('factory').{|admin.application-container.unknown-member:loacle|};
Application.getContainer('factory').module;
Application.getContainer('service').al;
";
    let problems = fixture.check_path(&format!("{admin_root}/consumer.ts"), source);
    assert!(suggestions(&problems[0]).contains(&"locale".to_owned()));
}

#[test]
fn admin_analyzer_reports_closed_shopware_context_typos() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/app/composables/use-context.ts"),
        "export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string; versionId: null | string };
}",
    );
    let source = "
Shopware.Context.api.{|admin.shopware-context.unknown-member:langaugeId|};
Shopware.Context.api.versionId;
Shopware.Context.customRuntimeHelper;
";
    let problems = fixture.check_path(&format!("{admin_root}/consumer.ts"), source);
    assert!(suggestions(&problems[0]).contains(&"languageId".to_owned()));
}

#[test]
fn admin_analyzer_reports_closed_shopware_utils_typos() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/core/service/util.service.ts"),
        "export const format = { date, fileSize };
export default { createId, format };
function createId(): string { return 'id'; }
function date(value: string): string { return value; }
function fileSize(bytes: number): string { return String(bytes); }",
    );
    let source = "
Shopware.Utils.{|admin.shopware-utils.unknown-member:creatId|}();
Shopware.Utils.format.{|admin.shopware-utils.unknown-member:dtae|}('2026-01-01');
Shopware.Utils.format.fileSize(42);
const utils = Shopware.Utils;
utils.{|admin.shopware-utils.unknown-member:creatId|}();
const { format } = Shopware.Utils;
format.{|admin.shopware-utils.unknown-member:dtae|}('2026-01-01');
";
    let problems = fixture.check_path(&format!("{admin_root}/consumer.ts"), source);
    let mut by_member = HashMap::new();
    for problem in &problems {
        by_member.insert(payload_str(problem, "memberName"), problem);
    }
    assert!(suggestions(by_member["creatId"]).contains(&"createId".to_owned()));
    assert!(suggestions(by_member["dtae"]).contains(&"date".to_owned()));
}

#[test]
fn admin_analyzer_validates_known_shopware_event_bus_payloads() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/core/service/util.service.ts"),
        "\nimport EventBus from './utils/eventBus.utils';\nexport default { EventBus };",
    );
    fixture.index(
        &format!("{admin_root}/core/service/utils/eventBus.utils.ts"),
        "
interface Events extends Record<string | symbol, unknown> {
    save: string;
    done: undefined;
}
const emitter = mitt<Events>();
export default emitter;",
    );
    let source = "
Shopware.Utils.EventBus.emit('{|admin.event-bus.missing-payload:save|}');
Shopware.Utils.EventBus.emit('save', {|admin.event-bus.payload-type:42|});
Shopware.Utils.EventBus.emit('save', 'id');
Shopware.Utils.EventBus.emit('done');
Shopware.Utils.EventBus.on('{|admin.event-bus.missing-handler:save|}');
Shopware.Utils.EventBus.on('save', {|admin.event-bus.handler-type:'not-a-handler'|});
Shopware.Utils.EventBus.off('save');
Shopware.Utils.EventBus.off('save', {|admin.event-bus.handler-type:42|});
Shopware.Utils.EventBus.emit('extension-event', 42);
const { EventBus } = Shopware.Utils;
EventBus.emit('save', {|admin.event-bus.payload-type:false|});
";
    let problems = fixture.check_path(&format!("{admin_root}/consumer.ts"), source);
    for problem in &problems {
        assert_eq!(payload_str(problem, "eventName"), "save");
    }
}
