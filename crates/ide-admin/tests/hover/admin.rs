//! Administration hover: components, props, registries and template members.

use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminSymbolKind, ComponentRegistrationKind, TwigBlock,
    TwigBlockScopeMember, VueComponent, VueComponentEvent, VueComponentMember,
    VueComponentMemberKind, VueComponentProp, VueComponentSlot, VueComponentSlotMember,
    VueLocalDirective,
};
use swlsp_ide_admin::hover::AdminHoverProvider;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

/// `VueComponent` has private fields, so build it field by field.
macro_rules! component {
    ($($field:ident: $value:expr),* $(,)?) => {{
        let mut component = VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}

struct Fixture {
    _dir: tempfile::TempDir,
    root: String,
    index: Arc<AdminComponentIndexer>,
}

impl Fixture {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().to_string_lossy().into_owned();
        let index = Arc::new(AdminComponentIndexer::new(&format!("{root}/cache"), None).unwrap());
        Self {
            _dir: dir,
            root,
            index,
        }
    }

    fn path(&self, relative: &str) -> String {
        format!("{}/{relative}", self.root)
    }

    fn provider(&self) -> AdminHoverProvider {
        AdminHoverProvider::new(self.root.clone(), self.index.clone())
    }

    fn save(&self, component: VueComponent) {
        self.index.save_component(&component).unwrap();
    }

    fn index_file(&self, path: &str, source: &str) {
        self.index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
}

/// A hand-built hover request.
struct Probe {
    /// Byte offset of `params.Position`; `None` leaves the position at 0:0.
    position: Option<u32>,
    /// Byte offset of the request's node and token.
    node: u32,
    token: bool,
    language: bool,
}

impl Probe {
    fn at(offset: u32) -> Self {
        Self {
            position: Some(offset),
            node: offset,
            token: true,
            language: false,
        }
    }

    fn unpositioned(offset: u32) -> Self {
        Self {
            position: None,
            node: offset,
            token: true,
            language: false,
        }
    }

    fn without_token(mut self) -> Self {
        self.token = false;
        self
    }
}

fn request(path: &str, source: &str, version: i32, probe: &Probe) -> HoverRequest {
    let document = Arc::new(TextDocument::new(file_uri(path), source, version));
    let root = document.root().unwrap();
    let mut params = protocol::HoverParams::default();
    params.text_document.uri = document.uri.clone();
    if let Some(position) = probe.position {
        let (line, character) = document.line_index().position_utf16(position);
        params.position = protocol::Position { line, character };
    }
    let offset = TextSize::from(probe.node);
    HoverRequest {
        params,
        syntax: SyntaxContext {
            language: if probe.language {
                document.language
            } else {
                None
            },
            node: root.node_at(offset),
            token: if probe.token {
                root.token_at(offset)
            } else {
                None
            },
            root: Some(root),
            document: Some(document),
        },
    }
}

fn hover(
    provider: &AdminHoverProvider,
    path: &str,
    source: &str,
    probe: Probe,
) -> Option<protocol::Hover> {
    provider
        .get_hover(&Context::background(), &request(path, source, 1, &probe))
        .unwrap()
}

fn index_of(source: &str, needle: &str) -> u32 {
    source.find(needle).unwrap() as u32
}

fn last_index_of(source: &str, needle: &str) -> u32 {
    source.rfind(needle).unwrap() as u32
}

#[track_caller]
fn assert_contains(value: &str, expected: &str) {
    assert!(
        value.contains(expected),
        "expected {expected:?} in:\n{value}"
    );
}

fn js_root(code: &str) -> swlsp_syntax::SyntaxNode {
    swlsp_parser_js::parse(code).root()
}

fn js_symbol(code: &str, col: u32) -> Option<swlsp_admin::AdminSymbolTarget> {
    let root = js_root(code);
    let node = root
        .node_at(TextSize::from(col))
        .unwrap_or_else(|| panic!("no node at {col}"));
    swlsp_admin::javascript_symbol_at(&node)
}

#[test]
fn admin_hover_is_in_component_call() {
    for (name, code, col, expected) in [
        (
            "Component.register first arg",
            "Component.register('my-component', () => import('./index'));",
            22,
            true,
        ),
        (
            "Component.extend first arg",
            "Component.extend('my-component', 'parent', () => import('./index'));",
            22,
            true,
        ),
        (
            "Component.extend second arg (parent)",
            "Component.extend('my-component', 'parent', () => import('./index'));",
            36,
            true,
        ),
        (
            "Shopware.Component.register",
            "Shopware.Component.register('my-component', () => import('./index'));",
            32,
            true,
        ),
        (
            "Shopware.Component.extend",
            "Shopware.Component.extend('my-component', 'parent', () => import('./index'));",
            45,
            true,
        ),
        (
            "not in component call",
            "const name = 'my-component';",
            16,
            false,
        ),
        (
            "different function call",
            "someFunc('my-component');",
            12,
            false,
        ),
    ] {
        let result =
            js_symbol(code, col).is_some_and(|target| target.kind == AdminSymbolKind::Component);
        assert_eq!(result, expected, "{name}");
    }
}

#[test]
fn admin_hover_extract_component_name() {
    for (name, code, col, expected) in [
        (
            "single quoted string",
            "Component.register('my-component', () => {});",
            22,
            "my-component",
        ),
        (
            "double quoted string",
            "Component.register(\"my-component\", () => {});",
            22,
            "my-component",
        ),
        (
            "parent component name",
            "Component.extend('child', 'sw-base-component', () => {});",
            30,
            "sw-base-component",
        ),
    ] {
        let result = js_symbol(code, col)
            .filter(|target| target.kind == AdminSymbolKind::Component)
            .map(|target| target.name)
            .unwrap_or_default();
        assert_eq!(result, expected, "{name}");
    }
}

#[test]
fn admin_twig_block_hover_shows_parent_and_deprecation() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    let parent_template = format!("{admin_root}/sw-card.html.twig");
    let child_template = format!("{admin_root}/acme-card.html.twig");
    fixture.save(component! {
        name: "sw-card".into(),
        kind: ComponentRegistrationKind::Register,
        file_path: format!("{admin_root}/sw-card.js"),
        template_path: parent_template.clone(),
        blocks: vec![TwigBlock {
            name: "sw_card_legacy".into(),
            file_path: parent_template.clone(),
            line: 4,
            deprecated: "Use sw_card_content instead.".into(),
            ..Default::default()
        }],
    });
    fixture.save(component! {
        name: "acme-card".into(),
        kind: ComponentRegistrationKind::Extend,
        target_component: "sw-card".into(),
        extends_component: "sw-card".into(),
        file_path: format!("{admin_root}/acme-card.js"),
        template_path: child_template.clone(),
    });
    let source = "{% block sw_card_legacy %}{% endblock %}";
    let offset = index_of(source, "sw_card_legacy") + 2;
    let hover = hover(
        &fixture.provider(),
        &child_template,
        source,
        Probe::at(offset),
    )
    .unwrap();
    let value = &hover.contents.value;
    assert_contains(value, "Administration Twig block");
    assert_contains(value, "Parent component:** `sw-card`");
    assert_contains(value, "Use sw_card_content instead");
    let range = hover.range.unwrap();
    assert_eq!(range.start.character, 9);
    assert_eq!(range.end.character, 23);
}

fn prop(name: &str, type_: &str) -> VueComponentProp {
    VueComponentProp {
        name: name.into(),
        type_: type_.into(),
        ..Default::default()
    }
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

#[test]
fn build_hover_content() {
    let fixture = Fixture::new();
    let provider = AdminHoverProvider::new("", fixture.index.clone());
    let cases: Vec<(&str, VueComponent, Vec<&str>)> = vec![
        (
            "component with props",
            component! {
                name: "sw-button".into(),
                props: vec![
                    VueComponentProp {
                        required: true,
                        ..prop("label", "String")
                    },
                    VueComponentProp {
                        default: "false".into(),
                        ..prop("disabled", "Boolean")
                    },
                ],
            },
            vec![
                "## `sw-button`",
                "### Props",
                "`label`: **String** *(required)*",
                "`disabled`: **Boolean** = `false`",
            ],
        ),
        (
            "component with emits",
            component! {
                name: "sw-input".into(),
                emits: strings(&["input", "change", "blur"]),
            },
            vec![
                "## `sw-input`",
                "### Events",
                "`input`",
                "`change`",
                "`blur`",
            ],
        ),
        (
            "component with methods",
            component! {
                name: "sw-modal".into(),
                methods: strings(&["open", "close", "toggle"]),
            },
            vec![
                "## `sw-modal`",
                "### Methods",
                "`open()`",
                "`close()`",
                "`toggle()`",
            ],
        ),
        (
            "component with computed",
            component! {
                name: "sw-list".into(),
                computed: strings(&["filteredItems", "totalCount"]),
            },
            vec![
                "## `sw-list`",
                "### Computed",
                "`filteredItems`",
                "`totalCount`",
            ],
        ),
        (
            "component that extends another",
            component! {
                name: "sw-custom-button".into(),
                extends_component: "sw-button".into(),
            },
            vec!["## `sw-custom-button`", "**Extends**: `sw-button`"],
        ),
        (
            "component with definition path",
            component! {
                name: "sw-card".into(),
                definition_path: "/path/to/sw-card/index.js".into(),
            },
            vec!["## `sw-card`", "*Defined in*: `/path/to/sw-card/index.js`"],
        ),
        (
            "full component",
            component! {
                name: "sw-data-grid".into(),
                extends_component: "sw-base-grid".into(),
                props: vec![
                    VueComponentProp {
                        required: true,
                        ..prop("columns", "Array")
                    },
                    prop("dataSource", "Array"),
                ],
                emits: strings(&["selection-change", "page-change"]),
                methods: strings(&["refresh", "selectAll"]),
                computed: strings(&["selectedItems"]),
                definition_path: "/path/to/sw-data-grid/index.js".into(),
            },
            vec![
                "## `sw-data-grid`",
                "**Extends**: `sw-base-grid`",
                "### Props",
                "`columns`: **Array** *(required)*",
                "`dataSource`: **Array**",
                "### Events",
                "`selection-change`",
                "`page-change`",
                "### Methods",
                "`refresh()`",
                "`selectAll()`",
                "### Computed",
                "`selectedItems`",
                "*Defined in*: `/path/to/sw-data-grid/index.js`",
            ],
        ),
    ];
    for (name, component, contains) in cases {
        let result = provider.build_hover_content(&[component]);
        for expected in contains {
            assert!(
                result.contains(expected),
                "{name}: {expected:?} in {result}"
            );
        }
    }
}

#[test]
fn admin_hover_content_exposes_deprecations() {
    let fixture = Fixture::new();
    let provider = AdminHoverProvider::new("", fixture.index.clone());
    let deprecated = VueComponentProp {
        deprecated: "tag:v6.8.0 - Use modernValue instead.".into(),
        ..prop("oldValue", "String")
    };
    let content = provider.build_hover_content(&[component! {
        name: "sw-legacy".into(),
        deprecated: "Use mt-modern instead.".into(),
        props: vec![deprecated.clone()],
    }]);
    assert_contains(&content, "**Deprecated:** Use mt-modern instead.");
    assert_contains(&content, "`oldValue`: **String** *(deprecated)*");

    let prop_content = AdminHoverProvider::prop_markdown("sw-legacy", &deprecated);
    assert_contains(&prop_content, "**prop** `oldValue`: `String`");
    assert_contains(&prop_content, "**Deprecated:** tag:v6.8.0");
}

#[test]
fn admin_component_member_hover_exposes_deprecation() {
    let fixture = Fixture::new();
    let template_path = fixture.path("Resources/app/administration/src/sw-card.html.twig");
    let definition_path = fixture.path("Resources/app/administration/src/sw-card.ts");
    fixture.save(component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        methods: strings(&["legacySave"]),
        members: vec![VueComponentMember {
            name: "legacySave".into(),
            kind: VueComponentMemberKind::Method,
            type_: "() => void".into(),
            file_path: definition_path.clone(),
            line: 12,
            deprecated: "Use save instead.".into(),
            ..Default::default()
        }],
    });
    let provider = fixture.provider();
    let this_hover = provider
        .this_member_hover(&file_uri(&definition_path), "legacySave")
        .unwrap()
        .unwrap();
    assert_contains(
        &this_hover.contents.value,
        "**Deprecated:** Use save instead.",
    );

    let source = "{{ legacySave() }}";
    let offset = index_of(source, "legacySave") + 2;
    let result = hover(&provider, &template_path, source, Probe::at(offset)).unwrap();
    assert_contains(&result.contents.value, "**Deprecated:** Use save instead.");
}

#[test]
fn admin_template_runtime_identifier_hover() {
    let fixture = Fixture::new();
    let template_path = fixture.path("Resources/app/administration/src/sw-card.html.twig");
    fixture.save(component! {
        name: "sw-card".into(),
        template_path: template_path.clone(),
        file_path: fixture.path("Resources/app/administration/src/index.ts"),
        blocks: vec![TwigBlock {
            name: "sw_card_row".into(),
            scope_members: vec![TwigBlockScopeMember {
                name: "item".into(),
                type_: "Product".into(),
                file_path: template_path.clone(),
                line: 4,
                ..Default::default()
            }],
            ..Default::default()
        }],
    });
    let provider = fixture.provider();
    for (name, source, needle, expected) in [
        (
            "Vue instance member",
            "{{ $t('key') }}",
            "$t",
            "Administration Vue component instance",
        ),
        (
            "JavaScript global",
            "{{ Object.keys(values) }}",
            "Object",
            "JavaScript template runtime",
        ),
        (
            "Twig block scope",
            "{% block sw_card_row %}{{ item.name }}{% endblock %}",
            "item",
            "Twig block scope",
        ),
    ] {
        let offset = index_of(source, needle) + 1;
        let result = hover(&provider, &template_path, source, Probe::at(offset))
            .unwrap_or_else(|| panic!("{name}: no hover"));
        assert_contains(&result.contents.value, expected);
    }
}

#[test]
fn admin_template_prop_hover() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/sw-card.html.twig");
    fixture.save(component! {
        name: "sw-card".into(),
        file_path: fixture.path("src/Resources/app/administration/src/component/sw-card/index.js"),
        template_path: template_path.clone(),
        props: vec![VueComponentProp {
            required: true,
            default: "'Card'".into(),
            documentation: "Heading shown above the card content.".into(),
            allowed_values: strings(&["Card", "Panel"]),
            allowed_values_complete: true,
            ..prop("title", "String")
        }],
    });
    let source = "{{ title }}";
    let offset = index_of(source, "title") + 1;
    let result = hover(
        &fixture.provider(),
        &template_path,
        source,
        Probe::unpositioned(offset),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "**prop** `title`: `String`");
    assert_contains(value, "Heading shown above the card content.");
    assert_contains(value, "Required");
    assert_contains(value, "Default");
    assert_contains(value, "Allowed values: `Card`, `Panel`");

    let attribute_source = "<div :title=\"title\"></div>";
    let attribute_offset = last_index_of(attribute_source, "title") + 1;
    let attribute_hover = fixture
        .provider()
        .get_hover(
            &Context::background(),
            &request(
                &template_path,
                attribute_source,
                2,
                &Probe::at(attribute_offset),
            ),
        )
        .unwrap()
        .unwrap();
    assert_contains(
        &attribute_hover.contents.value,
        "**prop** `title`: `String`",
    );
}

