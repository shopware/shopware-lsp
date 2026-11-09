#![allow(unused_imports)]
use super::support::*;
use crate::*;
use swlsp_indexer::Indexer;

fn assert_usage_occurrence_count(
    idx: &AdminComponentIndexer,
    kind: AdminSymbolKind,
    owner: &str,
    name: &str,
    expected: usize,
) {
    let sets = idx.get_usages(kind, owner, name).unwrap();
    let count: usize = sets.iter().map(|set| set.occurrences.len()).sum();
    assert_eq!(count, expected, "{kind} {name}");
}

fn admin_usage_set_occurrence_count(sets: &[AdminUsageSet]) -> usize {
    sets.iter().map(|set| set.occurrences.len()).sum()
}

fn node_at(root: &SyntaxNode, offset: usize) -> SyntaxNode {
    root.node_at(TextSize::from(offset as u32))
        .expect("node at offset")
}

fn target(kind: AdminSymbolKind, owner: &str, name: &str) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind,
        owner: owner.to_owned(),
        name: name.to_owned(),
    }
}

#[test]
fn admin_usage_index_tracks_registry_references_across_languages() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let js_path = path_string(&admin_root, "main.ts");
    let twig_path = path_string(&admin_root, "component.html.twig");
    let js_source = r#"
Shopware.Component.register('sw-card', definition);
Shopware.Component.extend('sw-child', 'sw-card', definition);
Shopware.Component.getComponentRegistry().has('sw-card');
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Service('acl');
Shopware.Store.register('session', { actions: { login() {} } });
Shopware.Store.get('session').login();
Shopware.Store.unregister('session');
Shopware.Mixin.register('listing', definition);
Shopware.Mixin.getByName('listing');
Shopware.Directive.register('tooltip', definition);
Shopware.Directive.getByName('tooltip');
Shopware.Filter.register('currency', value => value);
Shopware.Filter.getByName('currency');
Shopware.Module.register('sw-product', { routes: {} });
Shopware.Module.getModuleRegistry().get('sw-product');
this.$router.resolve({ name: 'sw.product.detail' });
export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};"#;
    let twig_source = r#"<sw-card v-tooltip.bottom="title" :disabled="acl.can('product.viewer')"><router-link :to="{ name: 'sw.product.detail' }" /></sw-card>"#;
    index_source(&idx, &js_path, js_source);
    index_source(&idx, &twig_path, twig_source);

    use AdminSymbolKind as K;
    assert_usage_occurrence_count(&idx, K::Component, "", "sw-card", 5);
    assert_usage_occurrence_count(&idx, K::Component, "", "sw-child", 1);
    assert_usage_occurrence_count(&idx, K::Service, "", "acl", 4);
    assert_usage_occurrence_count(&idx, K::Store, "", "session", 3);
    assert_usage_occurrence_count(&idx, K::StoreMember, "session", "login", 1);
    assert_usage_occurrence_count(&idx, K::Mixin, "", "listing", 2);
    assert_usage_occurrence_count(&idx, K::Directive, "", "tooltip", 3);
    assert_usage_occurrence_count(&idx, K::Filter, "", "currency", 2);
    assert_usage_occurrence_count(&idx, K::Module, "", "sw-product", 2);
    assert_usage_occurrence_count(&idx, K::Privilege, "", "product.viewer", 2);
    assert_usage_occurrence_count(&idx, K::ModuleRoute, "", "sw.product.detail", 2);

    let sets = idx.get_usages(K::Component, "", "sw-card").unwrap();
    let declarations = sets
        .iter()
        .flat_map(|set| &set.occurrences)
        .filter(|occurrence| occurrence.declaration)
        .count();
    assert_eq!(declarations, 1);

    index_source(&idx, &twig_path, "<div />");
    assert_usage_occurrence_count(&idx, K::Component, "", "sw-card", 3);
    assert_usage_occurrence_count(&idx, K::Directive, "", "tooltip", 2);
    idx.removed_files(std::slice::from_ref(&js_path)).unwrap();
    assert_usage_occurrence_count(&idx, K::Component, "", "sw-card", 0);
    assert_usage_occurrence_count(&idx, K::Directive, "", "tooltip", 0);
    assert_usage_occurrence_count(&idx, K::Filter, "", "currency", 0);
    idx.close().unwrap();
}

