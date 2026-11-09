use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex, PoisonError, RwLock};

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    SupplementalPathIndexer, SupplementalSyntaxIndexer, TransactionalClearer, TransactionalRemover,
};
use swlsp_php_types::{equal_fold, go_to_lower};

use crate::assetic::AsseticCatalog;
use crate::model::{Kind, Package, ReferenceKind, Resource, Usage, normalize_name};
use crate::package::{deduplicate_packages, packages_in_php, packages_in_xml, packages_in_yaml};
use crate::parser::{parse_entrypoints, parse_importmap, parse_manifest, parse_webpack_config};
use crate::paths::{base, clean, dir, ext, join, rel, slash_base, to_slash};
use crate::reference::{reference_range, references};
use crate::vite::parse_vite_config;

const STATIC_ASSET_EXTENSIONS: &[&str] = &[
    ".avif", ".bmp", ".css", ".eot", ".gif", ".ico", ".jpeg", ".jpg", ".js", ".json", ".less",
    ".map", ".mjs", ".mp3", ".mp4", ".ogg", ".otf", ".pdf", ".png", ".sass", ".scss", ".svg",
    ".ttf", ".webm", ".webp", ".woff", ".woff2", ".coffee", ".dart",
];

/// An immutable generation-scoped view of the asset names diagnostics use
/// together.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct NameCatalog {
    pub assets: Vec<String>,
    pub encore_entries: Vec<String>,
    pub importmap_entries: Vec<String>,
    pub vite_entries: Vec<String>,
}

#[derive(Default)]
struct Tracked {
    resources: HashSet<String>,
    usages: HashSet<String>,
}

#[derive(Default)]
struct Cache {
    generation: u64,
    built_at: u64,
    resources: Option<Arc<[Resource]>>,
    names: Option<Arc<NameCatalog>>,
}

struct Shared {
    tracked: RwLock<Tracked>,
    cache: Mutex<Cache>,
}

impl Shared {
    fn invalidate(&self) {
        let mut cache = self.cache.lock().unwrap_or_else(PoisonError::into_inner);
        cache.generation += 1;
        cache.resources = None;
        cache.names = None;
    }

    fn tracked(&self) -> std::sync::RwLockWriteGuard<'_, Tracked> {
        self.tracked.write().unwrap_or_else(PoisonError::into_inner)
    }
}

/// Indexes public assets, asset metadata (manifest, entrypoints, importmap,
/// webpack/Vite configs), asset package declarations and asset usages in
/// PHP and Twig.
pub struct Index {
    root: String,
    public_roots: [String; 2],
    normalized_root: String,
    records: DataIndexer<Resource>,
    usages: DataIndexer<Usage>,
    packages: DataIndexer<Package>,
    owned_store: Option<Store>,
    shared: Arc<Shared>,
    assetic: AsseticCatalog,
}

impl Index {
    /// Opens the repositories in `store`, or in an owned
    /// `<config_dir>/assets.db` when none is given.
    pub fn new(root: &str, config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let owned_store = match store {
            Some(_) => None,
            None => Some(Store::open(config_dir.as_ref().join("assets.db"))?),
        };
        let result = Self::open_in(
            root,
            store.or(owned_store.as_ref()).expect("store"),
            owned_store.clone(),
        );
        if result.is_err()
            && let Some(store) = &owned_store
        {
            let _ = store.close();
        }
        result
    }

    fn open_in(root: &str, store: &Store, owned_store: Option<Store>) -> Result<Self> {
        let records = DataIndexer::in_store(store, "symfony.assets")?;
        let usages = DataIndexer::in_store(store, "symfony.asset_usages")?;
        let packages = DataIndexer::in_store(store, "symfony.asset_packages")?;
        let tracked = Tracked {
            resources: records.get_all_file_paths()?.into_iter().collect(),
            usages: usages.get_all_file_paths()?.into_iter().collect(),
        };
        let root = clean(root);
        Ok(Self {
            public_roots: [
                join(&[root.as_str(), "public"]),
                join(&[root.as_str(), "web"]),
            ],
            normalized_root: to_slash(&root),
            assetic: AsseticCatalog::new(&root),
            root,
            records,
            usages,
            packages,
            owned_store,
            shared: Arc::new(Shared {
                tracked: RwLock::new(tracked),
                cache: Mutex::new(Cache::default()),
            }),
        })
    }

