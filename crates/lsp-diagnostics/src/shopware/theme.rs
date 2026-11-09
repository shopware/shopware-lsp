//! Port of Go `theme_diagnostics.go`.

use std::sync::Arc;

use serde_json::json;
use swlsp_extension::ExtensionIndexer;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, InspectionScope, InspectionSpec, Problem, TextDocument,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{SHOPWARE_ICON, TWIG_LITERAL_HASH_PAIR, TWIG_LITERAL_STRING};
use swlsp_syntax::SyntaxNodeExt;

use crate::{DeclaredAnalyzer, ProblemAnalyzer};

/// Icon lookups (Go `IconProvider`).
pub trait IconProvider: Send + Sync {
    fn icon_packs(&self) -> Vec<String>;
    fn icons(&self, pack: &str) -> Vec<String>;
    /// The icon path, or `""` when the pack has no such icon.
    fn icon(&self, pack: &str, icon: &str) -> String;
    /// A revision that advances whenever the answers above change; `None`
    /// when the provider cannot tell.
    fn revision(&self) -> Option<u64> {
        None
    }
}

impl IconProvider for swlsp_theme::IconProvider {
    fn revision(&self) -> Option<u64> {
        Some(swlsp_theme::IconProvider::revision(self))
    }

    fn icon_packs(&self) -> Vec<String> {
        swlsp_theme::IconProvider::icon_packs(self)
    }

    fn icons(&self, pack: &str) -> Vec<String> {
        swlsp_theme::IconProvider::icons(self, pack)
    }

    fn icon(&self, pack: &str, icon: &str) -> String {
        swlsp_theme::IconProvider::icon(self, pack, icon)
    }
}

/// Go `ThemeAnalyzer`: `sw_icon` names missing from their icon pack.
#[derive(Clone)]
pub struct ThemeAnalyzer {
    icon_provider: Arc<dyn IconProvider>,
}

impl ThemeAnalyzer {
    pub fn new(project_root: &str, extension_indexer: Arc<ExtensionIndexer>) -> Self {
        Self::with_icon_provider(Arc::new(swlsp_theme::IconProvider::new(
            project_root,
            extension_indexer,
        )))
    }

    pub fn with_icon_provider(icon_provider: Arc<dyn IconProvider>) -> Self {
        Self { icon_provider }
    }
}

impl DeclaredAnalyzer for ThemeAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.theme",
        description: "Storefront `sw_icon` calls for icons no icon pack provides.",
        domain: "shopware.theme",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "shopware-lsp",
        codes: &["theme.icon.missing"],
    };
}

impl ProblemAnalyzer for ThemeAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only `sw_icon` tags are checked, against the icon provider.
        if !swlsp_uriutil::filepath::ext(&document.uri).eq_ignore_ascii_case(".twig")
            || !document.source().contains("sw_icon")
        {
            Some(0)
        } else {
            self.icon_provider.revision()
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !swlsp_uriutil::filepath::ext(&document.uri).eq_ignore_ascii_case(".twig") {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut diagnostics = Vec::new();
        for tag in twigquery::iterate_nodes(&root, &[SHOPWARE_ICON]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            // The first string that is not inside a hash pair is the icon name.
            let Some(icon_name_node) =
                twigquery::iterate_nodes(&tag, &[TWIG_LITERAL_STRING]).find(|literal| {
                    twigquery::closest_node_of_kind(literal, &[TWIG_LITERAL_HASH_PAIR]).is_none()
                })
            else {
                continue;
            };
            let icon_name = twigquery::string_value(&icon_name_node).unwrap_or_default();
            let config = twigquery::hash_string_map(&tag);
            let pack = match config.get("pack") {
                Some(pack) => pack.clone(),
                // A non-string pack expression cannot be known statically.
                None if twigquery::hash_has_key(&tag, "pack") => continue,
                None => "default".to_owned(),
            };
            if self.icon_provider.icon(&pack, &icon_name).is_empty() {
                diagnostics.push(Problem {
                    id: DiagnosticId::from("theme.icon.missing"),
                    range: icon_name_node.range_trimmed_trivia(),
                    message: format!("Icon '{icon_name}' not found in pack '{pack}'"),
                    source: "shopware".to_owned(),
                    severity: Some(DiagnosticSeverity::ERROR),
                    payload: Some(json!({"iconName": icon_name, "pack": pack})),
                    ..Problem::default()
                });
            }
        }
        Ok(diagnostics)
    }
}
