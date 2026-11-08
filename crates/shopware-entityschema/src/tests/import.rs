//! Port of Go `import_test.go`.

use super::inheritance::{field, field_by_property, relation, s};
use crate::*;

#[test]
fn import_definition_combines_foreign_key_and_association() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example\Entity;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowHtml;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\UpdatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    public function getEntityName(): string { return self::ENTITY_NAME; }
    public function getEntityClass(): string { return ExampleEntity::class; }
    public function getCollectionClass(): string { return ExampleCollection::class; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
			(new StringField('name', 'name', 500))->removeFlag(ApiAware::class)->addFlags(new Required(), new ApiAware(), new AllowHtml())->setDescription('Display name'),
			(new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new Required()),
			(new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', false))->setDescription('Product relation'),
			new CreatedAtField(),
			new UpdatedAtField(),
            customFieldFactory(),
        ]);
    }
}";
    let lookup = |class: &str| -> Option<RelationTarget> {
        assert_eq!("Shopware\\Core\\Content\\Product\\ProductDefinition", class);
        Some(relation(
            class,
            "Shopware\\Core\\Content\\Product\\ProductEntity",
            "Shopware\\Core\\Content\\Product\\ProductCollection",
            "product",
        ))
    };
    let spec = import_definition(source, Some(&lookup)).unwrap();
    assert_eq!("acme_example", spec.entity_name);
    assert_eq!("Acme\\Example\\Entity\\ExampleEntity", spec.entity_class);
    assert_eq!(4, spec.fields.len());
    assert_eq!(FIELD_STRING, spec.fields[1].kind);
    assert_eq!(500, spec.fields[1].max_length);
    assert!(spec.fields[1].preserved_flags.is_empty());
    assert_eq!(
        Some(true),
        spec.fields[1].metadata.as_ref().unwrap().allow_html
    );
    assert_eq!(
        s(&["->removeFlag(ApiAware::class)"]),
        spec.fields[1].modifiers_before_flags
    );
    assert_eq!(
        s(&["->setDescription('Display name')"]),
        spec.fields[1].modifiers_after_flags
    );
    assert_eq!(FIELD_MANY_TO_ONE, spec.fields[2].kind);
    assert_eq!("productId", spec.fields[2].foreign_key_property_name);
    assert_eq!("product", spec.fields[2].target_entity_name);
    assert_eq!(
        s(&["->setDescription('Product relation')"]),
        spec.fields[2].association_before_flags
    );
    assert_eq!(FIELD_LOCKED, spec.fields[3].kind);
    let entity = schema_from_spec(&spec).unwrap();
    assert_eq!(5, entity.columns.len());
    assert!(entity.columns.contains_key("created_at"));
    assert!(entity.columns.contains_key("updated_at"));
    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert!(rewritten.contains("new AllowHtml()"));
    assert!(rewritten.contains("->removeFlag(ApiAware::class)->addFlags("));
    assert!(rewritten.contains(")->setDescription('Display name')"));
    assert!(rewritten.contains("->setDescription('Product relation')"));
    assert!(!rewritten.contains("new RestrictDelete()"));
    assert!(!rewritten.contains("new SetNullOnDelete()"));
    assert!(!rewritten.contains("new CreatedAtField"));
    assert!(!rewritten.contains("new UpdatedAtField"));
}

#[test]
fn imported_multiline_modifier_is_indentation_stable() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
        (new JsonField('config', 'config'))->setDescription(
            'First line '
            . 'second line'
        ),
    ]); }
}";
    let imported = import_definition(source, None).unwrap();
    assert_eq!(1, imported.fields[1].modifiers_before_flags.len());
    let rendered = render_definition(&imported).unwrap();
    let round_tripped = import_definition(&rendered, None).unwrap();
    assert_eq!(
        imported.fields[1].modifiers_before_flags,
        round_tripped.fields[1].modifiers_before_flags
    );
}

