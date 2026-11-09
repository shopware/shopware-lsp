//! Shopware version gates for generated DAL constructs. Unknown versions stay
//! permissive because the generator cannot prove a framework class is
//! unavailable.

use swlsp_php_project::{Version, parse_version_constraint};

use crate::model::{
    DEFINITION_BULK_EXTENSION, DEFINITION_ENTITY, DEFINITION_EXTENSION, DEFINITION_MAPPING,
    DefinitionKind,
};

const BULK_ENTITY_EXTENSION_MINIMUM_VERSION: Version = Version::new(6, 6, 10);
const ENUM_FIELD_MINIMUM_VERSION: Version = Version::new(6, 6, 10);

fn supported(version_constraint: &str, minimum: Version) -> bool {
    parse_version_constraint(version_constraint).is_none_or(|version| version >= minimum)
}

/// Whether BulkEntityExtension is available throughout the configured
/// Shopware version constraint.
pub fn bulk_entity_extension_supported(version_constraint: &str) -> bool {
    supported(version_constraint, BULK_ENTITY_EXTENSION_MINIMUM_VERSION)
}

/// Whether the class-based EnumField is available throughout the configured
/// Shopware compatibility range.
pub fn enum_field_supported(version_constraint: &str) -> bool {
    supported(version_constraint, ENUM_FIELD_MINIMUM_VERSION)
}

/// The class-based definition modes that may safely be generated for the
/// configured Shopware compatibility range.
pub fn definition_kinds_for_version(version_constraint: &str) -> Vec<DefinitionKind> {
    let mut kinds = vec![
        DEFINITION_ENTITY.to_owned(),
        DEFINITION_MAPPING.to_owned(),
        DEFINITION_EXTENSION.to_owned(),
    ];
    if bulk_entity_extension_supported(version_constraint) {
        kinds.push(DEFINITION_BULK_EXTENSION.to_owned());
    }
    kinds
}
