use std::sync::Arc;

use swlsp_appscript::Index as AppScriptIndex;
use swlsp_extension::ExtensionIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_uriutil::file_uri;

use super::analyze;
use crate::shopware::*;
use crate::test_support::document;

#[test]
fn app_script_analyzer_checks_hook_services_and_manifest_permissions() {
    let cache = tempfile::tempdir().unwrap();
    let hooks = AppScriptIndex::new(cache.path(), None).unwrap();
    let extensions = ExtensionIndexer::new(cache.path(), None).unwrap();
    for (path, source) in [
        (
            "/core/RepositoryFacadeHookFactory.php",
            r"<?php
class RepositoryFacadeHookFactory extends HookServiceFactory {
    public function getName(): string { return 'repository'; }
}",
        ),
        (
            "/core/ProductHook.php",
            r"<?php
class ProductHook extends Hook {
    public const HOOK_NAME = 'product-written';
    public static function getServiceIds(): array { return [RepositoryFacadeHookFactory::class]; }
    public function getName(): string { return self::HOOK_NAME; }
}",
        ),
    ] {
        hooks
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let app_root = cache.path().join("AcmeApp");
    let manifest_path = app_root.join("manifest.xml");
    extensions
        .index(&ParsedFile::new(
            manifest_path.to_string_lossy().as_ref(),
            b"<manifest>
    <meta><name>AcmeApp</name></meta>
    <permissions><read>product</read></permissions>
</manifest>"
                .to_vec(),
        ))
        .unwrap();
    let script_path = app_root.join("Resources/scripts/product-written/script.twig");
    let source = "{% set orders = services.repository.search('order', hook.criteria) %}
{% do services.cart.add('x') %}";
    let document = document(&file_uri(&script_path.to_string_lossy()), source);
    let problems = analyze(
        &AppScriptAnalyzer::new(Some(Arc::new(hooks)), Some(Arc::new(extensions))),
        &document,
    );
    assert_eq!(problems.len(), 2);
    assert_eq!(problems[0].id.as_str(), "app_script.service-unavailable");
    assert_eq!(problems[1].id.as_str(), "app_script.permission-missing");
}
