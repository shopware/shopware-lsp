use super::render::example_spec;
use crate::specialized_fields::CONSTRUCTOR_STORAGE_PROPERTY;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn php_errors(source: &str) -> usize {
    swlsp_parser_php::parse(source).errors.len()
}

fn require_issue_code(issues: &[ValidationIssue], code: &str) {
    assert!(
        issues.iter().any(|issue| issue.code == code),
        "validation issue not found: {code} in {issues:?}"
    );
}

fn string_field(id: &str, storage: &str, property: &str) -> FieldSpec {
    FieldSpec {
        id: s(id),
        kind: s(FIELD_STRING),
        storage_name: s(storage),
        property_name: s(property),
        editable: true,
        ..FieldSpec::default()
    }
}

#[test]
fn definition_behavior_imports_renders_rewrites_and_changes_schema() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Field;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogItemDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog_item';
    public function getEntityClass(): string { return CatalogItemEntity::class; }
    public function getCollectionClass(): string { return CatalogItemCollection::class; }
    protected function getParentDefinitionClass(): ?string { return CatalogDefinition::class; }
    public function isVersionAware(): bool { return true; }
    protected function defaultFields(): array { return [(new CreatedAtField())->addFlags(new ApiAware())]; }
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode'), new CustomFields()]; }
    public function getRestrictDeleteMetaFields(): FieldCollection {
        return $this->getFields()->filter(static fn (Field $field): bool => \in_array($field->getPropertyName(), ['id', 'createdAt'], true));
    }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
    public function custom(): bool { return true; }
}"#;
    let mut spec = import_definition(source, None).unwrap();
    let behavior = spec.definition_behavior.as_ref().unwrap();
    assert_eq!(
        behavior.parent_definition_class,
        "Acme\\Catalog\\CatalogDefinition"
    );
    assert_eq!(behavior.version_aware, Some(true));
    assert!(behavior.override_default_fields);
    assert_eq!(behavior.default_fields.len(), 1);
    assert_eq!(behavior.default_fields[0].kind, FIELD_CREATED_AT);
    assert!(behavior.override_base_fields);
    assert_eq!(behavior.base_fields.len(), 2);
    assert_eq!(behavior.base_fields[0].kind, FIELD_STRING);
    assert_eq!(
        behavior.restrict_delete_meta_properties,
        vec!["id", "createdAt"]
    );
    assert_eq!(validate_spec(&spec), vec![]);

    let schema = schema_from_spec(&spec).unwrap();
    assert!(schema.columns.contains_key("created_at"));
    assert!(schema.columns.contains_key("base_code"));
    assert!(schema.columns.contains_key("custom_fields"));
    assert!(!schema.columns.contains_key("updated_at"));

    let rendered = render_definition(&spec).unwrap();
    assert_eq!(php_errors(&rendered), 0, "{rendered}");
    for expected in [
        "return CatalogDefinition::class",
        "public function isVersionAware(): bool",
        "protected function defaultFields(): array",
        "new CreatedAtField()",
        "protected function getBaseFields(): array",
        "new StringField('base_code', 'baseCode')",
        "getRestrictDeleteMetaFields",
    ] {
        assert!(rendered.contains(expected), "{expected}");
    }
    let entity = render_entity(&spec).unwrap();
    assert!(entity.contains("protected ?string $baseCode = null;"));
    assert!(entity.contains("public function getBaseCode(): ?string"));
    assert!(entity.contains("use EntityCustomFieldsTrait;"));

    let behavior = spec.definition_behavior.as_mut().unwrap();
    behavior.parent_definition_class = s("Acme\\Catalog\\RootCatalogDefinition");
    behavior.version_aware = Some(false);
    behavior.default_fields.clear();
    behavior.base_fields = vec![FieldSpec {
        kind: s(FIELD_STRING),
        storage_name: s("base_label"),
        property_name: s("baseLabel"),
        editable: true,
        ..FieldSpec::default()
    }];
    behavior.restrict_delete_meta_properties = vec![s("id")];
    let rewritten = rewrite_definition(source, &spec).unwrap();
    for expected in [
        "return RootCatalogDefinition::class",
        "return false;",
        "return [\n        ];",
        "new StringField('base_label', 'baseLabel')",
        "['id']",
        "public function custom(): bool { return true; }",
    ] {
        assert!(rewritten.contains(expected), "{expected}\n{rewritten}");
    }
}

