use std::sync::Arc;

use swlsp_lsp::BoxError;

use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

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
    // A missing key is reported on the whole document.
    // The length problem sits on the description's own locale key.
    let fixture = r#"{|shopware.store.support-link:{
    "type": "shopware-platform-plugin",
    "require": {"shopware/core": "~6.7"},
    "extra": {
        "label": {"de-DE": "Acme", "en-GB": "Acme"},
        "description": {"de-DE": "DESCRIPTION", "{|shopware.store.description:en-GB|}": "short"},
        "manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}
    }
}|}"#
        .replace("DESCRIPTION", &"a".repeat(150));
    check_problems(
        &analyzer(Some(true)),
        "file:///project/custom/plugins/Acme/composer.json",
        &fixture,
    );
}

#[test]
fn shopware_store_composer_analyzer_requires_explicit_opt_in() {
    let source = r#"{"type":"shopware-platform-plugin"}"#;
    for enabled in [None, Some(false)] {
        check_problems(&analyzer(enabled), "file:///project/composer.json", source);
    }
    check_problems(
        &analyzer(Some(true)),
        "file:///project/composer.json",
        &format!(
            "{{|shopware.store.description:{{|shopware.store.label:\
             {{|shopware.store.manufacturer-link:{{|shopware.store.require-core:\
             {{|shopware.store.support-link:{source}|}}|}}|}}|}}|}}"
        ),
    );
}
