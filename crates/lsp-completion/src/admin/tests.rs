//! Port of Go `admin_completion_test.go`.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_admin::{
    ADMIN_PRIVILEGE_ADMINISTRATOR, AdminCMSRegistrationKind, AdminComponentIndexer,
    ComponentRegistrationKind, TwigBlock, TwigBlockScopeMember, VueComponent, VueComponentEvent,
    VueComponentMember, VueComponentMemberKind, VueComponentProp, VueComponentSlot,
    VueComponentSlotMember, VueDynamicComponentSelector, VueLocalComponent, VueLocalDirective,
};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::syntax as jssyntax;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;
use tempfile::TempDir;

use super::AdminCompletionProvider;

macro_rules! component {
    ($($field:ident : $value:expr),* $(,)?) => {{
        let mut component = VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}

type Items = Vec<protocol::CompletionItem>;
type ByLabel = HashMap<String, protocol::CompletionItem>;

struct Fixture {
    dir: TempDir,
    index: Arc<AdminComponentIndexer>,
}

impl Fixture {
    /// An index whose cache lives in `<root>/cache` (or the root itself).
    fn new(cache_subdir: bool) -> Self {
        let dir = tempfile::tempdir().unwrap();
        let cache = if cache_subdir {
            dir.path().join("cache")
        } else {
            dir.path().to_path_buf()
        };
        std::fs::create_dir_all(&cache).unwrap();
        let index = Arc::new(AdminComponentIndexer::new(cache.to_str().unwrap(), None).unwrap());
        Self { dir, index }
    }

    fn root(&self) -> String {
        self.dir.path().to_string_lossy().into_owned()
    }

    fn path(&self, relative: &str) -> String {
        format!("{}/{relative}", self.root())
    }

    fn index(&self, path: &str, source: &str) {
        self.index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }

    fn save(&self, component: VueComponent) {
        self.index.save_component(&component).unwrap();
    }

    fn provider(&self) -> AdminCompletionProvider {
        AdminCompletionProvider::new(self.index.clone())
    }

    fn update_live(&self, path: &str, source: &str) {
        let document = TextDocument::new(file_uri(path), source, 2);
        self.index.update_live_document(
            path,
            document.root().as_ref(),
            source,
            Some(document.line_index()),
        );
    }
}

/// A request whose position is `position` (`None` keeps 0:0) and whose
/// node (and optionally token) is the one at `node_offset`.
fn request(
    path: &str,
    source: &str,
    position: Option<u32>,
    node_offset: u32,
    with_token: bool,
) -> CompletionRequest {
    let document = Arc::new(TextDocument::new(file_uri(path), source, 1));
    let root = document.root().unwrap();
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    if let Some(position) = position {
        let (line, character) = document.line_index().position_utf16(position);
        params.position = protocol::Position { line, character };
    }
    let node = root.node_at(TextSize::from(node_offset));
    let token = if with_token {
        root.token_at(TextSize::from(node_offset))
    } else {
        None
    };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token,
            node,
        },
    }
}

fn complete(
    provider: &AdminCompletionProvider,
    path: &str,
    source: &str,
    position: Option<u32>,
    node_offset: u32,
    with_token: bool,
) -> Items {
    provider.get_completions(
        &Context::background(),
        &request(path, source, position, node_offset, with_token),
    )
}

/// Completion at the end of `marker` with the node and token one byte left.
fn complete_after(
    provider: &AdminCompletionProvider,
    path: &str,
    source: &str,
    marker: &str,
) -> Items {
    let offset = after(source, marker);
    complete(
        provider,
        path,
        source,
        Some(offset),
        offset.saturating_sub(1),
        true,
    )
}

fn after(source: &str, marker: &str) -> u32 {
    (source
        .find(marker)
        .unwrap_or_else(|| panic!("marker {marker:?}"))
        + marker.len()) as u32
}

fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

fn by_label(items: Items) -> ByLabel {
    items
        .into_iter()
        .map(|item| (item.label.clone(), item))
        .collect()
}

fn doc(item: &protocol::CompletionItem) -> &str {
    item.documentation
        .as_ref()
        .map_or("", |documentation| documentation.value.as_str())
}

fn has(labels: &[String], label: &str) -> bool {
    labels.iter().any(|candidate| candidate == label)
}

fn parse_js(source: &str) -> SyntaxNode {
    swlsp_parser_js::parse(source).root()
}

fn js_node_at(root: &SyntaxNode, line: u32, col: u32) -> SyntaxNode {
    let source = root.text().to_string();
    let offset = swlsp_syntax::LineIndex::new(source.as_str()).offset(line, col);
    root.node_at(TextSize::from(offset)).unwrap()
}

fn prop(name: &str, type_: &str) -> VueComponentProp {
    VueComponentProp {
        name: name.into(),
        type_: type_.into(),
        ..Default::default()
    }
}

#[test]
fn is_in_extend_parent_argument() {
    let provider = AdminCompletionProvider::default();
    let cases = [
        (
            "Component.extend('my-component', 'sw-base', () => import('./index'));",
            0,
            35,
            true,
        ),
        (
            "Shopware.Component.extend('my-component', 'sw-base', () => import('./index'));",
            0,
            45,
            true,
        ),
        (
            "Component.extend('my-component', 'sw-base', () => import('./index'));",
            0,
            20,
            false,
        ),
        (
            "Component.register('my-component', () => import('./index'));",
            0,
            22,
            false,
        ),
        (
            "const { Component } = Shopware;\nComponent.extend('foo', 'bar', {});",
            1,
            26,
            true,
        ),
    ];
    for (code, line, col, expected) in cases {
        let root = parse_js(code);
        let node = js_node_at(&root, line, col);
        assert_eq!(
            provider.is_in_extend_parent_argument(&node),
            expected,
            "{code}"
        );
    }
}

#[test]
fn is_second_string_argument() {
    let provider = AdminCompletionProvider::default();
    let root = parse_js("Component.extend('first', 'second', () => {});");
    assert!(provider.is_second_string_argument(&js_node_at(&root, 0, 28)));
    assert!(!provider.is_second_string_argument(&js_node_at(&root, 0, 20)));
}

#[test]
fn get_slot_completions_without_index() {
    let provider = AdminCompletionProvider::default();
    assert!(
        provider
            .get_slot_completions("sw-card", None, "")
            .is_empty()
    );
}

#[test]
fn vue_sfc_completion_routes_embedded_template_and_script() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.save(component! {
            name: "sw-card".into(),
            file_path: format!("{admin_root}/sw-card/index.ts"),
            props: vec![prop("title", "String")],
    });
    let provider = fixture.provider();
    let source = "<template><sw-card :tit /></template>
<script setup>
Shopware.Component.getComponentRegistry().get('sw-');
</script>";
    let path = format!("{admin_root}/Card.vue");
    assert!(has(
        &labels(&complete_after(&provider, &path, source, ":tit")),
        ":title"
    ));
    assert!(has(
        &labels(&complete_after(&provider, &path, source, "'sw-")),
        "sw-card"
    ));
}

#[test]
fn filter_completion_uses_live_registry() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let registration_path = format!("{admin_root}/app/filter/currency.ts");
    fixture.index(
        &registration_path,
        "Shopware.Filter.register('currency', (value: number): string => String(value));",
    );
    let provider = fixture.provider();
    let consumer = format!("{admin_root}/consumer.ts");
    let run = || {
        let source = "Shopware.Filter.getByName('curr')";
        let offset = (source.find("curr").unwrap() + 2) as u32;
        complete(&provider, &consumer, source, Some(offset), offset, true)
    };
    let items = run();
    let names = labels(&items);
    assert!(has(&names, "currency"));
    assert_eq!(names.iter().filter(|name| *name == "currency").count(), 1);
    for item in &items {
        if item.label == "currency" {
            assert!(
                item.detail.contains("(value: number) => string"),
                "{}",
                item.detail
            );
        }
    }

    let live = "Shopware.Filter.register('date', (value: Date): string => value.toISOString());";
    let root = parse_js(live);
    fixture.index.update_live_document(
        &registration_path,
        Some(&root),
        live,
        Some(&swlsp_syntax::LineIndex::new(live)),
    );
    let names = labels(&run());
    assert!(has(&names, "date"));
    assert!(!has(&names, "currency"));
}

#[test]
fn completion_uses_imported_script_setup_props() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let component_dir = format!("{admin_root}/component/sw-imported-card");
    let component_path = format!("{component_dir}/index.vue");
    let component_source = r#"<template>{{ hea }}<slot name="header" :item="heading" /></template>
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
        "export interface CardProps { mode: 'small' | 'large'; optionalTitle?: string }
