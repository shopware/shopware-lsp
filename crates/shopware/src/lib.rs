//! Shopware-specific semantic extensions layered on top of the
//! framework-neutral PHP engine: Shopware version resolution and PHP call
//! type inference.

mod small_entities;
mod type_extension;
mod version;

pub use small_entities::{SMALL_ENTITIES, entity_name_from_identifier, small_entities};
pub use type_extension::PhpTypeExtension;
pub use version::{ResolvedVersion, VersionError, VersionResolver, VersionSource};
