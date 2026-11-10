use super::*;
use crate::refactor::admin_rename_helpers::admin_rename_replacement;
use std::path::Path;
use std::sync::Arc;
use swlsp_admin::{
    AdminComponentIndexer, AdminNameStyle, AdminSourceRange, AdminSymbolKind, AdminSymbolTarget,
    ComponentDefinition, ComponentRegistrationKind, VueComponent, VueComponentEvent,
    VueComponentMember, VueComponentMemberKind, VueComponentProp, VueComponentSlot,
    VueComponentSlotMember, VueLocalDirective,
};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, ProviderResult, RenameProvider, RenameRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

pub(crate) fn path_string(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

/// A rename request for `path` with the cursor at `offset`.
pub(crate) fn rename_request(
    path: &str,
    source: &str,
    offset: u32,
    new_name: &str,
) -> RenameRequest {
    let document = Arc::new(TextDocument::new(file_uri(path), source, 1));
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().expect("root");
    let node = root.node_at(TextSize::from(offset));
    let token = root.token_at(TextSize::from(offset));
    RenameRequest {
        params: protocol::RenameParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            position: protocol::Position { line, character },
            new_name: new_name.to_owned(),
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token,
            node,
        },
    }
}

/// Applies edits in their listed order .
pub(crate) fn apply_edits(source: &str, edits: Option<&Vec<protocol::TextEdit>>) -> String {
    let line_index = LineIndex::new(source.to_owned());
    let mut result = source.to_owned();
    for edit in edits.into_iter().flatten() {
        let start =
            line_index.offset_utf16(edit.range.start.line, edit.range.start.character) as usize;
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character) as usize;
        result = format!("{}{}{}", &result[..start], edit.new_text, &result[end..]);
    }
    result
}

pub(crate) fn edit_count(edit: &protocol::WorkspaceEdit) -> usize {
    edit.changes.values().map(Vec::len).sum()
}

macro_rules! component {
    ($($field:ident : $value:expr),* $(,)?) => {{
        let mut component = VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}

struct Fixture {
    _root: tempfile::TempDir,
    root: String,
    admin: String,
    index: Arc<AdminComponentIndexer>,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        self.index.close().expect("close");
    }
}

impl Fixture {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        let cache = path_string(&root.path().join("cache"));
        let index = Arc::new(AdminComponentIndexer::new(&cache, None).unwrap());
        let root_path = path_string(root.path());
        let admin = format!("{root_path}/src/Administration/Resources/app/administration/src");
        Self {
            _root: root,
            root: root_path,
            admin,
            index,
        }
    }

    fn path(&self, relative: &str) -> String {
        format!("{}/{relative}", self.admin)
    }

    fn index(&self, files: &[(&str, &str)]) {
        for (path, source) in files {
            self.index
                .index(&ParsedFile::new(
                    (*path).to_owned(),
                    source.as_bytes().to_vec(),
                ))
                .unwrap();
        }
    }

    fn save(&self, component: VueComponent) {
        self.index.save_component(&component).unwrap();
    }

    fn provider(&self) -> AdminRenameProvider {
        AdminRenameProvider::new(Some(self.index.clone()))
    }

    fn rename(
        &self,
        path: &str,
        source: &str,
        needle: &str,
        new_name: &str,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>> {
        self.provider().rename(
            &Context::background(),
            &request(path, source, needle, new_name),
        )
    }
}

fn request(path: &str, source: &str, needle: &str, new_name: &str) -> swlsp_lsp::RenameRequest {
    let offset = (source.rfind(needle).expect("needle") + needle.len() / 2) as u32;
    self::rename_request(path, source, offset, new_name)
}

fn changes<'a>(
    edit: &'a protocol::WorkspaceEdit,
    path: &str,
) -> Option<&'a Vec<protocol::TextEdit>> {
    edit.changes.get(&file_uri(path))
}

fn error_of(result: ProviderResult<Option<protocol::WorkspaceEdit>>) -> String {
    result.expect_err("rename error").to_string()
}

fn ok(result: ProviderResult<Option<protocol::WorkspaceEdit>>) -> protocol::WorkspaceEdit {
    result.expect("rename").expect("edit")
}

fn slot_component(name: &str, file: String, template: String, slot_file: &str) -> VueComponent {
    component! {
        name: name.into(),
        file_path: file,
        template_path: template,
        slots: vec![VueComponentSlot {
            name: "header".into(),
            file_path: slot_file.to_owned(),
            line: 1,
            ..VueComponentSlot::default()
        }],
    }
}

