use std::path::Path;
use std::sync::Arc;

use swlsp_asset::Index;

use super::{analyze, index_file, suggestions, tempdir};
use crate::symfony::{
    AssetAnalyzer, MISSING_ASSET_CODE, MISSING_ASSET_PACKAGE_CODE, MISSING_ENCORE_ENTRY_CODE,
    MISSING_IMPORTMAP_CODE, MISSING_VITE_ENTRY_CODE,
};
use crate::test_support::diagnostics_document;

fn write(path: &Path, content: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

fn s(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn open_asset(root: &Path) -> (tempfile::TempDir, Arc<Index>) {
    let cache = tempdir();
    let index = Arc::new(Index::new(&s(root), cache.path(), None).unwrap());
    (cache, index)
}

fn template_uri(root: &Path, name: &str) -> String {
    format!("file://{}", s(&root.join("templates").join(name)))
}

#[test]
fn asset_diagnostics_report_missing_asset_and_encore_entry() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let asset_path = root.join("public/build/app.css");
    write(&asset_path, "body{}");
    let (_cache, index) = open_asset(root);
    index_file(index.as_ref(), &s(&asset_path), "body{}");
    index_file(
        index.as_ref(),
        &s(&root.join("webpack.config.js")),
        "Encore.addEntry('storefront', './assets/app.js');",
    );
    let source = "{{ asset('build/ap.css') }}
{{ asset('build/app.css') }}
{{ encore_entry_script_tags('storefrnot') }}
{{ encore_entry_script_tags('storefront') }}";
    let result = analyze(
        &AssetAnalyzer::new(Some(index), None),
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 2);
    let codes: Vec<&str> = result.iter().map(|problem| problem.id.0.as_str()).collect();
    assert!(codes.contains(&MISSING_ASSET_CODE));
    assert!(codes.contains(&MISSING_ENCORE_ENTRY_CODE));
    for problem in &result {
        let values = suggestions(problem);
        if problem.id.0 == MISSING_ASSET_CODE {
            assert!(values.contains(&"build/app.css".to_owned()));
        } else {
            assert!(values.contains(&"storefront".to_owned()));
        }
    }
}

#[test]
fn asset_diagnostics_are_package_aware_and_report_unknown_packages() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let target = root.join("public/bundles/administration/administration/app.js");
    let config = root.join("src/Administration/Resources/config/routes.xml");
    write(&target, "app");
    write(&config, "<routes/>");
    let (_cache, index) = open_asset(root);
    index_file(index.as_ref(), &s(&target), "app");
    index_file(index.as_ref(), &s(&config), "<routes/>");
    let source = "{{ asset('administration/app.js', '@Administration') }}
{{ asset('administration/ap.js', '@Administration') }}
{{ asset('administration/app.js', '@Administrtion') }}";
    let result = analyze(
        &AssetAnalyzer::new(Some(index), None),
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 2);
    let asset = result
        .iter()
        .find(|problem| problem.id.0 == MISSING_ASSET_CODE)
        .expect("asset problem");
    assert!(asset.message.contains("administration/ap.js"));
    assert!(suggestions(asset).contains(&"administration/app.js".to_owned()));
    let package = result
        .iter()
        .find(|problem| problem.id.0 == MISSING_ASSET_PACKAGE_CODE)
        .expect("package problem");
    assert!(package.message.contains("@Administrtion"));
    assert!(suggestions(package).contains(&"@Administration".to_owned()));
}

#[test]
fn asset_diagnostics_validate_legacy_assetic_tag_operands() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let target = root.join("src/MainBundle/Resources/public/css/app.css");
    write(&target, "body{}");
    let (_cache, index) = open_asset(root);
    index_file(index.as_ref(), &s(&target), "body{}");
    write(
        &root.join("app/cache/dev/appDevDebugProjectContainer.xml"),
        r#"<container><services>
<service id="assetic.asset_manager"><call method="addResource">
<service class="Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">
<argument><argument key="named_formula"/></argument>
</service></call></service>
</services></container>"#,
    );
    index.reload_assetic_catalog();
    let source = "{% stylesheets
    '@MainBundle/Resources/public/css/*.css'
    '@MainBundle/Resources/public/css/ap.css'
    'css/' ~ dynamic_name
    '@named_formula'
%}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}";
    let analyzer = AssetAnalyzer::new(Some(index), None);
    let result = analyze(
        &analyzer,
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_ASSET_CODE);
    assert!(result[0].message.contains("css/ap.css"));
    assert!(
        suggestions(&result[0]).contains(&"@MainBundle/Resources/public/css/app.css".to_owned())
    );

    let missing_named_source = "{% stylesheets '@named_formla' %}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}";
    let missing_named = analyze(
        &analyzer,
        &diagnostics_document(&template_uri(root, "named.html.twig"), missing_named_source),
    );
    assert_eq!(missing_named.len(), 1);
    assert!(missing_named[0].message.contains("@named_formla"));
    assert!(suggestions(&missing_named[0]).contains(&"@named_formula".to_owned()));
}

#[test]
fn asset_diagnostics_validate_importmap_entrypoints() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let (_cache, index) = open_asset(root);
    index_file(
        index.as_ref(),
        &s(&root.join("importmap.php")),
        "<?php
return [
    'app' => ['path' => './assets/app.js', 'entrypoint' => true],
    'bootstrap' => ['version' => '5.3.2'],
];",
    );
    let source = "{{ importmap('app') }}
{{ importmap('ap') }}
{{ importmap('bootstrap') }}";
    let result = analyze(
        &AssetAnalyzer::new(Some(index), None),
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.0, MISSING_IMPORTMAP_CODE);
        assert!(problem.message.contains("AssetMapper entrypoint"));
        if problem.message.contains("'ap'") {
            assert!(suggestions(problem).contains(&"app".to_owned()));
        }
    }
}

#[test]
fn asset_diagnostics_validate_vite_entrypoints() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let (_cache, index) = open_asset(root);
    index_file(
        index.as_ref(),
        &s(&root.join("vite.config.js")),
        "export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.js'}}}
});",
    );
    let source = "{{ vite_entry_script_tags('frontend') }}
{{ vite_entry_link_tags('fronend') }}";
    let result = analyze(
        &AssetAnalyzer::new(Some(index), None),
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_VITE_ENTRY_CODE);
    assert!(suggestions(&result[0]).contains(&"frontend".to_owned()));
}

#[test]
fn asset_diagnostics_validate_static_twig_html_assets() {
    let root_dir = tempdir();
    let root = root_dir.path();
    let logo = root.join("public/images/logo.svg");
    write(&logo, "<svg/>");
    let (_cache, index) = open_asset(root);
    index_file(index.as_ref(), &s(&logo), "<svg/>");
    let source = r#"<img src="/images/logo.svg">
<img src="images/log.svg">
<img src="https://cdn.example.test/missing.svg">
<img src="data:image/svg+xml;base64,abc">
<img src="{{ asset(dynamicPath) }}">
<script src="/_webpack_hot_proxy_/storefront/hot-reloading.js"></script>"#;
    let result = analyze(
        &AssetAnalyzer::new(Some(index), None),
        &diagnostics_document(&template_uri(root, "page.html.twig"), source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_ASSET_CODE);
    assert!(result[0].message.contains("images/log.svg"));
    assert!(suggestions(&result[0]).contains(&"images/logo.svg".to_owned()));
}
