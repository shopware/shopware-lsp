#![allow(unused_imports)]
use super::support::*;
use crate::*;

use std::path::Path;

fn admin_root(root: &Path) -> std::path::PathBuf {
    root.join("src/Administration/Resources/app/administration/src")
}

fn s(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

/// Go `index.UpdateLiveDocument(path, file.SyntaxTree().Root, source, file.LineIndex())`.
fn update_live(index: &AdminComponentIndexer, path: &str, source: &str) {
    let file = ParsedFile::new(path, source.as_bytes().to_vec());
    let root = file.syntax_tree().expect("syntax tree").root();
    index.update_live_document(path, Some(&root), source, Some(file.line_index()));
}

fn document_component(
    index: &AdminComponentIndexer,
    path: &str,
    source: &str,
) -> Option<VueComponent> {
    let file = ParsedFile::new(path, source.as_bytes().to_vec());
    let root = file.syntax_tree().expect("syntax tree").root();
    index
        .get_component_for_document(path, Some(&root), source, Some(file.line_index()))
        .expect("component for document")
}

fn effective(index: &AdminComponentIndexer, name: &str) -> Option<VueComponent> {
    index
        .get_effective_component(name)
        .expect("effective component")
}

#[test]
fn admin_indexer_composes_live_legacy_component_artifacts() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let admin_root = admin_root(root.path());
    let registration_path = s(&admin_root.join("component/index.js"));
    let component_dir = admin_root.join("component/sw-legacy-card");
    let definition_path = s(&component_dir.join("index.js"));
    let template_path = s(&component_dir.join("sw-legacy-card.html.twig"));

    let persisted_template = r#"<slot name="old-slot" :item="oldMember" />
{% block old_block %}{{ oldMember }}{% endblock %}"#;
    let persisted_definition = r#"import template from './sw-legacy-card.html.twig';
export default {
    template,
    props: {
        oldRequired: { type: String, required: true },
        oldOptional: Boolean,
    },
    emits: ['old-event'],
    data() { return { oldMember: 'old' }; },
};"#;
    let persisted_registration = r#"Shopware.Component.register(
    'sw-legacy-card',
    () => import('./sw-legacy-card'),
);"#;
    write_file(Path::new(&template_path), persisted_template);
    write_file(Path::new(&definition_path), persisted_definition);
    write_file(Path::new(&registration_path), persisted_registration);
    for (path, source) in [
        (&registration_path, persisted_registration),
        (&definition_path, persisted_definition),
        (&template_path, persisted_template),
    ] {
        index_source(&index, path, source);
    }

    let mut persisted = effective(&index, "sw-legacy-card").expect("persisted");
    assert!(persisted.component_prop("oldRequired").is_some());
    assert!(persisted.template_member("oldMember").is_some());
    assert!(persisted.component_slot("old-slot").is_some());
    assert!(persisted.component_block("old_block").is_some());
    persisted.props[0].name = "mutated-by-caller".to_owned();
    let cached = effective(&index, "sw-legacy-card").expect("cached");
    assert!(
        cached.component_prop("oldRequired").is_some(),
        "cached contracts must be defensive copies"
    );

    let live_definition = r#"import template from './sw-legacy-card.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
        liveOptional: String,
    },
    emits: ['live-event'],
    data() { return { liveMember: { id: 'draft' } }; },
};"#;
    update_live(&index, &definition_path, live_definition);
    let live = effective(&index, "sw-legacy-card").expect("live");
    let live_required = live.component_prop("liveRequired").expect("liveRequired");
    assert_eq!(live_required.type_, "Number");
    assert!(live_required.required);
    assert!(
        live.component_prop("oldRequired").is_none(),
        "an open definition replaces its persisted API"
    );
    assert!(live.template_member("liveMember").is_some());
    assert!(live.template_member("oldMember").is_none());
    assert!(live.component_event("live-event").is_some());

    // Removing the export from an open definition shadows the persisted file.
    let empty_definition = "export const draft = true;";
    update_live(&index, &definition_path, empty_definition);
    let empty = effective(&index, "sw-legacy-card").expect("empty");
    assert!(empty.props.is_empty());
    assert!(empty.component_prop("oldRequired").is_none());
    update_live(&index, &definition_path, live_definition);

    let live_template = r#"<slot name="live-slot" :record="liveMember" />
{% block live_block %}{{ liveMember.id }}{% endblock %}"#;
    update_live(&index, &template_path, live_template);
    let live = effective(&index, "sw-legacy-card").expect("live");
    assert!(live.component_slot("live-slot").is_some());
    assert!(
        live.component_slot("old-slot").is_none(),
        "an open template replaces persisted slots"
    );
    assert!(live.component_block("live_block").is_some());
    assert!(
        live.component_block("old_block").is_none(),
        "an open template replaces persisted blocks"
    );

    // A request-local Twig snapshot wins over the workspace overlay without
    // mutating it.
    let request_template = r#"<slot name="request-slot" :draft="liveMember" />"#;
    let request_component =
        document_component(&index, &template_path, request_template).expect("request component");
    assert!(request_component.component_slot("request-slot").is_some());
    assert!(request_component.component_slot("live-slot").is_none());
    let workspace_component = effective(&index, "sw-legacy-card").expect("workspace");
    assert!(
        workspace_component.component_slot("live-slot").is_some(),
        "request-local parsing must not change the overlay"
    );

    let live_registration = r#"Shopware.Component.register(
    'sw-renamed-card',
    () => import('./sw-legacy-card'),
);"#;
    update_live(&index, &registration_path, live_registration);
    assert!(effective(&index, "sw-legacy-card").is_none());
    let renamed = effective(&index, "sw-renamed-card").expect("renamed");
    assert!(renamed.component_prop("liveRequired").is_some());
    assert!(renamed.component_slot("live-slot").is_some());
    let names = index.get_all_component_names().unwrap();
    assert!(names.contains(&"sw-renamed-card".to_owned()));
    assert!(!names.contains(&"sw-legacy-card".to_owned()));

    index.remove_live_document(&registration_path);
    let fallback_registration = effective(&index, "sw-legacy-card").expect("fallback registration");
    assert!(
        fallback_registration
            .component_prop("liveRequired")
            .is_some(),
        "closing registration preserves other live buffers"
    );

    index.remove_live_document(&definition_path);
    let fallback_definition = effective(&index, "sw-legacy-card").expect("fallback definition");
    assert!(fallback_definition.component_prop("oldRequired").is_some());
    assert!(fallback_definition.component_prop("liveRequired").is_none());
    assert!(
        fallback_definition.component_slot("live-slot").is_some(),
        "closing definition preserves the live Twig buffer"
    );

    index.remove_live_document(&template_path);
    let fallback_template = effective(&index, "sw-legacy-card").expect("fallback template");
    assert!(fallback_template.component_slot("old-slot").is_some());
    assert!(fallback_template.component_slot("live-slot").is_none());
}