#[test]
fn admin_rename_updates_component_service_store_and_mixin_usages() {
    let fx = Fixture::new();
    let main_path = fx.path("main.ts");
    let definition_path = fx.path("sw-card/index.ts");
    let template_path = fx.path("sw-card/sw-card.html.twig");
    let main_source = "
Shopware.Component.register('sw-card', definition);
Shopware.Component.extend('sw-child', 'sw-card', definition);
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
Shopware.Filter.getByName('currency');";
    let definition_source = "export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};";
    let template_source = r#"<sw-card v-tooltip.bottom="message"></sw-card>"#;
    fx.index(&[
        (&main_path, main_source),
        (&definition_path, definition_source),
        (&template_path, template_source),
    ]);
    fx.save(component! {
        name: "sw-card".into(),
        file_path: main_path.clone(),
        definition_path: definition_path.clone(),
        injected: vec!["acl".into()],
    });
    let cases = [
        (
            "component",
            &template_path,
            template_source,
            "sw-card",
            "acme-card",
            4,
            2,
        ),
        (
            "service",
            &definition_path,
            definition_source,
            "this.acl",
            "accessControl",
            4,
            2,
        ),
        (
            "store",
            &main_path,
            "Shopware.Store.get('session')",
            "session",
            "userSession",
            3,
            1,
        ),
        (
            "mixin",
            &main_path,
            "Shopware.Mixin.getByName('listing')",
            "listing",
            "acme-listing",
            2,
            1,
        ),
        (
            "directive",
            &template_path,
            template_source,
            "tooltip",
            "hint",
            3,
            2,
        ),
        (
            "filter",
            &main_path,
            "Shopware.Filter.getByName('currency')",
            "currency",
            "money",
            2,
            1,
        ),
    ];
    for (name, path, source, needle, new_name, want_edits, want_files) in cases {
        let edit = ok(fx.rename(path, source, needle, new_name));
        assert_eq!(edit.changes.len(), want_files, "{name}");
        for edits in edit.changes.values() {
            for text_edit in edits {
                assert_eq!(text_edit.new_text, new_name, "{name}");
            }
        }
        assert_eq!(edit_count(&edit), want_edits, "{name}");
    }
    let edit = ok(fx.rename(&template_path, template_source, "sw-card", "acme-card"));
    assert_eq!(
        apply_edits(template_source, changes(&edit, &template_path)),
        r#"<acme-card v-tooltip.bottom="message"></acme-card>"#
    );
    assert!(
        apply_edits(main_source, changes(&edit, &main_path))
            .contains("Component.register('acme-card'")
    );
}

#[test]
fn admin_rename_updates_typed_shopware_event_bus_declaration_and_usages() {
    let fx = Fixture::new();
    let util_path = fx.path("core/service/util.service.ts");
    let event_bus_path = fx.path("core/service/utils/eventBus.utils.ts");
    let event_bus_source = "interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
    telemetry: { name: string };
}
const emitter = mitt<Events>();
export default emitter;";
    let first_path = fx.path("module/first/index.ts");
    let first_source = "Shopware.Utils.EventBus.on('save-event', handler);";
    let second_path = fx.path("module/second/index.ts");
    let second_source = "const { EventBus } = Shopware.Utils;
EventBus.emit('save-event', payload);
EventBus.off('save-event', handler);";
    fx.index(&[
        (
            &util_path,
            "import EventBus from './utils/eventBus.utils';\nexport default { EventBus };",
        ),
        (&event_bus_path, event_bus_source),
        (&first_path, first_source),
        (&second_path, second_source),
    ]);
    for (path, source) in [
        (&first_path, first_source),
        (&event_bus_path, event_bus_source),
    ] {
        let edit = ok(fx.rename(path, source, "save-event", "persisted-event"));
        assert_eq!(edit.changes.len(), 3);
        assert_eq!(edit_count(&edit), 4);
        assert!(
            apply_edits(event_bus_source, changes(&edit, &event_bus_path))
                .contains("'persisted-event': { id: string }")
        );
        assert!(
            apply_edits(first_source, changes(&edit, &first_path))
                .contains("EventBus.on('persisted-event'")
        );
        assert_eq!(
            apply_edits(second_source, changes(&edit, &second_path))
                .matches("persisted-event")
                .count(),
            2
        );
    }
    let edit = ok(fx.rename(&event_bus_path, event_bus_source, "telemetry", "analytics"));
    assert_eq!(edit_count(&edit), 1);
    assert!(
        apply_edits(event_bus_source, changes(&edit, &event_bus_path))
            .contains("analytics: { name: string }")
    );
    assert!(
        error_of(fx.rename(&first_path, first_source, "save-event", "telemetry"))
            .contains("already exists")
    );
    assert!(
        error_of(fx.rename(&first_path, first_source, "save-event", "invalid event"))
            .contains("valid Shopware EventBus event name")
    );
}

#[test]
fn admin_rename_updates_application_service_container_members() {
    let fx = Fixture::new();
    let registration_path = fx.path("services.ts");
    let consumer_path = fx.path("consumer.ts");
    let registration_source = "Shopware.Application.addServiceProvider('acl', factory);";
    let consumer_source = "
Shopware.Application.getContainer('service').acl;
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}";
    fx.index(&[
        (&registration_path, registration_source),
        (&consumer_path, consumer_source),
    ]);
    let edit = ok(fx.rename(&consumer_path, consumer_source, "acl", "accessControl"));
    assert_eq!(edit.changes.len(), 2);
    assert_eq!(changes(&edit, &registration_path).unwrap().len(), 1);
    assert_eq!(changes(&edit, &consumer_path).unwrap().len(), 2);
    assert!(
        apply_edits(registration_source, changes(&edit, &registration_path))
            .contains("addServiceProvider('accessControl'")
    );
    let renamed = apply_edits(consumer_source, changes(&edit, &consumer_path));
    assert!(renamed.contains(").accessControl"));
    assert!(renamed.contains("services.accessControl"));
}