#[test]
fn javascript_filter_name_for_callee_honors_const_scope() {
    let source = r#"
const format = Shopware.Filter.getByName('currency');
format(1);
function inner() {
    const format = Filter.getByName('date');
    format('2026-01-01');
}
format(2);
let mutable = Filter.getByName('asset');
mutable('/icon.svg');"#;
    let root = parse_js(source);
    let mut calls = Vec::new();
    let mut mutable = Vec::new();
    for call in jsquery::calls(&root, &[]) {
        let callee = jsquery::call_callee(&call).expect("callee");
        match jsquery::identifier_text(&callee).as_deref() {
            Some("format") => calls.push(callee),
            Some("mutable") => mutable.push(callee),
            _ => {}
        }
    }
    assert_eq!(calls.len(), 3);
    for (callee, expected) in calls.iter().zip(["currency", "date", "currency"]) {
        assert_eq!(
            javascript_filter_name_for_callee(callee).as_deref(),
            Some(expected)
        );
    }
    assert_eq!(mutable.len(), 1);
    assert!(javascript_filter_name_for_callee(&mutable[0]).is_none());
}

#[test]
fn collect_javascript_usages_indexes_shopware_event_bus_events() {
    let source = r#"
Shopware.Utils.EventBus.on('save', handler);
const { EventBus } = Shopware.Utils;
EventBus.emit('save', payload);
EventBus.off('save', handler);
other.EventBus.emit('save');
"#;
    let sets = collect_javascript_usages(
        &parse_js(source),
        "/project/consumer.ts",
        &LineIndex::new(source),
    );
    let event_set = sets
        .iter()
        .find(|set| set.kind == AdminSymbolKind::EventBusEvent && set.name == "save")
        .expect("event bus usage set");
    assert_eq!(event_set.occurrences.len(), 3);
    for occurrence in &event_set.occurrences {
        assert!(!occurrence.declaration);
    }
}

#[test]
fn collect_javascript_usages_does_not_publish_mixin_events() {
    let source = r#"
Shopware.Component.register('sw-owner', { emits: ['component-event'] });
Shopware.Mixin.register('shared', { emits: ['mixin-event'] });
"#;
    let sets = collect_javascript_usages(
        &parse_js(source),
        "/project/consumer.ts",
        &LineIndex::new(source),
    );
    let events: BTreeSet<String> = sets
        .iter()
        .filter(|set| set.kind == AdminSymbolKind::ComponentEvent)
        .map(|set| set.name.clone())
        .collect();
    assert_eq!(events, BTreeSet::from(["component-event".to_owned()]));
}

#[test]
fn collect_twig_usages_treats_event_local_lexically() {
    let source = r#"<button @click.stop="save($event)">{{ $event }}</button>"#;
    let sets = collect_twig_usages(
        &parse_twig(source),
        "/project/event-template.html.twig",
        &LineIndex::new(source),
    );
    let mut members: HashMap<String, usize> = HashMap::new();
    for set in &sets {
        if set.kind != AdminSymbolKind::ComponentMember {
            continue;
        }
        *members.entry(set.name.clone()).or_default() += set.occurrences.len();
    }
    assert_eq!(members.get("save").copied().unwrap_or_default(), 1);
    assert_eq!(members.get("$event").copied().unwrap_or_default(), 1);
}