    fn resources_in_file(&self, file: &ParsedFile) -> Vec<Resource> {
        let path = file.path();
        match base(path).to_lowercase().as_str() {
            "manifest.json" if is_asset_metadata_path(path) => {
                return parse_manifest(path, file.syntax_tree());
            }
            "entrypoints.json" if is_asset_metadata_path(path) => {
                return parse_entrypoints(path, file.syntax_tree());
            }
            "importmap.php" => return parse_importmap(path, file.syntax_tree()),
            "installed.php" if !is_composer_installed_path(path) => {
                return parse_importmap(path, file.syntax_tree());
            }
            "webpack.config.js" => return parse_webpack_config(path, file.syntax_tree()),
            "vite.config.js" | "vite.config.ts" => {
                return parse_vite_config(path, file.syntax_tree());
            }
            _ => {}
        }
        match self.public_name(path) {
            Some(name) if is_static_asset_path(path) => vec![Resource {
                name,
                file: path.to_owned(),
                target: path.to_owned(),
                kind: Kind::PublicFile,
                ..Resource::default()
            }],
            _ => Vec::new(),
        }
    }

    /// Every resource sorted by case-folded name, kind and file. The view
    /// is cached per index generation.
    pub fn resources_view(&self) -> Result<Arc<[Resource]>> {
        let mut cache = self
            .shared
            .cache
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        self.resources_locked(&mut cache)
    }

    pub fn resources(&self) -> Result<Vec<Resource>> {
        Ok(self.resources_view()?.to_vec())
    }

    fn resources_locked(&self, cache: &mut Cache) -> Result<Arc<[Resource]>> {
        if let Some(resources) = &cache.resources
            && cache.built_at == cache.generation
        {
            return Ok(resources.clone());
        }
        let mut resources = self.records.get_all_values()?;
        resources.sort_by(|left, right| {
            if left.name != right.name {
                return go_to_lower(&left.name).cmp(&go_to_lower(&right.name));
            }
            left.kind
                .cmp(&right.kind)
                .then_with(|| left.file.cmp(&right.file))
        });
        let resources: Arc<[Resource]> = resources.into();
        cache.resources = Some(resources.clone());
        cache.built_at = cache.generation;
        cache.names = None;
        Ok(resources)
    }

    pub fn name_catalog(&self) -> Result<Arc<NameCatalog>> {
        let mut cache = self
            .shared
            .cache
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        let resources = self.resources_locked(&mut cache)?;
        if let Some(names) = &cache.names {
            return Ok(names.clone());
        }
        let mut catalog = NameCatalog::default();
        let mut seen: [HashSet<String>; 4] = Default::default();
        for resource in resources.iter() {
            let (slot, list) = match resource.kind {
                Kind::EncoreEntry => (0, &mut catalog.encore_entries),
                Kind::ImportmapModule if resource.entrypoint => (1, &mut catalog.importmap_entries),
                Kind::ImportmapModule => continue,
                Kind::ViteEntry => (2, &mut catalog.vite_entries),
                _ => (3, &mut catalog.assets),
            };
            if seen[slot].insert(go_to_lower(&resource.name)) {
                list.push(resource.name.clone());
            }
        }
        let catalog = Arc::new(catalog);
        cache.names = Some(catalog.clone());
        Ok(catalog)
    }

    pub fn names(&self) -> Result<Vec<String>> {
        Ok(self.name_catalog()?.assets.clone())
    }

    pub fn entry_names(&self) -> Result<Vec<String>> {
        Ok(self.name_catalog()?.encore_entries.clone())
    }

    pub fn importmap_entry_names(&self) -> Result<Vec<String>> {
        Ok(self.name_catalog()?.importmap_entries.clone())
    }

    pub fn vite_entry_names(&self) -> Result<Vec<String>> {
        Ok(self.name_catalog()?.vite_entries.clone())
    }

    /// The entrypoint declaration together with installed-package metadata
    /// of the same module; empty unless one declaration is an entrypoint.
    pub fn find_importmap_entrypoint(&self, name: &str) -> Result<Vec<Resource>> {
        let resources = self.find(name, Kind::ImportmapModule)?;
        if resources.iter().any(|resource| resource.entrypoint) {
            Ok(resources)
        } else {
            Ok(Vec::new())
        }
    }