#[test]
fn admin_scoped_slot_binding_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let declaration_path = format!("{admin_root}/meteor/MtButton.d.ts");
    fixture.save(component! {
        name: "mt-button".into(),
        file_path: declaration_path.clone(),
        slots: vec![VueComponentSlot {
            name: "iconFront".into(),
            file_path: declaration_path.clone(),
            line: 10,
            payload_type: "{ size: number }".into(),
            members: vec![VueComponentSlotMember {
                name: "size".into(),
                type_: "number".into(),
                file_path: declaration_path.clone(),
                line: 12,
                ..Default::default()
            }],
            ..Default::default()
        }],
    });
    let source = "<mt-button><template #iconFront=\"{ size: iconSize }\"><span :title=\"iconSize\"></span>{{ iconSize }}</template></mt-button>";
    let provider = fixture.provider();
    for (name, needle) in [
        ("binding alias", "size: icon"),
        ("vue attribute expression", ":title=\"icon"),
        ("twig interpolation", "{{ icon"),
    ] {
        let offset = index_of(source, needle) + needle.len() as u32;
        let probe = Probe {
            position: Some(offset),
            node: offset - 1,
            token: true,
            language: false,
        };
        let result = hover(
            &provider,
            &format!("{admin_root}/view.html.twig"),
            source,
            probe,
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        let value = &result.contents.value;
        assert_contains(value, "**slot prop** `iconSize`: `number`");
        assert_contains(value, "mt-button.iconFront");
        assert_contains(value, "Contract member: `size`");
        assert_contains(value, "MtButton.d.ts:12");
    }
}

