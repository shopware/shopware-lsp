use crate::*;

#[test]
fn bulk_entity_extension_version_support() {
    assert!(!bulk_entity_extension_supported("~6.6.9"));
    assert!(bulk_entity_extension_supported("~6.6.10"));
    assert!(bulk_entity_extension_supported("^6.7"));
    assert!(
        bulk_entity_extension_supported(""),
        "unknown target versions must stay permissive"
    );
    assert_eq!(
        definition_kinds_for_version("6.6.9"),
        vec![DEFINITION_ENTITY, DEFINITION_MAPPING, DEFINITION_EXTENSION]
    );
    assert_eq!(
        definition_kinds_for_version("6.6.10"),
        vec![
            DEFINITION_ENTITY,
            DEFINITION_MAPPING,
            DEFINITION_EXTENSION,
            DEFINITION_BULK_EXTENSION
        ]
    );
}

#[test]
fn enum_field_version_support() {
    assert!(!enum_field_supported("6.6.9"));
    assert!(enum_field_supported("6.6.10"));
    assert!(enum_field_supported("~6.7"));
    assert!(enum_field_supported(""));
}
