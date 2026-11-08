use std::path::Path;
use std::sync::Arc;

use swlsp_admin::{
    ADMIN_PRIVILEGE_ADMINISTRATOR, AdminComponentIndexer, AdminSourceRange,
    ComponentRegistrationKind, TwigBlock, TwigBlockScopeMember, VueComponent, VueComponentEvent,
    VueComponentMember, VueComponentMemberKind, VueComponentProp, VueComponentSlot,
    VueComponentSlotMember, VueLocalDirective,
};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider, SyntaxContext, TextDocument};
use swlsp_lsp_protocol::{Location, Position};
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

use super::*;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn new_index(dir: &Path) -> Arc<AdminComponentIndexer> {
    Arc::new(AdminComponentIndexer::new(&p(dir), None).unwrap())
}

fn index_file(index: &AdminComponentIndexer, path: &Path, source: &str) {
    index
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

fn find(source: &str, needle: &str) -> u32 {
    source.find(needle).expect(needle) as u32
}

fn rfind(source: &str, needle: &str) -> u32 {
    source.rfind(needle).expect(needle) as u32
}

/// Mirrors the Go tests' hand-built requests: node/token at `node_offset`,
/// the cursor position at `position` (or 0:0 when absent).
#[derive(Clone, Copy)]
struct Shape {
    position: Option<u32>,
    token: bool,
    language: bool,
}

const FULL: Shape = Shape {
    position: None,
    token: true,
    language: false,
};

fn request(
    path: &Path,
    source: &str,
    version: i32,
    node_offset: u32,
    shape: Shape,
) -> DefinitionRequest {
    let document = Arc::new(TextDocument::new(file_uri(&p(path)), source, version));
    let root = document.root().unwrap();
    let mut params = protocol::DefinitionParams::default();
    params.text_document.uri = document.uri.clone();
    if let Some(position) = shape.position {
        let (line, character) = document.line_index().position_utf16(position);
        params.position = Position { line, character };
    }
    DefinitionRequest {
        params,
        syntax: SyntaxContext {
            language: if shape.language {
                document.language
            } else {
                None
            },
            node: root.node_at(TextSize::from(node_offset)),
            token: if shape.token {
                root.token_at(TextSize::from(node_offset))
            } else {
                None
            },
            root: Some(root),
            document: Some(document),
        },
    }
}

/// Node, token and position all at `offset`.
fn at(provider: &AdminDefinitionProvider, path: &Path, source: &str, offset: u32) -> Vec<Location> {
    at_shape(
        provider,
        path,
        source,
        1,
        offset,
        Shape {
            position: Some(offset),
            ..FULL
        },
    )
}

/// Node and token at `offset`, position left at 0:0.
fn at_no_position(
    provider: &AdminDefinitionProvider,
    path: &Path,
    source: &str,
    offset: u32,
) -> Vec<Location> {
    at_shape(provider, path, source, 1, offset, FULL)
}

fn at_shape(
    provider: &AdminDefinitionProvider,
    path: &Path,
    source: &str,
    version: i32,
    node_offset: u32,
    shape: Shape,
) -> Vec<Location> {
    provider.get_definition(
        &Context::background(),
        &request(path, source, version, node_offset, shape),
    )
}

fn js_node(code: &str, column: u32) -> SyntaxNode {
    let parse = swlsp_parser_js::parse(code);
    parse.tree.root().node_at(TextSize::from(column)).unwrap()
}

fn uri(path: &Path) -> String {
    file_uri(&p(path))
}

fn declared(
    start_line: u32,
    start_character: u32,
    end_line: u32,
    end_character: u32,
    identifier: bool,
) -> AdminSourceRange {
    AdminSourceRange {
        start_line,
        start_character,
        end_line,
        end_character,
        declaration: true,
        identifier,
        ..Default::default()
    }
}

fn component(name: &str) -> VueComponent {
    let mut component = VueComponent::default();
    component.name = name.to_owned();
    component
}

#[test]
fn is_in_component_call_matches_registrations() {
    let cases = [
        (
            "Component.register('my-component', () => import('./index'));",
            22,
            true,
        ),
        (
            "Component.extend('my-component', 'parent', () => import('./index'));",
            22,
            true,
        ),
        (
            "Component.extend('my-component', 'parent', () => import('./index'));",
            36,
            true,
        ),
        (
            "Shopware.Component.register('my-component', () => import('./index'));",
            32,
            true,
        ),
        (
            "Shopware.Component.extend('my-component', 'parent', () => import('./index'));",
            45,
            true,
        ),
        ("const name = 'my-component';", 16, false),
        ("someFunc('my-component');", 12, false),
    ];
    for (code, column, expected) in cases {
        assert_eq!(
            is_in_component_call(&js_node(code, column)),
            expected,
            "{code}"
        );
    }
}

#[test]
fn extract_component_name_reads_string_arguments() {
    let cases = [
        (
            "Component.register('my-component', () => {});",
            22,
            "my-component",
        ),
        (
            "Component.register(\"my-component\", () => {});",
            22,
            "my-component",
        ),
        (
            "Component.extend('child', 'sw-base-component', () => {});",
            30,
            "sw-base-component",
        ),
    ];
    for (code, column, expected) in cases {
        assert_eq!(
            extract_component_name(&js_node(code, column)),
            expected,
            "{code}"
        );
    }
}

#[test]
fn normalize_prop_name_cases() {
    for (input, expected) in [
        ("label", "label"),
        ("position-identifier", "positionIdentifier"),
        (":label", "label"),
        (":position-identifier", "positionIdentifier"),
        ("v-bind:label", "label"),
        ("v-bind:position-identifier", "positionIdentifier"),
        ("@click", ""),
        ("v-on:click", ""),
        ("v-if", ""),
        ("v-for", ""),
        ("v-model", ""),
    ] {
        assert_eq!(admin::normalize_prop_name(input), expected, "{input}");
    }
}

#[test]
fn kebab_to_camel_cases() {
    for (input, expected) in [
        ("label", "label"),
        ("position-identifier", "positionIdentifier"),
        ("my-prop-name", "myPropName"),
        ("a-b-c", "aBC"),
        ("", ""),
    ] {
        assert_eq!(admin::kebab_to_camel(input), expected);
    }
}

#[test]
fn prop_definition_uses_exact_imported_type_declaration_range() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let component_dir = root.join("Resources/app/administration/src/component/sw-card");
    let contracts_path = component_dir.join("contracts.ts");
    index_file(
        &index,
        &contracts_path,
        "export interface CardProps {\n    title: string;\n}\n",
    );
    index_file(
        &index,
        &component_dir.join("Card.vue"),
        "<template><div>{{ title }}</div></template>
<script setup lang=\"ts\">
import type { CardProps } from './contracts';
defineProps<CardProps>();
</script>",
    );
    index_file(
        &index,
        &component_dir.join("index.ts"),
        "Shopware.Component.register('sw-card', () => import('./Card.vue'));",
    );
    let consumer = root.join("Resources/app/administration/src/consumer.html.twig");
    let source = "<sw-card :title=\"heading\" />";
    let locations = at(
        &AdminDefinitionProvider::new(index),
        &consumer,
        source,
        find(source, "title") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&contracts_path));
    assert_eq!(
        locations[0].range.start,
        Position {
            line: 1,
            character: 4
        }
    );
    assert_eq!(
        locations[0].range.end,
        Position {
            line: 1,
            character: 9
        }
    );
}

