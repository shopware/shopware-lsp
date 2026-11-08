//! Loads the Composer model that defines a PHP workspace (port of
//! `internal/php/project`).

mod bootstrap;
mod composer;
mod paths;
#[cfg(test)]
mod tests;

use std::collections::{BTreeMap, HashSet};
use std::fmt;
use std::io;
use std::path::{Path, PathBuf};

pub use paths::{absolute, clean, join};

use composer::{ComposerAutoload, ComposerFile};

/// A semantic version triple. Ordering compares major, minor, then patch.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Version {
    pub major: i64,
    pub minor: i64,
    pub patch: i64,
}

impl Version {
    pub const fn new(major: i64, minor: i64, patch: i64) -> Self {
        Version {
            major,
            minor,
            patch,
        }
    }

    pub fn at_least(self, major: i64, minor: i64) -> bool {
        self >= Version::new(major, minor, 0)
    }

    pub fn at_least_patch(self, major: i64, minor: i64, patch: i64) -> bool {
        self >= Version::new(major, minor, patch)
    }

    /// -1, 0, or 1 when `self` is lower than, equal to, or greater than
    /// `other` (Go `Compare`).
    pub fn compare(self, other: Version) -> i32 {
        match self.cmp(&other) {
            std::cmp::Ordering::Less => -1,
            std::cmp::Ordering::Equal => 0,
            std::cmp::Ordering::Greater => 1,
        }
    }
}

impl fmt::Display for Version {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}.{}.{}", self.major, self.minor, self.patch)
    }
}

/// The root Composer project, its installed dependencies, and the PHP
/// platform it targets.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Model {
    pub root: PathBuf,
    pub php_version: Version,
    pub psr4: BTreeMap<String, Vec<PathBuf>>,
    pub classmap: Vec<PathBuf>,
    pub files: Vec<PathBuf>,
    pub exclude: Vec<PathBuf>,
    pub dependencies: Vec<Package>,
    pub required_extensions: Vec<String>,
    pub enabled_extensions: Vec<String>,
    pub disabled_extensions: Vec<String>,
    /// Extensions reported by the configured runtime. `None` means unknown
    /// (Go `nil`), which differs from an empty list.
    pub loaded_extensions: Option<Vec<String>>,
}

/// One package from `composer.lock`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Package {
    pub name: String,
    pub version: String,
    pub psr4: BTreeMap<String, Vec<PathBuf>>,
    pub classmap: Vec<PathBuf>,
    pub files: Vec<PathBuf>,
    pub install_path: PathBuf,
}

/// One namespace-to-source-root mapping from a Composer package.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Psr4Mapping {
    pub namespace: String,
    pub root: PathBuf,
}

/// Error returned while loading Composer metadata.
#[derive(Debug)]
pub enum Error {
    Io {
        context: String,
        source: io::Error,
    },
    Json {
        context: String,
        source: serde_json::Error,
    },
    OutsideRoot {
        directory: PathBuf,
        root: PathBuf,
    },
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Io { context, source } => write!(f, "{context}: {source}"),
            Error::Json { context, source } => write!(f, "{context}: {source}"),
            Error::OutsideRoot { directory, root } => write!(
                f,
                "composer directory {:?} is outside project root {:?}",
                directory.display().to_string(),
                root.display().to_string()
            ),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io { source, .. } => Some(source),
            Error::Json { source, .. } => Some(source),
            Error::OutsideRoot { .. } => None,
        }
    }
}

impl Model {
    /// Installed Composer version of the first matching package name.
    pub fn dependency_version(&self, names: &[&str]) -> Option<Version> {
        for name in names {
            if let Some(dependency) = self
                .dependencies
                .iter()
                .find(|dependency| eq_ignore_case(&dependency.name, name))
            {
                return parse_version_constraint(&dependency.version);
            }
        }
        None
    }

    /// Mappings owned by the nearest Composer package containing `directory`.
    /// Uninstalled custom plugins with their own `composer.json` do not
    /// appear in the root lock file, so they are read directly.
    pub fn psr4_mappings_for_directory(&self, directory: &Path) -> Result<Vec<Psr4Mapping>, Error> {
        let workspace_root = absolute(&self.root);
        let directory = absolute(directory);
        if !directory.starts_with(&workspace_root) {
            return Err(Error::OutsideRoot {
                directory,
                root: workspace_root,
            });
        }
        if let Some((package_root, composer)) =
            nearest_composer_package(&workspace_root, &directory)?
            && clean(&package_root) != clean(&workspace_root)
        {
            return Ok(composer_psr4_mappings(&package_root, &composer));
        }
        Ok(model_psr4_mappings(&self.psr4))
    }