export interface HeaderPayload { item: string; count?: number }
export interface CardSlots { header(props: HeaderPayload): unknown }",
    );
    fixture.index(
        &format!("{component_dir}/draft-contracts.ts"),
        "export interface DraftProfile { city: string; zip: string }
export interface DraftRow { label: string; active: boolean }
export interface DraftProps { profile: DraftProfile; rows: DraftRow[] }",
    );
    let draft_panel_path = format!("{component_dir}/DraftPanel.vue");
    fixture.index(
        &draft_panel_path,
        r#"<template><slot :item="label" /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
defineSlots<{ default(props: { item: string; active: boolean }): unknown }>();
</script>"#,
    );
    let provider = fixture.provider();
    let c = |path: &str, source: &str, marker: &str| {
        labels(&complete_after(&provider, path, source, marker))
    };

    let consumer = format!("{admin_root}/consumer.vue");
    let names = c(
        &consumer,
        "<template><sw-imported-card :mo /></template>",
        ":mo",
    );
    assert!(has(&names, ":mode"));
    assert!(has(&names, ":optional-title"));
    let slot_names = c(
        &consumer,
        "<template><sw-imported-card><template #hea /></sw-imported-card></template>",
        "#hea",
    );
    assert!(has(&slot_names, "header"));
    let slot_members = c(
        &consumer,
        r#"<template><sw-imported-card><template #header="{ it }" /></sw-imported-card></template>"#,
        "{ it",
    );
    assert!(has(&slot_members, "item"));
    assert!(has(
        &c(&component_path, component_source, "{{ hea"),
        "heading"
    ));
    let live_source = component_source.replace("heading", "headline");
    let live_bindings = c(&component_path, &live_source, "{{ hea");
    assert!(has(&live_bindings, "headline"));
    assert!(!has(&live_bindings, "heading"));
    let live_imported = "<template>
    {{ profile. }}
    <div v-for=\"row in rows\">{{ row. }}</div>
</template>
<script setup lang=\"ts\">
import type { DraftProps } from './draft-contracts';
const { profile, rows } = defineProps<DraftProps>();
</script>";
    let nested_profile = c(&component_path, live_imported, "profile.");
    assert!(has(&nested_profile, "city"));
    assert!(has(&nested_profile, "zip"));
    let nested_row = c(&component_path, live_imported, "row.");
    assert!(has(&nested_row, "label"));
    assert!(has(&nested_row, "active"));

    let local = |body: &str| {
        format!(
            "<template>{body}</template>
<script setup lang=\"ts\">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>"
        )
    };
    assert!(has(
        &c(&component_path, &local("<Dra />"), "<Dra"),
        "draft-panel"
    ));
    let local_props = c(&component_path, &local("<DraftPanel :la />"), ":la");
    assert!(has(&local_props, "label"));
    assert!(has(&local_props, "count"));
    assert!(has(
        &c(
            &component_path,
            &local("<DraftPanel><template #de /></DraftPanel>"),
            "#de"
        ),
        "default"
    ));
    let local_slot_members = c(
        &component_path,
        &local(r#"<DraftPanel><template #default="{ it }" /></DraftPanel>"#),
        "{ it",
    );
    assert!(has(&local_slot_members, "item"));
    assert!(has(&local_slot_members, "active"));
    assert!(has(
        &c(
            &component_path,
            &local(r#"<component :is="'draft-panel'" :la />"#),
            ":la"
        ),
        "label"
    ));

    let live_child = r#"<template><slot name="preview" :record="draftRecord" /></template>
<script setup lang="ts">
defineProps<{ draftLabel: string; enabled?: boolean }>();
defineSlots<{ preview(props: { record: { id: string } }): unknown }>();
</script>"#;
    fixture.update_live(&draft_panel_path, live_child);
    let live_child_props = c(&component_path, &local("<DraftPanel :dra />"), ":dra");
    assert!(has(&live_child_props, "draft-label"));
    assert!(!has(&live_child_props, "label"));
    assert!(has(
        &c(
            &component_path,
            &local("<DraftPanel><template #pre /></DraftPanel>"),
            "#pre"
        ),
        "preview"
    ));
    assert!(has(
        &c(
            &component_path,
            &local(r#"<DraftPanel><template #preview="{ rec }" /></DraftPanel>"#),
            "{ rec"
        ),
        "record"
    ));

    fixture.index.remove_live_document(&draft_panel_path);
    let fallback = c(&component_path, &local("<DraftPanel :la />"), ":la");
    assert!(has(&fallback, "label"));
    assert!(!has(&fallback, "draft-label"));
}

#[test]
fn completion_composes_unsaved_legacy_definition_and_twig() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let registration_path = format!("{admin_root}/component/index.js");
    let component_dir = format!("{admin_root}/component/sw-legacy-live");
    let definition_path = format!("{component_dir}/index.js");
    let template_path = format!("{component_dir}/sw-legacy-live.html.twig");
    std::fs::create_dir_all(&component_dir).unwrap();
    let files = [
        (
            registration_path.clone(),
            "Shopware.Component.register(
    'sw-legacy-live', () => import('./sw-legacy-live'),
);",
        ),
        (
            definition_path.clone(),
            "import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: { oldRequired: { type: String, required: true } },
    data() { return { oldMember: 'old' }; },
};",
        ),
        (
            template_path.clone(),
            "<slot name=\"old-slot\" />{{ oldMember }}",
        ),
    ];
    for (path, source) in &files {
        std::fs::write(path, source).unwrap();
    }
    for (path, source) in &files {
        fixture.index(path, source);
    }
    let provider = fixture.provider();
    let c = |path: &str, source: &str, marker: &str| {
        labels(&complete_after(&provider, path, source, marker))
    };
    let consumer = format!("{admin_root}/consumer.html.twig");
    assert!(has(
        &c(&consumer, "<sw-legacy-live :old />", ":old"),
        ":old-required"
    ));

    fixture.update_live(
        &definition_path,
        "import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
        liveOptional: Boolean,
    },
    data() { return { liveMember: { id: 'draft' } }; },
};",
    );
    let live_props = c(&consumer, "<sw-legacy-live :liv />", ":liv");
    assert!(has(&live_props, ":live-required"));
    assert!(has(&live_props, ":live-optional"));
    assert!(!has(&live_props, ":old-required"));
    let live_members = c(&template_path, "{{ liveM }}", "liveM");
    assert!(has(&live_members, "liveMember"));
    assert!(!has(&live_members, "oldMember"));

    fixture.update_live(
        &template_path,
        "<slot name=\"live-slot\" :record=\"liveMember\" />",
    );
    let live_slots = c(
        &consumer,
        "<sw-legacy-live><template #liv /></sw-legacy-live>",
        "#liv",
    );
    assert!(has(&live_slots, "live-slot"));
    assert!(!has(&live_slots, "old-slot"));

    fixture.index.remove_live_document(&definition_path);
    fixture.index.remove_live_document(&template_path);
    let fallback = fixture
        .index
        .get_effective_component("sw-legacy-live")
        .unwrap()
        .unwrap();
    assert!(fallback.component_slot("old-slot").is_some());
    assert!(has(
        &c(&consumer, "<sw-legacy-live :old />", ":old"),
        ":old-required"
    ));
    assert!(has(
        &c(
            &consumer,
            "<sw-legacy-live><template #old /></sw-legacy-live>",
            "#old"
        ),
        "old-slot"
    ));
}

#[test]
fn completion_uses_unsaved_mixin_and_directive_across_documents() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let registry_path = format!("{admin_root}/app/runtime.ts");
    fixture.index(
        &registry_path,
        "
Shopware.Mixin.register('shared-contract', {
    props: { oldLabel: String },
});
Shopware.Directive.register('old-focus', {});",
    );
    fixture.index(
        &format!("{admin_root}/component/sw-live-card/index.ts"),
        "Shopware.Component.register('sw-live-card', {
    mixins: [Shopware.Mixin.getByName('shared-contract')],
});",
    );
    let provider = fixture.provider();
    let c = |source: &str, marker: &str| {
        labels(&complete_after(
            &provider,
            &format!("{admin_root}/consumer.html.twig"),
            source,
            marker,
        ))
    };
    assert!(has(&c("<sw-live-card :old />", ":old"), ":old-label"));
    assert!(has(&c("<div v-old></div>", "v-old"), "v-old-focus"));

    fixture.update_live(
        &registry_path,
        "
Shopware.Mixin.register('shared-contract', {
    props: { liveLabel: { type: String, required: true } },
});
Shopware.Directive.register('live-focus', {});",
    );
    let live_props = c("<sw-live-card :liv />", ":liv");
    assert!(has(&live_props, ":live-label"));
    assert!(!has(&live_props, ":old-label"));
    let live_directives = c("<div v-liv></div>", "v-liv");
    assert!(has(&live_directives, "v-live-focus"));
    assert!(!has(&live_directives, "v-old-focus"));

    fixture.index.remove_live_document(&registry_path);
    assert!(has(&c("<sw-live-card :old />", ":old"), ":old-label"));
    assert!(has(&c("<div v-old></div>", "v-old"), "v-old-focus"));
}

