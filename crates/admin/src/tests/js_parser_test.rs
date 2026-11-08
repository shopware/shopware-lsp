#![allow(unused_imports)]
use super::support::*;
use crate::*;
use swlsp_indexer::Indexer;

fn registrations(code: &str, file_path: &str) -> Vec<VueComponent> {
    parse_component_registrations(&parse_js(code), code, file_path)
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

const COMPONENT_INDEX: &str =
    "/project/src/Administration/Resources/app/administration/src/app/component/index.ts";

#[test]
fn parse_component_register() {
    let code = "
Shopware.Component.register('sw-base-filter', () => import('src/app/component/filter/sw-base-filter/index'));
";
    let components = registrations(code, COMPONENT_INDEX);
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "sw-base-filter");
    assert_eq!(components[0].extends_component, "");
    assert_eq!(
        components[0].import_path,
        "src/app/component/filter/sw-base-filter/index"
    );
    assert_eq!(components[0].file_path, COMPONENT_INDEX);
    assert_eq!(components[0].line, 2);
}

#[test]
fn parse_component_and_prop_deprecations() {
    let code = "
/**
 * @deprecated tag:v6.8.0 - Use mt-card instead.
 */
Shopware.Component.register('sw-card', {
    props: {
        /**
         * @deprecated tag:v6.8.0 - Use dataSource instead.
         * The old value will be removed.
         */
        items: { type: Array },
        active: Boolean,
    },
});";
    let file_path = "/project/src/Administration/Resources/app/administration/src/app/component/sw-card/index.ts";
    let components = registrations(code, file_path);
    assert_eq!(components.len(), 1);
    assert_eq!(
        components[0].deprecated,
        "tag:v6.8.0 - Use mt-card instead."
    );
    let inline = components[0]
        .inline_definition
        .as_ref()
        .expect("inline definition");
    assert_eq!(inline.props.len(), 2);
    assert_eq!(
        inline.props[0].deprecated,
        "tag:v6.8.0 - Use dataSource instead. The old value will be removed."
    );
    assert!(inline.props[1].deprecated.is_empty());

    let export_code = "
/** @deprecated tag:v6.9.0 - Remove this wrapper. */
export default { props: { title: String } };";
    let definition = parse_component_definition(&parse_js(export_code), export_code);
    assert_eq!(definition.deprecated, "tag:v6.9.0 - Remove this wrapper.");
}

#[test]
fn parse_component_member_deprecations() {
    let code = "export default {
    props: {
        /** @deprecated Use title instead. */
        legacyTitle: String,
    },
    data() {
        return {
            /** @deprecated Use active instead. */
            legacyActive: false,
        };
    },
    computed: {
        /** @deprecated Use fullName instead. */
        legacyName() { return ''; },
    },
    methods: {
        /**
         * @deprecated tag:v6.8.0 - Use save instead.
         */
        legacySave() {},
    },
    setup() {
        const legacyOpen = () => {};
        return {
            /** @deprecated Use open instead. */
            legacyOpen,
        };
    },
};";
    let definition =
        parse_component_definition_with_line_index(&parse_js(code), &LineIndex::new(code));
    let deprecations: HashMap<String, String> = definition
        .members
        .iter()
        .map(|member| (member.name.clone(), member.deprecated.clone()))
        .collect();
    assert_eq!(deprecations["legacyTitle"], "Use title instead.");
    assert_eq!(deprecations["legacyActive"], "Use active instead.");
    assert_eq!(deprecations["legacyName"], "Use fullName instead.");
    assert_eq!(deprecations["legacySave"], "tag:v6.8.0 - Use save instead.");
    assert_eq!(deprecations["legacyOpen"], "Use open instead.");
}

#[test]
fn effective_component_does_not_inherit_parent_deprecation() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    index
        .save_component(&VueComponent {
            name: "sw-parent".to_owned(),
            file_path: "/project/parent.ts".to_owned(),
            deprecated: "Use sw-modern instead.".to_owned(),
            props: vec![VueComponentProp {
                name: "legacyValue".to_owned(),
                type_: "String".to_owned(),
                deprecated: "Use modernValue instead.".to_owned(),
                ..Default::default()
            }],
            members: vec![VueComponentMember {
                name: "legacySave".to_owned(),
                kind: VueComponentMemberKind::Method,
                deprecated: "Use save instead.".to_owned(),
                ..Default::default()
            }],
            methods: strings(&["legacySave"]),
            ..Default::default()
        })
        .unwrap();
    index
        .save_component(&VueComponent {
            name: "sw-child".to_owned(),
            file_path: "/project/child.ts".to_owned(),
            extends_component: "sw-parent".to_owned(),
            kind: ComponentRegistrationKind::Extend,
            members: vec![VueComponentMember {
                name: "legacySave".to_owned(),
                kind: VueComponentMemberKind::Method,
                ..Default::default()
            }],
            methods: strings(&["legacySave"]),
            ..Default::default()
        })
        .unwrap();

    let child = index
        .get_effective_component("sw-child")
        .unwrap()
        .expect("child");
    assert!(child.deprecated.is_empty());
    let prop = child.component_prop("legacyValue").expect("prop");
    assert_eq!(prop.deprecated, "Use modernValue instead.");
    let member = child.template_member("legacySave").expect("member");
    assert_eq!(member.deprecated, "Use save instead.");
}

