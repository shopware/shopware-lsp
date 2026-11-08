#![allow(unused_imports)]
use super::support::*;
use crate::*;
use swlsp_indexer::Indexer;

fn start_tags(source: &str) -> Vec<SyntaxNode> {
    twigquery::nodes(&parse_twig(source), &[twigsyntax::HTML_STARTING_TAG])
}

fn single_selector(source: &str) -> VueDynamicComponentSelector {
    let tags = start_tags(source);
    assert_eq!(tags.len(), 1);
    twig_dynamic_component_selector(&tags[0]).expect("selector")
}

fn names(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

#[test]
fn twig_dynamic_component_selector_cases() {
    let cases: &[(&str, &str, &[&str], bool)] = &[
        (
            "static attribute",
            r#"<component is="sw-card" />"#,
            &["sw-card"],
            true,
        ),
        (
            "bound literal",
            r#"<component :is="'sw-card'" />"#,
            &["sw-card"],
            true,
        ),
        (
            "finite conditional",
            r#"<component :is="enabled ? 'mt-switch' : 'mt-text-field'" />"#,
            &["mt-switch", "mt-text-field"],
            true,
        ),
        (
            "nested conditional",
            r#"<component :is="first ? (second ? 'sw-a' : 'sw-b') : 'sw-c'" />"#,
            &["sw-a", "sw-b", "sw-c"],
            true,
        ),
        (
            "partially static conditional",
            r#"<component v-bind:is="enabled ? componentName : 'sw-card'" />"#,
            &["sw-card"],
            false,
        ),
        (
            "runtime selector",
            r#"<component :is="componentName" />"#,
            &[],
            false,
        ),
    ];
    for (name, source, want, complete) in cases {
        let selector = single_selector(source);
        assert_eq!(selector.names(), names(want), "{name}");
        assert_eq!(selector.complete, *complete, "{name}");
        for candidate in &selector.candidates {
            assert_eq!(candidate.name, &source[candidate.range], "{name}");
            let offset = (source.find(&candidate.name).unwrap() + 1) as u32;
            let at = selector.candidate_at(offset).expect("candidate at offset");
            assert_eq!(candidate.name, at.name, "{name}");
        }
    }
}

#[test]
fn resolve_dynamic_component_selector_from_effective_member_values() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root.path().join("src/Resources/app/administration/src");
    let definition_path = path_string(&admin_root, "component/sw-host/index.js");
    let template_path = path_string(&admin_root, "component/sw-host/sw-host.html.twig");
    index_source(
        &idx,
        &definition_path,
        r#"
import template from './sw-host.html.twig';
import ImportedCard from './imported-card';
export default Shopware.Component.register('sw-host', {
    template,
    components: { ImportedCard },
    props: { runtimeComponent: { type: String, default: 'sw-fallback' } },
    computed: {
        elementType() { return this.disabled ? 'span' : 'router-link'; },
        selectedForm() {
            if (this.contact) { return 'sw-contact'; }
            return this.runtimeComponent;
        },
        inputComponent() {
            switch (this.fieldType) {
                case 'uuid': return 'sw-entity-multi-id-select';
                case 'float':
                case 'int': return 'sw-number-field';
                default: return 'sw-text-field';
            }
        },
    },
});"#,
    );
    for name in [
        "sw-entity-multi-id-select",
        "sw-number-field",
        "sw-text-field",
        "imported-card",
    ] {
        idx.save_component(&VueComponent {
            name: name.to_owned(),
            file_path: path_string(&admin_root.join(name), "index.js"),
            ..Default::default()
        })
        .unwrap();
    }
    let cases: &[(&str, &[&str], bool, usize)] = &[
        (
            "inputComponent",
            &[
                "sw-entity-multi-id-select",
                "sw-number-field",
                "sw-text-field",
            ],
            true,
            3,
        ),
        ("selectedForm", &["sw-contact", "sw-fallback"], false, 0),
        ("elementType", &["span", "router-link"], true, 0),
        ("runtimeComponent", &["sw-fallback"], false, 0),
        ("ImportedCard", &["imported-card"], true, 1),
    ];
    for (expression, want, complete, contracts) in cases {
        let selector = single_selector(&format!(r#"<component :is="{expression}" />"#));
        let (resolved, components, resolved_complete) = idx
            .resolve_dynamic_component_contracts(&template_path, &selector, None)
            .unwrap();
        assert_eq!(resolved.names(), names(want), "{expression}");
        assert_eq!(resolved.complete, *complete, "{expression}");
        assert_eq!(
            resolved_complete,
            *complete && *contracts > 0,
            "{expression}"
        );
        assert_eq!(components.len(), *contracts, "{expression}");
    }
    idx.close().unwrap();
}

#[test]
fn resolve_router_view_dynamic_component_contracts_from_module_routes() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root.path().join("src/Resources/app/administration/src");
    let template_path = path_string(&admin_root, "module/sw-account/sw-account.html.twig");
    index_source(
        &idx,
        &path_string(&admin_root, "module/sw-account/index.js"),
        r#"
import template from './sw-account.html.twig';
Shopware.Component.register('sw-account', { template });
Shopware.Module.register('sw-account', {
    routes: {
        index: {
            component: 'sw-account',
            children: {
                history: { component: 'sw-account-history' },
                general: {
                    component: 'sw-account-general',
                    children: {
                        details: { component: 'sw-account-details' },
                    },
                },
            },
        },
    },
});"#,
    );
    let account_prop = || VueComponentProp {
        name: "account".to_owned(),
        type_: "Account".to_owned(),
        ..Default::default()
    };
    for (name, props) in [
        ("sw-account-general", vec![account_prop()]),
        ("sw-account-history", vec![account_prop()]),
        ("sw-account-details", vec![]),
    ] {
        idx.save_component(&VueComponent {
            name: name.to_owned(),
            props,
            file_path: path_string(&admin_root.join("component").join(name), "index.js"),
            ..Default::default()
        })
        .unwrap();
    }

    let cases = [
        (
            "destructured default name",
            r#"<router-view v-slot="{ Component }"><component :is="Component" /></router-view>"#,
            "Component",
        ),
        (
            "destructured alias",
            r#"<router-view v-slot="{ Component: view }"><component :is="view" /></router-view>"#,
            "view",
        ),
        (
            "whole route slot object",
            r#"<router-view v-slot="route"><component :is="route.Component" /></router-view>"#,
            "route.Component",
        ),
    ];
    for (name, source, expression) in cases {
        let (start_tag, selector) = start_tags(source)
            .into_iter()
            .find_map(|candidate| {
                twig_dynamic_component_selector(&candidate).map(|selector| (candidate, selector))
            })
            .expect("dynamic component");
        assert_eq!(selector.expression, expression, "{name}");

        let (without_context, _, complete) = idx
            .resolve_dynamic_component_contracts(&template_path, &selector, None)
            .unwrap();
        assert!(!complete, "{name}");
        assert!(without_context.names().is_empty(), "{name}");

        let (resolved, components, complete) = idx
            .resolve_dynamic_component_contracts(&template_path, &selector, Some(&start_tag))
            .unwrap();
        assert!(complete, "{name}");
        assert_eq!(
            resolved.names(),
            names(&["sw-account-general", "sw-account-history"]),
            "{name}"
        );
        assert_eq!(components.len(), 2, "{name}");
    }

    let start_tag = start_tags(r#"<component :is="Component" />"#).remove(0);
    let selector = twig_dynamic_component_selector(&start_tag).expect("selector");
    let (resolved, components, complete) = idx
        .resolve_dynamic_component_contracts(&template_path, &selector, Some(&start_tag))
        .unwrap();
    assert!(!complete);
    assert!(resolved.names().is_empty());
    assert!(components.is_empty());
    idx.close().unwrap();
}

#[test]
fn dynamic_component_return_alternatives_survive_cache_restore() {
    let root = temp_dir();
    let cache_path = root.path().join("cache");
    let admin_root = root.path().join("src/Resources/app/administration/src");
    let definition_path = path_string(&admin_root, "component/sw-host/index.js");
    let template_path = path_string(&admin_root, "component/sw-host/sw-host.html.twig");
    let idx = new_indexer(&cache_path);
    index_source(
        &idx,
        &definition_path,
        r#"
import template from './sw-host.html.twig';
export default Shopware.Component.register('sw-host', {
    template,
    computed: {
        inputComponent() {
            switch (this.kind) {
                case 'card': return 'sw-card';
                default: return 'sw-panel';
            }
        },
    },
});"#,
    );
    for name in ["sw-card", "sw-panel"] {
        idx.save_component(&VueComponent {
            name: name.to_owned(),
            file_path: path_string(&admin_root.join(name), "index.js"),
            ..Default::default()
        })
        .unwrap();
    }
    idx.close().unwrap();
    drop(idx);

    let reopened = new_indexer(&cache_path);
    let selector = single_selector(r#"<component :is="inputComponent" />"#);
    let (resolved, components, complete) = reopened
        .resolve_dynamic_component_contracts(&template_path, &selector, None)
        .unwrap();
    assert!(complete);
    assert_eq!(resolved.names(), names(&["sw-card", "sw-panel"]));
    assert_eq!(components.len(), 2);
    reopened.close().unwrap();
}

#[test]
fn resolve_cms_registry_dynamic_component_contracts() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root.path().join("src/Resources/app/administration/src");
    let definition_path = path_string(&admin_root, "component/sw-host/index.js");
    let template_path = path_string(&admin_root, "component/sw-host/sw-host.html.twig");
    index_source(
        &idx,
        &path_string(&admin_root, "module/sw-cms/elements.js"),
        r#"
cmsService.registerCmsElement({
    name: 'hero',
    component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
cmsService.registerCmsElement({
    name: 'text',
    component: 'sw-cms-el-text',
    configComponent: 'sw-cms-el-config-text',
    previewComponent: 'sw-cms-el-preview-text',
});"#,
    );
    index_source(
        &idx,
        &definition_path,
        r#"
import template from './sw-host.html.twig';
export default Shopware.Component.register('sw-host', {
    template,
    props: { runtimeConfig: Object },
    computed: {
        cmsServiceState() {
            return this.cmsService.getCmsServiceState();
        },
        elementConfig() {
            return this.cmsServiceState.elementRegistry[this.element.type];
        },
        cmsElements() {
            const entries = Object.entries(this.cmsService.getCmsElementRegistry());
            return Object.fromEntries(entries);
        },
    },
});"#,
    );
    for name in [
        "sw-cms-el-hero",
        "sw-cms-el-text",
        "sw-cms-el-config-hero",
        "sw-cms-el-config-text",
        "sw-cms-el-preview-hero",
        "sw-cms-el-preview-text",
    ] {
        idx.save_component(&VueComponent {
            name: name.to_owned(),
            file_path: path_string(&admin_root.join(name), "index.js"),
            ..Default::default()
        })
        .unwrap();
    }

    let cases: &[(&str, &str, &[&str])] = &[
        (
            "render component through service state",
            "elementConfig.component",
            &["sw-cms-el-hero", "sw-cms-el-text"],
        ),
        (
            "optional config component through service state",
            "elementConfig?.configComponent",
            &["sw-cms-el-config-hero", "sw-cms-el-config-text"],
        ),
        (
            "config component through copied registry",
            "cmsElements[element.type].configComponent",
            &["sw-cms-el-config-hero", "sw-cms-el-config-text"],
        ),
    ];
    for (name, expression, want) in cases {
        let selector = single_selector(&format!(r#"<component :is="{expression}" />"#));
        let (resolved, components, complete) = idx
            .resolve_dynamic_component_contracts(&template_path, &selector, None)
            .unwrap();
        assert!(complete, "{name}");
        let mut got = resolved.names();
        got.sort();
        let mut expected = names(want);
        expected.sort();
        assert_eq!(got, expected, "{name}");
        assert_eq!(components.len(), want.len(), "{name}");
    }

    let selector = single_selector(r#"<component :is="runtimeConfig.component" />"#);
    let (resolved, components, complete) = idx
        .resolve_dynamic_component_contracts(&template_path, &selector, None)
        .unwrap();
    assert!(!complete);
    assert!(resolved.names().is_empty());
    assert!(components.is_empty());
    idx.close().unwrap();
}