#[test]
fn admin_rename_scopes_local_directive_to_owning_component() {
    let fx = Fixture::new();
    let global_path = fx.path("app/directive/hide.ts");
    let definition_path = fx.path("component/sw-owner/index.ts");
    let owner_template = fx.path("component/sw-owner/sw-owner.html.twig");
    let other_template = fx.path("component/other.html.twig");
    let definition_source = "export default { directives: { hide: {} } };";
    let template_source = r#"<div v-hide="hidden"></div>"#;
    fx.index(&[
        (&global_path, "Shopware.Directive.register('hide', {});"),
        (&definition_path, definition_source),
        (&owner_template, template_source),
        (&other_template, template_source),
    ]);
    fx.save(component! {
        name: "sw-owner".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: owner_template.clone(),
        local_directives: vec![VueLocalDirective {
            name: "hide".into(),
            file_path: definition_path.clone(),
            line: 1,
            ..VueLocalDirective::default()
        }],
    });
    let edit = ok(fx.rename(&owner_template, template_source, "hide", "conceal"));
    assert_eq!(edit.changes.len(), 2);
    assert!(changes(&edit, &global_path).is_none());
    assert!(changes(&edit, &other_template).is_none());
    assert_eq!(
        apply_edits(template_source, changes(&edit, &owner_template)),
        r#"<div v-conceal="hidden"></div>"#
    );
    assert_eq!(
        apply_edits(definition_source, changes(&edit, &definition_path)),
        "export default { directives: { conceal: {} } };"
    );
}

#[test]
fn admin_rename_updates_cms_registry_references() {
    let fx = Fixture::new();
    let registration_path = fx.path("cms.ts");
    let consumer_path = fx.path("consumer.ts");
    let registration_source = "
Shopware.Component.register('sw-cms-el-hero', {});
cmsService.registerCmsElement({ name: 'hero', component: 'sw-cms-el-hero' });
cmsService.registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });";
    let consumer_source = "
cmsService.getCmsElementConfigByName('hero');
cmsService.getCmsBlockConfigByName('hero-grid');";
    fx.index(&[
        (&registration_path, registration_source),
        (&consumer_path, consumer_source),
    ]);
    let edit = ok(fx.rename(&consumer_path, consumer_source, "hero');", "banner"));
    assert_eq!(edit.changes.len(), 2);
    assert_eq!(edit_count(&edit), 3);
    let renamed = apply_edits(registration_source, changes(&edit, &registration_path));
    assert!(renamed.contains("name: 'banner'"));
    assert!(renamed.contains("type: 'banner'"));
    let edit = ok(fx.rename(&consumer_path, consumer_source, "hero-grid", "banner-grid"));
    assert_eq!(edit_count(&edit), 2);
    let edit = ok(fx.rename(
        &registration_path,
        registration_source,
        "sw-cms-el-hero",
        "acme-cms-el-hero",
    ));
    assert_eq!(edit_count(&edit), 2);
    assert_eq!(
        apply_edits(registration_source, changes(&edit, &registration_path))
            .matches("acme-cms-el-hero")
            .count(),
        2
    );
    assert!(
        error_of(fx.rename(&consumer_path, consumer_source, "hero-grid", "banner grid"))
            .contains("valid Shopware CMS registry name")
    );
}

#[test]
fn admin_rename_rejects_conflicts_external_and_unsafe_names() {
    let fx = Fixture::new();
    let main_path = fx.path("main.ts");
    let main_source = "
Shopware.Component.register('sw-card', definition);
Shopware.Component.register('sw-existing', definition);
Shopware.Application.addServiceProvider('acl', factory);
export default { inject: ['acl'], methods: { run() { return this.acl; } } };";
    fx.index(&[(&main_path, main_source)]);
    fx.save(component! {
        name: "sw-card".into(),
        file_path: main_path.clone(),
        definition_path: main_path.clone(),
        injected: vec!["acl".into()],
    });
    let existing_path = fx.path("existing.ts");
    fx.index(&[(
        &existing_path,
        "Shopware.Component.register('sw-existing', definition);",
    )]);
    let runtime_path = fx.path("runtime-cms.ts");
    let runtime_source = "cmsService.getCmsElementConfigByName('runtime-element');";
    fx.index(&[(&runtime_path, runtime_source)]);

    assert!(
        error_of(fx.rename(&main_path, main_source, "sw-card", "sw-existing"))
            .contains("already exists")
    );
    assert!(
        error_of(fx.rename(&main_path, main_source, "this.acl", "access-control"))
            .contains("JavaScript identifier")
    );
    assert!(
        error_of(fx.rename(
            &runtime_path,
            runtime_source,
            "runtime-element",
            "renamed-element"
        ))
        .contains("declaration source is not indexed")
    );
    let meteor_path = format!(
        "{}/src/Administration/Resources/app/administration/node_modules/@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
        fx.root
    );
    fx.save(component! {
        name: "mt-button".into(),
        file_path: meteor_path.clone(),
        definition_path: meteor_path,
    });
    let meteor_template = fx.path("meteor.html.twig");
    let meteor_source = "<mt-button />";
    fx.index(&[(&meteor_template, meteor_source)]);
    assert!(
        error_of(fx.rename(&meteor_template, meteor_source, "mt-button", "acme-button"))
            .contains("external Meteor component")
    );
    let privilege_source = "acl.can('product.viewer')";
    assert!(
        fx.rename(
            &main_path,
            privilege_source,
            "product.viewer",
            "product.reader"
        )
        .unwrap()
        .is_none()
    );
}

