use std::sync::Arc;

use swlsp_lsp::CompletionProvider;

use super::*;
use crate::completion::AssetCompletionProvider;

fn write(path: &str, content: &str) {
    std::fs::create_dir_all(std::path::Path::new(path).parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

fn asset_index(root: &str, cache: &tempfile::TempDir) -> Arc<swlsp_asset::Index> {
    Arc::new(swlsp_asset::Index::new(root, cache.path(), None).unwrap())
}

fn twig_document(root: &str, name: &str, source: &str) -> Arc<swlsp_lsp::TextDocument> {
    document(&format!("file://{root}/templates/{name}"), source)
}

#[test]
fn asset_completion_for_twig_encore_and_typed_php_packages() {
    let (root_dir, asset_cache, php_cache) = (tempdir(), tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let asset_path = format!("{root}/public/build/app.css");
    write(&asset_path, "body{}");
    let index = asset_index(&root, &asset_cache);
    index_file(index.as_ref(), &asset_path, "body{}");
    index_file(
        index.as_ref(),
        &format!("{root}/webpack.config.js"),
        "Encore.addEntry('storefront', './assets/app.js');",
    );
    index_file(
        index.as_ref(),
        &format!("{root}/vite.config.ts"),
        "export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.ts'}}}
});",
    );
    let php = php_index(&php_cache);
    index_file(
        &php,
        &format!("{root}/vendor/assets.php"),
        r"<?php
namespace Symfony\Component\Asset;
interface PackageInterface {
    public function getUrl(string $path): string;
    public function getVersion(string $path): string;
}",
    );
    let provider = AssetCompletionProvider::new(index, Some(php));
    for (source, marker, label) in [
        ("{{ asset('build/ap') }}", "build/ap", "build/app.css"),
        (
            "{{ encore_entry_script_tags('store') }}",
            "store",
            "storefront",
        ),
        ("{{ vite_entry_link_tags('front') }}", "front", "frontend"),
    ] {
        let document = twig_document(&root, "page.html.twig", source);
        let offset = source.find(marker).unwrap() + marker.len();
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        require_completion(&items, label);
    }

    let php_source = r"<?php
use Symfony\Component\Asset\PackageInterface;
function url(PackageInterface $assets): string {
    return $assets->getUrl('build/ap');
}";
    let document = document(&format!("file://{root}/src/Usage.php"), php_source);
    let offset = php_source.find("build/ap").unwrap() + "build/ap".len();
    let items = provider.get_completions(&ctx(), &request(&document, offset));
    require_completion(&items, "build/app.css");
}

#[test]
fn asset_completion_uses_named_package_logical_paths_and_names() {
    let (root_dir, cache) = (tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let admin_asset = format!("{root}/public/bundles/administration/administration/app.js");
    let config_path = format!("{root}/src/Administration/Resources/config/routes.xml");
    write(&admin_asset, "app");
    write(&config_path, "<routes/>");
    let index = asset_index(&root, &cache);
    index_file(index.as_ref(), &admin_asset, "app");
    index_file(index.as_ref(), &config_path, "<routes/>");
    let provider = AssetCompletionProvider::new(index, None);
    for (source, marker, label) in [
        (
            "{{ asset('administration/ap', '@Administration') }}",
            "administration/ap",
            "administration/app.js",
        ),
        (
            "{{ asset('administration/app.js', '@Admin') }}",
            "@Admin",
            "@Administration",
        ),
    ] {
        let document = twig_document(&root, "page.html.twig", source);
        let offset = source.find(marker).unwrap() + marker.len();
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        require_completion(&items, label);
    }
}

#[test]
fn asset_completion_in_legacy_assetic_tags() {
    let (root_dir, cache) = (tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let target = format!("{root}/src/MainBundle/Resources/public/css/app.css");
    write(&target, "body{}");
    let index = asset_index(&root, &cache);
    index_file(index.as_ref(), &target, "body{}");
    let source = "{% stylesheets '@MainBundle/Resources/public/css/ap' %}
<link href=\"{{ asset_url }}\">
{% endstylesheets %}";
    let document = twig_document(&root, "page.html.twig", source);
    let offset = source.find("css/ap").unwrap() + "css/ap".len();
    let items = AssetCompletionProvider::new(index.clone(), None)
        .get_completions(&ctx(), &request(&document, offset));
    require_completion(&items, "@MainBundle/Resources/public/css/app.css");
    for item in &items {
        assert!(
            item.text_edit
                .as_ref()
                .is_none_or(|edit| !edit.new_text.contains("{{ asset("))
        );
    }

    write(
        &format!("{root}/app/cache/dev/appDevDebugProjectContainer.xml"),
        r#"<container><services>
<service id="assetic.asset_manager"><call method="addResource">
<service class="Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">
<argument><argument key="jquery_js"/></argument>
</service></call></service>
</services></container>"#,
    );
    index.reload_assetic_catalog();
    let named_source = "{% javascripts '@jquery_' %}
<script src=\"{{ asset_url }}\"></script>
{% endjavascripts %}";
    let named = twig_document(&root, "scripts.html.twig", named_source);
    let offset = named_source.find("@jquery_").unwrap() + "@jquery_".len();
    let items =
        AssetCompletionProvider::new(index, None).get_completions(&ctx(), &request(&named, offset));
    require_completion(&items, "@jquery_js");
}

#[test]
fn asset_package_completion_is_type_aware_in_php() {
    let (root_dir, cache, php_cache) = (tempdir(), tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let config = format!("{root}/config/packages/framework.yaml");
    let config_source = "framework:
  assets:
    packages:
      uploads:
        base_path: /uploads
";
    write(&config, config_source);
    let index = asset_index(&root, &cache);
    index_file(index.as_ref(), &config, config_source);
    let php = php_index(&php_cache);
    index_file(
        &php,
        &format!("{root}/vendor/Packages.php"),
        r"<?php
namespace Symfony\Component\Asset;
class Packages {
    public function getUrl(string $path, ?string $packageName = null): string {}
}",
    );
    let provider = AssetCompletionProvider::new(index, Some(php));
    for (class, want) in [
        (r"\Symfony\Component\Asset\Packages", true),
        (r"\App\Unrelated", false),
    ] {
        let source = format!(
            "<?php
class Usage {{
    public function url({class} $packages): string {{
        return $packages->getUrl(packageName: 'upl', path: 'image.png');
    }}
}}"
        );
        let document = document(&format!("file://{root}/src/Usage.php"), &source);
        let offset = source.find("upl").unwrap() + 3;
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        if want {
            require_completion(&items, "uploads");
        } else {
            assert!(!labels(&items).contains(&"uploads".to_string()));
        }
    }
}

#[test]
fn asset_completion_offers_only_importmap_entrypoints() {
    let (root_dir, cache) = (tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let index = asset_index(&root, &cache);
    index_file(
        index.as_ref(),
        &format!("{root}/importmap.php"),
        "<?php
return [
    'app' => ['path' => './assets/app.js', 'entrypoint' => true],
    'bootstrap' => ['version' => '5.3.2'],
];",
    );
    let source = "{{ importmap('ap') }}";
    let document = twig_document(&root, "page.html.twig", source);
    let offset = source.rfind("ap").unwrap() + 2;
    let items = AssetCompletionProvider::new(index, None)
        .get_completions(&ctx(), &request(&document, offset));
    require_completion(&items, "app");
    assert!(!labels(&items).contains(&"bootstrap".to_string()));
}

#[test]
fn asset_completion_wraps_typed_twig_html_attributes() {
    let (root_dir, cache) = (tempdir(), tempdir());
    let root = root_dir.path().to_string_lossy().into_owned();
    let files = [
        (format!("{root}/public/build/theme.css"), "body{}"),
        (format!("{root}/public/build/app.js"), "app"),
        (format!("{root}/public/images/logo.svg"), "<svg/>"),
    ];
    for (path, source) in &files {
        write(path, source);
    }
    let index = asset_index(&root, &cache);
    for (path, source) in &files {
        index_file(index.as_ref(), path, source);
    }
    let provider = AssetCompletionProvider::new(index, None);
    let tests = [
        (
            r#"<link rel="stylesheet" href="build/th">"#,
            "build/th",
            "build/theme.css",
            "{{ asset('build/theme.css') }}",
            "build/app.js",
            0,
        ),
        (
            "<script src='build/ap'></script>",
            "build/ap",
            "build/app.js",
            r#"{{ asset("build/app.js") }}"#,
            "build/theme.css",
            0,
        ),
        (
            r#"<img src="images/lo">"#,
            "images/lo",
            "images/logo.svg",
            "{{ asset('images/logo.svg') }}",
            "build/app.js",
            0,
        ),
        (
            r#"<img src="">"#,
            "",
            "images/logo.svg",
            "{{ asset('images/logo.svg') }}",
            "build/app.js",
            r#"<img src="">"#.find(r#""""#).unwrap() + 1,
        ),
    ];
    for (source, marker, label, new_text, exclude, offset) in tests {
        let document = twig_document(&root, "page.html.twig", source);
        let offset = if offset == 0 {
            source.find(marker).unwrap() + marker.len()
        } else {
            offset
        };
        let items = provider.get_completions(&ctx(), &request(&document, offset));
        assert!(!labels(&items).contains(&exclude.to_string()), "{source}");
        let selected = require_completion(&items, label);
        let edit = text_edit(selected);
        assert_eq!(edit.new_text, new_text);
        assert_eq!(edit.range.start.line, 0);
        let expected_start = if marker.is_empty() {
            offset
        } else {
            source.find(marker).unwrap()
        };
        assert_eq!(
            edit.range.start.character as usize, expected_start,
            "{source}"
        );
    }
}