#[test]
fn slot_completions_use_dynamic_component_intersection() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    for (name, dir, only) in [("sw-card-a", "a", "a-only"), ("sw-card-b", "b", "b-only")] {
        fixture.save(component! {
                    name: name.into(),
                    file_path: format!("{admin_root}/{dir}/index.ts"),
                    slots: vec![
                        VueComponentSlot {
                            name: "header".into(),
                            ..Default::default()
                        },
                        VueComponentSlot {
                            name: only.into(),
                            ..Default::default()
                        },
                    ],
        });
    }
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #hea></template></component>"#;
    let names = labels(&complete_after(
        &fixture.provider(),
        &format!("{admin_root}/consumer.html.twig"),
        source,
        "#hea",
    ));
    assert!(has(&names, "header"));
    assert!(!has(&names, "a-only"));
    assert!(!has(&names, "b-only"));
}

#[test]
fn dynamic_component_completion_uses_router_view_route_contracts() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let template_path = format!("{admin_root}/module/sw-shell/template.html.twig");
    fixture.index(
        &format!("{admin_root}/module/sw-shell/index.js"),
        "
import template from './template.html.twig';
Shopware.Component.register('sw-shell', { template });
Shopware.Module.register('sw-shell', {
    routes: {
        index: {
            component: 'sw-shell',
            children: {
                general: { component: 'sw-shell-general' },
                history: { component: 'sw-shell-history' },
            },
        },
    },
});",
    );
    for name in ["sw-shell-general", "sw-shell-history"] {
        fixture.save(component! {
                    name: name.into(),
                    file_path: format!("{admin_root}/component/{name}/index.js"),
                    props: vec![
                        VueComponentProp {
                            name: "account".into(),
                            type_: "Account".into(),
                            required: true,
                            ..Default::default()
                        },
                        prop(name.trim_start_matches("sw-shell-"), "Boolean"),
                    ],
        });
    }
    let source =
        r#"<router-view v-slot="{ Component: view }"><component :is="view" acc /></router-view>"#;
    let names = labels(&complete_after(
        &fixture.provider(),
        &template_path,
        source,
        " acc",
    ));
    assert!(has(&names, "account"));
    assert!(!has(&names, "general"));
    assert!(!has(&names, "history"));
}

#[test]
fn javascript_parser_builds_function_node() {
    let root = parse_js("function test() { return 42; }");
    assert!(
        root.descendants()
            .any(|node| node.kind() == jssyntax::JS_FUNCTION)
    );
}

#[test]
fn template_member_completions() {
    let fixture = Fixture::new(false);
    let template_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/sw-card.html.twig");
    let dir = template_path.rsplit_once('/').unwrap().0.to_owned();
    fixture.save(component! {
            name: "sw-card".into(),
            file_path: format!("{dir}/index.js"),
            template_path: template_path.clone(),
            props: vec![prop("title", "String")],
            data: vec!["isLoading".into()],
            computed: vec!["cardClasses".into()],
            methods: vec!["saveCard".into()],
            injected: vec!["repositoryFactory".into()],
    });
    let source = "{{ tit }}";
    let offset = (source.find("tit").unwrap() + 1) as u32;
    let items = by_label(complete(
        &fixture.provider(),
        &template_path,
        source,
        None,
        offset,
        true,
    ));
    for name in [
        "title",
        "isLoading",
        "cardClasses",
        "saveCard",
        "repositoryFactory",
    ] {
        assert!(items.contains_key(name), "{name}");
    }
    assert_eq!(items["saveCard"].insert_text, "saveCard($0)");
}

#[test]
fn composition_setup_member_completions() {
    let fixture = Fixture::new(true);
    let definition_path = fixture
        .path("src/Administration/Resources/app/administration/src/component/sw-setup/index.ts");
    let template_path = format!(
        "{}/sw-setup.html.twig",
        definition_path.rsplit_once('/').unwrap().0
    );
    fixture.index(
        &definition_path,
        "import template from './sw-setup.html.twig';
Shopware.Component.register('sw-setup', Shopware.Component.wrapComponentConfig({
    template,
    setup() {
        const count = ref<number>(0);
        const products = computed((): Product[] => []);
        const save = (id: string): Promise<void> => persist(id);
        const internalTitle: Ref<string> = ref('Fallback');
        const hidden = ref(false);
        return { count, products, save, title: internalTitle };
    },
\t\t}));",
    );
    let component = fixture
        .index
        .get_effective_component("sw-setup")
        .unwrap()
        .unwrap();
    assert_eq!(component.template_path, template_path);
    assert_eq!(component.template_members().len(), 4);

    let source = "{{ cou }}";
    let offset = (source.find("cou").unwrap() + 1) as u32;
    let items = by_label(complete(
        &fixture.provider(),
        &template_path,
        source,
        None,
        offset,
        true,
    ));
    assert_eq!(items["count"].detail, "data • number");
    assert_eq!(items["products"].detail, "computed • Product[]");
    assert_eq!(
        items["save"].detail,
        "method • (id: string) => Promise<void>"
    );
    assert_eq!(items["save"].insert_text, "save($0)");
    assert_eq!(items["title"].detail, "data • string");
    assert!(!items.contains_key("hidden"));
}

fn slot_member(name: &str, type_: &str) -> VueComponentSlotMember {
    VueComponentSlotMember {
        name: name.into(),
        type_: type_.into(),
        ..Default::default()
    }
}