#[test]
fn component_contract_documentation_persists_across_index_reload() {
    let dir = temp_dir();
    let cache_path = dir.path().join("cache");
    let index = new_indexer(&cache_path);
    index
        .save_component(&VueComponent {
            name: "sw-documented".to_owned(),
            file_path: "/project/sw-documented.ts".to_owned(),
            props: vec![VueComponentProp {
                name: "label".to_owned(),
                type_: "String".to_owned(),
                documentation: "Label displayed by the component.".to_owned(),
                ..Default::default()
            }],
            events: vec![VueComponentEvent {
                name: "save".to_owned(),
                type_: "[id: string]".to_owned(),
                documentation: "Persists the component.".to_owned(),
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    index.close().unwrap();
    drop(index);

    let reopened = new_indexer(&cache_path);
    let component = reopened
        .get_effective_component("sw-documented")
        .unwrap()
        .expect("component");
    let prop = component.component_prop("label").expect("prop");
    assert_eq!(prop.documentation, "Label displayed by the component.");
    let event = component.component_event("save").expect("event");
    assert_eq!(event.documentation, "Persists the component.");
    reopened.close().unwrap();
}

#[test]
fn template_parent_component_supports_extend_and_override_blocks() {
    let dir = temp_dir();
    let root = dir.path();
    let index = new_indexer(&root.join("cache"));
    let base_template = path_string(root, "a-base.html.twig");
    let override_template = path_string(root, "b-override.html.twig");
    let child_template = path_string(root, "c-child.html.twig");
    let base_block = TwigBlock {
        name: "sw_card_content".to_owned(),
        deprecated: "Use sw_card_body instead.".to_owned(),
        file_path: base_template.clone(),
        line: 4,
        scope_members: vec![TwigBlockScopeMember {
            name: "item".to_owned(),
            file_path: base_template.clone(),
            line: 2,
            ..Default::default()
        }],
        ..Default::default()
    };
    for component in [
        VueComponent {
            name: "sw-card".to_owned(),
            kind: ComponentRegistrationKind::Register,
            file_path: path_string(root, "a-base.js"),
            template_path: base_template.clone(),
            blocks: vec![base_block.clone()],
            ..Default::default()
        },
        VueComponent {
            name: "sw-card".to_owned(),
            kind: ComponentRegistrationKind::Override,
            target_component: "sw-card".to_owned(),
            file_path: path_string(root, "b-override.js"),
            template_path: override_template.clone(),
            blocks: vec![TwigBlock {
                name: "sw_card_content".to_owned(),
                file_path: override_template.clone(),
                line: 2,
                ..Default::default()
            }],
            ..Default::default()
        },
        VueComponent {
            name: "acme-card".to_owned(),
            kind: ComponentRegistrationKind::Extend,
            target_component: "sw-card".to_owned(),
            extends_component: "sw-card".to_owned(),
            file_path: path_string(root, "c-child.js"),
            template_path: child_template.clone(),
            ..Default::default()
        },
    ] {
        index.save_component(&component).unwrap();
    }

    let override_parent = index
        .get_parent_component_for_template(&override_template)
        .unwrap()
        .expect("override parent");
    let block = override_parent
        .component_block("sw_card_content")
        .expect("block");
    assert_eq!(block.file_path, base_template);
    assert_eq!(block.deprecated, base_block.deprecated);

    let effective_base = index
        .get_effective_component("sw-card")
        .unwrap()
        .expect("effective");
    let block = effective_base
        .component_block("sw_card_content")
        .expect("block");
    assert_eq!(block.file_path, override_template);
    assert_eq!(block.deprecated, base_block.deprecated);
    let item = block.scope_member("item").expect("item");
    assert_eq!(item.file_path, base_template);

    let child_parent = index
        .get_parent_component_for_template(&child_template)
        .unwrap()
        .expect("child parent");
    let block = child_parent
        .component_block("sw_card_content")
        .expect("block");
    assert_eq!(block.file_path, override_template);
    assert_eq!(block.deprecated, base_block.deprecated);
    assert!(block.scope_member("item").is_some());
}

#[test]
fn parse_component_definition_marks_runtime_open_members() {
    let code = "export default {
    computed: { ...mapState(() => Store.get('flow'), ['flow']) },
};";
    assert!(parse_component_definition(&parse_js(code), code).open_runtime_members);

    let code = "export default { computed: { title() { return 'title'; } } };";
    assert!(!parse_component_definition(&parse_js(code), code).open_runtime_members);
}

#[test]
fn parse_component_extend() {
    let code = "
Shopware.Component.extend('sw-condition-time-range', 'sw-condition-base', () => import('./rule/condition-type/sw-condition-time-range/index'));
";
    let components = registrations(code, COMPONENT_INDEX);
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "sw-condition-time-range");
    assert_eq!(components[0].extends_component, "sw-condition-base");
    assert_eq!(
        components[0].import_path,
        "./rule/condition-type/sw-condition-time-range/index"
    );
}

#[test]
fn parse_component_override() {
    let code = "
Component.override('sw-product-detail', {
    methods: { save() {} },
});
";
    let components = registrations(
        code,
        "/project/src/Administration/Resources/app/administration/src/extension/index.js",
    );
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "sw-product-detail");
    assert_eq!(components[0].kind, ComponentRegistrationKind::Override);
    assert_eq!(components[0].target_component, "sw-product-detail");
    let inline = components[0].inline_definition.as_ref().expect("inline");
    assert_eq!(inline.methods, strings(&["save"]));
}

#[test]
fn parse_multiple_components() {
    let code = "
Shopware.Component.register('sw-wizard-page', () => import('src/app/component/wizard/sw-wizard-page/index'));
Shopware.Component.register('sw-wizard', () => import('src/app/component/wizard/sw-wizard/index'));
Shopware.Component.extend('sw-sidebar-collapse', 'sw-collapse', () => import('./sidebar/sw-sidebar-collapse/index'));
";
    let components = registrations(code, COMPONENT_INDEX);
    assert_eq!(components.len(), 3);
    assert_eq!(components[0].name, "sw-wizard-page");
    assert_eq!(components[0].extends_component, "");
    assert_eq!(components[1].name, "sw-wizard");
    assert_eq!(components[1].extends_component, "");
    assert_eq!(components[2].name, "sw-sidebar-collapse");
    assert_eq!(components[2].extends_component, "sw-collapse");
}

#[test]
fn parse_vue_application_component_collections_registers_meteor_maps() {
    let code = "
import MtButton from '@shopware-ag/meteor-component-library/dist/esm/MtButton';
import { MtDropdownMenuRoot } from '@shopware-ag/meteor-component-library';

class Adapter {
async initComponents() {
const meteorComponents = {
    MtButton,
    MtDropdownMenuRoot,
} as const;
const lazyMeteorComponents = {
    MtPopover: () => import('@shopware-ag/meteor-component-library/dist/esm/MtPopover'),
};
const unrelated = { MtIgnored };

Object.entries(meteorComponents).forEach(([componentName, component]) => {
    const componentNameAsKebabCase = Shopware.Utils.string.kebabCase(componentName);
    this.app.component(componentNameAsKebabCase, component);
});
Object.entries(lazyMeteorComponents).forEach(([componentName, importMethod]) => {
    const componentNameAsKebabCase = Shopware.Utils.string.kebabCase(componentName);
    this.registerAsyncComponent(componentNameAsKebabCase, importMethod);
});
Object.entries(unrelated).forEach(([name, value]) => console.log(name, value));
}
}
";
    let file_path = "/project/src/Administration/Resources/app/administration/src/app/adapter/view/vue.adapter.ts";
    let components = registrations(code, file_path);
    assert_eq!(components.len(), 3);
    assert_eq!(components[0].name, "mt-button");
    assert_eq!(
        components[0].import_path,
        "@shopware-ag/meteor-component-library/dist/esm/MtButton"
    );
    assert_eq!(components[1].name, "mt-dropdown-menu-root");
    assert_eq!(
        components[1].import_path,
        "@shopware-ag/meteor-component-library"
    );
    assert_eq!(components[2].name, "mt-popover");
    assert_eq!(
        components[2].import_path,
        "@shopware-ag/meteor-component-library/dist/esm/MtPopover"
    );
    for component in &components {
        assert_eq!(component.file_path, file_path);
        assert_ne!(component.line, 0);
    }
}

#[test]
fn parse_vue_application_component_collections_requires_registration() {
    let code = "
const components = { SwNotActuallyRegistered };
Object.entries(components).forEach(([name, value]) => console.log(name, value));
";
    let components = registrations(
        code,
        "/project/src/Administration/Resources/app/administration/src/app/example.ts",
    );
    assert!(components.is_empty());
}

#[test]
fn resolve_import_path_cases() {
    let cases = [
        (
            "absolute src path with index",
            COMPONENT_INDEX,
            "src/app/component/filter/sw-base-filter/index",
            "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter/index/index.js",
        ),
        (
            "relative path with index",
            COMPONENT_INDEX,
            "./filter/sw-base-filter/index",
            "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter/index/index.js",
        ),
        (
            "parent relative path - directory import",
            "/project/src/Administration/Resources/app/administration/src/module/sw-settings/index.js",
            "../sw-other/component",
            "/project/src/Administration/Resources/app/administration/src/module/sw-other/component/index.js",
        ),
        (
            "import with .js extension",
            COMPONENT_INDEX,
            "./filter/sw-base-filter.js",
            "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter.js",
        ),
    ];
    for (name, registration_file, import_path, expected) in cases {
        assert_eq!(
            resolve_import_path(registration_file, import_path),
            expected,
            "{name}"
        );
    }
}

#[test]
fn indexer_filtering() {
    let code = "
Shopware.Component.register('sw-base-filter', () => import('src/app/component/filter/sw-base-filter/index'));
";
    // Parsing works regardless of path; Index() filters by path.
    let components = registrations(
        code,
        "/project/src/Storefront/Resources/app/storefront/src/main.js",
    );
    assert_eq!(components.len(), 1);
}

#[test]
fn parse_destructured_component() {
    let code = "
const { Component } = Shopware;

Component.register('my-component', {
    template,
    props: {
        title: String,
    },
    methods: {
        handleClick() {}
    },
});
";
    let file_path = "/project/src/Administration/Resources/app/administration/src/module/my-module/component/my-component/index.js";
    let components = registrations(code, file_path);
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "my-component");
    assert_eq!(components[0].extends_component, "");
    assert_eq!(components[0].definition_path, file_path);
    let inline = components[0].inline_definition.as_ref().expect("inline");
    assert!(inline.has_template);
    assert_eq!(inline.props.len(), 1);
    assert_eq!(inline.props[0].name, "title");
    assert_eq!(inline.props[0].type_, "String");
    assert_eq!(inline.methods, strings(&["handleClick"]));
}

