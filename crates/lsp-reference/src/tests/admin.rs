use std::path::PathBuf;
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminSourceRange, ComponentDefinition, ComponentRegistrationKind,
    VueComponent, VueComponentEvent, VueComponentMember, VueComponentMemberKind, VueComponentProp,
    VueComponentSlot, VueComponentSlotMember, VueLocalDirective,
};
use swlsp_lsp::TextDocument;
use swlsp_syntax::TextSize;
use swlsp_uriutil::file_uri;

use super::*;
use crate::AdminReferenceProvider;

pub(crate) struct Fixture {
    pub(crate) root: tempfile::TempDir,
    pub(crate) index: Arc<AdminComponentIndexer>,
    pub(crate) admin_root: PathBuf,
}

pub(crate) fn fixture() -> Fixture {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.path().join("cache")), None).unwrap());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    Fixture {
        root,
        index,
        admin_root,
    }
}

impl Fixture {
    pub(crate) fn path(&self, relative: &str) -> PathBuf {
        self.admin_root.join(relative)
    }

    pub(crate) fn index(&self, path: &Path, source: &str) {
        super::index(self.index.as_ref(), path, source);
    }

    pub(crate) fn save(&self, component: &VueComponent) {
        self.index.save_component(component).unwrap();
    }

    pub(crate) fn provider(&self) -> AdminReferenceProvider {
        AdminReferenceProvider::new(Some(self.index.clone()))
    }

    pub(crate) fn references(
        &self,
        path: &Path,
        source: &str,
        needle: &str,
        include_declaration: bool,
    ) -> Vec<protocol::Location> {
        references(
            &self.provider(),
            &admin_request(path, source, needle, include_declaration),
        )
    }
}

/// `VueComponent` has private fields; build it through `Default`.
pub(crate) fn component(name: &str, file: &Path, definition: &Path) -> VueComponent {
    let mut component = VueComponent::default();
    component.name = name.to_owned();
    component.file_path = p(file);
    component.definition_path = p(definition);
    component
}

pub(crate) fn prop(name: &str, type_: &str, file: &Path, line: u32) -> VueComponentProp {
    VueComponentProp {
        name: name.to_owned(),
        type_: type_.to_owned(),
        file_path: p(file),
        line,
        ..VueComponentProp::default()
    }
}

pub(crate) fn event(name: &str, type_: &str, file: &Path, line: u32) -> VueComponentEvent {
    VueComponentEvent {
        name: name.to_owned(),
        type_: type_.to_owned(),
        file_path: p(file),
        line,
        ..VueComponentEvent::default()
    }
}

/// The request at the middle of the last `needle` in `source`.
pub(crate) fn admin_request(
    path: &Path,
    source: &str,
    needle: &str,
    include_declaration: bool,
) -> ReferenceRequest {
    let document = Arc::new(TextDocument::new(file_uri(&p(path)), source, 1));
    let offset = source.rfind(needle).unwrap() + needle.len() / 2;
    let mut request = request(&document, offset, include_declaration);
    request.syntax.token = document
        .root()
        .and_then(|root| root.token_at(TextSize::from(offset as u32)));
    request
}

pub(crate) fn count(locations: &[protocol::Location], path: &Path) -> usize {
    count_uri(locations, &file_uri(&p(path)))
}

pub(crate) fn require_uri(locations: &[protocol::Location], path: &Path) {
    let want = file_uri(&p(path));
    assert!(
        locations.iter().any(|location| location.uri == want),
        "location {want} not found in {locations:#?}"
    );
}