#[test]
fn twig_block_definition_navigates_to_parent_declaration() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("Resources/app/administration/src");
    let parent_template = p(&admin_root.join("sw-card.html.twig"));
    let child_template = admin_root.join("acme-card.html.twig");
    let mut parent = component("sw-card");
    parent.kind = ComponentRegistrationKind::Register;
    parent.file_path = p(&admin_root.join("sw-card.js"));
    parent.template_path = parent_template.clone();
    parent.blocks = vec![TwigBlock {
        name: "sw_card_content".into(),
        file_path: parent_template.clone(),
        line: 5,
        name_range: declared(4, 9, 4, 24, true),
        scope_members: vec![TwigBlockScopeMember {
            name: "item".into(),
            file_path: parent_template.clone(),
            line: 3,
            name_range: declared(2, 20, 2, 24, true),
            ..Default::default()
        }],
        ..Default::default()
    }];
    index.save_component(&parent).unwrap();
    let mut child = component("acme-card");
    child.kind = ComponentRegistrationKind::Extend;
    child.target_component = "sw-card".into();
    child.extends_component = "sw-card".into();
    child.file_path = p(&admin_root.join("acme-card.js"));
    child.template_path = p(&child_template);
    index.save_component(&child).unwrap();
    let provider = AdminDefinitionProvider::new(index);

    let source = "{% block sw_card_content %}{% endblock %}";
    let locations = at(
        &provider,
        &child_template,
        source,
        find(source, "sw_card_content") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&parent_template));
    assert_eq!(
        locations[0].range.start,
        Position {
            line: 4,
            character: 9
        }
    );
    assert_eq!(
        locations[0].range.end,
        Position {
            line: 4,
            character: 24
        }
    );

    let member_source = "{% block sw_card_content %}{{ item.name }}{% endblock %}";
    let offset = find(member_source, "item") + 1;
    let locations = at_shape(
        &provider,
        &child_template,
        member_source,
        2,
        offset,
        Shape {
            position: Some(offset),
            ..FULL
        },
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&parent_template));
    assert_eq!(
        locations[0].range.start,
        Position {
            line: 2,
            character: 20
        }
    );
    assert_eq!(
        locations[0].range.end,
        Position {
            line: 2,
            character: 24
        }
    );
}

#[test]
fn template_member_definition_uses_owning_file() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(root);
    let template_path =
        root.join("src/Resources/app/administration/src/component/sw-child/sw-child.html.twig");
    let parent_definition =
        root.join("src/Resources/app/administration/src/component/sw-parent/index.js");
    let mut child = component("sw-child");
    child.file_path = p(&template_path.parent().unwrap().join("index.js"));
    child.template_path = p(&template_path);
    child.props = vec![VueComponentProp {
        name: "parentLabel".into(),
        type_: "String".into(),
        file_path: p(&parent_definition),
        line: 8,
        ..Default::default()
    }];
    index.save_component(&child).unwrap();
    let provider = AdminDefinitionProvider::new(index);

    let source = "{{ parentLabel }}";
    let locations = at_no_position(
        &provider,
        &template_path,
        source,
        find(source, "parentLabel") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&parent_definition));
    assert_eq!(locations[0].range.start.line, 7);

    let attribute_source = "<div :title=\"parentLabel\"></div>";
    let offset = rfind(attribute_source, "parentLabel") + 1;
    let locations = at_shape(
        &provider,
        &template_path,
        attribute_source,
        2,
        offset,
        Shape {
            position: Some(offset),
            ..FULL
        },
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&parent_definition));
    assert_eq!(locations[0].range.start.line, 7);
}

