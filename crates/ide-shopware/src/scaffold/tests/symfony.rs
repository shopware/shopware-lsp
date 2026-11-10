use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_php::PhpIndex;
use swlsp_uriutil::file_uri;
use tempfile::TempDir;

use super::{ctx, index_source, path_string, php_index, raw};
use crate::scaffold::{Provider, Request, Response};

struct Fixture {
    provider: Provider,
    php: PhpIndex,
    root: PathBuf,
    _dirs: Vec<TempDir>,
}

fn symfony_fixture() -> Fixture {
    let root_dir = TempDir::new().unwrap();
    let root = root_dir.path().to_path_buf();
    for directory in [
        "config",
        "src/Command",
        "src/Controller",
        "src/DependencyInjection",
        "src/Feature/Command",
        "src/Twig",
        "tests/Functional",
        "tests/Integration",
    ] {
        fs::create_dir_all(root.join(directory)).unwrap();
    }
    fs::write(
        root.join("composer.json"),
        r#"{
  "autoload": {
    "psr-4": {
      "App\\": "src/",
      "App\\Feature\\": "src/Feature/"
    }
  },
  "autoload-dev": {
    "psr-4": {
      "App\\Tests\\": "tests/"
    }
  }
}"#,
    )
    .unwrap();
    let cache = TempDir::new().unwrap();
    let php = php_index(cache.path());
    php.configure_project(&root).unwrap();
    Fixture {
        provider: Provider::new(&path_string(&root), Some(php.clone()), None, None),
        php,
        root,
        _dirs: vec![root_dir, cache],
    }
}

fn create(
    provider: &Provider,
    kind: &str,
    directory: &Path,
    name: &str,
) -> crate::scaffold::Result<Response> {
    let request = Request {
        kind: kind.to_owned(),
        directory_uri: file_uri(&path_string(directory)),
        name: name.to_owned(),
    };
    provider.create(&ctx(), Some(&raw(&request)))
}

fn parses(content: &str) -> bool {
    swlsp_parser_php::parse(content).errors.is_empty()
}

#[test]
fn command_uses_composer_namespace_and_modern_template() {
    let fixture = symfony_fixture();
    index_source(
        &fixture.php,
        fixture.root.join("vendor/InvokableCommand.php"),
        "<?php\nnamespace Symfony\\Component\\Console\\Command;\nclass InvokableCommand {}\n",
    );
    index_source(
        &fixture.php,
        fixture.root.join("vendor/AsCommand.php"),
        "<?php\nnamespace Symfony\\Component\\Console\\Attribute;\nclass AsCommand {}\n",
    );
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "HTTPServer",
    )
    .unwrap();
    assert_eq!(result.namespace, "App\\Command");
    assert_eq!(result.class_name, "HTTPServerCommand");
    assert_eq!(
        result.file_uri,
        file_uri(&path_string(
            fixture.root.join("src/Command/HTTPServerCommand.php")
        ))
    );
    assert!(result.content.contains("#[AsCommand("));
    assert!(result.content.contains("name: 'app:http_server'"));
    assert!(result.content.contains("public function __invoke("));
    assert!(parses(&result.content));
}

#[test]
fn selects_attribute_templates() {
    let fixture = symfony_fixture();
    index_source(
        &fixture.php,
        fixture.root.join("vendor/Route.php"),
        "<?php\nnamespace Symfony\\Component\\Routing\\Attribute;\nclass Route {}\n",
    );
    index_source(
        &fixture.php,
        fixture.root.join("vendor/AsTwigFunction.php"),
        "<?php\nnamespace Twig\\Attribute;\nclass AsTwigFunction {}\nclass AsTwigFilter {}\n",
    );
    let controller = create(
        &fixture.provider,
        "controller",
        &fixture.root.join("src/Controller"),
        "Product",
    )
    .unwrap();
    assert_eq!(controller.class_name, "ProductController");
    assert!(
        controller
            .content
            .contains("use Symfony\\Component\\Routing\\Attribute\\Route;")
    );
    assert!(controller.content.contains("#[Route('/product')]"));

    let extension = create(
        &fixture.provider,
        "twig-extension",
        &fixture.root.join("src/Twig"),
        "ProductExtension",
    )
    .unwrap();
    assert!(
        extension
            .content
            .contains("#[AsTwigFunction('my_function')]")
    );
    assert!(extension.content.contains("#[AsTwigFilter('my_filter')]"));
    assert!(!extension.content.contains("extends AbstractExtension"));
}

