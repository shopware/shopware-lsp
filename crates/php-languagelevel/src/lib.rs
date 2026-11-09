//! Centralizes the PHP version required by syntax understood by the
//! permissive PHP parser.

mod detect;

pub use detect::{Occurrence, detect};

use swlsp_php_project::Version;

/// A stable identifier for a version-gated PHP language feature.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Feature {
    Attributes,
    NamedArguments,
    MatchExpressions,
    NullsafeOperator,
    PropertyPromotion,
    ThrowExpressions,
    UnionTypes,
    Enums,
    IntersectionTypes,
    ReadonlyProperties,
    DnfTypes,
    ReadonlyClasses,
    TypedClassConstants,
    AsymmetricVisibility,
    PropertyHooks,
}

impl Feature {
    pub const ALL: [Feature; 15] = [
        Feature::Attributes,
        Feature::NamedArguments,
        Feature::MatchExpressions,
        Feature::NullsafeOperator,
        Feature::PropertyPromotion,
        Feature::ThrowExpressions,
        Feature::UnionTypes,
        Feature::Enums,
        Feature::IntersectionTypes,
        Feature::ReadonlyProperties,
        Feature::DnfTypes,
        Feature::ReadonlyClasses,
        Feature::TypedClassConstants,
        Feature::AsymmetricVisibility,
        Feature::PropertyHooks,
    ];

    /// The stable string ID used in configuration and diagnostics.
    pub fn id(self) -> &'static str {
        match self {
            Feature::Attributes => "attributes",
            Feature::NamedArguments => "named-arguments",
            Feature::MatchExpressions => "match-expressions",
            Feature::NullsafeOperator => "nullsafe-operator",
            Feature::PropertyPromotion => "property-promotion",
            Feature::ThrowExpressions => "throw-expressions",
            Feature::UnionTypes => "union-types",
            Feature::Enums => "enums",
            Feature::IntersectionTypes => "intersection-types",
            Feature::ReadonlyProperties => "readonly-properties",
            Feature::DnfTypes => "dnf-types",
            Feature::ReadonlyClasses => "readonly-classes",
            Feature::TypedClassConstants => "typed-class-constants",
            Feature::AsymmetricVisibility => "asymmetric-visibility",
            Feature::PropertyHooks => "property-hooks",
        }
    }

    pub fn from_id(id: &str) -> Option<Feature> {
        Feature::ALL.into_iter().find(|feature| feature.id() == id)
    }
}

/// The first PHP release supporting a feature.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Definition {
    pub feature: Feature,
    pub name: &'static str,
    pub major: i64,
    pub minor: i64,
}

/// Returns the registry definition of `feature`. Every [`Feature`] variant is
/// registered; string IDs are resolved through [`Feature::from_id`].
pub fn lookup(feature: Feature) -> Definition {
    let (name, major, minor) = match feature {
        Feature::Attributes => ("Attributes", 8, 0),
        Feature::NamedArguments => ("Named arguments", 8, 0),
        Feature::MatchExpressions => ("Match expressions", 8, 0),
        Feature::NullsafeOperator => ("The nullsafe operator", 8, 0),
        Feature::PropertyPromotion => ("Constructor property promotion", 8, 0),
        Feature::ThrowExpressions => ("Throw expressions", 8, 0),
        Feature::UnionTypes => ("Union types", 8, 0),
        Feature::Enums => ("Enums", 8, 1),
        Feature::IntersectionTypes => ("Intersection types", 8, 1),
        Feature::ReadonlyProperties => ("Readonly properties", 8, 1),
        Feature::DnfTypes => ("Disjunctive normal form types", 8, 2),
        Feature::ReadonlyClasses => ("Readonly classes", 8, 2),
        Feature::TypedClassConstants => ("Typed class constants", 8, 3),
        Feature::AsymmetricVisibility => ("Asymmetric property visibility", 8, 4),
        Feature::PropertyHooks => ("Property hooks", 8, 4),
    };
    Definition {
        feature,
        name,
        major,
        minor,
    }
}

/// Whether `version` supports `feature`.
pub fn supports(version: Version, feature: Feature) -> bool {
    let definition = lookup(feature);
    version.at_least(definition.major, definition.minor)
}

/// Whether `version` supports the feature with string ID `id`. Unknown IDs
/// are deliberately unsupported so a new feature cannot silently leak into
/// older projects without a registry entry.
pub fn supports_id(version: Version, id: &str) -> bool {
    Feature::from_id(id).is_some_and(|feature| supports(version, feature))
}

/// Registry definitions in deterministic version/ID order.
pub fn all() -> Vec<Definition> {
    let mut result: Vec<Definition> = Feature::ALL.into_iter().map(lookup).collect();
    result.sort_by(|left, right| {
        (left.major, left.minor, left.feature.id()).cmp(&(
            right.major,
            right.minor,
            right.feature.id(),
        ))
    });
    result
}

/// The user-facing version diagnostic.
pub fn unsupported_message(definition: &Definition, configured: Version) -> String {
    format!(
        "{} require PHP {}.{}; the project is configured for PHP {}.{}",
        definition.name, definition.major, definition.minor, configured.major, configured.minor
    )
}

#[cfg(test)]
mod tests;
