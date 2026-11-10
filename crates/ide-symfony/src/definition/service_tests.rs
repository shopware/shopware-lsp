use std::path::Path;
use std::sync::Arc;

use swlsp_lsp::{DefinitionRequest, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_MEMBER_ACCESS, PHP_STRING};
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

const PHP_CONFIG_URI: &str = "file:///project/config/services.php";

fn service_index(root: &Path, cache: &Path) -> Arc<ServiceIndex> {
    Arc::new(ServiceIndex::new(&p(root), cache, None).unwrap())
}

/// A PHP config document request with an explicit node.
fn php_request(document: &Arc<TextDocument>, node: SyntaxNode) -> DefinitionRequest {
    request_with_node(document, Some(node), 0)
}

fn strings(document: &TextDocument) -> Vec<SyntaxNode> {
    phpquery::nodes(&document.root().unwrap(), &[PHP_STRING])
}

fn string_with_value(document: &TextDocument, value: &str) -> SyntaxNode {
    strings(document)
        .into_iter()
        .find(|literal| phpquery::string_value(literal) == value)
        .unwrap_or_else(|| panic!("no string {value}"))
}

struct ConfigFixture {
    _dir: tempfile::TempDir,
    service_path: String,
    class_path: String,
    provider: ServiceDefinitionProvider,
}

fn config_fixture() -> ConfigFixture {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(&dir.path().join("php"));
    let services = service_index(&dir.path().join("root"), &dir.path().join("services"));
    let service_path = p(&dir.path().join("definitions.php"));
    index(
        services.as_ref(),
        &service_path,
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->services()->set('app.target', \App\Target::class)->tag('app.tag');
    $container->parameters()->set('app.parameter', 'value');
};
",
    );
    let class_path = p(&dir.path().join("Target.php"));
    index(
        &php,
        &class_path,
        r"<?php namespace App;
final class Target {
    public function configure(): void {}
    public static function create(): self { return new self(); }
}",
    );
    ConfigFixture {
        _dir: dir,
        service_path,
        class_path,
        provider: ServiceDefinitionProvider::new(Some(services), Some(php)),
    }
}

fn assert_target(
    fixture: &ConfigFixture,
    source: &str,
    node: impl Fn(&TextDocument) -> SyntaxNode,
    expected: &str,
) {
    let document = document(PHP_CONFIG_URI, source);
    let locations = definitions(&fixture.provider, &php_request(&document, node(&document)));
    assert_eq!(locations.len(), 1, "{source}");
    assert_eq!(locations[0].uri, file_uri(expected), "{source}");
}

fn first_string(document: &TextDocument) -> SyntaxNode {
    strings(document).remove(0)
}

