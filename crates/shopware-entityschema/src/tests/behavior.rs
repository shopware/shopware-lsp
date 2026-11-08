use std::collections::HashMap;

use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn php_errors(source: &str) -> usize {
    swlsp_parser_php::parse(source).errors.len()
}

#[test]
fn import_definition_promotes_behavior_flags_without_changing_semantics() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CascadeDelete;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Computed;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\NoConstraint;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToManyAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new StringField('url', 'url'))->addFlags(new Runtime(['path', 'updatedAt']), new SearchRanking(SearchRanking::HIGH_SEARCH_RANKING, false)),
        (new StringField('hash', 'hash'))->addFlags(new Computed()),
        (new StringField('dynamic', 'dynamic'))->addFlags(new Runtime(\array_merge(self::BASE, ['name']))),
        (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new NoConstraint()),
        (new OneToManyAssociationField('products', ProductDefinition::class, 'parent_id'))->addFlags(new CascadeDelete(false)),
    ]); }
}"#;
    let spec = import_definition(source, None).unwrap();
    assert_eq!(spec.fields.len(), 6);
    let by_property: HashMap<&str, &FieldSpec> = spec
        .fields
        .iter()
        .map(|field| (field.property_name.as_str(), field))
        .collect();

    let runtime_field = by_property["url"];
    assert_eq!(runtime_field.search_ranking, 500.0);
    assert_eq!(runtime_field.search_ranking_tokenize, Some(false));
    assert_eq!(
        runtime_field.behavior,
        Some(FieldBehavior {
            runtime: true,
            runtime_dependencies: vec![s("path"), s("updatedAt")],
            ..FieldBehavior::default()
        })
    );
    assert!(runtime_field.preserved_flags.is_empty());
    assert_eq!(
        by_property["hash"].behavior,
        Some(FieldBehavior {
            computed: true,
            ..FieldBehavior::default()
        })
    );
    assert_eq!(
        by_property["dynamic"]
            .behavior
            .as_ref()
            .unwrap()
            .runtime_dependencies_expression,
        "\\array_merge(self::BASE, ['name'])"
    );
    assert!(
        by_property["productId"]
            .behavior
            .as_ref()
            .unwrap()
            .no_constraint
    );
    let products = by_property["products"];
    assert_eq!(products.delete_behavior, DELETE_CASCADE);
    assert_eq!(products.delete_clone_relevant, Some(false));

    let entity = schema_from_spec(&spec).unwrap();
    assert!(!entity.columns.contains_key("url"));
    assert!(!entity.columns.contains_key("dynamic"));
    assert!(entity.columns.contains_key("hash"));
    assert!(entity.columns.contains_key("product_id"));
    assert!(
        entity.foreign_keys.is_empty(),
        "NoConstraint keeps the column but omits the physical constraint"
    );

    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert_eq!(php_errors(&rewritten), 0, "{rewritten}");
    for expected in [
        "new Runtime(['path', 'updatedAt'])",
        "new SearchRanking(500, false)",
        "new Runtime(\\array_merge(self::BASE, ['name']))",
        "new NoConstraint()",
        "new CascadeDelete(false)",
    ] {
        assert!(rewritten.contains(expected), "{expected}");
    }

    let entity_source = render_entity(&spec).unwrap();
    assert!(entity_source.contains("function getUrl()"));
    assert!(!entity_source.contains("function setUrl("));
    assert!(entity_source.contains("function getHash()"));
    assert!(!entity_source.contains("function setHash("));
}

#[test]
fn delete_flag_options_round_trip_on_all_association_kinds() {
    let target = RelationTarget {
        definition_class: s("Acme\\Target\\TargetDefinition"),
        entity_class: s("Acme\\Target\\TargetEntity"),
        collection_class: s("Acme\\Target\\TargetCollection"),
        entity_name: s("acme_target"),
        ..RelationTarget::default()
    };
    let relation = |id: &str, kind: &str, property: &str| FieldSpec {
        id: s(id),
        kind: s(kind),
        property_name: s(property),
        target_definition_class: target.definition_class.clone(),
        target_entity_class: target.entity_class.clone(),
        target_collection_class: target.collection_class.clone(),
        target_entity_name: target.entity_name.clone(),
        editable: true,
        ..FieldSpec::default()
    };
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example"),
        class_name: s("Example"),
        entity_name: s("acme_example"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                reference_storage_name: s("example_id"),
                delete_behavior: s(DELETE_SET_NULL),
                delete_enforced_by_constraint: Some(false),
                ..relation("one-many", FIELD_ONE_TO_MANY, "targets")
            },
            FieldSpec {
                mapping_definition_class: s("Acme\\Example\\ExampleTagDefinition"),
                mapping_local_column: s("example_id"),
                mapping_reference_column: s("target_id"),
                delete_behavior: s(DELETE_CASCADE),
                delete_clone_relevant: Some(false),
                ..relation("many-many", FIELD_MANY_TO_MANY, "tags")
            },
        ],
        ..EntitySpec::default()
    });
    let definition = render_definition(&spec).unwrap();
    assert_eq!(php_errors(&definition), 0, "{definition}");
    assert!(definition.contains("new SetNullOnDelete(false)"));
    assert_eq!(definition.matches("new CascadeDelete(false)").count(), 1);

    let lookup = |class: &str| (class == target.definition_class).then(|| target.clone());
    let imported = import_definition(&definition, Some(&lookup)).unwrap();
    assert_eq!(imported.fields[1].delete_behavior, DELETE_SET_NULL);
    assert_eq!(
        imported.fields[1].delete_enforced_by_constraint,
        Some(false)
    );
    assert_eq!(imported.fields[2].delete_behavior, DELETE_CASCADE);
    assert_eq!(imported.fields[2].delete_clone_relevant, Some(false));
}

#[test]
fn association_only_many_to_one_does_not_invent_storage() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\System\User\UserDefinition;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new ManyToOneAssociationField('activeUser', 'active_user_id', UserDefinition::class, 'id', false))->addFlags(new Runtime()),
    ]); }
}"#;
    let spec = import_definition(source, None).unwrap();
    assert_eq!(spec.fields.len(), 2);
    let association = &spec.fields[1];
    assert_eq!(association.kind, FIELD_MANY_TO_ONE);
    assert!(association.uses_existing_column);
    assert!(association.association_behavior.as_ref().unwrap().runtime);
    assert_eq!(validate_spec(&spec), vec![]);

    let entity = schema_from_spec(&spec).unwrap();
    assert!(!entity.columns.contains_key("active_user_id"));
    assert!(entity.foreign_keys.is_empty());

    let rendered = render_definition(&spec).unwrap();
    assert!(!rendered.contains("new FkField('active_user_id'"));
    assert!(rendered.contains("new ManyToOneAssociationField('activeUser', 'active_user_id'"));
}
