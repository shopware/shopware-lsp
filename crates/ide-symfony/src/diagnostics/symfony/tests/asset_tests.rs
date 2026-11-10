use std::path::Path;
use std::sync::Arc;

use swlsp_asset::Index;

use super::{index_file, suggestions, tempdir};
use crate::diagnostics::symfony::AssetAnalyzer;
use crate::diagnostics::test_support::check_problems;

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
    let (_, result) = check_problems(
        &AssetAnalyzer::new(Some(index), None),
        &template_uri(root, "page.html.twig"),
        "{{ asset('{|symfony.asset.missing:build/ap.css|}') }}
{{ asset('build/app.css') }}
{{ encore_entry_script_tags('{|symfony.encore.entry.missing:storefrnot|}') }}
{{ encore_entry_script_tags('storefront') }}",
    );
    assert!(suggestions(&result[0]).contains(&"build/app.css".to_owned()));
    assert!(suggestions(&result[1]).contains(&"storefront".to_owned()));
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
    let (_, result) = check_problems(
        &AssetAnalyzer::new(Some(index), None),
        &template_uri(root, "page.html.twig"),
        "{{ asset('administration/app.js', '@Administration') }}
{{ asset('{|symfony.asset.missing:administration/ap.js|}', '@Administration') }}
{{ asset('administration/app.js', '{|symfony.asset.package.missing:@Administrtion|}') }}",
    );
    let [asset, package] = result.as_slice() else {
        unreachable!()
    };
    assert!(asset.message.contains("administration/ap.js"));
    assert!(suggestions(asset).contains(&"administration/app.js".to_owned()));
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
    let analyzer = AssetAnalyzer::new(Some(index), None);
    let (_, result) = check_problems(
        &analyzer,
        &template_uri(root, "page.html.twig"),
        "{% stylesheets
    '@MainBundle/Resources/public/css/*.css'
    '{|symfony.asset.missing:@MainBundle/Resources/public/css/ap.css|}'
    'css/' ~ dynamic_name
    '@named_formula'
%}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}",
    );
    assert!(result[0].message.contains("css/ap.css"));
    assert!(
        suggestions(&result[0]).contains(&"@MainBundle/Resources/public/css/app.css".to_owned())
    );

    let (_, missing_named) = check_problems(
        &analyzer,
        &template_uri(root, "named.html.twig"),
        "{% stylesheets '{|symfony.asset.missing:@named_formla|}' %}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}",
    );
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
    let (_, result) = check_problems(
        &AssetAnalyzer::new(Some(index), None),
        &template_uri(root, "page.html.twig"),
        "{{ importmap('app') }}
{{ importmap('{|symfony.asset_mapper.entrypoint.missing:ap|}') }}
{{ importmap('{|symfony.asset_mapper.entrypoint.missing:bootstrap|}') }}",
    );
    for problem in &result {
        assert!(problem.message.contains("AssetMapper entrypoint"));
    }
    assert!(suggestions(&result[0]).contains(&"app".to_owned()));
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
    let (_, result) = check_problems(
        &AssetAnalyzer::new(Some(index), None),
        &template_uri(root, "page.html.twig"),
        "{{ vite_entry_script_tags('frontend') }}
{{ vite_entry_link_tags('{|symfony.vite.entry.missing:fronend|}') }}",
    );
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
    let (_, result) = check_problems(
        &AssetAnalyzer::new(Some(index), None),
        &template_uri(root, "page.html.twig"),
        r#"<img src="/images/logo.svg">
<img src="{|symfony.asset.missing:images/log.svg|}">
<img src="https://cdn.example.test/missing.svg">
<img src="data:image/svg+xml;base64,abc">
<img src="{{ asset(dynamicPath) }}">
<script src="/_webpack_hot_proxy_/storefront/hot-reloading.js"></script>"#,
    );
    assert!(result[0].message.contains("images/log.svg"));
    assert!(suggestions(&result[0]).contains(&"images/logo.svg".to_owned()));
}
