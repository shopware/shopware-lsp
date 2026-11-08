use super::render::example_spec;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn php_errors(source: &str) -> usize {
    swlsp_parser_php::parse(source).errors.len()
}

/// Appends a custom member before the final class brace (Go `source[:len-2]`).
fn with_custom_member(source: &str, member: &str) -> String {
    format!("{}\n    {member}\n}}\n", &source[..source.len() - 2])
}

#[test]
fn rewrite_definition_preserves_custom_members() {
    let mut spec = example_spec();
    let original = render_definition(&spec).unwrap();
    let original = with_custom_member(
        &original,
        "public function custom(): string { return 'keep'; }",
    );
    spec.fields.push(FieldSpec {
        id: s("description"),
        kind: s(FIELD_LONG_TEXT),
        property_name: s("description"),
        storage_name: s("description"),
        editable: true,
        ..FieldSpec::default()
    });
    let result = rewrite_definition(&original, &spec).unwrap();
    assert!(result.contains("function custom"));
    assert!(result.contains("new LongTextField('description', 'description')"));
    assert_eq!(php_errors(&result), 0);
}

#[test]
fn rewrite_definition_from_removes_only_unused_previous_schema_imports() {
    let mut previous = example_spec();
    previous.indexes.clear();
    previous.translation = Some(TranslationSpec {
        enabled: true,
        definition_class: s(
            "Acme\\Example\\Entity\\Catalog\\Aggregate\\CatalogItemTranslation\\CatalogItemTranslationDefinition",
        ),
        entity_class: s(
            "Acme\\Example\\Entity\\Catalog\\Aggregate\\CatalogItemTranslation\\CatalogItemTranslationEntity",
        ),
        collection_class: s(
            "Acme\\Example\\Entity\\Catalog\\Aggregate\\CatalogItemTranslation\\CatalogItemTranslationCollection",
        ),
        association_property: s("translations"),
        parent_property_name: s("catalogItem"),
        parent_storage_name: s("acme_catalog_item_id"),
        ..TranslationSpec::default()
    });
    previous.fields[1].translated = true;
    let source = render_definition(&previous).unwrap();
    let source = source.replacen(
        "use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;",
        "use Vendor\\CustomHelper;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;",
        1,
    );
    let source = with_custom_member(
        &source,
        "public function custom(): string { return CustomHelper::class; }",
    );

    let mut next = previous.clone();
    next.translation = None;
    next.fields[1].translated = false;
    let result = rewrite_definition_from(&source, &previous, &next).unwrap();
    assert!(!result.contains("TranslationsAssociationField"));
    assert!(!result.contains("TranslatedField"));
    assert!(!result.contains("CatalogItemTranslationDefinition"));
    assert!(result.contains("use Vendor\\CustomHelper;"));
    assert!(result.contains("return CustomHelper::class"));
    assert_eq!(php_errors(&result), 0);
}

#[test]
fn rewrite_definition_from_renames_literal_entity_identity() {
    let mut previous = example_spec();
    previous.mode = s("edit");
    let source = render_definition(&previous).unwrap();
    let mut next = previous.clone();
    next.entity_name = s("acme_catalog_entry");

    let rewritten = rewrite_definition_from(&source, &previous, &next).unwrap();
    assert!(rewritten.contains("const ENTITY_NAME = 'acme_catalog_entry';"));
    assert!(!rewritten.contains("const ENTITY_NAME = 'acme_catalog_item';"));
    assert_eq!(php_errors(&rewritten), 0);

    let custom = source.replacen(
        "final public const ENTITY_NAME",
        "#[CustomIdentity]\n    final public const ENTITY_NAME",
        1,
    );
    let error = rewrite_definition_from(&custom, &previous, &next).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("cannot overwrite customized ENTITY_NAME constant"),
        "{error}"
    );
}

fn article_spec() -> EntitySpec {
    complete_spec(EntitySpec {
        mode: s("edit"),
        namespace: s("Acme\\Example"),
        class_name: s("Article"),
        entity_name: s("acme_article"),
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
                translated: true,
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    })
}

#[test]
fn rewrite_translation_definition_from_renames_literal_entity_identity() {
    let previous = article_spec();
    let source = render_translation_definition(&previous).unwrap();
    let mut next = previous.clone();
    next.translation.as_mut().unwrap().entity_name = s("acme_article_locale");

    let rewritten = rewrite_translation_definition_from(&source, &previous, &next).unwrap();
    assert!(rewritten.contains("const ENTITY_NAME = 'acme_article_locale';"));
    assert!(!rewritten.contains("const ENTITY_NAME = 'acme_article_translation';"));
    assert_eq!(php_errors(&rewritten), 0);
}

#[test]
fn rewrite_collections_rename_only_safe_api_aliases() {
    let previous = example_spec();
    let mut next = previous.clone();
    next.entity_name = s("acme_catalog_entry");
    let source = render_collection(&previous).unwrap();
    let source = format!(
        "{}    public function custom(): bool {{ return true; }}\n}}\n",
        source.strip_suffix("}\n").unwrap()
    );

    let rewritten = rewrite_collection(&source, &previous, &next).unwrap();
    assert!(rewritten.contains("return 'acme_catalog_entry_collection';"));
    assert!(rewritten.contains("function custom"));

    let custom = source.replacen(
        "return 'acme_catalog_item_collection';",
        "return $this->customAlias();",
        1,
    );
    let error = rewrite_collection(&custom, &previous, &next).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("cannot overwrite customized collection getApiAlias"),
        "{error}"
    );

    let previous = article_spec();
    let mut next = previous.clone();
    next.translation.as_mut().unwrap().entity_name = s("acme_article_locale");
    let translation_source = render_translation_collection(&previous).unwrap();
    let translation_rewritten =
        rewrite_translation_collection(&translation_source, &previous, &next).unwrap();
    assert!(translation_rewritten.contains("return 'acme_article_locale_collection';"));
}

