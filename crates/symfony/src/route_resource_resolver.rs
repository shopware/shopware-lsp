//! Legacy `@Bundle` route resource resolution on top of the
//! filesystem-relative helpers. The bundle catalog is rebuilt only when the
//! PHP workspace generation changes.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::{Arc, Mutex, PoisonError};

use swlsp_indexer::{CancelToken, FileScanner, Result};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, SymbolKind};

use crate::route_resource::{
    MAX_ROUTE_RESOURCE_FILES, RouteResourceReference, route_resource_files,
    route_resource_files_at_target, route_resource_matches, route_resource_matches_target,
};
use crate::util::{clean_path, dir_path, join_path, relative_path};

const SYMFONY_BUNDLE_INTERFACE: &str = "Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface";
const MAX_BUNDLE_RESOURCE_CANDIDATES: usize = 1_000;
const BUNDLE_RESOURCE_ROOTS: &[&str] =
    &["Resources/config", "config", "Controller", "src/Controller"];

/// Indexed workspace paths below a directory (implemented by the scanner).
pub trait IndexedResourcePaths: Send + Sync {
    fn resource_paths(&self, directory: &str, limit: usize) -> Result<Vec<String>>;
}

impl IndexedResourcePaths for FileScanner {
    fn resource_paths(&self, directory: &str, limit: usize) -> Result<Vec<String>> {
        FileScanner::resource_paths(self, directory, limit)
    }
}

/// A conventional bundle file offered for `@Bundle/...` completion.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct BundleResourceCandidate {
    pub value: String,
    pub path: String,
}

#[derive(Default)]
struct BundleCatalog {
    roots: HashMap<String, Vec<String>>,
    names: HashMap<String, String>,
}

pub struct RouteResourceResolver {
    paths: Option<Arc<dyn IndexedResourcePaths>>,
    php: Option<PhpIndex>,
    catalog: Mutex<Option<(u64, Arc<BundleCatalog>)>>,
}

impl RouteResourceResolver {
    pub fn new(php: Option<PhpIndex>, paths: Option<Arc<dyn IndexedResourcePaths>>) -> Self {
        Self {
            paths,
            php,
            catalog: Mutex::new(None),
        }
    }

    pub fn files(&self, current_path: &str, reference: &RouteResourceReference) -> Vec<String> {
        if !reference.path.starts_with('@') {
            return route_resource_files(current_path, reference);
        }
        let mut result = Vec::new();
        for target in self.bundle_targets(&reference.path) {
            result.extend(route_resource_files_at_target(&target, reference));
            if result.len() >= MAX_ROUTE_RESOURCE_FILES {
                result.truncate(MAX_ROUTE_RESOURCE_FILES);
                break;
            }
        }
        unique_route_resource_paths(result)
    }

    pub fn matches(
        &self,
        current_path: &str,
        candidate: &str,
        reference: &RouteResourceReference,
    ) -> bool {
        if !reference.path.starts_with('@') {
            return route_resource_matches(current_path, candidate, reference);
        }
        self.bundle_targets(&reference.path)
            .iter()
            .any(|target| route_resource_matches_target(target, candidate, reference))
    }

    fn bundle_targets(&self, resource: &str) -> Vec<String> {
        let Some((name, relative)) = route_bundle_resource_parts(resource) else {
            return Vec::new();
        };
        let Some(catalog) = self.bundle_catalog() else {
            return Vec::new();
        };
        let roots = catalog
            .roots
            .get(&swlsp_textutil::lowercase(&name))
            .cloned()
            .unwrap_or_default();
        let targets = roots
            .into_iter()
            .map(|root| {
                if relative.is_empty() {
                    clean_path(&root)
                } else {
                    join_path(&[&root, &relative])
                }
            })
            .collect();
        unique_route_resource_paths(targets)
    }

    /// The conventional bundle config and controller files.
    pub fn bundle_resource_candidates(&self, cancel: &CancelToken) -> Vec<BundleResourceCandidate> {
        let Some(catalog) = self.bundle_catalog() else {
            return Vec::new();
        };
        let ordered: BTreeMap<&String, &Vec<String>> = catalog.roots.iter().collect();
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for (key, roots) in ordered {
            let name = catalog.names.get(key).map(String::as_str).unwrap_or("");
            for root in roots {
                for conventional in BUNDLE_RESOURCE_ROOTS {
                    let base = join_path(&[root, conventional]);
                    let Some(paths) = self.paths.as_ref() else {
                        continue;
                    };
                    if cancel.is_cancelled() || result.len() >= MAX_BUNDLE_RESOURCE_CANDIDATES {
                        continue;
                    }
                    let Ok(found) =
                        paths.resource_paths(&base, MAX_BUNDLE_RESOURCE_CANDIDATES - result.len())
                    else {
                        continue;
                    };
                    for path in found {
                        let Some(relative) = relative_path(root, &path) else {
                            continue;
                        };
                        let value = format!("@{name}/{relative}");
                        if !seen.insert(value.clone()) {
                            continue;
                        }
                        result.push(BundleResourceCandidate { value, path });
                    }
                }
            }
        }
        result.sort_by(|left, right| left.value.cmp(&right.value));
        result
    }

    fn bundle_catalog(&self) -> Option<Arc<BundleCatalog>> {
        let php = self.php.as_ref()?;
        let revision = php.revision();
        let mut cached = self.catalog.lock().unwrap_or_else(PoisonError::into_inner);
        if let Some((cached_revision, catalog)) = cached.as_ref()
            && *cached_revision == revision
        {
            return Some(catalog.clone());
        }
        let snapshot = php.semantic_snapshot();
        let mut catalog = BundleCatalog::default();
        for symbol in snapshot.global_symbols() {
            if symbol.kind != SymbolKind::Class
                || symbol.path.is_empty()
                || symbol.flags.has(Flags::INTERNAL)
                || !snapshot.is_subtype_of(&symbol.fully_qualified, SYMFONY_BUNDLE_INTERFACE)
            {
                continue;
            }
            let key = swlsp_textutil::lowercase(&symbol.name);
            catalog
                .names
                .insert(key.clone(), symbol.name.as_str().to_owned());
            catalog
                .roots
                .entry(key)
                .or_default()
                .push(clean_path(&dir_path(&symbol.path)));
        }
        for roots in catalog.roots.values_mut() {
            *roots = unique_route_resource_paths(std::mem::take(roots));
        }
        let catalog = Arc::new(catalog);
        *cached = Some((revision, catalog.clone()));
        Some(catalog)
    }
}

fn route_bundle_resource_parts(resource: &str) -> Option<(String, String)> {
    let resource = resource.trim();
    let rest = resource.strip_prefix('@')?;
    let normalized = rest.replace('\\', "/");
    let normalized = normalized.trim_start_matches('/');
    let separator = normalized.find('/').filter(|separator| *separator > 0)?;
    let name = &normalized[..separator];
    let relative = normalized[separator + 1..].trim_start_matches('/');
    Some((name.to_owned(), relative.to_owned()))
}

fn unique_route_resource_paths(paths: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut result: Vec<String> = paths
        .into_iter()
        .filter(|path| !path.is_empty())
        .map(|path| clean_path(&path))
        .filter(|path| seen.insert(path.clone()))
        .collect();
    result.sort();
    result
}

#[cfg(test)]
#[path = "tests_routes/route_resource_resolver.rs"]
mod tests;