#[test]
fn parse_inline_component_definition() {
    let code = "
Shopware.Component.register('inline-component', {
    template,
    
    emits: ['change', 'submit'],
    
    props: {
        name: {
            type: String,
            required: true,
        },
        count: {
            type: Number,
            default: 0,
        },
    },
    
    computed: {
        fullName() {
            return this.name;
        },
    },
    
    methods: {
        save() {},
        cancel() {},
    },
});
";
    let components = registrations(
        code,
        "/project/src/Administration/Resources/app/administration/src/component/index.js",
    );
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "inline-component");
    let def = components[0].inline_definition.as_ref().expect("inline");
    assert!(def.has_template);
    assert_eq!(def.emits, strings(&["change", "submit"]));
    assert_eq!(def.props.len(), 2);
    assert_eq!(def.props[0].name, "name");
    assert_eq!(def.props[0].type_, "String");
    assert!(def.props[0].required);
    assert_eq!(def.props[1].name, "count");
    assert_eq!(def.props[1].type_, "Number");
    assert_eq!(def.props[1].default, "0");
    assert_eq!(def.computed, strings(&["fullName"]));
    assert_eq!(def.methods, strings(&["save", "cancel"]));
}

#[test]
fn parse_extend_with_inline_definition() {
    let code = "
const { Component } = Shopware;

Component.extend('my-extended', 'sw-base', {
    props: {
        extra: Boolean,
    },
    methods: {
        customMethod() {},
    },
});
";
    let components = registrations(
        code,
        "/project/src/Administration/Resources/app/administration/src/module/my-module/index.js",
    );
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "my-extended");
    assert_eq!(components[0].extends_component, "sw-base");
    let def = components[0].inline_definition.as_ref().expect("inline");
    assert_eq!(def.props.len(), 1);
    assert_eq!(def.props[0].name, "extra");
    assert_eq!(def.props[0].type_, "Boolean");
    assert_eq!(def.methods, strings(&["customMethod"]));
}