#[test]
fn admin_references_cross_javascript_and_twig() {
    let f = fixture();
    let registration = f.path("main.ts");
    let definition = f.path("sw-card/index.ts");
    let template = f.path("sw-card/sw-card.html.twig");
    f.index(
        &registration,
        r"
Shopware.Component.register('sw-card', () => import('./sw-card'));
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
Shopware.Filter.getByName('currency');
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});
acl.can('product.viewer');
Shopware.Module.register('sw-product', {
    routes: { detail: { path: 'detail', component: 'sw-detail' } },
});
Shopware.Module.getModuleRegistry().get('sw-product');
this.$router.resolve({ name: 'sw.product.detail' });
",
    );
    f.index(
        &definition,
        r"
export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};",
    );
    let mut card = component("sw-card", &registration, &definition);
    card.injected = vec!["acl".to_owned()];
    f.save(&card);
    f.index(
        &template,
        r#"<sw-card v-tooltip.bottom="message" :disabled="acl.can('product.viewer')"><router-link :to="{ name: 'sw.product.detail' }" /></sw-card>"#,
    );
    let store_consumer = f.path("store-consumer.ts");
    let filter_consumer = f.path("filter-consumer.ts");
    let cases: &[(&str, &Path, &str, &str, bool, usize)] = &[
        (
            "component usages",
            &template,
            "<sw-card></sw-card>",
            "sw-card",
            false,
            3,
        ),
        (
            "component with declaration",
            &template,
            "<sw-card></sw-card>",
            "sw-card",
            true,
            4,
        ),
        (
            "service from this member",
            &definition,
            "export default { inject: ['acl'], methods: { check() { return this.acl; } } };",
            "this.acl",
            false,
            3,
        ),
        (
            "service excludes stale same-file declaration",
            &registration,
            "Shopware.Service('acl')",
            "acl",
            true,
            3,
        ),
        (
            "store member excludes stale same-file declaration",
            &registration,
            "Shopware.Store.get('session').login()",
            "login",
            true,
            1,
        ),
        (
            "store unregister usage",
            &store_consumer,
            "Shopware.Store.unregister('session')",
            "session",
            false,
            2,
        ),
        (
            "privilege excludes stale same-file declaration",
            &registration,
            "acl.can('product.viewer')",
            "product.viewer",
            true,
            3,
        ),
        (
            "privilege from Twig",
            &template,
            r#"<sw-card :disabled="acl.can('product.viewer')"></sw-card>"#,
            "product.viewer",
            false,
            3,
        ),
        (
            "module route from Twig",
            &template,
            r#"<router-link :to="{ name: 'sw.product.detail' }" />"#,
            "sw.product.detail",
            true,
            3,
        ),
        (
            "module registry usage",
            &registration,
            "Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            false,
            1,
        ),
        (
            "module registry excludes stale same-file declaration",
            &registration,
            "Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            true,
            1,
        ),
        (
            "directive from Twig",
            &template,
            r#"<div v-tooltip.bottom="message"></div>"#,
            "tooltip",
            false,
            2,
        ),
        (
            "directive excludes stale same-file declaration",
            &registration,
            "Shopware.Directive.getByName('tooltip')",
            "tooltip",
            true,
            2,
        ),
        (
            "filter registry usage",
            &filter_consumer,
            "Shopware.Filter.getByName('currency')",
            "currency",
            false,
            1,
        ),
        (
            "filter includes declaration",
            &filter_consumer,
            "Shopware.Filter.getByName('currency')",
            "currency",
            true,
            2,
        ),
    ];
    for (name, path, source, needle, include, want) in cases {
        let locations = f.references(path, source, needle, *include);
        assert_eq!(locations.len(), *want, "{name}: {locations:#?}");
    }
}

#[test]
fn admin_shopware_event_bus_references_include_typed_declaration() {
    let f = fixture();
    let util = f.path("core/service/util.service.ts");
    let event_bus = f.path("core/service/utils/eventBus.utils.ts");
    let event_bus_source = "interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
}
const emitter = mitt<Events>();
export default emitter;";
    let first = f.path("module/first/index.ts");
    let first_source = "Shopware.Utils.EventBus.on('save-event', handler);";
    let second = f.path("module/second/index.ts");
    let second_source = "const { EventBus } = Shopware.Utils;
EventBus.emit('save-event', payload);
EventBus.off('save-event', handler);";
    for (path, source) in [
        (
            &util,
            "import EventBus from './utils/eventBus.utils';\nexport default { EventBus };",
        ),
        (&event_bus, event_bus_source),
        (&first, first_source),
        (&second, second_source),
    ] {
        f.index(path, source);
    }
    let locations = f.references(&first, first_source, "save-event", false);
    assert_eq!(locations.len(), 3, "{locations:#?}");
    assert_eq!(count(&locations, &first), 1);
    assert_eq!(count(&locations, &second), 2);

    let locations = f.references(&first, first_source, "save-event", true);
    assert_eq!(locations.len(), 4, "{locations:#?}");
    let offset = event_bus_source.find("save-event").unwrap() as u32;
    let (line, character) = TextDocument::new(file_uri(&p(&event_bus)), event_bus_source, 1)
        .line_index()
        .position_utf16(offset);
    let declaration = locations
        .iter()
        .find(|location| location.uri == file_uri(&p(&event_bus)))
        .unwrap();
    assert_eq!(declaration.range.start.line, line);
    assert_eq!(declaration.range.start.character, character);
    assert_eq!(
        declaration.range.end.character,
        character + "save-event".len() as u32
    );
}