#[test]
fn admin_service_usages_include_application_container_members() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let path = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src/services.ts",
    );
    let source = r#"
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Application.getContainer('service').acl.can('read');
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}
Application.getContainer('factory').locale;
"#;
    index_source(&idx, &path, source);
    assert_usage_occurrence_count(&idx, AdminSymbolKind::Service, "", "acl", 3);

    let offset = source.rfind("acl").unwrap();
    let found = idx
        .javascript_symbol_at(&path, &node_at(&parse_js(source), offset + 1))
        .unwrap()
        .expect("symbol");
    assert_eq!(found, target(AdminSymbolKind::Service, "", "acl"));
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_scopes_local_directives_to_owning_template() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let global_path = path_string(&admin_root, "app/directive/hide.ts");
    let definition_path = path_string(&admin_root, "component/sw-owner/index.ts");
    let owner_template = path_string(&admin_root, "component/sw-owner/sw-owner.html.twig");
    let other_template = path_string(&admin_root, "component/other.html.twig");
    let definition_source = "export default {\n    directives: { hide: {} },\n};";
    for (path, source) in [
        (&global_path, "Shopware.Directive.register('hide', {});"),
        (&definition_path, definition_source),
        (&owner_template, r#"<div v-hide="hidden"></div>"#),
        (&other_template, r#"<div v-hide="hidden"></div>"#),
    ] {
        index_source(&idx, path, source);
    }
    let mut definition = parse_component_definition_with_line_index(
        &parse_js(definition_source),
        &LineIndex::new(definition_source),
    );
    set_definition_file_path(&mut definition, &definition_path);
    idx.save_component(&VueComponent {
        name: "sw-owner".to_owned(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: owner_template.clone(),
        local_directives: definition.local_directives.clone(),
        ..Default::default()
    })
    .unwrap();

    let offset = "<div v-".len() as u32;
    let local_target = idx
        .twig_symbol_at(
            &owner_template,
            &parse_twig(r#"<div v-hide="hidden"></div>"#),
            offset,
        )
        .unwrap()
        .expect("local directive");
    assert_eq!(local_target.owner, definition_path);
    let local_sets = idx.get_symbol_usages(&local_target).unwrap();
    assert_eq!(admin_usage_set_occurrence_count(&local_sets), 2);

    let global_target = idx
        .twig_symbol_at(
            &other_template,
            &parse_twig(r#"<div v-hide="hidden"></div>"#),
            offset,
        )
        .unwrap()
        .expect("global directive");
    assert!(global_target.owner.is_empty());
    let global_sets = idx.get_symbol_usages(&global_target).unwrap();
    assert_eq!(admin_usage_set_occurrence_count(&global_sets), 2);
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_tracks_cms_registries() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let path = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src/cms.ts",
    );
    let source = r#"
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
    defaultConfig: { card: { component: 'nested-component' } },
});
Shopware.Service('cmsService').registerCmsBlock({
    name: 'hero-grid', component: 'sw-cms-block-hero-grid',
    slots: { content: { type: 'hero' } },
});
Shopware.Service('cmsService').getCmsElementConfigByName('hero');
Shopware.Service('cmsService').getCmsBlockConfigByName('hero-grid');"#;
    index_source(&idx, &path, source);
    assert_usage_occurrence_count(&idx, AdminSymbolKind::CmsElement, "", "hero", 3);
    assert_usage_occurrence_count(&idx, AdminSymbolKind::CmsBlock, "", "hero-grid", 2);
    for name in [
        "sw-cms-el-hero",
        "sw-cms-el-config-hero",
        "sw-cms-el-preview-hero",
        "sw-cms-block-hero-grid",
    ] {
        assert_usage_occurrence_count(&idx, AdminSymbolKind::Component, "", name, 1);
    }

    let cases = [
        ("'hero', component", AdminSymbolKind::CmsElement, "hero"),
        ("type: 'hero'", AdminSymbolKind::CmsElement, "hero"),
        ("ByName('hero-grid'", AdminSymbolKind::CmsBlock, "hero-grid"),
        (
            "component: 'sw-cms-el-hero'",
            AdminSymbolKind::Component,
            "sw-cms-el-hero",
        ),
        (
            "configComponent: 'sw-cms-el-config-hero'",
            AdminSymbolKind::Component,
            "sw-cms-el-config-hero",
        ),
        (
            "previewComponent: 'sw-cms-el-preview-hero'",
            AdminSymbolKind::Component,
            "sw-cms-el-preview-hero",
        ),
        (
            "component: 'sw-cms-block-hero-grid'",
            AdminSymbolKind::Component,
            "sw-cms-block-hero-grid",
        ),
    ];
    for (needle, kind, name) in cases {
        let offset = source.find(needle).expect(needle);
        let node = node_at(&parse_js(source), offset + needle.find('\'').unwrap() + 1);
        let found = javascript_symbol_at(&node).unwrap_or_else(|| panic!("{needle}"));
        assert_eq!(found.kind, kind, "{needle}");
        assert_eq!(found.name, name, "{needle}");
    }

    let nested_offset = source.find("nested-component").unwrap() + 1;
    let nested_node = node_at(&parse_js(source), nested_offset);
    assert!(javascript_cms_component_reference_at(&nested_node).is_none());
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_tracks_source_owned_component_events_and_slots() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let definition_path = path_string(&admin_root, "component/sw-card/index.ts");
    let template_path = path_string(&admin_root, "component/sw-card/sw-card.html.twig");
    let consumer_path = path_string(&admin_root, "consumer.html.twig");
    index_source(
        &idx,
        &definition_path,
        r#"export default {
    emits: ['update:modelValue'],
    methods: { update(value) { this.$emit('update:modelValue', value); } },
};"#,
    );
    index_source(&idx, &template_path, r#"<div><slot name="header" /></div>"#);
    index_source(
        &idx,
        &consumer_path,
        r#"<sw-card @update:model-value.stop="update"><template #header /></sw-card>"#,
    );

    assert_usage_occurrence_count(
        &idx,
        AdminSymbolKind::ComponentEvent,
        &definition_path,
        "update:model-value",
        2,
    );
    assert_usage_occurrence_count(
        &idx,
        AdminSymbolKind::ComponentEvent,
        "sw-card",
        "update:model-value",
        1,
    );
    assert_usage_occurrence_count(
        &idx,
        AdminSymbolKind::ComponentSlot,
        &template_path,
        "header",
        1,
    );
    assert_usage_occurrence_count(&idx, AdminSymbolKind::ComponentSlot, "sw-card", "header", 1);

    let event_sets = idx
        .get_usages(
            AdminSymbolKind::ComponentEvent,
            &definition_path,
            "update:model-value",
        )
        .unwrap();
    assert_eq!(event_sets.len(), 1);
    let declarations = event_sets[0]
        .occurrences
        .iter()
        .filter(|occurrence| occurrence.declaration)
        .count();
    let camel_spellings = event_sets[0]
        .occurrences
        .iter()
        .filter(|occurrence| occurrence.name_style == AdminNameStyle::Camel)
        .count();
    assert_eq!(declarations, 1);
    assert_eq!(camel_spellings, 2);
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_tracks_finite_dynamic_component_contracts() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let template_path = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src/consumer.html.twig",
    );
    index_source(
        &idx,
        &template_path,
        r#"<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" @save="save" />"#,
    );
    for owner in ["sw-card", "sw-panel"] {
        assert_usage_occurrence_count(&idx, AdminSymbolKind::ComponentProp, owner, "title", 1);
        assert_usage_occurrence_count(&idx, AdminSymbolKind::ComponentEvent, owner, "save", 1);
    }
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_resolves_inferred_dynamic_component_contracts_at_query_time() {
    let root = temp_dir();
    let cache_path = root.path().join("cache");
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let template_path = path_string(&admin_root, "component/sw-host/sw-host.html.twig");
    let card_path = path_string(&admin_root, "component/sw-card/index.ts");
    let panel_path = path_string(&admin_root, "component/sw-panel/index.ts");
    let idx = new_indexer(&cache_path);
    let contract = |name: &str, path: &str| VueComponent {
        name: name.to_owned(),
        file_path: path.to_owned(),
        definition_path: path.to_owned(),
        props: ["title", "checked"]
            .into_iter()
            .map(|prop| VueComponentProp {
                name: prop.to_owned(),
                file_path: path.to_owned(),
                ..Default::default()
            })
            .collect(),
        events: ["save", "update:checked"]
            .into_iter()
            .map(|event| VueComponentEvent {
                name: event.to_owned(),
                file_path: path.to_owned(),
                ..Default::default()
            })
            .collect(),
        slots: vec![VueComponentSlot {
            name: "header".to_owned(),
            file_path: path.to_owned(),
            ..Default::default()
        }],
        ..Default::default()
    };
    for component in [
        contract("sw-card", &card_path),
        contract("sw-panel", &panel_path),
        VueComponent {
            name: "sw-host".to_owned(),
            file_path: path_string(&admin_root, "component/sw-host/index.ts"),
            template_path: template_path.clone(),
            members: vec![VueComponentMember {
                name: "dynamicCard".to_owned(),
                kind: VueComponentMemberKind::Computed,
                return_expressions: vec!["'sw-card'".to_owned(), "'sw-panel'".to_owned()],
                returns_complete: true,
                ..Default::default()
            }],
            ..Default::default()
        },
    ] {
        idx.save_component(&component).unwrap();
    }
    let source = concat!(
        r#"<component :is="dynamicCard" v-bind="{ title }" @save="save" v-model:checked="checked"><template #header /></component>"#,
        r#"<component :is="runtimeComponent" :title="title" />"#,
    );
    index_source(&idx, &template_path, source);

    let raw = idx
        .get_usages(
            AdminSymbolKind::ComponentProp,
            ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
            "title",
        )
        .unwrap();
    assert_eq!(raw.len(), 1);
    assert_eq!(raw[0].occurrences.len(), 2);
    assert_eq!(
        raw[0].occurrences[0].dynamic_component_selector,
        "dynamicCard"
    );
    assert_eq!(raw[0].occurrences[0].name_style, AdminNameStyle::Shorthand);
    assert_eq!(
        raw[0].occurrences[1].dynamic_component_selector,
        "runtimeComponent"
    );

    let assert_dynamic_usage = |query: AdminSymbolTarget, want_kind: AdminSymbolKind| {
        let sets = idx.get_symbol_usages(&query).unwrap();
        assert_eq!(admin_usage_set_occurrence_count(&sets), 1, "{query:?}");
        assert_eq!(sets.len(), 1);
        assert_eq!(sets[0].kind, want_kind);
        assert_eq!(sets[0].owner, ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER);
    };
    assert_dynamic_usage(
        target(AdminSymbolKind::ComponentProp, &card_path, "title"),
        AdminSymbolKind::ComponentProp,
    );
    assert_dynamic_usage(
        target(AdminSymbolKind::ComponentEvent, &card_path, "save"),
        AdminSymbolKind::ComponentEvent,
    );
    assert_dynamic_usage(
        target(AdminSymbolKind::ComponentSlot, &card_path, "header"),
        AdminSymbolKind::ComponentSlot,
    );
    assert_dynamic_usage(
        target(AdminSymbolKind::ComponentProp, &card_path, "checked"),
        AdminSymbolKind::ComponentModel,
    );
    assert_dynamic_usage(
        target(
            AdminSymbolKind::ComponentEvent,
            &card_path,
            "update:checked",
        ),
        AdminSymbolKind::ComponentModel,
    );

    idx.close().unwrap();
    drop(idx);
    let reopened = new_indexer(&cache_path);
    let sets = reopened
        .get_symbol_usages(&target(AdminSymbolKind::ComponentProp, &card_path, "title"))
        .unwrap();
    assert_eq!(admin_usage_set_occurrence_count(&sets), 1);
    reopened.close().unwrap();
}

#[test]
fn admin_usage_index_resolves_router_view_dynamic_component_contracts() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let definition_path = path_string(&admin_root, "module/sw-account/index.ts");
    let template_path = path_string(&admin_root, "module/sw-account/sw-account.html.twig");
    index_source(
        &idx,
        &definition_path,
        r#"
import template from './sw-account.html.twig';
Shopware.Component.register('sw-account', { template });
Shopware.Module.register('sw-account', {
    routes: {
        index: {
            component: 'sw-account',
            children: {
                general: { component: 'sw-account-general' },
                history: { component: 'sw-account-history' },
            },
        },
    },
});"#,
    );
    let general_path = path_string(&admin_root, "component/sw-account-general/index.ts");
    for component in [
        VueComponent {
            name: "sw-account-general".to_owned(),
            file_path: general_path.clone(),
            definition_path: general_path.clone(),
            props: vec![VueComponentProp {
                name: "account".to_owned(),
                file_path: general_path.clone(),
                ..Default::default()
            }],
            ..Default::default()
        },
        VueComponent {
            name: "sw-account-history".to_owned(),
            file_path: path_string(&admin_root, "component/sw-account-history/index.ts"),
            ..Default::default()
        },
    ] {
        idx.save_component(&component).unwrap();
    }
    let source = concat!(
        r#"<router-view v-slot="{ Component: view }"><component :is="view" :account="account" /></router-view>"#,
        r#"<router-view v-slot="route"><component :is="route.Component" :account="account" /></router-view>"#,
    );
    index_source(&idx, &template_path, source);
    let raw = idx
        .get_usages(
            AdminSymbolKind::ComponentProp,
            ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER,
            "account",
        )
        .unwrap();
    assert_eq!(raw.len(), 1);
    assert_eq!(raw[0].occurrences.len(), 2);
    assert!(raw[0].occurrences[0].dynamic_router_view);
    assert!(raw[0].occurrences[1].dynamic_router_view);
    let sets = idx
        .get_symbol_usages(&target(
            AdminSymbolKind::ComponentProp,
            &general_path,
            "account",
        ))
        .unwrap();
    assert_eq!(admin_usage_set_occurrence_count(&sets), 2);
    idx.close().unwrap();
}