fn slot_component(
    name: &str,
    file_path: String,
    slot: &str,
    member: Option<(&str, &str)>,
    slot_file: String,
    line: u32,
) -> VueComponent {
    let slot = match member {
        Some((member_name, member_type)) => VueComponentSlot {
            name: slot.into(),
            members: vec![VueComponentSlotMember {
                name: member_name.into(),
                type_: member_type.into(),
                file_path: slot_file,
                line,
                ..Default::default()
            }],
            ..Default::default()
        },
        None => VueComponentSlot {
            name: slot.into(),
            file_path: slot_file,
            line,
            ..Default::default()
        },
    };
    component! {
        name: name.into(),
        file_path: file_path,
        slots: vec![slot],
    }
}

#[test]
fn admin_scoped_slot_hover_uses_dynamic_component_payload_intersection() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    fixture.save(slot_component(
        "sw-card-a",
        format!("{admin_root}/a/index.ts"),
        "row",
        Some(("item", "Product")),
        format!("{admin_root}/a/card.twig"),
        4,
    ));
    fixture.save(slot_component(
        "sw-card-b",
        format!("{admin_root}/b/index.ts"),
        "row",
        Some(("item", "Category")),
        format!("{admin_root}/b/card.twig"),
        8,
    ));
    let provider = fixture.provider();
    for (name, source, needle) in [
        (
            "destructured local",
            "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #row=\"{ item: row }\">{{ row }}</template></component>",
            "{{ row",
        ),
        (
            "whole object member",
            "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #row=\"props\">{{ props.item }}</template></component>",
            "item",
        ),
    ] {
        let offset = index_of(source, needle) + needle.len() as u32;
        let probe = Probe {
            position: Some(offset),
            node: offset - 1,
            token: true,
            language: false,
        };
        let result = hover(
            &provider,
            &format!("{admin_root}/consumer.html.twig"),
            source,
            probe,
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        let value = &result.contents.value;
        assert_contains(value, "Product | Category");
        assert_contains(value, "`sw-card-a`, `sw-card-b`");
        assert_contains(value, "a/card.twig:4");
        assert_contains(value, "b/card.twig:8");
    }
}