#[test]
fn selects_historical_command_templates() {
    // configure
    let fixture = symfony_fixture();
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "Legacy",
    )
    .unwrap();
    assert!(result.content.contains("protected function configure()"));
    assert!(result.content.contains("->setName('app:legacy')"));

    // property
    let fixture = symfony_fixture();
    index_source(
        &fixture.php,
        fixture.root.join("vendor/Command.php"),
        "<?php\nnamespace Symfony\\Component\\Console\\Command;\nclass Command {\n    protected static $defaultName;\n}\n",
    );
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "Property",
    )
    .unwrap();
    assert!(
        result
            .content
            .contains("protected static $defaultName = 'app:property';")
    );
    assert!(!result.content.contains("#[AsCommand("));

    // attribute
    let fixture = symfony_fixture();
    index_source(
        &fixture.php,
        fixture.root.join("vendor/Command.php"),
        "<?php\nnamespace Symfony\\Component\\Console\\Command;\nclass Command {}\n",
    );
    index_source(
        &fixture.php,
        fixture.root.join("vendor/AsCommand.php"),
        "<?php\nnamespace Symfony\\Component\\Console\\Attribute;\nclass AsCommand {}\n",
    );
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "Attribute",
    )
    .unwrap();
    assert!(result.content.contains("#[AsCommand("));
    assert!(result.content.contains(" extends Command"));
    assert!(result.content.contains("protected function execute("));

    // invokable project preserving execute convention
    let fixture = symfony_fixture();
    for (path, source) in [
        (
            "vendor/Command.php",
            "<?php\nnamespace Symfony\\Component\\Console\\Command;\nclass Command {}\nclass InvokableCommand {}\n",
        ),
        (
            "vendor/AsCommand.php",
            "<?php\nnamespace Symfony\\Component\\Console\\Attribute;\nclass AsCommand {}\n",
        ),
        (
            "src/Command/ExistingCommand.php",
            "<?php\nnamespace App\\Command;\nclass ExistingCommand extends \\Symfony\\Component\\Console\\Command\\Command\n{\n    protected function execute(): int { return 0; }\n}\n",
        ),
    ] {
        index_source(&fixture.php, fixture.root.join(path), source);
    }
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "Convention",
    )
    .unwrap();
    assert!(result.content.contains("protected function execute("));
    assert!(!result.content.contains("public function __invoke("));
}

#[test]
fn reuses_indexed_command_prefix() {
    let mut fixture = symfony_fixture();
    let cache = TempDir::new().unwrap();
    let console = swlsp_console::Index::new(cache.path(), None).unwrap();
    index_source(
        &console,
        fixture.root.join("src/Command/ExistingCommand.php"),
        "<?php\nnamespace App\\Command;\n#[\\Symfony\\Component\\Console\\Attribute\\AsCommand(name: 'shopware:existing')]\nclass ExistingCommand {}\n",
    );
    fixture.provider.set_console(Some(Arc::new(console)));
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Command"),
        "CatalogSync",
    )
    .unwrap();
    assert!(
        result
            .content
            .contains("->setName('shopware:catalog_sync')")
    );
}

#[test]
fn generates_parseable_php_kinds() {
    let fixture = symfony_fixture();
    for (kind, directory, name, class_name) in [
        ("command", "src/Command", "CacheClear", "CacheClearCommand"),
        (
            "controller",
            "src/Controller",
            "Storefront",
            "StorefrontController",
        ),
        (
            "twig-extension",
            "src/Twig",
            "PriceExtension",
            "PriceExtension",
        ),
        (
            "compiler-pass",
            "src/DependencyInjection",
            "CollectServicesPass",
            "CollectServicesPass",
        ),
        (
            "kernel-test",
            "tests/Integration",
            "Container",
            "ContainerTest",
        ),
        (
            "web-test",
            "tests/Functional",
            "Storefront",
            "StorefrontTest",
        ),
    ] {
        let result = create(&fixture.provider, kind, &fixture.root.join(directory), name).unwrap();
        assert_eq!(result.class_name, class_name, "{kind}");
        assert_eq!(result.language, "php");
        assert!(parses(&result.content), "{kind}");
    }
}