#[test]
fn scoped_slot_contract_and_lexical_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let consumer_template = format!("{admin_root}/view/consumer.html.twig");
    fixture.save(component! {
            name: "sw-grid".into(),
            file_path: format!("{admin_root}/grid/index.js"),
            slots: vec![
                VueComponentSlot {
                    name: "result-item".into(),
                    payload_type: "{ item: Entity; index: number }".into(),
                    members: vec![slot_member("item", "Entity"), slot_member("index", "number")],
                    ..Default::default()
                },
                VueComponentSlot {
                    name_prefix: "column-".into(),
                    members: vec![
                        slot_member("item", "Entity"),
                        slot_member("isInlineEdit", "boolean"),
                    ],
                    ..Default::default()
                },
            ],
    });
    fixture.save(component! {
            name: "sw-consumer".into(),
            file_path: format!("{admin_root}/consumer/index.js"),
            template_path: consumer_template.clone(),
            props: vec![prop("title", "String")],
            members: vec![VueComponentMember {
                name: "dynamicCard".into(),
                kind: VueComponentMemberKind::Computed,
                return_expressions: vec!["'sw-card-a'".into(), "'sw-card-b'".into()],
                returns_complete: true,
                ..Default::default()
            }],
    });
    let provider = fixture.provider();
    let c = |source: &str, marker: &str| {
        by_label(complete_after(
            &provider,
            &consumer_template,
            source,
            marker,
        ))
    };

    let binding = c(
        r#"<sw-grid><template #result-item="{ it }">x</template></sw-grid>"#,
        "{ it",
    );
    assert!(binding.contains_key("item"));
    assert!(binding.contains_key("index"));
    assert!(binding["item"].detail.contains("Entity"));

    let body = c(
        r#"<sw-grid><template #result-item="{ item: row, index }">{{ ro }}</template></sw-grid>"#,
        "{{ ro",
    );
    for name in ["row", "index", "title"] {
        assert!(body.contains_key(name), "{name}");
    }
    assert!(body["row"].detail.contains("Entity"));

    let attribute = c(
        r#"<sw-grid><template #result-item="{ item: row }"><span :title="ro"></span></template></sw-grid>"#,
        r#":title="ro"#,
    );
    assert!(attribute.contains_key("row"));
    assert!(attribute.contains_key("title"));

    assert!(c(r#"<div :title="ti"></div>"#, r#":title="ti"#).contains_key("title"));

    let outside = c(
        r#"<sw-grid><template #result-item="{ item: row }">x</template></sw-grid>{{ ro }}"#,
        "</sw-grid>{{ ro",
    );
    assert!(!outside.contains_key("row"));
    assert!(outside.contains_key("title"));

    let default_slot = c(
        r#"<router-view v-slot="{ Component }">{{ Com }}</router-view>"#,
        "{{ Com",
    );
    assert!(
        default_slot["Component"]
            .detail
            .contains("router-view.default")
    );

    let dynamic_slot = c(
        r#"<sw-grid><template #[columnName]="{ item }">{{ it }}</template></sw-grid>"#,
        "{{ it",
    );
    assert!(dynamic_slot["item"].detail.contains("sw-grid.<dynamic>"));

    let family_binding = c(
        r#"<sw-grid><template #column-name="{ it }">x</template></sw-grid>"#,
        "{ it",
    );
    assert!(family_binding.contains_key("isInlineEdit"));
    assert!(family_binding["item"].detail.contains("Entity"));

    let family_body = c(
        r#"<sw-grid><template #column-name="{ item: row, isInlineEdit }">{{ ro }}</template></sw-grid>"#,
        "{{ ro",
    );
    assert!(family_body.contains_key("isInlineEdit"));
    assert!(family_body["row"].detail.contains("Entity"));

    let whole_object = c(
        r#"<sw-grid><template #result-item="props"><span :title="props.item"></span><span :title="props."></span></template></sw-grid>"#,
        r#":title="props."#,
    );
    assert!(whole_object.contains_key("index"));
    assert!(whole_object["item"].detail.contains("Entity"));
    assert!(!whole_object.contains_key("title"));

    let shadowed = c(
        r#"<sw-grid><template #result-item="props"><div v-for="props in rows"><span :title="props.name"></span><span :data-value="props."></span></div></template></sw-grid>"#,
        r#":data-value="props."#,
    );
    assert!(!shadowed.contains_key("item"));
    assert!(shadowed["name"].detail.contains("observed member"));

    let slot_shadows_loop = c(
        r#"<div v-for="item in items"><sw-grid><template #result-item="{ item }">{{ it }}</template></sw-grid></div>"#,
        "{{ it",
    );
    assert!(slot_shadows_loop["item"].detail.contains("slot local"));
    assert!(slot_shadows_loop["item"].detail.contains("Entity"));

    let loop_shadows_slot = c(
        r#"<sw-grid><template #result-item="{ item }"><div v-for="item in children">{{ it }}</div></template></sw-grid>"#,
        "{{ it",
    );
    assert!(loop_shadows_slot["item"].detail.contains("v-for local"));

    let nested = c(
        r#"<sw-grid><template #result-item="props"><sw-grid><template #result-item="{ item: innerItem }">{{ pr }}</template></sw-grid></template></sw-grid>"#,
        "{{ pr",
    );
    assert!(nested["props"].detail.contains("slot local"));
    assert!(nested["innerItem"].detail.contains("Entity"));

    fixture.save(component! {
            name: "sw-card-a".into(),
            file_path: format!("{admin_root}/a/index.ts"),
            slots: vec![VueComponentSlot {
                name: "row".into(),
                members_complete: true,
                members: vec![slot_member("item", "Product"), slot_member("onlyA", "string")],
                ..Default::default()
            }],
    });
    fixture.save(component! {
            name: "sw-card-b".into(),
            file_path: format!("{admin_root}/b/index.ts"),
            slots: vec![VueComponentSlot {
                name: "row".into(),
                members_complete: true,
                members: vec![slot_member("item", "Category")],
                ..Default::default()
            }],
    });
    let dynamic_binding = c(
        r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ it }">x</template></component>"#,
        "{ it",
    );
    assert!(
        dynamic_binding["item"]
            .detail
            .contains("Product | Category")
    );
    assert!(!dynamic_binding.contains_key("onlyA"));
    let dynamic_body = c(
        r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ ro }}</template></component>"#,
        "{{ ro",
    );
    assert!(dynamic_body["row"].detail.contains("Product | Category"));
    let dynamic_object = c(
        r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props. }}</template></component>"#,
        "props.",
    );
    assert!(dynamic_object.contains_key("item"));
    assert!(!dynamic_object.contains_key("onlyA"));
    let inferred = c(
        r#"<component :is="dynamicCard"><template #row="{ it }">x</template></component>"#,
        "{ it",
    );
    assert!(inferred.contains_key("item"));
    assert!(!inferred.contains_key("onlyA"));
}