#[test]
fn definition_navigates_imported_script_setup_prop() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let component_dir = admin_root.join("component/sw-imported-card");
    let component_path = component_dir.join("index.vue");
    let types_path = component_dir.join("contracts.ts");
    let draft_types_path = component_dir.join("draft-contracts.ts");
    let component_source =
        "<template>{{ heading }}<slot name=\"header\" :item=\"heading\" /></template>
<script setup lang=\"ts\">
import type { CardProps, CardSlots } from './contracts';
const { mode: heading } = defineProps<CardProps>();
defineSlots<CardSlots>();
</script>";
    index_file(&index, &component_path, component_source);
    index_file(
        &index,
        &component_dir.join("index.ts"),
        "Shopware.Component.register('sw-imported-card', () => import('./index.vue'));",
    );
    index_file(
        &index,
        &types_path,
        "export interface CardProps {\n    mode: 'small' | 'large';\n}\nexport interface HeaderPayload {\n    item: string;\n}\nexport interface CardSlots { header(props: HeaderPayload): unknown }",
    );
    index_file(
        &index,
        &draft_types_path,
        "export interface DraftProfile { city: string; zip: string }
export interface DraftProps { profile: DraftProfile }",
    );
    let local_child_path = component_dir.join("DraftPanel.vue");
    let local_child_source = "<template><slot /></template>
<script setup lang=\"ts\">
defineProps<{ label: string; count?: number }>();
</script>";
    std::fs::create_dir_all(&component_dir).unwrap();
    std::fs::write(&local_child_path, local_child_source).unwrap();
    index_file(&index, &local_child_path, local_child_source);
    let provider = AdminDefinitionProvider::new(index);

    let source = "<sw-imported-card :mode=\"'small'\" />";
    let locations = at(
        &provider,
        &admin_root.join("consumer.html.twig"),
        source,
        find(source, ":mode") + 2,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&types_path));
    assert_eq!(locations[0].range.start.line, 1);

    let slot_source = "<sw-imported-card><template #header=\"{ item: row }\">{{ row }}</template></sw-imported-card>";
    let locations = at(
        &provider,
        &admin_root.join("slot-consumer.html.twig"),
        slot_source,
        rfind(slot_source, "row") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&types_path));
    assert_eq!(locations[0].range.start.line, 4);

    let with_language = |source: &str, version: i32, offset: u32| {
        at_shape(
            &provider,
            &component_path,
            source,
            version,
            offset,
            Shape {
                position: Some(offset),
                token: true,
                language: true,
            },
        )
    };
    let live_component_source = component_source.replace("heading", "headline");
    let locations = with_language(
        &live_component_source,
        1,
        find(&live_component_source, "headline }}") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&component_path));
    assert_eq!(locations[0].range.start.line, 3);

    let live_imported_source = "<template>{{ profile.city }}</template>
<script setup lang=\"ts\">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>";
    let locations = with_language(
        live_imported_source,
        2,
        find(live_imported_source, "profile.city") + "profile.".len() as u32,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&draft_types_path));
    assert_eq!(locations[0].range.start.line, 0);

    let live_local_source = "<template><DraftPanel :label=\"title\" /></template>
<script setup lang=\"ts\">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>";
    let locations = with_language(
        live_local_source,
        3,
        find(live_local_source, "DraftPanel") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&local_child_path));
    let locations = with_language(live_local_source, 3, find(live_local_source, ":label") + 2);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&local_child_path));
}

#[test]
fn definition_uses_unsaved_legacy_definition_and_twig() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("component/index.js");
    let component_dir = admin_root.join("component/sw-legacy-live");
    let definition_path = component_dir.join("index.js");
    let template_path = component_dir.join("sw-legacy-live.html.twig");
    std::fs::create_dir_all(&component_dir).unwrap();
    let files = [
        (
            &registration_path,
            "Shopware.Component.register(
    'sw-legacy-live', () => import('./sw-legacy-live'),
);",
        ),
        (
            &definition_path,
            "import template from './sw-legacy-live.html.twig';
export default { template, props: { oldRequired: String } };",
        ),
        (&template_path, "<slot name=\"old-slot\" />"),
    ];
    for (path, source) in files {
        std::fs::write(path, source).unwrap();
    }
    for (path, source) in files {
        index_file(&index, path, source);
    }

    let live_definition = "import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
    },
};";
    let document = TextDocument::new(uri(&definition_path), live_definition, 2);
    index.update_live_document(
        &p(&definition_path),
        document.root().as_ref(),
        live_definition,
        Some(document.line_index()),
    );
    let live_template = "\n<slot name=\"live-slot\" :record=\"value\" />";
    let document = TextDocument::new(uri(&template_path), live_template, 3);
    index.update_live_document(
        &p(&template_path),
        document.root().as_ref(),
        live_template,
        Some(document.line_index()),
    );
    let provider = AdminDefinitionProvider::new(index);

    let consumer = admin_root.join("consumer.html.twig");
    let consumer_source = "<sw-legacy-live :live-required=\"1\">
    <template #live-slot />
</sw-legacy-live>";
    let definition_at = |marker: &str, inside: u32| {
        let offset = find(consumer_source, marker) + inside;
        at_shape(
            &provider,
            &consumer,
            consumer_source,
            1,
            offset,
            Shape {
                position: Some(offset),
                token: true,
                language: true,
            },
        )
    };
    let locations = definition_at("live-required", 2);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&definition_path));
    assert_eq!(locations[0].range.start.line, 4);
    let locations = definition_at("live-slot", 2);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&template_path));
    assert_eq!(locations[0].range.start.line, 1);
}

