#![allow(unused_imports)]
use super::support::*;
use crate::*;

use swlsp_indexer::Indexer;

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn find(source: &str, needle: &str) -> u32 {
    source
        .find(needle)
        .unwrap_or_else(|| panic!("missing {needle}")) as u32
}

fn rfind(source: &str, needle: &str) -> u32 {
    source
        .rfind(needle)
        .unwrap_or_else(|| panic!("missing {needle}")) as u32
}

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

#[test]
fn twig_vue_root_identifiers_exclude_keywords_and_arrow_locals() {
    let source = r#"<div v-for="row in rows.filter((candidate) => candidate.active)">{{ rows.map(({ name: label }) => label).length }} {{ outside }}</div>"#;
    let root = parse_twig(source);
    let names: Vec<String> = twig_vue_expression_root_identifiers(&root, source)
        .into_iter()
        .map(|identifier| identifier.name)
        .collect();
    assert_eq!(names, strings(&["row", "rows", "rows", "outside"]));
}

#[test]
fn twig_vue_local_root_identifier_ranges_test() {
    let source = r#"<div v-for="row in rows"><button @click="save(row, $event)">{{ row.name }} {{ outside }}</button></div>"#;
    let root = parse_twig(source);
    let identifiers = twig_vue_expression_root_identifiers(&root, source);
    let local_ranges = twig_vue_local_root_identifier_ranges(&root, source, &identifiers);
    let local_names: Vec<String> = identifiers
        .iter()
        .filter(|identifier| local_ranges.contains(&identifier.range))
        .map(|identifier| identifier.name.clone())
        .collect();
    assert_eq!(local_names, strings(&["row", "row", "$event", "row"]));
}

#[test]
fn twig_vue_for_bindings_are_lexical_and_shadowed() {
    let source = "<ul>
    <li v-for=\"(item, index) in items\" :key=\"item.id\">
        {{ item.name }} {{ index }}
        <span v-for=\"item of item.children\">{{ item.name }}</span>
        {{ item.id }}
    </li>
</ul>
{{ item }}";
    let root = parse_twig(source);

    let outer =
        twig_vue_binding_at_offset(&root, source, find(source, "item.name") + 2).expect("outer");
    assert_eq!(outer.kind, TwigVueBindingKind::For);
    assert_eq!(outer.name, "item");
    assert_eq!(outer.iterable, "items");

    let index_binding = twig_vue_binding_at_offset(
        &root,
        source,
        find(source, "{{ index") + "{{ in".len() as u32,
    )
    .expect("index");
    assert_eq!(index_binding.name, "index");

    let inner =
        twig_vue_binding_at_offset(&root, source, rfind(source, "item.name") + 2).expect("inner");
    assert_eq!(inner.iterable, "item.children");
    assert!(inner.scope_range.len() < outer.scope_range.len());

    let after_inner = twig_vue_binding_at_offset(&root, source, rfind(source, "item.id") + 2)
        .expect("after inner");
    assert_eq!(outer.declaration_range, after_inner.declaration_range);

    let outside_offset = rfind(source, "{{ item") + "{{ it".len() as u32;
    assert!(twig_vue_binding_at_offset(&root, source, outside_offset).is_none());
}

#[test]
fn twig_vue_for_binding_is_not_visible_in_iterable() {
    let source = r#"<div v-for="item in item.children">{{ item }}</div>"#;
    let root = parse_twig(source);
    let declaration_offset = find(source, "item in") + 1;
    let declaration =
        twig_vue_binding_at_offset(&root, source, declaration_offset).expect("declaration");
    assert!(declaration.is_declaration_offset(declaration_offset));
    assert!(twig_vue_binding_at_offset(&root, source, find(source, "item.children") + 1).is_none());
}