#[test]
fn php_service_config_definitions() {
    let fixture = config_fixture();
    let service_path = fixture.service_path.clone();
    let class_path = fixture.class_path.clone();

    // service helper
    assert_target(
        &fixture,
        "<?php /* ContainerConfigurator */ service('app.target');",
        |document| {
            let call = phpquery::calls(&document.root().unwrap(), &[]).remove(0);
            phpquery::string_argument(&call, 0).unwrap()
        },
        &service_path,
    );
    // class constant
    assert_target(
        &fixture,
        r"<?php
use App\Target;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
service(Target::class);
",
        |document| phpquery::nodes(&document.root().unwrap(), &[PHP_MEMBER_ACCESS]).remove(0),
        &class_path,
    );
    for source in [
        "<?php #[Autowire(service: 'app.target')] class Example {}",
        "<?php #[Autowire(param: 'app.parameter')] class Example {}",
        "<?php #[AutowireServiceClosure('app.target')] class Example {}",
        "<?php #[AutowireMethodOf(service: 'app.target')] class Example {}",
        "<?php #[AutowireCallable(service: 'app.target')] class Example {}",
        "<?php #[AutowireLocator(services: ['app.target'])] class Example {}",
        "<?php /* ContainerConfigurator */ tagged_iterator('app.tag');",
        "<?php #[TaggedIterator('app.tag')] class Example {}",
        "<?php #[TaggedLocator(tag: 'app.tag')] class Example {}",
        "<?php #[Autoconfigure(tags: ['app.tag'])] class Example {}",
        "<?php #[AutoconfigureTag(name: 'app.tag')] class Example {}",
    ] {
        assert_target(&fixture, source, first_string, &service_path);
    }

    // typed container get
    assert_target(
        &fixture,
        r"<?php
use Psr\Container\ContainerInterface;
function resolve(ContainerInterface $container): void {
    $container->get('app.target');
}",
        first_string,
        &service_path,
    );
    for interface_name in ["ParameterBagInterface", "ContainerBagInterface"] {
        for method_name in ["get", "has"] {
            let source = format!(
                r"<?php /* ContainerConfigurator */
use Symfony\Component\DependencyInjection\ParameterBag\{interface_name};
final class Resolver {{
    public function __construct(private {interface_name} $bag) {{}}
    public function resolve(): void {{
        $this->bag->{method_name}('app.parameter');
    }}
}}"
            );
            assert_target(&fixture, &source, first_string, &service_path);
        }
    }
    for (import_name, short_name, method) in [
        (
            r"Symfony\Component\DependencyInjection\ContainerInterface",
            "ContainerInterface",
            "getParameter",
        ),
        (
            r"Symfony\Component\DependencyInjection\ContainerInterface",
            "ContainerInterface",
            "hasParameter",
        ),
        (
            r"Symfony\Component\DependencyInjection\ParameterBag\ParameterBagInterface",
            "ParameterBagInterface",
            "set",
        ),
        (
            r"Symfony\Component\DependencyInjection\Loader\Configurator\ParametersConfigurator",
            "ParametersConfigurator",
            "set",
        ),
    ] {
        let source = format!(
            "<?php\nuse {import_name};\nfunction resolve({short_name} $parameters): void {{\n    $parameters->{method}('app.parameter');\n}}"
        );
        assert_target(&fixture, &source, first_string, &service_path);
    }

    for source in [
        r"<?php return ['services' => [
    'app.consumer' => ['arguments' => ['%app.parameter%']],
]];",
        r"<?php return ['services' => [
    'app.consumer' => ['calls' => [['configure', ['%app.parameter%']]]],
]];",
    ] {
        assert_target(
            &fixture,
            source,
            |document| string_with_value(document, "%app.parameter%"),
            &service_path,
        );
    }

    for (name, source) in [
        (
            "parent",
            r"<?php return ['services' => [
    'app.consumer' => ['parent' => 'app.target'],
]];",
        ),
        (
            "argument service",
            r"<?php return ['services' => [
    'app.consumer' => ['arguments' => ['@app.target']],
]];",
        ),
        (
            "tag",
            r"<?php return ['services' => [
    'app.consumer' => ['tags' => ['app.tag']],
]];",
        ),
        (
            "class string",
            r"<?php return ['services' => [
    'app.consumer' => ['class' => 'App\\Target'],
]];",
        ),
        (
            "service-level alias",
            r"<?php return ['services' => [
    'app.alias' => '@app.target',
]];",
        ),
        (
            "factory service",
            r"<?php return ['services' => [
    'app.consumer' => ['factory' => ['@app.target', 'create']],
]];",
        ),
        (
            "service helper",
            r"<?php return ['services' => [
    'app.consumer' => ['arguments' => [service('app.target')]],
]];",
        ),
        (
            "call method",
            r"<?php return ['services' => [
    'app.consumer' => [
        'class' => 'App\\Target',
        'calls' => [['configure', []]],
    ],
]];",
        ),
        (
            "factory method",
            r"<?php return ['services' => [
    'app.consumer' => [
        'factory' => ['@app.target', 'create'],
    ],
]];",
        ),
    ] {
        let expected = if matches!(name, "class string" | "call method" | "factory method") {
            &class_path
        } else {
            &service_path
        };
        assert_target(
            &fixture,
            source,
            |document| {
                let root = document.root().unwrap();
                let mut node = None;
                for literal in strings(document) {
                    if swlsp_symfony::php_array_service_reference_at(&literal)
                        != PhpConfigReferenceKind::None
                        || swlsp_symfony::php_config_reference_at(&literal)
                            != PhpConfigReferenceKind::None
                    {
                        node = Some(literal.clone());
                    }
                    if matches!(name, "call method" | "factory method")
                        && swlsp_symfony::php_array_service_method_at(&root, &literal).is_some()
                    {
                        node = Some(literal);
                    }
                }
                node.unwrap()
            },
            expected,
        );
    }
}