#[test]
fn admin_service_references_include_application_container_members() {
    let f = fixture();
    let registration = f.path("services.ts");
    let consumer = f.path("consumer.ts");
    let consumer_source = "
Shopware.Application.getContainer('service').acl;
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}";
    f.index(
        &registration,
        "Shopware.Application.addServiceProvider('acl', factory);",
    );
    f.index(&consumer, consumer_source);
    assert_eq!(
        f.references(&consumer, consumer_source, "acl", false).len(),
        2
    );
    assert_eq!(
        f.references(&consumer, consumer_source, "acl", true).len(),
        3
    );
}

#[test]
fn admin_references_keep_local_and_global_directives_separate() {
    let f = fixture();
    let global_path = f.path("app/directive/hide.ts");
    let definition = f.path("component/sw-owner/index.ts");
    let owner_template = f.path("component/sw-owner/sw-owner.html.twig");
    let other_template = f.path("component/other.html.twig");
    let template_source = r#"<div v-hide="hidden"></div>"#;
    for (path, source) in [
        (&global_path, "Shopware.Directive.register('hide', {});"),
        (&definition, "export default { directives: { hide: {} } };"),
        (&owner_template, template_source),
        (&other_template, template_source),
    ] {
        f.index(path, source);
    }
    let mut owner = component("sw-owner", &definition, &definition);
    owner.template_path = p(&owner_template);
    owner.local_directives = vec![VueLocalDirective {
        name: "hide".to_owned(),
        file_path: p(&definition),
        line: 1,
        ..VueLocalDirective::default()
    }];
    f.save(&owner);
    let local = f.references(&owner_template, template_source, "hide", true);
    assert_eq!(
        uris(&local),
        [file_uri(&p(&definition)), file_uri(&p(&owner_template))]
    );
    let global = f.references(&other_template, template_source, "hide", true);
    assert_eq!(
        uris(&global),
        [file_uri(&p(&global_path)), file_uri(&p(&other_template))]
    );
}

#[test]
fn admin_cms_registry_references() {
    let f = fixture();
    let registration = f.path("cms.ts");
    let consumer = f.path("consumer.ts");
    let registration_source = "
Shopware.Component.register('sw-cms-el-hero', {});
cmsService.registerCmsElement({ name: 'hero', component: 'sw-cms-el-hero' });
cmsService.registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });";
    let consumer_source = "
cmsService.getCmsElementConfigByName('hero');
cmsService.getCmsBlockConfigByName('hero-grid');";
    f.index(&registration, registration_source);
    f.index(&consumer, consumer_source);
    assert_eq!(
        f.references(&consumer, consumer_source, "hero');", true)
            .len(),
        3
    );
    assert_eq!(
        f.references(&consumer, consumer_source, "hero-grid", true)
            .len(),
        2
    );
    assert_eq!(
        f.references(&registration, registration_source, "sw-cms-el-hero", true)
            .len(),
        2
    );
}

#[test]
fn admin_references_respect_declaration_flag_and_stale_removal() {
    let f = fixture();
    let registration = f.path("main.ts");
    let usage = f.path("consumer.ts");
    let source = "Shopware.Component.override('sw-card', definition);";
    f.index(
        &registration,
        "Shopware.Component.register('sw-card', definition);",
    );
    f.index(&usage, source);
    let provider = f.provider();
    let request = admin_request(&usage, source, "sw-card", false);
    let locations = references(&provider, &request);
    assert_eq!(uris(&locations), [file_uri(&p(&usage))]);
    f.index(&usage, "export default {};");
    assert!(references(&provider, &request).is_empty());
}

