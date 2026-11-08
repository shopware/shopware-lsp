//! Port of Go `hierarchy_test.go`.

use super::inheritance::{backfill_free, field, php_ok, s};
use crate::*;

#[test]
fn version_aware_hierarchy_renders_imports_and_round_trips() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example\\Content\\Node".into(),
        class_name: "Node".into(),
        entity_name: "acme_node".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                api_aware: true,
                ..field("version", FIELD_VERSION)
            },
            FieldSpec {
                property_name: "children".into(),
                api_aware: true,
                association_api_aware: true,
                hierarchy_children_api_aware: true,
                hierarchy_version_api_aware: true,
                ..field("hierarchy", FIELD_HIERARCHY)
            },
        ],
        ..EntitySpec::default()
    });
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );
    assert_eq!(
        3,
        spec.fields.len(),
        "the parent version is owned by the hierarchy row"
    );
    let hierarchy = &spec.fields[2];
    assert!(hierarchy.hierarchy_version_aware);
    assert_eq!(spec.definition_class, hierarchy.target_definition_class);
    assert_eq!("parent_id", hierarchy.storage_name);
    assert_eq!("parent", hierarchy.hierarchy_parent_property);

    let definition = render_definition(&spec).unwrap();
    assert!(php_ok(&definition), "{definition}");
    assert!(definition.contains("new ParentFkField(NodeDefinition::class)"));
    assert!(
        definition
            .contains("new ReferenceVersionField(NodeDefinition::class, 'parent_version_id')")
    );
    assert!(definition.contains("new ParentAssociationField(NodeDefinition::class, 'id')"));
    assert!(definition.contains("new ChildrenAssociationField(NodeDefinition::class)"));
    assert_eq!(5, definition.matches("new ApiAware()").count());

    let entity_source = render_entity(&spec).unwrap();
    assert!(php_ok(&entity_source), "{entity_source}");
    assert!(entity_source.contains("protected ?string $parentId = null;"));
    assert!(entity_source.contains("protected ?NodeEntity $parent = null;"));
    assert!(entity_source.contains("protected ?NodeCollection $children = null;"));

    let spec_clone = spec.clone();
    let lookup = move |class: &str| -> Option<RelationTarget> {
        (class == spec_clone.definition_class).then(|| RelationTarget {
            definition_class: spec_clone.definition_class.clone(),
            entity_class: spec_clone.entity_class.clone(),
            collection_class: spec_clone.collection_class.clone(),
            entity_name: spec_clone.entity_name.clone(),
            version_aware: true,
            ..RelationTarget::default()
        })
    };
    let imported = import_definition(&definition, Some(&lookup)).unwrap();
    assert_eq!(3, imported.fields.len());
    assert_eq!(FIELD_HIERARCHY, imported.fields[2].kind);
    assert!(imported.fields[2].hierarchy_version_aware);
    assert!(imported.fields[2].api_aware);
    assert!(imported.fields[2].association_api_aware);
    assert!(imported.fields[2].hierarchy_children_api_aware);
    assert!(imported.fields[2].hierarchy_version_api_aware);

    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(before, after);
    assert_eq!(
        Column {
            name: "parent_id".into(),
            sql_type: "BINARY(16)".into(),
            ..Column::default()
        },
        before.columns["parent_id"]
    );
    assert_eq!(
        Column {
            name: "parent_version_id".into(),
            sql_type: "BINARY(16)".into(),
            ..Column::default()
        },
        before.columns["parent_version_id"]
    );
    let fk = &before.foreign_keys["fk.acme_node.parent_id"];
    assert_eq!(s(&["parent_id", "parent_version_id"]), fk.columns);
    assert_eq!(s(&["id", "version_id"]), fk.reference_columns);
    assert_eq!(DELETE_CASCADE, fk.on_delete);
    assert_eq!(
        s(&["parent_id", "parent_version_id"]),
        before.indexes["idx.acme_node.parent_id"].columns
    );
    assert!(index_specs_from_entity(&spec, &before).is_empty());
}

#[test]
fn unversioned_hierarchy_omits_parent_version() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Tree".into(),
        entity_name: "acme_tree".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                property_name: "queries".into(),
                ..field("hierarchy", FIELD_HIERARCHY)
            },
        ],
        ..EntitySpec::default()
    });
    let definition = render_definition(&spec).unwrap();
    assert!(definition.contains("new ChildrenAssociationField(TreeDefinition::class, 'queries')"));
    assert!(!definition.contains("parent_version_id"));
    let entity = schema_from_spec(&spec).unwrap();
    assert!(!entity.columns.contains_key("parent_version_id"));
    assert!(
        entity.foreign_keys["fk.acme_tree.parent_id"]
            .columns
            .is_empty()
    );
}

#[test]
fn hierarchy_validation_rejects_duplicate_and_non_entity_ownership() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        definition_kind: DEFINITION_MAPPING.into(),
        namespace: "Acme\\Example".into(),
        class_name: "Tree".into(),
        entity_name: "acme_tree".into(),
        fields: vec![
            field("first", FIELD_HIERARCHY),
            field("second", FIELD_HIERARCHY),
        ],
        ..EntitySpec::default()
    });
    let codes: Vec<String> = validate_spec(&spec)
        .into_iter()
        .map(|issue| issue.code)
        .collect();
    assert!(codes.contains(&"entity.hierarchy.owner.unsupported".to_owned()));
    assert!(codes.contains(&"entity.hierarchy.duplicate".to_owned()));
}
