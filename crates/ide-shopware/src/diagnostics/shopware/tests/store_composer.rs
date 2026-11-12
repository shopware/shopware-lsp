use std::path::Path;
use std::sync::Arc;

use swlsp_lsp::BoxError;
use swlsp_uriutil::file_uri;

use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

pub(super) struct StoreComplianceSetting(pub(super) bool);

impl StoreComplianceResolver for StoreComplianceSetting {
    fn store_compliance_enabled(&self, _dir: &str) -> Result<bool, BoxError> {
        Ok(self.0)
    }

    fn revision(&self) -> Option<u64> {
        Some(1)
    }
}

pub(super) fn compliance(enabled: Option<bool>) -> Option<Arc<dyn StoreComplianceResolver>> {
    enabled.map(|enabled| {
        Arc::new(StoreComplianceSetting(enabled)) as Arc<dyn StoreComplianceResolver>
    })
}

/// A PNG file header of the given dimensions, padded to `bytes`.
pub(super) fn write_png(path: &Path, width: u32, height: u32, bytes: usize) {
    let mut data = b"\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR".to_vec();
    data.extend(width.to_be_bytes());
    data.extend(height.to_be_bytes());
    data.resize(bytes.max(data.len()), 0);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, data).unwrap();
}

fn analyzer(enabled: Option<bool>) -> ShopwareStoreComposerAnalyzer {
    ShopwareStoreComposerAnalyzer::new(compliance(enabled))
}

/// A composer.json that satisfies every rule, with `EXTRA` as a hook for
/// additional `extra` keys.
const COMPLETE: &str = r#"{
    "name": "acme/plugin",
    "description": "Acme",
    "type": "shopware-platform-plugin",
    "version": "1.0.0",
    "license": "MIT",
    "authors": [{"name": "Acme"}],
    "autoload": {"psr-4": {"Acme\\": "src/"}},
    "require": {"shopware/core": "~6.7"},
    "extra": {
        "shopware-plugin-class": "Acme\\Acme",EXTRA
        "label": {"de-DE": "Acme", "en-GB": "Acme"},
        "description": {"de-DE": "DESCRIPTION", "en-GB": "DESCRIPTION"},
        "manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"},
        "supportLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}
    }
}"#;

fn complete(extra: &str) -> String {
    COMPLETE
        .replace("EXTRA", extra)
        .replace("DESCRIPTION", &"a".repeat(150))
}

#[test]
fn shopware_store_composer_analyzer() {
    let directory = tempfile::tempdir().unwrap();
    write_png(
        &directory.path().join("src/Resources/config/plugin.png"),
        128,
        128,
        1024,
    );
    let uri = file_uri(directory.path().join("composer.json").to_str().unwrap());
    check_problems(&analyzer(Some(true)), &uri, &complete(""));
    // A missing key is reported on its nearest existing parent; the length
    // problem sits on the description's own locale key.
    let fixture = complete("")
        .replace(
            r#""supportLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}"#,
            "",
        )
        .replace(
            r#""manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"},"#,
            r#""manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}"#,
        )
        .replace(
            r#""extra": {"#,
            r#""{|shopware.store.support-link:extra|}": {"#,
        )
        .replace(
            &format!(r#""en-GB": "{}""#, "a".repeat(150)),
            r#""{|shopware.store.description:en-GB|}": "short""#,
        );
    check_problems(&analyzer(Some(true)), &uri, &fixture);
}

#[test]
fn shopware_store_composer_analyzer_requires_explicit_opt_in() {
    let source = r#"{"type":"shopware-platform-plugin"}"#;
    for enabled in [None, Some(false)] {
        check_problems(&analyzer(enabled), "file:///project/composer.json", source);
    }
    let codes = [
        "shopware.store.authors",
        "shopware.store.autoload",
        "shopware.store.composer-description",
        "shopware.store.description",
        "shopware.store.icon",
        "shopware.store.label",
        "shopware.store.license",
        "shopware.store.manufacturer-link",
        "shopware.store.name",
        "shopware.store.plugin-class",
        "shopware.store.require-core",
        "shopware.store.support-link",
        "shopware.store.version",
    ];
    let mut fixture = source.to_owned();
    for code in codes.iter().rev() {
        fixture = format!("{{|{code}:{fixture}|}}");
    }
    check_problems(
        &analyzer(Some(true)),
        "file:///project/composer.json",
        &fixture,
    );
}

#[test]
fn shopware_store_composer_metadata_values() {
    let directory = tempfile::tempdir().unwrap();
    write_png(&directory.path().join("icon.png"), 128, 128, 1024);
    let uri = file_uri(directory.path().join("composer.json").to_str().unwrap());
    let icon = r#"
        "plugin-icon": "icon.png","#;
    let cases = [
        (
            r#""version": "1.0.0""#,
            r#""{|shopware.store.version:version|}": "one""#,
        ),
        (
            r#""license": "MIT""#,
            r#""{|shopware.store.license:license|}": "Custom""#,
        ),
        (
            r#""license": "MIT""#,
            r#""license": "(MIT OR GPL-2.0-or-later)""#,
        ),
        (r#""license": "MIT""#, r#""license": ["proprietary"]"#),
        (
            r#""authors": [{"name": "Acme"}]"#,
            r#""{|shopware.store.authors:authors|}": []"#,
        ),
        (
            r#""autoload": {"psr-4": {"Acme\\": "src/"}}"#,
            r#""{|shopware.store.autoload:autoload|}": {"classmap": ["src/"]}"#,
        ),
    ];
    for (from, to) in cases {
        let fixture = complete(icon).replace(from, to);
        assert_ne!(fixture, complete(icon), "{from}");
        check_problems(&analyzer(Some(true)), &uri, &fixture);
    }
}

#[test]
fn shopware_store_composer_icon() {
    let directory = tempfile::tempdir().unwrap();
    let uri = file_uri(directory.path().join("composer.json").to_str().unwrap());
    let icon = directory.path().join("icon.png");
    let fixture = |code: &str| {
        complete(&format!(
            r#"
        "{{|{code}:plugin-icon|}}": "icon.png","#
        ))
    };
    let (document, _) =
        check_problems(&analyzer(Some(true)), &uri, &fixture("shopware.store.icon"));
    // The icon file is an input: writing it changes the revision.
    let before = analyzer(Some(true)).input_revision(&document);
    std::fs::write(&icon, "not an image, but long enough to hold a header").unwrap();
    let after = analyzer(Some(true)).input_revision(&document);
    assert!(before.is_some() && after.is_some());
    assert_ne!(before, after);
    check_problems(&analyzer(Some(true)), &uri, &fixture("shopware.store.icon"));
    for (width, height, bytes) in [(64, 64, 1024), (128, 512, 1024), (128, 128, 31 * 1024)] {
        write_png(&icon, width, height, bytes);
        check_problems(
            &analyzer(Some(true)),
            &uri,
            &fixture("shopware.store.icon-size"),
        );
    }
    write_png(&icon, 256, 84, 30 * 1024);
    check_problems(
        &analyzer(Some(true)),
        &uri,
        &complete(
            r#"
        "plugin-icon": "icon.png","#,
        ),
    );
}
