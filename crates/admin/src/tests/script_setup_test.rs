#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn component_definition_member<'a>(
    definition: &'a ComponentDefinition,
    name: &str,
) -> Option<&'a VueComponentMember> {
    definition.members.iter().find(|member| member.name == name)
}

fn admin_src(root: &std::path::Path) -> std::path::PathBuf {
    root.join("src/Administration/Resources/app/administration/src")
}

#[test]
fn parse_script_setup_definition_test() {
    let source = r#"<template><LocalPanel :title="title" @save="save" /></template>
<script setup lang="ts">
import LocalPanel from './LocalPanel.vue';

interface Props {
    /** Card heading shown above the content. */
    title: string;
    count?: number;
}

const props = withDefaults(defineProps<Props>(), { count: 1 });
const emit = defineEmits<{
    /** Persists the selected record. */
    save: [id: string];
    /** Closes the panel. */
    (event: 'close'): void;
}>();
const value = defineModel<string>();
const doubled = computed(() => props.count * 2);
function save() { emit('save', '1'); }
</script>"#;
    let parsed = swlsp_parser_vue::parse_sfc(source);
    assert_eq!(parsed.sections.len(), 2);
    let definition = parse_script_setup_definition(
        &parsed.parse.root(),
        source,
        "/project/Resources/app/administration/src/LocalPage.vue",
        &LineIndex::new(source),
        parsed.sections[1].body_range,
    )
    .expect("definition");
    assert_eq!(definition.script_setup_prop_types, vec!["Props"]);
    assert_eq!(definition.script_setup_prop_defaults.len(), 1);
    assert_eq!(definition.script_setup_prop_defaults[0].name, "count");
    assert_eq!(definition.script_setup_prop_defaults[0].value, "1");
    assert_eq!(definition.script_setup_event_types.len(), 1);

    let props: HashMap<String, VueComponentProp> = definition
        .props
        .iter()
        .map(|p| (p.name.clone(), p.clone()))
        .collect();
    assert!(props["title"].required);
    assert_eq!(props["title"].type_, "string");
    assert_eq!(
        props["title"].documentation,
        "Card heading shown above the content."
    );
    assert!(!props["count"].required);
    assert_eq!(props["count"].default, "1");
    assert_eq!(props["modelValue"].type_, "string");

    assert!(definition.emits.contains(&"save".to_owned()));
    assert!(definition.emits.contains(&"close".to_owned()));
    let save_event = component_definition_event(&definition.events, "save").expect("save");
    assert_eq!(save_event.documentation, "Persists the selected record.");
    let close_event = component_definition_event(&definition.events, "close").expect("close");
    assert_eq!(close_event.documentation, "Closes the panel.");
    assert_eq!(close_event.type_, "(event: 'close')");
    assert!(definition.emits.contains(&"update:model-value".to_owned()));
    assert_eq!(definition.model_prop, "modelValue");
    assert_eq!(definition.model_event, "update:model-value");

    assert_eq!(definition.local_components.len(), 1);
    assert_eq!(definition.local_components[0].name, "local-panel");
    assert_eq!(
        definition.local_components[0].import_path,
        "./LocalPanel.vue"
    );
    for name in [
        "title",
        "count",
        "modelValue",
        "props",
        "emit",
        "value",
        "doubled",
        "save",
        "LocalPanel",
    ] {
        assert!(
            component_definition_member(&definition, name).is_some(),
            "expected member {name}"
        );
    }
}

#[test]
fn parse_script_setup_runtime_contracts() {
    let source = "<template><button>{{ label }}</button></template>
<script setup>
const props = defineProps({
    label: { type: String, required: true },
});
const emit = defineEmits(['submit']);
const checked = defineModel('checked', { type: Boolean, default: false });
</script>";
    let parsed = swlsp_parser_vue::parse_sfc(source);
    assert_eq!(parsed.sections.len(), 2);
    let definition = parse_script_setup_definition(
        &parsed.parse.root(),
        source,
        "/project/Resources/app/administration/src/Toggle.vue",
        &LineIndex::new(source),
        parsed.sections[1].body_range,
    )
    .expect("definition");
    let props: HashMap<String, VueComponentProp> = definition
        .props
        .iter()
        .map(|p| (p.name.clone(), p.clone()))
        .collect();
    assert!(props["label"].required);
    assert_eq!(props["label"].type_, "String");
    assert_eq!(props["checked"].type_, "Boolean");
    assert_eq!(props["checked"].default, "false");
    assert!(definition.emits.contains(&"submit".to_owned()));
    assert!(definition.emits.contains(&"update:checked".to_owned()));
}