#[test]
fn rewrite_entity_preserves_custom_members() {
    let before = example_spec();
    let original = render_entity(&before).unwrap();
    let original = with_custom_member(
        &original,
        "public function custom(): string { return 'keep'; }",
    );
    let mut after = before.clone();
    after.fields.push(FieldSpec {
        id: s("description"),
        kind: s(FIELD_LONG_TEXT),
        property_name: s("description"),
        storage_name: s("description"),
        editable: true,
        ..FieldSpec::default()
    });
    let result = rewrite_entity(&original, &before, &after).unwrap();
    assert!(result.contains("function custom"));
    assert!(result.contains("$description"));
    assert_eq!(php_errors(&result), 0);
}

fn parent_short_class(kind: &str) -> &'static str {
    match kind {
        DEFINITION_BULK_EXTENSION => "BulkEntityExtension",
        DEFINITION_EXTENSION => "EntityExtension",
        DEFINITION_MAPPING => "MappingEntityDefinition",
        _ => "EntityDefinition",
    }
}

#[test]
fn rewrite_definition_from_supports_every_class_based_kind_transition() {
    let kinds = [
        DEFINITION_ENTITY,
        DEFINITION_MAPPING,
        DEFINITION_EXTENSION,
        DEFINITION_BULK_EXTENSION,
    ];
    for from in kinds {
        for to in kinds {
            if from == to {
                continue;
            }
            let context = format!("{from}_to_{to}");
            let previous = transition_spec(from);
            let source = render_definition(&previous).unwrap();
            let source = with_custom_member(
                &source,
                "public function custom(): string { return 'keep'; }",
            );
            let mut next = transition_spec(to);
            next.definition_class = previous.definition_class.clone();

            let result = rewrite_definition_from(&source, &previous, &next)
                .unwrap_or_else(|error| panic!("{context}: {error}"));
            assert_eq!(php_errors(&result), 0, "{context}: {result}");
            assert!(result.contains("function custom"), "{context}");
            assert!(
                result.contains(&format!("extends {}", parent_short_class(to))),
                "{context}: {result}"
            );
            match to {
                DEFINITION_BULK_EXTENSION => {
                    assert!(result.contains("function collect"), "{context}");
                    assert!(!result.contains("function defineFields"), "{context}");
                    assert!(!result.contains("function extendFields"), "{context}");
                    assert!(!result.contains("const ENTITY_NAME"), "{context}");
                }
                DEFINITION_EXTENSION => {
                    assert!(result.contains("function extendFields"), "{context}");
                    assert!(!result.contains("function defineFields"), "{context}");
                    assert!(!result.contains("const ENTITY_NAME"), "{context}");
                }
                _ => {
                    assert!(result.contains("function defineFields"), "{context}");
                    assert!(!result.contains("function extendFields"), "{context}");
                    assert!(result.contains("const ENTITY_NAME"), "{context}");
                    if to == DEFINITION_MAPPING
                        && next.entity_class.is_empty()
                        && next.collection_class.is_empty()
                    {
                        assert!(!result.contains("function getEntityClass"), "{context}");
                        assert!(!result.contains("function getCollectionClass"), "{context}");
                    }
                }
            }
        }
    }
}

fn transition_spec(kind: &str) -> EntitySpec {
    let runtime_note = || FieldSpec {
        id: s("note"),
        kind: s(FIELD_STRING),
        property_name: s("note"),
        storage_name: s("acme_note"),
        behavior: Some(FieldBehavior {
            runtime: true,
            ..FieldBehavior::default()
        }),
        editable: true,
        ..FieldSpec::default()
    };
    let mut spec = EntitySpec {
        mode: s("edit"),
        definition_kind: s(kind),
        namespace: s("Acme\\Example"),
        class_name: s("Demo"),
        entity_name: s("acme_demo"),
        definition_class: s("Acme\\Example\\DemoDefinition"),
        shopware_version: s("6.7.1.0"),
        create_migration: true,
        ..EntitySpec::default()
    };
    match kind {
        DEFINITION_ENTITY => {
            spec.fields = vec![FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                property_name: s("id"),
                storage_name: s("id"),
                required: true,
                primary: true,
                editable: true,
                ..FieldSpec::default()
            }];
        }
        DEFINITION_MAPPING => {
            spec.fields = vec![FieldSpec {
                id: s("left"),
                kind: s(FIELD_BINARY_ID),
                property_name: s("leftId"),
                storage_name: s("left_id"),
                required: true,
                primary: true,
                editable: true,
                ..FieldSpec::default()
            }];
        }
        DEFINITION_EXTENSION => {
            spec.entity_name = s("product");
            spec.extended_definition_class =
                s("Shopware\\Core\\Content\\Product\\ProductDefinition");
            spec.fields = vec![runtime_note()];
        }
        _ => {
            spec.entity_name.clear();
            spec.bulk_extensions = vec![BulkExtensionTargetSpec {
                id: s("product"),
                entity_name: s("product"),
                extended_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
                fields: vec![runtime_note()],
                ..BulkExtensionTargetSpec::default()
            }];
        }
    }
    complete_spec(spec)
}
