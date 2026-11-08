mod common;

use std::sync::Arc;

use common::*;
use swlsp_asset::Index;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::AssetHoverProvider;

fn index(root: &std::path::Path) -> Arc<Index> {
    Arc::new(Index::new(&p(root), root.join(".cache"), None).unwrap())
}

fn twig_hover(root: &std::path::Path, index: Arc<Index>, source: &str, offset: usize) -> String {
    let document = TextDocument::new(
        format!("file://{}", p(&root.join("templates/page.html.twig"))),
        source,
        1,
    );
    let request = request_at(document, offset);
    let result = hover(
        &AssetHoverProvider::new(p(root), Some(index), None),
        &request,
    )
    .expect("hover");
    result.contents.value
}

#[test]
fn asset_hover_describes_resolved_public_asset() {
    let root = tempfile::tempdir().unwrap();
    let asset_path = root.path().join("public/build/app.css");
    write(&asset_path, "body{}");
    let index = index(root.path());
    index_file(index.as_ref(), &p(&asset_path), "body{}");
    let source = "{{ asset('build/app.css') }}";
    let value = twig_hover(
        root.path(),
        index,
        source,
        source.find("build/app.css").unwrap() + 2,
    );
    assert!(value.contains("Symfony asset"));
    assert!(value.contains("public/build/app.css"));
}

#[test]
fn asset_hover_describes_named_bundle_package() {
    let root = tempfile::tempdir().unwrap();
    let config = root
        .path()
        .join("src/Administration/Resources/config/routes.xml");
    write(&config, "<routes/>");
    let index = index(root.path());
    index_file(index.as_ref(), &p(&config), "<routes/>");
    let source = "{{ asset('administration/app.js', '@Administration') }}";
    let value = twig_hover(
        root.path(),
        index,
        source,
        source.find("@Administration").unwrap() + 2,
    );
    assert!(value.contains("Symfony asset package"));
    assert!(value.contains("bundle package"));
    assert!(value.contains("bundles/administration"));
}

#[test]
fn asset_hover_describes_vite_entrypoint() {
    let root = tempfile::tempdir().unwrap();
    let config = "export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.js'}}}
});";
    let index = index(root.path());
    index_file(
        index.as_ref(),
        &p(&root.path().join("vite.config.js")),
        config,
    );
    let source = "{{ vite_entry_script_tags('frontend') }}";
    let value = twig_hover(
        root.path(),
        index,
        source,
        source.find("frontend").unwrap() + 2,
    );
    assert!(value.contains("Symfony Vite entry"));
    assert!(value.contains("assets/frontend.js"));
}

#[test]
fn asset_hover_describes_importmap_entrypoint() {
    let root = tempfile::tempdir().unwrap();
    let importmap = "<?php
return [
    'app' => [
        'url' => 'https://cdn.example.test/app.js',
        'version' => '1.2.3',
        'entrypoint' => true,
    ],
];";
    let index = index(root.path());
    index_file(
        index.as_ref(),
        &p(&root.path().join("importmap.php")),
        importmap,
    );
    let source = "{{ importmap('app') }}";
    let value = twig_hover(root.path(), index, source, source.find("app").unwrap() + 1);
    assert!(value.contains("AssetMapper entrypoint"));
    assert!(value.contains("version `1.2.3`"));
    assert!(value.contains("cdn.example.test/app.js"));
}

#[test]
fn asset_hover_describes_twig_html_asset() {
    let root = tempfile::tempdir().unwrap();
    let target = root.path().join("public/images/logo.svg");
    write(&target, "<svg/>");
    let index = index(root.path());
    index_file(index.as_ref(), &p(&target), "<svg/>");
    let source = "<img src=\"/images/logo.svg\">";
    let value = twig_hover(
        root.path(),
        index,
        source,
        source.find("images/logo.svg").unwrap() + 2,
    );
    assert!(value.contains("Twig HTML asset"));
    assert!(value.contains("public/images/logo.svg"));
}