#[test]
fn vue_for_and_event_lexical_completions() {
    let fixture = Fixture::new(true);
    let template_path = fixture.path("src/Resources/app/administration/src/view.html.twig");
    let definition_path = fixture.path("src/Resources/app/administration/src/index.ts");
    fixture.index(
        &definition_path,
        "interface Row { id: string; name: string; active: boolean; child: { label: string; count: number }; }",
    );
    fixture.save(component! {
            name: "sw-view".into(),
            template_path: template_path.clone(),
            file_path: definition_path.clone(),
            definition_path: definition_path.clone(),
            props: vec![
                VueComponentProp {
                    file_path: definition_path.clone(),
                    ..prop("items", "Array as PropType<Row[]>")
                },
                VueComponentProp {
                    file_path: definition_path.clone(),
                    ..prop("itemsById", "Record<string, Row>")
                },
                prop(
                    "typedItems",
                    "Array as PropType<Array<{ id: string; name: string; active: boolean }>>",
                ),
                prop("title", "String"),
            ],
    });
    fixture.save(component! {
            name: "mt-switch".into(),
            events: vec![VueComponentEvent {
                name: "update:modelValue".into(),
                type_: "(value: boolean) => any".into(),
                ..Default::default()
            }],
    });
    let provider = fixture.provider();
    let c = |source: &str, marker: &str| {
        by_label(complete_after(&provider, &template_path, source, marker))
    };

    let looped = c(
        r#"<div v-for="(item, index) in items">{{ it }}</div>"#,
        "{{ it",
    );
    assert!(looped.contains_key("index"));
    assert!(looped.contains_key("title"));
    for part in ["v-for local", "items", "Row"] {
        assert!(
            looped["item"].detail.contains(part),
            "{}",
            looped["item"].detail
        );
    }

    let members = c(
        r#"<div v-for="item in items"><span :title="item.name"></span><span :data-value="item."></span></div>"#,
        r#":data-value="item."#,
    );
    assert!(members["name"].detail.contains("typed member"));
    assert!(members["name"].detail.contains("Row"));
    assert!(!members.contains_key("title"));

    let typed = c(
        r#"<div v-for="item in typedItems"><span :title="item."></span></div>"#,
        r#":title="item."#,
    );
    for name in ["id", "name", "active"] {
        assert!(typed.contains_key(name), "{name}");
    }
    assert!(typed["id"].detail.contains("string"));
    assert!(typed["active"].detail.contains("boolean"));
    let surface = c(r#"<span :title="typedItems."></span>"#, "typedItems.");
    for name in ["length", "map", "filter"] {
        assert!(surface.contains_key(name), "{name}");
    }
    assert!(!surface.contains_key("id"));

    let named = c(
        r#"<div v-for="item in items"><span :title="item."></span></div>"#,
        r#":title="item."#,
    );
    for name in ["id", "name", "active"] {
        assert!(named.contains_key(name), "{name}");
    }
    assert!(named["id"].detail.contains("string"));

    let nested = c(
        r#"<div v-for="item in items"><span :title="item.child."></span></div>"#,
        r#":title="item.child."#,
    );
    assert!(nested.contains_key("count"));
    assert!(nested["label"].detail.contains("string"));
    assert!(!nested.contains_key("id"));

    let record = c(
        r#"<div v-for="(item, itemId, index) in itemsById"><span :title="item."></span><span :data-key="itemId."></span><span :data-index="index."></span></div>"#,
        r#":title="item."#,
    );
    for name in ["id", "name", "active", "child"] {
        assert!(record.contains_key(name), "{name}");
    }
    let record_key = c(
        r#"<div v-for="(item, itemId, index) in itemsById"><span :data-key="itemId."></span></div>"#,
        "itemId.",
    );
    assert!(record_key.contains_key("length"));
    assert!(record_key.contains_key("toLowerCase"));
    let record_index = c(
        r#"<div v-for="(item, itemId, index) in itemsById"><span :data-index="index."></span></div>"#,
        "index.",
    );
    assert!(record_index.contains_key("toFixed"));
    assert!(!record_index.contains_key("name"));

    let handler = c(
        r#"<div v-for="item in items"><mt-switch @update:model-value="$ev" /></div>"#,
        r#"="$ev"#,
    );
    assert!(handler.contains_key("item"));
    assert!(handler["$event"].detail.contains("boolean"));
    assert!(
        handler["$event"]
            .detail
            .contains("mt-switch.update:model-value")
    );

    let iterable = c(r#"<div v-for="item in tit"></div>"#, "in tit");
    assert!(!iterable.contains_key("item"));
    assert!(iterable.contains_key("title"));
}

#[test]
fn component_entity_member_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let template_path = format!("{admin_root}/component/view.html.twig");
    let definition_path = format!("{admin_root}/component/index.ts");
    fixture.index(
        &format!("{admin_root}/entity-schema-definition.d.ts"),
        "declare namespace EntitySchema {
interface cms_page { id: string; name: string; sections: EntityCollection<'cms_section'>; }
interface cms_section { id: string; name: string; position: number; }
}",
    );
    let member = |name: &str, kind, type_: &str| VueComponentMember {
        name: name.into(),
        kind,
        type_: type_.into(),
        file_path: definition_path.clone(),
        ..Default::default()
    };
    fixture.save(component! {
            name: "sw-cms-page".into(),
            template_path: template_path.clone(),
            file_path: definition_path.clone(),
            definition_path: definition_path.clone(),
            members: vec![
                member("page", VueComponentMemberKind::Prop, "Entity<'cms_page'>"),
                member(
                    "sectionsById",
                    VueComponentMemberKind::Computed,
                    "Record<string, Entity<'cms_section'>>",
                ),
                member(
                    "sectionGroups",
                    VueComponentMemberKind::Computed,
                    "Record<string, EntityCollection<'cms_section'>>",
                ),
            ],
    });
    let provider = fixture.provider();
    let c = |source: &str, marker: &str| {
        let offset = after(source, marker);
        by_label(complete(
            &provider,
            &template_path,
            source,
            Some(offset),
            offset - 1,
            false,
        ))
    };
    let assert_all = |items: &ByLabel, names: &[&str]| {
        for name in names {
            assert!(items.contains_key(*name), "{name}: {:?}", items.keys());
        }
    };

    let page = c(r#"<div :title="page."></div>"#, "page.");
    assert_all(&page, &["id", "name", "sections", "getEntityName"]);
    assert!(
        page["sections"]
            .detail
            .contains("EntityCollection<'cms_section'>")
    );

    let collection = c(r#"<div :title="page.sections."></div>"#, "page.sections.");
    assert_all(&collection, &["total", "first"]);
    assert!(!collection.contains_key("position"));
    let called = c(
        r#"<div :title="page.sections.first()."></div>"#,
        "page.sections.first().",
    );
    assert_all(&called, &["id", "name", "position"]);
    assert!(!called.contains_key("total"));
    let indexed = c(
        r#"<div :title="page.sections[0]."></div>"#,
        "page.sections[0].",
    );
    assert_all(&indexed, &["id", "name", "position"]);
    assert!(!indexed.contains_key("total"));
    assert_all(
        &c(
            r#"<div v-for="(section, index) in page.sections" :title="page.sections[index]."></div>"#,
            "page.sections[index].",
        ),
        &["id", "name", "position"],
    );
    assert_all(
        &c(
            r#"<div v-for="(section, index) in page.sections" :title="page.sections[index - 1]."></div>"#,
            "page.sections[index - 1].",
        ),
        &["id", "name", "position"],
    );
    assert_all(
        &c(
            r#"<div :title="sectionsById[currentGroup]."></div>"#,
            "sectionsById[currentGroup].",
        ),
        &["id", "name", "position"],
    );
    assert_all(
        &c(
            r#"<div v-for="section in page.sections" :title="section."></div>"#,
            "section.",
        ),
        &["id", "name", "position"],
    );
    let mapped = c(
        r#"<div v-for="name in page.sections?.map((section) => section.name) ?? []" :title="name."></div>"#,
        "name.",
    );
    assert_all(&mapped, &["length", "toLowerCase", "trim"]);
    assert!(!mapped.contains_key("position"));
    let projected = c(
        r#"<div v-for="card in page.sections.map((section) => ({ id: section.id, label: section.name }))" :title="card."></div>"#,
        "card.",
    );
    assert_all(&projected, &["id", "label"]);
    assert!(!projected.contains_key("position"));
    assert_all(
        &c(
            r#"<div v-for="section in Object.values(sectionsById)" :title="section."></div>"#,
            "section.",
        ),
        &["id", "name", "position"],
    );
    assert_all(
        &c(
            r#"<div v-for="section in sectionGroups[currentGroup]" :title="section."></div>"#,
            "section.",
        ),
        &["id", "name", "position"],
    );
    assert_all(
        &c(
            r#"<div v-for="label in ['primary', 'fallback']" :title="label."></div>"#,
            "label.",
        ),
        &["length", "toUpperCase"],
    );
}

#[test]
fn prop_completion_uses_template_attribute_case() {
    let fixture = Fixture::new(false);
    fixture.save(component! {
            name: "mt-card".into(),
            file_path: "/project/Resources/app/administration/src/mt-card/index.js".into(),
            props: vec![
                VueComponentProp {
                    documentation: "Stable extension position used by the card.".into(),
                    ..prop("positionIdentifier", "String")
                },
                prop("modelValue", "String"),
                prop("checked", "Boolean"),
            ],
            events: vec![
                VueComponentEvent {
                    name: "update:modelValue".into(),
                    type_: "(value: string) => void".into(),
                    documentation: "Updates the public model value.".into(),
                    ..Default::default()
                },
                VueComponentEvent {
                    name: "update:checked".into(),
                    type_: "(value: boolean) => void".into(),
                    ..Default::default()
                },
            ],
    });
    let items = by_label(
        fixture
            .provider()
            .get_component_prop_completions("mt-card", ""),
    );
    assert!(items.contains_key("position-identifier"));
    assert!(items.contains_key(":position-identifier"));
    assert!(!items.contains_key("positionIdentifier"));
    assert!(
        doc(&items["position-identifier"]).contains("Stable extension position used by the card.")
    );
    assert!(
        doc(&items[":position-identifier"]).contains("Stable extension position used by the card.")
    );
    let update = &items["@update:model-value"];
    assert!(update.detail.contains("value: string"));
    assert!(doc(update).contains("Updates the public model value."));
    assert!(doc(update).contains("**Payload:** `(value: string) => void`"));
    assert_eq!(items["v-model"].insert_text, r#"v-model="$0""#);
    assert!(items["v-model"].detail.contains("modelValue"));
    assert!(items["v-model:checked"].detail.contains("boolean"));
    assert!(!items.contains_key("v-model:model-value"));
}

#[test]
fn completions_expose_component_and_prop_deprecations() {
    let fixture = Fixture::new(false);
    let template_path = "/project/Resources/app/administration/src/consumer.html.twig";
    fixture.save(component! {
            name: "sw-legacy".into(),
            file_path: "/project/sw-legacy/index.ts".into(),
            definition_path: "/project/sw-legacy/index.ts".into(),
            template_path: template_path.into(),
            deprecated: "tag:v6.8.0 - Use mt-modern instead.".into(),
            props: vec![
                VueComponentProp {
                    deprecated: "Use modernValue instead.".into(),
                    ..prop("oldValue", "String")
                },
                prop("active", "Boolean"),
                VueComponentProp {
                    deprecated: "Use value instead.".into(),
                    ..prop("modelValue", "String")
                },
            ],
            events: vec![VueComponentEvent {
                name: "update:modelValue".into(),
                ..Default::default()
            }],
            methods: vec!["legacySave".into(), "save".into()],
            members: vec![
                VueComponentMember {
                    name: "legacySave".into(),
                    kind: VueComponentMemberKind::Method,
                    deprecated: "Use save instead.".into(),
                    ..Default::default()
                },
                VueComponentMember {
                    name: "save".into(),
                    kind: VueComponentMemberKind::Method,
                    ..Default::default()
                },
            ],
    });
    let provider = fixture.provider();
    let tags = by_label(provider.get_component_tag_completions(template_path));
    assert!(tags["sw-legacy"].deprecated);
    assert!(doc(&tags["sw-legacy"]).contains("Use mt-modern"));
    assert!(by_label(provider.get_component_completions())["sw-legacy"].deprecated);

    let props = by_label(provider.get_component_prop_completions("sw-legacy", template_path));
    for label in ["old-value", ":old-value", "v-model"] {
        assert!(props[label].deprecated, "{label}");
        assert!(doc(&props[label]).contains("Deprecated"), "{label}");
    }
    assert!(!props["active"].deprecated);
    assert!(!doc(&props["active"]).contains("Deprecated"));

    let this_members = provider
        .get_this_member_completions(&file_uri("/project/sw-legacy/index.ts"))
        .expect("component file");
    for members in [
        by_label(provider.get_template_member_completions(&file_uri(template_path))),
        by_label(this_members),
    ] {
        assert!(members["legacySave"].deprecated);
        assert!(doc(&members["legacySave"]).contains("Use save"));
        assert!(!members["save"].deprecated);
    }
}

#[test]
fn completion_offers_parent_twig_blocks_with_deprecation() {
    let fixture = Fixture::new(false);
    let admin_root = "/project/Resources/app/administration/src";
    let parent_template = format!("{admin_root}/sw-card.html.twig");
    let child_template = format!("{admin_root}/acme-card.html.twig");
    fixture.save(component! {
            name: "sw-card".into(),
            kind: ComponentRegistrationKind::Register,
            file_path: format!("{admin_root}/sw-card.js"),
            template_path: parent_template.clone(),
            blocks: vec![
                TwigBlock {
                    name: "sw_card_legacy".into(),
                    file_path: parent_template.clone(),
                    line: 3,
                    deprecated: "Use sw_card_content instead.".into(),
                    ..Default::default()
                },
                TwigBlock {
                    name: "sw_card_content".into(),
                    file_path: parent_template.clone(),
                    line: 7,
                    ..Default::default()
                },
            ],
    });
    fixture.save(component! {
            name: "acme-card".into(),
            kind: ComponentRegistrationKind::Extend,
            target_component: "sw-card".into(),
            extends_component: "sw-card".into(),
            file_path: format!("{admin_root}/acme-card.js"),
            template_path: child_template.clone(),
    });
    let items = by_label(complete_after(
        &fixture.provider(),
        &child_template,
        "{% block sw_card_ %}{% endblock %}",
        "sw_card_",
    ));
    assert!(items["sw_card_legacy"].deprecated);
    assert!(doc(&items["sw_card_legacy"]).contains("Use sw_card_content"));
    assert!(!items["sw_card_content"].deprecated);
}

#[test]
fn completion_includes_template_scoped_local_components() {
    let fixture = Fixture::new(false);
    let template_path = "/project/Resources/app/administration/src/sw-wrapper/template.html.twig";
    fixture.save(component! {
            name: "sw-wrapper".into(),
            file_path: "/project/Resources/app/administration/src/sw-wrapper/index.js".into(),
            template_path: template_path.into(),
            local_components: vec![VueLocalComponent {
                name: "mt-card-original".into(),
                symbol: "MtCard".into(),
                file_path: "/project/Resources/app/administration/src/sw-wrapper/index.js".into(),
                line: 7,
                ..Default::default()
            }],
    });
    fixture.save(component! {
            name: "mt-card".into(),
            file_path: "/project/Resources/app/administration/src/mt-card/index.js".into(),
            props: vec![prop("title", "String")],
    });
    let provider = fixture.provider();
    assert!(has(
        &labels(&provider.get_component_tag_completions(template_path)),
        "mt-card-original"
    ));
    assert!(!has(
        &labels(&provider.get_component_tag_completions(
            "/project/Resources/app/administration/src/other/template.html.twig"
        )),
        "mt-card-original"
    ));
    assert!(has(
        &labels(&provider.get_component_prop_completions("mt-card-original", template_path)),
        "title"
    ));
}

fn text_edit_new_text(item: &protocol::CompletionItem) -> String {
    item.text_edit.as_ref().expect("text edit").new_text.clone()
}

#[test]
fn dynamic_component_selector_and_contract_completions() {
    let fixture = Fixture::new(false);
    let template_path = "/project/Resources/app/administration/src/consumer.html.twig";
    fixture.save(component! {
            name: "sw-card".into(),
            file_path: "/project/sw-card/index.ts".into(),
            props: vec![
                prop("title", "String"),
                prop("cardOnly", "String"),
                prop("variant", r#""primary" | "secondary""#),
            ],
    });
    fixture.save(component! {
            name: "sw-panel".into(),
            file_path: "/project/sw-panel/index.ts".into(),
            props: vec![
                prop("title", "String"),
                prop("panelOnly", "String"),
                prop("variant", r#""primary" | "tertiary""#),
            ],
    });
    for (name, path, only) in [
        ("sw-cms-el-config-hero", "/project/cms/hero.ts", "heroOnly"),
        ("sw-cms-el-config-text", "/project/cms/text.ts", "textOnly"),
    ] {
        fixture.save(component! {
                    name: name.into(),
                    file_path: path.into(),
                    props: vec![prop("element", "Object"), prop(only, "Boolean")],
        });
    }
    fixture.index(
        "/project/Resources/app/administration/src/cms/registry.ts",
        "
cmsService.registerCmsElement({
    name: 'hero', configComponent: 'sw-cms-el-config-hero',
});
cmsService.registerCmsElement({
    name: 'text', configComponent: 'sw-cms-el-config-text',
});",
    );
    fixture.save(component! {
            name: "sw-host".into(),
            file_path: "/project/sw-host/index.ts".into(),
            template_path: template_path.into(),
            members: vec![
                VueComponentMember {
                    name: "dynamicCard".into(),
                    kind: VueComponentMemberKind::Computed,
                    return_expressions: vec!["'sw-card'".into(), "'sw-panel'".into()],
                    returns_complete: true,
                    ..Default::default()
                },
                VueComponentMember {
                    name: "cmsElements".into(),
                    kind: VueComponentMemberKind::Computed,
                    cms_registry_kind: AdminCMSRegistrationKind::Element,
                    ..Default::default()
                },
            ],
    });
    let provider = fixture.provider();
    let c = |source: &str, marker: &str| {
        by_label(complete_after(&provider, template_path, source, marker))
    };

    let selector = c(r#"<component :is="'sw-ca'" />"#, "sw-ca");
    assert_eq!(selector["sw-card"].insert_text, "sw-card");
    assert!(selector["sw-card"].detail.contains("dynamic component"));

    let contract = c(r#"<component :is="'sw-card'"  />"#, r#"" "#);
    assert!(contract.contains_key("title"));
    assert!(contract.contains_key(":title"));

    let union = c(
        r#"<component :is="active ? 'sw-card' : 'sw-panel'"  />"#,
        r#"" "#,
    );
    assert!(union.contains_key("title"));
    assert!(!union.contains_key("card-only"));
    assert!(!union.contains_key("panel-only"));

    let inferred = c(r#"<component :is="dynamicCard"  />"#, r#"" "#);
    assert!(inferred.contains_key("title"));
    assert!(!inferred.contains_key("card-only"));
    assert!(!inferred.contains_key("panel-only"));

    let owner = fixture
        .index
        .get_component_by_template_path(template_path)
        .unwrap()
        .unwrap();
    let cms_member = owner.template_member("cmsElements").unwrap();
    assert_eq!(
        cms_member.cms_registry_kind,
        AdminCMSRegistrationKind::Element
    );
    assert_eq!(
        fixture
            .index
            .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)
            .unwrap()
            .len(),
        2
    );
    let (resolved, contracts, complete) = fixture
        .index
        .resolve_dynamic_component_contracts(
            template_path,
            &VueDynamicComponentSelector {
                expression: "cmsElements[element.type].configComponent".into(),
                ..Default::default()
            },
            None,
        )
        .unwrap();
    assert!(complete, "{:?}", resolved.names());
    assert_eq!(contracts.len(), 2);
    let cms = c(
        r#"<component :is="cmsElements[element.type].configComponent"  />"#,
        r#"" "#,
    );
    assert!(cms.contains_key("element"));
    assert!(!cms.contains_key("hero-only"));
    assert!(!cms.contains_key("text-only"));

    let object = c(r#"<sw-card v-bind="{ cardOnly: existing, ti }" />"#, ", ti");
    assert!(!object.contains_key("cardOnly"));
    assert_eq!(object["title"].insert_text, "title: $0");

    let dynamic_object = c(r#"<component :is="dynamicCard" v-bind="{ ti }" />"#, "ti");
    assert!(dynamic_object.contains_key("title"));
    assert!(!dynamic_object.contains_key("cardOnly"));
    assert!(!dynamic_object.contains_key("panelOnly"));

    let static_values = c(r#"<sw-card variant="sec" />"#, r#"variant="sec"#);
    assert!(static_values.contains_key("primary"));
    assert!(!static_values.contains_key("tertiary"));
    assert_eq!(text_edit_new_text(&static_values["secondary"]), "secondary");
    let empty_values = c(r#"<sw-card variant="" />"#, r#"variant=""#);
    assert!(empty_values.contains_key("primary"));
    assert!(empty_values.contains_key("secondary"));
    let bound_values = c(r#"<sw-card :variant="'sec'" />"#, r#":variant="'sec"#);
    assert!(bound_values.contains_key("primary"));
    assert_eq!(text_edit_new_text(&bound_values["secondary"]), "secondary");
    let object_bound = c(
        r#"<sw-card v-bind="{ variant: 'sec' }" />"#,
        "variant: 'sec",
    );
    assert!(object_bound.contains_key("primary"));
    assert!(!object_bound.contains_key("tertiary"));
    assert_eq!(text_edit_new_text(&object_bound["secondary"]), "secondary");

    let dynamic_values = c(
        r#"<component :is="dynamicCard" variant="p" />"#,
        r#"variant="p"#,
    );
    assert!(dynamic_values.contains_key("primary"));
    assert!(!dynamic_values.contains_key("secondary"));
    assert!(!dynamic_values.contains_key("tertiary"));
    let dynamic_object_bound = c(
        r#"<component :is="dynamicCard" v-bind="{ variant: 'p' }" />"#,
        "variant: 'p",
    );
    assert!(dynamic_object_bound.contains_key("primary"));
    assert!(!dynamic_object_bound.contains_key("secondary"));
    assert!(!dynamic_object_bound.contains_key("tertiary"));
}

#[test]
fn this_member_completions() {
    let fixture = Fixture::new(false);
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/index.js");
    fixture.save(component! {
            name: "sw-card".into(),
            file_path: definition_path.clone(),
            definition_path: definition_path.clone(),
            props: vec![prop("title", "String")],
            data: vec!["isLoading".into()],
            computed: vec!["cardClasses".into()],
            methods: vec!["saveCard".into()],
            injected: vec!["repositoryFactory".into()],
    });
    let source = "export default { methods: { run() { return this.; } } };";
    let offset = after(source, "this.");
    let items = by_label(complete(
        &fixture.provider(),
        &definition_path,
        source,
        None,
        offset - 1,
        true,
    ));
    for name in [
        "title",
        "isLoading",
        "cardClasses",
        "saveCard",
        "repositoryFactory",
        "$emit",
        "$router",
        "$t",
    ] {
        assert!(items.contains_key(name), "{name}");
    }
    assert_eq!(items["saveCard"].insert_text, "saveCard($0)");
}

#[test]
fn runtime_registry_completions() {
    let fixture = Fixture::new(false);
    let registration_path =
        fixture.path("src/Administration/Resources/app/administration/src/main.ts");
    fixture.index(
        &registration_path,
        "
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('profile', {
    state() { return { currentUser: null }; },
    getters: { displayName() {} },
    actions: { load() {} },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});
",
    );
    let provider = fixture.provider();
    let document_path =
        fixture.path("src/Administration/Resources/app/administration/src/consumer.ts");
    let cases: [(&str, &str, &str, &[&str]); 7] = [
        ("service lookup", "Shopware.Service('a')", "a", &["acl"]),
        (
            "component injection",
            "export default { inject: ['a'] }",
            "a",
            &["acl"],
        ),
        ("store lookup", "Shopware.Store.get('p')", "p", &["profile"]),
        (
            "store unregister",
            "Shopware.Store.unregister('p')",
            "p",
            &["profile"],
        ),
        (
            "store member",
            "Shopware.Store.get('profile').",
            ".",
            &["currentUser", "displayName", "load"],
        ),
        (
            "privilege role",
            "this.acl.can('product.v')",
            "product.v",
            &["product.viewer", "product:read"],
        ),
        (
            "privilege service",
            "Shopware.Service('privileges').getPrivileges('product')",
            "product')",
            &["product.viewer", "product:read"],
        ),
    ];
    for (name, source, needle, expected) in cases {
        let offset = source.rfind(needle).unwrap() as u32;
        let items = by_label(complete(
            &provider,
            &document_path,
            source,
            None,
            offset,
            true,
        ));
        for label in expected {
            assert!(items.contains_key(*label), "{name}: {label}");
        }
        if name == "store member" {
            assert_eq!(items["load"].insert_text, "load($0)");
            assert!(items["currentUser"].detail.contains("null"));
            assert!(items["load"].detail.contains("() => unknown"));
        }
    }
}

#[test]
fn application_container_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
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
    fixture.index(
        &format!("{admin_root}/services.ts"),
        "Shopware.Application.addServiceProvider('runtimeService', factory);",
    );
    let provider = fixture.provider();
    let document_path = format!("{admin_root}/consumer.ts");
    let c = |source: &str, needle: &str| {
        let offset = source.rfind(needle).unwrap() as u32;
        by_label(complete(
            &provider,
            &document_path,
            source,
            None,
            offset,
            true,
        ))
    };
    let names = c("Application.getContainer('fa')", "fa");
    for name in ["factory", "service", "init", "init-pre", "init-post"] {
        assert!(names.contains_key(name), "{name}");
    }
    let factories = c("Application.getContainer('factory').", ".");
    for name in ["$list", "locale", "module"] {
        assert!(factories.contains_key(name), "{name}");
    }
    assert_eq!(factories["$list"].insert_text, "$list($0)");
    let services = c(
        "function run() {
    const container = Shopware.Application.getContainer('service');
    return container.;
}",
        ".;",
    );
    for name in ["$list", "acl", "runtimeService"] {
        assert!(services.contains_key(name), "{name}");
    }
}

#[test]
fn shopware_context_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/app/composables/use-context.ts"),
        "export interface ContextState {
    app: { config: { version: null | string; shopId: null | string } };
    api: { languageId: null | string; versionId: null | string };
}",
    );
    let provider = fixture.provider();
    let document_path = format!("{admin_root}/module/example/index.ts");
    let c = |source: &str| {
        let offset = source.rfind('.').unwrap() as u32;
        by_label(complete(
            &provider,
            &document_path,
            source,
            None,
            offset,
            true,
        ))
    };
    let root_members = c("Shopware.Context.");
    assert!(root_members.contains_key("app"));
    assert!(root_members.contains_key("api"));
    let api = c("Shopware.Context.api.");
    assert!(api.contains_key("versionId"));
    assert!(api["languageId"].detail.contains("null | string"));
    let config = c("Shopware.Context.app.config.");
    assert!(config.contains_key("version"));
    assert!(config.contains_key("shopId"));
}

#[test]
fn shopware_utils_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/core/service/util.service.ts"),
        "export const format = { date, fileSize };
export default { createId, format };
function createId(): string { return 'id'; }
function date(value: string): string { return value; }
function fileSize(bytes: number): string { return String(bytes); }",
    );
    let provider = fixture.provider();
    let document_path = format!("{admin_root}/module/example/index.ts");
    let c = |source: &str| {
        let offset = source.rfind('.').unwrap() as u32;
        by_label(complete(
            &provider,
            &document_path,
            source,
            None,
            offset,
            true,
        ))
    };
    let root_members = c("Shopware.Utils.");
    assert!(root_members.contains_key("format"));
    assert_eq!(root_members["createId"].insert_text, "createId($0)");
    let format = c("Shopware.Utils.format.");
    assert!(format.contains_key("fileSize"));
    assert!(format["date"].detail.contains("(value: string) => string"));
    for source in [
        "const format = Shopware.Utils.format; format.",
        "const { format } = Shopware.Utils; format.",
    ] {
        let members = c(source);
        assert!(members.contains_key("date"), "{source}");
        assert!(members.contains_key("fileSize"), "{source}");
    }
}

#[test]
fn shopware_event_bus_event_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/core/service/util.service.ts"),
        "
import EventBus from './utils/eventBus.utils';
export default { EventBus };
",
    );
    fixture.index(
        &format!("{admin_root}/core/service/utils/eventBus.utils.ts"),
        "
interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
    telemetry: undefined;
}
const emitter = mitt<Events>();
export default emitter;
",
    );
    let provider = fixture.provider();
    let document_path = format!("{admin_root}/module/example/index.ts");
    for (name, source, marker) in [
        (
            "direct",
            "Shopware.Utils.EventBus.on('sav', handler);",
            "sav",
        ),
        (
            "destructured",
            "const { EventBus } = Shopware.Utils;\nEventBus.off('tel', handler);",
            "tel",
        ),
        (
            "alias",
            "const bus = Shopware.Utils.EventBus;\nbus.emit('sav', payload);",
            "sav",
        ),
    ] {
        let offset = (source.rfind(marker).unwrap() + marker.len()) as u32;
        let items = by_label(complete(
            &provider,
            &document_path,
            source,
            None,
            offset,
            true,
        ));
        assert!(items.contains_key("telemetry"), "{name}");
        assert!(
            items["save-event"].detail.contains("{ id: string }"),
            "{name}"
        );
    }
}

#[test]
fn twig_privilege_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/privileges.ts"),
        "Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});",
    );
    let source = r#"<mt-button :disabled="acl.can('product.v')" />"#;
    let offset = after(source, "product.v");
    let items = by_label(complete(
        &fixture.provider(),
        &format!("{admin_root}/view.html.twig"),
        source,
        Some(offset),
        offset - 1,
        false,
    ));
    assert!(
        items[ADMIN_PRIVILEGE_ADMINISTRATOR]
            .detail
            .contains("Built-in")
    );
    assert!(items.contains_key("product.viewer"));
    assert!(items.contains_key("product:read"));
}