#[test]
fn scoped_slot_binding_definition_uses_contract_member() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Resources/app/administration/src");
    let declaration_path = p(&admin_root.join("meteor/MtButton.d.ts"));
    let mut button = component("mt-button");
    button.file_path = declaration_path.clone();
    button.slots = vec![VueComponentSlot {
        name: "iconFront".into(),
        file_path: declaration_path.clone(),
        line: 10,
        members: vec![VueComponentSlotMember {
            name: "size".into(),
            type_: "number".into(),
            file_path: declaration_path.clone(),
            line: 12,
            name_range: declared(11, 8, 11, 12, true),
        }],
        ..Default::default()
    }];
    index.save_component(&button).unwrap();
    let provider = AdminDefinitionProvider::new(index);

    for (file, source, needle) in [
        (
            "view.html.twig",
            "<mt-button><template #iconFront=\"{ size: iconSize }\">{{ iconSize }}</template></mt-button>",
            "iconSize",
        ),
        (
            "whole.html.twig",
            "<mt-button><template #iconFront=\"props\">{{ props.size }}</template></mt-button>",
            "size",
        ),
    ] {
        let locations = at(
            &provider,
            &admin_root.join(file),
            source,
            rfind(source, needle) + 1,
        );
        assert_eq!(locations.len(), 1, "{file}");
        assert_eq!(locations[0].uri, file_uri(&declaration_path));
        assert_eq!(locations[0].range.start.line, 11);
        assert_eq!(locations[0].range.start.character, 8);
        assert_eq!(locations[0].range.end.character, 12);
    }
}

fn sorted_uris(locations: &[Location]) -> Vec<String> {
    let mut uris: Vec<String> = locations
        .iter()
        .map(|location| location.uri.clone())
        .collect();
    uris.sort();
    uris
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

#[test]
fn scoped_slot_definition_uses_every_dynamic_candidate() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Resources/app/administration/src");
    let first_path = p(&admin_root.join("a/card.twig"));
    let second_path = p(&admin_root.join("b/card.twig"));
    for (name, index_path, type_, path, line) in [
        ("sw-card-a", "a/index.ts", "Product", &first_path, 4),
        ("sw-card-b", "b/index.ts", "Category", &second_path, 8),
    ] {
        let mut card = component(name);
        card.file_path = p(&admin_root.join(index_path));
        card.slots = vec![VueComponentSlot {
            name: "row".into(),
            members: vec![VueComponentSlotMember {
                name: "item".into(),
                type_: type_.into(),
                file_path: path.clone(),
                line,
                ..Default::default()
            }],
            ..Default::default()
        }];
        index.save_component(&card).unwrap();
    }
    let provider = AdminDefinitionProvider::new(index);
    for (source, needle) in [
        (
            "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #row=\"{ item: row }\">{{ row }}</template></component>",
            "{{ row",
        ),
        (
            "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #row=\"props\">{{ props.item }}</template></component>",
            "item",
        ),
    ] {
        let offset = find(source, needle) + needle.len() as u32;
        let locations = at_shape(
            &provider,
            &admin_root.join("consumer.html.twig"),
            source,
            1,
            offset - 1,
            Shape {
                position: Some(offset),
                ..FULL
            },
        );
        assert_eq!(locations.len(), 2, "{source}");
        assert_eq!(
            sorted_uris(&locations),
            sorted(vec![file_uri(&first_path), file_uri(&second_path)])
        );
    }
}

#[test]
fn dynamic_slot_family_definition() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Resources/app/administration/src");
    let template_path = p(&admin_root.join("grid/sw-grid.html.twig"));
    let mut grid = component("sw-grid");
    grid.template_path = template_path.clone();
    grid.slots = vec![VueComponentSlot {
        name_prefix: "column-".into(),
        file_path: template_path.clone(),
        line: 27,
        members: vec![VueComponentSlotMember {
            name: "item".into(),
            file_path: template_path.clone(),
            line: 28,
            ..Default::default()
        }],
        ..Default::default()
    }];
    index.save_component(&grid).unwrap();
    let provider = AdminDefinitionProvider::new(index);
    let source = "<sw-grid><template #column-name=\"{ item }\">{{ item }}</template></sw-grid>";
    for (needle, line) in [("column-name", 26), ("{{ item", 27)] {
        let offset = find(source, needle) + needle.len() as u32;
        let locations = at_shape(
            &provider,
            &admin_root.join("consumer.html.twig"),
            source,
            1,
            offset - 1,
            Shape {
                position: Some(offset),
                ..FULL
            },
        );
        assert_eq!(locations.len(), 1, "{needle}");
        assert_eq!(locations[0].uri, file_uri(&template_path));
        assert_eq!(locations[0].range.start.line, line);
    }
}

