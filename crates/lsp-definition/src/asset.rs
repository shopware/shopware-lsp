use std::collections::HashSet;
use std::sync::Arc;

use swlsp_asset::{Index, Kind, Package, ReferenceKind, Resource};
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{LineIndex, TextRange};

use crate::common::{ext_lower, protocol_range, request_uri};
use crate::php_common::php_context;

pub struct AssetDefinitionProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl AssetDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for AssetDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(node)) = (&self.index, request.node.as_ref()) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        let extension = ext_lower(uri);
        if extension != ".php" && extension != ".twig" {
            return Vec::new();
        }
        let Some(reference) = swlsp_asset::reference_at(uri, node) else {
            return Vec::new();
        };
        if extension == ".php" {
            let Some(php) = &self.php else {
                return Vec::new();
            };
            let context = php_context(Some(php), request);
            if !swlsp_asset::validate_php_reference(context.as_ref(), &reference, php) {
                return Vec::new();
            }
        }
        let resources = match reference.kind {
            ReferenceKind::EncoreEntry => index.find(&reference.name, Kind::EncoreEntry),
            ReferenceKind::Importmap => index.find_importmap_entrypoint(&reference.name),
            ReferenceKind::ViteEntry => index.find(&reference.name, Kind::ViteEntry),
            ReferenceKind::AsseticNamed => Ok(index.find_assetic_named(&reference.name)),
            ReferenceKind::AssetPackage => {
                return index
                    .find_packages(&reference.name)
                    .map(|packages| package_locations(&packages))
                    .unwrap_or_default();
            }
            ReferenceKind::Asset => {
                if reference.assetic {
                    index.find_assetic_assets(&reference.name, &reference.package)
                } else {
                    index.find_assets_for_package(&reference.name, &reference.package)
                }
            }
        };
        resources
            .map(|resources| resource_locations(&resources))
            .unwrap_or_default()
    }
}

fn ranged_location(file: &str, range: TextRange) -> protocol::Location {
    let mut location = protocol::Location {
        uri: swlsp_uriutil::file_uri(file),
        range: protocol::Range::default(),
    };
    if range.is_empty() {
        return location;
    }
    if let Ok(source) = std::fs::read(file) {
        let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
        location.range = protocol_range(&lines, range);
    }
    location
}

fn package_locations(packages: &[Package]) -> Vec<protocol::Location> {
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for package in packages {
        if package.file.is_empty() {
            continue;
        }
        let location = ranged_location(&package.file, package.range);
        if !seen.insert((location.uri.clone(), package.range)) {
            continue;
        }
        result.push(location);
    }
    result
}

fn resource_locations(resources: &[Resource]) -> Vec<protocol::Location> {
    let mut seen_targets = HashSet::new();
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for resource in resources {
        if !resource.target.is_empty()
            && std::fs::metadata(&resource.target).is_ok_and(|info| !info.is_dir())
        {
            let uri = swlsp_uriutil::file_uri(&resource.target);
            if seen_targets.insert(uri.clone()) {
                result.push(protocol::Location {
                    uri,
                    range: protocol::Range::default(),
                });
            }
        }
        if resource.file.is_empty() || resource.range.is_empty() || resource.file == resource.target
        {
            continue;
        }
        let location = ranged_location(&resource.file, resource.range);
        if !seen.insert((location.uri.clone(), resource.range)) {
            continue;
        }
        result.push(location);
    }
    result
}