#[test]
fn admin_component_indexer_indexes_registration_and_definition() {
    let dir = temp_dir();
    let indexer = new_indexer(dir.path());
    index_source(
        &indexer,
        COMPONENT_INDEX,
        "
Shopware.Component.register('sw-test-component', () => import('src/app/component/test/sw-test-component/index'));
",
    );
    let definition_code = "
import template from './sw-test-component.html.twig';

export default {
    template,
    
    emits: ['change', 'submit'],
    
    props: {
        title: {
            type: String,
            required: true,
        },
        disabled: {
            type: Boolean,
            default: false,
        },
    },
    
    computed: {
        isActive() {
            return !this.disabled;
        },
    },
    
    methods: {
        handleClick() {
            this.$emit('change');
        },
        submit() {
            this.$emit('submit');
        },
    },
};
";
    let definition_path = "/project/src/Administration/Resources/app/administration/src/app/component/test/sw-test-component/index.js";
    index_source(&indexer, definition_path, definition_code);

    let components = indexer.get_component("sw-test-component").unwrap();
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "sw-test-component");

    let def = indexer
        .get_component_definition(definition_path)
        .unwrap()
        .expect("definition");
    assert_eq!(
        def.template_path,
        "/project/src/Administration/Resources/app/administration/src/app/component/test/sw-test-component/sw-test-component.html.twig"
    );
    assert!(def.has_template);
    assert_eq!(def.emits, strings(&["change", "submit"]));
    assert_eq!(def.props.len(), 2);
    assert_eq!(def.props[0].name, "title");
    assert_eq!(def.props[0].type_, "String");
    assert!(def.props[0].required);
    assert_eq!(def.props[1].name, "disabled");
    assert_eq!(def.props[1].type_, "Boolean");
    assert!(!def.props[1].required);
    assert_eq!(def.props[1].default, "false");
    assert_eq!(def.computed, strings(&["isActive"]));
    assert_eq!(def.methods, strings(&["handleClick", "submit"]));

    assert_eq!(indexer.get_all_components().unwrap().len(), 1);
    indexer.close().unwrap();
}