#[test]
fn admin_dynamic_slot_family_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let template_path = format!("{admin_root}/grid/sw-grid.html.twig");
    fixture.save(component! {
        name: "sw-grid".into(),
        template_path: template_path.clone(),
        slots: vec![VueComponentSlot {
            name_prefix: "column-".into(),
            file_path: template_path.clone(),
            line: 27,
            members: vec![VueComponentSlotMember {
                name: "item".into(),
                type_: "Entity".into(),
                file_path: template_path.clone(),
                line: 28,
                ..Default::default()
            }],
            ..Default::default()
        }],
    });
    let source = "<sw-grid><template #column-name=\"{ item: row }\">{{ row }}</template></sw-grid>";
    let provider = fixture.provider();
    for (name, needle, contains) in [
        (
            "family slot",
            "column-name",
            vec![
                "**slot** `column-name`",
                "Dynamic slot family: `column-*`",
                "`item`: `Entity`",
                "sw-grid.html.twig:27",
            ],
        ),
        (
            "family local",
            "{{ row",
            vec![
                "**slot prop** `row`: `Entity`",
                "Dynamic slot family: `column-*`",
                "Contract member: `item`",
                "sw-grid.html.twig:28",
            ],
        ),
    ] {
        let offset = index_of(source, needle) + needle.len() as u32;
        let probe = Probe {
            position: Some(offset),
            node: offset - 1,
            token: true,
            language: false,
        };
        let result = hover(
            &provider,
            &format!("{admin_root}/consumer.html.twig"),
            source,
            probe,
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        for expected in contains {
            assert_contains(&result.contents.value, expected);
        }
    }
}

#[test]
fn admin_slot_hover_resolves_dynamic_component_owner() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.save(slot_component(
        "sw-card-a",
        format!("{admin_root}/a/index.ts"),
        "header",
        None,
        format!("{admin_root}/a/card.html.twig"),
        4,
    ));
    fixture.save(slot_component(
        "sw-card-b",
        format!("{admin_root}/b/index.ts"),
        "header",
        None,
        format!("{admin_root}/b/card.html.twig"),
        8,
    ));
    let source =
        "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #header /></component>";
    let offset = index_of(source, "header") + 2;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/consumer.html.twig"),
        source,
        Probe::at(offset),
    )
    .unwrap();
    assert_contains(&result.contents.value, "`sw-card-a`");
    assert_contains(&result.contents.value, "`sw-card-b`");
}

#[test]
fn admin_this_member_hover() {
    let fixture = Fixture::new();
    let definition_path =
        fixture.path("src/Resources/app/administration/src/component/sw-card/index.js");
    fixture.save(component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        members: vec![VueComponentMember {
            name: "cardClasses".into(),
            kind: VueComponentMemberKind::Computed,
            file_path: definition_path.clone(),
            line: 18,
            ..Default::default()
        }],
        computed: strings(&["cardClasses"]),
    });
    let source = "export default { methods: { render() { return this.cardClasses; } } };";
    let offset = index_of(source, "cardClasses") + 1;
    let result = hover(
        &fixture.provider(),
        &definition_path,
        source,
        Probe::unpositioned(offset),
    )
    .unwrap();
    assert_contains(&result.contents.value, "**computed** `cardClasses`");
    assert_contains(&result.contents.value, "sw-card");
}

#[test]
fn admin_runtime_registry_hover() {
    let fixture = Fixture::new();
    let registration_path =
        fixture.path("src/Administration/Resources/app/administration/src/main.ts");
    fixture.index_file(
        &registration_path,
        "
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('profile', {
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
    for (name, source, needle, contains) in [
        (
            "service",
            "Shopware.Service('acl')",
            "acl",
            vec!["Administration service", "`acl`", "main.ts:2"],
        ),
        (
            "store member",
            "Shopware.Store.get('profile').load()",
            "load",
            vec![
                "**action** `load`: `() => unknown`",
                "store `profile`",
                "main.ts:4",
            ],
        ),
        (
            "store unregister",
            "Shopware.Store.unregister('profile')",
            "profile",
            vec!["Administration store", "`profile`", "main.ts:3"],
        ),
        (
            "privilege role",
            "acl.can('product.viewer')",
            "product.viewer",
            vec![
                "Administration privilege role",
                "`product.viewer`",
                "main.ts:8",
            ],
        ),
        (
            "permission",
            "const item = { privilege: 'product:read' }",
            "product:read",
            vec![
                "Administration permission",
                "`product:read`",
                "product.viewer",
                "main.ts:8",
            ],
        ),
    ] {
        let offset = last_index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &document_path,
            source,
            Probe::unpositioned(offset),
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        for value in contains {
            assert_contains(&result.contents.value, value);
        }
    }
}

#[test]
fn admin_application_container_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/global.types.ts"),
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
    fixture.index_file(
        &format!("{admin_root}/services.ts"),
        "Shopware.Application.addServiceProvider('acl', factory);",
    );
    let provider = fixture.provider();
    let document_path = format!("{admin_root}/consumer.ts");
    let at = |source: &str, needle: &str| {
        let offset = last_index_of(source, needle) + 1;
        hover(
            &provider,
            &document_path,
            source,
            Probe::unpositioned(offset),
        )
    };

    let container = at("Application.getContainer('factory')", "factory").unwrap();
    assert_contains(&container.contents.value, "Application container");
    assert_contains(&container.contents.value, "FactoryContainer");

    let factory = at("Application.getContainer('factory').locale", "locale").unwrap();
    assert_contains(&factory.contents.value, "factory` container member");
    assert_contains(&factory.contents.value, "LocaleFactory");
    assert_contains(&factory.contents.value, "global.types.ts:4");

    let service = at(
        "function run() {
    const services = Application.getContainer('service');
    return services.acl;
}",
        "acl",
    )
    .unwrap();
    assert_contains(&service.contents.value, "Administration service");
    assert_contains(&service.contents.value, "Container type: `AclService`");
}

#[test]
fn admin_shopware_context_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/composables/use-context.ts"),
        "export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}",
    );
    let source = "const languageId = Shopware.Context.api.languageId;";
    let offset = last_index_of(source, "languageId") + 1;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/module/example/index.ts"),
        source,
        Probe::unpositioned(offset),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "Shopware.Context.api.languageId");
    assert_contains(value, "null | string");
    assert_contains(value, "use-context.ts:3");
}

