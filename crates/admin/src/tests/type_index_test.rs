#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn by_name(members: &[TwigVueMember]) -> HashMap<String, TwigVueMember> {
    members
        .iter()
        .map(|member| (member.name.clone(), member.clone()))
        .collect()
}

#[test]
fn parse_admin_type_file_retains_structural_declarations() {
    let path = "/project/Resources/app/administration/src/component/index.ts";
    let source = r#"
import type { External as Imported } from './types';

interface Base<T> {
    id: T;
}

interface Row extends Base<string> {
    /** Whether this row is currently visible. */
    visible: boolean;
    /**
     * Label information rendered by the row.
     * @default undefined
     */
    nested?: { label: string };
}

type Result<T> = Imported & {
    value: T;
};

interface OpenRow {
    fixed: string;
    [key: string]: unknown;
}
"#;
    let file = parse_admin_type_file(path, source, None);
    assert_eq!(file.imports.len(), 1);
    assert_eq!(file.imports[0].local_name, "Imported");
    assert_eq!(file.imports[0].imported_name, "External");
    assert_eq!(file.declarations.len(), 4);

    let row = admin_type_declaration_named(&file, "Row").expect("Row");
    assert_eq!(row.extends, vec!["Base<string>"]);
    assert_eq!(row.members.len(), 2);
    assert_eq!(row.members[0].name, "nested");
    assert_eq!(row.members[0].type_, "{ label: string }");
    assert_eq!(
        row.members[0].documentation,
        "Label information rendered by the row."
    );
    assert_eq!(row.members[0].definition_path, path);
    assert!(row.members[0].definition_line > 0);
    let nested_start = source.find("nested?:").unwrap() as u32;
    let (nested_line, nested_character) = LineIndex::new(source).position_utf16(nested_start);
    assert_eq!(
        row.members[0].definition_range,
        AdminSourceRange {
            start_line: nested_line,
            start_character: nested_character,
            end_line: nested_line,
            end_character: nested_character + "nested".len() as u32,
            declaration: true,
            identifier: true,
            ..Default::default()
        }
    );
    assert_eq!(
        row.members[1].documentation,
        "Whether this row is currently visible."
    );

    let result = admin_type_declaration_named(&file, "Result").expect("Result");
    assert_eq!(result.parameters, vec!["T"]);
    assert!(result.alias.contains("Imported"));
    assert!(result.alias.contains("value: T"));
    let open_row = admin_type_declaration_named(&file, "OpenRow").expect("OpenRow");
    assert!(open_row.open);
    assert!(vue_member_names(&open_row.members).contains(&"fixed".to_owned()));
}

#[test]
fn vue_type_members_accept_comma_separated_object_fields() {
    let members = vue_type_members(
        "{
		name?: string,
		distinguishableName?: string,
		meta: Record<string, { label: string, count: number }>,
		format: (value: string, options: { locale: string, currency: string }) => string
	}",
    );
    let by_name = by_name(&members);
    assert_eq!(by_name.len(), 4);
    assert_eq!(by_name["name"].type_, "string");
    assert_eq!(by_name["distinguishableName"].type_, "string");
    assert_eq!(
        by_name["meta"].type_,
        "Record<string, { label: string, count: number }>"
    );
    assert_eq!(
        by_name["format"].type_,
        "(value: string, options: { locale: string, currency: string }) => string"
    );
}

#[test]
fn admin_type_union_includes_branch_specific_object_fields() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let shape = idx
        .resolve_vue_type(
            "{ id: string; title: string } | { id: string; title: string; privilege: string }",
            "/project/component.ts",
            &[],
        )
        .expect("shape");
    assert!(shape.complete);
    let privilege = twig_vue_member_named(&shape.members, "privilege").expect("privilege");
    assert_eq!(privilege.type_, "string | undefined");
}

#[test]
fn admin_type_index_resolves_local_imported_inherited_and_generic_shapes() {
    let root = temp_dir();
    let admin_root = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src",
    );
    let idx = new_indexer(&root.path().join("cache"));

    let types_path = gopath::join2(&admin_root, "component/row.types.ts");
    index_source(
        &idx,
        &types_path,
        r#"
export interface External<T> {
    id: T;
    child: { label: string; count: number };
}
"#,
    );
    let component_path = gopath::join2(&admin_root, "component/index.ts");
    index_source(
        &idx,
        &component_path,
        r#"
import type { External as BaseRow } from './row.types';
interface AddressRow extends BaseRow<string> {
    visible: boolean;
}
type OptionalAddressRow = AddressRow | null;
interface OpenAddressRow {
    visible: boolean;
    [key: string]: unknown;
}
type OpenAddressAlias = {
    label: string;
    [name: string]: unknown;
};
"#,
    );
    let indexed_file = idx
        .admin_type_file(&component_path)
        .expect("type file")
        .expect("indexed");
    assert_eq!(indexed_file.declarations.len(), 4);
    assert_eq!(indexed_file.imports.len(), 1);

    let direct_shape = idx
        .resolve_vue_type("AddressRow", &component_path, &[])
        .expect("shape");
    assert!(direct_shape.complete);
    let shape = idx
        .resolve_vue_type("OptionalAddressRow", &component_path, &[])
        .expect("shape");
    assert!(shape.complete);
    let members = by_name(&shape.members);
    assert_eq!(members["id"].type_, "string");
    assert_eq!(members["child"].type_, "{ label: string; count: number }");
    assert_eq!(members["visible"].type_, "boolean");
    assert_eq!(members["id"].definition_path, types_path);
    assert_eq!(members["visible"].definition_path, component_path);
    for open_type in ["OpenAddressRow", "OpenAddressAlias"] {
        let open_shape = idx
            .resolve_vue_type(open_type, &component_path, &[])
            .expect("shape");
        assert!(!open_shape.complete, "{open_type}");
        assert!(!open_shape.members.is_empty(), "{open_type}");
    }

    let child = idx
        .resolve_vue_type(&members["child"].type_, &types_path, &[])
        .expect("shape");
    assert!(child.complete);
    assert_eq!(child.members.len(), 2);
    let mut names = vec![child.members[0].name.clone(), child.members[1].name.clone()];
    names.sort();
    assert_eq!(names, vec!["count", "label"]);
}

