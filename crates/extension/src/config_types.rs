//! shopware-cli extension configuration (`.shopware-extension.yml`). These
//! types mirror `shopware-cli/internal/extension/config.go`; they describe
//! extension metadata, not the LSP's `.config/shopware/lsp.yaml`.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::yaml_decode::{Decoder, Id, NULL_TAG, R, Target, struct_target};

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Config {
    /// Date-based compatibility behavior, formatted as `YYYY-MM-DD`.
    pub compatibility_date: String,
    pub store: ConfigStore,
    pub build: ConfigBuild,
    pub changelog: ConfigChangelog,
    pub validation: ConfigValidation,
    /// The configuration file this snapshot was read from (not decoded).
    pub storage_location: String,
    /// The Go error text of a partial decode (not decoded).
    pub decode_error: String,
}

struct_target!(Config, "Config", {
    "compatibility_date" => compatibility_date,
    "store" => store,
    "build" => build,
    "changelog" => changelog,
    "validation" => validation,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuild {
    /// Additional bundles to be considered for building.
    pub extra_bundles: Vec<ConfigExtraBundle>,
    /// Overrides the Shopware version constraint used for building.
    pub shopware_version_constraint: String,
    pub zip: ConfigBuildZip,
}

struct_target!(ConfigBuild, "ConfigBuild", {
    "extraBundles" => extra_bundles,
    "shopwareVersionConstraint" => shopware_version_constraint,
    "zip" => zip,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZip {
    pub composer: ConfigBuildZipComposer,
    pub assets: ConfigBuildZipAssets,
    pub pack: ConfigBuildZipPack,
    pub checksum: ConfigBuildZipChecksum,
}

struct_target!(ConfigBuildZip, "ConfigBuildZip", {
    "composer" => composer,
    "assets" => assets,
    "pack" => pack,
    "checksum" => checksum,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipChecksum {
    /// Files excluded from the checksum calculation.
    pub ignore: Vec<String>,
}

struct_target!(ConfigBuildZipChecksum, "ConfigBuildZipChecksum", { "ignore" => ignore });

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipComposer {
    pub enabled: bool,
    pub before_hooks: Vec<String>,
    pub after_hooks: Vec<String>,
    pub excluded_packages: Vec<String>,
}

struct_target!(ConfigBuildZipComposer, "ConfigBuildZipComposer", {
    "enabled" => enabled,
    "before_hooks" => before_hooks,
    "after_hooks" => after_hooks,
    "excluded_packages" => excluded_packages,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipAssets {
    pub enabled: bool,
    pub enable_asset_caching: bool,
    pub before_hooks: Vec<String>,
    pub after_hooks: Vec<String>,
    pub enable_es_build_for_admin: bool,
    pub enable_es_build_for_storefront: bool,
    pub disable_sass: bool,
    pub npm_strict: bool,
    pub additional_caches: Vec<ConfigBuildZipAssetsAdditionalCache>,
}

struct_target!(ConfigBuildZipAssets, "ConfigBuildZipAssets", {
    "enabled" => enabled,
    "enable_asset_caching" => enable_asset_caching,
    "before_hooks" => before_hooks,
    "after_hooks" => after_hooks,
    "enable_es_build_for_admin" => enable_es_build_for_admin,
    "enable_es_build_for_storefront" => enable_es_build_for_storefront,
    "disable_sass" => disable_sass,
    "npm_strict" => npm_strict,
    "additional_caches" => additional_caches,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipAssetsAdditionalCache {
    pub path: String,
    pub source_paths: Vec<String>,
}

struct_target!(ConfigBuildZipAssetsAdditionalCache, "ConfigBuildZipAssetsAdditionalCache", {
    "path" => path,
    "source_paths" => source_paths,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipPackExcludes {
    pub paths: Vec<String>,
}

struct_target!(ConfigBuildZipPackExcludes, "ConfigBuildZipPackExcludes", { "paths" => paths });

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigBuildZipPack {
    pub excludes: ConfigBuildZipPackExcludes,
    pub before_hooks: Vec<String>,
}

struct_target!(ConfigBuildZipPack, "ConfigBuildZipPack", {
    "excludes" => excludes,
    "before_hooks" => before_hooks,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigExtraBundle {
    /// Relative to the extension root (src folder).
    pub path: String,
    /// Defaults to the folder name of `path` when empty.
    pub name: String,
}

struct_target!(ConfigExtraBundle, "ConfigExtraBundle", { "path" => path, "name" => name });

/// The extension's Store listing metadata.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStore {
    pub availabilities: Option<Vec<String>>,
    pub default_locale: Option<String>,
    pub localizations: Option<Vec<String>>,
    #[serde(rename = "type")]
    pub kind: Option<String>,
    pub icon: Option<String>,
    pub automatic_bugfix_version_compatibility: Option<bool>,
    pub meta_title: ConfigTranslated<String>,
    pub meta_description: ConfigTranslated<String>,
    pub description: ConfigTranslated<String>,
    pub installation_manual: ConfigTranslated<String>,
    pub tags: ConfigTranslated<Vec<String>>,
    pub videos: ConfigTranslated<Vec<String>>,
    pub highlights: ConfigTranslated<Vec<String>>,
    pub features: ConfigTranslated<Vec<String>>,
    pub faq: ConfigTranslated<Vec<ConfigStoreFaq>>,
    pub images: Option<Vec<ConfigStoreImage>>,
    pub image_directory: Option<String>,
    pub demo_shops: Option<Vec<ConfigStoreDemoShop>>,
}

struct_target!(ConfigStore, "ConfigStore", {
    "availabilities" => availabilities,
    "default_locale" => default_locale,
    "localizations" => localizations,
    "type" => kind,
    "icon" => icon,
    "automatic_bugfix_version_compatibility" => automatic_bugfix_version_compatibility,
    "meta_title" => meta_title,
    "meta_description" => meta_description,
    "description" => description,
    "installation_manual" => installation_manual,
    "tags" => tags,
    "videos" => videos,
    "highlights" => highlights,
    "features" => features,
    "faq" => faq,
    "images" => images,
    "image_directory" => image_directory,
    "demo_shops" => demo_shops,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStoreDemoShop {
    #[serde(rename = "type")]
    pub kind: String,
    pub link: String,
    pub localization: String,
    pub login_name: String,
    pub login_password: String,
}

struct_target!(ConfigStoreDemoShop, "ConfigStoreDemoShop", {
    "type" => kind,
    "link" => link,
    "localization" => localization,
    "login_name" => login_name,
    "login_password" => login_password,
});

/// A value per Store language.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ConfigTranslated<T> {
    pub german: Option<T>,
    pub english: Option<T>,
}

impl<T> Default for ConfigTranslated<T> {
    fn default() -> Self {
        ConfigTranslated {
            german: None,
            english: None,
        }
    }
}

impl<T: Target + Default> Target for ConfigTranslated<T> {
    fn type_name() -> String {
        format!("extension.ConfigTranslated[{}]", T::qualified_name())
    }

    fn qualified_name() -> String {
        format!("extension.ConfigTranslated[{}]", T::qualified_name())
    }

    fn mapping(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        decoder.mapping_struct(id, self)
    }
}

impl<T: Target + Default> crate::yaml_decode::StructTarget for ConfigTranslated<T> {
    const FIELDS: &'static [&'static str] = &["de", "en"];

    fn field(&mut self, decoder: &mut Decoder<'_>, index: usize, value: Id) -> R<()> {
        match index {
            0 => decoder.unmarshal(value, &mut self.german)?,
            _ => decoder.unmarshal(value, &mut self.english)?,
        };
        Ok(())
    }
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStoreFaq {
    pub question: String,
    pub answer: String,
    pub position: i64,
}

struct_target!(ConfigStoreFaq, "ConfigStoreFaq", {
    "question" => question,
    "answer" => answer,
    "position" => position,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStoreImage {
    /// Relative to the extension root.
    pub file: String,
    pub activate: ConfigStoreImageActivate,
    pub preview: ConfigStoreImagePreview,
    /// Ascending gallery position.
    pub position: i64,
    /// Deprecated: use `position`.
    pub priority: i64,
}

struct_target!(ConfigStoreImage, "ConfigStoreImage", {
    "file" => file,
    "activate" => activate,
    "preview" => preview,
    "position" => position,
    "priority" => priority,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStoreImageActivate {
    pub german: bool,
    pub english: bool,
}

struct_target!(ConfigStoreImageActivate, "ConfigStoreImageActivate", {
    "de" => german,
    "en" => english,
});

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigStoreImagePreview {
    pub german: bool,
    pub english: bool,
}

struct_target!(ConfigStoreImagePreview, "ConfigStoreImagePreview", {
    "de" => german,
    "en" => english,
});

/// Extension validation settings.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigValidation {
    pub ignore: ConfigValidationList,
    pub store_compliance: bool,
    /// Overrides the PHP version used for linting (e.g. `8.4`).
    pub php_version: String,
}

struct_target!(ConfigValidation, "ConfigValidation", {
    "ignore" => ignore,
    "store_compliance" => store_compliance,
    "php_version" => php_version,
});

/// Go `ConfigValidationList` (a named slice type).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct ConfigValidationList(pub Vec<ConfigValidationIgnore>);

impl std::ops::Deref for ConfigValidationList {
    type Target = Vec<ConfigValidationIgnore>;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

impl Target for ConfigValidationList {
    fn type_name() -> String {
        "extension.ConfigValidationList".to_owned()
    }

    fn qualified_name() -> String {
        "extension.ConfigValidationList".to_owned()
    }

    fn null(&mut self) -> bool {
        self.0.clear();
        true
    }

    fn sequence(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        self.0.sequence(decoder, id)
    }
}

/// Mirrors `shopware-cli/internal/changelog.Config`.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigChangelog {
    pub enabled: bool,
    pub pattern: String,
    pub template: String,
    pub variables: BTreeMap<String, String>,
    /// Not decoded.
    pub vcs_url: String,
}

struct_target!(ConfigChangelog, "ConfigChangelog", {
    "enabled" => enabled,
    "pattern" => pattern,
    "template" => template,
    "variables" => variables,
});

/// A validation ignore entry: CLI shorthand identifier or an object.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ConfigValidationIgnore {
    pub identifier: String,
    pub path: String,
    pub message: String,
}

impl Target for ConfigValidationIgnore {
    fn type_name() -> String {
        "extension.ConfigValidationIgnore".to_owned()
    }

    fn qualified_name() -> String {
        "extension.ConfigValidationIgnore".to_owned()
    }

    /// Go `UnmarshalYAML`: scalars are identifiers; anything else decodes
    /// as the plain object type in a nested decoder whose type errors make
    /// this element fail.
    fn decode(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        if decoder.short_tag(id) == NULL_TAG {
            return decoder.dispatch(id, self);
        }
        let node = decoder.node(id);
        if node.kind == crate::yaml_decode::NodeKind::Scalar {
            self.identifier = node.value.clone();
            return Ok(true);
        }
        let mut nested = decoder.nested();
        let mut object = IgnoreObject(std::mem::take(self));
        let result = nested.unmarshal(id, &mut object);
        *self = object.0;
        result?;
        if nested.terrors.is_empty() {
            return Ok(true);
        }
        let errors = std::mem::take(&mut nested.terrors);
        decoder.terrors.extend(errors);
        Ok(false)
    }
}

/// Go's local `type object ConfigValidationIgnore` used by `UnmarshalYAML`.
struct IgnoreObject(ConfigValidationIgnore);

impl Target for IgnoreObject {
    fn type_name() -> String {
        "extension.object".to_owned()
    }

    fn mapping(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        decoder.mapping_struct(id, self)
    }
}

impl crate::yaml_decode::StructTarget for IgnoreObject {
    const FIELDS: &'static [&'static str] = &["identifier", "path", "message"];

    fn field(&mut self, decoder: &mut Decoder<'_>, index: usize, value: Id) -> R<()> {
        let target = match index {
            0 => &mut self.0.identifier,
            1 => &mut self.0.path,
            _ => &mut self.0.message,
        };
        decoder.unmarshal(value, target)?;
        Ok(())
    }
}