#[test]
fn twig_module_route_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/module/sw-product/index.ts"),
        "Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
        create: { path: 'create', component: 'sw-product-create' },
    },
});",
    );
    let source = r#"<router-link :to="{ name: 'sw.product.d' }" />"#;
    let offset = after(source, "sw.product.d");
    let items = by_label(complete(
        &fixture.provider(),
        &format!("{admin_root}/view.html.twig"),
        source,
        Some(offset),
        offset - 1,
        false,
    ));
    assert!(items.contains_key("sw.product.detail"));
    assert!(items.contains_key("sw.product.create"));
}

#[test]
fn imported_setup_store_member_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let store_path = format!("{admin_root}/app/store/session.store.ts");
    let factory_path = format!("{admin_root}/app/composables/use-session.ts");
    std::fs::create_dir_all(factory_path.rsplit_once('/').unwrap().0).unwrap();
    std::fs::write(&factory_path, "").unwrap();
    fixture.index(
        &store_path,
        "
import useSession from '../composables/use-session';
export default Shopware.Store.register('session', useSession);",
    );
    fixture.index(
        &factory_path,
        "
const currentUser = ref(null);
const userPending = computed(() => !currentUser.value);
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, userPending, setCurrentUser };
}",
    );
    let source = "Shopware.Store.get('session').";
    let offset = source.rfind('.').unwrap() as u32;
    let items = by_label(complete(
        &fixture.provider(),
        &format!("{admin_root}/consumer.ts"),
        source,
        None,
        offset,
        true,
    ));
    assert!(items.contains_key("currentUser"));
    assert!(items.contains_key("userPending"));
    assert_eq!(items["setCurrentUser"].insert_text, "setCurrentUser($0)");
}