#[test]
fn admin_usage_index_tracks_component_object_bindings() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let template_path = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src/consumer.html.twig",
    );
    let source = r#"<sw-card v-bind="{ title, 'is-disabled': disabled, ...attrs }" />"#;
    index_source(&idx, &template_path, source);

    assert_usage_occurrence_count(&idx, AdminSymbolKind::ComponentProp, "sw-card", "title", 1);
    assert_usage_occurrence_count(
        &idx,
        AdminSymbolKind::ComponentProp,
        "sw-card",
        "isDisabled",
        1,
    );
    let sets = idx
        .get_usages(AdminSymbolKind::ComponentProp, "sw-card", "title")
        .unwrap();
    assert_eq!(sets.len(), 1);
    assert_eq!(sets[0].occurrences.len(), 1);
    assert_eq!(sets[0].occurrences[0].name_style, AdminNameStyle::Shorthand);
    let member_sets = idx
        .get_usages(AdminSymbolKind::ComponentMember, "", "title")
        .unwrap();
    assert_eq!(member_sets.len(), 1);
    assert_eq!(member_sets[0].occurrences.len(), 1);
    assert_eq!(
        member_sets[0].occurrences[0].name_style,
        AdminNameStyle::Shorthand
    );

    let tree = parse_twig(source);
    for (needle, name) in [("title", "title"), ("is-disabled", "isDisabled")] {
        let offset = (source.find(needle).unwrap() + 1) as u32;
        let found = twig_symbol_at_offset(&tree, offset).expect(needle);
        assert_eq!(found.kind, AdminSymbolKind::ComponentProp);
        assert_eq!(found.owner, "sw-card");
        assert_eq!(found.name, name);
    }
    idx.close().unwrap();
}