#[test]
fn php_doc_service_assistant_tag_definitions() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(&cache.path().join("php"));
    let services = service_index(cache.path(), &cache.path().join("services"));
    let service_path = p(&cache.path().join("services.xml"));
    index(
        services.as_ref(),
        &service_path,
        r#"<container><services>
<service id="app.target" class="App\Target"/>
</services></container>"#,
    );
    index(
        &php,
        &p(&cache.path().join("ServiceAware.php")),
        r"<?php
interface ServiceAware
{
    /** @param string $service #Service */
    public function connect(string $service): void;
}

class ServiceConsumer implements ServiceAware
{
    public function connect(string $service): void {}

    /** @param string $value #Route */
    public function route(string $value): void {}
}

/** @param string $service #Service */
function resolve_service(string $service): void {}
",
    );
    let provider = ServiceDefinitionProvider::new(Some(services), Some(php));
    for (source, has_target) in [
        ("<?php resolve_service('app.target');", true),
        (
            "<?php
$consumer = new ServiceConsumer();
$consumer->connect('app.target');
",
            true,
        ),
        (
            "<?php
$consumer = new ServiceConsumer();
$consumer->route('app.target');
",
            false,
        ),
    ] {
        let document = document("file:///project/src/Usage.php", source);
        let node = string_with_value(&document, "app.target");
        let locations = definitions(&provider, &php_request(&document, node));
        if !has_target {
            assert!(locations.is_empty(), "{source}");
            continue;
        }
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(&service_path));
    }
}

#[test]
fn php_doc_parameter_assistant_tag_definitions() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(&cache.path().join("php"));
    let services = service_index(cache.path(), &cache.path().join("services"));
    let parameter_path = p(&cache.path().join("parameters.php"));
    index(
        services.as_ref(),
        &parameter_path,
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->parameters()->set('app.feature', true);
};",
    );
    index(
        &php,
        &p(&cache.path().join("ParameterAware.php")),
        r"<?php
interface ParameterAware
{
    /** @param string $name #Parameter */
    public function read(string $name): void;
}
class ParameterConsumer implements ParameterAware
{
    public function read(string $name): void {}

    /** @param string $service #Service */
    public function service(string $service): void {}
}
",
    );
    let provider = ServiceDefinitionProvider::new(Some(services), Some(php));
    for (method, has_target) in [("read", true), ("service", false)] {
        let source = format!(
            "<?php\n$consumer = new ParameterConsumer();\n$consumer->{method}('app.feature');\n"
        );
        let document = document("file:///project/src/Usage.php", &source);
        let locations = definitions(&provider, &php_request(&document, first_string(&document)));
        if !has_target {
            assert!(locations.is_empty(), "{method}");
            continue;
        }
        assert_eq!(locations.len(), 1, "{method}");
        assert_eq!(locations[0].uri, file_uri(&parameter_path));
    }
}

#[test]
fn autowire_callable_method_definitions() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(&cache.path().join("php"));
    let services = service_index(cache.path(), &cache.path().join("services"));
    let class_path = cache.path().join("Formatter.php");
    let class_source = r"<?php
namespace App;
class Formatter {
    public function format(): string { return ''; }
    private function privateMethod(): void {}
    public static function staticMethod(): void {}
}
";
    write(&class_path, class_source);
    index(&php, &p(&class_path), class_source);
    index(
        services.as_ref(),
        &p(&cache.path().join("services.xml")),
        r#"<container><services>
<service id="app.formatter" class="App\Formatter"/>
</services></container>"#,
    );
    let provider = ServiceDefinitionProvider::new(Some(services), Some(php));
    for source in [
        "<?php #[AutowireCallable(service: 'app.formatter', method: 'format')] class Consumer {}",
        r"<?php
use App\Formatter as TargetFormatter;
#[AutowireCallable(service: TargetFormatter::class, method: 'format')]
class Consumer {}",
    ] {
        let document = document(PHP_CONFIG_URI, source);
        let node = string_with_value(&document, "format");
        let locations = definitions(&provider, &php_request(&document, node));
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(&p(&class_path)));
    }
    for method in ["privateMethod", "staticMethod"] {
        let source = format!(
            "<?php #[AutowireCallable(service: 'app.formatter', method: '{method}')] class Consumer {{}}"
        );
        let document = document(PHP_CONFIG_URI, &source);
        let node = strings(&document).remove(1);
        assert!(
            definitions(&provider, &php_request(&document, node)).is_empty(),
            "{method}"
        );
    }
}

