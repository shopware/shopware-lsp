use std::collections::BTreeMap;

use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

#[test]
fn snapshot_canonical_round_trip_and_graph() {
    let first = Snapshot {
        kind: s(SNAPSHOT_MIGRATION),
        plugin: PluginIdentity {
            composer_name: s("acme/example"),
        },
        schema: empty_schema(),
        ..Snapshot::default()
    }
    .seal()
    .unwrap();
    let second = Snapshot {
        kind: s(SNAPSHOT_MIGRATION),
        parents: vec![first.id.clone()],
        plugin: first.plugin.clone(),
        schema: empty_schema(),
        decisions: vec![Decision {
            kind: s("columnCreate"),
            entity: s("example"),
            to: s("name"),
            ..Decision::default()
        }],
        ..Snapshot::default()
    }
    .seal()
    .unwrap();

    let encoded = marshal_snapshot(&second).unwrap();
    assert_eq!(*encoded.last().unwrap(), b'\n');
    let parsed = parse_snapshot(&encoded).unwrap();
    assert_eq!(second.id, parsed.id);

    let graph = build_snapshot_graph(&[
        SnapshotFile {
            path: s("first.json"),
            snapshot: first,
        },
        SnapshotFile {
            path: s("second.json"),
            snapshot: second.clone(),
        },
    ])
    .unwrap();
    assert_eq!(graph.leaves.len(), 1);
    assert_eq!(second.id, graph.leaves[0].snapshot.id);
}

fn reencode(snapshot: &Snapshot, key: &str, value: serde_json::Value) -> Vec<u8> {
    let encoded = gojson::to_vec(snapshot).unwrap();
    let mut payload: serde_json::Map<String, serde_json::Value> =
        serde_json::from_slice(&encoded).unwrap();
    payload.insert(s(key), value);
    serde_json::to_vec(&payload).unwrap()
}

#[test]
fn snapshot_rejects_content_with_stale_id() {
    let snapshot = Snapshot {
        kind: s(SNAPSHOT_BASELINE),
        schema: empty_schema(),
        ..Snapshot::default()
    }
    .seal()
    .unwrap();
    let encoded = reencode(&snapshot, "kind", serde_json::json!("merge"));
    let error = parse_snapshot(&encoded).unwrap_err();
    assert!(error.to_string().contains("does not match"), "{error}");
}