#[test]
fn admin_indexer_live_inline_registration_can_appear_and_disappear() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let path = s(&root
        .path()
        .join("src/Administration/Resources/app/administration/src/component/live.js"));
    let source = r#"Shopware.Component.register('sw-live-only', {
    props: { title: { type: String, required: true } },
    data() { return { visible: true }; },
});"#;
    let file = ParsedFile::new(path.as_str(), source.as_bytes().to_vec());
    let tree_root = file.syntax_tree().unwrap().root();
    index.update_live_document(
        &path,
        Some(&tree_root),
        source,
        Some(&LineIndex::new(source)),
    );
    let component = effective(&index, "sw-live-only").expect("live component");
    assert!(component.component_prop("title").is_some());
    assert!(component.template_member("visible").is_some());

    update_live(&index, &path, "export const unrelated = true;");
    assert!(effective(&index, "sw-live-only").is_none());
}

#[test]
fn admin_indexer_request_local_twig_retains_inherited_block_scope() {
    let root = temp_dir();
    let index = new_indexer(root.path());
    let parent_template = "/project/Resources/app/administration/src/parent.html.twig";
    let child_template = "/project/Resources/app/administration/src/child.html.twig";
    index
        .save_component(&VueComponent {
            name: "sw-parent".to_owned(),
            file_path: "/project/parent.js".to_owned(),
            template_path: parent_template.to_owned(),
            blocks: vec![TwigBlock {
                name: "shared".to_owned(),
                file_path: parent_template.to_owned(),
                scope_members: vec![TwigBlockScopeMember {
                    name: "set".to_owned(),
                    type_: "CustomFieldSet".to_owned(),
                    file_path: parent_template.to_owned(),
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    index
        .save_component(&VueComponent {
            name: "sw-child".to_owned(),
            kind: ComponentRegistrationKind::Extend,
            extends_component: "sw-parent".to_owned(),
            target_component: "sw-parent".to_owned(),
            file_path: "/project/child.js".to_owned(),
            template_path: child_template.to_owned(),
            blocks: vec![TwigBlock {
                name: "shared".to_owned(),
                file_path: child_template.to_owned(),
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    let source = "{% block shared %}{{ set.name }}{% endblock %}";
    let component = document_component(&index, child_template, source).expect("component");
    let block = component.component_block("shared").expect("shared block");
    let set = block.scope_member("set").expect("set scope member");
    assert_eq!(set.file_path, parent_template);
}

#[test]
fn admin_indexer_live_runtime_registries_replace_persisted_source_file() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let admin_root = admin_root(root.path());
    let registry_path = s(&admin_root.join("app/runtime.ts"));

    let persisted_source = r#"
Shopware.Application.addServiceProvider('old-service', factory);
Shopware.Store.register('old-store', {
    actions: { oldAction() {} },
});
Shopware.Mixin.register('shared-mixin', {
    data() { return { oldMixinMember: true }; },
});
Shopware.Directive.register('old-directive', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'old-element', component: 'sw-cms-el-old',
});
Shopware.Module.register('old-module', {
    routes: { index: { path: 'index', component: 'sw-old' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'old', roles: { viewer: { privileges: ['old:read'] } },
});"#;
    index_source(&index, &registry_path, persisted_source);
    index
        .save_component(&VueComponent {
            name: "sw-mixin-consumer".to_owned(),
            file_path: s(&admin_root.join("consumer.js")),
            mixins: vec!["shared-mixin".to_owned()],
            ..Default::default()
        })
        .unwrap();
    let persisted_component = effective(&index, "sw-mixin-consumer").expect("persisted");
    assert!(
        persisted_component
            .template_member("oldMixinMember")
            .is_some()
    );

    let live_source = r#"
Shopware.Application.addServiceProvider('live-service', factory);
Shopware.Store.register('live-store', {
    actions: { liveAction() {} },
});
Shopware.Mixin.register('shared-mixin', {
    data() { return { liveMixinMember: true }; },
});
Shopware.Directive.register('live-directive', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'live-element', component: 'sw-cms-el-live',
});
Shopware.Module.register('live-module', {
    routes: { detail: { path: 'detail', component: 'sw-live' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'live', roles: { editor: { privileges: ['live:update'] } },
});"#;
    update_live(&index, &registry_path, live_source);

    assert!(index.get_service("old-service").unwrap().is_empty());
    let live_services = index.get_service("live-service").unwrap();
    assert_eq!(live_services.len(), 1);
    assert_eq!(live_services[0].file_path, registry_path);

    assert!(index.get_store("old-store").unwrap().is_empty());
    let live_stores = index.get_store("live-store").unwrap();
    assert_eq!(live_stores.len(), 1);
    assert!(live_stores[0].member("liveAction").is_some());

    assert!(index.get_directive("old-directive").unwrap().is_empty());
    assert_eq!(index.get_directive("live-directive").unwrap().len(), 1);

    assert!(
        index
            .get_cms_registration(AdminCMSRegistrationKind::Element, "old-element")
            .unwrap()
            .is_empty()
    );
    let live_cms = index
        .get_cms_registration(AdminCMSRegistrationKind::Element, "live-element")
        .unwrap();
    assert_eq!(live_cms.len(), 1);
    assert_eq!(live_cms[0].component, "sw-cms-el-live");

    assert!(index.get_module("old-module").unwrap().is_empty());
    let (live_module, live_route) = index
        .get_module_route("live.module.detail")
        .unwrap()
        .expect("live route");
    assert_eq!(live_module.name, "live-module");
    assert_eq!(live_route.component, "sw-live");

    assert!(index.get_privilege("old.viewer").unwrap().is_empty());
    assert_eq!(index.get_privilege("live:update").unwrap().len(), 1);

    let live_component = effective(&index, "sw-mixin-consumer").expect("live component");
    assert!(live_component.template_member("liveMixinMember").is_some());
    assert!(live_component.template_member("oldMixinMember").is_none());

    // A syntactically valid but empty buffer replaces all registrations from
    // the source file until it is closed.
    update_live(&index, &registry_path, "export const draft = true;");
    assert!(index.get_all_services().unwrap().is_empty());
    assert!(index.get_all_modules().unwrap().is_empty());

    index.remove_live_document(&registry_path);
    assert_eq!(index.get_service("old-service").unwrap().len(), 1);
    let fallback_component = effective(&index, "sw-mixin-consumer").expect("fallback");
    assert!(
        fallback_component
            .template_member("oldMixinMember")
            .is_some()
    );
}

#[test]
fn admin_indexer_live_store_factory_replaces_persisted_members() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let admin_root = admin_root(root.path());
    let store_path = s(&admin_root.join("app/store/session.ts"));
    let factory_path = s(&admin_root.join("app/composable/use-session.ts"));
    write_file(Path::new(&factory_path), "");
    index_source(
        &index,
        &store_path,
        "import useSession from '../composable/use-session';
export default Shopware.Store.register('session', useSession);",
    );
    let persisted_factory = "const oldMember = ref(null);
export default function useSession() { return { oldMember }; }";
    index_source(&index, &factory_path, persisted_factory);

    let live_factory = "const liveMember = ref(null);
function save() {}
export default function useSession() { return { liveMember, save }; }";
    update_live(&index, &factory_path, live_factory);
    let stores = index.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert!(stores[0].member("liveMember").is_some());
    assert!(stores[0].member("save").is_some());
    assert!(stores[0].member("oldMember").is_none());

    update_live(&index, &factory_path, "export const draft = true;");
    let stores = index.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert!(stores[0].member("oldMember").is_none());

    index.remove_live_document(&factory_path);
    let stores = index.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert!(stores[0].member("oldMember").is_some());
}

#[test]
fn admin_indexer_live_vue_runtime_registry_replaces_persisted_file() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let path = s(&root
        .path()
        .join("src/Administration/Resources/app/administration/src/component/runtime.vue"));
    let persisted = "<script setup>
Shopware.Directive.register('vue-old', {});
</script>
<template><div /></template>";
    index_source(&index, &path, persisted);
    assert_eq!(index.get_directive("vue-old").unwrap().len(), 1);

    let live = "<script setup>
Shopware.Directive.register('vue-live', {});
</script>
<template><div /></template>";
    update_live(&index, &path, live);
    assert!(index.get_directive("vue-old").unwrap().is_empty());
    assert_eq!(index.get_directive("vue-live").unwrap().len(), 1);

    index.remove_live_document(&path);
    assert_eq!(index.get_directive("vue-old").unwrap().len(), 1);
}
