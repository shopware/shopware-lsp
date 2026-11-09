//! Asset, entrypoint and package references.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_asset::{
    Index, Kind, Package, Reference, ReferenceKind, Resource, Usage, reference_at, reference_range,
    references, validate_php_reference,
};
use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_textutil::eq_ignore_case;

use crate::{FileLines, document_location, lower_ext, php_context};

pub struct AssetReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl AssetReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for AssetReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(node), Some(_)) = (&self.index, &request.node, &request.document)
        else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        let extension = lower_ext(uri);
        if extension != ".php" && extension != ".twig" {
            return Ok(Vec::new());
        }
        let Some(reference) = reference_at(uri, node) else {
            return Ok(Vec::new());
        };
        if extension == ".php" {
            let Some(php) = &self.php else {
                return Ok(Vec::new());
            };
            let context = php_context(Some(php), request);
            if !validate_php_reference(context.as_ref(), &reference, php) {
                return Ok(Vec::new());
            }
        }
        let current_path = swlsp_uriutil::path(uri).unwrap_or_default();
        let mut filtered: Vec<Usage> = index
            .usages_for_package(&reference.name, reference.kind, &reference.package)?
            .into_iter()
            .filter(|usage| usage.file != current_path)
            .collect();
        if let Some(root) = &request.root {
            for current in references(&current_path, root) {
                if current.kind == reference.kind
                    && eq_ignore_case(&current.name, &reference.name)
                    && eq_ignore_case(&current.package, &reference.package)
                {
                    filtered.push(Usage {
                        range: reference_range(&current),
                        name: current.name,
                        package: current.package,
                        kind: current.kind,
                        file: current_path.clone(),
                    });
                }
            }
        }
        let mut files = FileLines::default();
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut add = |result: &mut Vec<protocol::Location>, location: protocol::Location| {
            if seen.insert(location.clone()) {
                result.push(location);
            }
        };
        if request.params.context.include_declaration {
            if reference.kind == ReferenceKind::AssetPackage {
                for location in
                    package_locations(&mut files, &index.find_packages(&reference.name)?)
                {
                    add(&mut result, location);
                }
            }
            for resource in reference_resources(index, &reference)? {
                let Some(location) = declaration_location(&mut files, &resource) else {
                    continue;
                };
                add(&mut result, location);
                if reference.kind == ReferenceKind::ViteEntry
                    && !resource.file.is_empty()
                    && !resource.range.is_empty()
                    && let Some(config) = files.location(&resource.file, resource.range)
                {
                    add(&mut result, config);
                }
            }
        }
        for usage in &filtered {
            let location = if usage.file == current_path {
                let Some(line_index) = request.line_index() else {
                    continue;
                };
                document_location(request, line_index, usage.range)
            } else {
                let Some(location) = files.location(&usage.file, usage.range) else {
                    continue;
                };
                location
            };
            add(&mut result, location);
        }
        Ok(result)
    }
}

fn reference_resources(index: &Index, reference: &Reference) -> ProviderResult<Vec<Resource>> {
    Ok(match reference.kind {
        ReferenceKind::EncoreEntry => index.find(&reference.name, Kind::EncoreEntry)?,
        ReferenceKind::Importmap => index.find_importmap_entrypoint(&reference.name)?,
        ReferenceKind::ViteEntry => index.find(&reference.name, Kind::ViteEntry)?,
        ReferenceKind::AsseticNamed => index.find_assetic_named(&reference.name),
        ReferenceKind::AssetPackage => Vec::new(),
        ReferenceKind::Asset if reference.assetic => {
            index.find_assetic_assets(&reference.name, &reference.package)?
        }
        ReferenceKind::Asset => {
            index.find_assets_for_package(&reference.name, &reference.package)?
        }
    })
}

fn package_locations(files: &mut FileLines, packages: &[Package]) -> Vec<protocol::Location> {
    let mut result = Vec::new();
    for current in packages {
        if current.file.is_empty() {
            continue;
        }
        match files.location(&current.file, current.range) {
            Some(location) => result.push(location),
            None if current.range.is_empty() => result.push(protocol::Location {
                uri: swlsp_uriutil::file_uri(&current.file),
                range: protocol::Range::default(),
            }),
            None => {}
        }
    }
    result
}

fn declaration_location(files: &mut FileLines, resource: &Resource) -> Option<protocol::Location> {
    if !resource.target.is_empty()
        && std::fs::metadata(&resource.target).is_ok_and(|info| !info.is_dir())
    {
        return Some(protocol::Location {
            uri: swlsp_uriutil::file_uri(&resource.target),
            range: protocol::Range::default(),
        });
    }
    if resource.file.is_empty() {
        return None;
    }
    files.location(&resource.file, resource.range)
}
