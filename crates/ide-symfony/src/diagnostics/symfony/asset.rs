//! Missing Symfony assets.

use std::sync::Arc;

use swlsp_asset::{
    HtmlAssetType, Index, Kind, Reference, ReferenceKind, Resource, reference_range, references,
    validate_php_reference,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_php::{PhpContext, PhpIndex};

use super::{lower_ext, symfony_warning};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_ASSET_CODE: &str = "symfony.asset.missing";
pub const MISSING_ASSET_PACKAGE_CODE: &str = "symfony.asset.package.missing";
pub const MISSING_ENCORE_ENTRY_CODE: &str = "symfony.encore.entry.missing";
pub const MISSING_IMPORTMAP_CODE: &str = "symfony.asset_mapper.entrypoint.missing";
pub const MISSING_VITE_ENTRY_CODE: &str = "symfony.vite.entry.missing";

pub struct AssetAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
}

impl AssetAnalyzer {
    pub fn new(index: Option<Arc<Index>>, php_index: Option<PhpIndex>) -> Self {
        Self { index, php_index }
    }
}

impl DeclaredAnalyzer for AssetAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.asset",
        description: "Asset paths, packages, Encore and Vite entries, and AssetMapper entrypoints that do not exist.",
        domain: "symfony.assets",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Twig],
        source: "symfony",
        codes: &[
            MISSING_ASSET_CODE,
            MISSING_ASSET_PACKAGE_CODE,
            MISSING_IMPORTMAP_CODE,
            MISSING_ENCORE_ENTRY_CODE,
            MISSING_VITE_ENTRY_CODE,
        ],
    };
}

impl ProblemAnalyzer for AssetAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.index) + crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.index, document.root()) else {
            return Ok(Vec::new());
        };
        let extension = lower_ext(&document.uri);
        if extension != ".php" && extension != ".twig" {
            return Ok(Vec::new());
        }
        let names = index.name_catalog()?;
        let catalog = Catalog {
            assets: names.assets.clone(),
            encore: names.encore_entries.clone(),
            packages: index.package_names()?,
            importmaps: names.importmap_entries.clone(),
            vite: names.vite_entries.clone(),
            assetic: index.assetic_named_names(),
        };
        if catalog.empty() {
            return Ok(Vec::new());
        }
        let validation_context = match (&self.php_index, extension.as_str()) {
            (Some(php_index), ".php") => {
                swlsp_lsp_phpanalysis::context_for_document(php_index, document)?
            }
            _ => None,
        };
        let run = Run {
            index,
            php_index: self.php_index.as_ref(),
            extension,
            validation_context,
            catalog,
        };
        let mut result = Vec::new();
        for reference in references(&document.uri, &root) {
            if !run.valid_reference(&reference) {
                continue;
            }
            let resolution = run.resolve(&reference)?;
            if resolution.skip
                || resolution.found
                || non_actionable_html_asset(&reference, &resolution.suggestions)
            {
                continue;
            }
            result.push(symfony_warning(
                resolution.code,
                reference_range(&reference),
                resolution.message,
                resolution.suggestions,
            ));
        }
        Ok(result)
    }
}

struct Catalog {
    assets: Vec<String>,
    encore: Vec<String>,
    packages: Vec<String>,
    importmaps: Vec<String>,
    vite: Vec<String>,
    assetic: Vec<String>,
}

impl Catalog {
    fn empty(&self) -> bool {
        self.assets.is_empty()
            && self.encore.is_empty()
            && self.packages.is_empty()
            && self.importmaps.is_empty()
            && self.vite.is_empty()
            && self.assetic.is_empty()
    }
}

struct Run<'a> {
    index: &'a Index,
    php_index: Option<&'a PhpIndex>,
    extension: String,
    validation_context: Option<PhpContext>,
    catalog: Catalog,
}

struct Resolution {
    found: bool,
    skip: bool,
    suggestions: Vec<String>,
    code: &'static str,
    message: String,
}

impl Resolution {
    fn new(found: bool, suggestions: Vec<String>, code: &'static str, message: String) -> Self {
        Self {
            found,
            skip: false,
            suggestions,
            code,
            message,
        }
    }
}