    pub fn find(&self, name: &str, kind: Kind) -> Result<Vec<Resource>> {
        let name = normalize_name(name);
        Ok(self
            .resources_view()?
            .iter()
            .filter(|resource| resource.kind == kind && equal_fold(&resource.name, &name))
            .cloned()
            .collect())
    }

    pub fn vite_entries_for_target(&self, target: &str) -> Result<Vec<Resource>> {
        if target.trim().is_empty() {
            return Ok(Vec::new());
        }
        let target = clean(target);
        Ok(self
            .resources_view()?
            .iter()
            .filter(|resource| {
                resource.kind == Kind::ViteEntry
                    && !resource.target.is_empty()
                    && clean(&resource.target) == target
            })
            .cloned()
            .collect())
    }

    /// Resources addressable by `asset()` (everything but Encore/Vite entries).
    pub fn find_assets(&self, name: &str) -> Result<Vec<Resource>> {
        let name = normalize_name(name);
        Ok(self
            .resources_view()?
            .iter()
            .filter(|resource| is_asset_kind(resource.kind) && equal_fold(&resource.name, &name))
            .cloned()
            .collect())
    }

    pub fn packages(&self) -> Result<Vec<Package>> {
        Ok(deduplicate_packages(self.packages.get_all_values()?))
    }

    pub fn package_names(&self) -> Result<Vec<String>> {
        let mut seen = HashSet::new();
        let mut result: Vec<String> = self
            .packages()?
            .into_iter()
            .filter(|package| seen.insert(go_to_lower(&package.name)))
            .map(|package| package.name)
            .collect();
        result.sort_by_cached_key(|name| go_to_lower(name));
        Ok(result)
    }

    pub fn find_packages(&self, name: &str) -> Result<Vec<Package>> {
        let name = name.trim();
        Ok(self
            .packages()?
            .into_iter()
            .filter(|package| equal_fold(&package.name, name))
            .collect())
    }

    pub fn find_assets_for_package(&self, name: &str, package_name: &str) -> Result<Vec<Resource>> {
        if package_name.trim().is_empty() {
            return self.find_assets(name);
        }
        let packages = self.find_packages(package_name)?;
        if packages.is_empty() {
            return Ok(Vec::new());
        }
        let targets = package_asset_targets(name, package_name, &packages);
        let theme_target = format!("theme/*/{}", normalize_name(name));
        Ok(self
            .resources_view()?
            .iter()
            .filter(|resource| {
                is_asset_kind(resource.kind)
                    && targets.iter().any(|target| {
                        equal_fold(&resource.name, target)
                            || *target == theme_target
                                && theme_resource_matches(&resource.name, name)
                    })
            })
            .cloned()
            .collect())
    }

    /// Resources matching a legacy Assetic file, directory or glob operand.
    /// Bundle operands use their `bundles/<name>` public prefix.
    pub fn find_assetic_assets(&self, name: &str, package_name: &str) -> Result<Vec<Resource>> {
        let name = normalize_name(name);
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let pattern = if package_name.is_empty() {
            name
        } else {
            let base_path = assetic_bundle_base_path(package_name);
            if base_path.is_empty() {
                return Ok(Vec::new());
            }
            normalize_name(&format!("{base_path}/{name}"))
        };
        Ok(self
            .resources_view()?
            .iter()
            .filter(|resource| {
                is_asset_kind(resource.kind) && assetic_resource_matches(&resource.name, &pattern)
            })
            .cloned()
            .collect())
    }

    pub fn assetic_named_names(&self) -> Vec<String> {
        let mut seen = HashSet::new();
        let mut result: Vec<String> = self
            .assetic
            .resources(false)
            .into_iter()
            .filter(|resource| {
                !resource.name.is_empty() && seen.insert(go_to_lower(&resource.name))
            })
            .map(|resource| resource.name)
            .collect();
        result.sort_by_cached_key(|name| go_to_lower(name));
        result
    }

    pub fn find_assetic_named(&self, name: &str) -> Vec<Resource> {
        let name = name.strip_prefix('@').unwrap_or(name);
        self.assetic
            .resources(false)
            .into_iter()
            .filter(|resource| equal_fold(&resource.name, name))
            .collect()
    }

