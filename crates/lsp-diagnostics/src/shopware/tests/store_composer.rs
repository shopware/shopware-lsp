use std::sync::Arc;

use swlsp_lsp::BoxError;

use super::analyze;
use crate::shopware::*;
use crate::test_support::document;

struct StoreComplianceSetting(bool);

impl StoreComplianceResolver for StoreComplianceSetting {
    fn store_compliance_enabled(&self, _dir: &str) -> Result<bool, BoxError> {
        Ok(self.0)
    }
}

fn analyzer(enabled: Option<bool>) -> ShopwareStoreComposerAnalyzer {
    ShopwareStoreComposerAnalyzer::new(enabled.map(|enabled| {
        Arc::new(StoreComplianceSetting(enabled)) as Arc<dyn StoreComplianceResolver>
    }))
}

#[test]
fn shopware_store_composer_analyzer() {
    let description = "a".repeat(150);
    let document = document(
        "file:///project/custom/plugins/Acme/composer.json",
        &format!(
            r#"{{
    "type": "shopware-platform-plugin",
    "require": {{"shopware/core": "~6.7"}},
    "extra": {{
        "label": {{"de-DE": "Acme", "en-GB": "Acme"}},
        "description": {{"de-DE": "{description}", "en-GB": "short"}},
        "manufacturerLink": {{"de-DE": "https://example.com", "en-GB": "https://example.com"}}
    }}
}}"#
        ),
    );
    let problems = analyze(&analyzer(Some(true)), &document);
    assert_eq!(problems.len(), 2);
    assert_eq!(problems[0].id.as_str(), "shopware.store.description");
    assert_eq!(problems[1].id.as_str(), "shopware.store.support-link");
}

#[test]
fn shopware_store_composer_analyzer_requires_explicit_opt_in() {
    let document = document(
        "file:///project/composer.json",
        r#"{"type":"shopware-platform-plugin"}"#,
    );
    for enabled in [None, Some(false)] {
        assert!(analyze(&analyzer(enabled), &document).is_empty());
    }
    assert!(!analyze(&analyzer(Some(true)), &document).is_empty());
}