#[test]
fn admin_indexer_indexes_vue_sfc_contract_and_markup() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let admin_root = admin_src(root.path());
    let component_path = path_string(&admin_root, "component/sw-sfc-card/index.vue");
    let registration_path = path_string(&admin_root, "component/sw-sfc-card/index.ts");
    let source = r#"<template>
    <sw-button>{{ title }}</sw-button>
    <slot name="actions" :item="title" />
</template>
<script setup lang="ts">
interface Props { title: string; count?: number }
const props = defineProps<Props>();
const submit = () => props.title;
</script>"#;
    index_source(&index, &component_path, source);
    index_source(
        &index,
        &registration_path,
        "Shopware.Component.register('sw-sfc-card', () => import('./index.vue'));",
    );

    let definition = index
        .get_component_definition(&component_path)
        .unwrap()
        .expect("definition");
    assert_eq!(definition.template_path, component_path);
    assert!(definition.has_template);
    assert_eq!(definition.slots.len(), 1);
    assert_eq!(definition.slots[0].name, "actions");
    assert_eq!(definition.slots[0].file_path, component_path);
    assert!(definition.slots[0].name_range.declaration);
    assert!(!definition.slots[0].name_range.identifier);
    let slot_range = &definition.slots[0].name_range;
    let slot_line = source
        .split('\n')
        .nth(slot_range.start_line as usize)
        .unwrap();
    assert_eq!(
        &slot_line[slot_range.start_character as usize..slot_range.end_character as usize],
        "actions"
    );

    let component = index
        .get_effective_component("sw-sfc-card")
        .unwrap()
        .expect("component");
    assert_eq!(component.definition_path, component_path);
    let prop = component.component_prop("title").expect("title");
    assert!(prop.required);
    assert_eq!(prop.type_, "string");
    assert!(component.template_member("submit").is_some());

    let usages = index
        .get_usages(AdminSymbolKind::Component, "", "sw-button")
        .unwrap();
    assert_eq!(usages.len(), 1);
    assert_eq!(usages[0].file_path, component_path);

    let type_file = index
        .admin_type_file(&component_path)
        .unwrap()
        .expect("type file");
    assert_eq!(type_file.declarations.len(), 1);
    assert_eq!(type_file.declarations[0].name, "Props");
}

#[test]
fn admin_indexer_indexes_vue_sfc_options_api() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let component_path = path_string(&admin_src(root.path()), "component/sw-options/index.vue");
    let source = "<template><slot name=\"footer\" /></template>
<script lang=\"ts\">
export default defineComponent({
    props: { title: { type: String, required: true } },
    emits: ['save'],
    methods: { submit() { this.$emit('save'); } },
});
</script>";
    index_source(&index, &component_path, source);
    let definition = index
        .get_component_definition(&component_path)
        .unwrap()
        .expect("definition");
    assert_eq!(definition.props.len(), 1);
    assert_eq!(definition.props[0].name, "title");
    assert!(definition.props[0].required);
    assert!(definition.emits.contains(&"save".to_owned()));
    assert!(definition.methods.contains(&"submit".to_owned()));
    assert_eq!(definition.slots.len(), 1);
    assert_eq!(definition.slots[0].name, "footer");
}

#[test]
fn admin_indexer_resolves_imported_script_setup_contracts_lazily() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let admin_root = admin_src(root.path());
    let component_path = path_string(&admin_root, "component/sw-imported-card/index.vue");
    let registration_path = path_string(&admin_root, "component/sw-imported-card/index.ts");
    let types_path = path_string(&admin_root, "component/sw-imported-card/contracts.ts");
    let live_types_path = path_string(&admin_root, "component/sw-imported-card/draft-contracts.ts");
    let component_source = "<template>
    <p>interface Bogus { ignored: string }</p>