#[test]
fn slot_definition_resolves_dynamic_component_owner() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let first_path = p(&admin_root.join("a/card.html.twig"));
    let second_path = p(&admin_root.join("b/card.html.twig"));
    for (name, index_path, path, line) in [
        ("sw-card-a", "a/index.ts", &first_path, 4),
        ("sw-card-b", "b/index.ts", &second_path, 8),
    ] {
        let mut card = component(name);
        card.file_path = p(&admin_root.join(index_path));
        card.template_path = path.clone();
        card.slots = vec![VueComponentSlot {
            name: "header".into(),
            file_path: path.clone(),
            line,
            ..Default::default()
        }];
        index.save_component(&card).unwrap();
    }
    let source =
        "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #header /></component>";
    let locations = at(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("consumer.html.twig"),
        source,
        find(source, "header") + 2,
    );
    assert_eq!(locations.len(), 2);
    assert_eq!(
        sorted_uris(&locations),
        sorted(vec![file_uri(&first_path), file_uri(&second_path)])
    );
}

#[test]
fn this_member_definition_uses_inherited_origin() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(root);
    let definition_path =
        root.join("src/Resources/app/administration/src/component/sw-child/index.js");
    let parent_path =
        root.join("src/Resources/app/administration/src/component/sw-parent/index.js");
    let mut child = component("sw-child");
    child.file_path = p(&definition_path);
    child.definition_path = p(&definition_path);
    child.computed = vec!["parentTitle".into()];
    child.members = vec![VueComponentMember {
        name: "parentTitle".into(),
        kind: VueComponentMemberKind::Computed,
        file_path: p(&parent_path),
        line: 12,
        ..Default::default()
    }];
    index.save_component(&child).unwrap();
    let source = "export default { computed: { value() { return this.parentTitle; } } };";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &definition_path,
        source,
        find(source, "parentTitle") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&parent_path));
    assert_eq!(locations[0].range.start.line, 11);
}

#[test]
fn runtime_registry_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(root);
    let registration_path =
        root.join("src/Administration/Resources/app/administration/src/main.ts");
    index_file(
        &index,
        &registration_path,
        "
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('profile', {
    actions: {
        load() {},
    },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: {
        viewer: {
            privileges: ['product:read'],
        },
    },
});
",
    );
    let provider = AdminDefinitionProvider::new(index);
    let document_path = registration_path.parent().unwrap().join("consumer.ts");
    for (name, source, needle, expected_line) in [
        ("service", "Shopware.Service('acl')", "acl", 1),
        ("store", "Shopware.Store.get('profile')", "profile", 2),
        (
            "store unregister",
            "Shopware.Store.unregister('profile')",
            "profile",
            2,
        ),
        (
            "store member",
            "Shopware.Store.get('profile').load()",
            "load",
            4,
        ),
        (
            "privilege role",
            "this.acl.can('product.viewer')",
            "product.viewer",
            10,
        ),
        (
            "permission",
            "const route = { privilege: 'product:read' }",
            "product:read",
            11,
        ),
    ] {
        let locations =
            at_no_position(&provider, &document_path, source, rfind(source, needle) + 1);
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, uri(&registration_path), "{name}");
        assert_eq!(locations[0].range.start.line, expected_line, "{name}");
    }
}

#[test]
fn application_container_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let global_path = admin_root.join("global.types.ts");
    index_file(
        &index,
        &global_path,
        "export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
}",
    );
    let service_path = admin_root.join("services.ts");
    index_file(
        &index,
        &service_path,
        "Shopware.Application.addServiceProvider('acl', factory);",
    );
    let provider = AdminDefinitionProvider::new(index);
    let document_path = admin_root.join("consumer.ts");
    let definition = |source: &str, needle: &str| {
        at_no_position(&provider, &document_path, source, rfind(source, needle) + 1)
    };

    let locations = definition("Application.getContainer('factory').locale", "locale");
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&global_path));
    assert_eq!(locations[0].range.start.line, 3);
    assert_eq!(locations[0].range.start.character, 8);
    assert_eq!(locations[0].range.end.character, 14);

    let locations = definition(
        "function run() {
    const services = Shopware.Application.getContainer('service');
    return services.acl;
}",
        "acl",
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&service_path));
    assert_eq!(locations[0].range.start.line, 0);
}

#[test]
fn shopware_context_definition_uses_nested_type_range() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let context_path = admin_root.join("app/composables/use-context.ts");
    index_file(
        &index,
        &context_path,
        "export interface ContextState {
    app: { environment: null | string };
    api: {
        languageId: null | string;
    };
}",
    );
    let source = "const languageId = Shopware.Context.api.languageId;";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("module/example/index.ts"),
        source,
        rfind(source, "languageId") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&context_path));
    assert_eq!(locations[0].range.start.line, 3);
    assert_eq!(locations[0].range.start.character, 8);
    assert_eq!(locations[0].range.end.character, 18);
}

#[test]
fn shopware_utils_definition_uses_export_range() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let util_path = admin_root.join("core/service/util.service.ts");
    index_file(
        &index,
        &util_path,
        "export const format = { date };
export default { format };
function date(value: string): string { return value; }",
    );
    let source = "const { date: formatDate } = Shopware.Utils.format;
formatDate('2026-01-01');";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("module/example/index.ts"),
        source,
        rfind(source, "formatDate") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&util_path));
    assert_eq!(locations[0].range.start.line, 0);
    assert_eq!(locations[0].range.start.character, 24);
    assert_eq!(locations[0].range.end.character, 28);
}

#[test]
fn shopware_event_bus_definition_uses_event_key_range() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let util_path = admin_root.join("core/service/util.service.ts");
    let event_bus_path = admin_root.join("core/service/utils/eventBus.utils.ts");
    let event_bus_source = "interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
}
const emitter = mitt<Events>();
export default emitter;";
    index_file(
        &index,
        &util_path,
        "import EventBus from './utils/eventBus.utils';
export default { EventBus };",
    );
    index_file(&index, &event_bus_path, event_bus_source);
    let source = "const { EventBus } = Shopware.Utils;