#[test]
fn admin_type_index_resolves_shopware_entity_schema_and_collection() {
    let root = temp_dir();
    let admin_root = path_string(
        root.path(),
        "src/Administration/Resources/app/administration/src",
    );
    let idx = new_indexer(&root.path().join("cache"));

    let schema_path = gopath::join2(&admin_root, "entity-schema-definition.d.ts");
    index_source(
        &idx,
        &schema_path,
        r#"
declare namespace EntitySchema {
    interface product {
        id: string;
        name: string;
        manufacturer?: Entity<'product_manufacturer'>;
    }
    interface product_manufacturer {
        id: string;
        name: string;
    }
}
"#,
    );
    let augmentation_path = gopath::join2(&admin_root, "plugin/entity-extension.d.ts");
    index_source(
        &idx,
        &augmentation_path,
        r#"
declare namespace EntitySchema {
    interface product { customSearchKeyword?: string; }
}
"#,
    );

    let component_path = gopath::join2(&admin_root, "component/index.ts");
    let shape = idx
        .resolve_vue_type("Entity<'product'>", &component_path, &[])
        .expect("shape");
    assert!(shape.complete);
    let members = by_name(&shape.members);
    assert_eq!(members["name"].type_, "string");
    assert_eq!(
        members["manufacturer"].type_,
        "Entity<'product_manufacturer'>"
    );
    assert_eq!(members["customSearchKeyword"].type_, "string");
    assert_eq!(members["getEntityName"].type_, "() => string");
    assert_eq!(members["manufacturer"].definition_path, schema_path);
    assert_eq!(
        members["customSearchKeyword"].definition_path,
        augmentation_path
    );
    let prop_shape = idx
        .resolve_vue_type(
            "Object as PropType<Entity<'product'>>",
            &component_path,
            &[],
        )
        .expect("shape");
    assert!(prop_shape.complete);
    assert!(vue_member_names(&prop_shape.members).contains(&"manufacturer".to_owned()));

    let manufacturer = idx
        .resolve_vue_type(
            &members["manufacturer"].type_,
            &members["manufacturer"].definition_path,
            &[],
        )
        .expect("shape");
    assert!(manufacturer.complete);
    assert!(vue_member_names(&manufacturer.members).contains(&"name".to_owned()));

    let collection = idx
        .resolve_vue_type(
            "EntitySchema.EntityCollection<'product'>",
            &schema_path,
            &[],
        )
        .expect("shape");
    assert!(!collection.complete);
    let collection_names = vue_member_names(&collection.members);
    assert!(collection_names.contains(&"total".to_owned()));
    assert!(collection_names.contains(&"filter".to_owned()));
    let collection_filter = twig_vue_member_named(&collection.members, "filter").expect("filter");
    assert_eq!(
        collection_filter.type_,
        "(predicate: Function) => Array<Entity<'product'>>"
    );
    let collection_find = twig_vue_member_named(&collection.members, "find").expect("find");
    assert_eq!(
        collection_find.type_,
        "(predicate: Function) => Entity<'product'> | undefined"
    );
    assert_eq!(
        vue_iterable_element_type("EntityCollection<'product'> | null"),
        "Entity<'product'>"
    );
    let string_shape = idx
        .resolve_vue_type("string", &schema_path, &[])
        .expect("shape");
    assert!(!string_shape.complete);
    let string_names = vue_member_names(&string_shape.members);
    assert!(string_names.contains(&"length".to_owned()));
    assert!(string_names.contains(&"trim".to_owned()));
    let array_shape = idx
        .resolve_vue_type("Entity<'product'>[]", &schema_path, &[])
        .expect("shape");
    assert!(!array_shape.complete);
    let array_names = vue_member_names(&array_shape.members);
    assert!(array_names.contains(&"length".to_owned()));
    assert!(array_names.contains(&"find".to_owned()));
    let record_shape = idx
        .resolve_vue_type("Record<string, Entity<'product'>>", &schema_path, &[])
        .expect("shape");
    assert!(!record_shape.complete);
    assert!(record_shape.members.is_empty());
    let function_shape = idx
        .resolve_vue_type("Function", &schema_path, &[])
        .expect("shape");
    assert!(!function_shape.complete);
    assert!(vue_member_names(&function_shape.members).contains(&"call".to_owned()));
    for open_type in [
        "unknown",
        "any",
        "object",
        "Object",
        "Date",
        "RegExp",
        "null",
        "undefined",
        "void",
        "never",
    ] {
        let open_shape = idx
            .resolve_vue_type(open_type, &schema_path, &[])
            .expect("shape");
        assert!(
            !open_shape.complete,
            "{open_type} must not drive closed-world markup diagnostics"
        );
    }
}