\t<slot :title=\"heading\" />
\t<slot name=\"header\" />
</template>
<script setup lang=\"ts\">
import type { CardProps, CardEvents, CardSlots } from './contracts';
const { title: heading, count = 1, mode: displayMode } = defineProps<CardProps>();
const emit = defineEmits<CardEvents>();
defineSlots<CardSlots>();
</script>";

    // Index the dependent SFC first: contract correctness must not depend
    // on file ordering.
    index_source(&index, &component_path, component_source);
    index_source(
        &index,
        &registration_path,
        "Shopware.Component.register('sw-imported-card', () => import('./index.vue'));",
    );
    let index_types = |extra: &str| {
        let source = format!(
            "export interface BaseProps {{
    /** Heading displayed above the imported card. */
    title: string;
    count?: number;
{extra}}}
export interface VariantProps {{ mode: 'small' | 'large'; ignored: string }}
export interface Flags {{ enabled: boolean; internal: string; requiredFlag?: boolean }}
export type CardProps = Partial<BaseProps> & Pick<VariantProps, 'mode'> & Omit<Flags, 'internal' | 'requiredFlag'> & Required<Pick<Flags, 'requiredFlag'>>;
export interface CardEvents {{
    /** Persists the selected record. */
    save: [id: string];
    /** Closes the card with an optional reason. */
    (event: 'close', reason: string): void;
}}

export interface HeaderPayload {{ item: string; selected?: boolean }}
export interface CardSlots {{
    default(props: {{ title: string }}): unknown;
    header(props: HeaderPayload): unknown;
    footer(): unknown;
}}
"
        );
        index_source(&index, &types_path, &source);
    };
    index_types("");
    index_source(
        &index,
        &live_types_path,
        "export interface DraftProfile { city: string; zip: string }
export interface DraftRow { label: string; active: boolean }
export interface LiveProps { profile: DraftProfile; rows: DraftRow[] }
",
    );

    let definition = index
        .get_component_definition(&component_path)
        .unwrap()
        .expect("definition");
    assert_eq!(definition.script_setup_prop_bindings.len(), 3);
    let props: HashMap<String, VueComponentProp> = definition
        .props
        .iter()
        .map(|p| (p.name.clone(), p.clone()))
        .collect();
    assert!(!props["title"].required);
    assert_eq!(props["title"].type_, "string");
    assert_eq!(
        props["title"].documentation,
        "Heading displayed above the imported card."
    );
    assert_eq!(props["title"].file_path, types_path);
    assert_ne!(props["title"].name_range, AdminSourceRange::default());
    assert!(props["title"].name_range.declaration);
    assert!(!props["count"].required);
    assert_eq!(props["count"].default, "1");
    assert!(props["mode"].required);
    assert_eq!(props["mode"].type_, "'small' | 'large'");
    assert!(props["enabled"].required);
    assert!(props["requiredFlag"].required);
    assert!(!props.contains_key("ignored"));
    assert!(!props.contains_key("internal"));
    assert!(definition.emits.contains(&"save".to_owned()));
    assert!(definition.emits.contains(&"close".to_owned()));
    let resolved_events: HashMap<String, VueComponentEvent> = definition
        .events
        .iter()
        .map(|event| (canonical_event_name(&event.name), event.clone()))
        .collect();
    for event_name in ["save", "close"] {
        let event = resolved_events.get(event_name).expect(event_name);
        assert_eq!(event.file_path, types_path, "{event_name}");
        assert!(event.name_range.declaration, "{event_name}");
        assert!(
            event.name_range.end_character > event.name_range.start_character,
            "{event_name}"
        );
    }
    assert_eq!(
        resolved_events["save"].documentation,
        "Persists the selected record."
    );
    assert_eq!(
        resolved_events["close"].documentation,
        "Closes the card with an optional reason."
    );
    assert!(component_definition_member(&definition, "title").is_some());
    let heading = component_definition_member(&definition, "heading").expect("heading");
    assert_eq!(heading.type_, "string");
    assert_eq!(heading.file_path, component_path);
    assert_ne!(heading.name_range, AdminSourceRange::default());
    let display_mode =
        component_definition_member(&definition, "displayMode").expect("displayMode");
    assert_eq!(display_mode.type_, "'small' | 'large'");

    let live_source =
        component_source
            .replace("heading", "headline")
            .replacen("count = 1", "count = 2", 1);
    let live_parsed = swlsp_parser_vue::parse_sfc(&live_source);
    let live_root = live_parsed.parse.root();
    let live_component = index
        .get_component_for_document(
            &component_path,
            Some(&live_root),
            &live_source,
            Some(&LineIndex::new(live_source.as_str())),
        )
        .unwrap()
        .expect("live component");
    assert!(live_component.template_member("headline").is_some());
    assert!(live_component.template_member("heading").is_none());
    let live_count = live_component.component_prop("count").expect("count");
    assert_eq!(live_count.default, "2");
    let persisted_component = index
        .get_effective_component("sw-imported-card")
        .unwrap()
        .expect("persisted");
    assert!(
        persisted_component.template_member("heading").is_some(),
        "request-local overlay must not mutate the index"
    );
    assert!(persisted_component.template_member("headline").is_none());

    // A newly added import and a local declaration participate in
    // request-local type resolution before the Vue document is reindexed.
    let live_imported_source = "<template>
    <p>{{ profile.city }}</p>
    <p>{{ localState.flag }}</p>
    <p v-for=\"row in rows\">{{ row.label }}</p>