#[test]
fn admin_symbol_targets_use_semantic_contexts() {
    use AdminSymbolKind as K;
    let js_cases = [
        (
            "Shopware.Component.extend('sw-child', 'sw-card', value)",
            "sw-card",
            K::Component,
            "",
            "sw-card",
        ),
        (
            "Shopware.Application.addServiceProvider('acl', factory)",
            "acl",
            K::Service,
            "",
            "acl",
        ),
        (
            "Shopware.Store.register({ id: 'session' })",
            "session",
            K::Store,
            "",
            "session",
        ),
        (
            "Shopware.Store.get('session').login()",
            "login",
            K::StoreMember,
            "session",
            "login",
        ),
        (
            "Shopware.Mixin.getByName('listing')",
            "listing",
            K::Mixin,
            "",
            "listing",
        ),
        (
            "Shopware.Component.getComponentRegistry().has('sw-card')",
            "sw-card",
            K::Component,
            "",
            "sw-card",
        ),
        (
            "Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            K::Module,
            "",
            "sw-product",
        ),
        (
            "acl.can('product.viewer')",
            "product.viewer",
            K::Privilege,
            "",
            "product.viewer",
        ),
        (
            "this.$router.resolve({ name: 'sw.product.detail' })",
            "sw.product.detail",
            K::ModuleRoute,
            "",
            "sw.product.detail",
        ),
    ];
    for (source, needle, kind, owner, name) in js_cases {
        let offset = source.find(needle).unwrap() + 1;
        let found = javascript_symbol_at(&node_at(&parse_js(source), offset)).expect(source);
        assert_eq!(found.kind, kind, "{source}");
        assert_eq!(found.owner, owner, "{source}");
        assert_eq!(found.name, name, "{source}");
    }

    let twig_source = r#"<sw-card :disabled="acl.can('product.viewer')"></sw-card>"#;
    let twig_root = parse_twig(twig_source);
    for offset in [
        twig_source.find("sw-card").unwrap() + 1,
        twig_source.rfind("sw-card").unwrap() + 1,
    ] {
        let found = twig_symbol_at_offset(&twig_root, offset as u32).expect("component");
        assert_eq!(found.kind, K::Component);
        assert_eq!(found.name, "sw-card");
    }
    let privilege_offset = (twig_source.find("product.viewer").unwrap() + 1) as u32;
    let privilege_target = twig_symbol_at_offset(&twig_root, privilege_offset).expect("privilege");
    assert_eq!(privilege_target.kind, K::Privilege);
    assert_eq!(privilege_target.name, "product.viewer");

    let markup_source = r#"<sw-card @update:model-value.stop="update" v-model:checked.trim="selected"><template #header /></sw-card>"#;
    let markup_root = parse_twig(markup_source);
    for (needle, kind, owner, name) in [
        (
            "update:model-value",
            K::ComponentEvent,
            "sw-card",
            "update:model-value",
        ),
        ("checked", K::ComponentModel, "sw-card", "v-model:checked"),
        ("header", K::ComponentSlot, "sw-card", "header"),
    ] {
        let offset = (markup_source.find(needle).unwrap() + 1) as u32;
        let found = twig_symbol_at_offset(&markup_root, offset).expect(needle);
        assert_eq!(found.kind, kind, "{needle}");
        assert_eq!(found.owner, owner, "{needle}");
        assert_eq!(found.name, name, "{needle}");
    }
}