#[test]
fn admin_shopware_utils_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/core/service/util.service.ts"),
        "export const format = { date };
export default { format };
function date(value: string): string { return value; }",
    );
    let source = "const { date: formatDate } = Shopware.Utils.format;
formatDate('2026-01-01');";
    let offset = last_index_of(source, "formatDate") + 1;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/module/example/index.ts"),
        source,
        Probe::unpositioned(offset),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "Shopware.Utils.format.date");
    assert_contains(value, "(value: string) => string");
    assert_contains(value, "util.service.ts:1");
}

#[test]
fn admin_shopware_event_bus_event_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/core/service/util.service.ts"),
        "
import EventBus from './utils/eventBus.utils';
export default { EventBus };
",
    );
    fixture.index_file(
        &format!("{admin_root}/core/service/utils/eventBus.utils.ts"),
        "
interface Events extends Record<string | symbol, unknown> {
    'language-change': { languageId: string };
}
const emitter = mitt<Events>();
export default emitter;
",
    );
    let source = "const bus = Shopware.Utils.EventBus;
bus.emit('language-change', { languageId });";
    let offset = last_index_of(source, "language-change") + 1;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/module/example/index.ts"),
        source,
        Probe::unpositioned(offset),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "Shopware EventBus event");
    assert_contains(value, "language-change");
    assert_contains(value, "{ languageId: string }");
    assert_contains(value, "eventBus.utils.ts:3");
}

#[test]
fn admin_component_mixin_and_module_registry_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/main.ts"),
        "
Shopware.Component.register('sw-card', { props: { title: String } });
Shopware.Mixin.register('listing', { methods: { load() {} } });
Shopware.Module.register('sw-product', {
    type: 'core',
    title: 'sw-product.general.mainMenuItemGeneral',
    routes: { index: { path: 'index', component: 'sw-card' } },
});",
    );
    let provider = fixture.provider();
    for (name, source, needle, contains) in [
        (
            "component registry",
            "Shopware.Component.getComponentRegistry().get('sw-card')",
            "sw-card",
            vec!["`sw-card`", "### Props", "`title`"],
        ),
        (
            "mixin",
            "Shopware.Mixin.getByName('listing')",
            "listing",
            vec!["Administration mixin", "`listing`", "Indexed members: 1"],
        ),
        (
            "module registry",
            "Module.getModuleRegistry().get('sw-product')",
            "sw-product",
            vec![
                "Administration module",
                "`sw-product`",
                "Type: `core`",
                "Indexed routes: 1",
            ],
        ),
    ] {
        let offset = index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &format!("{admin_root}/consumer.ts"),
            source,
            Probe::unpositioned(offset),
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        for expected in contains {
            assert_contains(&result.contents.value, expected);
        }
    }
}

#[test]
fn admin_directive_hover_from_javascript_and_twig() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/directive/tooltip.directive.ts"),
        "Shopware.Directive.register('tooltip', {});",
    );
    let provider = fixture.provider();
    for (source, needle, extension) in [
        ("Shopware.Directive.getByName('tooltip')", "tooltip", ".ts"),
        (
            "<div v-tooltip.bottom=\"message\"></div>",
            "tooltip",
            ".html.twig",
        ),
    ] {
        let offset = index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &format!("{admin_root}/consumer{extension}"),
            source,
            Probe::at(offset),
        )
        .unwrap_or_else(|| panic!("{extension}: no hover"));
        let value = &result.contents.value;
        assert_contains(value, "Administration Vue directive");
        assert_contains(value, "`v-tooltip`");
        assert_contains(value, "tooltip.directive.ts:1");
    }
}

#[test]
fn admin_filter_hover_shows_callable_contract() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/filter/currency.ts"),
        "Shopware.Filter.register('currency', (value: number): string => String(value));",
    );
    let source = "Shopware.Filter.getByName('currency')";
    let offset = index_of(source, "currency") + 1;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/consumer.ts"),
        source,
        Probe::at(offset),
    )
    .unwrap();
    assert_contains(&result.contents.value, "Administration filter");
    assert_contains(&result.contents.value, "(value: number) => string");
}

#[test]
fn admin_cms_registry_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let registration_path = format!("{admin_root}/cms/hero.ts");
    let registration_source = "Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', label: 'cms.hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});";
    fixture.index_file(&registration_path, registration_source);
    fixture.index_file(
        &format!("{admin_root}/component/sw-cms-el-hero/index.ts"),
        "Shopware.Component.register('sw-cms-el-hero', { props: ['config'] });",
    );
    let source = "cmsService.getCmsElementConfigByName('hero')";
    let offset = index_of(source, "hero") + 1;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/consumer.ts"),
        source,
        Probe::at(offset),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "Shopware CMS element");
    assert_contains(value, "sw-cms-el-hero");
    assert_contains(value, "sw-cms-el-config-hero");
    assert_contains(value, "sw-cms-el-preview-hero");

    let component_offset = index_of(registration_source, "sw-cms-el-hero") + 1;
    let component_hover = hover(
        &fixture.provider(),
        &registration_path,
        registration_source,
        Probe::at(component_offset),
    )
    .unwrap();
    assert_contains(&component_hover.contents.value, "sw-cms-el-hero");
    assert_contains(&component_hover.contents.value, "config");
}

#[test]
fn admin_directive_hover_identifies_template_local_declaration() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let local_path = format!("{admin_root}/component/sw-owner/index.ts");
    let template_path = format!("{admin_root}/component/sw-owner/sw-owner.html.twig");
    fixture.save(component! {
        name: "sw-owner".into(),
        file_path: local_path.clone(),
        definition_path: local_path.clone(),
        template_path: template_path.clone(),
        local_directives: vec![VueLocalDirective {
            name: "hide".into(),
            file_path: local_path.clone(),
            line: 7,
            ..Default::default()
        }],
    });
    let source = "<div v-hide=\"hidden\"></div>";
    let offset = index_of(source, "hide") + 1;
    let result = hover(
        &fixture.provider(),
        &template_path,
        source,
        Probe::at(offset),
    )
    .unwrap();
    assert_contains(&result.contents.value, "Component-local");
    assert_contains(&result.contents.value, "sw-owner/index.ts:7");
}