#[test]
fn import_version_gated_local_association_enriches_target_metadata() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Acme\Target\TargetDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection {
        $target = Feature::isActive('v6.8.0.0')
            ? new OneToOneAssociationField('target', 'target_id', 'id', TargetDefinition::class, false)
            : new ManyToOneAssociationField('target', 'target_id', TargetDefinition::class);
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
            $target,
        ]);
    }
}";
    let lookup = |class: &str| -> Option<RelationTarget> {
        assert_eq!("Acme\\Target\\TargetDefinition", class);
        Some(relation(
            class,
            "Acme\\Target\\TargetEntity",
            "Acme\\Target\\TargetCollection",
            "acme_target",
        ))
    };
    let imported = import_definition(source, Some(&lookup)).unwrap();
    assert_eq!(2, imported.fields.len());
    assert_eq!(
        "Acme\\Target\\TargetEntity",
        imported.fields[1].target_entity_class
    );
    assert_eq!(
        "Acme\\Target\\TargetCollection",
        imported.fields[1].target_collection_class
    );
    assert_eq!("acme_target", imported.fields[1].target_entity_name);
}

#[test]
fn index_specs_from_entity_excludes_only_generated_relation_indexes() {
    let spec = complete_spec(EntitySpec {
        mode: "edit".into(),
        namespace: "Acme\\Example".into(),
        class_name: "Example".into(),
        entity_name: "acme_example".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                property_name: "product".into(),
                storage_name: "product_id".into(),
                target_definition_class: "Shopware\\Core\\Content\\Product\\ProductDefinition"
                    .into(),
                target_entity_class: "Shopware\\Core\\Content\\Product\\ProductEntity".into(),
                target_entity_name: "product".into(),
                ..field("relation", FIELD_MANY_TO_ONE)
            },
        ],
        ..EntitySpec::default()
    });
    let mut entity = schema_from_spec(&spec).unwrap();
    entity.indexes.insert(
        "uniq.acme_example.product".into(),
        Index {
            name: "uniq.acme_example.product".into(),
            unique: true,
            columns: s(&["product_id"]),
        },
    );
    let indexes = index_specs_from_entity(&spec, &entity);
    assert_eq!(
        vec![IndexSpec {
            name: "uniq.acme_example.product".into(),
            kind: INDEX_UNIQUE.into(),
            columns: s(&["product_id"]),
            translation: false,
        }],
        indexes
    );
}

#[test]
fn import_definition_accepts_literal_local_field_array() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    public function getEntityName(): string { return self::ENTITY_NAME; }
    protected function defineFields(): FieldCollection {
        $fields = [new IdField('id', 'id'), new StringField('name', 'name')];
        return new FieldCollection($fields);
    }
}";
    let spec = import_definition(source, None).unwrap();
    assert_eq!(2, spec.fields.len());
    assert_eq!(FIELD_ID, spec.fields[0].kind);
    assert_eq!(FIELD_STRING, spec.fields[1].kind);

    let mutated = source.replacen(
        "return new FieldCollection($fields);",
        "$fields[] = new StringField('dynamic', 'dynamic');\n        return new FieldCollection($fields);",
        1,
    );
    let error = import_definition(&mutated, None).unwrap_err();
    assert!(error.to_string().contains("no literal array"), "{error}");
}

#[test]
fn import_definition_infers_external_relation_metadata_without_index() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\System\Language\LanguageDefinition;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new FkField('language_id', 'languageId', LanguageDefinition::class),
        new ManyToOneAssociationField('language', 'language_id', LanguageDefinition::class, 'id', false),
    ]); }
}";
    let spec = import_definition(source, None).unwrap();
    assert_eq!(2, spec.fields.len());
    let relation = &spec.fields[1];
    assert_eq!("language", relation.target_entity_name);
    assert_eq!(
        "Shopware\\Core\\System\\Language\\LanguageEntity",
        relation.target_entity_class
    );
    assert_eq!(
        "Shopware\\Core\\System\\Language\\LanguageCollection",
        relation.target_collection_class
    );
    let entity = schema_from_spec(&spec).unwrap();
    assert_eq!(
        "language",
        entity.foreign_keys["fk.acme_example.language_id"].reference_entity
    );
}

#[test]
fn import_and_attach_translation_definition() {
    let parent_source = r"<?php declare(strict_types=1);
namespace Acme\Example\Content\Blog;
use Acme\Example\Content\Blog\Aggregate\BlogTranslation\BlogTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslatedField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslationsAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class BlogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_blog';
    public function getEntityClass(): string { return BlogEntity::class; }
    public function getCollectionClass(): string { return BlogCollection::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required()),
        (new TranslatedField('name', true))->addFlags(new ApiAware()),
        new TranslatedField('internalLink'),
        (new TranslationsAssociationField(BlogTranslationDefinition::class, 'acme_blog_id'))->addFlags(new Required()),
    ]); }
    public function customParentMethod(): bool { return true; }
}";
    let translation_source = r"<?php declare(strict_types=1);
