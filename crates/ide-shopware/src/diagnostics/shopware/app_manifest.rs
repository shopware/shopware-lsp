//! Shopware Store requirements for an app `manifest.xml`.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_xml::query as xml;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_uriutil::filepath;

use super::disk_stamp::file_stamp;
use super::store_composer::{StoreComplianceResolver, store_compliance_enabled};
use super::store_icon::{IconIssue, icon_finding};
use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const DEFAULT_ICON: &str = "Resources/config/plugin.png";

/// The `<meta>` elements a Store release needs, with their codes.
const REQUIRED_META: &[(&str, &str)] = &[
    ("author", "shopware.store.app.author"),
    ("copyright", "shopware.store.app.copyright"),
    ("license", "shopware.store.app.license"),
];

#[derive(Clone)]
pub struct AppManifestAnalyzer {
    compliance: Option<Arc<dyn StoreComplianceResolver>>,
}

impl AppManifestAnalyzer {
    pub fn new(compliance: Option<Arc<dyn StoreComplianceResolver>>) -> Self {
        Self { compliance }
    }
}

impl DeclaredAnalyzer for AppManifestAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.store_app_manifest",
        description: "Store requirements for an app's manifest.xml: author, copyright and license, no development secret, and the app icon. Opt-in per extension.",
        domain: "shopware.store",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Xml],
        source: "shopware-lsp",
        codes: &[
            "shopware.store.app.author",
            "shopware.store.app.copyright",
            "shopware.store.app.icon",
            "shopware.store.app.icon-size",
            "shopware.store.app.license",
            "shopware.store.app.secret",
        ],
    };
}

impl ProblemAnalyzer for AppManifestAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        // Only `manifest.xml` is checked: against the extension configuration
        // and the icon file on disk.
        let compliance = match &self.compliance {
            Some(compliance) if filepath::base(&document.uri) == "manifest.xml" => compliance,
            _ => return Some(0),
        };
        let revision = compliance.revision()?;
        let (Ok(path), Some(root)) = (swlsp_uriutil::path(&document.uri), document.root()) else {
            return Some(revision);
        };
        let icon = xml::elements(&root, &["manifest"])
            .into_iter()
            .next()
            .and_then(|manifest| xml::child_element(&manifest, &["meta"]))
            .and_then(|meta| icon_element(&meta).1);
        let icon = filepath::join(&[
            filepath::dir(&path).as_str(),
            icon.as_deref().unwrap_or(DEFAULT_ICON),
        ]);
        Some(revision.wrapping_add(file_stamp(&icon)))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if filepath::base(&document.uri) != "manifest.xml" {
            return Ok(Vec::new());
        }
        let app_root = filepath::dir(&swlsp_uriutil::path(&document.uri)?);
        if !store_compliance_enabled(self.compliance.as_ref(), &app_root) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Some(manifest) = xml::elements(&root, &["manifest"]).into_iter().next() else {
            return Ok(Vec::new());
        };
        let Some(manifest_range) = name_range(&manifest) else {
            return Ok(Vec::new());
        };
        let meta = xml::child_element(&manifest, &["meta"]);
        // Missing elements are reported where they belong: on `<meta>`.
        let meta_range = meta.as_ref().and_then(name_range).unwrap_or(manifest_range);
        let child = |name: &str| {
            meta.as_ref()
                .and_then(|meta| xml::child_element(meta, &[name]))
        };
        let mut result = Vec::new();
        for (name, code) in REQUIRED_META {
            let present =
                child(name).is_some_and(|element| !xml::text_content(&element).trim().is_empty());
            if !present {
                result.push(warning(
                    code,
                    meta_range,
                    format!("Store: the manifest requires a <{name}> element in <meta>"),
                ));
            }
        }
        if let Some(secret) = xml::child_element(&manifest, &["setup"])
            .and_then(|setup| xml::child_element(&setup, &["secret"]))
            && let Some(range) = name_range(&secret)
        {
            result.push(warning(
                "shopware.store.app.secret",
                range,
                "Store: <secret> is for local development only; the Store generates the app secret"
                    .to_owned(),
            ));
        }
        let (icon_element, icon) = meta.as_ref().map(icon_element).unwrap_or_default();
        let shown = icon.as_deref().unwrap_or(DEFAULT_ICON);
        if let Some((issue, message)) = icon_finding(&filepath::join(&[&app_root, shown]), shown) {
            let code = match issue {
                IconIssue::Unusable => "shopware.store.app.icon",
                IconIssue::Size => "shopware.store.app.icon-size",
            };
            let range = icon_element
                .as_ref()
                .and_then(name_range)
                .unwrap_or(meta_range);
            result.push(warning(code, range, message));
        }
        result.sort_by_key(|problem| (problem.range.start(), problem.id.clone()));
        Ok(result)
    }
}

/// The `<icon>` element of `<meta>` and the path it names, relative to the
/// app root.
fn icon_element(meta: &SyntaxNode) -> (Option<SyntaxNode>, Option<String>) {
    let element = xml::child_element(meta, &["icon"]);
    let icon = element
        .as_ref()
        .map(|element| xml::text_content(element).trim().to_owned())
        .filter(|icon| !icon.is_empty());
    (element, icon)
}

fn name_range(element: &SyntaxNode) -> Option<TextRange> {
    xml::element_name_token(element).map(|token| token.text_range())
}
