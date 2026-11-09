//! Tests for enum field.

use super::inheritance::{
    backfill_free, field, field_by_property, php_ok, require_issue_code, stored, validation_codes,
};
use crate::*;

#[test]
fn class_based_enum_field_renders_imports_and_round_trips() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Catalog".into(),
        entity_name: "acme_catalog".into(),
        shopware_version: "^6.6.10".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                enum_class: "Acme\\Catalog\\Status".into(),
                enum_case: "Active".into(),
                enum_backing_type: "string".into(),
                required: true,
                migration_default: "'active'".into(),
                ..stored("status", FIELD_ENUM, "status", "status")
            },
        ],
        ..EntitySpec::default()
    });
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );

    let definition = render_definition(&spec).unwrap();
    let entity = render_entity(&spec).unwrap();
    assert!(php_ok(&definition), "{definition}");
    assert!(php_ok(&entity), "{entity}");
    assert!(
        definition
            .contains("use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\EnumField;")
    );
    assert!(definition.contains("use Acme\\Catalog\\Status;"));
    assert!(definition.contains("new EnumField('status', 'status', Status::Active)"));
    assert!(entity.contains("protected Status $status;"));
    assert!(entity.contains("public function getStatus(): Status"));

    let mut imported = import_definition(&definition, None).unwrap();
    let status = field_by_property(&imported.fields, "status");
    assert_eq!(FIELD_ENUM, status.kind);
    assert_eq!("Acme\\Catalog\\Status", status.enum_class);
    assert_eq!("Active", status.enum_case);
    assert!(
        status.enum_backing_type.is_empty(),
        "the constructor does not encode the enum backing type"
    );
    for field in &mut imported.fields {
        if field.property_name == "status" {
            field.enum_backing_type = "string".into();
        }
    }

    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(before, after);
}

#[test]
fn enum_field_sql_types_and_validation() {
    let enum_field = |backing: &str| FieldSpec {
        kind: FIELD_ENUM.into(),
        property_name: "state".into(),
        enum_backing_type: backing.into(),
        ..FieldSpec::default()
    };
    assert_eq!("VARCHAR(255)", sql_type(&enum_field("string")).unwrap());
    assert_eq!("INT", sql_type(&enum_field("int")).unwrap());
    let error = sql_type(&enum_field("")).unwrap_err();
    assert!(error.to_string().contains("has no backing type"));

    let mut base = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Catalog".into(),
        entity_name: "acme_catalog".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                enum_class: "Acme\\Catalog\\State".into(),
                enum_case: "Ready".into(),
                enum_backing_type: "int".into(),
                ..stored("state", FIELD_ENUM, "state", "state")
            },
        ],
        ..EntitySpec::default()
    });
    base.shopware_version = "6.6.9".into();
    require_issue_code(
        &validate_spec(&base),
        "entity.field.enum.version.unsupported",
    );
    base.shopware_version = "6.6.10".into();
    assert!(
        !validation_codes(&validate_spec(&base))
            .join(",")
            .contains("entity.field.enum")
    );

    base.fields[1].enum_case.clear();
    require_issue_code(&validate_spec(&base), "entity.field.enum.case.invalid");
    base.fields[1].enum_case = "Ready".into();
    base.fields[1].enum_backing_type.clear();
    require_issue_code(&validate_spec(&base), "entity.field.enum.backing.invalid");
}
