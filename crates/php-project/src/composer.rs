//! Composer JSON shapes.

use std::collections::BTreeMap;
use std::path::Path;

use serde::Deserialize;

use crate::{Package, join};

#[derive(Default, Deserialize)]
#[serde(default)]
pub(crate) struct ComposerFile {
    #[serde(deserialize_with = "null_default")]
    pub require: BTreeMap<String, String>,
    #[serde(rename = "require-dev", deserialize_with = "null_default")]
    pub require_dev: BTreeMap<String, String>,
    #[serde(deserialize_with = "null_default")]
    pub config: ComposerConfig,
    #[serde(deserialize_with = "null_default")]
    pub autoload: ComposerAutoload,
    #[serde(rename = "autoload-dev", deserialize_with = "null_default")]
    pub autoload_dev: ComposerAutoload,
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub(crate) struct ComposerConfig {
    #[serde(deserialize_with = "null_default")]
    pub platform: BTreeMap<String, serde_json::Value>,
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub(crate) struct ComposerAutoload {
    #[serde(rename = "psr-4", deserialize_with = "null_default")]
    pub psr4: BTreeMap<String, StringOrStrings>,
    #[serde(deserialize_with = "null_default")]
    pub classmap: Vec<String>,
    #[serde(deserialize_with = "null_default")]
    pub files: Vec<String>,
    #[serde(rename = "exclude-from-classmap", deserialize_with = "null_default")]
    pub exclude: Vec<String>,
}

#[derive(Deserialize)]
#[serde(untagged)]
pub(crate) enum StringOrStrings {
    One(String),
    Many(Vec<String>),
}

impl StringOrStrings {
    pub fn iter(&self) -> impl Iterator<Item = &str> {
        let values: &[String] = match self {
            StringOrStrings::One(value) => std::slice::from_ref(value),
            StringOrStrings::Many(values) => values,
        };
        values.iter().map(String::as_str)
    }
}

/// JSON `null` decodes to the zero value, as in Go.
fn null_default<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Default + Deserialize<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

/// A non-blank platform string value.
pub(crate) fn platform_string(raw: &serde_json::Value) -> Option<String> {
    match raw {
        serde_json::Value::String(value) if !value.trim().is_empty() => Some(value.clone()),
        _ => None,
    }
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct LockFile {
    #[serde(deserialize_with = "null_default")]
    packages: Vec<LockPackage>,
    #[serde(rename = "packages-dev", deserialize_with = "null_default")]
    packages_dev: Vec<LockPackage>,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct LockPackage {
    #[serde(deserialize_with = "null_default")]
    name: String,
    #[serde(deserialize_with = "null_default")]
    version: String,
    #[serde(deserialize_with = "null_default")]
    autoload: ComposerAutoload,
}

pub(crate) fn load_packages(root: &Path) -> Vec<Package> {
    let Ok(content) = std::fs::read(root.join("composer.lock")) else {
        return Vec::new();
    };
    let Ok(lock) = serde_json::from_slice::<LockFile>(&content) else {
        return Vec::new();
    };
    lock.packages
        .iter()
        .chain(&lock.packages_dev)
        .map(|package| {
            let install_path = join(&root.join("vendor"), &package.name);
            let mut psr4: BTreeMap<String, Vec<_>> = BTreeMap::new();
            for (namespace, paths) in &package.autoload.psr4 {
                for path in paths.iter() {
                    psr4.entry(namespace.clone())
                        .or_default()
                        .push(join(&install_path, path));
                }
            }
            Package {
                name: package.name.clone(),
                version: package.version.clone(),
                psr4,
                classmap: package
                    .autoload
                    .classmap
                    .iter()
                    .map(|path| join(&install_path, path))
                    .collect(),
                files: package
                    .autoload
                    .files
                    .iter()
                    .map(|path| join(&install_path, path))
                    .collect(),
                install_path,
            }
        })
        .collect()
}