    pub fn reload_assetic_catalog(&self) {
        self.assetic.resources(true);
    }

    /// Asset names for Assetic completion, relative to the bundle's public
    /// prefix when `package_name` is given.
    pub fn assetic_names(&self, package_name: &str) -> Result<Vec<String>> {
        let prefix = if package_name.is_empty() {
            String::new()
        } else {
            let base_path = assetic_bundle_base_path(package_name);
            if base_path.is_empty() {
                return Ok(Vec::new());
            }
            format!("{base_path}/")
        };
        let lower_prefix = go_to_lower(&prefix);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for resource in self.resources_view()?.iter() {
            if !is_asset_kind(resource.kind) {
                continue;
            }
            let mut name = normalize_name(&resource.name);
            if !prefix.is_empty() {
                if !go_to_lower(&name).starts_with(&lower_prefix) || name.len() < prefix.len() {
                    continue;
                }
                name = name[prefix.len()..].to_owned();
            }
            if !name.is_empty() && seen.insert(go_to_lower(&name)) {
                result.push(name);
            }
        }
        result.sort_by_cached_key(|name| go_to_lower(name));
        Ok(result)
    }

    /// Logical names of the package's resources (relative to its base
    /// path; `theme` without a base path strips `theme/<name>/`).
    pub fn names_for_package(&self, package_name: &str) -> Result<Vec<String>> {
        if package_name.trim().is_empty() {
            return self.names();
        }
        let packages = self.find_packages(package_name)?;
        if packages.is_empty() {
            return Ok(Vec::new());
        }
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for resource in self.resources_view()?.iter() {
            if !is_asset_kind(resource.kind) {
                continue;
            }
            for package in &packages {
                let logical = if equal_fold(package_name, "theme") && package.base_path.is_empty() {
                    theme_logical_name(&resource.name)
                } else {
                    package_logical_name(&resource.name, package)
                };
                let Some(logical) = logical else {
                    continue;
                };
                if seen.insert(go_to_lower(&logical)) {
                    result.push(logical);
                }
                break;
            }
        }
        result.sort_by_cached_key(|name| go_to_lower(name));
        Ok(result)
    }