#[test]
fn admin_model_references_unify_prop_and_update_event() {
    let f = fixture();
    let definition = f.path("sw-field/index.ts");
    let template = f.path("consumer/consumer.html.twig");
    let mut field = component("sw-field", &definition, &definition);
    field.props = vec![
        prop("modelValue", "Boolean", &definition, 3),
        prop("checked", "Boolean", &definition, 4),
    ];
    field.events = vec![
        event(
            "update:modelValue",
            "(value: boolean) => void",
            &definition,
            7,
        ),
        event("update:checked", "(value: boolean) => void", &definition, 8),
    ];
    f.save(&field);
    let source = r#"<sw-field v-model="enabled" v-model:checked="selected" />"#;
    f.index(&template, source);
    for needle in ["v-model", "checked"] {
        let locations = f.references(&template, source, needle, true);
        assert_uris_match(
            &locations,
            &[
                file_uri(&p(&template)),
                file_uri(&p(&definition)),
                file_uri(&p(&definition)),
            ],
        );
    }
}

fn computed_selector(name: &str, returns: &[&str]) -> VueComponentMember {
    VueComponentMember {
        name: name.to_owned(),
        kind: VueComponentMemberKind::Computed,
        return_expressions: returns.iter().map(|value| (*value).to_owned()).collect(),
        returns_complete: true,
        ..VueComponentMember::default()
    }
}

#[test]
fn admin_dynamic_component_references_include_every_static_branch() {
    let f = fixture();
    let definition = f.path("sw-card/index.ts");
    let template = f.path("consumer.html.twig");
    let mut card = component("sw-card", &definition, &definition);
    card.line = 4;
    f.save(&card);
    let source = r#"<component :is="active ? 'sw-card' : 'sw-card'" />"#;
    f.index(&template, source);
    let locations = f.references(&template, source, "sw-card", true);
    assert_uris_match(
        &locations,
        &[
            file_uri(&p(&definition)),
            file_uri(&p(&template)),
            file_uri(&p(&template)),
        ],
    );
}

#[test]
fn admin_inferred_dynamic_component_attribute_references_use_every_contract() {
    let f = fixture();
    let template = f.path("consumer.html.twig");
    let card_path = f.path("sw-card/index.ts");
    let panel_path = f.path("sw-panel/index.ts");
    let mut card = component("sw-card", &card_path, &card_path);
    card.props = vec![prop("title", "", &card_path, 5)];
    let mut panel = component("sw-panel", &panel_path, &panel_path);
    panel.props = vec![prop("title", "", &panel_path, 7)];
    let mut host = component("sw-host", &f.path("sw-host/index.ts"), Path::new(""));
    host.template_path = p(&template);
    host.members = vec![computed_selector(
        "dynamicCard",
        &["'sw-card'", "'sw-panel'"],
    )];
    for value in [&card, &panel, &host] {
        f.save(value);
    }
    let source = r#"<component :is="dynamicCard" v-bind="{ title: first }" /><component :is="dynamicCard" v-bind="{ title: second }" />"#;
    let locations = f.references(&template, source, "title", true);
    assert_eq!(locations.len(), 4, "{locations:#?}");
    assert_eq!(count(&locations, &template), 2);
    let declarations: Vec<_> = locations
        .iter()
        .filter(|location| location.uri != file_uri(&p(&template)))
        .cloned()
        .collect();
    assert_uris_match(
        &declarations,
        &[file_uri(&p(&card_path)), file_uri(&p(&panel_path))],
    );
}

#[test]
fn admin_references_from_declaration_include_indexed_inferred_dynamic_usage() {
    let f = fixture();
    let definition = f.path("component/sw-card/index.ts");
    let template = f.path("component/sw-host/sw-host.html.twig");
    let definition_source = "export default { props: { title: String } };";
    f.index(&definition, definition_source);
    let mut card = component("sw-card", &definition, &definition);
    card.props = vec![prop("title", "", &definition, 0)];
    f.save(&card);
    let mut host = component(
        "sw-host",
        &f.path("component/sw-host/index.ts"),
        Path::new(""),
    );
    host.template_path = p(&template);
    host.members = vec![computed_selector("dynamicCard", &["'sw-card'"])];
    f.save(&host);
    f.index(
        &template,
        r#"<component :is="dynamicCard" :title="title" />"#,
    );
    let locations = f.references(&definition, definition_source, "title", true);
    assert_uris_match(
        &locations,
        &[file_uri(&p(&definition)), file_uri(&p(&template))],
    );
}

