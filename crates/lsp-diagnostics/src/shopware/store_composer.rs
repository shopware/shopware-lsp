//! Port of Go `shopware_store_composer.go`.

use std::sync::{Arc, LazyLock};

use regex::Regex;
use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_syntax::{TextRange, TextSize};

use super::util::{go_dir, warning};
use crate::ProblemAnalyzer;

/// Supplies the extension's explicit Store validation setting (Go
/// `StoreComplianceResolver`).
pub trait StoreComplianceResolver: Send + Sync {
    fn store_compliance_enabled(&self, dir: &str) -> Result<bool, BoxError>;
}

/// The production resolver: the indexed extension configuration.
impl StoreComplianceResolver for swlsp_extension::ConfigIndex {
    fn store_compliance_enabled(&self, dir: &str) -> Result<bool, BoxError> {
        Ok(swlsp_extension::ConfigIndex::store_compliance_enabled(
            self, dir,
        )?)
    }
}

/// Go `ShopwareStoreComposerAnalyzer`.
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
            .store_compliance_enabled(&go_dir(&path))
            .unwrap_or(false)
    }
}

impl ProblemAnalyzer for ShopwareStoreComposerAnalyzer {
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
        let problem = |id: &str, key: &str, message: &str| store_problem(source, id, key, message);
        let mut result = Vec::new();
        match composer.get("extra") {
            Some(Value::Object(extra)) => {
                result.extend(validate_localized(
                    source,
                    extra,
                    "label",
                    "shopware.store.label",
                    false,
                ));
                result.extend(validate_localized(
                    source,
                    extra,
                    "description",
                    "shopware.store.description",
                    true,
                ));
                result.extend(validate_localized(
                    source,
                    extra,
                    "manufacturerLink",
                    "shopware.store.manufacturer-link",
                    false,
                ));
                result.extend(validate_localized(
                    source,
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
                // Go `strings.TrimSpace(fmt.Sprint(value)) == ""` only holds
                // for blank strings.
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
    source: &str,
    extra: &Map<String, Value>,
    key: &str,
    id: &str,
    check_length: bool,
) -> Vec<Problem> {
    let Some(Value::Object(value)) = extra.get(key) else {
        return vec![store_problem(
            source,
            id,
            key,
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
                source,
                id,
                key,
                &format!("Store: extra.{key} requires locale {locale}"),
            ));
            continue;
        }
        let length = text.chars().count();
        if check_length && !(150..=185).contains(&length) {
            result.push(store_problem(
                source,
                id,
                locale,
                &format!(
                    "Store: {locale} description must contain 150 to 185 characters; got {length}"
                ),
            ));
        }
    }
    result
}

fn store_problem(source: &str, id: &str, key: &str, message: &str) -> Problem {
    let mut range = json_key_range(source, key);
    if range.is_empty() {
        range = TextRange::new(TextSize::from(0), TextSize::of(source));
    }
    warning(id, range, message.to_owned())
}

/// Go `jsonKeyRange`: the key text of the first `"key"\s*:` occurrence.
fn json_key_range(source: &str, key: &str) -> TextRange {
    static CACHE: LazyLock<std::sync::Mutex<std::collections::HashMap<String, Regex>>> =
        LazyLock::new(Default::default);
    let pattern = {
        let mut cache = CACHE.lock().unwrap_or_else(|poison| poison.into_inner());
        cache
            .entry(key.to_owned())
            .or_insert_with(|| {
                Regex::new(&format!("\"{}\"[\\t\\n\\x0C\\r ]*:", regex::escape(key)))
                    .expect("json key pattern")
            })
            .clone()
    };
    match pattern.find(source) {
        Some(found) => {
            let start = TextSize::from(found.start() as u32 + 1);
            TextRange::new(start, start + TextSize::of(key))
        }
        None => TextRange::default(),
    }
}