#[test]
fn generates_project_aware_service_configurations() {
    let fixture = symfony_fixture();
    let config = fixture.root.join("config");
    let yaml = create(&fixture.provider, "services-yaml", &config, "services").unwrap();
    assert_eq!(yaml.language, "yaml");
    assert_eq!(
        yaml.file_uri,
        file_uri(&path_string(config.join("services.yaml")))
    );
    assert!(yaml.content.contains("  App\\:"));
    assert!(yaml.content.contains("resource: '../src/'"));
    assert!(swlsp_parser_yaml::parse(&yaml.content).errors.is_empty());

    let xml = create(&fixture.provider, "services-xml", &config, "services").unwrap();
    assert!(xml.content.contains(r#"namespace="App\""#));
    assert!(xml.content.contains(r#"resource="../src/""#));
    assert!(swlsp_parser_xml::parse(&xml.content).errors.is_empty());

    let php = create(&fixture.provider, "services-php", &config, "services").unwrap();
    assert!(
        php.content
            .contains("$services->load('App\\\\', '../src/')")
    );
    assert!(parses(&php.content));
}

#[test]
fn uses_most_specific_composer_mapping() {
    let fixture = symfony_fixture();
    let result = create(
        &fixture.provider,
        "command",
        &fixture.root.join("src/Feature/Command"),
        "Refresh",
    )
    .unwrap();
    assert_eq!(result.namespace, "App\\Feature\\Command");
}

#[test]
fn uses_custom_plugin_composer_mapping() {
    let root_dir = TempDir::new().unwrap();
    let root = root_dir.path();
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(
        root.join("composer.json"),
        r#"{"autoload":{"psr-4":{"Shopware\\":"src/"}}}"#,
    )
    .unwrap();
    let plugin_root = root.join("custom/plugins/FroshTools");
    let command_directory = plugin_root.join("src/Command");
    let config_directory = plugin_root.join("src/Resources/config");
    fs::create_dir_all(&command_directory).unwrap();
    fs::create_dir_all(&config_directory).unwrap();
    fs::write(
        plugin_root.join("composer.json"),
        r#"{
  "name": "frosh/tools",
  "autoload": {"psr-4": {"Frosh\\Tools\\": "src/"}},
  "autoload-dev": {"psr-4": {"Frosh\\Tools\\Tests\\": "tests/"}}
}"#,
    )
    .unwrap();
    let cache = TempDir::new().unwrap();
    let php = php_index(cache.path());
    php.configure_project(root).unwrap();
    let provider = Provider::new(&path_string(root), Some(php), None, None);

    let command = create(&provider, "command", &command_directory, "Refresh").unwrap();
    assert_eq!(command.namespace, "Frosh\\Tools\\Command");
    assert!(command.content.contains("namespace Frosh\\Tools\\Command;"));
    assert!(parses(&command.content));

    let services = create(&provider, "services-yaml", &config_directory, "services").unwrap();
    assert!(services.content.contains("  Frosh\\Tools\\:"));
    assert!(services.content.contains("resource: '../../'"));
    assert!(
        swlsp_parser_yaml::parse(&services.content)
            .errors
            .is_empty()
    );
}

#[test]
fn supports_global_composer_mapping() {
    let root_dir = TempDir::new().unwrap();
    let root = root_dir.path();
    fs::create_dir_all(root.join("src")).unwrap();
    fs::create_dir_all(root.join("config")).unwrap();
    fs::write(
        root.join("composer.json"),
        r#"{"autoload":{"psr-4":{"":"src/"}}}"#,
    )
    .unwrap();
    let cache = TempDir::new().unwrap();
    let php = php_index(cache.path());
    php.configure_project(root).unwrap();
    let provider = Provider::new(&path_string(root), Some(php), None, None);

    let class = create(&provider, "compiler-pass", &root.join("src"), "GlobalType").unwrap();
    assert!(class.namespace.is_empty());
    assert!(!class.content.contains("namespace ;"));
    assert!(parses(&class.content));

    let service = create(&provider, "services-yaml", &root.join("config"), "services").unwrap();
    assert!(service.content.contains("  '':"));
    assert!(swlsp_parser_yaml::parse(&service.content).errors.is_empty());
}

#[test]
fn rejects_unsafe_or_conflicting_targets() {
    let fixture = symfony_fixture();
    let command_directory = fixture.root.join("src/Command");
    fs::write(command_directory.join("ExistingCommand.php"), "<?php").unwrap();

    let error = create(&fixture.provider, "command", &command_directory, "Existing").unwrap_err();
    assert!(error.to_string().contains("already exists"), "{error}");
    let error = create(
        &fixture.provider,
        "command",
        &command_directory,
        "../Escape",
    )
    .unwrap_err();
    assert!(
        error.to_string().contains("invalid PHP class name"),
        "{error}"
    );
    let error = create(
        &fixture.provider,
        "not-a-scaffold",
        &command_directory,
        "Unknown",
    )
    .unwrap_err();
    assert!(
        error
            .to_string()
            .contains("unsupported Symfony scaffold kind")
    );
    let error = create(&fixture.provider, "form", &command_directory, "ProductType").unwrap_err();
    assert!(
        error
            .to_string()
            .contains("unsupported Symfony scaffold kind")
    );

    let outside = TempDir::new().unwrap();
    let error = create(&fixture.provider, "command", outside.path(), "Outside").unwrap_err();
    assert!(
        error.to_string().contains("outside the workspace"),
        "{error}"
    );

    let link = fixture.root.join("linked-outside");
    std::os::unix::fs::symlink(outside.path(), &link).unwrap();
    let error = create(&fixture.provider, "command", &link, "Outside").unwrap_err();
    assert!(
        error.to_string().contains("outside the workspace"),
        "{error}"
    );
}