#[test]
fn component_and_module_registry_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/main.ts"),
        "
Shopware.Component.register('sw-card', { props: { title: String } });
Shopware.Module.register('sw-product', {
    title: 'sw-product.general.mainMenuItemGeneral',
    routes: { index: { path: 'index', component: 'sw-card' } },
});",
    );
    let provider = fixture.provider();
    for (name, source, needle, expected) in [
        (
            "component get",
            "Shopware.Component.getComponentRegistry().get('sw-ca')",
            "sw-ca",
            "sw-card",
        ),
        (
            "component has",
            "Component.getComponentRegistry().has('sw-ca')",
            "sw-ca",
            "sw-card",
        ),
        (
            "module get",
            "Shopware.Module.getModuleRegistry().get('sw-pro')",
            "sw-pro",
            "sw-product",
        ),
        (
            "module has",
            "Module.getModuleRegistry().has('sw-pro')",
            "sw-pro",
            "sw-product",
        ),
    ] {
        let offset = (source.find(needle).unwrap() + 1) as u32;
        let items = by_label(complete(
            &provider,
            &format!("{admin_root}/consumer.ts"),
            source,
            None,
            offset,
            true,
        ));
        assert!(items.contains_key(expected), "{name}");
        if expected == "sw-product" {
            assert!(items[expected].detail.contains("Administration module"));
        }
    }
}

