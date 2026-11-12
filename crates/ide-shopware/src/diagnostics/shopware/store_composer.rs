//! Shopware Store requirements for an extension `composer.json`.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::filepath;

use super::disk_stamp::file_stamp;
use super::json_keys::nearest_key_range;
use super::store_metadata::{icon_path, metadata_problems};
use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

/// Supplies the extension's explicit Store validation setting.
pub trait StoreComplianceResolver: Send + Sync {
    fn store_compliance_enabled(&self, dir: &str) -> Result<bool, BoxError>;
    /// Advances whenever an answer may change; `None` when unknown.
    fn revision(&self) -> Option<u64> {
        None
    }
}

/// The production resolver: the indexed extension configuration.
impl StoreComplianceResolver for swlsp_extension::ConfigIndex {
    fn revision(&self) -> Option<u64> {
        Some(swlsp_indexer::Revisioned::revision(self))
    }

    fn store_compliance_enabled(&self, dir: &str) -> Result<bool, BoxError> {
        Ok(swlsp_extension::ConfigIndex::store_compliance_enabled(
            self, dir,
        )?)
    }
}

#[derive(Clone)]
pub struct ShopwareStoreComposerAnalyzer {
    compliance: Option<Arc<dyn StoreComplianceResolver>>,
}

impl ShopwareStoreComposerAnalyzer {
    pub fn new(compliance: Option<Arc<dyn StoreComplianceResolver>>) -> Self {
        Self { compliance }
    }
}

/// Whether the extension rooted at `dir` opted into the Store checks.
pub(crate) fn store_compliance_enabled(
    compliance: Option<&Arc<dyn StoreComplianceResolver>>,
    dir: &str,
) -> bool {
    compliance.is_some_and(|compliance| compliance.store_compliance_enabled(dir).unwrap_or(false))
}

impl DeclaredAnalyzer for ShopwareStoreComposerAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.store_composer",
        description: "Store requirements for an extension's composer.json: name, description, license, version, authors, plugin class, autoloading, localized label and description, manufacturer and support links, a shopware/core requirement and the extension icon. Opt-in per extension.",
        domain: "shopware.store",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Json],
        source: "shopware-lsp",
        codes: &[
            "shopware.store.authors",
            "shopware.store.autoload",
            "shopware.store.composer-description",
            "shopware.store.description",
            "shopware.store.icon",
            "shopware.store.icon-size",
            "shopware.store.label",
            "shopware.store.license",
            "shopware.store.manufacturer-link",
            "shopware.store.name",
            "shopware.store.plugin-class",
            "shopware.store.require-core",
            "shopware.store.support-link",
            "shopware.store.version",
        ],
    };
}

impl ProblemAnalyzer for ShopwareStoreComposerAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only a plugin's `composer.json` is checked: against the extension
        // configuration and its open overlays, and the icon file on disk.
        if swlsp_uriutil::filepath::base(&document.uri) != "composer.json"
            || !document.source().contains("shopware-platform-plugin")
        {
            return Some(0);
        }
        let Some(compliance) = &self.compliance else {
            return Some(0);
        };
        let revision = compliance.revision()?;
        let (Ok(path), Ok(Value::Object(composer))) = (
            swlsp_uriutil::path(&document.uri),
            serde_json::from_str::<Value>(document.source()),
        ) else {
            return Some(revision);
        };
        let icon = filepath::join(&[filepath::dir(&path).as_str(), icon_path(&composer)]);
        Some(revision.wrapping_add(file_stamp(&icon)))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if swlsp_uriutil::filepath::base(&document.uri) != "composer.json"
            || !document.source().contains("shopware-platform-plugin")
        {
            return Ok(Vec::new());
        }
        let Ok(Value::Object(composer)) = serde_json::from_str::<Value>(document.source()) else {
            return Ok(Vec::new());
        };
        let extension_root = filepath::dir(&swlsp_uriutil::path(&document.uri)?);
        if composer.get("type").and_then(Value::as_str) != Some("shopware-platform-plugin")
            || !store_compliance_enabled(self.compliance.as_ref(), &extension_root)
        {
            return Ok(Vec::new());
        }
        let source = document.source();
        let root = swlsp_parser_json::parse(source).root();
        let problem =
            |id: &str, key: &str, message: &str| store_problem(&root, id, &[key], message);
        let mut result = Vec::new();
        match composer.get("extra") {
            Some(Value::Object(extra)) => {
                result.extend(validate_localized(
                    &root,
                    extra,
                    "label",
                    "shopware.store.label",
                    false,
                ));
                result.extend(validate_localized(
                    &root,
                    extra,
                    "description",
                    "shopware.store.description",
                    true,
                ));
                result.extend(validate_localized(
                    &root,
                    extra,
                    "manufacturerLink",
                    "shopware.store.manufacturer-link",
                    false,
                ));
                result.extend(validate_localized(
                    &root,
                    extra,
                    "supportLink",
                    "shopware.store.support-link",
                    false,
                ));
            }
            _ => {
                result.push(problem(
                    "shopware.store.label",
                    "extra",
                    "Store: localized extra.label is required",
                ));
                result.push(problem(
                    "shopware.store.description",
                    "extra",
                    "Store: localized extra.description is required",
                ));
                result.push(problem(
                    "shopware.store.manufacturer-link",
                    "extra",
                    "Store: localized extra.manufacturerLink is required",
                ));
                result.push(problem(
                    "shopware.store.support-link",
                    "extra",
                    "Store: localized extra.supportLink is required",
                ));
            }
        }
        let core_missing = match composer.get("require") {
            Some(Value::Object(requirements)) => match requirements.get("shopware/core") {
                None | Some(Value::Null) => true,
                // Only a blank string counts as a missing constraint.
                Some(Value::String(value)) => value.trim().is_empty(),
                Some(_) => false,
            },
            _ => true,
        };
        if core_missing {
            result.push(problem(
                "shopware.store.require-core",
                "require",
                "Store: composer require must contain shopware/core",
            ));
        }
        result.extend(metadata_problems(&root, &composer, &extension_root));
        Ok(result)
    }
}

fn validate_localized(
    root: &SyntaxNode,
    extra: &Map<String, Value>,
    key: &str,
    id: &str,
    check_length: bool,
) -> Vec<Problem> {
    let Some(Value::Object(value)) = extra.get(key) else {
        return vec![store_problem(
            root,
            id,
            &["extra", key],
            &format!("Store: extra.{key} must be an object containing de-DE and en-GB"),
        )];
    };
    let mut result = Vec::new();
    for locale in ["de-DE", "en-GB"] {
        let text = value
            .get(locale)
            .and_then(Value::as_str)
            .unwrap_or_default();
        if text.is_empty() {
            result.push(store_problem(
                root,
                id,
                &["extra", key],
                &format!("Store: extra.{key} requires locale {locale}"),
            ));
            continue;
        }
        let length = text.chars().count();
        if check_length && !(150..=185).contains(&length) {
            result.push(store_problem(
                root,
                id,
                &["extra", key, locale],
                &format!(
                    "Store: {locale} description must contain 150 to 185 characters; got {length}"
                ),
            ));
        }
    }
    result
}

/// A problem on the key at `path`, on the deepest key of it that exists, or
/// on the whole document.
fn store_problem(root: &SyntaxNode, id: &str, path: &[&str], message: &str) -> Problem {
    warning(id, nearest_key_range(root, path), message.to_owned())
}