</template>
<script setup lang=\"ts\">
import type { LiveProps } from './draft-contracts';
interface LocalDraft { localState: { flag: boolean; note: string } }
const { profile, rows } = defineProps<LiveProps>();
const { localState } = defineProps<LocalDraft>();
</script>";
    let live_imported_parsed = swlsp_parser_vue::parse_sfc(live_imported_source);
    let live_imported_root = live_imported_parsed.parse.root();
    let live_imported = index
        .get_component_for_document(
            &component_path,
            Some(&live_imported_root),
            live_imported_source,
            Some(&LineIndex::new(live_imported_source)),
        )
        .unwrap()
        .expect("live imported");
    let profile = live_imported.template_member("profile").expect("profile");
    assert_eq!(profile.type_, "DraftProfile");
    assert_eq!(profile.type_context_path, live_types_path);
    assert!(live_imported.template_member("heading").is_none());

    let city_offset =
        (live_imported_source.find("profile.city").unwrap() + "profile.".len()) as u32;
    let city = index
        .resolve_twig_vue_instance_member_for_component(
            &live_imported_root,
            live_imported_source,
            city_offset,
            &component_path,
            Some(&live_imported),
        )
        .unwrap()
        .expect("city");
    assert!(city.member_found);
    assert_eq!(city.member.type_, "string");
    assert_eq!(city.member.definition_path, live_types_path);

    let flag_offset =
        (live_imported_source.find("localState.flag").unwrap() + "localState.".len()) as u32;
    let flag = index
        .resolve_twig_vue_instance_member_for_component(
            &live_imported_root,
            live_imported_source,
            flag_offset,
            &component_path,
            Some(&live_imported),
        )
        .unwrap()
        .expect("flag");
    assert!(flag.member_found);
    assert_eq!(flag.member.type_, "boolean");
    assert_eq!(flag.member.definition_path, component_path);

    let row_offset = (live_imported_source.find("row.label").unwrap() + "row.".len()) as u32;
    let row = index
        .resolve_twig_vue_member_for_component(
            &live_imported_root,
            live_imported_source,
            row_offset,
            &component_path,
            Some(&live_imported),
        )
        .unwrap()
        .expect("row");
    assert!(row.member_found);
    assert_eq!(row.member.type_, "string");
    assert_eq!(row.member.definition_path, live_types_path);

    let type_file = index
        .admin_type_file(&component_path)
        .unwrap()
        .expect("type file");
    assert!(
        type_file.declarations.is_empty(),
        "template text must not become TypeScript declarations"
    );
    assert_eq!(type_file.imports.len(), 3);

    // Reindex only the imported type owner; lazy materialization exposes the
    // new prop without touching the Vue component again.
    index_types("    subtitle?: boolean;\n");
    let component = index
        .get_effective_component("sw-imported-card")
        .unwrap()
        .expect("component");
    let subtitle = component.component_prop("subtitle").expect("subtitle");
    assert!(!subtitle.required);
    assert_eq!(subtitle.type_, "boolean");
    assert_eq!(subtitle.file_path, types_path);
    assert_ne!(subtitle.name_range, AdminSourceRange::default());
    let event = component.component_event("save").expect("save");
    assert_eq!(event.type_, "[id: string]");
    assert_eq!(event.documentation, "Persists the selected record.");
    assert_eq!(event.file_path, types_path);
    let close_event = component.component_event("close").expect("close");
    assert!(close_event.type_.contains("reason: string"));
    assert_eq!(
        close_event.documentation,
        "Closes the card with an optional reason."
    );
    assert_eq!(close_event.file_path, types_path);
    let slots: HashMap<String, VueComponentSlot> = component
        .slots
        .iter()
        .map(|slot| (slot.name.clone(), slot.clone()))
        .collect();
    for name in ["default", "header", "footer"] {
        assert!(slots.contains_key(name), "{name}");
    }
    assert!(slots["default"].members_complete);
    let default_title = slots["default"].member("title").expect("title");
    assert_eq!(default_title.type_, "string");
    assert!(slots["header"].members_complete);
    assert_eq!(slots["header"].file_path, component_path);
    assert!(slots["header"].name_range.declaration);
    assert!(!slots["header"].name_range.identifier);
    let header_item = slots["header"].member("item").expect("item");
    assert_eq!(header_item.type_, "string");
    assert_eq!(header_item.file_path, types_path);
    assert!(header_item.name_range.declaration);
    assert!(header_item.name_range.identifier);
    assert!(header_item.name_range.end_character > header_item.name_range.start_character);
    assert_eq!(slots["footer"].file_path, types_path);
    assert!(slots["footer"].name_range.declaration);
    assert!(slots["footer"].name_range.identifier);
    assert!(slots["footer"].name_range.end_character > slots["footer"].name_range.start_character);
}