#[test]
fn directive_completions_in_javascript_and_twig() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/app/directive/tooltip.directive.ts"),
        "Shopware.Directive.register('tooltip', {});",
    );
    let provider = fixture.provider();
    for (name, source, needle, expected, extension) in [
        (
            "registry lookup",
            "Shopware.Directive.getByName('too')",
            "too",
            "tooltip",
            ".ts",
        ),
        (
            "native markup",
            r#"<div v-too="message"></div>"#,
            "v-too",
            "v-tooltip",
            ".html.twig",
        ),
    ] {
        let offset = (source.find(needle).unwrap() + 2) as u32;
        let items = by_label(complete(
            &provider,
            &format!("{admin_root}/consumer{extension}"),
            source,
            Some(offset),
            offset,
            true,
        ));
        assert!(items[expected].detail.contains("Vue directive"), "{name}");
    }
    assert!(provider.trigger_characters().contains(&"-".to_owned()));
}

#[test]
fn cms_registry_completions() {
    let fixture = Fixture::new(true);
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index(
        &format!("{admin_root}/cms.ts"),
        "
Shopware.Component.register('sw-cms-el-hero', {});
Shopware.Component.register('sw-cms-el-config-hero', {});
Shopware.Component.register('sw-cms-el-preview-hero', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', label: 'Hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });",
    );
    let provider = fixture.provider();
    for (name, source, needle, expected, detail) in [
        (
            "element lookup",
            "Shopware.Service('cmsService').getCmsElementConfigByName('he')",
            "he",
            "hero",
            "Shopware CMS",
        ),
        (
            "block lookup",
            "Shopware.Service('cmsService').getCmsBlockConfigByName('hero-g')",
            "hero-g",
            "hero-grid",
            "Shopware CMS",
        ),
        (
            "block slot element",
            "cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'he' } } })",
            "he",
            "hero",
            "Shopware CMS",
        ),
        (
            "element component link",
            "cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-he' })",
            "sw-cms-el-he",
            "sw-cms-el-hero",
            "",
        ),
        (
            "element config component link",
            "cmsService.registerCmsElement({ name: 'other', configComponent: 'sw-cms-el-config-he' })",
            "sw-cms-el-config-he",
            "sw-cms-el-config-hero",
            "",
        ),
    ] {
        let offset = (source.rfind(needle).unwrap() + needle.len()) as u32;
        let items = by_label(complete(
            &provider,
            &format!("{admin_root}/consumer.ts"),
            source,
            Some(offset),
            offset,
            true,
        ));
        assert!(items.contains_key(expected), "{name}: {:?}", items.keys());
        if !detail.is_empty() {
            assert!(items[expected].detail.contains(detail), "{name}");
        }
    }
}

#[test]
fn directive_completions_are_template_scoped() {
    let fixture = Fixture::new(false);
    let owner_template = "/project/Resources/app/administration/src/owner/template.html.twig";
    fixture.save(component! {
            name: "sw-owner".into(),
            template_path: owner_template.into(),
            file_path: "/project/Resources/app/administration/src/owner/index.js".into(),
            local_directives: vec![VueLocalDirective {
                name: "hide".into(),
                file_path: "/project/Resources/app/administration/src/owner/index.js".into(),
                line: 4,
                ..Default::default()
            }],
    });
    let provider = fixture.provider();
    assert!(has(
        &labels(&provider.get_directive_completions(true, owner_template)),
        "v-hide"
    ));
    assert!(!has(
        &labels(&provider.get_directive_completions(
            true,
            "/project/Resources/app/administration/src/other/template.html.twig"
        )),
        "v-hide"
    ));
}

#[test]
fn template_completions_include_runtime_scope() {
    let fixture = Fixture::new(true);
    let template_path =
        fixture.path("src/Administration/Resources/app/administration/src/sw-card.html.twig");
    fixture.save(component! {
            name: "sw-card".into(),
            template_path: template_path.clone(),
            file_path: fixture.path("src/Administration/Resources/app/administration/src/index.ts"),
            members: vec![VueComponentMember {
                name: "title".into(),
                kind: VueComponentMemberKind::Prop,
                type_: "string".into(),
                ..Default::default()
            }],
            blocks: vec![TwigBlock {
                name: "sw_card_row".into(),
                scope_members: vec![TwigBlockScopeMember {
                    name: "item".into(),
                    type_: "Product".into(),
                    ..Default::default()
                }],
                ..Default::default()
            }],
    });
    let provider = fixture.provider();
    // Go `twigCompletionAt`: node one byte left of the cursor, no token.
    let c = |source: &str, marker: &str| {
        let offset = after(source, marker);
        by_label(complete(
            &provider,
            &template_path,
            source,
            Some(offset),
            offset - 1,
            false,
        ))
    };
    let items = c("{{ tit }}", "tit");
    assert!(items.contains_key("title"));
    assert!(items["$t"].detail.contains("Vue template instance"));
    assert!(doc(&items["$t"]).contains("component instance"));
    assert!(
        items["Object"]
            .detail
            .contains("JavaScript template global")
    );
    assert!(doc(&items["Object"]).contains("ObjectConstructor"));

    let block = c("{% block sw_card_row %}{{ it }}{% endblock %}", "{{ it");
    assert!(block["item"].detail.contains("Twig block scope"));
    assert!(doc(&block["item"]).contains("Product"));
}
