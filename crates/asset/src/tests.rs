use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_indexer::{CancelToken, FileScanner, Indexer, ParsedFile, SupplementalPathIndexer};

use super::*;
use crate::index::slash_path_within;

fn s(path: &Path) -> String {
    path.to_str().unwrap().to_owned()
}

fn write_files(files: &[(PathBuf, &str)]) {
    for (path, source) in files {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    }
}

fn index_files(index: &Index, files: &[(PathBuf, &str)]) {
    for (path, source) in files {
        index
            .index(&ParsedFile::new(s(path), source.as_bytes().to_vec()))
            .unwrap();
    }
}

fn scan(root: &Path, cache: &Path, index: Arc<Index>) {
    let mut scanner = FileScanner::new(s(root), cache.join("scanner.db"), None).unwrap();
    scanner.add_indexer(index);
    scanner.index_all(&CancelToken::new()).unwrap();
    scanner.close().unwrap();
}

#[test]
fn index_collects_public_manifest_and_encore_resources() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let build = root.join("public/build");
    let assets = root.join("assets");
    write_files(&[
        (build.join("app.123.css"), "body{}"),
        (build.join("app.123.js"), "console.log('app')"),
        (
            build.join("manifest.json"),
            "{\n  \"build/app.css\": \"/build/app.123.css\",\n  \"build/app.js\": \"/build/app.123.js\"\n}",
        ),
        (
            build.join("entrypoints.json"),
            "{\n  \"entrypoints\": {\n    \"app\": {\"js\": [\"/build/app.123.js\"], \"css\": [\"/build/app.123.css\"]},\n    \"admin\": {\"js\": []}\n  }\n}",
        ),
        (
            root.join("webpack.config.js"),
            "\nEncore.addEntry('storefront', './assets/storefront.js')\n    .addStyleEntry('theme', './assets/theme.css');",
        ),
        (assets.join("storefront.js"), "export default {};"),
        (assets.join("theme.css"), "body{}"),
        (root.join("public/media/upload.jpg"), "generated"),
    ]);

    let index = Arc::new(Index::new(&s(root), cache.path(), None).unwrap());
    scan(root, cache.path(), index.clone());

    let names = index.names().unwrap();
    assert!(names.contains(&"build/app.123.css".to_owned()), "{names:?}");
    assert!(names.contains(&"build/app.css".to_owned()));
    assert!(!names.contains(&"media/upload.jpg".to_owned()));
    let entries = index.entry_names().unwrap();
    for entry in ["app", "admin", "storefront", "theme"] {
        assert!(entries.contains(&entry.to_owned()), "{entry}: {entries:?}");
    }
    let first = index.name_catalog().unwrap();
    let second = index.name_catalog().unwrap();
    assert!(!first.assets.is_empty());
    assert!(Arc::ptr_eq(&first, &second));

    let manifest = index.find("build/app.css", Kind::ManifestAsset).unwrap();
    assert_eq!(manifest.len(), 1);
    assert_eq!(manifest[0].target, s(&build.join("app.123.css")));
    let storefront = index.find("storefront", Kind::EncoreEntry).unwrap();
    assert_eq!(storefront.len(), 1);
    assert_eq!(storefront[0].target, s(&assets.join("storefront.js")));
    index.close().unwrap();

    let restored = Index::new(&s(root), cache.path(), None).unwrap();
    assert_eq!(restored.entry_names().unwrap(), entries);
    restored.close().unwrap();
}

#[test]
fn public_path_selection_uses_root_component_boundaries() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let index = Index::new(&s(root), cache.path(), None).unwrap();

    assert_eq!(
        index.public_relative(&s(&root.join("public"))).as_deref(),
        Some("")
    );
    assert_eq!(
        index
            .public_relative(&s(&root.join("web/build/app.js")))
            .as_deref(),
        Some("build/app.js")
    );
    assert!(
        index
            .public_relative(&s(&root.join("publicity/app.js")))
            .is_none()
    );
    assert!(
        index
            .public_relative(&format!("{}/public/../private/app.js", s(root)))
            .is_none()
    );

    assert!(index.should_index_path(&s(&root.join("public/build/app.js"))));
    assert!(index.should_index_path(&s(&root.join("public/media.jpg"))));
    assert!(!index.should_index_path(&s(&root.join("public/MeDiA/upload.jpg"))));
    assert!(!index.should_enter_directory(&s(&root.join("public/UPLOADS"))));
    assert!(index.should_enter_directory(&s(&root.join("public/build"))));
    assert!(index.should_enter_directory(&s(&root.join("src/DemoBundle/Resources/public"))));
    assert!(index.should_index_path(&s(&root.join("src/DemoBundle/Resources/public/app.js"))));
    assert!(!index.should_index_path(&s(&root.join("src/DemoBundle/Resources/publicx/app.js"))));

    let (relative, bundle) = index
        .bundle_public_relative(&s(&root.join("src/DemoBundle/Resources/public/app.js")))
        .unwrap();
    assert_eq!(relative, "app.js");
    assert_eq!(bundle, "demo");
    assert!(slash_path_within(".", "src/DemoBundle"));
    assert!(!slash_path_within(".", "../src"));
    assert!(!slash_path_within("/a/b", "/a/bc"));
    index.close().unwrap();
}