    /// Merges explicit editor/runtime overrides with Composer requirements.
    /// Disabled entries win.
    pub fn configure_extensions<S: AsRef<str>>(&mut self, enabled: &[S], disabled: &[S]) {
        self.enabled_extensions = normalized_extensions(enabled.iter().map(AsRef::as_ref));
        let combined: Vec<String> = self
            .disabled_extensions
            .iter()
            .map(String::as_str)
            .chain(disabled.iter().map(AsRef::as_ref))
            .map(str::to_owned)
            .collect();
        self.disabled_extensions = normalized_extensions(combined.iter().map(String::as_str));
    }

    pub fn stub_extensions(&self) -> Vec<String> {
        let disabled: HashSet<String> = self
            .disabled_extensions
            .iter()
            .map(|extension| normalize_extension(extension))
            .collect();
        normalized_extensions(
            self.required_extensions
                .iter()
                .chain(&self.enabled_extensions)
                .map(String::as_str),
        )
        .into_iter()
        .filter(|extension| !disabled.contains(extension))
        .collect()
    }

    /// `(enabled, known)` availability of an extension.
    pub fn extension_availability(&self, extension: &str) -> (bool, bool) {
        let extension = normalize_extension(extension);
        if self
            .disabled_extensions
            .iter()
            .any(|disabled| normalize_extension(disabled) == extension)
        {
            return (false, true);
        }
        if let Some(loaded) = &self.loaded_extensions {
            if loaded
                .iter()
                .any(|current| normalize_extension(current) == extension)
            {
                return (true, true);
            }
            if self.stub_extensions().contains(&extension) {
                return (false, true);
            }
            return (false, false);
        }
        if self.stub_extensions().contains(&extension) {
            return (true, true);
        }
        (false, false)
    }

    /// Sorted, deduplicated PSR-4 and classmap roots of the project and its
    /// dependencies.
    pub fn source_roots(&self) -> Vec<PathBuf> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for paths in self.psr4.values() {
            for path in paths {
                if seen.insert(path.clone()) {
                    result.push(path.clone());
                }
            }
        }
        result.extend(self.classmap.iter().cloned());
        for dependency in &self.dependencies {
            for paths in dependency.psr4.values() {
                for path in paths {
                    if seen.insert(path.clone()) {
                        result.push(path.clone());
                    }
                }
            }
            for path in &dependency.classmap {
                if seen.insert(path.clone()) {
                    result.push(path.clone());
                }
            }
        }
        // Go sorts path strings byte-wise, unlike `Path`'s component order.
        result.sort_by(|left, right| left.as_os_str().cmp(right.as_os_str()));
        result
    }

    fn add_autoload(&mut self, base: &Path, autoload: &ComposerAutoload) {
        for (namespace, paths) in &autoload.psr4 {
            for path in paths.iter() {
                self.psr4
                    .entry(namespace.clone())
                    .or_default()
                    .push(join(base, path));
            }
        }
        for path in &autoload.classmap {
            self.classmap.push(join(base, path));
        }
        for path in &autoload.files {
            self.files.push(join(base, path));
        }
        for path in &autoload.exclude {
            self.exclude.push(join(base, path));
        }
    }
}

/// Loads `composer.json`, `composer.lock`, and PHPUnit bootstrap autoloads
/// beneath `root`. A missing `composer.json` yields defaults (PHP 8.2).
pub fn load(root: &Path) -> Result<Model, Error> {
    let root = absolute(root);
    let mut model = Model {
        root: root.clone(),
        php_version: Version::new(8, 2, 0),
        ..Model::default()
    };
    let content = match std::fs::read(root.join("composer.json")) {
        Ok(content) => content,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(model),
        Err(source) => {
            return Err(Error::Io {
                context: "read composer.json".to_owned(),
                source,
            });
        }
    };
    let composer: ComposerFile =
        serde_json::from_slice(&content).map_err(|source| Error::Json {
            context: "parse composer.json".to_owned(),
            source,
        })?;
    let mut constraint = composer.require.get("php").cloned().unwrap_or_default();
    if let Some(platform) = composer
        .config
        .platform
        .get("php")
        .and_then(composer::platform_string)
    {
        constraint = platform;
    }
    if let Some(version) = parse_version_constraint(&constraint) {
        model.php_version = version;
    }
    model.add_autoload(&root, &composer.autoload);
    model.add_autoload(&root, &composer.autoload_dev);
    let mut required: Vec<String> = composer
        .require
        .keys()
        .chain(composer.require_dev.keys())
        .filter_map(|name| composer_extension_name(name))
        .collect();
    let mut disabled = Vec::new();
    for (name, raw) in &composer.config.platform {
        let Some(extension) = composer_extension_name(name) else {
            continue;
        };
        if raw == &serde_json::Value::Bool(false) {
            disabled.push(extension);
            continue;
        }
        if composer::platform_string(raw).is_some() {
            required.push(extension);
        }
    }
    model.required_extensions = normalized_extensions(required.iter().map(String::as_str));
    model.disabled_extensions = normalized_extensions(disabled.iter().map(String::as_str));
    bootstrap::add_phpunit_bootstrap_autoloads(&mut model, &root);
    model.dependencies = composer::load_packages(&root);
    Ok(model)
}

