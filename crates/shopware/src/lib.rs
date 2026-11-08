//! Shopware-specific semantic extensions layered on top of the
//! framework-neutral PHP engine (port of the `internal/shopware` root
//! package): Shopware version resolution and PHP call type inference.

mod type_extension;
mod version;

pub use type_extension::PhpTypeExtension;
pub use version::{ResolvedVersion, VersionError, VersionResolver, VersionSource};
