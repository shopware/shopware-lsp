#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn vue_template_runtime_catalog() {
    let builtin = vue_builtin_member("$t").expect("$t");
    assert_eq!(builtin.kind, VueComponentMemberKind::Method);
    assert!(builtin.type_.contains("key: string"));

    let object = vue_template_global("Object").expect("Object");
    assert_eq!(object.kind, VueComponentMemberKind::Data);
    assert_eq!(object.type_, "ObjectConstructor");
    assert!(vue_template_global("runtimePluginValue").is_none());
}

#[test]
fn effective_component_infers_legacy_javascript_return_chains() {
    let root_dir = temp_dir();
    let idx = new_indexer(&root_dir.path().join("cache"));
    let admin_root = path_string(
        root_dir.path(),
        "src/Administration/Resources/app/administration/src",
    );
    index_source(
        &idx,
        &gopath::join2(&admin_root, "app/composables/use-context.ts"),
        r#"export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}"#,
    );
    let schema_path = gopath::join2(&admin_root, "entity-schema-definition.d.ts");
    index_source(
        &idx,
        &schema_path,
        r#"
declare namespace EntitySchema {
    interface order {
        id: string;
        transactions: EntityCollection<'order_transaction'>;
    }
    interface order_transaction {
        id: string;
        amount: { total: number; tax: number };
    }
    interface media { id: string; fileName: string; }
    interface promotion { id: string; name: string; }
}
"#,
    );
    let store_path = gopath::join2(&admin_root, "app/store/marketing.store.ts");
    let store_source = r#"
type MarketingState = {
    activeCampaign: Entity<'promotion'> | null;
};
Shopware.Store.register({
    id: 'marketing',
	state: (): MarketingState => ({
		activeCampaign: null,
		loading: { products: false },
    }),
    getters: {
        campaignName(): string { return this.activeCampaign?.name ?? ''; },
    },
    actions: {
        selectCampaign(campaign: Entity<'promotion'>): void {},
    },
});"#;
    index_source(&idx, &store_path, store_source);
    let stores = idx.get_store("marketing").expect("stores");
    assert_eq!(stores.len(), 1);
    assert_eq!(stores[0].state_type, "MarketingState");
    let active_campaign = stores[0].member("activeCampaign").expect("activeCampaign");
    assert_eq!(active_campaign.type_, "Entity<'promotion'> | null");

    let definition_path = gopath::join2(&admin_root, "component/sw-legacy/index.js");
    let template_path = gopath::join2(&admin_root, "component/sw-legacy/sw-legacy.html.twig");
    let source = r#"
	export default {
	    props: {
	        order: { type: Object as PropType<Entity<'order'>>, required: true },
	    },
    inject: ['repositoryFactory'],
		data() {
		return {
			mediaItems: [],
			nullableMediaItems: null,
			callbackMediaItems: null,
			runtimeError: null,
			selectedMedia: null,
			mediaById: {} as Record<string, Entity<'media'>>,
		};
	},
    computed: {
        transactions() { return this.order.transactions; },
        lastTransaction() { return this.transactions.last(); },
        mediaRepository() { return this.repositoryFactory.create('media'); },
        newMedia() { return this.mediaRepository.create(); },
		filteredMediaItems() { return this.mediaItems.filter((item) => item.fileName); },
		mediaFileNames() { return this.mediaItems.map((item) => item.fileName); },
		mediaCards() {
			return this.mediaItems.map((item) => ({
				id: item.id,
				label: item.fileName,
			}));
		},
		safeMediaCards() {
			return this.nullableMediaItems?.map((item) => {
				return { ...item, label: item.fileName };
			}) ?? [];
		},
		dynamicMediaCards() { return this.mediaItems.map(this.createMediaCard); },
		mediaValues() { return Object.values(this.mediaById); },
		mediaKeys() { return Object.keys(this.mediaById).filter((key) => key); },
		mediaEntries() { return Object.entries(this.mediaById); },
		mediaEntryNames() {
			return Object.entries(this.mediaById).map((entry) => entry[1].fileName);
		},
		rebuiltMediaById() { return Object.fromEntries(Object.entries(this.mediaById)); },
		firstMappedMedia() { return this.mediaById[this.selectedId]; },
		firstLoadedMedia() { return this.mediaItems[0]; },
		staticMediaCards() { return [{ id: 'example', label: 'Example' }]; },
		loading() { return Shopware.Store.get('marketing').loading; },
        activeCampaign() { return Shopware.Store.get('marketing').activeCampaign; },
		languageId() { return Shopware.Context.api.languageId; },
    },
	methods: {
        getLastTransaction() { return this.transactions.last(); },
		getSlots() { return this.$slots; },
		async loadMedia() {
			const mediaItems = await this.mediaRepository.search(this.criteria);
			const selectedMedia = await this.mediaRepository.get(this.selectedId);
			this.mediaItems = mediaItems;
			this.nullableMediaItems = mediaItems;
			this.selectedMedia = selectedMedia;
		},
		loadMediaCallback() {
			this.mediaRepository.search(this.criteria).then((mediaItems) => {
				this.callbackMediaItems = mediaItems;
			});
		},
		setRuntimeError() {
			this.runtimeError = { title: 'Failed', message: 'Try again' };
		},
    },
};"#;
    let mut definition =
        parse_component_definition_with_line_index(&parse_js(source), &LineIndex::new(source));
    let parsed_nullable = definition
        .members
        .iter()
        .find(|member| member.name == "nullableMediaItems")
        .expect("nullableMediaItems");
    assert_eq!(parsed_nullable.type_, "null");
    assert_eq!(parsed_nullable.source_expression, "null");
    set_definition_file_path(&mut definition, &definition_path);
    definition.template_path = template_path.clone();
    idx.save_component_definition(&normalize_definition_path(&definition_path), &definition)
        .expect("save definition");
    idx.save_component(&VueComponent {
        name: "sw-legacy".to_owned(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path: template_path.clone(),
        ..Default::default()
    })
    .expect("save component");

    let component = idx
        .get_effective_component("sw-legacy")
        .expect("effective component")
        .expect("component exists");
    let member_type = |name: &str| -> String {
        component
            .template_member(name)
            .unwrap_or_else(|| panic!("{name}"))
            .type_
    };
    assert_eq!(
        member_type("transactions"),
        "EntityCollection<'order_transaction'>"
    );
    assert_eq!(
        member_type("lastTransaction"),
        "Entity<'order_transaction'> | null"
    );
    assert_eq!(
        member_type("getLastTransaction"),
        "() => Entity<'order_transaction'> | null"
    );
    assert_eq!(
        member_type("getSlots"),
        "() => Record<string, Function | undefined>"
    );
    assert_eq!(member_type("mediaRepository"), "Repository<'media'>");
    assert_eq!(member_type("newMedia"), "Entity<'media'>");
    assert_eq!(member_type("filteredMediaItems"), "Array<Entity<'media'>>");
    assert_eq!(member_type("mediaFileNames"), "Array<string>");
    assert_eq!(
        member_type("mediaCards"),
        "Array<{ id: string; label: string }>"
    );
    assert_eq!(
        member_type("safeMediaCards"),
        "Array<Entity<'media'> & { label: string }>"
    );
    assert_eq!(member_type("dynamicMediaCards"), "Array<unknown>");
    assert_eq!(member_type("mediaValues"), "Array<Entity<'media'>>");
    assert_eq!(member_type("mediaKeys"), "Array<string>");
    assert_eq!(
        member_type("mediaEntries"),
        "Array<[string, Entity<'media'>]>"
    );
    assert_eq!(member_type("mediaEntryNames"), "Array<string>");
    assert_eq!(
        member_type("rebuiltMediaById"),
        "Record<string, Entity<'media'>>"
    );
    assert_eq!(member_type("firstMappedMedia"), "Entity<'media'>");
    assert_eq!(member_type("firstLoadedMedia"), "Entity<'media'>");
    assert_eq!(
        member_type("staticMediaCards"),
        "Array<{ id: string; label: string }>"
    );
    assert_eq!(member_type("mediaItems"), "EntityCollection<'media'>");
    assert_eq!(
        member_type("nullableMediaItems"),
        "EntityCollection<'media'> | null"
    );
    assert_eq!(
        member_type("callbackMediaItems"),
        "EntityCollection<'media'> | null"
    );
    assert_eq!(member_type("selectedMedia"), "Entity<'media'> | null");
    let runtime_error = component
        .template_member("runtimeError")
        .expect("runtimeError");
    assert!(runtime_error.type_.contains("title: string"));
    assert!(runtime_error.open_runtime_shape);
    assert_eq!(member_type("activeCampaign"), "Entity<'promotion'> | null");
    assert_eq!(member_type("languageId"), "null | string");
    let loading = component.template_member("loading").expect("loading");
    assert!(loading.open_runtime_shape);

    let markup = r#"<div :title="lastTransaction.amount.total"></div>"#;
    let twig_root = parse_twig(markup);
    let offset = (markup.find("amount.total").unwrap() + "amount.to".len()) as u32;
    let resolved = idx
        .resolve_twig_vue_instance_member(&twig_root, markup, offset, &template_path)
        .expect("resolve")
        .expect("resolved");
    assert!(resolved.member_found);
    assert_eq!(resolved.member.type_, "number");
    assert_eq!(resolved.member.definition_path, schema_path);

    let markup = r#"<div v-for="media in mediaItems" :title="media.fileName"></div>"#;
    let twig_root = parse_twig(markup);
    let offset = (markup.find("fileName").unwrap() + 2) as u32;
    let local_resolved = idx
        .resolve_twig_vue_member(&twig_root, markup, offset, &template_path)
        .expect("resolve")
        .expect("resolved");
    assert!(local_resolved.member_found);
    assert_eq!(local_resolved.member.type_, "string");
    assert_eq!(local_resolved.member.definition_path, schema_path);

    let markup =
        r#"<div v-for="card in safeMediaCards" :title="card.fileName">{{ card.label }}</div>"#;
    let twig_root = parse_twig(markup);
    let offset = (markup.find("card.label").unwrap() + "card.la".len()) as u32;
    let projected = idx
        .resolve_twig_vue_member(&twig_root, markup, offset, &template_path)
        .expect("resolve")
        .expect("resolved");
    assert!(projected.member_found);
    assert_eq!(projected.member.type_, "string");

    let markup = r#"<div v-for="(slot, name, index) in getSlots()">{{ name.length }} {{ index.toFixed() }}</div>"#;
    let twig_root = parse_twig(markup);
    let name_offset = (markup.find("name.length").unwrap() + 1) as u32;
    let name_binding = idx
        .resolve_twig_vue_binding(&twig_root, markup, name_offset, &template_path)
        .expect("resolve")
        .expect("binding");
    assert_eq!(name_binding.type_, "string");
    let index_offset = (markup.find("index.toFixed").unwrap() + 1) as u32;
    let index_binding = idx
        .resolve_twig_vue_binding(&twig_root, markup, index_offset, &template_path)
        .expect("resolve")
        .expect("binding");
    assert_eq!(index_binding.type_, "number");
}

#[test]
fn static_component_expression_parsing_stays_conservative() {
    let segments =
        vue_static_this_expression("this.orders?.last?.().transactions.first()").expect("segments");
    assert_eq!(segments.len(), 4);
    assert_eq!(segments[0].name, "orders");
    assert!(segments[1].called);
    assert!(segments[3].called);

    let segments = vue_static_this_expression("this.orders[index].name").expect("segments");
    assert_eq!(segments.len(), 3);
    assert!(segments[1].indexed);
    assert_eq!(segments[1].index_expression, "index");
    assert!(vue_static_this_expression("this.orders[this.sorter]().name").is_none());

    let (store, store_segments) =
        vue_static_store_expression("Shopware.Store.get('marketing')?.activeCampaign.name")
            .expect("store");
    assert_eq!(store, "marketing");
    assert_eq!(store_segments.len(), 2);
    assert_eq!(store_segments[0].name, "activeCampaign");
    let template_segments =
        vue_static_template_expression("page.sections.filter((section) => section.name)")
            .expect("segments");
    assert_eq!(template_segments.len(), 3);
    assert_eq!(template_segments[0].name, "page");
    assert_eq!(template_segments[2].name, "filter");
    assert!(template_segments[2].called);
    assert_eq!(template_segments[2].arguments, "(section) => section.name");

    let template_segments =
        vue_static_template_expression("page.sections?.map((section) => section.name) ?? []")
            .expect("segments");
    assert_eq!(template_segments.len(), 3);
    assert_eq!(template_segments[2].name, "map");
    assert!(template_segments[2].optional);
}

#[test]
fn merge_vue_assignment_types_test() {
    assert_eq!(
        merge_vue_assignment_types(
            "Array",
            &["Array".to_owned(), "EntityCollection<'media'>".to_owned()]
        ),
        "EntityCollection<'media'>"
    );
    assert_eq!(
        merge_vue_assignment_types("null", &["Entity<'media'> | null".to_owned()]),
        "Entity<'media'> | null"
    );
    assert!(merge_vue_assignment_types("Array", &["Array".to_owned()]).is_empty());
    assert!(merge_vue_assignment_types("Array", &["Array<unknown>".to_owned()]).is_empty());
}

#[test]
fn effective_component_refines_empty_array_from_literal_writes() {
    let root_dir = temp_dir();
    let idx = new_indexer(&root_dir.path().join("cache"));
    let definition_path = path_string(
        root_dir.path(),
        "src/Administration/Resources/app/administration/src/component/view/index.js",
    );
    let template_path = gopath::join2(&gopath::dir(&definition_path), "view.html.twig");
    let code = r#"export default {
	data() { return { items: [] }; },
	methods: {
		load() {
			this.items = [
				{ id: 'one', label: 'One' },
				{ id: 'two', label: 'Two' },
			];
		},
	},
};"#;
    let mut definition =
        parse_component_definition_with_line_index(&parse_js(code), &LineIndex::new(code));
    set_definition_file_path(&mut definition, &definition_path);
    definition.template_path = template_path.clone();
    idx.save_component_definition(&normalize_definition_path(&definition_path), &definition)
        .expect("save definition");
    idx.save_component(&VueComponent {
        name: "sw-view".to_owned(),
        file_path: definition_path.clone(),
        definition_path: definition_path.clone(),
        template_path,
        ..Default::default()
    })
    .expect("save component");

    let component = idx
        .get_effective_component("sw-view")
        .expect("effective component")
        .expect("component exists");
    let items = component.template_member("items").expect("items");
    assert_eq!(items.type_, "Array<{ id: string; label: string }>");
    let shape = idx
        .resolve_vue_type(&items.type_, &definition_path, &[])
        .expect("shape");
    assert!(vue_member_names(&shape.members).contains(&"length".to_owned()));
}