#[test]
fn admin_rename_updates_template_scoped_component_alias() {
    let cases = [
        (
            "'mt-card-original': MtCard",
            "mt-card-original",
            "mt-card-original",
            "plugin-card",
            "'plugin-card': MtCard",
        ),
        (
            "MtCard",
            "mt-card",
            "MtCard",
            "plugin-card",
            "'plugin-card': MtCard",
        ),
    ];
    for (property, old_name, declaration_needle, new_name, expected_declaration) in cases {
        let fx = Fixture::new();
        let definition_path = fx.path("component/sw-wrapper/index.ts");
        let template_path = fx.path("component/sw-wrapper/sw-wrapper.html.twig");
        let definition_source = format!(
            "
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({{
    template,
    components: {{ {property} }},
}});"
        );
        let template_source = format!("<{old_name}></{old_name}>");
        fx.index(&[
            (&definition_path, &definition_source),
            (&template_path, &template_source),
        ]);
        let expected_template = format!("<{new_name}></{new_name}>");
        let edit = ok(fx.rename(&template_path, &template_source, old_name, new_name));
        assert_eq!(edit.changes.len(), 2, "{property}");
        assert_eq!(
            apply_edits(&template_source, changes(&edit, &template_path)),
            expected_template
        );
        assert!(
            apply_edits(&definition_source, changes(&edit, &definition_path))
                .contains(expected_declaration)
        );
        let edit = ok(fx.rename(
            &definition_path,
            &definition_source,
            declaration_needle,
            new_name,
        ));
        assert_eq!(edit.changes.len(), 2, "{property}");
        assert_eq!(
            apply_edits(&template_source, changes(&edit, &template_path)),
            expected_template
        );
    }
}

#[test]
fn admin_rename_event_includes_template_scoped_aliases() {
    let fx = Fixture::new();
    let target_path = fx.path("component/mt-card/index.ts");
    let wrapper_path = fx.path("component/sw-wrapper/index.ts");
    let local_template = fx.path("component/sw-wrapper/sw-wrapper.html.twig");
    let global_template = fx.path("global.html.twig");
    let target_source = "Shopware.Component.register('mt-card', { emits: ['change'] });";
    let wrapper_source = "
import MtCard from '../mt-card';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});";
    let local_source = r#"<mt-card-original @change="save" />"#;
    let global_source = r#"<mt-card @change="save" />"#;
    fx.index(&[
        (&target_path, target_source),
        (&wrapper_path, wrapper_source),
        (&local_template, local_source),
        (&global_template, global_source),
    ]);
    let edit = ok(fx.rename(&local_template, local_source, "change", "changed"));
    assert_eq!(edit.changes.len(), 3);
    assert!(
        apply_edits(target_source, changes(&edit, &target_path)).contains("emits: ['changed']")
    );
    assert!(apply_edits(local_source, changes(&edit, &local_template)).contains("@changed="));
    assert!(apply_edits(global_source, changes(&edit, &global_template)).contains("@changed="));
}

#[test]
fn admin_rename_updates_inherited_component_events_and_slots() {
    let fx = Fixture::new();
    let definition_path = fx.path("component/sw-card/index.ts");
    let template_path = fx.path("component/sw-card/sw-card.html.twig");
    let consumer_path = fx.path("consumer.html.twig");
    let definition_source = "export default {
\tprops: { positionIdentifier: String, existingProp: Boolean },
    emits: ['update:modelValue', 'save-complete'],
\tmethods: { update(value) { this.positionIdentifier; this.$emit('update:modelValue', value); } },
};";
    let template_source = r#"<div><slot name="header" /><slot name="footer" /></div>"#;
    let consumer_source = r#"<sw-card :position-identifier="position" @update:model-value="update"><template #header /></sw-card>
