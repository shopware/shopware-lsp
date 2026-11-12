//! Files an app must not ship: PHP files, and Twig files outside
//! `Resources/views` and `Resources/scripts`.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::store_composer::{StoreComplianceResolver, store_compliance_enabled};
use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const PHP_CODE: &str = "shopware.store.app.php-file";
const TWIG_CODE: &str = "shopware.store.app.twig-location";

/// Where Twig files of an app may live, below its root.
const TWIG_DIRECTORIES: &[&str] = &["Resources/views/", "Resources/scripts/"];

/// Finds the app a file belongs to.
pub trait AppLocator: Send + Sync {
    /// The root directory of the app containing `path`.
    fn app_root(&self, path: &str) -> Option<String>;
    /// Advances whenever an answer may change; `None` when unknown.
    fn revision(&self) -> Option<u64> {
        None
    }
}

/// The production locator: the indexed extensions.
impl AppLocator for swlsp_extension::ExtensionIndexer {
    fn app_root(&self, path: &str) -> Option<String> {
        let app = self.find_app_for_file(path).ok()??;
        Some(app.root_path())
    }

    fn revision(&self) -> Option<u64> {
        Some(swlsp_indexer::Revisioned::revision(self))
    }
}

#[derive(Clone)]
pub struct AppLayoutAnalyzer {
    apps: Arc<dyn AppLocator>,
    compliance: Option<Arc<dyn StoreComplianceResolver>>,
}

impl AppLayoutAnalyzer {
    pub fn new(
        apps: Arc<dyn AppLocator>,
        compliance: Option<Arc<dyn StoreComplianceResolver>>,
    ) -> Self {
        Self { apps, compliance }
    }
}

impl DeclaredAnalyzer for AppLayoutAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.store_app_layout",
        description: "Files the Store rejects in an app: PHP files, and Twig files outside Resources/views and Resources/scripts. Opt-in per extension.",
        domain: "shopware.store",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Twig],
        source: "shopware-lsp",
        codes: &[PHP_CODE, TWIG_CODE],
    };
}

impl ProblemAnalyzer for AppLayoutAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        // The path is checked against the indexed apps and the extension
        // configuration.
        let compliance = match &self.compliance {
            Some(compliance) => compliance.revision()?,
            None => return Some(0),
        };
        Some(self.apps.revision()?.wrapping_add(compliance))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let extension = filepath::ext(&document.uri).to_ascii_lowercase();
        if (extension != ".php" && extension != ".twig") || self.compliance.is_none() {
            return Ok(Vec::new());
        }
        let path = filepath::clean(&swlsp_uriutil::path(&document.uri)?);
        let Some(app_root) = self.apps.app_root(&path) else {
            return Ok(Vec::new());
        };
        if !store_compliance_enabled(self.compliance.as_ref(), &app_root) {
            return Ok(Vec::new());
        }
        let relative = filepath::to_slash(&filepath::rel(&app_root, &path)?);
        let range = first_line(document.source());
        if extension == ".php" {
            return Ok(vec![warning(
                PHP_CODE,
                range,
                "Store: an app must not contain PHP files".to_owned(),
            )]);
        }
        if TWIG_DIRECTORIES
            .iter()
            .any(|directory| relative.starts_with(directory))
        {
            return Ok(Vec::new());
        }
        Ok(vec![warning(
            TWIG_CODE,
            range,
            "Store: Twig files of an app belong in Resources/views or Resources/scripts".to_owned(),
        )])
    }
}

/// The first line of `source`, without its line break.
fn first_line(source: &str) -> TextRange {
    let line = source.lines().next().unwrap_or_default();
    TextRange::new(TextSize::from(0), TextSize::of(line))
}