#[test]
fn admin_markup_event_and_slot_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Resources/app/administration/src");
    let definition_path = format!("{admin_root}/component/sw-card/index.js");
    let template_path = format!("{admin_root}/component/sw-card/sw-card.html.twig");
    fixture.save(component! {
        name: "sw-card".into(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        props: vec![VueComponentProp {
            file_path: definition_path.clone(),
            line: 5,
            ..prop("modelValue", "String")
        }],
        events: vec![VueComponentEvent {
            name: "update:modelValue".into(),
            type_: "(value: string) => void".into(),
            documentation: "Updates the public model value.".into(),
            file_path: definition_path.clone(),
            line: 9,
            ..Default::default()
        }],
        slots: vec![VueComponentSlot {
            name: "header".into(),
            file_path: template_path.clone(),
            line: 4,
            ..Default::default()
        }],
    });
    let provider = fixture.provider();
    for (name, source, needle, contains) in [
        (
            "event",
            "<sw-card @update:model-value.stop=\"onUpdate\" />",
            "update:model-value",
            vec![
                "**event** `update:model-value`",
                "value: string",
                "Updates the public model value.",
                "index.js:9",
            ],
        ),
        (
            "model",
            "<sw-card v-model.trim=\"title\" />",
            "v-model",
            vec![
                "**model binding** `v-model.trim`: `string`",
                "modelValue",
                "update:model-value",
                "index.js:5",
                "index.js:9",
            ],
        ),
        (
            "slot",
            "<sw-card><template #header></template></sw-card>",
            "header",
            vec!["**slot** `header`", "sw-card", "sw-card.html.twig:4"],
        ),
    ] {
        let offset = index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &template_path,
            source,
            Probe::unpositioned(offset),
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        for expected in contains {
            assert_contains(&result.contents.value, expected);
        }
    }
}

#[test]
fn admin_vue_for_and_event_lexical_hover() {
    let fixture = Fixture::new();
    fixture.save(component! {
        name: "mt-switch".into(),
        events: vec![
            VueComponentEvent {
                name: "update:modelValue".into(),
                type_: "(value: boolean) => any".into(),
                file_path: fixture.path("meteor/MtSwitch.d.ts"),
                line: 22,
                ..Default::default()
            },
            VueComponentEvent {
                name: "media-upload-add".into(),
                type_: "UploadTask[]".into(),
                file_path: fixture.path("sw-upload-listener/index.js"),
                line: 37,
                ..Default::default()
            },
        ],
    });
    let provider = fixture.provider();
    for (name, source, marker, contains) in [
        (
            "v-for local",
            "<div v-for=\"item in products\">{{ item.name }}</div>",
            "item.name",
            vec!["**v-for local** `item`", "Iterates `products`"],
        ),
        (
            "typed event payload",
            "<mt-switch @update:model-value=\"save($event)\" />",
            "$event",
            vec![
                "**event payload** `$event`: `boolean`",
                "mt-switch.update:model-value",
                "MtSwitch.d.ts:22",
            ],
        ),
        (
            "direct annotation payload",
            "<mt-switch @media-upload-add=\"consume($event)\" />",
            "$event",
            vec![
                "**event payload** `$event`: `UploadTask[]`",
                "mt-switch.media-upload-add",
                "index.js:37",
            ],
        ),
    ] {
        let offset = index_of(source, marker) + 1;
        let result = hover(
            &provider,
            &fixture.path("Resources/app/administration/view.html.twig"),
            source,
            Probe::at(offset),
        )
        .unwrap_or_else(|| panic!("{name}: no hover"));
        for expected in contains {
            assert_contains(&result.contents.value, expected);
        }
    }
}