#[test]
fn javascript_component_event_contexts() {
    for (source, needle, expected, found) in [
        (
            "export default { emits: ['update:modelValue'] };",
            "update:modelValue",
            "update:model-value",
            true,
        ),
        (
            "export default { emits: { save: null } };",
            "save",
            "save",
            true,
        ),
        (
            "export default { methods: { save() { this.$emit('saved'); } } };",
            "saved",
            "saved",
            true,
        ),
        (
            "export default { props: { saved: String } };",
            "saved",
            "",
            false,
        ),
    ] {
        let offset = source.find(needle).unwrap() + 1;
        let name = javascript_component_event_at(&node_at(&parse_js(source), offset));
        assert_eq!(name.is_some(), found, "{source}");
        assert_eq!(name.unwrap_or_default(), expected, "{source}");
    }
}

#[test]
fn javascript_registry_reference_contexts() {
    use AdminSymbolKind as K;
    let cases = [
        (
            "Shopware.Component.getComponentRegistry().get('sw-card')",
            "sw-card",
            K::Component,
            "get",
            true,
        ),
        (
            "Component.getComponentRegistry().has('sw-card')",
            "sw-card",
            K::Component,
            "has",
            true,
        ),
        (
            "Shopware.Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            K::Module,
            "get",
            true,
        ),
        (
            "Module.getModuleRegistry().has('sw-product')",
            "sw-product",
            K::Module,
            "has",
            true,
        ),
        (
            "other.getComponentRegistry().get('sw-card')",
            "sw-card",
            K::Unset,
            "",
            false,
        ),
        (
            "Shopware.Module.getModuleRegistry().get(moduleName)",
            "moduleName",
            K::Unset,
            "",
            false,
        ),
    ];
    for (source, needle, kind, operation, found) in cases {
        let offset = source.find(needle).unwrap() + 1;
        let reference = javascript_registry_reference_at(&node_at(&parse_js(source), offset));
        assert_eq!(reference.is_some(), found, "{source}");
        let Some(reference) = reference else {
            continue;
        };
        assert_eq!(reference.target.kind, kind, "{source}");
        assert_eq!(reference.operation, operation, "{source}");
        assert_eq!(reference.target.name, needle, "{source}");
    }
}