<sw-child :position-identifier="position" @update:model-value="update"><template #header /></sw-child>"#;
    fx.index(&[
        (&definition_path, definition_source),
        (&template_path, template_source),
        (&consumer_path, consumer_source),
    ]);
    let event = |name: &str| VueComponentEvent {
        name: name.into(),
        file_path: definition_path.clone(),
        line: 2,
        ..VueComponentEvent::default()
    };
    let slot = |name: &str| VueComponentSlot {
        name: name.into(),
        file_path: template_path.clone(),
        line: 1,
        ..VueComponentSlot::default()
    };
    let prop = |name: &str| VueComponentProp {
        name: name.into(),
        file_path: definition_path.clone(),
        line: 2,
        ..VueComponentProp::default()
    };
    let events = vec![event("update:modelValue"), event("save-complete")];
    let slots = vec![slot("header"), slot("footer")];
    let props = vec![prop("positionIdentifier"), prop("existingProp")];
    fx.save(component! {
        name: "sw-card".into(),
        kind: ComponentRegistrationKind::Register,
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        props: props.clone(),
        events: events.clone(),
        slots: slots.clone(),
    });
    fx.index
        .save_component_definition(
            &definition_path[..definition_path.rfind('/').unwrap()],
            &ComponentDefinition {
                file_path: definition_path.clone(),
                template_path: template_path.clone(),
                props,
                emits: vec!["update:modelValue".into(), "save-complete".into()],
                events,
                slots,
                ..ComponentDefinition::default()
            },
        )
        .unwrap();
    fx.save(component! {
        name: "sw-child".into(),
        kind: ComponentRegistrationKind::Extend,
        extends_component: "sw-card".into(),
        target_component: "sw-card".into(),
        file_path: fx.path("component/sw-child/index.ts"),
    });

    let edit = ok(fx.rename(
        &consumer_path,
        consumer_source,
        "position-identifier",
        "product-id",
    ));
    assert_eq!(edit.changes.len(), 2);
    assert_eq!(changes(&edit, &definition_path).unwrap().len(), 2);
    assert_eq!(changes(&edit, &consumer_path).unwrap().len(), 2);
    let definition = apply_edits(definition_source, changes(&edit, &definition_path));
    assert!(definition.contains("props: { productId:"));
    assert!(definition.contains("this.productId"));
    assert_eq!(
        apply_edits(consumer_source, changes(&edit, &consumer_path))
            .matches(":product-id")
            .count(),
        2
    );

    let edit = ok(fx.rename(
        &consumer_path,
        consumer_source,
        "update:model-value",
        "update:currentValue",
    ));
    assert_eq!(edit.changes.len(), 2);
    assert_eq!(changes(&edit, &definition_path).unwrap().len(), 2);
    assert_eq!(changes(&edit, &consumer_path).unwrap().len(), 2);
    let definition = apply_edits(definition_source, changes(&edit, &definition_path));
    assert!(definition.contains("emits: ['update:currentValue'"));
    assert!(definition.contains("$emit('update:currentValue'"));
    let consumer = apply_edits(consumer_source, changes(&edit, &consumer_path));
    assert_eq!(consumer.matches("@update:current-value").count(), 2);
    assert!(!consumer.contains("@update:model-value"));

    let edit = ok(fx.rename(&consumer_path, consumer_source, "header", "toolbar"));
    assert_eq!(edit.changes.len(), 2);
    assert_eq!(changes(&edit, &template_path).unwrap().len(), 1);
    assert_eq!(changes(&edit, &consumer_path).unwrap().len(), 2);
    assert!(
        apply_edits(template_source, changes(&edit, &template_path))
            .contains(r#"slot name="toolbar""#)
    );
    assert_eq!(
        apply_edits(consumer_source, changes(&edit, &consumer_path))
            .matches("#toolbar")
            .count(),
        2
    );

    for (needle, new_name) in [
        ("update:model-value", "save-complete"),
        ("position-identifier", "existing-prop"),
        ("header", "footer"),
    ] {
        assert!(
            error_of(fx.rename(&consumer_path, consumer_source, needle, new_name))
                .contains("already exists"),
            "{needle}"
        );
    }
}

#[test]
fn admin_event_rename_quotes_object_keys_and_rejects_external_meteor() {
    let fx = Fixture::new();
    let definition_path = fx.path("component/sw-card/index.ts");
    let consumer_path = fx.path("consumer.html.twig");
    let definition_source = "export default { emits: { saved: null } };";
    let consumer_source = r#"<sw-card @saved="onSaved" />"#;
    fx.index(&[
        (&definition_path, definition_source),
        (&consumer_path, consumer_source),
    ]);
    let event = VueComponentEvent {
        name: "saved".into(),
        file_path: definition_path.clone(),
        line: 1,
        ..VueComponentEvent::default()
    };
    fx.save(component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        events: vec![event.clone()],
    });
    fx.index
        .save_component_definition(
            &definition_path[..definition_path.rfind('/').unwrap()],
            &ComponentDefinition {
                file_path: definition_path.clone(),
                emits: vec!["saved".into()],
                events: vec![event],
                ..ComponentDefinition::default()
            },
        )
        .unwrap();
    let edit = ok(fx.rename(&consumer_path, consumer_source, "saved", "save-complete"));
    assert_eq!(
        apply_edits(definition_source, changes(&edit, &definition_path)),
        r#"export default { emits: { "save-complete": null } };"#
    );

    let meteor_path = format!(
        "{}/src/Administration/Resources/app/administration/node_modules/@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
        fx.root
    );
    let meteor_source = r#"<mt-button @click="onClick"><template #iconFront /></mt-button>"#;
    let meteor_usage = fx.path("meteor.html.twig");
    fx.save(component! {
        name: "mt-button".into(),
        file_path: meteor_path.clone(),
        definition_path: meteor_path.clone(),
        events: vec![VueComponentEvent {
            name: "click".into(),
            file_path: meteor_path.clone(),
            line: 3,
            ..VueComponentEvent::default()
        }],
        slots: vec![VueComponentSlot {
            name: "iconFront".into(),
            file_path: meteor_path,
            line: 4,
            ..VueComponentSlot::default()
        }],
    });
    fx.index(&[(&meteor_usage, meteor_source)]);
    for needle in ["click", "iconFront"] {
        assert!(
            error_of(fx.rename(&meteor_usage, meteor_source, needle, "renamed"))
                .contains("external Meteor"),
            "{needle}"
        );
    }
}