#[test]
fn index_resolves_named_bundle_base_path_and_theme_packages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let files = [
        (
            root.join("public/bundles/administration/administration/app.js"),
            "console.log('admin')",
        ),
        (root.join("public/theme/theme-id/assets/logo.svg"), "<svg/>"),
        (
            root.join("src/Administration/Resources/config/routes.xml"),
            "<routes/>",
        ),
        (
            root.join("src/Storefront/Resources/config/services.xml"),
            "<container><services><service id=\"theme\">\n<tag name=\"assets.package\" package=\"theme\"/>\n</service></services></container>",
        ),
    ];
    write_files(&files);
    let index = Index::new(&s(root), cache.path(), None).unwrap();
    index_files(&index, &files);
    let names = index.package_names().unwrap();
    assert!(names.contains(&"@Administration".to_owned()), "{names:?}");
    assert!(names.contains(&"theme".to_owned()));
    let admin = index
        .find_assets_for_package("administration/app.js", "@Administration")
        .unwrap();
    assert_eq!(admin.len(), 1);
    assert_eq!(
        index
            .find_assets_for_package("assets/logo.svg", "theme")
            .unwrap()
            .len(),
        1
    );
    assert!(
        index
            .names_for_package("@Administration")
            .unwrap()
            .contains(&"administration/app.js".to_owned())
    );
    assert!(
        index
            .names_for_package("theme")
            .unwrap()
            .contains(&"assets/logo.svg".to_owned())
    );
    index.close().unwrap();

    let restored = Index::new(&s(root), cache.path(), None).unwrap();
    assert_eq!(restored.package_names().unwrap(), names);
    assert_eq!(
        restored
            .find_assets_for_package("administration/app.js", "@Administration")
            .unwrap(),
        admin
    );
    restored.close().unwrap();
}

#[test]
fn index_resolves_legacy_assetic_bundle_files_directories_and_globs() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let css = root.join("src/MainBundle/Resources/public/css");
    let app = css.join("app.css");
    write_files(&[
        (app.clone(), "body{}"),
        (css.join("theme.scss"), "$color: red;"),
    ]);

    let index = Arc::new(Index::new(&s(root), cache.path(), None).unwrap());
    scan(root, cache.path(), index.clone());

    assert_eq!(
        index.assetic_names("@MainBundle").unwrap(),
        ["css/app.css", "css/theme.scss"]
    );
    let exact = index
        .find_assetic_assets("css/app.css", "@MainBundle")
        .unwrap();
    assert_eq!(exact.len(), 1);
    assert_eq!(exact[0].target, s(&app));
    let glob = index
        .find_assetic_assets("css/*.css", "@MainBundle")
        .unwrap();
    assert_eq!(glob.len(), 1);
    assert_eq!(glob[0].target, s(&app));
    assert_eq!(
        index
            .find_assetic_assets("css/", "@MainBundle")
            .unwrap()
            .len(),
        2
    );
    index.close().unwrap();
}

