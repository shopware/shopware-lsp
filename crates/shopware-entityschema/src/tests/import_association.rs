//! Port of Go `import_association_test.go` (benchmarks omitted).

use super::inheritance::{field_by_property, php_ok, s};
use crate::*;

fn association_import_source(fields: &str) -> String {
    format!(
        r"<?php
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToManyAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToManyAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CascadeDelete;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CustomFlag;
class ExampleDefinition extends EntityDefinition {{
 public const ENTITY_NAME = 'acme_example';
 protected function defineFields(): FieldCollection {{
 return new FieldCollection([new IdField('id', 'id'), {fields}]);
 }}
}}"
    )
}

fn association_import_target(class: &str) -> Option<RelationTarget> {
    (class == "Acme\\Example\\TargetDefinition").then(|| RelationTarget {
        definition_class: class.to_owned(),
        entity_name: "target".into(),
        entity_class: "Acme\\Example\\TargetEntity".into(),
        collection_class: "Acme\\Example\\TargetCollection".into(),
        fields: vec![RelationTargetField {
            property_name: "code".into(),
            storage_name: "target_code".into(),
            primary: true,
        }],
        ..RelationTarget::default()
    })
}

#[test]
fn import_to_one_associations_preserve_column_and_association_contracts() {
    for kind in [FIELD_MANY_TO_ONE, FIELD_ONE_TO_ONE] {
        for with_column in [false, true] {
            let name = format!("{kind}/column={with_column}");
            let mut constructor =
                "new ManyToOneAssociationField('target', 'target_id', TargetDefinition::class, 'code')".to_owned();
            if kind == FIELD_ONE_TO_ONE {
                constructor = "new OneToOneAssociationField('target', 'target_id', 'code', TargetDefinition::class)".to_owned();
            }
            let mut column = String::new();
            if !with_column {
                constructor = constructor.replacen("'target_id'", "'id'", 1);
            } else {
                column = "(new FkField('target_id', 'targetId', TargetDefinition::class, 'code'))->addFlags(new Required())->setDescription('Column'),\n".to_owned();
            }
            let source = association_import_source(&format!(
                "{column}({constructor})->removeFlag(Inherited::class)->addFlags(new ApiAware(), new CascadeDelete(), new CustomFlag())->setDescription('Association')"
            ));
            let spec = import_definition(&source, Some(&association_import_target)).unwrap();
            assert_eq!(2, spec.fields.len(), "{name}");
            let field = &spec.fields[1];
            assert_eq!(kind, field.kind, "{name}");
            assert_eq!("target", field.property_name);
            assert_eq!("target_code", field.reference_storage_name, "{name}");
            assert_eq!("target", field.target_entity_name);
            assert_eq!(with_column, field.required, "{name}");
            assert_eq!(!with_column, field.uses_existing_column, "{name}");
            assert_eq!(
                kind == FIELD_ONE_TO_ONE,
                field.association_autoload,
                "{name}"
            );
            assert!(!field.api_aware, "{name}");
            assert!(field.association_api_aware, "{name}");
            assert_eq!(DELETE_CASCADE, field.delete_behavior, "{name}");
            assert_eq!(
                s(&["->removeFlag(Inherited::class)"]),
                field.association_before_flags,
                "{name}"
            );
            assert_eq!(
                s(&["->setDescription('Association')"]),
                field.association_after_flags,
                "{name}"
            );
            assert!(
                field
                    .association_flags
                    .contains(&"new CustomFlag()".to_owned()),
                "{name}"
            );
            if with_column {
                assert_eq!("targetId", field.foreign_key_property_name);
                assert_eq!(
                    s(&["->setDescription('Column')"]),
                    field.modifiers_after_flags
                );
                assert!(field.raw.contains("new FkField"));
            } else {
                assert!(field.foreign_key_property_name.is_empty(), "{name}");
            }
            let rendered = render_definition(&spec).unwrap();
            assert!(php_ok(&rendered), "{rendered}");
            let reimported =
                import_definition(&rendered, Some(&association_import_target)).unwrap();
            let again = field_by_property(&reimported.fields, "target");
            assert_eq!(field.kind, again.kind, "{name}");
            assert_eq!(
                field.uses_existing_column, again.uses_existing_column,
                "{name}"
            );
            assert_eq!(field.required, again.required, "{name}");
            assert_eq!(
                field.association_autoload, again.association_autoload,
                "{name}"
            );
            assert_eq!(field.association_flags, again.association_flags, "{name}");
            assert_eq!(
                field.association_before_flags, again.association_before_flags,
                "{name}"
            );
            assert_eq!(
                field.association_after_flags, again.association_after_flags,
                "{name}"
            );
        }
    }
}

#[test]
fn import_fields_preserves_unknown_expressions_and_orders_unpaired_columns() {
    let source = association_import_source(
        "new FkField('z_id', 'zId', TargetDefinition::class),
 customFieldFactory(),
 new FkField('a_id', 'aId', TargetDefinition::class),
 new UnknownField('opaque'),",
    );
    let spec = import_definition(&source, Some(&association_import_target)).unwrap();
    assert_eq!(5, spec.fields.len());
    assert_eq!(FIELD_LOCKED, spec.fields[1].kind);
    assert_eq!("customFieldFactory()", spec.fields[1].raw);
    assert!(!spec.fields[1].editable);
    assert_eq!(FIELD_LOCKED, spec.fields[2].kind);
    assert_eq!("new UnknownField('opaque')", spec.fields[2].raw);
    assert_eq!("a_id", spec.fields[3].storage_name);
    assert_eq!("z_id", spec.fields[4].storage_name);
    let rendered = render_definition(&spec).unwrap();
    assert!(rendered.contains("customFieldFactory()"));
    assert!(rendered.contains("new UnknownField('opaque')"));
}

#[test]
fn import_definition_rejects_partial_dynamic_collections() {
    for statement in [
        "$fields[] = customFieldFactory();",
        "$fields[0] = customFieldFactory();",
        "$fields = dynamicFields();",
    ] {
        let source = association_import_source("").replacen(
            "return new FieldCollection([new IdField('id', 'id'), ]);",
            &format!("$fields = [new IdField('id', 'id')];\n{statement}\nreturn new FieldCollection($fields);"),
            1,
        );
        let error = import_definition(&source, None).unwrap_err();
        assert!(
            error
                .to_string()
                .contains("FieldCollection has no literal array"),
            "{statement}: {error}"
        );
    }
}