fn nearest_composer_package(
    workspace_root: &Path,
    directory: &Path,
) -> Result<Option<(PathBuf, ComposerFile)>, Error> {
    let mut current = clean(directory);
    loop {
        let path = current.join("composer.json");
        match std::fs::read(&path) {
            Ok(content) => {
                let composer: ComposerFile =
                    serde_json::from_slice(&content).map_err(|source| Error::Json {
                        context: format!("parse {}", path.display()),
                        source,
                    })?;
                return Ok(Some((current, composer)));
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(source) => {
                return Err(Error::Io {
                    context: format!("read {}", path.display()),
                    source,
                });
            }
        }
        if current == workspace_root {
            return Ok(None);
        }
        match current.parent() {
            Some(parent) if parent != current => current = parent.to_path_buf(),
            _ => return Ok(None),
        }
    }
}

fn composer_psr4_mappings(package_root: &Path, composer: &ComposerFile) -> Vec<Psr4Mapping> {
    let mut result = Vec::new();
    for autoload in [&composer.autoload, &composer.autoload_dev] {
        for (namespace, paths) in &autoload.psr4 {
            for path in paths.iter() {
                result.push(Psr4Mapping {
                    namespace: namespace.clone(),
                    root: join(package_root, path),
                });
            }
        }
    }
    unique_psr4_mappings(result)
}

fn model_psr4_mappings(values: &BTreeMap<String, Vec<PathBuf>>) -> Vec<Psr4Mapping> {
    let mut result = Vec::new();
    for (namespace, roots) in values {
        for root in roots {
            result.push(Psr4Mapping {
                namespace: namespace.clone(),
                root: clean(root),
            });
        }
    }
    unique_psr4_mappings(result)
}

fn unique_psr4_mappings(mut values: Vec<Psr4Mapping>) -> Vec<Psr4Mapping> {
    values.sort_by(|left, right| {
        left.root
            .as_os_str()
            .cmp(right.root.as_os_str())
            .then_with(|| left.namespace.cmp(&right.namespace))
    });
    values.dedup();
    values
}

fn composer_extension_name(name: &str) -> Option<String> {
    let name = name.trim().to_lowercase();
    let extension = name.strip_prefix("ext-")?;
    if extension.is_empty() {
        return None;
    }
    Some(normalize_extension(extension))
}

/// Lowercases, strips an `ext-` prefix, and maps `-` to `_`.
pub fn normalize_extension(extension: &str) -> String {
    let extension = extension.trim().to_lowercase();
    let extension = extension.strip_prefix("ext-").unwrap_or(&extension);
    extension.replace('-', "_")
}

fn normalized_extensions<'a>(extensions: impl IntoIterator<Item = &'a str>) -> Vec<String> {
    let mut result: Vec<String> = extensions
        .into_iter()
        .map(normalize_extension)
        .filter(|extension| !extension.is_empty())
        .collect();
    result.sort();
    result.dedup();
    result
}

/// Extracts the first `major[.minor[.patch]]` number sequence from a
/// Composer constraint such as `^8.2 || ^8.3`.
pub fn parse_version_constraint(source: &str) -> Option<Version> {
    let bytes = source.as_bytes();
    let start = bytes.iter().position(u8::is_ascii_digit)?;
    let mut parts = [0i64; 3];
    let mut index = start;
    for (part, slot) in parts.iter_mut().enumerate() {
        if part > 0 {
            if bytes.get(index) != Some(&b'.')
                || !bytes.get(index + 1).is_some_and(u8::is_ascii_digit)
            {
                break;
            }
            index += 1;
        }
        let digits_start = index;
        while bytes.get(index).is_some_and(u8::is_ascii_digit) {
            index += 1;
        }
        *slot = parse_decimal(&source[digits_start..index]);
    }
    Some(Version::new(parts[0], parts[1], parts[2]))
}

fn parse_decimal(digits: &str) -> i64 {
    // Go's Sscanf leaves the zero value on overflow.
    digits.parse().unwrap_or(0)
}

/// Normalizes a namespace to its trailing-separator form (`App\`).
pub fn normalize_namespace(namespace: &str) -> String {
    format!("{}\\", namespace.trim_matches('\\'))
}

fn eq_ignore_case(left: &str, right: &str) -> bool {
    left.chars()
        .flat_map(char::to_lowercase)
        .eq(right.chars().flat_map(char::to_lowercase))
}