EventBus.on('save-event', handler);";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("module/example/index.ts"),
        source,
        rfind(source, "save-event") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&event_bus_path));
    let lines = LineIndex::new(event_bus_source.to_owned());
    let (line, character) = lines.position_utf16(find(event_bus_source, "save-event"));
    assert_eq!(locations[0].range.start.line, line);
    assert_eq!(locations[0].range.start.character, character);
    assert_eq!(
        locations[0].range.end.character,
        character + "save-event".len() as u32
    );
}

#[test]
fn vue_for_and_event_lexical_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let event_path = p(&root.join("meteor/MtSwitch.d.ts"));
    let mut switch = component("mt-switch");
    switch.events = vec![VueComponentEvent {
        name: "update:modelValue".into(),
        type_: "(value: boolean) => any".into(),
        file_path: event_path.clone(),
        line: 22,
        ..Default::default()
    }];
    index.save_component(&switch).unwrap();
    let provider = AdminDefinitionProvider::new(index);
    let view = root.join("Resources/app/administration/view.html.twig");
    for (source, marker, expected_uri, line, character) in [
        (
            "<div v-for=\"(item, index) in items\">{{ item.name }}</div>",
            "item.name",
            None,
            0,
            13,
        ),
        (
            "<mt-switch @update:model-value=\"save($event)\" />",
            "$event",
            Some(file_uri(&event_path)),
            21,
            0,
        ),
    ] {
        let locations = at(&provider, &view, source, rfind(source, marker) + 1);
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, expected_uri.unwrap_or_else(|| uri(&view)));
        assert_eq!(locations[0].range.start.line, line);
        assert_eq!(locations[0].range.start.character, character);
    }
}

#[test]
fn model_definition_returns_prop_and_event() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let prop_path = p(&root.join("component/props.ts"));
    let event_path = p(&root.join("component/events.ts"));
    let mut switch = component("mt-switch");
    switch.props = vec![VueComponentProp {
        name: "checked".into(),
        type_: "Boolean".into(),
        file_path: prop_path.clone(),
        line: 7,
        ..Default::default()
    }];
    switch.events = vec![VueComponentEvent {
        name: "update:checked".into(),
        type_: "(value: boolean) => void".into(),
        file_path: event_path.clone(),
        line: 12,
        ..Default::default()
    }];
    index.save_component(&switch).unwrap();
    let source = "<mt-switch v-model:checked=\"enabled\" />";
    let locations = at(
        &AdminDefinitionProvider::new(index),
        &root.join("Resources/app/administration/view.html.twig"),
        source,
        find(source, "checked") + 1,
    );
    assert_eq!(locations.len(), 2);
    assert_eq!(locations[0].uri, file_uri(&prop_path));
    assert_eq!(locations[0].range.start.line, 6);
    assert_eq!(locations[1].uri, file_uri(&event_path));
    assert_eq!(locations[1].range.start.line, 11);
}

#[test]
fn nested_vue_member_definition_uses_type_declaration() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("Resources/app/administration/src/component/sw-view");
    let definition_path = admin_root.join("index.ts");
    let template_path = admin_root.join("view.html.twig");
    index_file(
        &index,
        &definition_path,
        "interface Manufacturer { name: string; }\ninterface Product { manufacturer: Manufacturer; getManufacturer(): Manufacturer; }",
    );
    let mut view = component("sw-view");
    view.file_path = p(&definition_path);
    view.definition_path = p(&definition_path);
    view.template_path = p(&template_path);
    view.members = vec![
        VueComponentMember {
            name: "productsById".into(),
            kind: VueComponentMemberKind::Computed,
            type_: "Record<string, Product>".into(),
            file_path: p(&definition_path),
            ..Default::default()
        },
        VueComponentMember {
            name: "selectedProduct".into(),
            kind: VueComponentMemberKind::Prop,
            type_: "Product".into(),
            file_path: p(&definition_path),
            ..Default::default()
        },
    ];
    index.save_component(&view).unwrap();
    let provider = AdminDefinitionProvider::new(index);

    let source = "<div v-for=\"product in Object.values(productsById)\">{{ product.manufacturer.name }}</div>";
    let locations = at(
        &provider,
        &template_path,
        source,
        find(source, "name }}") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&definition_path));
    assert_eq!(locations[0].range.start.line, 0);

    let indexed_source =
        "<div :title=\"productsById[selectedProduct.manufacturer.name].manufacturer.name\"></div>";
    let locations = at(
        &provider,
        &template_path,
        indexed_source,
        rfind(indexed_source, "name\"") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&definition_path));
    assert_eq!(locations[0].range.start.line, 0);

    let instance_source = "<div :title=\"selectedProduct.getManufacturer().name\"></div>";
    let offset = find(instance_source, "name\"") + 1;
    let locations = at_shape(
        &provider,
        &template_path,
        instance_source,
        1,
        offset,
        Shape {
            position: Some(offset),
            token: false,
            language: false,
        },
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&definition_path));
    assert_eq!(locations[0].range.start.line, 0);
}

fn no_token(offset: u32) -> Shape {
    Shape {
        position: Some(offset),
        token: false,
        language: false,
    }
}