// -- configured service methods -----------------------------------------------

/// The source without its `<caret>` marker, and the marker's byte offset.
fn caret(source: &str) -> (String, usize) {
    let offset = index_of(source, "<caret>");
    (source.replacen("<caret>", "", 1), offset)
}

#[test]
fn configured_service_method_definition_across_yaml_and_xml() {
    let root = tempfile::tempdir().unwrap();
    let php_path = root.path().join("src/Configured.php");
    let php_source = r"<?php
namespace App;
class ParentConfigured
{
    public function inherited(): void {}
}
class Configured extends ParentConfigured
{
    public function setLogger(): void {}
    public function onKernelRequest(): void {}
}
class Builder
{
    public function create(): Product { return new Product(); }
    public static function build(): Product { return new Product(); }
}
class Product {}
";
    write(&php_path, php_source);
    let php = php_index(&root.path().join(".cache"));
    index(&php, &p(&php_path), php_source);
    let provider = ServiceDefinitionProvider::new(None, Some(php));

    for (name, file, source, method) in [
        (
            "YAML inherited call",
            "services.yaml",
            r"services:
  app.consumer:
    class: App\Configured
    calls:
      - [inher<caret>ited, []]
",
            "inherited",
        ),
        (
            "YAML service factory",
            "services.yaml",
            r"services:
  app.builder:
    class: App\Builder
  app.product:
    class: App\Product
    factory: ['@app.builder', cre<caret>ate]
",
            "create",
        ),
        (
            "YAML class factory string",
            "services.yaml",
            r"services:
  app.product:
    class: App\Product
    factory: 'App\Builder::bu<caret>ild'
",
            "build",
        ),
        (
            "XML call",
            "services.xml",
            r#"<container><services>
  <service id="app.consumer" class="App\Configured">
    <call method="setLog<caret>ger"/>
  </service>
</services></container>"#,
            "setLogger",
        ),
        (
            "XML tag callback",
            "services.xml",
            r#"<container><services>
  <service id="app.consumer" class="App\Configured">
    <tag name="kernel.event_listener" method="onKernel<caret>Request"/>
  </service>
</services></container>"#,
            "onKernelRequest",
        ),
        (
            "XML service factory",
            "services.xml",
            r#"<container><services>
  <service id="app.builder" class="App\Builder"/>
  <service id="app.product" class="App\Product">
    <factory service="app.builder" method="cre<caret>ate"/>
  </service>
</services></container>"#,
            "create",
        ),
    ] {
        let (source, offset) = caret(source);
        let document = document(
            &file_uri(&p(&root.path().join("config").join(file))),
            &source,
        );
        let locations = definitions(&provider, &request_at(&document, offset));
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&p(&php_path)), "{name}");
        assert_eq!(location_text(php_source, &locations[0]), method, "{name}");
    }
}

// -- YAML named arguments and default bindings ---------------------------------

fn yaml_request(source: &str, offset: usize, uri: &str) -> (Arc<TextDocument>, DefinitionRequest) {
    let document = document(uri, source);
    let request = request_at(&document, offset);
    (document, request)
}

#[test]
fn yaml_service_named_argument_definition() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(&dir.path().join("cache"));
    let php_path = dir.path().join("Consumer.php");
    let php_source = "<?php
namespace App;
class ParentConsumer {
    public function __construct(string $logger, int $count = 0) {}
}
\tclass ChildConsumer extends ParentConsumer {}
";
    write(&php_path, php_source);
    index(&php, &p(&php_path), php_source);
    let provider = ServiceDefinitionProvider::new(None, Some(php));

    let source = r"services:
  app.consumer:
    class: App\ChildConsumer
    arguments:
      $logger: '@logger'";
    let offset = index_of(source, "$logger") + 3;
    let (_document, request) = yaml_request(source, offset, "file:///project/config/services.yaml");
    let locations = definitions(&provider, &request);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&php_path)));
    let lines = swlsp_syntax::LineIndex::new(php_source.to_owned());
    let (line, character) = lines.position_utf16(index_of(php_source, "$logger") as u32);
    assert_eq!(locations[0].range.start.line, line);
    assert_eq!(locations[0].range.start.character, character);
}