impl Run<'_> {
    fn valid_reference(&self, reference: &Reference) -> bool {
        if reference.name.is_empty() {
            return false;
        }
        if self.extension != ".php" {
            return true;
        }
        let Some(php_index) = self.php_index else {
            return false;
        };
        validate_php_reference(self.validation_context.as_ref(), reference, php_index)
    }

    fn resolve(&self, reference: &Reference) -> Result<Resolution, BoxError> {
        let name = &reference.name;
        Ok(match reference.kind {
            ReferenceKind::EncoreEntry => Resolution::new(
                !self.index.find(name, Kind::EncoreEntry)?.is_empty(),
                swlsp_suggestion::similar(name, &self.catalog.encore),
                MISSING_ENCORE_ENTRY_CODE,
                format!("Webpack Encore entry '{name}' not found"),
            ),
            ReferenceKind::Importmap => Resolution::new(
                !self.index.find_importmap_entrypoint(name)?.is_empty(),
                swlsp_suggestion::similar(name, &self.catalog.importmaps),
                MISSING_IMPORTMAP_CODE,
                format!("AssetMapper entrypoint '{name}' not found"),
            ),
            ReferenceKind::ViteEntry => Resolution::new(
                !self.index.find(name, Kind::ViteEntry)?.is_empty(),
                swlsp_suggestion::similar(name, &self.catalog.vite),
                MISSING_VITE_ENTRY_CODE,
                format!("Vite entry '{name}' not found"),
            ),
            ReferenceKind::AsseticNamed => Resolution::new(
                !self.index.find_assetic_named(name).is_empty(),
                swlsp_suggestion::similar(name, &self.catalog.assetic)
                    .into_iter()
                    .map(|value| format!("@{value}"))
                    .collect(),
                MISSING_ASSET_CODE,
                format!("Assetic named asset '@{name}' not found"),
            ),
            ReferenceKind::AssetPackage => Resolution::new(
                !self.index.find_packages(name)?.is_empty(),
                swlsp_suggestion::similar(name, &self.catalog.packages),
                MISSING_ASSET_PACKAGE_CODE,
                format!("Symfony asset package '{name}' not found"),
            ),
            ReferenceKind::Asset => self.resolve_asset(reference)?,
        })
    }

    fn resolve_asset(&self, reference: &Reference) -> Result<Resolution, BoxError> {
        let mut resolution = Resolution::new(
            false,
            Vec::new(),
            MISSING_ASSET_CODE,
            format!("Symfony asset '{}' not found", reference.name),
        );
        if !reference.package.is_empty()
            && !reference.assetic
            && self.index.find_packages(&reference.package)?.is_empty()
        {
            resolution.skip = true;
            return Ok(resolution);
        }
        let (resources, names) = self.asset_resources_and_names(reference)?;
        resolution.found = !resources.is_empty();
        resolution.suggestions = swlsp_suggestion::similar(&reference.name, &names);
        if reference.assetic && !reference.package.is_empty() {
            let prefix = format!("{}/Resources/public/", reference.package);
            for suggestion in &mut resolution.suggestions {
                suggestion.insert_str(0, &prefix);
            }
        }
        Ok(resolution)
    }

    fn asset_resources_and_names(
        &self,
        reference: &Reference,
    ) -> Result<(Vec<Resource>, Vec<String>), BoxError> {
        if reference.assetic {
            let resources = self
                .index
                .find_assetic_assets(&reference.name, &reference.package)?;
            let names = self.index.assetic_names(&reference.package)?;
            return Ok((resources, names));
        }
        let resources = self
            .index
            .find_assets_for_package(&reference.name, &reference.package)?;
        let names = self.index.names_for_package(&reference.package)?;
        Ok((resources, names))
    }
}

/// Raw HTML paths can be generated by runtime endpoints (for example a
/// development proxy) even when they look like static files. Report only
/// actionable HTML typos with a nearby indexed asset.
fn non_actionable_html_asset(reference: &Reference, suggestions: &[String]) -> bool {
    reference.html_type != HtmlAssetType::None && !reference.assetic && suggestions.is_empty()
}