#[test]
fn admin_indexer_resolves_unsaved_local_vue_component_contract() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let component_dir = admin_src(root.path()).join("component/sw-live-owner");
    let owner_path = path_string(&component_dir, "index.vue");
    let child_path = path_string(&component_dir, "DraftCard.vue");
    let child_source = "<template><slot :item=\"label\" /></template>
<script setup lang=\"ts\">
defineProps<{ label: string }>();
defineSlots<{ default(props: { item: string; count?: number }): unknown }>();
</script>";
    index_source(&index, &child_path, child_source);
    index_source(
        &index,
        &owner_path,
        "<template><div /></template><script setup lang=\"ts\"></script>",
    );

    let live_source = "<template>
    <DraftCard :label=\"title\">
        <template #default=\"slotProps\">{{ slotProps.item }}</template>
    </DraftCard>
    <component :is=\"'draft-card'\" :label=\"title\" />
</template>
<script setup lang=\"ts\">
import DraftCard from './DraftCard.vue';
const title = 'Draft';
</script>";
    let parsed = swlsp_parser_vue::parse_sfc(live_source);
    let parsed_root = parsed.parse.root();
    let live_owner = index
        .get_component_for_document(
            &owner_path,
            Some(&parsed_root),
            live_source,
            Some(&LineIndex::new(live_source)),
        )
        .unwrap()
        .expect("live owner");
    assert!(live_owner.local_component("draft-card").is_some());

    let child = index
        .get_component_for_template_tag(&owner_path, "draft-card", Some(&live_owner))
        .unwrap()
        .expect("child");
    let label = child.component_prop("label").expect("label");
    assert!(label.required);
    assert_eq!(label.file_path, child_path);
    let child_slot = child.component_slot("default").expect("default slot");
    assert!(child_slot.member("item").is_some());
    for owner in [Some(&live_owner), None] {
        assert_eq!(
            index
                .get_component_for_template_tag_shared(&owner_path, "draft-card", owner)
                .unwrap()
                .map(|component| component.as_ref().clone()),
            index
                .get_component_for_template_tag(&owner_path, "draft-card", owner)
                .unwrap(),
        );
    }

    assert!(
        index
            .get_component_for_template_tag(&owner_path, "draft-card", None)
            .unwrap()
            .is_none(),
        "the open owner must not leak into the index"
    );

    let item_offset = (live_source.find("slotProps.item").unwrap() + "slotProps.".len()) as u32;
    let scopes = twig_scoped_slots_at_offset(&parsed_root, item_offset);
    assert!(!scopes.is_empty());
    let slot_start_tag = twig_scoped_slot_starting_tag(&parsed_root, scopes.last().unwrap())
        .expect("slot start tag");
    let (consumers, consumer_complete) = index
        .resolve_twig_slot_consumer_components(&owner_path, &slot_start_tag, Some(&live_owner))
        .unwrap();
    assert!(consumer_complete);
    assert_eq!(consumers.len(), 1);
    let item_node = parsed_root.node_at(TextSize::from(item_offset)).unwrap();
    let resolved_item = index
        .resolve_twig_scoped_slot_member_for_owner(
            &parsed_root,
            &item_node,
            live_source,
            item_offset,
            &owner_path,
            Some(&live_owner),
        )
        .unwrap()
        .expect("resolved item");
    assert!(resolved_item.member_found);
    assert_eq!(resolved_item.member.type_, "string");
    assert_eq!(resolved_item.member.file_path, child_path);

    let is_offset = live_source.find(":is=").unwrap() as u32;
    let dynamic_tag =
        twigquery::starting_html_tag_at(&parsed_root.node_at(TextSize::from(is_offset)).unwrap())
            .expect("dynamic tag");
    let selector = twig_dynamic_component_selector(&dynamic_tag).expect("selector");
    let (_, dynamic_components, complete) = index
        .resolve_dynamic_component_contracts_for_owner(
            &owner_path,
            &selector,
            Some(&live_owner),
            Some(&dynamic_tag),
        )
        .unwrap();
    assert!(complete);
    assert_eq!(dynamic_components.len(), 1);
    assert_eq!(dynamic_components[0].name, "draft-card");
    assert!(dynamic_components[0].component_prop("label").is_some());
}