#[test]
fn admin_rename_vue_for_local_respects_shadowing() {
    let fx = Fixture::new();
    let path = format!("{}/x/Resources/app/administration/view.html.twig", fx.root);
    let source = r#"<div v-for="(item, index) in items">
    {{ item.name }}
    <span v-for="item in item.children">{{ item.name }}</span>
    {{ item.id }}
</div>"#;
    let edit = ok(fx.rename(&path, source, "item.id", "row"));
    let edits = changes(&edit, &path).unwrap();
    assert_eq!(edits.len(), 4);
    assert_eq!(
        apply_edits(source, Some(edits)),
        r#"<div v-for="(row, index) in items">
    {{ row.name }}
    <span v-for="item in row.children">{{ item.name }}</span>
    {{ row.id }}
</div>"#
    );
    assert!(
        error_of(fx.rename(&path, source, "item.id", "index"))
            .contains("already exists in this v-for scope")
    );
    let event_source = r#"<button @click="save($event)" />"#;
    assert!(
        error_of(fx.rename(&path, event_source, "$event", "event")).contains("implicit $event")
    );
}

#[test]
fn admin_rename_rejects_dynamic_slot_family_consumer() {
    let fx = Fixture::new();
    let template_path = fx.path("component/sw-grid/grid.html.twig");
    let consumer_path = fx.path("consumer.html.twig");
    let consumer_source = "<sw-grid><template #column-name /></sw-grid>";
    fx.index(&[(&consumer_path, consumer_source)]);
    fx.save(component! {
        name: "sw-grid".into(),
        file_path: fx.path("component/sw-grid/index.ts"),
        template_path: template_path.clone(),
        slots: vec![VueComponentSlot {
            name_prefix: "column-".into(),
            file_path: template_path,
            line: 9,
            ..VueComponentSlot::default()
        }],
    });
    assert!(
        error_of(fx.rename(
            &consumer_path,
            consumer_source,
            "column-name",
            "column-title"
        ))
        .contains("dynamic slot family")
    );
}

#[test]
fn admin_rename_resolves_safe_dynamic_slot_owner() {
    let fx = Fixture::new();
    let shared = fx.path("base/card.html.twig");
    fx.save(slot_component(
        "sw-card-a",
        fx.path("a/index.ts"),
        shared.clone(),
        &shared,
    ));
    fx.save(slot_component(
        "sw-card-b",
        fx.path("b/index.ts"),
        fx.path("b/card.html.twig"),
        &shared,
    ));
    let declaration_source = r#"<slot name="header" />"#;
    fx.index(&[(&shared, declaration_source)]);
    let consumer_path = fx.path("consumer.html.twig");
    let consumer_source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'" #header />
<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>"#;
    fx.index(&[(&consumer_path, consumer_source)]);
    let edit = ok(fx.rename(&consumer_path, consumer_source, "header", "toolbar"));
    assert_eq!(
        apply_edits(declaration_source, changes(&edit, &shared)),
        r#"<slot name="toolbar" />"#
    );
    let consumer = apply_edits(consumer_source, changes(&edit, &consumer_path));
    assert_eq!(consumer.matches("#toolbar").count(), 2);
    assert!(!consumer.contains("#header"));

    let first = fx.path("c/card.html.twig");
    let second = fx.path("d/card.html.twig");
    fx.save(slot_component(
        "sw-card-c",
        fx.path("c/index.ts"),
        first.clone(),
        &first,
    ));
    fx.save(slot_component(
        "sw-card-d",
        fx.path("d/index.ts"),
        second.clone(),
        &second,
    ));
    let ambiguous =
        r#"<component :is="active ? 'sw-card-c' : 'sw-card-d'"><template #header /></component>"#;
    assert!(
        error_of(fx.rename(&consumer_path, ambiguous, "header", "toolbar"))
            .contains("distinct declarations")
    );
}

