use super::*;
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;
use swlsp_admin::{
    AdminComponentIndexer, ComponentDefinition, VueComponent, VueComponentMember,
    VueComponentMemberKind, VueComponentProp, VueComponentSlot, VueComponentSlotMember,
    VueLocalDirective,
};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, SemanticToken, SemanticTokensProvider, SemanticTokensRequest, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_CLASS as CLASS, SEMANTIC_TOKEN_FUNCTION as FUNCTION,
    SEMANTIC_TOKEN_PROPERTY as PROPERTY, SEMANTIC_TOKEN_VARIABLE as VARIABLE,
};
use swlsp_uriutil::file_uri;

fn request(uri: &str, source: &str, version: i32) -> SemanticTokensRequest {
    SemanticTokensRequest {
        params: protocol::SemanticTokensParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, source, version)),
    }
}

fn tokens(
    provider: &dyn SemanticTokensProvider,
    uri: &str,
    source: &str,
    version: i32,
) -> Vec<SemanticToken> {
    provider
        .get_semantic_tokens(&Context::background(), &request(uri, source, version))
        .expect("semantic tokens")
}

fn text<'a>(source: &'a str, token: &SemanticToken) -> &'a str {
    &source[token.range]
}

fn assert_tokens(source: &str, actual: &[SemanticToken], expected: &[(&str, u32)]) {
    let actual: Vec<(&str, u32)> = actual
        .iter()
        .map(|token| (text(source, token), token.token_type))
        .collect();
    assert_eq!(actual, expected);
}

fn path_string(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

// --- Administration markup --------------------------------------------------

struct Admin {
    root: tempfile::TempDir,
    index: Arc<AdminComponentIndexer>,
}

impl Drop for Admin {
    fn drop(&mut self) {
        self.index.close().expect("close admin index");
    }
}

impl Admin {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        let cache = path_string(&root.path().join("cache"));
        let index = Arc::new(AdminComponentIndexer::new(&cache, None).expect("admin index"));
        Self { root, index }
    }

    fn path(&self, relative: &str) -> String {
        path_string(&self.root.path().join(relative))
    }

    fn admin_path(&self, relative: &str) -> String {
        self.path(&format!(
            "src/Administration/Resources/app/administration/src/{relative}"
        ))
    }

    fn index_file(&self, path: &str, source: &str) {
        self.index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .expect("index file");
    }

    fn save(&self, component: VueComponent) {
        self.index
            .save_component(&component)
            .expect("save component");
    }

    fn tokens(&self, path: &str, source: &str, version: i32) -> Vec<SemanticToken> {
        tokens(
            &AdminMarkupProvider::new(self.index.clone()),
            &file_uri(path),
            source,
            version,
        )
    }
}

