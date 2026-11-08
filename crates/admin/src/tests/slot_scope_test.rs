#![allow(unused_imports)]
use super::support::*;
use crate::*;

use swlsp_indexer::Indexer;

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn binding(member: &str, local: &str, whole: bool) -> TwigSlotBinding {
    TwigSlotBinding {
        member_name: member.to_owned(),
        local_name: local.to_owned(),
        whole_object: whole,
        ..Default::default()
    }
}

fn node_at(root: &SyntaxNode, offset: u32) -> SyntaxNode {
    root.node_at(TextSize::from(offset))
        .expect("node at offset")
}

#[test]
fn parse_scoped_slot_bindings_test() {
    assert_eq!(
        parse_scoped_slot_bindings("{ item: row, index = 0, active, ...rest }"),
        vec![
            binding("item", "row", false),
            binding("index", "index", false),
            binding("active", "active", false),
            binding("", "rest", true),
        ]
    );
    assert_eq!(
        parse_scoped_slot_bindings("props"),
        vec![binding("", "props", true)]
    );
    assert_eq!(
        sorted(object_binding_names("{ active: activeItem, label }")),
        sorted(strings(&["active", "label"]))
    );
    assert_eq!(
        sorted(object_binding_names(
            "{
			item,
			isInlineEdit: (isInlineEdit(item) && item.active),
			label: getLabel(item, { translated: true }),
			...forwarded,
		}"
        )),
        sorted(strings(&["item", "isInlineEdit", "label"]))
    );
    let (names, complete) = vue_object_binding_names("{ label, count: value }");
    assert!(complete);
    assert_eq!(sorted(names), sorted(strings(&["label", "count"])));
    let (names, complete) = vue_object_binding_names("{ label, ...forwarded }");
    assert!(!complete);
    assert_eq!(names, strings(&["label"]));
}

#[test]
fn nested_scoped_slots_retain_outer_bindings() {
    let source = r#"<sw-parent><template #outer="props"><sw-child><template #inner="{ currentValue }"><span :title="props.title + currentValue"></span></template></sw-child></template></sw-parent>"#;
    let root = parse_twig(source);
    let offset = source.rfind("props.title").unwrap() as u32 + 1;
    let scopes = twig_scoped_slots_at_offset(&root, offset);
    assert_eq!(scopes.len(), 2);
    assert_eq!(scopes[0].bindings.len(), 1);
    assert_eq!(scopes[0].bindings[0].local_name, "props");
    assert_eq!(scopes[1].bindings.len(), 1);
    assert_eq!(scopes[1].bindings[0].local_name, "currentValue");

    let mut props_identifier = TwigVueMember::default();
    for identifier in twig_vue_expression_root_identifiers(&root, source) {
        if identifier.name == "props" {
            props_identifier = identifier;
        }
    }
    assert_eq!(props_identifier.name, "props");
    assert!(twig_vue_root_identifier_is_local(
        &root,
        source,
        &props_identifier
    ));
}

#[test]
fn expression_root_identifier_at_offset_test() {
    let source = "item.name + ({ name: item }).name + name";
    let property = (source.find(".name").unwrap() + ".na".len()) as u32;
    assert!(expression_root_identifier_at_offset(source, property).is_none());
    let key = (source.find("{ name").unwrap() + "{ na".len()) as u32;
    assert!(expression_root_identifier_at_offset(source, key).is_none());
    let root = source.rfind("name").unwrap() as u32 + 1;
    let (name, _) = expression_root_identifier_at_offset(source, root).expect("root identifier");
    assert_eq!(name, "name");
}