#[test]
fn admin_rename_from_declaration_updates_indexed_inferred_dynamic_usages() {
    let fx = Fixture::new();
    let definition_path = fx.path("component/sw-card/index.ts");
    let component_template = fx.path("component/sw-card/sw-card.html.twig");
    let consumer_path = fx.path("component/sw-host/sw-host.html.twig");
    let definition_source = "export default {
    props: { title: String },
    emits: ['saved'],
};";
    let component_template_source = r#"<slot name="header"></slot>"#;
    let consumer_source = r#"<component :is="dynamicCard" v-bind="{ title }" @saved="save"><template #header /></component>"#;
    fx.index(&[
        (&definition_path, definition_source),
        (&component_template, component_template_source),
        (&consumer_path, consumer_source),
    ]);
    let props = vec![VueComponentProp {
        name: "title".into(),
        file_path: definition_path.clone(),
        ..VueComponentProp::default()
    }];
    let events = vec![VueComponentEvent {
        name: "saved".into(),
        file_path: definition_path.clone(),
        ..VueComponentEvent::default()
    }];
    let slots = vec![VueComponentSlot {
        name: "header".into(),
        file_path: component_template.clone(),
        ..VueComponentSlot::default()
    }];
    fx.save(component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: component_template.clone(),
        props: props.clone(),
        events: events.clone(),
        slots: slots.clone(),
    });
    fx.index
        .save_component_definition(
            &definition_path[..definition_path.rfind('/').unwrap()],
            &ComponentDefinition {
                file_path: definition_path.clone(),
                template_path: component_template.clone(),
                props,
                emits: vec!["saved".into()],
                events,
                slots,
                ..ComponentDefinition::default()
            },
        )
        .unwrap();
    let computed = |returns: &[&str]| VueComponentMember {
        name: "dynamicCard".into(),
        kind: VueComponentMemberKind::Computed,
        return_expressions: returns.iter().map(|value| (*value).to_owned()).collect(),
        returns_complete: true,
        ..VueComponentMember::default()
    };
    fx.save(component! {
        name: "sw-host".into(),
        file_path: fx.path("component/sw-host/index.ts"),
        template_path: consumer_path.clone(),
        members: vec![computed(&["'sw-card'"])],
    });

    let edit = ok(fx.rename(&definition_path, definition_source, "title", "heading"));
    assert!(
        apply_edits(definition_source, changes(&edit, &definition_path))
            .contains("props: { heading: String }")
    );
    assert!(
        apply_edits(consumer_source, changes(&edit, &consumer_path))
            .contains(r#"v-bind="{ heading: title }""#)
    );
    let edit = ok(fx.rename(&definition_path, definition_source, "saved", "stored"));
    assert!(
        apply_edits(consumer_source, changes(&edit, &consumer_path)).contains(r#"@stored="save""#)
    );

    let slot_request = request(
        &component_template,
        component_template_source,
        "header",
        "toolbar",
    );
    let offset = slot_request
        .syntax
        .document
        .as_ref()
        .unwrap()
        .line_index()
        .offset_utf16(
            slot_request.params.position.line,
            slot_request.params.position.character,
        );
    let slot_target = fx
        .index
        .twig_symbol_at(
            &component_template,
            slot_request.syntax.root.as_ref().unwrap(),
            offset,
        )
        .unwrap()
        .expect("slot target");
    assert_eq!(slot_target.owner, component_template);
    let edit = ok(fx.provider().rename(&Context::background(), &slot_request));
    assert!(apply_edits(consumer_source, changes(&edit, &consumer_path)).contains("#toolbar"));

    let panel_path = fx.path("component/sw-panel/index.ts");
    fx.save(component! {
        name: "sw-panel".into(),
        file_path: panel_path.clone(),
        definition_path: panel_path.clone(),
        props: vec![VueComponentProp {
            name: "title".into(),
            file_path: panel_path,
            ..VueComponentProp::default()
        }],
    });
    let ambiguous_template = fx.path("component/sw-ambiguous-host/template.html.twig");
    fx.save(component! {
        name: "sw-ambiguous-host".into(),
        file_path: fx.path("component/sw-ambiguous-host/index.ts"),
        template_path: ambiguous_template.clone(),
        members: vec![computed(&["'sw-card'", "'sw-panel'"])],
    });
    fx.index(&[(
        &ambiguous_template,
        r#"<component :is="dynamicCard" :title="title" />"#,
    )]);
    assert!(
        error_of(fx.rename(&definition_path, definition_source, "title", "heading"))
            .contains("distinct declarations")
    );
}

#[test]
fn admin_rename_scoped_slot_locals_under_dynamic_component() {
    let fx = Fixture::new();
    for (name, file) in [("sw-card-a", "a/index.ts"), ("sw-card-b", "b/index.ts")] {
        fx.save(component! {
            name: name.into(),
            file_path: fx.path(file),
            slots: vec![VueComponentSlot {
                name: "row".into(),
                members: vec![VueComponentSlotMember {
                    name: "item".into(),
                    ..VueComponentSlotMember::default()
                }],
                ..VueComponentSlot::default()
            }],
        });
    }
    let path = fx.path("consumer.html.twig");
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ row.name }}<div v-for="row in rows">{{ row.name }}</div>{{ row.id }}</template></component>"#;
    let edit = ok(fx.rename(&path, source, "row.id", "entry"));
    assert_eq!(
        apply_edits(source, changes(&edit, &path)),
        r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: entry }">{{ entry.name }}<div v-for="row in rows">{{ row.name }}</div>{{ entry.id }}</template></component>"#
    );
    let shorthand =
        r#"<sw-card-a><template #row="{ item }">{{ item.name }}</template></sw-card-a>"#;
    let edit = ok(fx.rename(&path, shorthand, "item.name", "row"));
    assert_eq!(
        apply_edits(shorthand, changes(&edit, &path)),
        r#"<sw-card-a><template #row="{ item: row }">{{ row.name }}</template></sw-card-a>"#
    );
    let alias = r#"<sw-card-a><template #row="{ item: row }">{{ row }}</template></sw-card-a>"#;
    assert!(error_of(fx.rename(&path, alias, "item", "entry")).contains("contract member"));
    let whole = r#"<sw-card-a><template #row="props">{{ props.item }}</template></sw-card-a>"#;
    assert!(error_of(fx.rename(&path, whole, "item", "entry")).contains("contract member"));
}

