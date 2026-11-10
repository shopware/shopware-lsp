use super::*;
use crate::definition::php_common::test_support::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::Indexer;
use swlsp_uriutil::file_uri;

fn cache(dir: &Path) -> std::path::PathBuf {
    dir.join("cache")
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    index(indexer, path, source);
}

// -- translation --------------------------------------------------------------

fn translation_index(root: &Path) -> Arc<swlsp_translation::Index> {
    let index = Arc::new(swlsp_translation::Index::new(cache(root), None).unwrap());
    index_file(
        &*index,
        "/project/translations/admin.en.yaml",
        "admin.dashboard: Dashboard\n",
    );
    index
}

/// A translation request with an explicit node at position 0:0.
fn node_request(
    document: &Arc<swlsp_lsp::TextDocument>,
    offset: usize,
) -> swlsp_lsp::DefinitionRequest {
    let mut request = request_at(document, offset);
    request.params.position = Default::default();
    request
}

// -- symfony config -----------------------------------------------------------

fn symfony_config_index(root: &Path) -> Arc<swlsp_symfonyconfig::Index> {
    Arc::new(swlsp_symfonyconfig::Index::new(cache(root), None).unwrap())
}

// -- assets -------------------------------------------------------------------

fn asset_index(root: &Path) -> Arc<swlsp_asset::Index> {
    Arc::new(swlsp_asset::Index::new(&p(root), root.join("asset-cache"), None).unwrap())
}

fn template_document(root: &Path, name: &str, source: &str) -> Arc<swlsp_lsp::TextDocument> {
    document(
        &format!("file://{}", p(&root.join("templates").join(name))),
        source,
    )
}

// -- environment --------------------------------------------------------------

#[test]
fn environment_definition_navigates_every_declaration() {
    let root = tempfile::tempdir().unwrap();
    let env_path = p(&root.path().join(".env"));
    let docker_path = p(&root.path().join("Dockerfile"));
    write(Path::new(&env_path), "APP_ENV=dev\n");
    write(Path::new(&docker_path), "ENV APP_ENV prod\n");
    let index = Arc::new(swlsp_environment::Index::new(cache(root.path()), None).unwrap());
    index_file(&*index, &env_path, "APP_ENV=dev\n");
    index_file(&*index, &docker_path, "ENV APP_ENV prod\n");

    let source = "parameters:\n  app_env: '%env(APP_ENV)%'\n";
    let path = p(&root.path().join("config/services.yaml"));
    let document = document(&file_uri(&path), source);
    // No tree is passed; the provider falls back to the source scan.
    let request = request_with_node(&document, None, index_of(source, "APP_ENV") + 2);
    let locations = definitions(&EnvironmentDefinitionProvider::new(Some(index)), &request);
    assert_eq!(locations.len(), 2);
    let mut uris: Vec<_> = locations
        .iter()
        .map(|location| location.uri.clone())
        .collect();
    uris.sort();
    let mut expected = vec![file_uri(&env_path), file_uri(&docker_path)];
    expected.sort();
    assert_eq!(uris, expected);
}

#[test]
fn environment_definition_from_direct_php_references() {
    let root = tempfile::tempdir().unwrap();
    let env_path = p(&root.path().join(".env"));
    write(Path::new(&env_path), "APP_ENV=dev\n");
    let index = Arc::new(swlsp_environment::Index::new(cache(root.path()), None).unwrap());
    index_file(&*index, &env_path, "APP_ENV=dev\n");
    let provider = EnvironmentDefinitionProvider::new(Some(index));
    for (name, source) in [
        (
            "Autowire attribute",
            "<?php #[Autowire(env: 'bool:APP_ENV')] class Config {}",
        ),
        ("env function", "<?php env('resolve:APP_ENV');"),
    ] {
        let path = p(&root.path().join("src").join(format!("{name}.php")));
        let document = document(&file_uri(&path), source);
        let request = request_at(&document, index_of(source, "APP_ENV") + 2);
        let locations = definitions(&provider, &request);
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&env_path));
    }
}