#[test]
fn admin_vue_expression_member_hover() {
    let fixture = Fixture::new();
    let template_path = fixture.path("Resources/app/administration/src/view.html.twig");
    let definition_path = fixture.path("Resources/app/administration/src/index.ts");
    fixture.index_file(
        &definition_path,
        "
interface Manufacturer { name: string; }
interface Product { name: string; manufacturer: Manufacturer; getManufacturer(): Manufacturer; }
",
    );
    fixture.save(component! {
        name: "sw-view".into(),
        template_path: template_path.clone(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        props: vec![
            VueComponentProp {
                file_path: definition_path.clone(),
                ..prop("products", "Array as PropType<Product[]>")
            },
            VueComponentProp {
                file_path: definition_path.clone(),
                ..prop("productsById", "Record<string, Product>")
            },
            VueComponentProp {
                file_path: definition_path.clone(),
                ..prop("selectedProduct", "Product")
            },
        ],
    });
    let declaration_path = fixture.path("meteor/SwInheritWrapper.d.ts");
    fixture.save(component! {
        name: "sw-inherit-wrapper".into(),
        file_path: declaration_path.clone(),
        slots: vec![VueComponentSlot {
            name: "content".into(),
            file_path: declaration_path.clone(),
            members: vec![VueComponentSlotMember {
                name: "currentValue".into(),
                type_: "string".into(),
                file_path: declaration_path.clone(),
                line: 31,
                ..Default::default()
            }],
            ..Default::default()
        }],
    });
    let provider = fixture.provider();
    let typed_source = "<div v-for=\"product in products\">{{ product.name }}</div>";
    let typed_document = TextDocument::new(file_uri(&template_path), typed_source, 1);
    let typed_offset = index_of(typed_source, "product.name") + 1;
    let typed_binding = fixture
        .index
        .resolve_twig_vue_binding(
            &typed_document.root().unwrap(),
            typed_source,
            typed_offset,
            &template_path,
        )
        .unwrap()
        .unwrap();
    assert_eq!(typed_binding.type_, "Product");

    for (name, source, marker, contains) in [
        (
            "v-for observed member",
            "<div v-for=\"product in products\">{{ product.name }}</div>",
            "name",
            vec![
                "**property** `product.name`",
                "Binding type: `Product`",
                "Iterates `products`",
            ],
        ),
        (
            "v-for nested typed member",
            "<div v-for=\"product in products\">{{ product.manufacturer.name }}</div>",
            "name }}",
            vec![
                "**property** `product.manufacturer.name`: `string`",
                "Receiver type: `Manufacturer`",
            ],
        ),
        (
            "mapped primitive member",
            "<div v-for=\"productName in products?.map((product) => product.name) ?? []\">{{ productName.length }}</div>",
            "length",
            vec![
                "**property** `productName.length`: `number`",
                "Binding type: `string`",
            ],
        ),
        (
            "mapped projection member",
            "<div v-for=\"card in products.map((product) => ({ label: product.name }))\">{{ card.label }}</div>",
            "label }}",
            vec![
                "**property** `card.label`: `string`",
                "Binding type: `{ label: string }`",
            ],
        ),
        (
            "record value member",
            "<div v-for=\"(product, productId, index) in productsById\">{{ product.name }}</div>",
            "name }}",
            vec![
                "**property** `product.name`: `string`",
                "Binding type: `Product`",
            ],
        ),
        (
            "record key member",
            "<div v-for=\"(product, productId, index) in productsById\">{{ productId.length }}</div>",
            "length",
            vec![
                "**property** `productId.length`: `number`",
                "Binding type: `string`",
            ],
        ),
        (
            "record numeric index member",
            "<div v-for=\"(product, productId, index) in productsById\">{{ index.toFixed() }}</div>",
            "toFixed",
            vec![
                "**property** `index.toFixed()`: `(digits?: number) => string`",
                "Binding type: `number`",
            ],
        ),
        (
            "Object.values record member",
            "<div v-for=\"product in Object.values(productsById)\">{{ product.name }}</div>",
            "name }}",
            vec![
                "**property** `product.name`: `string`",
                "Binding type: `Product`",
            ],
        ),
        (
            "Object.keys result member",
            "<div v-for=\"productId in Object.keys(productsById)\">{{ productId.length }}</div>",
            "length",
            vec![
                "**property** `productId.length`: `number`",
                "Binding type: `string`",
            ],
        ),
        (
            "static literal iterable member",
            "<div v-for=\"label in ['primary', 'fallback']\">{{ label.length }}</div>",
            "length",
            vec![
                "**property** `label.length`: `number`",
                "Binding type: `string`",
            ],
        ),
        (
            "component nested typed member",
            "<div :title=\"selectedProduct.manufacturer.name\"></div>",
            "name\"",
            vec![
                "**property** `selectedProduct.manufacturer.name`: `string`",
                "Receiver type: `Manufacturer`",
                "**prop** `selectedProduct`",
                "component `sw-view`",
            ],
        ),
        (
            "component indexed array member",
            "<div :title=\"products[0].manufacturer.name\"></div>",
            "name\"",
            vec![
                "**property** `products[0].manufacturer.name`: `string`",
                "Receiver type: `Manufacturer`",
                "component `sw-view`",
            ],
        ),
        (
            "component v-for indexed array member",
            "<div v-for=\"(product, index) in products\">{{ products[index].manufacturer.name }}</div>",
            "name }}",
            vec![
                "**property** `products[index].manufacturer.name`: `string`",
                "Receiver type: `Manufacturer`",
                "component `sw-view`",
            ],
        ),
        (
            "component indexed Record member",
            "<div :title=\"productsById[selectedProduct.name].manufacturer.name\"></div>",
            "name\"",
            vec![
                "**property** `productsById[selectedProduct.name].manufacturer.name`: `string`",
                "Receiver type: `Manufacturer`",
                "component `sw-view`",
            ],
        ),
        (
            "component called typed member",
            "<div :title=\"selectedProduct.getManufacturer().name\"></div>",
            "name\"",
            vec![
                "**property** `selectedProduct.getManufacturer().name`: `string`",
                "Receiver type: `Manufacturer`",
                "component `sw-view`",
            ],
        ),
        (
            "whole slot object member",
            "<sw-inherit-wrapper><template #content=\"props\"><span :title=\"props.currentValue\"></span></template></sw-inherit-wrapper>",
            "currentValue",
            vec![
                "**slot prop** `props.currentValue`: `string`",
                "sw-inherit-wrapper.content",
            ],
        ),
    ] {
        let offset = index_of(source, marker) + 1;
        let result = hover(&provider, &template_path, source, Probe::at(offset))
            .unwrap_or_else(|| panic!("{name}: no hover"));
        for expected in contains {
            assert!(
                result.contents.value.contains(expected),
                "{name}: {expected:?} in {}",
                result.contents.value
            );
        }
    }
}

#[test]
fn admin_vue_expression_member_hover_uses_unsaved_type_import() {
    let fixture = Fixture::new();
    let component_dir =
        fixture.path("src/Administration/Resources/app/administration/src/component/sw-draft-card");
    let component_path = format!("{component_dir}/index.vue");
    fixture.index_file(
        &component_path,
        "<template>{{ title }}</template>
<script setup lang=\"ts\">
const { title } = defineProps<{ title: string }>();
</script>",
    );
    fixture.index_file(
        &format!("{component_dir}/index.ts"),
        "Shopware.Component.register('sw-draft-card', () => import('./index.vue'));",
    );
    fixture.index_file(
        &format!("{component_dir}/draft-contracts.ts"),
        "export interface DraftProfile { city: string; zip: string }
export interface DraftProps { profile: DraftProfile }",
    );
    fixture.index_file(
        &format!("{component_dir}/DraftPanel.vue"),
        "<template><slot /></template>
<script setup lang=\"ts\">
defineProps<{ label: string; count?: number }>();
</script>",
    );
    let provider = fixture.provider();

    let live_source = "<template>{{ profile.city }}</template>
<script setup lang=\"ts\">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>";
    let offset = index_of(live_source, "profile.city") + "profile.".len() as u32;
    let probe = Probe {
        language: true,
        ..Probe::at(offset)
    };
    let result = provider
        .get_hover(
            &Context::background(),
            &request(&component_path, live_source, 2, &probe),
        )
        .unwrap()
        .unwrap();
    assert_contains(&result.contents.value, "`profile.city`: `string`");
    assert_contains(&result.contents.value, "Receiver type: `DraftProfile`");

    let live_local_source = "<template><DraftPanel :label=\"title\" /></template>
<script setup lang=\"ts\">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>";
    let local_offset = index_of(live_local_source, ":label") + 2;
    let probe = Probe {
        language: true,
        ..Probe::at(local_offset)
    };
    let local_hover = provider
        .get_hover(
            &Context::background(),
            &request(&component_path, live_local_source, 3, &probe),
        )
        .unwrap()
        .unwrap();
    assert_contains(&local_hover.contents.value, "**prop** `label`: `string`");
    assert_contains(&local_hover.contents.value, "`draft-panel`");
}

#[test]
fn admin_twig_privilege_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/privileges.ts"),
        "Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});",
    );
    let source = "<mt-button :disabled=\"acl.can('product.viewer')\" />";
    let offset = index_of(source, "product.viewer") + 2;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/view.html.twig"),
        source,
        Probe::at(offset).without_token(),
    )
    .unwrap();
    assert_contains(&result.contents.value, "Administration privilege role");
    assert_contains(&result.contents.value, "`product.viewer`");
}