#[test]
fn admin_component_indexer_indexes_mixins_modules_and_routes() {
    let dir = temp_dir();
    let admin_index = new_indexer(dir.path());
    let file_path =
        "/project/src/Administration/Resources/app/administration/src/module/sw-product/index.js";
    let content = "
Shopware.Mixin.register('notification', {});
Module.register('sw-product', {
    type: 'core',
    name: 'product',
    title: 'sw-product.general.title',
    description: 'sw-product.general.description',
    routes: {
\t\tindex: { path: 'index', components: { default: 'sw-product-list' } },
\t\tdetail: {
\t\t\tpath: 'detail/:id',
\t\t\tcomponent: 'sw-product-detail',
\t\t\tchildren: {
\t\t\t\tbase: {
\t\t\t\t\tpath: 'base',
\t\t\t\t\tcomponent: 'sw-product-detail-base',
\t\t\t\t\tchildren: {
\t\t\t\t\t\tprices: { path: 'prices', component: 'sw-product-prices' },
\t\t\t\t\t},
\t\t\t\t},
\t\t\t},
\t\t},
\t\tcreate: {
\t\t\tpath: 'create',
\t\t\tcomponent: 'sw-product-create',
\t\t\tchildren: createChildren(),
\t\t},
    },
});

function createChildren() {
\treturn {
\t\tgeneral: {
\t\t\tpath: 'general',
\t\t\tcomponent: 'sw-product-create-general',
\t\t},
\t};
}
";
    index_source(&admin_index, file_path, content);

    let mixins = admin_index.get_mixin("notification").unwrap();
    assert_eq!(mixins.len(), 1);
    assert_eq!(mixins[0].line, 2);

    let modules = admin_index.get_module("sw-product").unwrap();
    assert_eq!(modules.len(), 1);
    assert_eq!(modules[0].display_name, "product");
    assert_eq!(modules[0].type_, "core");
    assert_eq!(modules[0].routes.len(), 6);
    assert_eq!(modules[0].routes[0].name, "sw.product.index");
    assert_eq!(modules[0].routes[0].component, "sw-product-list");
    assert_eq!(modules[0].routes[2].name, "sw.product.detail.base");
    assert_eq!(modules[0].routes[2].component, "sw-product-detail-base");
    assert_eq!(modules[0].routes[3].name, "sw.product.detail.base.prices");
    assert_eq!(modules[0].routes[4].name, "sw.product.create");
    assert_eq!(modules[0].routes[5].name, "sw.product.create.general");
    assert_eq!(modules[0].routes[5].component, "sw-product-create-general");

    let (module, route) = admin_index
        .get_module_route("sw.product.detail")
        .unwrap()
        .expect("route");
    assert_eq!(module.name, "sw-product");
    assert_eq!(route.path, "detail/:id");

    let (_, route) = admin_index
        .get_module_route("sw.product.detail.base.prices")
        .unwrap()
        .expect("route");
    assert_eq!(route.component, "sw-product-prices");

    let (_, route) = admin_index
        .get_module_route("sw.product.create.general")
        .unwrap()
        .expect("route");
    assert_eq!(route.path, "general");
    admin_index.close().unwrap();
}

#[test]
fn admin_component_indexer_indexes_const_config_and_middleware_routes() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    let file_path = "/project/Resources/app/administration/src/module/routes.js";
    let source = "
const securityOptions = {
    type: 'plugin',
    name: 'settings-security',
    routes: {
        index: { path: 'index', component: 'sw-settings-security-view' },
    },
};
Shopware.Module.register('sw-settings-security', securityOptions);