#[test]
fn admin_references_keep_local_component_aliases_template_scoped() {
    let f = fixture();
    let first_definition = f.path("component/sw-first/index.ts");
    let first_template = f.path("component/sw-first/sw-first.html.twig");
    let second_definition = f.path("component/sw-second/index.ts");
    let second_template = f.path("component/sw-second/sw-second.html.twig");
    let template_source = r#"<mt-card-original @change="save"></mt-card-original>"#;
    let definition_source = |template: &str| {
        format!(
            "
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './{template}';
export default Shopware.Component.wrapComponentConfig({{
    template,
    components: {{ 'mt-card-original': MtCard }},
}});"
        )
    };
    for (definition, template, base) in [
        (&first_definition, &first_template, "sw-first.html.twig"),
        (&second_definition, &second_template, "sw-second.html.twig"),
    ] {
        f.index(definition, &definition_source(base));
        f.index(template, template_source);
    }
    let meteor = f.root.path().join("meteor/MtCard.d.ts");
    let mut card = component("mt-card", &meteor, &meteor);
    card.events = vec![event("change", "", &meteor, 3)];
    f.save(&card);

    let locations = f.references(&first_template, template_source, "mt-card-original", false);
    assert_eq!(locations.len(), 2, "{locations:#?}");
    assert!(
        locations
            .iter()
            .all(|location| location.uri == file_uri(&p(&first_template)))
    );

    let locations = f.references(&first_template, template_source, "mt-card-original", true);
    assert_eq!(locations.len(), 3, "{locations:#?}");
    assert_eq!(locations[0].uri, file_uri(&p(&first_definition)));

    let locations = f.references(&first_template, template_source, "change", false);
    assert_uris_match(
        &locations,
        &[
            file_uri(&p(&first_template)),
            file_uri(&p(&second_template)),
        ],
    );

    let source = definition_source("sw-first.html.twig");
    let locations = f.references(&first_definition, &source, "mt-card-original", false);
    assert_eq!(locations.len(), 2, "{locations:#?}");
    assert!(
        locations
            .iter()
            .all(|location| location.uri == file_uri(&p(&first_template)))
    );
}

fn declaration_range(line: u32, start: u32, end: u32) -> AdminSourceRange {
    AdminSourceRange {
        start_line: line,
        start_character: start,
        end_line: line,
        end_character: end,
        declaration: true,
        ..AdminSourceRange::default()
    }
}

fn location(path: &Path, line: u32, start: u32, end: u32) -> protocol::Location {
    protocol::Location {
        uri: file_uri(&p(path)),
        range: protocol::Range {
            start: protocol::Position {
                line,
                character: start,
            },
            end: protocol::Position {
                line,
                character: end,
            },
        },
    }
}

#[test]
fn admin_references_for_inherited_component_events_and_slots() {
    let f = fixture();
    let definition = f.path("component/sw-card/index.ts");
    let template = f.path("component/sw-card/sw-card.html.twig");
    let consumer = f.path("consumer.html.twig");
    let definition_source = "export default {
\tprops: { title: String },
    emits: ['update:modelValue'],
\tmethods: { update(value) { this.title; this.$emit('update:modelValue', value); } },
};";
    let event_range = declaration_range(2, 13, 30);
    let slot_range = declaration_range(0, 17, 23);
    let template_source = r#"<div><slot name="header" /></div>"#;
    let consumer_source = r#"<sw-card :title="title" @update:model-value="update"><template #header /></sw-card>