#[test]
fn admin_indexer_live_documents_compose_across_vue_and_typescript() {
    let root = temp_dir();
    let index = new_indexer(&root.path().join("cache"));
    let component_dir = admin_src(root.path()).join("component/live-stack");
    let owner_path = path_string(&component_dir, "Owner.vue");
    let child_path = path_string(&component_dir, "DraftCard.vue");
    let types_path = path_string(&component_dir, "draft-contract.ts");

    let persisted_types = "export interface DraftProps { persistedLabel: string }";
    let persisted_child = "<template><slot /></template>
<script setup lang=\"ts\">
import type { DraftProps } from './draft-contract';
defineProps<DraftProps>();
</script>";
    for (path, source) in [
        (&types_path, persisted_types),
        (&child_path, persisted_child),
        (&owner_path, "<template><div /></template>"),
    ] {
        index_source(&index, path, source);
    }

    let owner_source = "<template><DraftCard /></template>
<script setup lang=\"ts\">import DraftCard from './DraftCard.vue';</script>";
    let owner_document = swlsp_parser_vue::parse_sfc(owner_source);
    let owner_root = owner_document.parse.root();
    let owner = index
        .get_component_for_document(
            &owner_path,
            Some(&owner_root),
            owner_source,
            Some(&LineIndex::new(owner_source)),
        )
        .unwrap()
        .expect("owner");

    let resolve_child = || {
        index
            .get_component_for_template_tag(&owner_path, "DraftCard", Some(&owner))
            .unwrap()
            .expect("child")
    };
    let persisted = resolve_child();
    assert!(persisted.component_prop("persistedLabel").is_some());

    // Opening the child and its imported contract creates one composed live
    // generation even though the parent request owns neither document.
    let child_document = swlsp_parser_vue::parse_sfc(persisted_child);
    index.update_live_document(
        &child_path,
        Some(&child_document.parse.root()),
        persisted_child,
        Some(&LineIndex::new(persisted_child)),
    );
    let live_types = "export interface DraftProps { liveLabel: string; count?: number }";
    index.update_live_document(
        &types_path,
        None,
        live_types,
        Some(&LineIndex::new(live_types)),
    );
    let live = resolve_child();
    assert!(live.component_prop("liveLabel").is_some());
    assert!(live.component_prop("count").is_some());
    assert!(live.component_prop("persistedLabel").is_none());

    // Type overlays are resolved lazily.
    let renamed_types = "export interface DraftProps { renamedLabel: string }";
    index.update_live_document(
        &types_path,
        None,
        renamed_types,
        Some(&LineIndex::new(renamed_types)),
    );
    let renamed = resolve_child();
    assert!(renamed.component_prop("renamedLabel").is_some());
    assert!(renamed.component_prop("liveLabel").is_none());

    // Editing the child itself replaces rather than accumulates its API.
    let changed_child_source = "<template><slot name=\"body\" :item=\"childOnly\" /></template>
<script setup lang=\"ts\">defineProps<{ childOnly: boolean }>();</script>";
    let changed_child = swlsp_parser_vue::parse_sfc(changed_child_source);
    index.update_live_document(
        &child_path,
        Some(&changed_child.parse.root()),
        changed_child_source,
        Some(&LineIndex::new(changed_child_source)),
    );
    let changed = resolve_child();
    let child_only = changed.component_prop("childOnly").expect("childOnly");
    assert_eq!(child_only.type_, "boolean");
    assert!(changed.component_prop("renamedLabel").is_none());
    assert!(changed.component_slot("body").is_some());

    index.remove_live_document(&child_path);
    index.remove_live_document(&types_path);
    let fallback = resolve_child();
    assert!(fallback.component_prop("persistedLabel").is_some());
    assert!(fallback.component_prop("childOnly").is_none());
}