/// `VueComponent` has private fields, so struct-update syntax is unavailable.
macro_rules! vue {
    ($($field:ident: $value:expr),* $(,)?) => {{
        let mut component = VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}

fn prop(name: &str) -> VueComponentProp {
    VueComponentProp {
        name: name.to_owned(),
        type_: "String".to_owned(),
        ..Default::default()
    }
}

fn slot(name: &str) -> VueComponentSlot {
    VueComponentSlot {
        name: name.to_owned(),
        ..Default::default()
    }
}

fn slot_member(name: &str) -> VueComponentSlotMember {
    VueComponentSlotMember {
        name: name.to_owned(),
        ..Default::default()
    }
}

fn member(name: &str, kind: VueComponentMemberKind, type_: &str) -> VueComponentMember {
    VueComponentMember {
        name: name.to_owned(),
        kind,
        type_: type_.to_owned(),
        ..Default::default()
    }
}

#[test]
fn admin_markup_semantic_tokens_color_components_props_events_and_slots() {
    let admin = Admin::new();
    admin.index_file(
        &admin.admin_path("component/sw-card/index.ts"),
        "Shopware.Directive.register('tooltip', {});
		Shopware.Component.register('sw-card', {
			props: { title: String, modelValue: String }, emits: ['update:modelValue'],
		});",
    );
    let component = admin
        .index
        .get_effective_component("sw-card")
        .unwrap()
        .expect("sw-card");
    assert!(component.emits.contains(&"update:modelValue".to_owned()));
    admin
        .index
        .save_component_definition(
            "sw-card",
            &ComponentDefinition {
                file_path: component.file_path.clone(),
                props: component.props.clone(),
                emits: component.emits.clone(),
                events: component.events.clone(),
                slots: vec![
                    slot("footer"),
                    VueComponentSlot {
                        name_prefix: "column-".to_owned(),
                        ..Default::default()
                    },
                ],
                ..Default::default()
            },
        )
        .unwrap();
    let source = r##"<sw-card v-tooltip.bottom="tip" :title.sync="title" v-model.trim="value" @update:model-value.stop="save" class="plain" v-if="visible" @click="click">
	<template #footer>Content</template><template #column-name>Column</template><template #missing>Ignored</template>
</sw-card>
<sw-unknown title="ignored" />
<div class="plain" v-hide="hidden"></div>"##;
    let template_path = admin.admin_path("component.html.twig");
    admin.save(vue! {
    name: "sw-owner".to_owned(),
    file_path: admin.admin_path("owner.ts"),
    template_path: template_path.clone(),
    local_directives: vec![VueLocalDirective {
        name: "hide".to_owned(),
        ..Default::default()
    }],    });
    let actual = admin.tokens(&template_path, source, 1);
    assert_tokens(
        source,
        &actual,
        &[
            ("sw-card", CLASS),
            ("v-tooltip.bottom", FUNCTION),
            (":title.sync", PROPERTY),
            ("v-model.trim", PROPERTY),
            ("@update:model-value.stop", FUNCTION),
            ("#footer", PROPERTY),
            ("#column-name", PROPERTY),
            ("sw-card", CLASS),
            ("v-hide", FUNCTION),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_resolve_template_scoped_component() {
    let admin = Admin::new();
    let definition_path = admin.admin_path("component/sw-wrapper/index.ts");
    let template_path = admin.admin_path("component/sw-wrapper/sw-wrapper.html.twig");
    admin.index_file(
        &definition_path,
        "
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
    props: { title: String },
});
",
    );
    admin.save(vue! {
    name: "mt-card".to_owned(),
    file_path: admin.path("meteor/MtCard.d.ts"),
    props: vec![prop("title")],    });
    let source = r#"<mt-card-original title="Card"></mt-card-original><mt-not-local />"#;
    let actual = admin.tokens(&template_path, source, 1);
    assert_tokens(
        source,
        &actual,
        &[
            ("mt-card-original", CLASS),
            ("title", PROPERTY),
            ("mt-card-original", CLASS),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_use_unsaved_local_vue_component() {
    let admin = Admin::new();
    let component_dir =
        "src/Administration/Resources/app/administration/src/component/sw-live-owner";
    let owner_path = admin.path(&format!("{component_dir}/index.vue"));
    admin.index_file(
        &owner_path,
        r#"<template><div /></template><script setup lang="ts"></script>"#,
    );
    admin.index_file(
        &admin.path(&format!("{component_dir}/DraftPanel.vue")),
        r#"<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string }>();
defineSlots<{ default(props: { item: string }): unknown }>();
</script>"#,
    );
    let source = r#"<template><DraftPanel :label="title"><template #default="props">{{ props.item }}</template></DraftPanel></template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>"#;
    let actual = admin.tokens(&owner_path, source, 2);
    let seen: HashMap<&str, u32> = actual
        .iter()
        .map(|token| (text(source, token), token.token_type))
        .collect();
    assert_eq!(seen.get("DraftPanel"), Some(&CLASS));
    assert_eq!(seen.get(":label"), Some(&PROPERTY));
    assert_eq!(seen.get("#default"), Some(&PROPERTY));
    assert_eq!(seen.get("item"), Some(&PROPERTY));
}

#[test]
fn admin_markup_semantic_tokens_resolve_dynamic_component_contract() {
    let admin = Admin::new();
    let template_path = admin.admin_path("consumer.html.twig");
    admin.save(vue! {
    name: "sw-card".to_owned(),
    file_path: admin.admin_path("sw-card/index.ts"),
    props: vec![prop("title")],    });
    admin.save(vue! {
    name: "sw-host".to_owned(),
    file_path: admin.admin_path("sw-host/index.ts"),
    template_path: template_path.clone(),
    members: vec![
        VueComponentMember {
            return_expressions: vec!["'sw-card'".to_owned()],
            returns_complete: true,
            ..member("dynamicCard", VueComponentMemberKind::Computed, "")
        },
        member("heading", VueComponentMemberKind::Data, "string"),
    ],    });
    let source = r#"<component :is="'sw-card'" :title="title" />"#;
    let actual = admin.tokens(&template_path, source, 1);
    assert_tokens(source, &actual, &[("sw-card", CLASS), (":title", PROPERTY)]);

    let inferred = r#"<component :is="dynamicCard" v-bind="{ title: heading }" />"#;
    let actual = admin.tokens(&template_path, inferred, 1);
    assert_tokens(
        inferred,
        &actual,
        &[
            ("dynamicCard", VARIABLE),
            ("title", PROPERTY),
            ("heading", VARIABLE),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_use_finite_dynamic_contract_intersection() {
    let admin = Admin::new();
    admin.save(vue! {
    name: "sw-card".to_owned(),
    file_path: admin.admin_path("sw-card/index.ts"),
    props: vec![prop("title"), prop("cardOnly")],    });
    admin.save(vue! {
    name: "sw-panel".to_owned(),
    file_path: admin.admin_path("sw-panel/index.ts"),
    props: vec![prop("title")],    });
    let source =
        r#"<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" :card-only="value" />"#;
    let actual = admin.tokens(&admin.admin_path("consumer.html.twig"), source, 1);
    assert_tokens(
        source,
        &actual,
        &[
            ("sw-card", CLASS),
            ("sw-panel", CLASS),
            (":title", PROPERTY),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_use_dynamic_owner_slot_intersection() {
    let admin = Admin::new();
    admin.save(vue! {
    name: "sw-card-a".to_owned(),
    file_path: admin.admin_path("a/index.ts"),
    slots: vec![slot("header"), slot("a-only")],    });
    admin.save(vue! {
    name: "sw-card-b".to_owned(),
    file_path: admin.admin_path("b/index.ts"),
    slots: vec![slot("header")],    });
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /><template #a-only /></component>"#;
    let actual = admin.tokens(&admin.admin_path("consumer.html.twig"), source, 1);
    let texts: Vec<&str> = actual.iter().map(|token| text(source, token)).collect();
    assert!(texts.contains(&"#header"), "{texts:?}");
    assert!(!texts.contains(&"#a-only"), "{texts:?}");
}

#[test]
fn admin_markup_semantic_tokens_color_dynamic_scoped_slot_payload() {
    let admin = Admin::new();
    let template_path = admin.admin_path("consumer.html.twig");
    for (name, file) in [("sw-card-a", "a/index.ts"), ("sw-card-b", "b/index.ts")] {
        admin.save(vue! {
        name: name.to_owned(),
        file_path: admin.admin_path(file),
        slots: vec![VueComponentSlot {
            members: vec![slot_member("item")],
            ..slot("row")
        }],        });
    }
    admin.save(vue! {
    name: "sw-host".to_owned(),
    file_path: admin.admin_path("host/index.ts"),
    template_path: template_path.clone(),    });
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }}</template></component>"#;
    let actual = admin.tokens(&template_path, source, 1);
    let mut types: HashMap<&str, Vec<u32>> = HashMap::new();
    for token in &actual {
        types
            .entry(text(source, token))
            .or_default()
            .push(token.token_type);
    }
    assert!(
        types
            .get("props")
            .is_some_and(|types| types.contains(&VARIABLE))
    );
    assert!(
        types
            .get("item")
            .is_some_and(|types| types.contains(&PROPERTY))
    );
}

#[test]
fn admin_markup_semantic_tokens_ignore_storefront_twig() {
    let admin = Admin::new();
    let actual = admin.tokens(&admin.path("templates/card.html.twig"), "<sw-card />", 1);
    assert!(actual.is_empty());
}

#[test]
fn admin_markup_semantic_tokens_color_vue_lexical_bindings() {
    let admin = Admin::new();
    let source = r#"<div v-for="(item, index) in items"><button @click="save(item, $event)">{{ item.name }} {{ item.children[0].name }} {{ index }}</button></div>"#;
    let actual = admin.tokens(
        &admin.path("Resources/app/administration/view.html.twig"),
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("item", VARIABLE),
            ("index", VARIABLE),
            ("item", VARIABLE),
            ("$event", VARIABLE),
            ("item", VARIABLE),
            ("name", PROPERTY),
            ("item", VARIABLE),
            ("children", PROPERTY),
            ("name", PROPERTY),
            ("index", VARIABLE),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_color_component_setup_members() {
    let admin = Admin::new();
    let template_path =
        admin.path("Resources/app/administration/src/component/sw-setup/view.html.twig");
    admin.save(vue! {
    name: "sw-setup".to_owned(),
    file_path: admin.path("Resources/app/administration/src/component/sw-setup/index.ts"),
    template_path: template_path.clone(),
    members: vec![
        member("count", VueComponentMemberKind::Data, "number"),
        member("detail", VueComponentMemberKind::Data, "{ name: string }"),
        member(
            "save",
            VueComponentMemberKind::Method,
            "(count: number) => void",
        ),
    ],    });
    let source = r#"<button @click="save(count)">{{ count }} {{ detail.name }}</button>"#;
    let actual = admin.tokens(&template_path, source, 1);
    assert_tokens(
        source,
        &actual,
        &[
            ("save", FUNCTION),
            ("count", VARIABLE),
            ("count", VARIABLE),
            ("detail", VARIABLE),
            ("name", PROPERTY),
        ],
    );
}

#[test]
fn admin_markup_semantic_tokens_color_whole_slot_object_members() {
    let admin = Admin::new();
    admin.save(vue! {
    name: "sw-inherit-wrapper".to_owned(),
    file_path: admin.path("index.js"),
    slots: vec![VueComponentSlot {
        members: vec![VueComponentSlotMember {
            type_: "string".to_owned(),
            ..slot_member("currentValue")
        }],
        ..slot("content")
    }],    });
    let source = r#"<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>"#;
    let actual = admin.tokens(
        &admin.path("Resources/app/administration/view.html.twig"),
        source,
        1,
    );
    let matches: Vec<u32> = actual
        .iter()
        .filter(|token| text(source, token) == "currentValue")
        .map(|token| token.token_type)
        .collect();
    assert!(!matches.is_empty());
    assert!(matches.iter().all(|token_type| *token_type == PROPERTY));
}
