//! Tests for association autoload.

use super::inheritance::{field, field_by_property, php_ok, relation, require_issue_code};
use crate::*;

#[test]
fn to_one_association_autoload_imports_native_defaults_and_named_arguments() {
    let source = r"<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField;

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
            new FkField('product_id', 'productId', ProductDefinition::class),
            new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', autoload: true),
            new FkField('fallback_product_id', 'fallbackProductId', ProductDefinition::class),
            new ManyToOneAssociationField('fallbackProduct', 'fallback_product_id', ProductDefinition::class),
            new FkField('featured_product_id', 'featuredProductId', ProductDefinition::class),
            new OneToOneAssociationField('featuredProduct', 'featured_product_id', 'id', ProductDefinition::class),
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
    assert!(field_by_property(&spec.fields, "product").association_autoload);
    assert!(
        !field_by_property(&spec.fields, "fallbackProduct").association_autoload,
        "ManyToOneAssociationField defaults autoload to false"
    );
    assert!(
        field_by_property(&spec.fields, "featuredProduct").association_autoload,
        "OneToOneAssociationField defaults autoload to true"
    );

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered));
    assert!(rendered.contains("new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', true)"));
    assert!(rendered.contains("new ManyToOneAssociationField('fallbackProduct', 'fallback_product_id', ProductDefinition::class, 'id', false)"));
    assert!(rendered.contains("new OneToOneAssociationField('featuredProduct', 'featured_product_id', 'id', ProductDefinition::class, true)"));

    let before = schema_from_spec(&spec).unwrap();
    for field in &mut spec.fields {
        field.association_autoload = false;
    }
    let after = schema_from_spec(&spec).unwrap();
    assert_eq!(before, after, "autoload must not affect migration schema");
}

#[test]
fn association_autoload_validation_rejects_to_many_fields() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Example".into(),
        entity_name: "acme_example".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                property_name: "products".into(),
                target_definition_class: "Shopware\\Core\\Content\\Product\\ProductDefinition"
                    .into(),
                target_collection_class: "Shopware\\Core\\Content\\Product\\ProductCollection"
                    .into(),
                reference_storage_name: "example_id".into(),
                source_column: "id".into(),
                association_autoload: true,
                ..field("products", FIELD_ONE_TO_MANY)
            },
        ],
        ..EntitySpec::default()
    });
    require_issue_code(
        &validate_spec(&spec),
        "entity.association.autoload.unsupported",
    );
}