#[test]
fn javascript_module_route_reference_contexts() {
    let source = r#"
Module.register('sw-product', {
    name: 'product',
    routes: {
        detail: {
            path: 'detail/:id',
            redirect: { name: 'sw.product.detail.base' },
            meta: { parentPath: 'sw.product.parent' },
        },
    },
    navigation: [{ path: 'sw.product.index' }],
    settingsItem: { to: 'sw.settings.product.index' },
});
this.$router.push({ name: 'sw.product.detail' });
const unrelated = { name: 'sw.not.a.route', path: 'sw.also.not.a.route', to: 'sw.nope' };
"#;
    let root = parse_js(source);
    for (needle, want) in [
        ("product", false),
        ("detail/:id", false),
        ("sw.product.detail.base", true),
        ("sw.product.parent", true),
        ("sw.product.index", true),
        ("sw.settings.product.index", true),
        ("sw.product.detail", true),
        ("sw.not.a.route", false),
        ("sw.also.not.a.route", false),
        ("sw.nope", false),
    ] {
        let offset = source.find(&format!("'{needle}'")).unwrap() + 2;
        assert_eq!(
            is_javascript_module_route_reference(&node_at(&root, offset)),
            want,
            "{needle}"
        );
    }
}

#[test]
fn admin_indexer_ignores_generated_administration_artifacts() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let generated_path = path_string(
        root.path(),
        "custom/plugins/Demo/src/Resources/app/administration/.tmp/vite/deps/bundle.js",
    );
    index_source(
        &idx,
        &generated_path,
        "Shopware.Component.register('sw-generated', {});",
    );
    assert!(idx.get_component("sw-generated").unwrap().is_empty());
    assert_usage_occurrence_count(&idx, AdminSymbolKind::Component, "", "sw-generated", 0);
    idx.close().unwrap();
}