<sw-child :title="title" @update:model-value="update"><template #header /></sw-child>"#;
    for (path, source) in [
        (&definition, definition_source),
        (&template, template_source),
        (&consumer, consumer_source),
    ] {
        f.index(path, source);
    }
    let props = vec![prop("title", "", &definition, 2)];
    let mut update = event("update:modelValue", "", &definition, 2);
    update.name_range = event_range.clone();
    let slots = vec![VueComponentSlot {
        name: "header".to_owned(),
        file_path: p(&template),
        line: 1,
        name_range: slot_range.clone(),
        ..VueComponentSlot::default()
    }];
    let mut card = component("sw-card", &definition, &definition);
    card.kind = ComponentRegistrationKind::Register;
    card.template_path = p(&template);
    card.props = props.clone();
    card.events = vec![update.clone()];
    card.slots = slots.clone();
    f.save(&card);
    let parsed = ComponentDefinition {
        file_path: p(&definition),
        props,
        emits: vec!["update:modelValue".to_owned()],
        events: vec![update],
        slots,
        template_path: p(&template),
        ..ComponentDefinition::default()
    };
    f.index
        .save_component_definition(&p(definition.parent().unwrap()), &parsed)
        .unwrap();
    let mut child = component(
        "sw-child",
        &f.path("component/sw-child/index.ts"),
        Path::new(""),
    );
    child.kind = ComponentRegistrationKind::Extend;
    child.extends_component = "sw-card".to_owned();
    child.target_component = "sw-card".to_owned();
    f.save(&child);

    let cases: &[(&str, &Path, &str, &str, bool, usize)] = &[
        (
            "event from markup",
            &consumer,
            consumer_source,
            "update:model-value",
            false,
            3,
        ),
        (
            "event with declaration",
            &definition,
            definition_source,
            "update:modelValue",
            true,
            4,
        ),
        (
            "prop from markup",
            &consumer,
            consumer_source,
            ":title",
            false,
            3,
        ),
        (
            "prop with declaration",
            &definition,
            definition_source,
            "title:",
            true,
            4,
        ),
        (
            "slot from markup",
            &consumer,
            consumer_source,
            "header",
            false,
            2,
        ),
        (
            "slot from declaration",
            &template,
            template_source,
            "header",
            true,
            3,
        ),
    ];
    for (name, path, source, needle, include, want) in cases {
        let locations = f.references(path, source, needle, *include);
        assert_eq!(locations.len(), *want, "{name}: {locations:#?}");
        match *name {
            "event with declaration" => {
                assert!(
                    locations.contains(&location(&definition, 2, 13, 30)),
                    "{name}"
                );
            }
            "slot from declaration" => {
                assert!(
                    locations.contains(&location(&template, 0, 17, 23)),
                    "{name}"
                );
            }
            _ => {}
        }
    }
}

#[test]
fn admin_vue_lexical_references_respect_scopes() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(AdminComponentIndexer::new(&p(cache.path()), None).unwrap());
    let provider = AdminReferenceProvider::new(Some(index));
    let dir = tempfile::tempdir().unwrap();
    let path = dir
        .path()
        .join("Resources/app/administration/view.html.twig");
    let get = |source: &str, needle: &str, include: bool| {
        references(&provider, &admin_request(&path, source, needle, include))
    };
    let source = r#"<div v-for="item in items">
    {{ item.name }}
    <span v-for="item in item.children">{{ item.name }}</span>
    {{ item.id }}
</div>"#;
    assert_eq!(get(source, "item.id", false).len(), 3);
    let with_declaration = get(source, "item.id", true);
    assert_eq!(with_declaration.len(), 4);
    assert_eq!(with_declaration[0].range.start.character, 12);

    let event_source = r#"<button @click="$emit('selected', $event); save($event)" />"#;
    assert_eq!(get(event_source, "$event)", true).len(), 2);

    let member_source = r#"<div v-for="item in items">{{ item.name }} {{ item.name }}<span v-for="item in children">{{ item.inner }}</span></div>"#;
    let members = get(member_source, "name", true);
    assert_eq!(members.len(), 2);
    for location in &members {
        assert_eq!(
            location.range.end.character - location.range.start.character,
            4
        );
    }
    let nested = r#"<div v-for="item in items">{{ item.child.name }} {{ item.child.name }}</div>"#;
    assert_eq!(get(nested, "name", true).len(), 2);
}