#[test]
fn admin_builtin_privilege_hover_has_no_fake_source() {
    let fixture = Fixture::new();
    let result = fixture
        .provider()
        .privilege_hover(swlsp_admin::ADMIN_PRIVILEGE_ADMINISTRATOR)
        .unwrap()
        .unwrap();
    assert_contains(&result.contents.value, "Built-in Administration privilege");
    assert_contains(&result.contents.value, "`admin`");
    assert!(!result.contents.value.contains("Defined in"));
}

#[test]
fn admin_twig_module_route_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/module/sw-product/index.ts"),
        "Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
    },
});",
    );
    let source = "<router-link :to=\"{ name: 'sw.product.detail' }\" />";
    let offset = index_of(source, "sw.product.detail") + 2;
    let result = hover(
        &fixture.provider(),
        &format!("{admin_root}/view.html.twig"),
        source,
        Probe::at(offset).without_token(),
    )
    .unwrap();
    let value = &result.contents.value;
    assert_contains(value, "Administration module route");
    assert_contains(value, "`sw.product.detail`");
    assert_contains(value, "`sw-product-detail`");
}

#[test]
fn admin_dynamic_component_selector_and_prop_hover() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    let template_path = format!("{admin_root}/consumer.html.twig");
    fixture.save(component! {
        name: "sw-card".into(),
        file_path: format!("{admin_root}/sw-card/index.ts"),
        props: vec![prop("title", "String")],
    });
    fixture.save(component! {
        name: "sw-panel".into(),
        file_path: format!("{admin_root}/sw-panel/index.ts"),
        props: vec![prop("title", "String")],
    });
    fixture.save(component! {
        name: "sw-host".into(),
        file_path: format!("{admin_root}/sw-host/index.ts"),
        template_path: template_path.clone(),
        members: vec![VueComponentMember {
            name: "dynamicCard".into(),
            kind: VueComponentMemberKind::Computed,
            return_expressions: strings(&["'sw-card'", "'sw-panel'"]),
            returns_complete: true,
            ..Default::default()
        }],
    });
    let provider = fixture.provider();
    let source = "<component :is=\"'sw-card'\" :title=\"title\" />";
    for (needle, expected) in [
        ("sw-card", "Vue dynamic component selector"),
        (":title", "**prop** `title`"),
    ] {
        let offset = index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &template_path,
            source,
            Probe::at(offset).without_token(),
        )
        .unwrap_or_else(|| panic!("{needle}: no hover"));
        assert_contains(&result.contents.value, expected);
    }

    let both = |source: &str, needle: &str| {
        let offset = index_of(source, needle) + 1;
        let result = hover(
            &provider,
            &template_path,
            source,
            Probe::at(offset).without_token(),
        )
        .unwrap_or_else(|| panic!("{source}: no hover"));
        assert_contains(&result.contents.value, "Component: `sw-card`");
        assert_contains(&result.contents.value, "Component: `sw-panel`");
    };
    both(
        "<component :is=\"active ? 'sw-card' : 'sw-panel'\" :title=\"title\" />",
        ":title",
    );
    both(
        "<component :is=\"dynamicCard\" v-bind=\"{ title: heading }\" />",
        "title",
    );

    fixture.index_file(
        &format!("{admin_root}/module/sw-host/routes.js"),
        "Shopware.Module.register('sw-host', {
    routes: {
        index: {
            component: 'sw-host',
            children: {
                card: { component: 'sw-card' },
                panel: { component: 'sw-panel' },
            },
        },
    },
});",
    );
    both(
        "<router-view v-slot=\"{ Component: view }\"><component :is=\"view\" :title=\"heading\" /></router-view>",
        ":title",
    );
}

#[test]
fn admin_hover_preserves_section_order_and_location_precedence() {
    let fixture = Fixture::new();
    let root = fixture.root.clone();
    let provider = AdminHoverProvider::new(root.clone(), fixture.index.clone());
    let content = provider.build_hover_content(&[
        component! {
            name: "sw-card".into(),
            deprecated: "Use another card.".into(),
            extends_component: "sw-base".into(),
            props: vec![VueComponentProp {
                required: true,
                deprecated: "Use label.".into(),
                default: "Untitled".into(),
                ..prop("title", "String")
            }],
            events: vec![VueComponentEvent {
                name: "save".into(),
                type_: "(id: string) => void".into(),
                ..Default::default()
            }],
            methods: strings(&["save"]),
            computed: strings(&["label"]),
            data: strings(&["open"]),
            injected: strings(&["repositoryFactory"]),
            slots: vec![VueComponentSlot {
                name: "default".into(),
                ..Default::default()
            }],
            definition_path: format!("{root}/definition.js"),
            file_path: format!("{root}/registration.js"),
        },
        component! {
            name: "sw-other".into(),
            file_path: format!("{root}/other.js"),
        },
    ]);
    let expected = "## `sw-card`\n\n".to_owned()
        + "**Deprecated:** Use another card.\n\n"
        + "**Extends**: `sw-base`\n\n"
        + "### Props\n\n- `title`: **String** *(required)* *(deprecated)* = `Untitled`\n\n"
        + "### Events\n\n- `save`: `(id: string) => void`\n\n"
        + "### Methods\n\n- `save()`\n\n"
        + "### Computed\n\n- `label`\n\n"
        + "### Data\n\n- `open`\n\n"
        + "### Injected services\n\n- `repositoryFactory`\n\n"
        + "### Slots\n\n- `default`\n\n"
        + "*Defined in*: `definition.js`\n\n---\n\n"
        + "## `sw-other`\n\n*Registered in*: `other.js`\n";
    assert_eq!(content, expected);
    assert!(provider.build_hover_content(&[]).is_empty());
    assert_eq!(
        provider.build_hover_content(&[component! {
            name: "empty".into(),
        }]),
        "## `empty`\n\n"
    );
}