function profileRouteMiddleware(next, currentRoute) {
    const childRouteName = 'sw.profile.index.mfa';
    const children = currentRoute.children || [];
    children.push({
        name: childRouteName,
        path: 'mfa',
        component: 'frosh-profile-mfa',
    });
    next(currentRoute);
}
Shopware.Module.register('frosh-profile-mfa', {
    routeMiddleware: profileRouteMiddleware,
});

Module.register('sw-theme-manager', {
    routeMiddleware(next, currentRoute) {
        const name = 'sw.sales.channel.detail.theme';
        currentRoute.children.push({
            name,
            path: '/sw/sales/channel/detail/:id/theme',
            component: 'sw-sales-channel-detail-theme',
        });
        next(currentRoute);
    },
});
";
    index_source(&index, file_path, source);

    for (route_name, expected_component) in [
        ("sw.settings.security.index", "sw-settings-security-view"),
        ("sw.profile.index.mfa", "frosh-profile-mfa"),
        (
            "sw.sales.channel.detail.theme",
            "sw-sales-channel-detail-theme",
        ),
    ] {
        let (_, route) = index
            .get_module_route(route_name)
            .unwrap()
            .expect(route_name);
        assert_eq!(route.component, expected_component, "{route_name}");
        assert_ne!(route.line, 0, "{route_name}");
    }

    let security = index.get_module("sw-settings-security").unwrap();
    assert_eq!(security.len(), 1);
    assert_eq!(security[0].type_, "plugin");
    assert_eq!(security[0].display_name, "settings-security");
    index.close().unwrap();
}

#[test]
fn wrap_component_config() {
    let dir = temp_dir();
    let indexer = new_indexer(dir.path());
    let wrap_code = "
import { MtCard } from '@shopware-ag/meteor-component-library';
import template from './mt-card.html.twig';

export default Shopware.Component.wrapComponentConfig({
    template,

    components: {
        'mt-card-original': MtCard,
    },

    inheritAttrs: false,

    props: {
        positionIdentifier: {
            type: String,
            required: true,
            default: null,
        },
        title: {
            type: String,
        },
    },

    computed: {
        filteredSlots() {
            return this.$slots;
        },
    },

    methods: {
        getFilteredSlots() {
            return this.$slots;
        },
    },
});
";
    let wrap_path = "/project/src/Administration/Resources/app/administration/src/app/component/meteor-wrapper/mt-card/index.ts";
    index_source(&indexer, wrap_path, wrap_code);

    let components = indexer.get_component("mt-card").unwrap();
    assert_eq!(components.len(), 1);
    assert_eq!(components[0].name, "mt-card");
    assert_eq!(components[0].file_path, wrap_path);
    assert_eq!(components[0].definition_path, wrap_path);
    assert_eq!(components[0].local_components.len(), 1);
    let local = &components[0].local_components[0];
    assert_eq!(local.name, "mt-card-original");
    assert_eq!(local.symbol, "MtCard");
    assert_eq!(local.import_path, "@shopware-ag/meteor-component-library");
    assert_eq!(local.file_path, wrap_path);
    assert_ne!(local.line, 0);
    assert!(local.quoted);
    assert!(!local.shorthand);
    assert_eq!(local.line - 1, local.name_range.start_line);
    assert!(local.name_range.start_character < local.name_range.end_character);

    assert_eq!(components[0].props.len(), 2);
    assert_eq!(components[0].props[0].name, "positionIdentifier");
    assert_eq!(components[0].props[0].type_, "String");
    assert!(components[0].props[0].required);
    assert_eq!(components[0].props[1].name, "title");
    assert_eq!(components[0].props[1].type_, "String");
    assert_eq!(components[0].computed, strings(&["filteredSlots"]));
    assert_eq!(components[0].methods, strings(&["getFilteredSlots"]));

    let with_definition = indexer.get_component_with_definition("mt-card").unwrap();
    assert_eq!(with_definition.len(), 1);
    assert_eq!(with_definition[0].name, "mt-card");
    assert_eq!(with_definition[0].props.len(), 2);

    // Local aliases resolve only for the template that owns the declaration.
    let local = indexer
        .get_component_for_template_tag(&with_definition[0].template_path, "mt-card-original", None)
        .unwrap()
        .expect("local component");
    assert_eq!(local.name, "mt-card-original");
    assert_eq!(local.props.len(), 2);
    for name in ["mt-card-original", "mt-card", "missing"] {
        assert_eq!(
            indexer
                .get_component_for_template_tag_shared(
                    &with_definition[0].template_path,
                    name,
                    None
                )
                .unwrap()
                .map(|component| component.as_ref().clone()),
            indexer
                .get_component_for_template_tag(&with_definition[0].template_path, name, None)
                .unwrap(),
        );
    }

    assert!(
        indexer
            .get_component("mt-card-original")
            .unwrap()
            .is_empty()
    );

    let local = indexer
        .get_component_for_template_tag(
            "/project/another-component.html.twig",
            "mt-card-original",
            None,
        )
        .unwrap();
    assert!(local.is_none());
    indexer.close().unwrap();
}