// -- validation ---------------------------------------------------------------

#[test]
fn constraint_option_definition_navigates_property() {
    let root = tempfile::tempdir().unwrap();
    let constraint_path = p(&root.path().join("vendor/Constraint.php"));
    let usage_path = p(&root.path().join("src/Validation.php"));
    let constraint_source = "<?php
namespace Symfony\\Component\\Validator;
class Constraint { public string $message; }
";
    let usage_source = "<?php
class UniqueName extends \\Symfony\\Component\\Validator\\Constraint {}
new UniqueName(['message' => 'key']);
";
    write(Path::new(&constraint_path), constraint_source);
    write(Path::new(&usage_path), usage_source);
    let php = php_index(&cache(root.path()));
    index(&php, &constraint_path, constraint_source);
    index(&php, &usage_path, usage_source);
    let document = document(&file_uri(&usage_path), usage_source);
    let request = request_at(&document, index_of(usage_source, "message") + 2);
    let locations = definitions(&ValidationDefinitionProvider::new(Some(php)), &request);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&constraint_path));
}

#[test]
fn translation_definition_resolves_twig_key_and_domain() {
    let root = tempfile::tempdir().unwrap();
    let provider = TranslationDefinitionProvider::new(Some(translation_index(root.path())), None);
    let source = "{{ 'admin.dashboard'|trans({}, 'admin') }}";
    let document = document("file:///project/template.twig", source);
    let locations = definitions(
        &provider,
        &node_request(&document, index_of(source, "admin.dashboard") + 2),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(
        locations[0].uri,
        file_uri("/project/translations/admin.en.yaml")
    );
    assert_eq!(locations[0].range.start.line, 0);

    let locations = definitions(
        &provider,
        &node_request(&document, last_index_of(source, "admin") + 2),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(
        locations[0].uri,
        file_uri("/project/translations/admin.en.yaml")
    );
}

#[test]
fn phpdoc_translation_assistant_tag_definition() {
    let root = tempfile::tempdir().unwrap();
    let php = php_index(&root.path().join("php"));
    index(
        &php,
        "/project/TranslationAssistant.php",
        "<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}

/** @param string $domain #TranslationDomain */
function resolve_domain(string $domain): void {}
",
    );
    let provider =
        TranslationDefinitionProvider::new(Some(translation_index(root.path())), Some(php));
    for (name, source, needle) in [
        (
            "key with named sibling domain",
            "<?php resolve_translation(\n    domain: 'admin',\n    key: 'admin.dashboard',\n);",
            "admin.dashboard",
        ),
        ("domain", "<?php resolve_domain('admin');", "admin"),
    ] {
        let document = document("file:///project/Usage.php", source);
        let locations = definitions(
            &provider,
            &node_request(&document, last_index_of(source, needle) + 2),
        );
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(
            locations[0].uri,
            file_uri("/project/translations/admin.en.yaml")
        );
    }
}

#[test]
fn symfony_config_definition_navigates_to_tree_root() {
    let root = tempfile::tempdir().unwrap();
    let declaration_path = p(&root
        .path()
        .join("src/DependencyInjection/Configuration.php"));
    let declaration_source = "<?php
namespace App\\DependencyInjection;
use Symfony\\Component\\Config\\Definition\\Builder\\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
";
    write(Path::new(&declaration_path), declaration_source);
    let index = symfony_config_index(root.path());
    index_file(&*index, &declaration_path, declaration_source);
    let provider = SymfonyConfigDefinitionProvider::new(Some(index));
    for (name, source) in [
        (
            "app.php",
            "<?php return ['when@prod' => ['app_root' => []]];",
        ),
        ("app.yaml", "when@prod:\n  app_root: {}\n"),
    ] {
        let path = p(&root.path().join("config/packages").join(name));
        let document = document(&file_uri(&path), source);
        let locations = definitions(
            &provider,
            &request_at(&document, index_of(source, "app_root") + 2),
        );
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&declaration_path));
        assert_eq!(locations[0].range.start.line, 5);
        assert_eq!(locations[0].range.start.character, 32);
    }
}