#[test]
fn twig_scoped_slot_at_offset_is_lexical_and_innermost() {
    let source = "<sw-grid>
    <template #result-item=\"{ item: row, index }\">
        {{ row }}
        <span :title=\"index\"></span>
        <sw-card>
            <template #header=\"{ title }\">{{ title }}</template>
        </sw-card>
    </template>
</sw-grid>
{{ row }}";
    let root = parse_twig(source);
    let row_offset = (source.find("{{ row }}").unwrap() + "{{ r".len()) as u32;
    let scope = twig_scoped_slot_at_offset(&root, row_offset).expect("scope");
    assert_eq!(scope.component_name, "sw-grid");
    assert_eq!(scope.slot_name, "result-item");
    assert_eq!(scope.bindings[0].local_name, "row");
    assert_eq!(&source[scope.bindings[0].member_range], "item");
    assert_eq!(&source[scope.bindings[0].local_range], "row");

    let index_offset = (source.find(":title=\"index\"").unwrap() + ":title=\"ind".len()) as u32;
    let index_node = node_at(&root, index_offset);
    assert!(is_twig_vue_expression_at(&index_node, index_offset));
    let (name, _) = identifier_at_offset(source, index_offset).expect("identifier");
    assert_eq!(name, "index");

    let nested_offset = (source.rfind("{{ title }}").unwrap() + "{{ ti".len()) as u32;
    let nested = twig_scoped_slot_at_offset(&root, nested_offset).expect("nested");
    assert_eq!(nested.component_name, "sw-card");
    assert_eq!(nested.slot_name, "header");

    let outside_offset = (source.rfind("{{ row }}").unwrap() + "{{ r".len()) as u32;
    assert!(twig_scoped_slot_at_offset(&root, outside_offset).is_none());
}