#[test]
fn derive_component_name_from_path_cases() {
    for (path, expected) in [
        ("/path/to/mt-card/index.ts", "mt-card"),
        ("/path/to/mt-card/index.js", "mt-card"),
        ("/path/to/sw-button.js", "sw-button"),
        ("/path/to/sw-button.ts", "sw-button"),
        ("/path/to/component/my-component/index.ts", "my-component"),
    ] {
        assert_eq!(derive_component_name_from_path(path), expected, "{path}");
    }
}

fn prop(name: &str) -> VueComponentProp {
    VueComponentProp {
        name: name.to_owned(),
        ..Default::default()
    }
}

#[test]
fn deduplicate_components_cases() {
    let cases: Vec<(&str, Vec<VueComponent>, usize, usize)> = vec![
        ("empty list", vec![], 0, 0),
        (
            "single component",
            vec![VueComponent {
                name: "test".to_owned(),
                props: vec![prop("prop1")],
                ..Default::default()
            }],
            1,
            1,
        ),
        (
            "two components same name - prefer one with more props",
            vec![
                VueComponent {
                    name: "test".to_owned(),
                    file_path: "/file1.ts".to_owned(),
                    ..Default::default()
                },
                VueComponent {
                    name: "test".to_owned(),
                    file_path: "/file2.ts".to_owned(),
                    props: vec![prop("prop1"), prop("prop2")],
                    ..Default::default()
                },
            ],
            1,
            2,
        ),
        (
            "merge data from both components",
            vec![
                VueComponent {
                    name: "test".to_owned(),
                    file_path: "/file1.ts".to_owned(),
                    extends_component: "parent".to_owned(),
                    ..Default::default()
                },
                VueComponent {
                    name: "test".to_owned(),
                    file_path: "/file2.ts".to_owned(),
                    props: vec![prop("prop1")],
                    definition_path: "/def.ts".to_owned(),
                    ..Default::default()
                },
            ],
            1,
            1,
        ),
    ];
    for (name, components, want_len, want_props) in cases {
        let result = deduplicate_components(components.clone());
        assert_eq!(result.len(), want_len, "{name}");
        if want_len > 0 {
            assert_eq!(result[0].props.len(), want_props, "{name}");
        }
    }
}

#[test]
fn merge_components_prefers_populated_values() {
    let fallback = VueComponent {
        name: "test".to_owned(),
        extends_component: "parent".to_owned(),
        import_path: "/import/path".to_owned(),
        props: vec![prop("fallbackProp")],
        emits: strings(&["fallbackEmit"]),
        ..Default::default()
    };
    let preferred = VueComponent {
        name: "test".to_owned(),
        definition_path: "/def/path".to_owned(),
        props: vec![prop("preferredProp1"), prop("preferredProp2")],
        methods: strings(&["method1"]),
        ..Default::default()
    };
    let result = merge_components(&fallback, &preferred);
    assert_eq!(result.name, "test");
    assert_eq!(result.definition_path, "/def/path");
    assert_eq!(result.props.len(), 2);
    assert_eq!(result.props[0].name, "preferredProp1");
    assert_eq!(result.methods.len(), 1);
    assert_eq!(result.extends_component, "parent");
    assert_eq!(result.import_path, "/import/path");
    assert_eq!(result.emits, strings(&["fallbackEmit"]));
}

#[test]
fn overlay_components_retains_inherited_slot_owner() {
    let parent = VueComponent {
        name: "sw-parent".to_owned(),
        template_path: "/project/sw-parent.html.twig".to_owned(),
        slots: vec![VueComponentSlot {
            name: "header".to_owned(),
            file_path: "/project/sw-parent.html.twig".to_owned(),
            line: 4,
            ..Default::default()
        }],
        ..Default::default()
    };
    let child = VueComponent {
        name: "sw-child".to_owned(),
        template_path: "/project/sw-child.html.twig".to_owned(),
        slots: vec![VueComponentSlot {
            name: "footer".to_owned(),
            file_path: "/project/sw-child.html.twig".to_owned(),
            line: 8,
            ..Default::default()
        }],
        ..Default::default()
    };
    let result = overlay_components(&parent, &child);
    assert_eq!(result.slots.len(), 2);
    assert_eq!(result.slots[0].file_path, "/project/sw-parent.html.twig");
    assert_eq!(result.slots[1].file_path, "/project/sw-child.html.twig");
}