#[test]
fn definition_behavior_preserves_non_literal_methods_as_locked_source() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function isInheritanceAware(): bool { $this->audit(); return true; }
    public function isVersionAware(): bool { return true && $this->configuredVersioning(); }
    protected function getParentDefinitionClass(): ?string { $this->audit(); return RootDefinition::class; }
    protected function defaultFields(): array { $this->audit(); return [new IdField('default_id', 'defaultId')]; }
    protected function getBaseFields(): array { $this->audit(); return [new IdField('base_id', 'baseId')]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}"#;
    let spec = import_definition(source, None).unwrap();
    let behavior = spec.definition_behavior.clone().unwrap();
    for raw in [
        &behavior.inheritance_aware_method_raw,
        &behavior.version_aware_method_raw,
        &behavior.parent_definition_method_raw,
        &behavior.default_fields_method_raw,
        &behavior.base_fields_method_raw,
    ] {
        assert!(!raw.is_empty());
    }
    assert!(behavior.override_default_fields);
    assert!(behavior.override_base_fields);
    assert_eq!(validate_spec(&spec), vec![]);

    let rendered = render_definition(&spec).unwrap();
    assert!(rendered.contains(&behavior.inheritance_aware_method_raw));
    let rewritten = rewrite_definition(source, &spec).unwrap();
    for raw in [
        &behavior.inheritance_aware_method_raw,
        &behavior.version_aware_method_raw,
        &behavior.parent_definition_method_raw,
        &behavior.default_fields_method_raw,
        &behavior.base_fields_method_raw,
    ] {
        assert!(rewritten.contains(raw.as_str()), "{raw}");
    }

    let mut creation = spec.clone();
    creation.mode = s("new");
    require_issue_code(
        &validate_spec(&creation),
        "entity.definitionBehavior.raw.creation.unsupported",
    );
    let mut conflict = spec.clone();
    conflict
        .definition_behavior
        .as_mut()
        .unwrap()
        .parent_definition_class = s("Acme\\Catalog\\RootDefinition");
    require_issue_code(
        &validate_spec(&conflict),
        "entity.definitionBehavior.raw.conflict",
    );
    let mut base_conflict = spec.clone();
    base_conflict
        .definition_behavior
        .as_mut()
        .unwrap()
        .base_fields = vec![FieldSpec {
        kind: s(FIELD_STRING),
        storage_name: s("base"),
        property_name: s("base"),
        editable: true,
        ..FieldSpec::default()
    }];
    require_issue_code(
        &validate_spec(&base_conflict),
        "entity.definitionBehavior.raw.conflict",
    );
    let mut missing_override = example_spec();
    missing_override.definition_behavior = Some(DefinitionBehaviorSpec {
        base_fields: vec![FieldSpec {
            kind: s(FIELD_STRING),
            storage_name: s("base"),
            property_name: s("base"),
            editable: true,
            ..FieldSpec::default()
        }],
        ..DefinitionBehaviorSpec::default()
    });
    require_issue_code(
        &validate_spec(&missing_override),
        "entity.definitionBehavior.baseFields.override.missing",
    );
}