#[test]
fn admin_component_instance_member_references() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.path().join("cache")), None).unwrap());
    let base = root
        .path()
        .join("Resources/app/administration/src/component/sw-view");
    let definition = base.join("index.ts");
    let template = base.join("view.html.twig");
    super::index(
        index.as_ref(),
        &definition,
        "interface Manufacturer { name: string; }\ninterface Product { manufacturer: Manufacturer; getManufacturer(): Manufacturer; }",
    );
    let member = |name: &str, type_: &str| VueComponentMember {
        name: name.to_owned(),
        kind: VueComponentMemberKind::Prop,
        type_: type_.to_owned(),
        file_path: p(&definition),
        ..VueComponentMember::default()
    };
    let mut view = component("sw-view", &definition, &definition);
    view.template_path = p(&template);
    view.members = vec![member("page", "Product"), member("pages", "Product[]")];
    index.save_component(&view).unwrap();
    let provider = AdminReferenceProvider::new(Some(index));
    let get = |source: &str, needle: &str, include: bool| {
        references(
            &provider,
            &admin_request(&template, source, needle, include),
        )
    };
    let source = "<div v-for=\"page in pages\">{{ page.manufacturer.name }}</div>
{{ page.manufacturer.name }} {{ page.manufacturer.name }}";
    assert_eq!(get(source, "name }}", false).len(), 2);
    let with_declaration = get(source, "name }}", true);
    assert_eq!(with_declaration.len(), 3);
    require_uri(&with_declaration, &definition);
    assert_eq!(get(source, "page.manufacturer", false).len(), 2);
    let root_with_declaration = get(source, "page.manufacturer", true);
    assert_eq!(root_with_declaration.len(), 3);
    require_uri(&root_with_declaration, &definition);
    let call_source = "{{ page.getManufacturer.name }} {{ page.getManufacturer().name }} {{ page.getManufacturer().name }}";
    assert_eq!(get(call_source, "name }}", false).len(), 2);
    assert_eq!(get(call_source, "name }}", true).len(), 3);
    let indexed_source = "{{ pages[1].manufacturer.name }} {{ pages[0].manufacturer.name }} {{ pages[0].manufacturer.name }}";
    assert_eq!(get(indexed_source, "name }}", false).len(), 2);
    let indexed_with_declaration = get(indexed_source, "name }}", true);
    assert_eq!(indexed_with_declaration.len(), 3);
    require_uri(&indexed_with_declaration, &definition);
}

fn slot_member(name: &str, type_: &str, file: &Path, line: u32) -> VueComponentSlotMember {
    VueComponentSlotMember {
        name: name.to_owned(),
        type_: type_.to_owned(),
        file_path: p(file),
        line,
        ..VueComponentSlotMember::default()
    }
}

fn slot(name: &str, file: &Path, line: u32) -> VueComponentSlot {
    VueComponentSlot {
        name: name.to_owned(),
        file_path: p(file),
        line,
        ..VueComponentSlot::default()
    }
}

#[test]
fn admin_whole_slot_object_member_references() {
    let f = fixture();
    let declaration = f.root.path().join("meteor/SwInheritWrapper.d.ts");
    let mut member = slot_member("currentValue", "", &declaration, 14);
    member.name_range = AdminSourceRange {
        identifier: true,
        ..declaration_range(13, 6, 18)
    };
    let mut wrapper = component("sw-inherit-wrapper", &declaration, Path::new(""));
    wrapper.slots = vec![VueComponentSlot {
        name: "content".to_owned(),
        members: vec![member],
        ..VueComponentSlot::default()
    }];
    f.save(&wrapper);
    let path = f
        .root
        .path()
        .join("Resources/app/administration/view.html.twig");
    let source = r#"<sw-inherit-wrapper><template #content="props"><div v-for="props in rows">{{ props.currentValue }}</div>{{ props.currentValue }} {{ props.currentValue }}</template></sw-inherit-wrapper>"#;
    assert_eq!(f.references(&path, source, "currentValue", false).len(), 2);
    let with_declaration = f.references(&path, source, "currentValue", true);
    assert_eq!(with_declaration.len(), 3);
    assert!(with_declaration.contains(&location(&declaration, 13, 6, 18)));
}

#[test]
fn admin_references_resolve_dynamic_slot_families() {
    let f = fixture();
    let template = f.path("component/sw-grid/grid.html.twig");
    let consumer = f.path("consumer.html.twig");
    let consumer_source = "<sw-grid><template #column-name /></sw-grid>
<sw-child><template #column-name /></sw-child>";
    f.index(&consumer, consumer_source);
    let mut grid = component(
        "sw-grid",
        &f.path("component/sw-grid/index.ts"),
        Path::new(""),
    );
    grid.template_path = p(&template);
    grid.slots = vec![VueComponentSlot {
        name_prefix: "column-".to_owned(),
        file_path: p(&template),
        line: 9,
        ..VueComponentSlot::default()
    }];
    f.save(&grid);
    let mut child = component(
        "sw-child",
        &f.path("component/sw-child/index.ts"),
        Path::new(""),
    );
    child.kind = ComponentRegistrationKind::Extend;
    child.extends_component = "sw-grid".to_owned();
    child.target_component = "sw-grid".to_owned();
    f.save(&child);
    assert_eq!(
        f.references(&consumer, consumer_source, "column-name", false)
            .len(),
        2
    );
    let locations = f.references(&consumer, consumer_source, "column-name", true);
    assert_eq!(locations.len(), 3);
    require_uri(&locations, &template);
}

