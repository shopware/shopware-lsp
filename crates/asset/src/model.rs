use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

#[derive(
    Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize,
)]
pub enum Kind {
    #[default]
    PublicFile,
    ManifestAsset,
    EncoreEntry,
    ImportmapModule,
    ViteEntry,
    AsseticNamedAsset,
}

impl Kind {
    /// The stable numeric value used in persisted keys.
    pub(crate) fn ordinal(self) -> u8 {
        self as u8
    }
}

impl std::fmt::Display for Kind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Kind::ManifestAsset => "manifest asset",
            Kind::EncoreEntry => "Encore entry",
            Kind::ImportmapModule => "AssetMapper module",
            Kind::ViteEntry => "Vite entry",
            Kind::AsseticNamedAsset => "Assetic named asset",
            Kind::PublicFile => "public asset",
        })
    }
}

/// An addressable asset: a public file, a manifest key, an Encore/Vite
/// entry, an importmap module or an Assetic named formula.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Resource {
    pub name: String,
    pub file: String,
    pub target: String,
    pub kind: Kind,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    pub url: String,
    pub version: String,
    pub module_type: String,
    pub entrypoint: bool,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum ReferenceKind {
    #[default]
    Asset,
    EncoreEntry,
    AssetPackage,
    Importmap,
    ViteEntry,
    AsseticNamed,
}

impl ReferenceKind {
    pub(crate) fn ordinal(self) -> u8 {
        self as u8
    }
}

/// A persisted asset reference in PHP or Twig.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Usage {
    pub name: String,
    pub package: String,
    pub kind: ReferenceKind,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
}

/// One named Symfony Asset package. `base_path` is the workspace-public
/// prefix that maps a logical package path to an indexed resource name when
/// it is statically known.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Package {
    pub name: String,
    pub base_path: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    pub inferred: bool,
}

/// Slash-separated, without leading slashes or empty segments.
pub(crate) fn normalize_name(value: &str) -> String {
    let mut value = value.trim().replace('\\', "/");
    value = value.trim_start_matches('/').to_owned();
    while value.contains("//") {
        value = value.replace("//", "/");
    }
    value
}

mod range_serde {
    use serde::{Deserialize, Deserializer, Serialize, Serializer};
    use swlsp_syntax::TextRange;

    pub fn serialize<S: Serializer>(value: &TextRange, serializer: S) -> Result<S::Ok, S::Error> {
        (u32::from(value.start()), u32::from(value.end())).serialize(serializer)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<TextRange, D::Error> {
        let (start, end) = <(u32, u32)>::deserialize(deserializer)?;
        if start > end {
            return Err(serde::de::Error::custom("invalid text range"));
        }
        Ok(TextRange::new(start.into(), end.into()))
    }
}
