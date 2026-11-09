//! Shopware Store requirements for an extension `composer.json`.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_json::syntax::{JSON_OBJECT, JSON_PAIR, JSON_STRING, TK_STRING};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::util::warning;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

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

    fn store_compliance_enabled(&self, uri: &str) -> bool {
        let (Ok(path), Some(compliance)) = (swlsp_uriutil::path(uri), &self.compliance) else {
            return false;
        };
        compliance
            .store_compliance_enabled(&filepath::dir(&path))
            .unwrap_or(false)
    }
}

impl DeclaredAnalyzer for ShopwareStoreComposerAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.store_composer",
        description: "Store requirements for an extension's composer.json: localized label and description, manufacturer and support links, and a shopware/core requirement. Opt-in per extension.",
        domain: "shopware.store",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Json],
        source: "shopware-lsp",
        codes: &[
            "shopware.store.description",
            "shopware.store.label",
            "shopware.store.manufacturer-link",
            "shopware.store.require-core",
            "shopware.store.support-link",
        ],
    };
}

impl ProblemAnalyzer for ShopwareStoreComposerAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only `composer.json` is checked, against the extension configuration
        // and its open overlays (and the document itself).
        if swlsp_uriutil::filepath::base(&document.uri) != "composer.json" {
            return Some(0);
        }
        match &self.compliance {
            Some(compliance) => compliance.revision(),
            None => Some(0),
        }
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
        if composer.get("type").and_then(Value::as_str) != Some("shopware-platform-plugin")
            || !self.store_compliance_enabled(&document.uri)
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

/// A problem on the key at `path`, or on the whole document when the key
/// does not exist.
fn store_problem(root: &SyntaxNode, id: &str, path: &[&str], message: &str) -> Problem {
    let range = json_key_range(root, path).unwrap_or_else(|| root.text_range());
    warning(id, range, message.to_owned())
}

/// The text of the key at `path` (object keys from the document root),
/// without its quotes.
fn json_key_range(root: &SyntaxNode, path: &[&str]) -> Option<TextRange> {
    let mut object = root.descendants().find(|node| node.kind() == JSON_OBJECT)?;
    let mut found = None;
    for (depth, key) in path.iter().enumerate() {
        let pair = object
            .children()
            .filter(|pair| pair.kind() == JSON_PAIR)
            .find(|pair| pair_key(pair).is_some_and(|(text, _)| text == *key))?;
        found = pair_key(&pair).map(|(_, range)| range);
        if depth + 1 < path.len() {
            object = pair
                .children()
                .skip(1)
                .find(|value| value.kind() == JSON_OBJECT)?;
        }
    }
    found
}

/// The unquoted text and range of a pair's key.
fn pair_key(pair: &SyntaxNode) -> Option<(String, TextRange)> {
    let key = pair.children().find(|child| child.kind() == JSON_STRING)?;
    let token = key
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == TK_STRING)?;
    let text = token.text();
    let inner = text.strip_prefix('"')?.strip_suffix('"')?;
    let start = token.text_range().start() + TextSize::from(1);
    Some((
        inner.to_owned(),
        TextRange::new(start, start + TextSize::of(inner)),
    ))
}