    pub fn usages(&self, name: &str, kind: ReferenceKind) -> Result<Vec<Usage>> {
        let name = normalize_name(name);
        let mut result: Vec<Usage> = self
            .usages
            .get_all_values()?
            .into_iter()
            .filter(|usage| usage.kind == kind && equal_fold(&usage.name, &name))
            .collect();
        result.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    pub fn usages_for_package(
        &self,
        name: &str,
        kind: ReferenceKind,
        package_name: &str,
    ) -> Result<Vec<Usage>> {
        Ok(self
            .usages(name, kind)?
            .into_iter()
            .filter(|usage| equal_fold(&usage.package, package_name))
            .collect())
    }

    fn public_name(&self, path: &str) -> Option<String> {
        match self.public_relative(path) {
            Some(relative) if !relative.is_empty() => Some(normalize_name(&to_slash(&relative))),
            _ => self.bundle_public_name(path),
        }
    }

    fn bundle_public_name(&self, path: &str) -> Option<String> {
        let (relative, public_name) = self.bundle_public_relative(path)?;
        (!relative.is_empty()).then(|| format!("bundles/{public_name}/{relative}"))
    }

    /// (`relative`, `public name`) for a path below a workspace bundle's
    /// `Resources/public`.
    pub(crate) fn bundle_public_relative(&self, path: &str) -> Option<(String, String)> {
        let normalized = to_slash(&clean(path));
        const MARKER: &str = "/Resources/public";
        let position = normalized.rfind(MARKER)?;
        let after = position + MARKER.len();
        if normalized.len() > after && normalized.as_bytes()[after] != b'/' {
            return None;
        }
        let bundle_dir = &normalized[..position];
        if !slash_path_within(&self.normalized_root, bundle_dir) {
            return None;
        }
        let lower = go_to_lower(&slash_base(bundle_dir));
        let public_name = lower.strip_suffix("bundle").unwrap_or(&lower);
        if public_name.is_empty() {
            return None;
        }
        Some((normalize_name(&normalized[after..]), public_name.to_owned()))
    }

    /// The path relative to `<root>/public` or `<root>/web` (empty for the
    /// public root itself).
    pub(crate) fn public_relative(&self, path: &str) -> Option<String> {
        let path = clean(path);
        for root in &self.public_roots {
            if path == *root {
                return Some(String::new());
            }
            if path.len() > root.len()
                && path.starts_with(root.as_str())
                && is_separator(path.as_bytes()[root.len()])
            {
                return Some(path[root.len() + 1..].to_owned());
            }
        }
        None
    }

    fn is_resource_candidate(&self, path: &str) -> bool {
        match base(path).to_lowercase().as_str() {
            "webpack.config.js" | "vite.config.js" | "vite.config.ts" | "importmap.php" => {
                return true;
            }
            "installed.php" if !is_composer_installed_path(path) => return true,
            "manifest.json" | "entrypoints.json" if is_asset_metadata_path(path) => return true,
            _ => {}
        }
        self.public_name(path).is_some() && is_static_asset_path(path)
    }

    fn is_package_candidate(&self, path: &str) -> bool {
        let extension = ext(path).to_lowercase();
        let normalized = to_slash(&clean(path));
        if extension == ".php" {
            if normalized.contains("/DependencyInjection/") {
                return true;
            }
            return rel(&self.root, path)
                .is_ok_and(|relative| to_slash(&relative).starts_with("config/"));
        }
        if !matches!(extension.as_str(), ".xml" | ".yaml" | ".yml") {
            return false;
        }
        if normalized.contains("/Resources/config/") || normalized.contains("/DependencyInjection/")
        {
            return true;
        }
        let Ok(relative) = rel(&self.root, path) else {
            return false;
        };
        if relative == ".." || relative.starts_with(&format!("..{}", std::path::MAIN_SEPARATOR)) {
            return false;
        }
        let relative = to_slash(&relative);
        relative == "config" || relative.starts_with("config/")
    }

    fn tracked(&self, path: &str) -> (bool, bool) {
        let tracked = self
            .shared
            .tracked
            .read()
            .unwrap_or_else(PoisonError::into_inner);
        (
            tracked.resources.contains(path),
            tracked.usages.contains(path),
        )
    }

    fn publish(
        &self,
        mutation: Option<&Mutation>,
        update: impl FnOnce(&Shared) + Send + 'static,
    ) -> Result<()> {
        let shared = self.shared.clone();
        let publish = move || update(&shared);
        match mutation {
            None => {
                publish();
                Ok(())
            }
            Some(mutation) => mutation.after_commit(publish),
        }
    }

    fn publish_path(
        &self,
        path: &str,
        resource: bool,
        present: bool,
        mutation: Option<&Mutation>,
    ) -> Result<()> {
        let path = path.to_owned();
        self.publish(mutation, move |shared| {
            {
                let mut tracked = shared.tracked();
                let paths = if resource {
                    &mut tracked.resources
                } else {
                    &mut tracked.usages
                };
                if present {
                    paths.insert(path);
                } else {
                    paths.remove(&path);
                }
            }
            if resource {
                shared.invalidate();
            }
        })
    }

    fn remove_paths(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let paths = paths.to_vec();
        self.publish(mutation, move |shared| {
            {
                let mut tracked = shared.tracked();
                for path in &paths {
                    tracked.resources.remove(path);
                    tracked.usages.remove(path);
                }
            }
            shared.invalidate();
        })
    }

    fn reset_paths(&self, mutation: Option<&Mutation>) -> Result<()> {
        self.publish(mutation, |shared| {
            *shared.tracked() = Tracked::default();
            shared.invalidate();
        })
    }
}

/// A file's keyed packages, resources, and usages prepared ahead of the
/// write transaction. `None` leaves that repository untouched.
struct PreparedAssets {
    packages: Option<Vec<(String, Package)>>,
    resources: Option<Vec<(String, Resource)>>,
    usages: Option<Vec<(String, Usage)>>,
}

impl Index {
    /// Collects the file's keyed records without writing anything; `None`
    /// for files that neither are candidates nor have tracked records.
    fn extract(&self, file: &ParsedFile) -> Option<PreparedAssets> {
        let path = file.path();
        let resource_candidate = self.is_resource_candidate(path);
        let usage_candidate = is_usage_candidate(file);
        let package_candidate = self.is_package_candidate(path);
        let (resource_tracked, usage_tracked) = self.tracked(path);
        if !resource_candidate
            && !usage_candidate
            && !package_candidate
            && !resource_tracked
            && !usage_tracked
        {
            return None;
        }
        Some(PreparedAssets {
            packages: package_candidate.then(|| package_items(file)),
            resources: (resource_candidate || resource_tracked)
                .then(|| resource_items(self.resources_in_file(file))),
            usages: (usage_candidate || usage_tracked).then(|| usage_items(file)),
        })
    }