#[test]
fn symfony_config_definition_navigates_to_imported_php_resource() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("config/packages");
    let target = p(&config_dir.join("legacy.php"));
    write(Path::new(&target), "<?php return [];");
    let provider = SymfonyConfigDefinitionProvider::new(Some(symfony_config_index(root.path())));
    for (name, source) in [
        (
            "app.php",
            "<?php\nreturn [\n    'imports' => [\n        ['resource' => 'legacy.php'],\n    ],\n];\n",
        ),
        ("app.yaml", "imports:\n  - resource: legacy.php\n"),
    ] {
        let document = document(&file_uri(&p(&config_dir.join(name))), source);
        let locations = definitions(
            &provider,
            &request_at(&document, index_of(source, "legacy.php") + 2),
        );
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&target));
    }
}

#[test]
fn symfony_config_definition_navigates_to_imported_resource_glob() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("config/packages");
    let mut expected = Vec::new();
    for name in ["legacy_a.php", "legacy_b.php"] {
        let path = p(&config_dir.join(name));
        write(Path::new(&path), "<?php return [];");
        expected.push(file_uri(&path));
    }
    let source = "<?php\nreturn ['imports' => [['resource' => 'legacy_*.php']]];\n";
    let document = document(&file_uri(&p(&config_dir.join("app.php"))), source);
    let provider = SymfonyConfigDefinitionProvider::new(Some(symfony_config_index(root.path())));
    let locations = definitions(
        &provider,
        &request_at(&document, index_of(source, "legacy_") + 2),
    );
    let uris: Vec<_> = locations.into_iter().map(|location| location.uri).collect();
    assert_eq!(uris, expected);
}

#[test]
fn asset_definition_navigates_physical_manifest_and_encore_targets() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let build = root.join("public/build");
    let assets = root.join("assets");
    for (path, source) in [
        (build.join("app.css"), "body{}"),
        (build.join("app.js"), "console.log('app')"),
        (assets.join("main.js"), "export default {};"),
        (assets.join("frontend.ts"), "export const app = {};"),
    ] {
        write(&path, source);
    }
    let manifest_path = p(&build.join("manifest.json"));
    let manifest = r#"{"build/app.js": "/build/app.js"}"#;
    let webpack_path = p(&root.join("webpack.config.js"));
    let webpack = "Encore.addEntry('storefront', './assets/main.js');";
    let vite_path = p(&root.join("vite.config.ts"));
    let vite = "export default defineConfig({\n  build: {rollupOptions: {input: {frontend: './assets/frontend.ts'}}}\n});";
    write(Path::new(&manifest_path), manifest);
    write(Path::new(&webpack_path), webpack);
    write(Path::new(&vite_path), vite);
    let index = asset_index(root);
    for (path, source) in [
        (p(&build.join("app.css")), "body{}"),
        (manifest_path, manifest),
        (webpack_path, webpack),
        (vite_path, vite),
    ] {
        index_file(&*index, &path, source);
    }
    let provider = AssetDefinitionProvider::new(Some(index), Some(php_index(&root.join("php"))));
    for (source, value, target) in [
        (
            "{{ asset('build/app.css') }}",
            "build/app.css",
            build.join("app.css"),
        ),
        (
            "{{ asset('build/app.js') }}",
            "build/app.js",
            build.join("app.js"),
        ),
        (
            "{{ encore_entry_script_tags('storefront') }}",
            "storefront",
            assets.join("main.js"),
        ),
        (
            "{{ vite_entry_script_tags('frontend') }}",
            "frontend",
            assets.join("frontend.ts"),
        ),
        (
            r#"<script src="/build/app.js"></script>"#,
            "build/app.js",
            build.join("app.js"),
        ),
    ] {
        let document = template_document(root, "page.html.twig", source);
        let locations = definitions(
            &provider,
            &request_at(&document, index_of(source, value) + 2),
        );
        assert!(!locations.is_empty(), "{value}");
        assert_eq!(locations[0].uri, file_uri(&p(&target)), "{value}");
    }
}

