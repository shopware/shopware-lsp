//! The preview image a theme's `theme.json` names for the Store.

use std::sync::Arc;

use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_uriutil::filepath;

use super::disk_stamp::file_stamp;
use super::json_keys::nearest_key_range;
use super::store_composer::{StoreComplianceResolver, store_compliance_enabled};
use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const CODE: &str = "shopware.store.theme.preview-media";

#[derive(Clone)]
pub struct StoreThemeAnalyzer {
    compliance: Option<Arc<dyn StoreComplianceResolver>>,
}

impl StoreThemeAnalyzer {
    pub fn new(compliance: Option<Arc<dyn StoreComplianceResolver>>) -> Self {
        Self { compliance }
    }

    /// The `Resources` directory of a `Resources/theme.json` whose extension
    /// opted into the Store checks: the extension root is that directory's
    /// parent (an app) or the one above it (a plugin with `src/`).
    fn checked_resources(&self, uri: &str) -> Option<String> {
        if filepath::base(uri) != "theme.json" {
            return None;
        }
        let resources = filepath::dir(&swlsp_uriutil::path(uri).ok()?);
        if filepath::base(&resources) != "Resources" {
            return None;
        }
        let parent = filepath::dir(&resources);
        [parent.clone(), filepath::dir(&parent)]
            .iter()
            .any(|root| store_compliance_enabled(self.compliance.as_ref(), root))
            .then_some(resources)
    }
}

impl DeclaredAnalyzer for StoreThemeAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.store_theme",
        description: "Store requirement for a theme's theme.json: a preview image that exists. Opt-in per extension.",
        domain: "shopware.store",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Json],
        source: "shopware-lsp",
        codes: &[CODE],
    };
}

impl ProblemAnalyzer for StoreThemeAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        // Only `theme.json` is checked: against the extension configuration
        // and the preview image on disk.
        let compliance = match &self.compliance {
            Some(compliance) if filepath::base(&document.uri) == "theme.json" => compliance,
            _ => return Some(0),
        };
        let revision = compliance.revision()?;
        let (Ok(path), Ok(Value::Object(theme))) = (
            swlsp_uriutil::path(&document.uri),
            serde_json::from_str::<Value>(document.source()),
        ) else {
            return Some(revision);
        };
        let image = filepath::join(&[filepath::dir(&path).as_str(), preview_media(&theme)]);
        Some(revision.wrapping_add(file_stamp(&image)))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(resources) = self.checked_resources(&document.uri) else {
            return Ok(Vec::new());
        };
        let Ok(Value::Object(theme)) = serde_json::from_str::<Value>(document.source()) else {
            return Ok(Vec::new());
        };
        let root = swlsp_parser_json::parse(document.source()).root();
        let problem =
            |message: String| warning(CODE, nearest_key_range(&root, &["previewMedia"]), message);
        let preview = preview_media(&theme);
        if preview.is_empty() {
            return Ok(vec![problem(
                "Store: theme.json requires previewMedia".to_owned(),
            )]);
        }
        let missing = matches!(
            std::fs::metadata(filepath::join(&[&resources, preview])),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound
        );
        Ok(missing
            .then(|| {
                problem(format!(
                    "Store: the theme preview image {preview} does not exist"
                ))
            })
            .into_iter()
            .collect())
    }
}

/// The `previewMedia` path, relative to the `Resources` directory; empty
/// when the theme names none.
fn preview_media(theme: &serde_json::Map<String, Value>) -> &str {
    theme
        .get("previewMedia")
        .and_then(Value::as_str)
        .map(str::trim)
        .unwrap_or_default()
}