#[test]
fn admin_references_resolve_dynamic_scoped_slot_payload_declarations() {
    let f = fixture();
    let first = f.path("a/card.twig");
    let second = f.path("b/card.twig");
    let mut card_a = component("sw-card-a", &f.path("a/index.ts"), Path::new(""));
    card_a.slots = vec![VueComponentSlot {
        name: "row".to_owned(),
        members: vec![slot_member("item", "Product", &first, 4)],
        ..VueComponentSlot::default()
    }];
    let mut card_b = component("sw-card-b", &f.path("b/index.ts"), Path::new(""));
    card_b.slots = vec![VueComponentSlot {
        name: "row".to_owned(),
        members: vec![slot_member("item", "Category", &second, 8)],
        ..VueComponentSlot::default()
    }];
    f.save(&card_a);
    f.save(&card_b);
    let consumer = f.path("consumer.html.twig");
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }} {{ props.item }}</template></component>"#;
    assert_eq!(f.references(&consumer, source, "item", false).len(), 2);
    let with_declarations = f.references(&consumer, source, "item", true);
    assert_eq!(with_declarations.len(), 4, "{with_declarations:#?}");
    require_uri(&with_declarations, &first);
    require_uri(&with_declarations, &second);
    assert_eq!(f.references(&consumer, source, "props\"", false).len(), 2);
    assert_eq!(f.references(&consumer, source, "props\"", true).len(), 3);
}

#[test]
fn admin_references_resolve_slots_below_dynamic_component_owner() {
    let f = fixture();
    let first = f.path("a/card.html.twig");
    let second = f.path("b/card.html.twig");
    for (name, index_path, template, line) in [
        ("sw-card-a", "a/index.ts", &first, 4),
        ("sw-card-b", "b/index.ts", &second, 8),
    ] {
        let mut card = component(name, &f.path(index_path), Path::new(""));
        card.template_path = p(template);
        card.slots = vec![slot("header", template, line)];
        f.save(&card);
    }
    let consumer = f.path("consumer.html.twig");
    let source =
        r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>"#;
    let without = f.references(&consumer, source, "header", false);
    assert_eq!(uris(&without), [file_uri(&p(&consumer))]);
    let with_declaration = f.references(&consumer, source, "header", true);
    assert_eq!(with_declaration.len(), 3, "{with_declaration:#?}");
    require_uri(&with_declaration, &first);
    require_uri(&with_declaration, &second);
}

#[test]
fn admin_references_follow_inherited_component_members() {
    let f = fixture();
    let base = f.path("component/sw-base/index.ts");
    let base_template = f.path("component/sw-base/sw-base.html.twig");
    let child = f.path("component/sw-child/index.ts");
    let child_template = f.path("component/sw-child/sw-child.html.twig");
    let base_source = "import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: { save() { this.count += 1; } },
    data() {
        const count = 0;
        return { count };
    },
});";
    let child_template_source = r#"<div v-for="count in rows">{{ count }}</div>{{ count }}"#;
    for (path, source) in [
        (&base, base_source),
        (&base_template, "{{ count }}"),
        (
            &child,
            "import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', {
    template,
    methods: { touch() { return this.count; } },
});",
        ),
        (&child_template, child_template_source),
    ] {
        f.index(path, source);
    }
    let without = f.references(&child_template, child_template_source, "count", false);
    assert_eq!(without.len(), 4, "{without:#?}");
    assert_eq!(count(&without, &child_template), 1);
    let with_declaration = f.references(&child_template, child_template_source, "count", true);
    assert_eq!(with_declaration.len(), 5, "{with_declaration:#?}");
    assert_eq!(count(&with_declaration, &base), 2);
    assert_eq!(f.references(&base, base_source, "count", true).len(), 5);
}