#[test]
fn index_collects_importmap_entrypoints_and_installed_modules() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let app_target = root.join("assets/app.js");
    let vendor_target = root.join("assets/vendor/bootstrap/bootstrap.index.js");
    let files = [
        (app_target.clone(), "console.log('app')"),
        (vendor_target.clone(), "export default {};"),
        (
            root.join("importmap.php"),
            "<?php\nreturn [\n    'app' => [\n        'path' => './assets/app.js',\n        'entrypoint' => true,\n    ],\n    'bootstrap' => [\n        'version' => '5.3.2',\n    ],\n];",
        ),
        (
            root.join("assets/vendor/installed.php"),
            "<?php return [\n    'bootstrap' => [\n        'version' => '5.3.2',\n        'dependencies' => [],\n    ],\n];",
        ),
        (
            root.join("vendor/composer/installed.php"),
            "<?php\nreturn ['packages' => []];",
        ),
    ];
    write_files(&files);
    let index = Index::new(&s(root), cache.path(), None).unwrap();
    index_files(&index, &files);
    let entrypoints = index.importmap_entry_names().unwrap();
    assert_eq!(entrypoints, ["app"]);
    let app = index.find_importmap_entrypoint("app").unwrap();
    assert_eq!(app.len(), 1);
    assert_eq!(app[0].target, s(&app_target));
    assert!(app[0].entrypoint);
    assert!(!app[0].range.is_empty());
    let bootstrap = index.find("bootstrap", Kind::ImportmapModule).unwrap();
    assert_eq!(bootstrap.len(), 2);
    assert_eq!(bootstrap[0].version, "5.3.2");
    assert!(
        bootstrap
            .iter()
            .any(|resource| resource.target == s(&vendor_target))
    );
    assert!(
        index
            .find_importmap_entrypoint("bootstrap")
            .unwrap()
            .is_empty()
    );
    assert!(
        index
            .find("packages", Kind::ImportmapModule)
            .unwrap()
            .is_empty()
    );
    index.close().unwrap();

    let restored = Index::new(&s(root), cache.path(), None).unwrap();
    assert_eq!(restored.importmap_entry_names().unwrap(), entrypoints);
    assert_eq!(restored.find_importmap_entrypoint("app").unwrap(), app);
    restored.close().unwrap();
}

fn assetic_container_fixture(name: &str) -> String {
    format!(
        r#"<container>
  <services>
    <service id="assetic.asset_manager">
      <call method="addResource">
        <argument type="service">
          <service class="Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">
            <argument type="collection">
              <argument key="{name}" type="collection">
                <argument type="collection">
                  <argument>../app/Resources/bower/jquery1.js</argument>
                  <argument>../app/Resources/bower/jquery2.js</argument>
                </argument>
                <argument type="collection"/>
                <argument type="collection"/>
              </argument>
            </argument>
          </service>
        </argument>
      </call>
    </service>
  </services>
</container>"#
    )
}

#[test]
fn assetic_catalog_loads_and_reloads_compiled_named_assets() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let first = root.join("app/Resources/bower/jquery1.js");
    let second = root.join("app/Resources/bower/jquery2.js");
    write_files(&[(first.clone(), "jquery"), (second.clone(), "jquery")]);
    let index = Index::new(&s(root), cache.path(), None).unwrap();
    assert!(index.assetic_named_names().is_empty());

    let container = root.join("app/cache/dev/appDevDebugProjectContainer.xml");
    write_files(&[(container.clone(), &assetic_container_fixture("jquery_js"))]);
    index.reload_assetic_catalog();
    assert_eq!(index.assetic_named_names(), ["jquery_js"]);
    let resources = index.find_assetic_named("@jquery_js");
    assert_eq!(resources.len(), 2);
    let mut targets = vec![resources[0].target.clone(), resources[1].target.clone()];
    targets.sort();
    assert_eq!(targets, [s(&first), s(&second)]);
    assert_eq!(resources[0].file, s(&container));
    let content = std::fs::read_to_string(&container).unwrap();
    assert_eq!(&content[resources[0].range], "jquery_js");

    std::thread::sleep(std::time::Duration::from_millis(20));
    write_files(&[(container, &assetic_container_fixture("jquery_runtime"))]);
    index.reload_assetic_catalog();
    assert_eq!(index.assetic_named_names(), ["jquery_runtime"]);
    index.close().unwrap();
}

#[test]
fn packages_in_xml_collect_tags_and_infer_bundle_packages() {
    let source = "<container><services>\n<service id=\"assets.theme\">\n  <tag name=\"assets.package\" package=\"theme\"/>\n</service>\n</services></container>";
    let packages = packages_in_xml(
        "/project/src/Administration/Resources/config/services.xml",
        &swlsp_parser_xml::parse(source).tree.root(),
    );
    assert_eq!(packages.len(), 2);
    assert_eq!(packages[0].name, "@Administration");
    assert_eq!(packages[0].base_path, "bundles/administration");
    assert!(packages[0].inferred);
    assert_eq!(packages[1].name, "theme");
    assert!(!packages[1].inferred);
    assert!(!packages[1].range.is_empty());
}

#[test]
fn packages_in_php_collect_symfony_and_shopware_package_tags() {
    let source = "<?php\n$services->set('theme', ThemePackage::class)\n    ->tag('shopware.asset', ['asset' => 'theme']);\n$services->set('uploads', Package::class)\n    ->tag('assets.package', ['package' => 'uploads']);\n$services->set('dynamic', Package::class)\n    ->tag('assets.package', ['package' => $name]);\n";
    let packages = packages_in_php(
        "/project/src/Storefront/DependencyInjection/theme.php",
        &swlsp_parser_php::parse(source).tree.root(),
    );
    assert_eq!(packages.len(), 2);
    assert_eq!(packages[0].name, "theme");
    assert_eq!(packages[1].name, "uploads");
    assert_eq!(&source[packages[0].range], "theme");
}