    /// Writes prepared records for `file` and publishes its tracked paths
    /// after commit.
    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedAssets>) -> Result<()> {
        let Some(prepared) = prepared else {
            return Ok(());
        };
        let path = file.path();
        if let Some(items) = &prepared.packages {
            self.packages.batch_save_items_in(
                file.mutation(),
                [(path, items.iter().map(|(key, item)| (key.as_str(), item)))],
            )?;
        }
        if let Some(items) = &prepared.resources {
            self.records.batch_save_items_in(
                file.mutation(),
                [(path, items.iter().map(|(key, item)| (key.as_str(), item)))],
            )?;
            self.publish_path(path, true, !items.is_empty(), file.mutation())?;
        }
        if let Some(items) = &prepared.usages {
            self.usages.batch_save_items_in(
                file.mutation(),
                [(path, items.iter().map(|(key, item)| (key.as_str(), item)))],
            )?;
            return self.publish_path(path, false, !items.is_empty(), file.mutation());
        }
        Ok(())
    }
}

fn package_items(file: &ParsedFile) -> Vec<(String, Package)> {
    let path = file.path();
    let packages = match (file.syntax_tree(), file.extension()) {
        (Some(tree), ".xml") => packages_in_xml(path, &tree.root()),
        (Some(tree), ".yaml" | ".yml") => packages_in_yaml(path, &tree.root()),
        (Some(tree), ".php") => packages_in_php(path, &tree.root()),
        _ => Vec::new(),
    };
    keyed(packages.into_iter().enumerate().map(|(position, package)| {
        let key = format!(
            "{}:{}",
            go_to_lower(&package.name),
            go_to_lower(&package.base_path)
        );
        (position, key, package)
    }))
}

fn resource_items(resources: Vec<Resource>) -> Vec<(String, Resource)> {
    keyed(
        resources
            .into_iter()
            .enumerate()
            .filter_map(|(position, mut resource)| {
                resource.name = normalize_name(&resource.name);
                if resource.name.is_empty() {
                    return None;
                }
                let key = format!(
                    "{}:{}",
                    resource.kind.ordinal(),
                    go_to_lower(&resource.name)
                );
                Some((position, key, resource))
            }),
    )
}

fn usage_items(file: &ParsedFile) -> Vec<(String, Usage)> {
    let path = file.path();
    let usages = file
        .syntax_tree()
        .map(|tree| references(path, &tree.root()))
        .unwrap_or_default();
    keyed(
        usages
            .into_iter()
            .enumerate()
            .filter_map(|(position, reference)| {
                if reference.name.is_empty() {
                    return None;
                }
                let usage = Usage {
                    range: reference_range(&reference),
                    name: reference.name,
                    package: reference.package,
                    kind: reference.kind,
                    file: path.to_owned(),
                };
                let key = format!(
                    "{}:{}:{}",
                    usage.kind.ordinal(),
                    go_to_lower(&usage.name),
                    u32::from(usage.range.start())
                );
                Some((position, key, usage))
            }),
    )
}

