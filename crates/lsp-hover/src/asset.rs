//! Symfony asset, Encore, AssetMapper, Vite, and Assetic hover.

use std::collections::HashSet;
use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_asset::{HtmlAssetType, Index, Kind, Package, Reference, ReferenceKind, Resource};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_uriutil::filepath;

use crate::{
    escape_markdown, hover_protocol_range, lower_ext, markdown_hover, php_context, rel_or,
    request_uri,
};

pub struct AssetHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl AssetHoverProvider {
    pub fn new(root: impl Into<String>, index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self {
            root: root.into(),
            index,
            php,
        }
    }

    fn package_hover(
        &self,
        reference: &Reference,
        packages: &[Package],
        request: &HoverRequest,
    ) -> Option<protocol::Hover> {
        let mut markdown = format!(
            "**Symfony asset package** `{}`",
            escape_markdown(&reference.name)
        );
        let mut seen = HashSet::new();
        for current in packages {
            let mut description = if current.inferred {
                "bundle package".to_owned()
            } else {
                "configured package".to_owned()
            };
            if !current.base_path.is_empty() {
                let _ = write!(description, " · `{}`", escape_markdown(&current.base_path));
            }
            let target = match filepath::rel(&self.root, &current.file) {
                Ok(relative) => filepath::to_slash(&relative),
                Err(_) => current.file.clone(),
            };
            let mut line = format!("\n\n- {description}");
            if !target.is_empty() {
                let _ = write!(line, " · `{}`", escape_markdown(&target));
            }
            if !seen.insert(line.clone()) {
                continue;
            }
            markdown.push_str(&line);
        }
        let line_index = request.line_index()?;
        Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                swlsp_asset::reference_range(reference),
                line_index,
            )),
        ))
    }
}

impl HoverProvider for AssetHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let uri = request_uri(request);
        let extension = lower_ext(uri);
        if extension != ".php" && extension != ".twig" {
            return Ok(None);
        }
        let Some(reference) = swlsp_asset::reference_at(uri, node) else {
            return Ok(None);
        };
        if extension == ".php" {
            let Some(php) = self.php.as_ref() else {
                return Ok(None);
            };
            let context = php_context(Some(php), request);
            if !swlsp_asset::validate_php_reference(context.as_ref(), &reference, php) {
                return Ok(None);
            }
        }
        let mut title = "Symfony asset";
        let resources: Vec<Resource> = match reference.kind {
            ReferenceKind::EncoreEntry => {
                title = "Webpack Encore entry";
                index.find(&reference.name, Kind::EncoreEntry)?
            }
            ReferenceKind::Importmap => {
                title = "Symfony AssetMapper entrypoint";
                index.find_importmap_entrypoint(&reference.name)?
            }
            ReferenceKind::ViteEntry => {
                title = "Symfony Vite entry";
                index.find(&reference.name, Kind::ViteEntry)?
            }
            ReferenceKind::AsseticNamed => {
                title = "Assetic named asset";
                index.find_assetic_named(&reference.name)
            }
            ReferenceKind::AssetPackage => {
                let packages = index.find_packages(&reference.name)?;
                if packages.is_empty() {
                    return Ok(None);
                }
                return Ok(self.package_hover(&reference, &packages, request));
            }
            ReferenceKind::Asset => {
                let resources = if reference.assetic {
                    title = "Assetic file";
                    index.find_assetic_assets(&reference.name, &reference.package)?
                } else {
                    index.find_assets_for_package(&reference.name, &reference.package)?
                };
                if reference.html_type != HtmlAssetType::None && !reference.assetic {
                    title = "Twig HTML asset";
                }
                resources
            }
        };
        if resources.is_empty() {
            return Ok(None);
        }
        let mut markdown = format!("**{title}** `{}`", escape_markdown(&reference.name));
        for resource in &resources {
            let target = if resource.target.is_empty() {
                &resource.file
            } else {
                &resource.target
            };
            let display = rel_or(&self.root, target);
            let _ = write!(
                markdown,
                "\n\n- {} · `{}`",
                resource.kind,
                escape_markdown(&filepath::to_slash(&display))
            );
            if !resource.version.is_empty() {
                let _ = write!(
                    markdown,
                    " · version `{}`",
                    escape_markdown(&resource.version)
                );
            }
            if !resource.url.is_empty() {
                let _ = write!(markdown, " · `{}`", escape_markdown(&resource.url));
            }
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                swlsp_asset::reference_range(&reference),
                line_index,
            )),
        )))
    }
}