#[test]
fn packages_in_yaml_collect_base_paths_and_ignore_dynamic_paths() {
    let source = "framework:\n  assets:\n    packages:\n      uploads:\n        base_path: /uploads\n      remote:\n        base_path: '%env(CDN_PATH)%'\n";
    let packages = packages_in_yaml(
        "/project/config/packages/framework.yaml",
        &swlsp_parser_yaml::parse(source).tree.root(),
    );
    assert_eq!(packages.len(), 2);
    assert_eq!(packages[0].name, "remote");
    assert!(packages[0].base_path.is_empty());
    assert_eq!(packages[1].name, "uploads");
    assert_eq!(packages[1].base_path, "uploads");
    assert_eq!(&source[packages[1].range], "uploads");
}

#[test]
fn references_extract_twig_assets_encore_and_php_packages() {
    let cases = [
        (
            "/project/templates/page.html.twig",
            r#"{{ asset('build/app.css') }}
{{ asset('administration/app.js', '@Administration') }}
{{ asset('ignored.css', {'package': 'nested-package'}) }}
{{ encore_entry_script_tags('app') }}
{{ vite_entry_link_tags('vite-app') }}
{{ vite_entry_script_tags('vite-admin') }}
{{ importmap('frontend') }}
{{ importmap(dynamic) }}
{{ encore_entry_link_tags(dynamic) }}
<link rel="stylesheet" href="/build/theme.css">
<script src="./build/runtime.js"></script>
<img src="images/logo.svg">
<img src="https://cdn.example.test/logo.svg">
<img src="{{ asset('images/dynamic.png') }}">"#,
            vec![
                (
                    ReferenceKind::Asset,
                    vec![
                        "build/app.css",
                        "administration/app.js",
                        "ignored.css",
                        "images/dynamic.png",
                        "build/theme.css",
                        "build/runtime.js",
                        "images/logo.svg",
                    ],
                ),
                (ReferenceKind::AssetPackage, vec!["@Administration"]),
                (ReferenceKind::EncoreEntry, vec!["app"]),
                (ReferenceKind::ViteEntry, vec!["vite-app", "vite-admin"]),
                (ReferenceKind::Importmap, vec!["frontend"]),
            ],
        ),
        (
            "/project/src/Assets.php",
            "<?php\n$packages->getUrl('build/app.css');\n$packages->getVersion('build/app.js', 'theme');\n$packages->getUrl(packageName: 'uploads', path: 'image.png');",
            vec![
                (
                    ReferenceKind::Asset,
                    vec!["build/app.css", "build/app.js", "image.png"],
                ),
                (ReferenceKind::AssetPackage, vec!["theme", "uploads"]),
            ],
        ),
    ];
    for (path, source, expected) in cases {
        let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
        let refs = references(path, &parsed.syntax_tree().unwrap().root());
        let mut actual: BTreeMap<u8, Vec<String>> = BTreeMap::new();
        let mut html_types = Vec::new();
        for reference in &refs {
            actual
                .entry(reference.kind as u8)
                .or_default()
                .push(reference.name.clone());
            assert!(!reference_range(reference).is_empty());
            if reference.kind == ReferenceKind::Asset && reference.name == "administration/app.js" {
                assert_eq!(reference.package, "@Administration");
            }
            if reference.kind == ReferenceKind::AssetPackage && reference.name == "@Administration"
            {
                assert_eq!(reference.asset_name, "administration/app.js");
            }
            if reference.html_type != HtmlAssetType::None {
                html_types.push(reference.html_type);
            }
        }
        let expected: BTreeMap<u8, Vec<String>> = expected
            .into_iter()
            .map(|(kind, names)| (kind as u8, names.into_iter().map(str::to_owned).collect()))
            .collect();
        assert_eq!(actual, expected, "{path}");
        if path.ends_with(".twig") {
            assert_eq!(
                html_types,
                [
                    HtmlAssetType::Css,
                    HtmlAssetType::JavaScript,
                    HtmlAssetType::Image
                ]
            );
        }
    }
}