#[test]
fn resolve_twig_scoped_slot_binding_test() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    index
        .save_component(&VueComponent {
            name: "sw-grid".to_owned(),
            slots: vec![VueComponentSlot {
                name: "result-item".to_owned(),
                file_path: "/component.html.twig".to_owned(),
                line: 8,
                members: vec![VueComponentSlotMember {
                    name: "item".to_owned(),
                    type_: "Entity".to_owned(),
                    file_path: "/component.html.twig".to_owned(),
                    line: 9,
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    let source =
        r#"<sw-grid><template #result-item="{ item: row }">{{ row }}</template></sw-grid>"#;
    let root = parse_twig(source);
    let offset = source.rfind("row").unwrap() as u32 + 1;
    let resolved = index
        .resolve_twig_scoped_slot_binding(&root, &node_at(&root, offset), source, offset, "")
        .unwrap()
        .expect("resolved");
    assert_eq!(resolved.identifier, "row");
    assert_eq!(resolved.binding.member_name, "item");
    assert!(resolved.member_found);
    assert_eq!(resolved.member.type_, "Entity");
    index.close().unwrap();
}

#[test]
fn scoped_slot_locals_work_for_default_and_dynamic_unknown_contracts() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    for (name, source, local, component, slot) in [
        (
            "default slot on component",
            "<router-view v-slot=\"{ Component }\">{{ Component }}</router-view>",
            "Component",
            "router-view",
            "default",
        ),
        (
            "dynamic slot",
            "<sw-grid><template #[columnName]=\"{ item }\">{{ item }}</template></sw-grid>",
            "item",
            "sw-grid",
            "",
        ),
    ] {
        let root = parse_twig(source);
        let offset = source.rfind(local).unwrap() as u32 + 1;
        let scope =
            twig_scoped_slot_at_offset(&root, offset).unwrap_or_else(|| panic!("{name}: scope"));
        assert_eq!(scope.component_name, component, "{name}");
        assert_eq!(scope.slot_name, slot, "{name}");
        let resolved = index
            .resolve_twig_scoped_slot_binding(&root, &node_at(&root, offset), source, offset, "")
            .unwrap()
            .unwrap_or_else(|| panic!("{name}: resolved"));
        assert_eq!(resolved.identifier, local, "{name}");
        assert!(!resolved.member_found, "{name}");
    }
    index.close().unwrap();
}

#[test]
fn resolve_twig_scoped_slot_uses_dynamic_component_contract_intersection() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    let member = |name: &str, type_: &str, file: &str, line: u32| VueComponentSlotMember {
        name: name.to_owned(),
        type_: type_.to_owned(),
        file_path: file.to_owned(),
        line,
        ..Default::default()
    };
    for component in [
        VueComponent {
            name: "sw-card-a".to_owned(),
            file_path: "/a/index.ts".to_owned(),
            slots: vec![VueComponentSlot {
                name: "row".to_owned(),
                members_complete: true,
                members: vec![
                    member("item", "Product", "/a/card.twig", 2),
                    member("onlyA", "string", "/a/card.twig", 3),
                ],
                ..Default::default()
            }],
            ..Default::default()
        },
        VueComponent {
            name: "sw-card-b".to_owned(),
            file_path: "/b/index.ts".to_owned(),
            slots: vec![VueComponentSlot {
                name: "row".to_owned(),
                members_complete: true,
                members: vec![member("item", "Category", "/b/card.twig", 5)],
                ..Default::default()
            }],
            ..Default::default()
        },
    ] {
        index.save_component(&component).unwrap();
    }
    let source = r#"<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item, onlyA }">{{ item }}</template></component>"#;
    let root = parse_twig(source);
    let offset = source.rfind("item").unwrap() as u32 + 1;
    let scope = twig_scoped_slot_at_offset(&root, offset).expect("scope");
    assert!(scope.component_name.is_empty());
    assert_ne!(scope.starting_tag_range.len(), TextSize::from(0));

    let resolved = index
        .resolve_twig_scoped_slot(&root, offset, "")
        .unwrap()
        .expect("resolved");
    assert!(resolved.contracts_complete);
    assert_eq!(
        sorted(resolved.component_names()),
        strings(&["sw-card-a", "sw-card-b"])
    );
    let item = resolved.slot.member("item").expect("item");
    assert_eq!(item.type_, "Product | Category");
    assert!(resolved.slot.member("onlyA").is_none());
    assert!(resolved.slot.members_complete);

    let binding = index
        .resolve_twig_scoped_slot_binding(&root, &node_at(&root, offset), source, offset, "")
        .unwrap()
        .expect("binding");
    assert!(binding.member_found);
    assert_eq!(binding.members.len(), 2);
    index.close().unwrap();
}

#[test]
fn resolve_twig_scoped_slot_uses_inferred_dynamic_component_owner() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    let template_path = "/administration/consumer.html.twig";
    let card = |name: &str, file: &str| VueComponent {
        name: name.to_owned(),
        file_path: file.to_owned(),
        slots: vec![VueComponentSlot {
            name: "row".to_owned(),
            members: vec![VueComponentSlotMember {
                name: "item".to_owned(),
                type_: "Entity".to_owned(),
                ..Default::default()
            }],
            ..Default::default()
        }],
        ..Default::default()
    };
    for component in [
        card("sw-card-a", "/a/index.ts"),
        card("sw-card-b", "/b/index.ts"),
        VueComponent {
            name: "sw-host".to_owned(),
            file_path: "/host/index.ts".to_owned(),
            template_path: template_path.to_owned(),
            members: vec![VueComponentMember {
                name: "dynamicCard".to_owned(),
                kind: VueComponentMemberKind::Computed,
                return_expressions: strings(&["'sw-card-a'", "'sw-card-b'"]),
                returns_complete: true,
                ..Default::default()
            }],
            ..Default::default()
        },
    ] {
        index.save_component(&component).unwrap();
    }
    let source = r#"<component :is="dynamicCard"><template #row="{ item }">{{ item }}</template></component>"#;
    let root = parse_twig(source);
    let offset = source.rfind("item").unwrap() as u32 + 1;
    let without_path = index
        .resolve_twig_scoped_slot(&root, offset, "")
        .unwrap()
        .expect("without path");
    assert!(!without_path.contracts_complete);

    let resolved = index
        .resolve_twig_scoped_slot(&root, offset, template_path)
        .unwrap()
        .expect("resolved");
    assert!(resolved.contracts_complete);
    assert!(resolved.slot.member("item").is_some());
    index.close().unwrap();
}

#[test]
fn twig_scoped_slot_binding_references_respect_shadowing() {
    let dir = temp_dir();
    let index = new_indexer(dir.path());
    index
        .save_component(&VueComponent {
            name: "sw-grid".to_owned(),
            file_path: "/grid.ts".to_owned(),
            slots: vec![VueComponentSlot {
                name: "row".to_owned(),
                members: vec![VueComponentSlotMember {
                    name: "item".to_owned(),
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    let source = r#"<sw-grid><template #row="{ item: row }">{{ row }}<div v-for="row in rows">{{ row }}</div>{{ row.name }}</template></sw-grid>"#;
    let root = parse_twig(source);
    let offset = source.rfind("row.name").unwrap() as u32 + 1;
    let resolved = index
        .resolve_twig_scoped_slot_binding(&root, &node_at(&root, offset), source, offset, "")
        .unwrap()
        .expect("resolved");
    let ranges = twig_scoped_slot_binding_references(&root, source, &resolved);
    assert_eq!(ranges.len(), 3);
    for range in ranges {
        assert_eq!(&source[range], "row");
    }
    index.close().unwrap();
}