#[test]
fn schema_from_spec_ignores_php_namespace_moves() {
    let before_spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Old\\Content\\Blog"),
        class_name: s("Blog"),
        entity_name: s("acme_blog"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("name"),
                kind: s(FIELD_STRING),
                property_name: s("name"),
                storage_name: s("name"),
                required: true,
                api_aware: true,
                search_ranking: 500.0,
                preserved_flags: vec![s("new AllowHtml()")],
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("products"),
                kind: s(FIELD_ONE_TO_MANY),
                property_name: s("products"),
                target_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
                target_collection_class: s("Shopware\\Core\\Content\\Product\\ProductCollection"),
                target_entity_name: s("product"),
                reference_storage_name: s("blog_id"),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("custom"),
                kind: s(FIELD_LOCKED),
                raw: s("customFieldFactory()"),
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let mut after_spec = before_spec.clone();
    after_spec.namespace = s("Acme\\New\\Domain\\Blog");
    after_spec.definition_class.clear();
    after_spec.entity_class.clear();
    after_spec.collection_class.clear();
    let after_spec = complete_spec(after_spec);

    let before_entity = schema_from_spec(&before_spec).unwrap();
    let after_entity = schema_from_spec(&after_spec).unwrap();
    assert_eq!(before_entity, after_entity);

    let mut before = empty_schema();
    before
        .entities
        .insert(before_entity.name.clone(), before_entity);
    let mut after = empty_schema();
    after
        .entities
        .insert(after_entity.name.clone(), after_entity);
    assert!(!diff_schemas(&before, &after).database_changed());
}

#[test]
fn snapshot_serializes_database_metadata_only() {
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example\\Content\\Blog"),
        class_name: s("Blog"),
        entity_name: s("acme_blog"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("name"),
                kind: s(FIELD_STRING),
                property_name: s("displayName"),
                storage_name: s("name"),
                api_aware: true,
                search_ranking: 500.0,
                preserved_flags: vec![s("new AllowHtml()")],
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("custom"),
                kind: s(FIELD_LOCKED),
                raw: s("customFieldFactory()"),
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let entity = schema_from_spec(&spec).unwrap();
    let mut schema = empty_schema();
    schema.entities.insert(entity.name.clone(), entity);
    let snapshot = Snapshot {
        kind: s(SNAPSHOT_MIGRATION),
        plugin: PluginIdentity {
            composer_name: s("acme/example"),
        },
        migrations: vec![MigrationReference {
            path: s("src/Migration/Migration1.php"),
            timestamp: 1,
            sha256: "a".repeat(64),
        }],
        schema,
        ..Snapshot::default()
    }
    .seal()
    .unwrap();
    let serialized = String::from_utf8(marshal_snapshot(&snapshot).unwrap()).unwrap();
    assert!(serialized.contains("\"formatVersion\": 2"));
    assert!(serialized.contains("\"composerName\": \"acme/example\""));
    assert!(serialized.contains("\"sqlType\": \"VARCHAR(255)\""));
    for key in [
        "\"pluginClass\"",
        "\"class\"",
        "\"definitionClass\"",
        "\"entityClass\"",
        "\"collectionClass\"",
        "\"propertyName\"",
        "\"kind\": \"string\"",
        "\"apiAware\"",
        "\"searchRanking\"",
        "\"flags\"",
        "\"associations\"",
        "\"opaqueFields\"",
        "customFieldFactory",
        "Acme\\\\Example",
    ] {
        assert!(!serialized.contains(key), "{key}");
    }
}

#[test]
fn snapshot_rejects_previous_format() {
    let snapshot = Snapshot {
        kind: s(SNAPSHOT_BASELINE),
        schema: empty_schema(),
        ..Snapshot::default()
    }
    .seal()
    .unwrap();
    let encoded = reencode(&snapshot, "formatVersion", serde_json::json!(1)); // Go encodes float64(1) as `1`.
    let error = parse_snapshot(&encoded).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("unsupported entity snapshot format 1"),
        "{error}"
    );
}

fn single(name: &str, column: Column) -> Schema {
    let mut schema = empty_schema();
    schema.entities.insert(
        s(name),
        Entity {
            name: s(name),
            columns: BTreeMap::from([(column.name.clone(), column)]),
            ..Entity::default()
        },
    );
    schema
}

#[test]
fn schema_diff_requires_explicit_rename_resolution() {
    let before = single(
        "acme",
        Column {
            name: s("old_name"),
            sql_type: s("VARCHAR(255)"),
            not_null: true,
            ..Column::default()
        },
    );
    let after = single(
        "acme",
        Column {
            name: s("new_name"),
            sql_type: s("VARCHAR(500)"),
            not_null: true,
            ..Column::default()
        },
    );
    let diff = diff_schemas(&before, &after);
    assert_eq!(diff.rename_questions.len(), 1);
    let error = resolve_rename_questions(diff.clone(), &[]).unwrap_err();
    assert!(error.to_string().contains("unresolved"));
    let (_, decisions) = resolve_rename_questions(
        diff,
        &[Decision {
            kind: s("columnRename"),
            entity: s("acme"),
            from: s("old_name"),
            to: s("new_name"),
            ..Decision::default()
        }],
    )
    .unwrap();
    assert_eq!(decisions.len(), 1);
}

#[test]
fn schema_diff_requires_explicit_entity_rename_resolution() {
    let id = Column {
        name: s("id"),
        sql_type: s("BINARY(16)"),
        not_null: true,
        primary_key: true,
        ..Column::default()
    };
    let before = single("old_table", id.clone());
    let after = single("new_table", id);

    let diff = diff_schemas(&before, &after);
    assert_eq!(diff.entity_rename_questions.len(), 1);
    assert_eq!(diff.entity_rename_questions[0].added, "new_table");
    assert_eq!(
        diff.entity_rename_questions[0].candidates[0].from,
        "old_table"
    );
    let (_, _, decisions) = resolve_schema_diff(&before, &after, &[]);
    assert!(
        decisions
            .unwrap_err()
            .to_string()
            .contains("unresolved entity change")
    );

    let (resolved_previous, resolved, decisions) = resolve_schema_diff(
        &before,
        &after,
        &[Decision {
            kind: s("entityRename"),
            entity: s("new_table"),
            from: s("old_table"),
            to: s("new_table"),
            ..Decision::default()
        }],
    );
    let decisions = decisions.unwrap();
    assert!(resolved_previous.entities.contains_key("new_table"));
    assert!(!resolved_previous.entities.contains_key("old_table"));
    assert!(resolved.created_entities.is_empty());
    assert!(resolved.removed_entities.is_empty());
    assert_eq!(decisions.len(), 1);
}

#[test]
fn snapshot_graph_detects_missing_parents_and_cycles() {
    let orphan = Snapshot {
        kind: s(SNAPSHOT_MIGRATION),
        parents: vec![s("missing")],
        schema: empty_schema(),
        ..Snapshot::default()
    }
    .seal()
    .unwrap();
    let graph = build_snapshot_graph(&[SnapshotFile {
        path: s("orphan"),
        snapshot: orphan.clone(),
    }])
    .unwrap();
    assert_eq!(graph.missing[&orphan.id], vec!["missing"]);

    let cyclic = |id: &str, parent: &str| Snapshot {
        id: s(id),
        format_version: SNAPSHOT_FORMAT_VERSION,
        kind: s(SNAPSHOT_MERGE),
        parents: vec![s(parent)],
        schema: empty_schema(),
        ..Snapshot::default()
    };
    let error = build_snapshot_graph(&[
        SnapshotFile {
            path: s("a"),
            snapshot: cyclic("a", "b"),
        },
        SnapshotFile {
            path: s("b"),
            snapshot: cyclic("b", "a"),
        },
    ])
    .unwrap_err();
    assert!(error.to_string().contains("cycle"));
}