#[test]
fn mapping_and_translation_explicit_version_awareness_round_trip() {
    let mapping = complete_spec(EntitySpec {
        mode: s("new"),
        definition_kind: s(DEFINITION_MAPPING),
        namespace: s("Acme\\Catalog"),
        class_name: s("CatalogMap"),
        entity_name: s("acme_catalog_map"),
        definition_behavior: Some(DefinitionBehaviorSpec {
            version_aware: Some(true),
            ..DefinitionBehaviorSpec::default()
        }),
        fields: vec![FieldSpec {
            id: s("left"),
            kind: s(FIELD_BINARY_ID),
            storage_name: s("left_id"),
            property_name: s("leftId"),
            required: true,
            primary: true,
            editable: true,
            ..FieldSpec::default()
        }],
        ..EntitySpec::default()
    });
    let source = render_definition(&mapping).unwrap();
    assert!(source.contains("public function isVersionAware(): bool"));
    let imported = import_definition(&source, None).unwrap();
    assert_eq!(
        imported.definition_behavior.unwrap().version_aware,
        Some(true)
    );

    let mut parent = example_spec();
    parent.translation = Some(TranslationSpec {
        enabled: true,
        entity_name: s("acme_catalog_item_translation"),
        definition_class: s("Acme\\Catalog\\CatalogItemTranslationDefinition"),
        entity_class: s("Acme\\Catalog\\CatalogItemTranslationEntity"),
        collection_class: s("Acme\\Catalog\\CatalogItemTranslationCollection"),
        parent_definition_class: parent.definition_class.clone(),
        parent_storage_name: s("acme_catalog_item_id"),
        parent_property_name: s("catalogItem"),
        association_property: s("translations"),
        association_local_field: s("id"),
        definition_behavior: Some(DefinitionBehaviorSpec {
            version_aware: Some(true),
            ..DefinitionBehaviorSpec::default()
        }),
        ..TranslationSpec::default()
    });
    parent.fields.push(FieldSpec {
        translated: true,
        ..string_field("label", "label", "label")
    });
    let translation_source = render_translation_definition(&parent).unwrap();
    assert!(translation_source.contains("public function isVersionAware(): bool"));
    let translation = import_translation_definition(&translation_source, None).unwrap();
    assert_eq!(
        translation.spec.definition_behavior.unwrap().version_aware,
        Some(true)
    );
}

#[test]
fn base_field_changes_rewrite_generated_entity_members_and_traits() {
    let custom_fields = FieldSpec {
        id: s("base-custom-fields"),
        kind: s(FIELD_JSON),
        storage_name: s("custom_fields"),
        property_name: s("customFields"),
        editable: true,
        implementation: Some(FieldImplementation {
            class: s("Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CustomFields"),
            constructor_mode: s(CONSTRUCTOR_STORAGE_PROPERTY),
            fixed_storage_name: s("custom_fields"),
            fixed_property_name: s("customFields"),
            entity_type: s("array"),
            entity_trait: s(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCustomFieldsTrait",
            ),
            manage_entity: true,
            ..FieldImplementation::default()
        }),
        ..FieldSpec::default()
    };
    let mut previous = example_spec();
    previous.mode = s("edit");
    previous.definition_behavior = Some(DefinitionBehaviorSpec {
        override_base_fields: true,
        base_fields: vec![
            string_field("base-code", "base_code", "baseCode"),
            custom_fields,
        ],
        ..DefinitionBehaviorSpec::default()
    });
    let previous = complete_spec(previous);
    let source = render_entity(&previous).unwrap();
    assert!(source.contains("$baseCode"));
    assert!(source.contains("use EntityCustomFieldsTrait;"));

    let mut next = previous.clone();
    next.definition_behavior = Some(DefinitionBehaviorSpec {
        override_base_fields: true,
        base_fields: vec![string_field("base-label", "base_label", "baseLabel")],
        ..DefinitionBehaviorSpec::default()
    });
    let rewritten = rewrite_entity(&source, &previous, &next).unwrap();
    assert!(!rewritten.contains("$baseCode"));
    assert!(!rewritten.contains("EntityCustomFieldsTrait"));
    assert!(rewritten.contains("$baseLabel"));
}

#[test]
fn translation_base_field_changes_rewrite_generated_members() {
    let mut previous = complete_spec(EntitySpec {
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
                translated: true,
                ..string_field("name", "name", "name")
            },
        ],
        ..EntitySpec::default()
    });
    previous.translation.as_mut().unwrap().definition_behavior = Some(DefinitionBehaviorSpec {
        override_base_fields: true,
        base_fields: vec![string_field("locale", "locale", "locale")],
        ..DefinitionBehaviorSpec::default()
    });
    let previous = complete_spec(previous);
    let source = render_translation_entity(&previous).unwrap();
    assert!(source.contains("$locale"));

    let mut next = previous.clone();
    next.translation.as_mut().unwrap().definition_behavior = Some(DefinitionBehaviorSpec {
        override_base_fields: true,
        base_fields: vec![string_field("region", "region", "region")],
        ..DefinitionBehaviorSpec::default()
    });
    let rewritten = rewrite_translation_entity(&source, &previous, &next).unwrap();
    assert!(!rewritten.contains("$locale"));
    assert!(rewritten.contains("$region"));
}