#[test]
fn twig_vue_event_binding_and_payload_type() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    index
        .save_component(&VueComponent {
            name: "mt-switch".to_owned(),
            events: vec![VueComponentEvent {
                name: "update:modelValue".to_owned(),
                type_: "(value: boolean) => any".to_owned(),
                file_path: "/meteor/MtSwitch.d.ts".to_owned(),
                line: 17,
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    let source = r#"<mt-switch @update:model-value="update($event)" />"#;
    let root = parse_twig(source);
    let binding = index
        .resolve_twig_vue_binding(&root, source, find(source, "$event") + 2, "")
        .unwrap()
        .expect("binding");
    assert_eq!(binding.kind, TwigVueBindingKind::Event);
    assert_eq!(binding.type_, "boolean");
    assert_eq!(binding.definition_path, "/meteor/MtSwitch.d.ts");
    assert_eq!(binding.definition_line, 17);
    index.close().unwrap();
}

fn event_heavy_source() -> String {
    let mut source = String::from("<section>");
    for index in 0..80 {
        source.push_str(&format!(
            r#"<button @click="handle{index}($event)" @focus="focus{index}($event)"></button>"#
        ));
    }
    source.push_str("</section>");
    source
}

/// Correctness part of Go `BenchmarkTwigVueBindingsAtOffsetEventHeavy` and
/// `BenchmarkTwigVueBindingReferencesEventHeavy`.
#[test]
fn twig_vue_event_heavy_bindings() {
    let content = event_heavy_source();
    let root = parse_twig(&content);
    let offset = rfind(&content, "$event") + 2;
    let bindings = twig_vue_bindings_at_offset(&root, &content, offset);
    assert_eq!(bindings.len(), 1);
    assert!(twig_vue_binding_at_offset(&root, &content, offset).is_some());
    assert_eq!(
        twig_vue_binding_references(&root, &content, &bindings[0]).len(),
        1
    );
}

#[test]
fn twig_vue_for_binding_gets_iterable_element_type() {
    let root_dir = temp_dir();
    let index = new_indexer(&root_dir.path().join("cache"));
    let template_path = path_string(
        root_dir.path(),
        "Resources/app/administration/view.html.twig",
    );
    index
        .save_component(&VueComponent {
            name: "sw-view".to_owned(),
            template_path: template_path.clone(),
            props: vec![
                VueComponentProp {
                    name: "products".to_owned(),
                    type_: "Array as PropType<Product[]>".to_owned(),
                    ..Default::default()
                },
                VueComponentProp {
                    name: "productsById".to_owned(),
                    type_: "Record<string, Product>".to_owned(),
                    ..Default::default()
                },
            ],
            members: vec![
                VueComponentMember {
                    name: "thumbnailSizes".to_owned(),
                    kind: VueComponentMemberKind::Data,
                    type_: "Array<{ id: string }>".to_owned(),
                    element_members: vec![VueComponentElementMember {
                        name: "deletable".to_owned(),
                        type_: "boolean".to_owned(),
                        file_path: "/project/component.js".to_owned(),
                        line: 17,
                    }],
                    ..Default::default()
                },
                VueComponentMember {
                    name: "sections".to_owned(),
                    kind: VueComponentMemberKind::Computed,
                    type_: "{ accessibility: Array<{ id: string }> }".to_owned(),
                    open_runtime_shape: true,
                    ..Default::default()
                },
            ],
            ..Default::default()
        })
        .unwrap();
    let source =
        r#"<div v-for="(product, index) in products">{{ product.name }} {{ index }}</div>"#;
    let twig_root = parse_twig(source);
    let product = index
        .resolve_twig_vue_binding(
            &twig_root,
            source,
            find(source, "product.name") + 1,
            &template_path,
        )
        .unwrap()
        .expect("product");
    assert_eq!(product.type_, "Product");
    assert_eq!(vue_member_names(&product.members), strings(&["name"]));
    let iteration_index = index
        .resolve_twig_vue_binding(
            &twig_root,
            source,
            rfind(source, "{{ index") + "{{ in".len() as u32,
            &template_path,
        )
        .unwrap()
        .expect("index");
    assert_eq!(iteration_index.type_, "number");

    let augmented_source = r#"<div v-for="size in thumbnailSizes">{{ size.deletable }}</div>"#;
    let augmented_root = parse_twig(augmented_source);
    let augmented = index
        .resolve_twig_vue_member(
            &augmented_root,
            augmented_source,
            find(augmented_source, "deletable") + 2,
            &template_path,
        )
        .unwrap()
        .expect("augmented");
    assert!(augmented.members_complete);
    assert!(augmented.member_found);
    assert_eq!(augmented.member.type_, "boolean");
    assert_eq!(augmented.member.definition_path, "/project/component.js");
    assert_eq!(augmented.member.definition_line, 17);

    let open_source =
        r#"<div v-for="section in sections.accessibility">{{ section.privilege }}</div>"#;
    let open_root = parse_twig(open_source);
    let open_binding = index
        .resolve_twig_vue_binding(
            &open_root,
            open_source,
            rfind(open_source, "section.privilege") + 2,
            &template_path,
        )
        .unwrap()
        .expect("open binding");
    assert!(!open_binding.members_complete);

    let record_source = r#"<div v-for="(product, productId, index) in productsById">{{ product.name }} {{ productId.length }} {{ index.toFixed() }}</div>"#;
    let record_root = parse_twig(record_source);
    let product = index
        .resolve_twig_vue_binding(
            &record_root,
            record_source,
            find(record_source, "product.name") + 1,
            &template_path,
        )
        .unwrap()
        .expect("record product");
    assert_eq!(product.type_, "Product");
    let key = index
        .resolve_twig_vue_binding(
            &record_root,
            record_source,
            find(record_source, "productId.length") + 1,
            &template_path,
        )
        .unwrap()
        .expect("key");
    assert_eq!(key.type_, "string");
    let record_index = index
        .resolve_twig_vue_binding(
            &record_root,
            record_source,
            find(record_source, "index.toFixed") + 1,
            &template_path,
        )
        .unwrap()
        .expect("record index");
    assert_eq!(record_index.type_, "number");
    index.close().unwrap();
}

#[test]
fn resolve_twig_vue_nested_named_type_member() {
    let root_dir = temp_dir();
    let index = new_indexer(&root_dir.path().join("cache"));
    let admin_root = path_string(
        root_dir.path(),
        "Resources/app/administration/src/component",
    );
    let definition_path = format!("{admin_root}/index.ts");
    let template_path = format!("{admin_root}/view.html.twig");
    index_source(
        &index,
        &definition_path,
        "
interface Address { city: string; }
interface Manufacturer { name: string; address: Address; }
interface Option { label: string; }
interface Variant { options: Option[]; }
interface Product { id: string; manufacturer: Manufacturer; variants: Variant[]; }
",
    );
    index
        .save_component(&VueComponent {
            name: "sw-view".to_owned(),
            file_path: definition_path.clone(),
            definition_path: definition_path.clone(),
            template_path: template_path.clone(),
            members: vec![VueComponentMember {
                name: "products".to_owned(),
                kind: VueComponentMemberKind::Computed,
                type_: "Product[]".to_owned(),
                file_path: definition_path.clone(),
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    let source = r#"<div v-for="product in products">{{ product.manufacturer.address.city }} {{ product.manufacturer.address.city }}</div>"#;
    let twig_root = parse_twig(source);
    let resolved = index
        .resolve_twig_vue_member(&twig_root, source, find(source, "city") + 2, &template_path)
        .unwrap()
        .expect("resolved");
    assert!(resolved.receiver_found);
    assert!(resolved.member_found);
    assert_eq!(resolved.member.name, "city");
    assert_eq!(resolved.member.type_, "string");
    assert_eq!(resolved.member.definition_path, definition_path);
    assert_eq!(
        resolved.access.member_path(),
        strings(&["manufacturer", "address", "city"])
    );
    let references = twig_vue_binding_member_path_references(
        &twig_root,
        source,
        &resolved.binding,
        &resolved.access.member_path(),
    );
    assert_eq!(references.len(), 2);

    let nested_loop_source = r#"<div v-for="product in products"><div v-for="variant in product.variants"><span v-for="option in variant.options">{{ option.label }}</span></div></div>"#;
    let nested_loop_root = parse_twig(nested_loop_source);
    let nested_offset = find(nested_loop_source, "option.label") + "option.la".len() as u32;
    let nested = index
        .resolve_twig_vue_member(
            &nested_loop_root,
            nested_loop_source,
            nested_offset,
            &template_path,
        )
        .unwrap()
        .expect("nested");
    assert_eq!(nested.binding.type_, "Option");
    assert!(nested.member_found);
    assert_eq!(nested.member.type_, "string");
    index.close().unwrap();
}

#[test]
fn resolve_twig_vue_component_entity_member_and_nested_entity_loop() {
    let root_dir = temp_dir();
    let index = new_indexer(&root_dir.path().join("cache"));
    let admin_root = path_string(
        root_dir.path(),
        "Resources/app/administration/src/component",
    );
    let schema_path = format!("{}/entity-schema-definition.d.ts", gopath::dir(&admin_root));
    index_source(
        &index,
        &schema_path,
        "
declare namespace EntitySchema {
    interface cms_page {
        id: string;
        name: string;
        sections: EntityCollection<'cms_section'>;
    }
    interface cms_section { id: string; name: string; }
}
",
    );
    let definition_path = format!("{admin_root}/index.ts");
    let template_path = format!("{admin_root}/view.html.twig");
    let member = |name: &str, kind: VueComponentMemberKind, type_: &str| VueComponentMember {
        name: name.to_owned(),
        kind,
        type_: type_.to_owned(),
        file_path: definition_path.clone(),
        ..Default::default()
    };
    index
        .save_component(&VueComponent {
            name: "sw-cms-page".to_owned(),
            file_path: definition_path.clone(),
            definition_path: definition_path.clone(),
            template_path: template_path.clone(),
            members: vec![
                member("page", VueComponentMemberKind::Prop, "Entity<'cms_page'>"),
                member(
                    "getPage",
                    VueComponentMemberKind::Method,
                    "() => Entity<'cms_page'>",
                ),
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
                member("currentGroup", VueComponentMemberKind::Prop, "string"),
            ],
            ..Default::default()
        })
        .unwrap();

    let source = concat!(
        "<div>{{ page.name }} {{ page.sections.first().name }} {{ page.sections[0].name }} {{ sectionsById[currentGroup].name }} {{ getPage().name }}",
        r#"<section v-for="section in page.sections">{{ section.name }}</section>"#,
        r#"<section v-for="group in sectionGroups">{{ group[0].name }}</section>"#,
        r#"<section v-for="filteredSection in page.sections.filter((section) => section.name)">{{ filteredSection.name }}</section>"#,
        r#"<section v-for="mappedName in page.sections?.map((section) => section.name) ?? []">{{ mappedName.length }}</section>"#,
        r#"<section v-for="mappedSection in page.sections.map((section) => ({ id: section.id, label: section.name }))">{{ mappedSection.label }}</section>"#,
        r#"<section v-for="recordSection in Object.values(sectionsById)">{{ recordSection.name }}</section>"#,
        r#"<section v-for="groupedSection in sectionGroups[currentGroup]">{{ groupedSection.name }}</section>"#,
        r#"<section v-for="staticLabel in ['first', 'second']">{{ staticLabel.length }}</section>"#,
        "</div>"
    );
    let twig_root = parse_twig(source);
    let instance = |needle: &str, prefix: &str| {
        index
            .resolve_twig_vue_instance_member(
                &twig_root,
                source,
                find(source, needle) + prefix.len() as u32,
                &template_path,
            )
            .unwrap()
    };
    let resolved = instance("page.name", "page.na").expect("page.name");
    assert!(resolved.receiver_found);
    assert!(resolved.member_found);
    assert_eq!(resolved.qualified_name(), "page.name");
    assert_eq!(resolved.member.type_, "string");
    assert_eq!(resolved.member.definition_path, schema_path);

    let collection_call = instance("page.sections.first().name", "page.sections.first().na")
        .expect("collection call");
    assert!(collection_call.member_found);
    assert_eq!(collection_call.member.type_, "string");
    assert_eq!(
        collection_call.receiver_type,
        "Entity<'cms_section'> | null"
    );
    assert_eq!(
        collection_call.qualified_name(),
        "page.sections.first().name"
    );

    let indexed_collection =
        instance("page.sections[0].name", "page.sections[0].na").expect("indexed collection");
    assert!(indexed_collection.member_found);
    assert_eq!(indexed_collection.member.type_, "string");
    assert_eq!(indexed_collection.receiver_type, "Entity<'cms_section'>");
    assert_eq!(indexed_collection.qualified_name(), "page.sections[0].name");

    let indexed_record = instance(
        "sectionsById[currentGroup].name",
        "sectionsById[currentGroup].na",
    )
    .expect("indexed record");
    assert!(indexed_record.member_found, "resolved={indexed_record:?}");
    assert_eq!(indexed_record.member.type_, "string");
    assert_eq!(indexed_record.receiver_type, "Entity<'cms_section'>");
    assert_eq!(
        indexed_record.qualified_name(),
        "sectionsById[currentGroup].name"
    );

    let root_call = instance("getPage().name", "getPage().na").expect("root call");
    assert!(root_call.member_found);
    assert_eq!(root_call.member.type_, "string");
    assert_eq!(root_call.qualified_name(), "getPage().name");

    let member_at = |offset: u32| {
        index
            .resolve_twig_vue_member(&twig_root, source, offset, &template_path)
            .unwrap()
    };
    let group_item =
        member_at(find(source, "group[0].name") + "group[0].na".len() as u32).expect("group item");
    assert!(group_item.member_found);
    assert_eq!(group_item.binding.type_, "EntityCollection<'cms_section'>");
    assert_eq!(group_item.receiver_type, "Entity<'cms_section'>");
    assert_eq!(group_item.member.type_, "string");

    let section =
        member_at(find(source, "{{ section.name") + "{{ section.na".len() as u32).expect("section");
    assert_eq!(section.binding.type_, "Entity<'cms_section'>");
    assert!(section.binding.members_complete);
    assert!(section.member_found);
    assert_eq!(section.member.definition_path, schema_path);

    let filtered =
        member_at(rfind(source, "filteredSection.name") + "filteredSection.na".len() as u32)
            .expect("filtered");
    assert_eq!(filtered.binding.type_, "Entity<'cms_section'>");
    assert!(filtered.member_found);
    assert_eq!(filtered.member.type_, "string");
    assert_eq!(filtered.member.definition_path, schema_path);

    let mapped_name = member_at(rfind(source, "mappedName.length") + "mappedName.len".len() as u32)
        .expect("mapped name");
    assert_eq!(mapped_name.binding.type_, "string");
    assert!(mapped_name.member_found);
    assert_eq!(mapped_name.member.type_, "number");

    let mapped_section =
        member_at(rfind(source, "mappedSection.label") + "mappedSection.la".len() as u32)
            .expect("mapped section");
    assert_eq!(
        mapped_section.binding.type_,
        "{ id: string; label: string }"
    );
    assert!(mapped_section.member_found);
    assert_eq!(mapped_section.member.type_, "string");

    let record_section =
        member_at(rfind(source, "recordSection.name") + "recordSection.na".len() as u32)
            .expect("record section");
    assert_eq!(record_section.binding.type_, "Entity<'cms_section'>");
    assert!(record_section.member_found);
    assert_eq!(record_section.member.type_, "string");

    let grouped_section =
        member_at(rfind(source, "groupedSection.name") + "groupedSection.na".len() as u32)
            .expect("grouped section");
    assert_eq!(grouped_section.binding.type_, "Entity<'cms_section'>");
    assert!(grouped_section.member_found);
    assert_eq!(grouped_section.member.type_, "string");

    let static_label =
        member_at(rfind(source, "staticLabel.length") + "staticLabel.len".len() as u32)
            .expect("static label");
    assert_eq!(static_label.binding.type_, "string");
    assert!(static_label.member_found);
    assert_eq!(static_label.member.type_, "number");

    let shadowed_source = r#"<div v-for="page in pages">{{ page.name }}</div>"#;
    let shadowed_root = parse_twig(shadowed_source);
    let shadowed = index
        .resolve_twig_vue_instance_member(
            &shadowed_root,
            shadowed_source,
            find(shadowed_source, "page.name") + "page.na".len() as u32,
            &template_path,
        )
        .unwrap();
    assert!(shadowed.is_none());
    index.close().unwrap();
}

#[test]
fn twig_vue_binding_references_exclude_properties_strings_and_shadowing() {
    let source = "<div v-for=\"item in items\">
    {{ item.name }}
    <button @click=\"select(item, 'item', { item: item })\"></button>
    <span v-for=\"item in children\">{{ item }}</span>
</div>";
    let root = parse_twig(source);
    let target = twig_vue_binding_at_offset(&root, source, find(source, "item in items") + 1)
        .expect("target");
    let references = twig_vue_binding_references(&root, source, &target);
    let values: Vec<&str> = references
        .iter()
        .map(|reference| &source[*reference])
        .collect();
    assert_eq!(values, vec!["item", "item", "item", "item"]);
    for reference in &references {
        let start = u32::from(reference.start());
        assert_ne!(start, find(source, ".name") + 1);
        assert_ne!(start, find(source, "'item'") + 1);
        assert_ne!(start, find(source, "{ item:") + 2);
        assert_ne!(start, rfind(source, "{{ item") + 3);
    }
}

#[test]
fn native_vue_event_payload_types() {
    assert_eq!(native_event_payload_type("click"), "MouseEvent");
    assert_eq!(native_event_payload_type("keyup"), "KeyboardEvent");
    assert_eq!(native_event_payload_type("blur"), "FocusEvent");
    assert_eq!(event_payload_type("(value: boolean) => void"), "boolean");
    assert_eq!(event_payload_type("() => void"), "");
    assert_eq!(event_payload_type("[id: string]"), "string");
    assert_eq!(event_payload_type("[Product, boolean]"), "Product");
    assert_eq!(
        event_payload_type("(event: 'close', reason: string)"),
        "string"
    );
    assert_eq!(event_payload_type("(...args: any[]) => void"), "any");
    assert_eq!(event_payload_type("UploadTask[]"), "UploadTask[]");
    assert_eq!(event_payload_type("void"), "");
}

#[test]
fn vue_iterable_element_type_test() {
    for (value, expected) in [
        ("Product[]", "Product"),
        ("readonly Product[]", "Product"),
        ("Array<Product>", "Product"),
        (
            "ReadonlyArray<Record<string, number>>",
            "Record<string, number>",
        ),
        ("Array as PropType<Array<Product>>", "Product"),
        ("Array as unknown as PropType<ReadonlyArray<Row>>", "Row"),
        ("[Product, Category]", "Product | Category"),
        ("Record<string, Product>", "Product"),
        ("ReadonlyMap<string, Product>", "[string, Product]"),
        (
            "{ primary: Product; fallback: Category }",
            "Category | Product",
        ),
        ("number", "number"),
        ("string", "string"),
        ("Array", ""),
        ("Object", ""),
    ] {
        assert_eq!(vue_iterable_element_type(value), expected, "{value}");
    }
}

#[test]
fn vue_iterable_binding_type_distinguishes_keys_and_indexes() {
    assert_eq!(vue_iterable_binding_type("Product[]", 0), "Product");
    assert_eq!(vue_iterable_binding_type("Product[]", 1), "number");
    assert_eq!(
        vue_iterable_binding_type("Record<string, Product>", 0),
        "Product"
    );
    assert_eq!(
        vue_iterable_binding_type("Record<string, Product>", 1),
        "string"
    );
    assert_eq!(
        vue_iterable_binding_type("Record<string, Product>", 2),
        "number"
    );
    assert_eq!(
        vue_iterable_binding_type("{ primary: Product; fallback: Product }", 1),
        "string"
    );
}

#[test]
fn twig_vue_references_handle_template_literal_interpolations() {
    let source = concat!(
        r#"<div v-for="item in items" :key=""#,
        "`literal-item-${item.id}-${`nested-${item.name}`}`",
        r#"">{{ item }}</div>"#
    );
    let root = parse_twig(source);
    let target = twig_vue_binding_at_offset(&root, source, find(source, "item in items") + 1)
        .expect("target");
    let references = twig_vue_binding_references(&root, source, &target);
    assert_eq!(references.len(), 4);
    for expected in ["${item.id}", "${item.name}", "{{ item }}"] {
        let position = find(source, expected) + expected.find("item").unwrap() as u32;
        assert!(
            references.contains(&range(position, position + 4)),
            "{expected}"
        );
    }
    let literal_position = find(source, "literal-item") + "literal-".len() as u32;
    for reference in &references {
        assert_ne!(u32::from(reference.start()), literal_position);
    }
}

#[test]
fn twig_vue_expression_member_access_and_completion_cursor() {
    let source = r#"<div :title="row?.title" :data-value="row.">{{ row.name }} {{ parent.row.name }} {{ products.last().name }} {{ getProduct?.().manufacturer.name }} {{ products.get(productId).name }} {{ products[0].name }} {{ productsById?.[productId].name }} {{ 'row.fake' }}</div>"#;
    let root = parse_twig(source);
    let at = |offset: u32| twig_vue_expression_member_at_offset(&root, source, offset);

    let title = at(find(source, "row?.title") + "row?.ti".len() as u32).expect("title");
    assert_eq!(title.root, "row");
    assert_eq!(title.member, "title");

    let completion_offset = find(source, "row.\"") + "row.".len() as u32;
    let completion = at(completion_offset).expect("completion");
    assert_eq!(completion.root, "row");
    assert!(completion.member.is_empty());
    assert_eq!(
        u32::from(completion.member_range.start()),
        completion_offset
    );

    let chained =
        at(find(source, "parent.row.name") + "parent.row.na".len() as u32).expect("chained");
    assert_eq!(chained.root, "parent");
    assert_eq!(chained.member, "name");
    assert_eq!(chained.receiver.len(), 1);
    assert_eq!(chained.receiver[0].name, "row");
    assert_eq!(chained.member_path(), strings(&["row", "name"]));

    let called = at(find(source, "products.last().name") + "products.last().na".len() as u32)
        .expect("called");
    assert_eq!(called.root, "products");
    assert_eq!(called.receiver.len(), 1);
    assert_eq!(called.receiver[0].name, "last");
    assert!(called.receiver[0].called);
    assert_eq!(called.qualified_name(), "products.last().name");

    let root_call = at(find(source, "getProduct?.().manufacturer.name")
        + "getProduct?.().manufacturer.na".len() as u32)
    .expect("root call");
    assert!(root_call.root_called);
    assert_eq!(root_call.qualified_name(), "getProduct().manufacturer.name");

    let argument_call =
        at(find(source, "products.get(productId).name")
            + "products.get(productId).na".len() as u32)
        .expect("argument call");
    assert_eq!(argument_call.receiver.len(), 1);
    assert!(argument_call.receiver[0].called);

    let method = at(find(source, "last().name") + 2).expect("method");
    assert_eq!(method.member, "last");
    assert!(method.member_called);

    let computed =
        at(find(source, "products[0].name") + "products[0].na".len() as u32).expect("computed");
    assert_eq!(computed.root, "products");
    assert_eq!(computed.receiver.len(), 1);
    assert!(computed.receiver[0].indexed);
    assert_eq!(computed.receiver[0].index_expression, "0");
    assert_eq!(computed.member_path(), strings(&["[0]", "name"]));
    assert_eq!(computed.qualified_name(), "products[0].name");

    let dynamic = at(find(source, "productsById?.[productId].name")
        + "productsById?.[productId].na".len() as u32)
    .expect("dynamic");
    assert_eq!(dynamic.root, "productsById");
    assert_eq!(dynamic.receiver.len(), 1);
    assert!(dynamic.receiver[0].indexed);
    assert!(dynamic.receiver[0].optional);
    assert_eq!(dynamic.receiver[0].index_expression, "productId");
    assert_eq!(dynamic.qualified_name(), "productsById?.[productId].name");

    assert!(at(find(source, "row.fake") + "row.fa".len() as u32).is_none());
}

#[test]
fn twig_vue_call_at_offset_tracks_nested_arguments_and_filters() {
    let source = concat!(
        r#"<button @click="save(first, nested('x,y'), { tags: ['a', 'b'] }, "#,
        "`label-${format(value, 2)}`); value | default('fallback')\">Save</button>"
    );
    let root = parse_twig(source);

    let outer = twig_vue_call_at_offset(&root, source, find(source, "'b'") + 1).expect("outer");
    assert_eq!(outer.name, "save");
    assert_eq!(outer.active_argument, 2);
    assert!(!outer.filter);

    let nested = twig_vue_call_at_offset(&root, source, find(source, "'x,y'") + 2).expect("nested");
    assert_eq!(nested.name, "nested");
    assert_eq!(nested.active_argument, 0);

    let template_call =
        twig_vue_call_at_offset(&root, source, find(source, " 2)") + 1).expect("template call");
    assert_eq!(template_call.name, "format");
    assert_eq!(template_call.active_argument, 1);

    let filter =
        twig_vue_call_at_offset(&root, source, find(source, "'fallback'") + 2).expect("filter");
    assert_eq!(filter.name, "default");
    assert!(filter.filter);
}

fn twig_vue_call_argument_texts<'a>(source: &'a str, call: &TwigVueCallSite) -> Vec<&'a str> {
    call.arguments.iter().map(|range| &source[*range]).collect()
}

#[test]
fn twig_vue_calls_returns_exact_argument_ranges() {
    let source = concat!(
        r#"<button @click="save(first, nested('x,y'), { tags: ['a', 'b'] }, "#,
        "`label-${format(value, 2)}`); value | default('fallback'); 'fake('\">Save</button>"
    );
    let root = parse_twig(source);
    let calls = twig_vue_calls(&root, source);
    assert_eq!(calls.len(), 4);
    let names: Vec<&str> = calls.iter().map(|call| call.call.name.as_str()).collect();
    assert_eq!(names, vec!["save", "nested", "format", "default"]);
    assert_eq!(
        twig_vue_call_argument_texts(source, &calls[0]),
        vec![
            "first",
            "nested('x,y')",
            "{ tags: ['a', 'b'] }",
            "`label-${format(value, 2)}`"
        ]
    );
    assert_eq!(
        twig_vue_call_argument_texts(source, &calls[1]),
        vec!["'x,y'"]
    );
    assert_eq!(
        twig_vue_call_argument_texts(source, &calls[2]),
        vec!["value", "2"]
    );
    assert_eq!(
        twig_vue_call_argument_texts(source, &calls[3]),
        vec!["'fallback'"]
    );
    assert!(calls[3].call.filter);
}

#[test]
fn twig_vue_binding_members_respect_lexical_shadowing() {
    let source = "<div v-for=\"item in items\">
    {{ item.name }} {{ item?.active }} {{ item.name }}
    <span v-for=\"item in children\">{{ item.inner }}</span>
    {{ item.after }}
</div>";
    let root = parse_twig(source);
    let outer = twig_vue_binding_at_offset(&root, source, find(source, "item in items") + 1)
        .expect("outer");
    let members = twig_vue_binding_members(&root, source, &outer);
    assert_eq!(
        vue_member_names(&members),
        strings(&["active", "after", "name"])
    );

    let name_references = twig_vue_binding_member_references(&root, source, &outer, "name");
    assert_eq!(name_references.len(), 2);
    for reference in name_references {
        assert_eq!(&source[reference], "name");
    }
}

/// Resolving against precomputed document bindings matches the per-offset
/// scan at every offset, including nested `v-for` shadowing, iterable
/// expressions and `$event` handlers.
#[test]
fn twig_vue_binding_at_offset_in_matches_per_offset_scan() {
    let source = r#"<sw-list v-for="(item, index) in items" @change="onChange($event, item)">
    <div v-for="item in item.children" :title="item.label + index">{{ item.name }}</div>
    <sw-field @update:value="value => save($event, value, index)" :value="index"></sw-field>
</sw-list>
<p>{{ item }} {{ $event }}</p>"#;
    let root = parse_twig(source);
    let bindings = twig_vue_document_bindings(&root, source);
    assert!(!bindings.is_empty());
    for offset in 0..=source.len() as u32 {
        assert_eq!(
            twig_vue_binding_at_offset_in(&bindings, &root, source, offset),
            twig_vue_binding_at_offset(&root, source, offset),
            "offset {offset}"
        );
    }
}