#[test]
fn effective_component_includes_parents_overrides_and_mixins() {
    let dir = temp_dir();
    let root = dir.path();
    let index = new_indexer(root);
    let admin_root = root.join("src/Resources/app/administration/src");
    let child_path = admin_root
        .join("component/sw-child/index.js")
        .to_str()
        .unwrap()
        .to_owned();
    let files = [
        (
            admin_root
                .join("mixin/notification.js")
                .to_str()
                .unwrap()
                .to_owned(),
            "
Mixin.register('notification', {
    computed: { notificationTitle() { return ''; } },
    methods: { createNotificationSuccess() {} },
});",
        ),
        (
            admin_root
                .join("component/sw-parent/index.js")
                .to_str()
                .unwrap()
                .to_owned(),
            "
Component.register('sw-parent', {
    props: { parentLabel: { type: String, required: true } },
\temits: ['parent-event'],
    inject: ['repositoryFactory'],
    data() { return { parentLoading: false }; },
\tmethods: { notifyParent() { this.$emit('runtime-event'); } },
});",
        ),
        (
            child_path.clone(),
            "
Component.extend('sw-child', 'sw-parent', {
    mixins: [Mixin.getByName('notification')],
    props: { childCount: Number },
    computed: { childTitle() { return ''; } },
});",
        ),
        (
            admin_root
                .join("extension/sw-child/index.js")
                .to_str()
                .unwrap()
                .to_owned(),
            "
Component.override('sw-child', {
    props: { overrideEnabled: Boolean },
    methods: { saveOverride() {} },
});",
        ),
    ];
    for (path, source) in &files {
        index_source(&index, path, source);
    }

    let component = index
        .get_effective_component("sw-child")
        .unwrap()
        .expect("component");
    assert_eq!(
        sorted(prop_names(&component.props)),
        sorted(strings(&["parentLabel", "childCount", "overrideEnabled"]))
    );
    assert_eq!(
        sorted(component.injected.clone()),
        strings(&["repositoryFactory"])
    );
    assert_eq!(sorted(component.data.clone()), strings(&["parentLoading"]));
    assert_eq!(
        sorted(component.computed.clone()),
        sorted(strings(&["notificationTitle", "childTitle"]))
    );
    assert_eq!(
        sorted(component.methods.clone()),
        sorted(strings(&[
            "notifyParent",
            "createNotificationSuccess",
            "saveOverride"
        ]))
    );
    assert_eq!(
        sorted(component.emits.clone()),
        sorted(strings(&["parent-event", "runtime-event"]))
    );
    let parent_event = component
        .component_event("parent-event")
        .expect("parent event");
    assert!(parent_event.file_path.contains("sw-parent"));
    assert_ne!(parent_event.line, 0);

    let parent_prop = component
        .props
        .iter()
        .find(|prop| prop.name == "parentLabel")
        .cloned()
        .unwrap_or_default();
    assert!(parent_prop.file_path.contains("sw-parent"));
    assert_ne!(parent_prop.line, 0);
    let by_definition = index
        .get_components_by_definition_path(&child_path)
        .unwrap();
    assert_eq!(by_definition.len(), 1);
    assert_eq!(by_definition[0].name, "sw-child");
    assert!(prop_names(&by_definition[0].props).contains(&"parentLabel".to_owned()));
    index.close().unwrap();
}

#[test]
fn effective_component_includes_define_component_mixin_and_registration() {
    let dir = temp_dir();
    let root = dir.path();
    let index = new_indexer(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let mixin_path = admin_root
        .join("app/mixin/validation.mixin.ts")
        .to_str()
        .unwrap()
        .to_owned();
    let component_path = admin_root
        .join("component/sw-card/index.ts")
        .to_str()
        .unwrap()
        .to_owned();

    index_source(
        &index,
        &mixin_path,
        "export default Shopware.Mixin.register('validation', defineComponent({
            inject: { validationService: { default: null } },
            props: { validation: { type: String } },
            methods: {
                validate(value: string): boolean {
                    if (value) { return true; }
                    return false;
                },
            },
        }));",
    );
    index_source(
        &index,
        &component_path,
        "Shopware.Component.register('sw-card', defineComponent({
            props: { title: { type: String, required: true } },
            mixins: [Shopware.Mixin.getByName('validation')],
        }));",
    );

    let mixins = index.get_mixin("validation").unwrap();
    assert_eq!(mixins.len(), 1);
    assert_eq!(
        prop_names(&mixins[0].definition.props),
        strings(&["validation"])
    );
    assert_eq!(mixins[0].definition.methods, strings(&["validate"]));
    assert_eq!(
        mixins[0].definition.injected,
        strings(&["validationService"])
    );

    let component = index
        .get_effective_component("sw-card")
        .unwrap()
        .expect("component");
    assert_eq!(
        sorted(prop_names(&component.props)),
        sorted(strings(&["validation", "title"]))
    );
    assert!(component.methods.contains(&"validate".to_owned()));
    assert!(component.injected.contains(&"validationService".to_owned()));
    index.close().unwrap();
}