#[test]
fn admin_component_member_usages_follow_inheritance_and_lexical_shadowing() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let base_path = path_string(&admin_root, "component/sw-base/index.ts");
    let base_template = path_string(&admin_root, "component/sw-base/sw-base.html.twig");
    let child_path = path_string(&admin_root, "component/sw-child/index.ts");
    let child_template = path_string(&admin_root, "component/sw-child/sw-child.html.twig");
    index_source(
        &idx,
        &base_path,
        r#"import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    data() {
        const count = 0;
        return { count };
    },
    methods: { save() { this.count += 1; } },
});"#,
    );
    index_source(
        &idx,
        &child_path,
        r#"import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', {
    template,
    methods: { touch() { return this.count; } },
});"#,
    );
    index_source(&idx, &base_template, "{{ count }}");
    index_source(
        &idx,
        &child_template,
        r#"<div v-for="count in rows">{{ count }}</div>{{ count }}"#,
    );

    let component = idx
        .get_effective_component("sw-base")
        .unwrap()
        .expect("sw-base");
    let member = component.template_member("count").expect("count member");
    assert!(member.renameable());
    let sets = idx
        .get_symbol_usages(&component_member_target(&member))
        .unwrap();
    let occurrences: Vec<&AdminSourceRange> =
        sets.iter().flat_map(|set| &set.occurrences).collect();
    let declarations: Vec<_> = occurrences
        .iter()
        .filter(|occurrence| occurrence.declaration)
        .collect();
    let shorthand_declarations = declarations
        .iter()
        .filter(|occurrence| occurrence.name_style == AdminNameStyle::Shorthand)
        .count();
    assert_eq!(occurrences.len(), 5);
    assert_eq!(declarations.len(), 1);
    assert_eq!(shorthand_declarations, 1);
    idx.close().unwrap();
}