#[test]
fn admin_rename_rejects_compound_model_attribute() {
    let fx = Fixture::new();
    let definition_path = fx.path("sw-field/index.ts");
    let template_path = fx.path("consumer.html.twig");
    fx.save(component! {
        name: "sw-field".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        props: vec![VueComponentProp {
            name: "modelValue".into(),
            file_path: definition_path.clone(),
            line: 2,
            ..VueComponentProp::default()
        }],
        events: vec![VueComponentEvent {
            name: "update:modelValue".into(),
            file_path: definition_path,
            line: 3,
            ..VueComponentEvent::default()
        }],
    });
    let source = r#"<sw-field v-model="value" />"#;
    fx.index(&[(&template_path, source)]);
    assert!(
        error_of(fx.rename(&template_path, source, "v-model", "value"))
            .contains("compound v-model")
    );
}

#[test]
fn admin_rename_updates_dynamic_component_literal() {
    let fx = Fixture::new();
    let registration_path = fx.path("main.ts");
    let template_path = fx.path("consumer.html.twig");
    let registration_source = "Shopware.Component.register('sw-card', {});";
    let template_source = r#"<component :is="active ? 'sw-card' : 'sw-card'" />"#;
    fx.index(&[
        (&registration_path, registration_source),
        (&template_path, template_source),
    ]);
    let edit = ok(fx.rename(&template_path, template_source, "sw-card", "sw-panel"));
    assert_eq!(
        apply_edits(registration_source, changes(&edit, &registration_path)),
        "Shopware.Component.register('sw-panel', {});"
    );
    assert_eq!(
        apply_edits(template_source, changes(&edit, &template_path)),
        r#"<component :is="active ? 'sw-panel' : 'sw-panel'" />"#
    );
}

#[test]
fn admin_rename_updates_inherited_component_member_usages() {
    let fx = Fixture::new();
    let base_path = fx.path("component/sw-base/index.ts");
    let base_template = fx.path("component/sw-base/sw-base.html.twig");
    let child_path = fx.path("component/sw-child/index.ts");
    let child_template = fx.path("component/sw-child/sw-child.html.twig");
    let base_source = "import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: { save() { this.count += 1; } },
    data() {
        const count = 0;
        return { count };
    },
});";
    let child_source = "import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', {
    template,
    methods: { touch() { return this.count; } },
});";
    let base_template_source = "{{ count }}";
    let child_template_source = r#"<div v-for="count in rows">{{ count }}</div>{{ count }}"#;
    fx.index(&[
        (&base_path, base_source),
        (&base_template, base_template_source),
        (&child_path, child_source),
        (&child_template, child_template_source),
    ]);
    let edit = ok(fx.rename(&child_template, child_template_source, "count", "total"));
    assert_eq!(edit.changes.len(), 4);
    let base = apply_edits(base_source, changes(&edit, &base_path));
    assert!(base.contains("return { total: count };"));
    assert!(base.contains("this.total += 1"));
    assert!(apply_edits(child_source, changes(&edit, &child_path)).contains("this.total"));
    assert_eq!(
        apply_edits(base_template_source, changes(&edit, &base_template)),
        "{{ total }}"
    );
    assert_eq!(
        apply_edits(child_template_source, changes(&edit, &child_template)),
        r#"<div v-for="count in rows">{{ count }}</div>{{ total }}"#
    );
    let from_declaration = ok(fx.rename(&base_path, base_source, "count", "total"));
    assert_eq!(edit.changes, from_declaration.changes);
    let unsaved = format!("{child_template_source} {{{{ count }}}}");
    let from_unsaved = ok(fx.rename(&child_template, &unsaved, "count", "total"));
    assert_eq!(changes(&from_unsaved, &child_template).unwrap().len(), 2);
    assert_eq!(
        apply_edits(&unsaved, changes(&from_unsaved, &child_template)),
        r#"<div v-for="count in rows">{{ count }}</div>{{ total }} {{ total }}"#
    );
}

#[test]
fn admin_rename_rejects_inherited_component_member_conflict() {
    let fx = Fixture::new();
    let base_path = fx.path("component/sw-base/index.ts");
    let base_template = fx.path("component/sw-base/sw-base.html.twig");
    let child_path = fx.path("component/sw-child/index.ts");
    fx.index(&[
        (
            &base_path,
            "import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    data() { return { count: 0 }; },
});",
        ),
        (&base_template, "{{ count }}"),
        (
            &child_path,
            "Component.extend('sw-child', 'sw-base', {
    computed: { total() { return this.count; } },
});",
        ),
    ]);
    assert!(
        error_of(fx.rename(&base_template, "{{ count }}", "count", "total"))
            .contains("already exists")
    );
}

#[test]
fn admin_rename_replacement_expands_object_binding_shorthand() {
    let mut shorthand = AdminSourceRange {
        name_style: AdminNameStyle::Shorthand,
        ..AdminSourceRange::default()
    };
    let target = |kind| AdminSymbolTarget {
        kind,
        name: "title".into(),
        ..AdminSymbolTarget::default()
    };
    assert_eq!(
        admin_rename_replacement(
            &target(AdminSymbolKind::ComponentProp),
            "heading",
            &shorthand
        ),
        "heading: title"
    );
    assert_eq!(
        admin_rename_replacement(
            &target(AdminSymbolKind::ComponentMember),
            "heading",
            &shorthand
        ),
        "title: heading"
    );
    shorthand.declaration = true;
    assert_eq!(
        admin_rename_replacement(
            &target(AdminSymbolKind::ComponentMember),
            "heading",
            &shorthand
        ),
        "heading: title"
    );
}