#[test]
fn references_extract_only_static_assetic_tag_operands() {
    let source = r"{% stylesheets
    'css/app.css'
    '@MainBundle/Resources/public/css/theme.scss'
    'css/' ~ dynamic_name
    filter='cssrewrite'
%}
<link href=`{{ asset_url }}`>
{% endstylesheets %}
{% javascripts
    'js/app.js'
    '@named_formula'
%}
<script src=`{{ asset_url }}`></script>
{% endjavascripts %}"
        .replace('`', "\"");
    let path = "/project/templates/page.html.twig";
    let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
    let assetic: Vec<Reference> = references(path, &parsed.syntax_tree().unwrap().root())
        .into_iter()
        .filter(|reference| reference.assetic)
        .collect();
    assert_eq!(assetic.len(), 4);
    assert_eq!(assetic[0].name, "css/app.css");
    assert!(assetic[0].package.is_empty());
    assert_eq!(assetic[0].html_type, HtmlAssetType::Css);
    assert_eq!(assetic[1].name, "css/theme.scss");
    assert_eq!(assetic[1].package, "@MainBundle");
    assert_eq!(assetic[1].html_type, HtmlAssetType::Css);
    assert_eq!(assetic[2].name, "js/app.js");
    assert_eq!(assetic[2].html_type, HtmlAssetType::JavaScript);
    assert_eq!(assetic[3].name, "named_formula");
    assert_eq!(assetic[3].kind, ReferenceKind::AsseticNamed);
}

#[test]
fn parse_vite_config_direct_variables_and_spreads() {
    let cases = [
        (
            "export default defineConfig({\n  build: {\n    rollupOptions: {\n      input: {\n        app: './assets/app.js',\n        'admin/main': './assets/admin.ts'\n      }\n    }\n  }\n});",
            vec![("app", "assets/app.js"), ("admin/main", "assets/admin.ts")],
        ),
        (
            "const entries = {\n  app: './assets/app.js',\n  admin: './assets/admin.js'\n};\nexport default defineConfig({\n  build: {rollupOptions: {input: entries}}\n});",
            vec![("app", "assets/app.js"), ("admin", "assets/admin.js")],
        ),
        (
            "const legacyEntries = {\n  'global/main': './assets/main.js'\n};\nconst vueEntries = {\n  'vue/app': './assets/vue/app.ts'\n};\nexport default defineConfig({\n  build: {rollupOptions: {input: {\n    ...legacyEntries,\n    ...vueEntries,\n    extra: './assets/extra.js'\n  }}}\n});",
            vec![
                ("global/main", "assets/main.js"),
                ("vue/app", "assets/vue/app.ts"),
                ("extra", "assets/extra.js"),
            ],
        ),
    ];
    for (source, expected) in cases {
        let path = "/project/vite.config.ts";
        let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
        let resources = crate::vite::parse_vite_config(path, parsed.syntax_tree());
        let mut actual = BTreeMap::new();
        for resource in &resources {
            assert_eq!(resource.kind, Kind::ViteEntry);
            assert_eq!(&source[resource.range], resource.name);
            actual.insert(
                resource.name.clone(),
                resource
                    .target
                    .strip_prefix("/project/")
                    .unwrap()
                    .to_owned(),
            );
        }
        let expected: BTreeMap<String, String> = expected
            .into_iter()
            .map(|(name, target)| (name.to_owned(), target.to_owned()))
            .collect();
        assert_eq!(actual, expected);
    }
}

#[test]
fn index_persists_vite_entries_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let config_path = s(&root.join("vite.config.js"));
    let index = Index::new(&s(root), cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            config_path.clone(),
            b"export default defineConfig({\n  build: {rollupOptions: {input: {app: './assets/app.js'}}}\n});".to_vec(),
        ))
        .unwrap();
    index
        .index(&ParsedFile::new(
            s(&root.join("templates/base.html.twig")),
            b"{{ vite_entry_script_tags('app') }}".to_vec(),
        ))
        .unwrap();
    assert_eq!(index.vite_entry_names().unwrap(), ["app"]);
    assert_eq!(
        index.usages("app", ReferenceKind::ViteEntry).unwrap().len(),
        1
    );
    index.close().unwrap();

    let restored = Index::new(&s(root), cache.path(), None).unwrap();
    assert_eq!(restored.vite_entry_names().unwrap(), ["app"]);
    restored
        .index(&ParsedFile::new(
            config_path,
            b"export default {};".to_vec(),
        ))
        .unwrap();
    assert!(restored.vite_entry_names().unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn html_asset_type_matches_extensions() {
    assert!(matches_html_asset_type("a.SCSS", HtmlAssetType::Css));
    assert!(!matches_html_asset_type("a.png", HtmlAssetType::JavaScript));
}
