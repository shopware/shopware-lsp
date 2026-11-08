use super::render::example_spec;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn require_issue_code(issues: &[ValidationIssue], code: &str) {
    assert!(
        issues.iter().any(|issue| issue.code == code),
        "validation issue not found: {code} in {issues:?}"
    );
}

fn default(property: &str, expression: &str) -> DefinitionDefaultSpec {
    DefinitionDefaultSpec {
        property_name: s(property),
        value_expression: s(expression),
    }
}

#[test]
fn definition_metadata_imports_renders_and_rewrites() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Catalog;
use Acme\Catalog\Hydration\CatalogHydrator;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { return '6.7.1.0'; }
    public function getDefaults(): array { return ['active' => true, 'kind' => self::KIND_DEFAULT, 'config' => []]; }
    public function getChildDefaults(): array { return ['kind' => self::KIND_DEFAULT]; }
    public function getHydratorClass(): string { return CatalogHydrator::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
    public function custom(): bool { return true; }
}"#;
    let mut spec = import_definition(source, None).unwrap();
    let metadata = spec.definition_metadata.as_ref().unwrap();
    assert_eq!(metadata.since, "6.7.1.0");
    assert_eq!(
        metadata.defaults,
        vec![
            default("active", "true"),
            default("kind", "self::KIND_DEFAULT"),
            default("config", "[]")
        ]
    );
    assert_eq!(
        metadata.child_defaults,
        vec![default("kind", "self::KIND_DEFAULT")]
    );
    assert_eq!(
        metadata.hydrator_class,
        "Acme\\Catalog\\Hydration\\CatalogHydrator"
    );
    assert_eq!(validate_spec(&spec), vec![]);

    let rendered = render_definition(&spec).unwrap();
    assert!(
        swlsp_parser_php::parse(&rendered).errors.is_empty(),
        "{rendered}"
    );
    assert!(rendered.contains("public function getDefaults(): array"));
    assert!(rendered.contains("'kind' => self::KIND_DEFAULT"));
    assert!(rendered.contains("return CatalogHydrator::class"));

    let metadata = spec.definition_metadata.as_mut().unwrap();
    metadata.since = s("6.7.2.0");
    metadata.defaults[0].value_expression = s("false");
    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert!(rewritten.contains("return '6.7.2.0';"));
    assert!(rewritten.contains("'active' => false"));
    assert!(rewritten.contains("public function custom(): bool { return true; }"));
}

#[test]
fn definition_metadata_preserves_custom_methods_and_validates_conflicts() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { $this->audit(); return '6.7.0.0'; }
    public function getDefaults(): array { $this->audit(); return ['active' => true]; }
    public function getChildDefaults(): array { return ['active' => true] + $this->computedDefaults(); }
    public function getHydratorClass(): string { $this->audit(); return CatalogHydrator::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}"#;
    let spec = import_definition(source, None).unwrap();
    let metadata = spec.definition_metadata.clone().unwrap();
    for raw in [
        &metadata.since_method_raw,
        &metadata.defaults_method_raw,
        &metadata.child_defaults_method_raw,
        &metadata.hydrator_method_raw,
    ] {
        assert!(!raw.is_empty());
    }
    let rendered = render_definition(&spec).unwrap();
    assert!(rendered.contains(&metadata.defaults_method_raw));
    let rewritten = rewrite_definition(source, &spec).unwrap();
    for raw in [
        &metadata.defaults_method_raw,
        &metadata.since_method_raw,
        &metadata.child_defaults_method_raw,
        &metadata.hydrator_method_raw,
    ] {
        assert!(rewritten.contains(raw.as_str()), "{raw}");
    }

    let mut conflict = spec.clone();
    conflict.definition_metadata.as_mut().unwrap().defaults = vec![default("active", "true")];
    require_issue_code(
        &validate_spec(&conflict),
        "entity.definitionMetadata.raw.conflict",
    );
    let metadata = conflict.definition_metadata.as_mut().unwrap();
    metadata.defaults_method_raw.clear();
    metadata.defaults[0].value_expression = s("broken(");
    require_issue_code(
        &validate_spec(&conflict),
        "entity.definitionMetadata.default.expression.invalid",
    );
}

#[test]
fn translation_definition_metadata_round_trips() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogTranslationDefinition extends EntityTranslationDefinition {
    public const ENTITY_NAME = 'acme_catalog_translation';
    public function getEntityClass(): string { return CatalogTranslationEntity::class; }
    public function getCollectionClass(): string { return CatalogTranslationCollection::class; }
    protected function getParentDefinitionClass(): string { return CatalogDefinition::class; }
    public function since(): ?string { return '6.7.1.0'; }
    public function getDefaults(): array { return ['label' => 'default']; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new StringField('label', 'label')]); }
}"#;
    let mut imported = import_translation_definition(source, None).unwrap();
    let metadata = imported.spec.definition_metadata.as_ref().unwrap();
    assert_eq!(metadata.since, "6.7.1.0");
    assert_eq!(metadata.defaults, vec![default("label", "'default'")]);

    let mut parent = example_spec();
    parent.translation = Some(TranslationSpec {
        enabled: true,
        entity_name: s("acme_catalog_translation"),
        definition_class: imported.spec.definition_class.clone(),
        entity_class: imported.spec.entity_class.clone(),
        collection_class: imported.spec.collection_class.clone(),
        parent_definition_class: parent.definition_class.clone(),
        parent_storage_name: s("acme_catalog_item_id"),
        parent_property_name: s("catalogItem"),
        association_property: s("translations"),
        association_local_field: s("acme_catalog_item_id"),
        ..TranslationSpec::default()
    });
    parent.fields.push(FieldSpec {
        id: s("label"),
        kind: s(FIELD_STRING),
        property_name: s("label"),
        storage_name: s("label"),
        translated: true,
        editable: true,
        ..FieldSpec::default()
    });
    imported.spec.parent_definition_class = parent.definition_class.clone();
    let parent = attach_translation(parent, imported);
    let rendered = render_translation_definition(&parent).unwrap();
    assert!(rendered.contains("public function since(): ?string"));
    assert!(rendered.contains("'label' => 'default'"));
}
