//! Tests for write protected.

use super::inheritance::{
    field, field_by_property, php_ok, relation, require_issue_code, s, stored,
};
use crate::*;

#[test]
fn write_protected_flags_import_as_typed_scopes_and_render() {
    let source = r"<?php declare(strict_types=1);

namespace Acme\Example;

use Acme\Scope\CustomScope;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslatedField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslationsAssociationField;

final class ExampleDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'acme_example';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            (new StringField('internal', 'internal'))->addFlags(new WriteProtected()),
            (new StringField('system_value', 'systemValue'))->addFlags(new WriteProtected(Context::SYSTEM_SCOPE, 'custom')),
            (new StringField('dynamic', 'dynamic'))->addFlags(new WriteProtected(CustomScope::VALUE)),
            (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new WriteProtected('crud')),
            (new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class))->addFlags(new WriteProtected(Context::USER_SCOPE)),
            (new TranslatedField('name'))->addFlags(new WriteProtected(Context::CRUD_API_SCOPE)),
            (new TranslationsAssociationField(ExampleTranslationDefinition::class, 'acme_example_id'))->addFlags(new WriteProtected('system')),
        ]);
    }
}
";
    let target = relation(
        "Shopware\\Core\\Content\\Product\\ProductDefinition",
        "Shopware\\Core\\Content\\Product\\ProductEntity",
        "Shopware\\Core\\Content\\Product\\ProductCollection",
        "product",
    );
    let lookup = move |class: &str| (class == target.definition_class).then(|| target.clone());
    let mut spec = import_definition(source, Some(&lookup)).unwrap();
    assert!(field_by_property(&spec.fields, "internal").write_protected);
    assert!(
        field_by_property(&spec.fields, "internal")
            .write_protected_scopes
            .is_empty()
    );
    assert_eq!(
        s(&["system", "custom"]),
        field_by_property(&spec.fields, "systemValue").write_protected_scopes
    );
    let dynamic = field_by_property(&spec.fields, "dynamic");
    assert!(!dynamic.write_protected);
    assert_eq!(
        s(&["new WriteProtected(CustomScope::VALUE)"]),
        dynamic.preserved_flags
    );
    let product = field_by_property(&spec.fields, "product");
    assert_eq!(s(&["crud"]), product.write_protected_scopes);
    assert!(product.association_write_protected);
    assert_eq!(s(&["user"]), product.association_write_scopes);
    let name = field_by_property(&spec.fields, "name");
    assert!(name.translation_write_protected);
    assert_eq!(s(&["crud"]), name.translation_write_scopes);
    let translation = spec.translation.as_ref().unwrap();
    assert!(translation.association_write_protected);
    assert_eq!(s(&["system"]), translation.association_write_scopes);

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered), "{rendered}");
    assert!(rendered.contains("new WriteProtected('system', 'custom')"));
    assert!(rendered.contains("new WriteProtected(CustomScope::VALUE)"));
    assert!(rendered.contains("new WriteProtected('user')"));
    assert!(rendered.contains("new WriteProtected('crud')"));

    let before = schema_from_spec(&spec).unwrap();
    for field in &mut spec.fields {
        field.write_protected = false;
        field.write_protected_scopes.clear();
        field.association_write_protected = false;
        field.association_write_scopes.clear();
        field.translation_write_protected = false;
        field.translation_write_scopes.clear();
    }
    let translation = spec.translation.as_mut().unwrap();
    translation.association_write_protected = false;
    translation.association_write_scopes.clear();
    let after = schema_from_spec(&spec).unwrap();
    assert_eq!(
        before, after,
        "write protection must not affect migration schema"
    );
}

#[test]
fn hierarchy_write_protection_round_trip() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Example".into(),
        entity_name: "acme_example".into(),
        fields: vec![
            field("id", FIELD_ID),
            field("version", FIELD_VERSION),
            FieldSpec {
                property_name: "children".into(),
                target_definition_class: "Acme\\Example\\ExampleDefinition".into(),
                target_entity_class: "Acme\\Example\\ExampleEntity".into(),
                target_collection_class: "Acme\\Example\\ExampleCollection".into(),
                target_entity_name: "acme_example".into(),
                write_protected: true,
                write_protected_scopes: s(&["system"]),
                association_write_protected: true,
                association_write_scopes: s(&["user"]),
                hierarchy_children_protected: true,
                hierarchy_children_write_scopes: s(&["crud"]),
                hierarchy_version_aware: true,
                hierarchy_version_protected: true,
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
    let rendered = render_definition(&spec).unwrap();
    assert_eq!(4, rendered.matches("new WriteProtected(").count());

    let spec_clone = spec.clone();
    let lookup = move |class: &str| {
        (class == spec_clone.definition_class).then(|| RelationTarget {
            definition_class: spec_clone.definition_class.clone(),
            entity_class: spec_clone.entity_class.clone(),
            collection_class: spec_clone.collection_class.clone(),
            entity_name: spec_clone.entity_name.clone(),
            version_aware: true,
            ..RelationTarget::default()
        })
    };
    let imported = import_definition(&rendered, Some(&lookup)).unwrap();
    let hierarchy = field_by_property(&imported.fields, "children");
    assert_eq!(s(&["system"]), hierarchy.write_protected_scopes);
    assert_eq!(s(&["user"]), hierarchy.association_write_scopes);
    assert_eq!(s(&["crud"]), hierarchy.hierarchy_children_write_scopes);
    assert!(hierarchy.hierarchy_version_protected);
}

#[test]
fn translation_storage_write_protection_renders_typed_import() {
    let source = r"<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

final class ExampleTranslationDefinition extends EntityTranslationDefinition
{
    public const ENTITY_NAME = 'acme_example_translation';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    public function getParentDefinitionClass(): string
    {
        return ExampleDefinition::class;
    }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            (new StringField('name', 'name'))->addFlags(new WriteProtected('user')),
        ]);
    }
}
";
    let translation = import_translation_definition(source, None).unwrap();
    let name = field_by_property(&translation.fields, "name");
    assert!(name.write_protected);
    assert_eq!(s(&["user"]), name.write_protected_scopes);

    let parent = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Example".into(),
        entity_name: "acme_example".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                translated: true,
                ..stored("name", FIELD_STRING, "name", "name")
            },
        ],
        translation: Some(TranslationSpec {
            enabled: true,
            ..TranslationSpec::default()
        }),
        ..EntitySpec::default()
    });
    let combined = attach_translation(parent, translation);
    let rendered = render_translation_definition(&combined).unwrap();
    assert!(rendered.contains(
        "use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\WriteProtected;"
    ));
    assert!(rendered.contains("new WriteProtected('user')"));
    assert!(php_ok(&rendered), "{rendered}");
}

#[test]
fn write_protection_validation() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Example".into(),
        entity_name: "acme_example".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                write_protected_scopes: s(&["system"]),
                ..stored("name", FIELD_STRING, "name", "name")
            },
        ],
        ..EntitySpec::default()
    });
    require_issue_code(
        &validate_spec(&spec),
        "entity.field.writeProtection.invalid",
    );
}