/// Saves `items` for `path`; equal keys get a `#<position>` suffix like Go.
fn keyed<T>(items: impl IntoIterator<Item = (usize, String, T)>) -> Vec<(String, T)> {
    let mut result: Vec<(String, T)> = Vec::new();
    let mut positions: HashMap<String, usize> = HashMap::new();
    for (position, mut key, item) in items {
        if positions.contains_key(&key) {
            key = format!("{key}#{position}");
        }
        // Go writes into a map: a repeated suffixed key replaces the earlier
        // item.
        match positions.get(&key) {
            Some(&existing) => result[existing].1 = item,
            None => {
                positions.insert(key.clone(), result.len());
                result.push((key, item));
            }
        }
    }
    result
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "symfony.assets"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.persist(file, self.extract(file).as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        join_errors([
            self.records.batch_delete_by_file_paths(paths),
            self.usages.batch_delete_by_file_paths(paths),
            self.packages.batch_delete_by_file_paths(paths),
        ])?;
        self.remove_paths(paths, None)
    }

    fn close(&self) -> Result<()> {
        let mut results = vec![
            self.records.close(),
            self.usages.close(),
            self.packages.close(),
        ];
        if let Some(store) = &self.owned_store {
            results.push(store.close());
        }
        join_errors(results)
    }

    fn clear(&self) -> Result<()> {
        join_errors([
            self.records.clear(),
            self.usages.clear(),
            self.packages.clear(),
        ])?;
        self.reset_paths(None)
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
        Some(self)
    }

    fn as_supplemental_syntax(&self) -> Option<&dyn SupplementalSyntaxIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl PreparingIndexer for Index {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(self.extract(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<Option<PreparedAssets>>()) {
            Some(prepared) => self.persist(file, prepared.as_ref()),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        join_errors([
            self.records
                .batch_delete_by_file_paths_in(Some(mutation), paths),
            self.usages
                .batch_delete_by_file_paths_in(Some(mutation), paths),
            self.packages
                .batch_delete_by_file_paths_in(Some(mutation), paths),
        ])?;
        self.remove_paths(paths, Some(mutation))
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        join_errors([
            self.records.clear_in(Some(mutation)),
            self.usages.clear_in(Some(mutation)),
            self.packages.clear_in(Some(mutation)),
        ])?;
        self.reset_paths(Some(mutation))
    }
}

impl SupplementalPathIndexer for Index {
    fn should_enter_directory(&self, path: &str) -> bool {
        match self.public_relative(path) {
            None => self.bundle_public_relative(path).is_some(),
            Some(relative) if relative.is_empty() => true,
            Some(relative) => {
                let first = relative
                    .split(is_separator_char)
                    .next()
                    .unwrap_or(&relative);
                !is_dynamic_public_directory(first)
            }
        }
    }

    fn should_index_path(&self, path: &str) -> bool {
        match self.public_relative(path) {
            Some(relative) => {
                !relative.is_empty()
                    && is_indexable_public_relative(&relative)
                    && is_static_asset_path(path)
            }
            None => self.bundle_public_name(path).is_some() && is_static_asset_path(path),
        }
    }
}

impl SupplementalSyntaxIndexer for Index {
    fn should_preparse_path(&self, path: &str) -> bool {
        match base(path).to_lowercase().as_str() {
            "manifest.json" | "entrypoints.json" => is_asset_metadata_path(path),
            "importmap.php" => true,
            "installed.php" => !is_composer_installed_path(path),
            _ => false,
        }
    }
}

fn join_errors(results: impl IntoIterator<Item = Result<()>>) -> Result<()> {
    let messages: Vec<String> = results
        .into_iter()
        .filter_map(|result| result.err().map(|error| error.to_string()))
        .collect();
    if messages.is_empty() {
        Ok(())
    } else {
        Err(Error::msg(messages.join("\n")))
    }
}

fn is_separator(byte: u8) -> bool {
    byte == b'/' || byte == std::path::MAIN_SEPARATOR as u8
}

fn is_separator_char(character: char) -> bool {
    character == '/' || character == std::path::MAIN_SEPARATOR
}

fn is_asset_kind(kind: Kind) -> bool {
    kind != Kind::EncoreEntry && kind != Kind::ViteEntry
}

pub(crate) fn slash_path_within(root: &str, candidate: &str) -> bool {
    if root == "." {
        return candidate == "."
            || candidate != ".." && !candidate.starts_with("../") && !candidate.starts_with('/');
    }
    if root.ends_with('/') {
        return candidate.starts_with(root);
    }
    candidate == root
        || candidate.len() > root.len()
            && candidate.starts_with(root)
            && candidate.as_bytes()[root.len()] == b'/'
}

fn is_indexable_public_relative(relative: &str) -> bool {
    match relative.find(is_separator_char) {
        None => true,
        Some(position) => !is_dynamic_public_directory(&relative[..position]),
    }
}

fn is_dynamic_public_directory(name: &str) -> bool {
    [
        "cache",
        "files",
        "media",
        "thumbnail",
        "thumbnails",
        "upload",
        "uploads",
    ]
    .iter()
    .any(|candidate| equal_fold(name, candidate))
}

fn is_static_asset_path(path: &str) -> bool {
    STATIC_ASSET_EXTENSIONS.contains(&ext(path).to_lowercase().as_str())
}

fn is_asset_metadata_path(path: &str) -> bool {
    let normalized = to_slash(path).to_lowercase();
    ["/public/", "/web/", "/build/", "/.vite/"]
        .iter()
        .any(|marker| normalized.contains(marker))
}

fn is_composer_installed_path(path: &str) -> bool {
    equal_fold(&base(&dir(path)), "composer")
}

fn is_usage_candidate(file: &ParsedFile) -> bool {
    let source = file.source();
    match file.extension() {
        ".php" => source.contains("getUrl") || source.contains("getVersion"),
        ".twig" => [
            "asset(",
            "importmap(",
            "encore_entry_",
            "vite_entry_",
            "<link",
            "<script",
            "<img",
        ]
        .iter()
        .any(|needle| source.contains(needle)),
        _ => false,
    }
}

fn package_asset_targets(name: &str, package_name: &str, packages: &[Package]) -> Vec<String> {
    let name = normalize_name(name);
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut add = |value: String| {
        let value = normalize_name(&value);
        if !value.is_empty() && seen.insert(go_to_lower(&value)) {
            result.push(value);
        }
    };
    for package in packages {
        if package.base_path.is_empty() {
            add(name.clone());
        } else {
            add(format!("{}/{name}", package.base_path));
        }
    }
    if equal_fold(package_name, "theme") {
        add(format!("theme/*/{name}"));
    }
    result
}

fn package_logical_name(resource_name: &str, package: &Package) -> Option<String> {
    let resource_name = normalize_name(resource_name);
    if package.base_path.is_empty() {
        return Some(resource_name);
    }
    let prefix = format!("{}/", normalize_name(&package.base_path));
    if resource_name.len() <= prefix.len()
        || !resource_name.is_char_boundary(prefix.len())
        || !equal_fold(&resource_name[..prefix.len()], &prefix)
    {
        return None;
    }
    Some(resource_name[prefix.len()..].to_owned())
}

fn theme_resource_matches(resource_name: &str, logical_name: &str) -> bool {
    theme_logical_name(resource_name)
        .is_some_and(|logical| equal_fold(&logical, &normalize_name(logical_name)))
}

/// `theme/<id>/<path>` → `<path>`.
fn theme_logical_name(resource_name: &str) -> Option<String> {
    let name = normalize_name(resource_name);
    let parts: Vec<&str> = name.split('/').collect();
    if parts.len() < 3 || !equal_fold(parts[0], "theme") {
        return None;
    }
    Some(parts[2..].join("/"))
}

fn assetic_resource_matches(name: &str, pattern: &str) -> bool {
    let name = normalize_name(name);
    let pattern = normalize_name(pattern);
    if equal_fold(&name, &pattern) {
        return true;
    }
    if pattern.ends_with('/') {
        return go_to_lower(&name).starts_with(&go_to_lower(&pattern));
    }
    if !pattern.contains(['*', '?', '[']) {
        return false;
    }
    swlsp_pathmatch::go_glob(&go_to_lower(&pattern), &go_to_lower(&name), false)
}

fn assetic_bundle_base_path(package_name: &str) -> String {
    let name = package_name.trim();
    let name = go_to_lower(name.strip_prefix('@').unwrap_or(name));
    let name = name.strip_suffix("bundle").unwrap_or(&name);
    if name.is_empty() {
        String::new()
    } else {
        format!("bundles/{name}")
    }
}

/// Advanced by every committed change to the indexed resources, usages and
/// packages, and by changes of the compiled Assetic resources.
impl swlsp_indexer::Revisioned for Index {
    fn revision(&self) -> u64 {
        self.records.revision()
            + self.usages.revision()
            + self.packages.revision()
            + self.assetic.revision()
    }
}