namespace Acme\Example\Content\Blog\Aggregate\BlogTranslation;
use Acme\Example\Content\Blog\BlogDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class BlogTranslationDefinition extends EntityTranslationDefinition {
    public const ENTITY_NAME = 'acme_blog_translation';
    public function getEntityClass(): string { return BlogTranslationEntity::class; }
    public function getCollectionClass(): string { return BlogTranslationCollection::class; }
    protected function getParentDefinitionClass(): string { return BlogDefinition::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new StringField('name', 'name', 500))->addFlags(new Required()),
        new IdField('internal_link', 'internalLink'),
    ]); }
    public function customTranslationMethod(): bool { return true; }
}";
    let parent = import_definition(parent_source, None).unwrap();
    assert!(parent.translation.is_some());
    assert_eq!(3, parent.fields.len());
    assert!(parent.fields[1].translated);

    let translation = import_translation_definition(translation_source, None).unwrap();
    let combined = attach_translation(parent, translation);
    assert_eq!(3, combined.fields.len());
    let name = &combined.fields[1];
    assert_eq!(FIELD_STRING, name.kind);
    assert!(name.translated);
    assert!(name.translation_use_for_sort);
    assert_eq!(Some(true), name.translation_api_aware);
    assert_eq!(500, name.max_length);
    assert!(name.required);
    assert_eq!(FIELD_BINARY_ID, combined.fields[2].kind);
    assert_eq!("internal_link", combined.fields[2].storage_name);

    let parent_after = rewrite_definition(parent_source, &combined).unwrap();
    assert!(parent_after.contains("customParentMethod"));
    assert!(parent_after.contains("new TranslatedField('name', true)"));
    let translation_after = rewrite_translation_definition(translation_source, &combined).unwrap();
    assert!(translation_after.contains("customTranslationMethod"));
    assert!(translation_after.contains("new StringField('name', 'name', 500)"));
    let translation_entity = render_translation_entity(&combined).unwrap();
    let translation_entity = format!(
        "{}    public function customEntityMethod(): bool {{ return true; }}\n}}\n",
        translation_entity
            .strip_suffix("}\n")
            .unwrap_or(&translation_entity)
    );
    let mut next = combined.clone();
    next.fields.push(FieldSpec {
        property_name: "description".into(),
        storage_name: "description".into(),
        translated: true,
        ..field("description", FIELD_LONG_TEXT)
    });
    let translation_entity_after =
        rewrite_translation_entity(&translation_entity, &combined, &next).unwrap();
    assert!(translation_entity_after.contains("customEntityMethod"));
    assert!(translation_entity_after.contains("protected ?string $description = null;"));
}

#[test]
fn import_json_property_mapping_does_not_treat_nested_fields_as_flags() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example\Content\Page;
use Shopware\Core\Framework\Api\Context\AdminApiSource;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField as NestedBoolField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class PageDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_page';
    protected function defineFields(): FieldCollection { return new FieldCollection([
		(new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
        (new JsonField('visibility', 'visibility', [
            new NestedBoolField('mobile', 'mobile'),
        ], []))->addFlags(new ApiAware(AdminApiSource::class)),
    ]); }
}";
    let spec = import_definition(source, None).unwrap();
    assert_eq!(2, spec.fields.len());
    let visibility = field_by_property(&spec.fields, "visibility");
    assert!(visibility.api_aware);
    assert_eq!(
        s(&["Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource"]),
        visibility.api_aware_sources
    );
    assert!(
        visibility
            .json_property_mapping_expression
            .contains("new \\Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\BoolField")
    );
    assert_eq!("[]", visibility.json_default_expression);
    assert!(visibility.preserved_flags.is_empty());

    let rendered = render_definition(&spec).unwrap();
    assert!(rendered.contains("new ApiAware(AdminApiSource::class)"));
    assert!(rendered.contains("new \\Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\BoolField('mobile', 'mobile')"));
    assert!(rendered.contains("], []))"));

    let round_tripped = import_definition(&rendered, None).unwrap();
    let again = field_by_property(&round_tripped.fields, "visibility");
    assert_eq!(visibility.api_aware_sources, again.api_aware_sources);
    assert_eq!(
        visibility.json_property_mapping_expression,
        again.json_property_mapping_expression
    );
    assert_eq!(
        visibility.json_default_expression,
        again.json_default_expression
    );
}