#[test]
fn twig_privilege_definition() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("privileges.ts");
    index_file(
        &index,
        &registration_path,
        "Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});",
    );
    let source = "<mt-button :disabled=\"acl.can('product.viewer')\" />";
    let offset = find(source, "product.viewer") + 2;
    let locations = at_shape(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("view.html.twig"),
        source,
        1,
        offset,
        no_token(offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&registration_path));
    assert_eq!(locations[0].range.start.line, 1);
}

#[test]
fn builtin_privilege_has_no_definition() {
    let dir = tempfile::tempdir().unwrap();
    let index = new_index(&dir.path().join("cache"));
    let locations =
        AdminDefinitionProvider::new(index).privilege_definition(ADMIN_PRIVILEGE_ADMINISTRATOR);
    assert!(locations.is_empty());
}

#[test]
fn twig_module_route_definition() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("module/sw-product/index.ts");
    index_file(
        &index,
        &registration_path,
        "Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
    },
});",
    );
    let source = "<router-link :to=\"{ name: 'sw.product.detail' }\" />";
    let offset = find(source, "sw.product.detail") + 2;
    let locations = at_shape(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("view.html.twig"),
        source,
        1,
        offset,
        no_token(offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&registration_path));
    assert_eq!(locations[0].range.start.line, 2);
}

#[test]
fn service_definition_uses_implementation() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("main.ts");
    let implementation_path = admin_root.join("acl.service.ts");
    std::fs::create_dir_all(&admin_root).unwrap();
    std::fs::write(&implementation_path, "").unwrap();
    index_file(
        &index,
        &registration_path,
        "
import AclService from './acl.service';
Shopware.Application.addServiceProvider('acl', () => new AclService());
",
    );
    let source = "Shopware.Service('acl')";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("consumer.ts"),
        source,
        find(source, "acl") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&implementation_path));
    assert_eq!(locations[0].range.start.line, 0);
}

#[test]
fn imported_setup_store_member_definition() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let store_path = admin_root.join("app/store/session.store.ts");
    let factory_path = admin_root.join("app/composables/use-session.ts");
    std::fs::create_dir_all(factory_path.parent().unwrap()).unwrap();
    std::fs::write(&factory_path, "").unwrap();
    index_file(
        &index,
        &store_path,
        "
import useSession from '../composables/use-session';
export default Shopware.Store.register('session', useSession);",
    );
    index_file(
        &index,
        &factory_path,
        "
const currentUser = ref(null);
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, setCurrentUser };
}",
    );
    let source = "Shopware.Store.get('session').setCurrentUser()";
    let locations = at_no_position(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("consumer.ts"),
        source,
        find(source, "setCurrentUser") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&factory_path));
    assert_eq!(locations[0].range.start.line, 2);
}

#[test]
fn component_and_module_registry_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("main.ts");
    index_file(
        &index,
        &registration_path,
        "
Shopware.Component.register('sw-card', { props: { title: String } });
Shopware.Module.register('sw-product', {
    routes: { index: { path: 'index', component: 'sw-card' } },
});",
    );
    let provider = AdminDefinitionProvider::new(index);
    for (source, needle, line) in [
        (
            "Shopware.Component.getComponentRegistry().has('sw-card')",
            "sw-card",
            0,
        ),
        (
            "Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            2,
        ),
    ] {
        let locations = at_no_position(
            &provider,
            &admin_root.join("consumer.ts"),
            source,
            find(source, needle) + 1,
        );
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, uri(&registration_path));
        assert_eq!(locations[0].range.start.line, line);
    }
}

#[test]
fn directive_definitions_from_javascript_and_twig() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("app/directive/tooltip.directive.ts");
    index_file(
        &index,
        &registration_path,
        "Shopware.Directive.register('tooltip', {});",
    );
    let provider = AdminDefinitionProvider::new(index);
    for (source, needle, extension) in [
        ("Shopware.Directive.getByName('tooltip')", "tooltip", ".ts"),
        (
            "<div v-tooltip.bottom=\"message\"></div>",
            "tooltip",
            ".html.twig",
        ),
    ] {
        let locations = at(
            &provider,
            &admin_root.join(format!("consumer{extension}")),
            source,
            find(source, needle) + 1,
        );
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, uri(&registration_path));
        assert_eq!(locations[0].range.start.line, 0);
    }
}

#[test]
fn filter_definition() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let registration_path = admin_root.join("app/filter/currency.ts");
    index_file(
        &index,
        &registration_path,
        "Shopware.Filter.register('currency', value => value);",
    );
    let source = "Shopware.Filter.getByName('currency')";
    let locations = at(
        &AdminDefinitionProvider::new(index),
        &admin_root.join("consumer.ts"),
        source,
        find(source, "currency") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, uri(&registration_path));
}

#[test]
fn cms_registry_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let element_path = admin_root.join("cms/hero.ts");
    let block_path = admin_root.join("cms/hero-grid.ts");
    let component_path = admin_root.join("component/sw-cms-el-hero/index.ts");
    index_file(
        &index,
        &element_path,
        "Shopware.Service('cmsService').registerCmsElement({ name: 'hero' });",
    );
    index_file(
        &index,
        &block_path,
        "Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });",
    );
    index_file(
        &index,
        &component_path,
        "Shopware.Component.register('sw-cms-el-hero', { props: ['config'] });",
    );
    let provider = AdminDefinitionProvider::new(index);
    for (source, needle, target) in [
        (
            "cmsService.getCmsElementConfigByName('hero')",
            "hero",
            &element_path,
        ),
        (
            "cmsService.getCmsBlockConfigByName('hero-grid')",
            "hero-grid",
            &block_path,
        ),
        (
            "cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'hero' } } })",
            "hero",
            &element_path,
        ),
        (
            "cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-hero' })",
            "sw-cms-el-hero",
            &component_path,
        ),
    ] {
        let locations = at(
            &provider,
            &admin_root.join("consumer.ts"),
            source,
            rfind(source, needle) + 1,
        );
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, uri(target));
    }
}