#[test]
fn yaml_service_named_argument_definition_ignores_unknown_and_factory() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(dir.path());
    index(
        &php,
        "/project/src/Consumer.php",
        r"<?php
namespace App;
class Consumer {
    public function __construct(string $logger) {}
}",
    );
    let provider = ServiceDefinitionProvider::new(None, Some(php));
    for source in [
        r"services:
  app.consumer:
    class: App\Consumer
    arguments:
      $missing: value",
        r"services:
  app.consumer:
    class: App\Consumer
    factory: ['@factory', create]
    arguments:
      $logger: value",
    ] {
        let offset = last_index_of(source, "$") + 2;
        let (_document, request) =
            yaml_request(source, offset, "file:///project/config/services.yaml");
        assert!(definitions(&provider, &request).is_empty(), "{source}");
    }
}

#[test]
fn yaml_service_default_binding_definition() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(&dir.path().join("cache"));
    let php_path = dir.path().join("Consumers.php");
    let php_source = r"<?php
namespace Psr\Log;
interface LoggerInterface {}

namespace BindArgument;
class Consumer {
    public function __construct(
        $proxyUrl,
        string $defaultUri,
        iterable $rules,
        \Psr\Log\LoggerInterface $logger,
    ) {}
}
class MismatchConsumer {
    public function __construct(int $defaultUri) {}
}
";
    write(&php_path, php_source);
    index(&php, &p(&php_path), php_source);
    let provider = ServiceDefinitionProvider::new(None, Some(php));

    for (name, key, service, parameter, expect_matches) in [
        (
            "name only",
            "$proxyUrl",
            r"BindArgument\Consumer",
            "$proxyUrl",
            1,
        ),
        (
            "scalar type",
            "string $defaultUri",
            r"BindArgument\Consumer",
            "$defaultUri",
            1,
        ),
        (
            "iterable type",
            "iterable $rules",
            r"BindArgument\Consumer",
            "$rules",
            1,
        ),
        (
            "object type",
            r"Psr\Log\LoggerInterface $logger",
            r"BindArgument\Consumer",
            "$logger",
            1,
        ),
        (
            "type mismatch",
            "string $defaultUri",
            r"BindArgument\MismatchConsumer",
            "$defaultUri",
            0,
        ),
    ] {
        let source =
            format!("services:\n  _defaults:\n    bind:\n      {key}: value\n  {service}: ~\n");
        let offset = index_of(&source, "$") + 2;
        let (_document, request) =
            yaml_request(&source, offset, "file:///project/config/services.yaml");
        let locations = definitions(&provider, &request);
        assert_eq!(locations.len(), expect_matches, "{name}");
        if expect_matches == 0 {
            continue;
        }
        assert_eq!(locations[0].uri, file_uri(&p(&php_path)));
        let lines = swlsp_syntax::LineIndex::new(php_source.to_owned());
        let (line, character) = lines.position_utf16(index_of(php_source, parameter) as u32);
        assert_eq!(locations[0].range.start.line, line, "{name}");
        assert_eq!(locations[0].range.start.character, character, "{name}");
    }
}

#[test]
fn yaml_service_default_binding_definition_expands_prototype_services() {
    let project_root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(&cache.path().join("php"));
    let php_path = project_root.path().join("src/PrototypeConsumer.php");
    let php_source = r"<?php
namespace App;
class PrototypeConsumer {
    public function __construct(string $projectDir) {}
}";
    write(&php_path, php_source);
    index(&php, &p(&php_path), php_source);

    let services = service_index(project_root.path(), &cache.path().join("services"));
    services.set_php_index(Some(php.clone()));
    let provider = ServiceDefinitionProvider::new(Some(services), Some(php));

    let config_path = project_root.path().join("config/services.yaml");
    let source = r"services:
  _defaults:
    bind:
      string $projectDir: '%kernel.project_dir%'
  'App\':
    resource: '../src'
";
    let offset = index_of(source, "$projectDir") + 2;
    let (_document, request) = yaml_request(source, offset, &file_uri(&p(&config_path)));
    let locations = definitions(&provider, &request);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&p(&php_path)));
}