#[test]
fn asset_definition_resolves_named_package_and_its_declaration() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let target = p(&root.join("public/bundles/administration/administration/app.js"));
    let config = p(&root.join("src/Administration/Resources/config/routes.xml"));
    write(Path::new(&target), "app");
    write(Path::new(&config), "<routes/>");
    let index = asset_index(root);
    index_file(&*index, &target, "app");
    index_file(&*index, &config, "<routes/>");
    let provider = AssetDefinitionProvider::new(Some(index), None);
    let source = "{{ asset('administration/app.js', '@Administration') }}";
    let document = template_document(root, "page.html.twig", source);
    for (value, expected) in [
        ("administration/app.js", &target),
        ("@Administration", &config),
    ] {
        let locations = definitions(
            &provider,
            &request_at(&document, index_of(source, value) + 2),
        );
        assert!(!locations.is_empty(), "{value}");
        assert_eq!(locations[0].uri, file_uri(expected));
    }
}

#[test]
fn asset_definition_navigates_legacy_assetic_bundle_glob() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let target = p(&root.join("src/MainBundle/Resources/public/css/app.css"));
    write(Path::new(&target), "body{}");
    let index = asset_index(root);
    index_file(&*index, &target, "body{}");
    let source = "{% stylesheets '@MainBundle/Resources/public/css/*.css' %}\n<link href=\"{{ asset_url }}\">\n{% endstylesheets %}";
    let document = template_document(root, "page.html.twig", source);
    let provider = AssetDefinitionProvider::new(Some(index.clone()), None);
    let locations = definitions(
        &provider,
        &request_at(&document, index_of(source, "css/*.css") + 2),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&target));

    let container = root.join("app/cache/dev/appDevDebugProjectContainer.xml");
    write(
        &container,
        &format!(
            "<container><services><service id=\"assetic.asset_manager\"><call method=\"addResource\"><service class=\"Symfony\\Bundle\\AsseticBundle\\Factory\\Resource\\ConfigurationResource\"><argument><argument key=\"app_css\"><argument><argument>{target}</argument></argument></argument></argument></service></call></service></services></container>"
        ),
    );
    index.reload_assetic_catalog();
    let named =
        "{% stylesheets '@app_css' %}\n<link href=\"{{ asset_url }}\">\n{% endstylesheets %}";
    let document = template_document(root, "named.html.twig", named);
    let locations = definitions(
        &provider,
        &request_at(&document, index_of(named, "@app_css") + 2),
    );
    assert!(!locations.is_empty());
    assert_eq!(locations[0].uri, file_uri(&target));
}

#[test]
fn asset_definition_navigates_importmap_target_and_declaration() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let target = p(&root.join("assets/app.js"));
    let importmap_path = p(&root.join("importmap.php"));
    let importmap = "<?php
return [
    'app' => [
        'path' => './assets/app.js',
        'entrypoint' => true,
    ],
];";
    write(Path::new(&target), "app");
    write(Path::new(&importmap_path), importmap);
    let index = asset_index(root);
    index_file(&*index, &importmap_path, importmap);
    let source = "{{ importmap('app') }}";
    let document = template_document(root, "page.html.twig", source);
    let locations = definitions(
        &AssetDefinitionProvider::new(Some(index), None),
        &request_at(&document, index_of(source, "app") + 1),
    );
    assert_eq!(locations.len(), 2);
    assert_eq!(locations[0].uri, file_uri(&target));
    assert_eq!(locations[1].uri, file_uri(&importmap_path));
    assert_eq!(locations[1].range.start.line, 2);
}