#[test]
fn directive_definition_prefers_template_local_declaration() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let global_path = admin_root.join("app/directive/hide.ts");
    let local_path = p(&admin_root.join("component/sw-owner/index.ts"));
    let template_path = admin_root.join("component/sw-owner/sw-owner.html.twig");
    index_file(
        &index,
        &global_path,
        "Shopware.Directive.register('hide', {});",
    );
    let mut owner = component("sw-owner");
    owner.file_path = local_path.clone();
    owner.definition_path = local_path.clone();
    owner.template_path = p(&template_path);
    owner.local_directives = vec![VueLocalDirective {
        name: "hide".into(),
        file_path: local_path.clone(),
        line: 7,
        ..Default::default()
    }];
    index.save_component(&owner).unwrap();
    let source = "<div v-hide=\"hidden\"></div>";
    let locations = at(
        &AdminDefinitionProvider::new(index),
        &template_path,
        source,
        find(source, "hide") + 1,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&local_path));
    assert_eq!(locations[0].range.start.line, 6);
}

#[test]
fn markup_event_and_inherited_slot_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Resources/app/administration/src");
    let event_path = p(&admin_root.join("component/sw-parent/index.js"));
    let parent_template = p(&admin_root.join("component/sw-parent/sw-parent.html.twig"));
    let child_template = admin_root.join("component/sw-child/sw-child.html.twig");
    let mut child = component("sw-child");
    child.file_path = event_path.clone();
    child.definition_path = event_path.clone();
    child.template_path = p(&child_template);
    child.events = vec![VueComponentEvent {
        name: "update:modelValue".into(),
        file_path: event_path.clone(),
        line: 11,
        name_range: declared(10, 8, 10, 25, false),
        ..Default::default()
    }];
    child.slots = vec![VueComponentSlot {
        name: "header".into(),
        file_path: parent_template.clone(),
        line: 7,
        name_range: declared(6, 12, 6, 18, false),
        ..Default::default()
    }];
    index.save_component(&child).unwrap();
    let provider = AdminDefinitionProvider::new(index);
    for (source, needle, expected_path, line, start, end) in [
        (
            "<sw-child @update:model-value.stop=\"onUpdate\" />",
            "update:model-value",
            &event_path,
            10,
            8,
            25,
        ),
        (
            "<sw-child><template #header></template></sw-child>",
            "header",
            &parent_template,
            6,
            12,
            18,
        ),
    ] {
        let locations =
            at_no_position(&provider, &child_template, source, find(source, needle) + 1);
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(expected_path));
        assert_eq!(locations[0].range.start.line, line);
        assert_eq!(locations[0].range.start.character, start);
        assert_eq!(locations[0].range.end.character, end);
    }
}

#[test]
fn dynamic_component_selector_and_prop_definitions() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let index = new_index(&root.join("cache"));
    let admin_root = root.join("src/Resources/app/administration/src");
    let definition_path = p(&admin_root.join("component/sw-card/index.ts"));
    let panel_definition_path = p(&admin_root.join("component/sw-panel/index.ts"));
    let template_path = admin_root.join("consumer.html.twig");
    for (name, path, line) in [
        ("sw-card", &definition_path, 5),
        ("sw-panel", &panel_definition_path, 8),
    ] {
        let mut card = component(name);
        card.file_path = path.clone();
        card.definition_path = path.clone();
        card.props = vec![VueComponentProp {
            name: "title".into(),
            file_path: path.clone(),
            line,
            ..Default::default()
        }];
        index.save_component(&card).unwrap();
    }
    let mut host = component("sw-host");
    host.file_path = p(&admin_root.join("sw-host/index.ts"));
    host.template_path = p(&template_path);
    host.members = vec![VueComponentMember {
        name: "dynamicCard".into(),
        kind: VueComponentMemberKind::Computed,
        return_expressions: vec!["'sw-card'".into(), "'sw-panel'".into()],
        returns_complete: true,
        ..Default::default()
    }];
    index.save_component(&host).unwrap();
    let provider = AdminDefinitionProvider::new(index);

    let source = "<component :is=\"'sw-card'\" :title=\"title\" />";
    for (needle, line) in [("sw-card", 0), (":title", 4)] {
        let locations = at(&provider, &template_path, source, find(source, needle) + 1);
        assert_eq!(locations.len(), 1, "{needle}");
        assert_eq!(locations[0].uri, file_uri(&definition_path));
        assert_eq!(locations[0].range.start.line, line);
    }

    let both = sorted(vec![
        file_uri(&definition_path),
        file_uri(&panel_definition_path),
    ]);
    for (source, needle) in [
        (
            "<component :is=\"active ? 'sw-card' : 'sw-panel'\" :title=\"title\" />",
            ":title",
        ),
        (
            "<component :is=\"dynamicCard\" :title=\"title\" />",
            ":title",
        ),
        (
            "<component :is=\"dynamicCard\" v-bind=\"{ title: heading }\" />",
            "title",
        ),
    ] {
        let locations = at(&provider, &template_path, source, find(source, needle) + 1);
        assert_eq!(locations.len(), 2, "{source}");
        assert_eq!(sorted_uris(&locations), both);
    }
}
