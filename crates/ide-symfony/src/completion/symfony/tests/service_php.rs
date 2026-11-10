//! Tests for service completion in PHP configuration.

use std::sync::Arc;

use swlsp_lsp::TextDocument;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_symfony::{PhpConfigReferenceKind, ServiceIndex};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

use super::*;
use crate::completion::symfony::ServiceCompletionProvider;

fn service_index(root: &Path, cache: &Path) -> Arc<ServiceIndex> {
    Arc::new(ServiceIndex::new(&p(root), cache, None).unwrap())
}

fn first_string(document: &TextDocument) -> SyntaxNode {
    phpquery::nodes(&document.root().unwrap(), &[PHP_STRING])
        .into_iter()
        .next()
        .expect("string literal")
}

fn labels_at(
    provider: &ServiceCompletionProvider,
    uri: &str,
    source: &str,
    pick: impl Fn(&TextDocument) -> SyntaxNode,
) -> Vec<String> {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let node = pick(&document);
    let offset = u32::from(node.text_range().start());
    let request = request_for(&document, Some(node), offset);
    labels(&completions(provider, &request))
}

#[test]
fn php_service_config_completions() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(&dir.path().join("php"), &[]);
    let services = service_index(&dir.path().join("root"), &dir.path().join("services"));
    index_file(
        services.as_ref(),
        "definitions.php",
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('app.target')->tag('app.tag');
    $container->parameters()->set('app.parameter', 'value');
};
",
    );
    let provider = ServiceCompletionProvider::new(Some(services.clone()), Some(php.clone()));
    for (source, label) in [
        (
            "<?php /* ContainerConfigurator */ service('');",
            "app.target",
        ),
        (
            "<?php /* ContainerConfigurator */ param('');",
            "app.parameter",
        ),
        (
            "<?php /* ContainerConfigurator */ tagged_iterator('');",
            "app.tag",
        ),
        (
            "<?php #[Autowire(service: '')] class Example {}",
            "app.target",
        ),
        (
            "<?php #[Autowire(param: '')] class Example {}",
            "app.parameter",
        ),
        (
            "<?php #[AutowireServiceClosure('')] class Example {}",
            "app.target",
        ),
        (
            "<?php #[AutowireMethodOf(service: '')] class Example {}",
            "app.target",
        ),
        (
            "<?php #[AutowireCallable(service: '')] class Example {}",
            "app.target",
        ),
        (
            "<?php #[AutowireLocator(services: [''])] class Example {}",
            "app.target",
        ),
        (
            "<?php #[AutowireLocator(exclude: '')] class Example {}",
            "app.target",
        ),
        ("<?php #[TaggedIterator('')] class Example {}", "app.tag"),
        (
            "<?php #[TaggedLocator(tag: '')] class Example {}",
            "app.tag",
        ),
        (
            "<?php #[Autoconfigure(tags: [''])] class Example {}",
            "app.tag",
        ),
        (
            "<?php #[AutoconfigureTag(name: '')] class Example {}",
            "app.tag",
        ),
    ] {
        let labels = labels_at(
            &provider,
            "file:///project/config/services.php",
            source,
            first_string,
        );
        assert!(labels.contains(&label.to_string()), "{source}: {labels:?}");
    }

    // typed container get
    let source = r"<?php
use Psr\Container\ContainerInterface;
function resolve(ContainerInterface $container): void {
    $container->get('');
}";
    let labels = labels_at(
        &provider,
        "file:///project/resolve.php",
        source,
        first_string,
    );
    assert!(labels.contains(&"app.target".to_string()));

    for interface in ["ParameterBagInterface", "ContainerBagInterface"] {
        for method in ["get", "has"] {
            let source = format!(
                r"<?php /* ContainerConfigurator */
use Symfony\Component\DependencyInjection\ParameterBag\{interface};
final class Resolver {{
    public function __construct(private {interface} $bag) {{}}
    public function resolve(): void {{
        $this->bag->{method}('');
    }}
}}"
            );
            let labels = labels_at(
                &provider,
                "file:///project/resolve.php",
                &source,
                first_string,
            );
            assert!(
                labels.contains(&"app.parameter".to_string()),
                "{interface} {method}"
            );
            assert!(
                !labels.contains(&"app.target".to_string()),
                "{interface} {method}"
            );
        }
    }

    for (import, short, method) in [
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
            "<?php\nuse {import};\nfunction resolve({short} $parameters): void {{\n    $parameters->{method}('');\n}}"
        );
        let labels = labels_at(
            &provider,
            "file:///project/resolve.php",
            &source,
            first_string,
        );
        assert!(labels.contains(&"app.parameter".to_string()), "{method}");
        assert!(!labels.contains(&"app.target".to_string()), "{method}");
    }
}

#[test]
fn phpdoc_service_assistant_tag_completes_call_arguments() {
    let dir = tempfile::tempdir().unwrap();
    let cache = dir.path();
    let services = service_index(cache, &cache.join("services"));
    index_file(
        services.as_ref(),
        &p(&cache.join("services.xml")),
        r#"<container><services>
<service id="app.target" class="App\Target"/>
</services></container>"#,
    );
    let php = php_index(
        &cache.join("php"),
        &[(
            &p(&cache.join("ServiceAware.php")),
            r"<?php
interface ServiceAware
{
    /** @param string $service #Service */
    public function connect(string $service): void;
}

class ServiceConsumer implements ServiceAware
{
    /** @param string $service #Service */
    public function __construct(string $service) {}

    public function connect(string $service): void {}

    /** @param string $value #Route */
    public function route(string $value): void {}
}

/** @param string $service #Service */
function resolve_service(string $service): void {}
",
        )],
    );
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));
    for (name, source, found) in [
        (
            "named constructor argument",
            "<?php new ServiceConsumer(service: 'app.<caret>');",
            true,
        ),
        (
            "inherited method contract",
            "<?php\n$consumer = new ServiceConsumer('app.seed');\n$consumer->connect('app.<caret>');\n",
            true,
        ),
        ("function", "<?php resolve_service('app.<caret>');", true),
        (
            "different assistant tag",
            "<?php\n$consumer = new ServiceConsumer('app.seed');\n$consumer->route('app.<caret>');\n",
            false,
        ),
    ] {
        let (source, offset) = completion_caret(source);
        let (document, request) = bundle_request("/project/src/Usage.php", &source, offset);
        let items = completions(&provider, &request);
        if !found {
            assert!(
                !labels(&items).contains(&"app.target".to_string()),
                "{name}"
            );
            continue;
        }
        let item = require_completion(&items, "app.target");
        assert_eq!(item.detail, r"App\Target", "{name}");
        let edit = edit(&item);
        assert_eq!(edit.new_text, "app.target");
        assert_eq!(range_text(&document, edit.range), "app.", "{name}");
    }
}

#[test]
fn phpdoc_parameter_assistant_tag_completes_call_arguments() {
    let dir = tempfile::tempdir().unwrap();
    let cache = dir.path();
    let services = service_index(cache, &cache.join("services"));
    index_file(
        services.as_ref(),
        &p(&cache.join("parameters.php")),
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->parameters()->set('app.feature', true);
};",
    );
    let php = php_index(
        &cache.join("php"),
        &[(
            &p(&cache.join("ParameterAware.php")),
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
/** @param string $name #Parameter */
function resolve_parameter(string $name): void {}
",
        )],
    );
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));
    for (name, source, found) in [
        (
            "named function argument",
            "<?php resolve_parameter(name: 'app.<caret>');",
            true,
        ),
        (
            "inherited method contract",
            "<?php\n$consumer = new ParameterConsumer();\n$consumer->read('app.<caret>');\n",
            true,
        ),
        (
            "different assistant tag",
            "<?php\n$consumer = new ParameterConsumer();\n$consumer->service('app.<caret>');\n",
            false,
        ),
    ] {
        let (source, offset) = completion_caret(source);
        let (document, request) = bundle_request("/project/src/Usage.php", &source, offset);
        let items = completions(&provider, &request);
        if !found {
            assert!(
                !labels(&items).contains(&"app.feature".to_string()),
                "{name}"
            );
            continue;
        }
        let item = require_completion(&items, "app.feature");
        assert_eq!(item.kind, kind(protocol::CompletionItemKind::REFERENCE));
        let edit = edit(&item);
        assert_eq!(edit.new_text, "app.feature");
        assert_eq!(range_text(&document, edit.range), "app.", "{name}");
    }
}

#[test]
fn autowire_callable_method_completions() {
    let dir = tempfile::tempdir().unwrap();
    let cache = dir.path();
    let php = php_index(
        &cache.join("php"),
        &[(
            &p(&cache.join("Formatter.php")),
            r"<?php
namespace App;
class Formatter {
    public function format(): string { return ''; }
    public function process(): void {}
    private function privateMethod(): void {}
    public static function staticMethod(): void {}
}
",
        )],
    );
    let services = service_index(cache, &cache.join("services"));
    index_file(
        services.as_ref(),
        &p(&cache.join("services.xml")),
        r#"<container><services>
<service id="app.formatter" class="App\Formatter"/>
<service id="app.formatter_alias" alias="app.formatter"/>
</services></container>"#,
    );
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));
    for source in [
        "<?php #[AutowireCallable(service: 'app.formatter_alias', method: '')] class Consumer {}",
        r"<?php
use App\Formatter as TargetFormatter;
#[AutowireCallable(service: TargetFormatter::class, method: '')]
class Consumer {}",
    ] {
        let labels = labels_at(
            &provider,
            "file:///project/src/Consumer.php",
            source,
            |document| {
                phpquery::nodes(&document.root().unwrap(), &[PHP_STRING])
                    .into_iter()
                    .find(|literal| phpquery::string_value(literal).is_empty())
                    .expect("empty literal")
            },
        );
        assert!(labels.contains(&"format".to_string()), "{labels:?}");
        assert!(labels.contains(&"process".to_string()));
        assert!(!labels.contains(&"privateMethod".to_string()));
        assert!(!labels.contains(&"staticMethod".to_string()));
    }
}

#[test]
fn when_attribute_environment_completions() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(&dir.path().join("php"), &[]);
    let services = service_index(dir.path(), &dir.path().join("services"));
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));
    for source in [
        "<?php #[When('')] class Example {}",
        "<?php #[When(env: '')] class Example {}",
    ] {
        let mut labels = labels_at(
            &provider,
            "file:///project/src/Example.php",
            source,
            first_string,
        );
        labels.sort();
        assert_eq!(labels, ["dev", "never", "prod", "test"]);
    }
}

#[test]
fn php_array_service_parameter_completions() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/src/Target.php",
            r"<?php namespace App;
class Target {
    public function configure(): void {}
    public static function create(): self { return new self(); }
    private function hidden(): void {}
}",
        )],
    );
    let services = service_index(dir.path(), &dir.path().join("services"));
    index_file(
        services.as_ref(),
        "parameters.php",
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->parameters()->set('app.parameter', true);
    $container->services()->set('app.target', \App\Target::class)->tag('app.tag');
};",
    );
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));

    for source in [
        "<?php return ['services' => [\n    'app.consumer' => ['arguments' => ['%app.']],\n]];",
        "<?php return ['services' => [\n    'app.consumer' => ['calls' => [['configure', ['%app.']]]],\n]];",
    ] {
        let document = Arc::new(TextDocument::new(
            "file:///project/config/services.php",
            source,
            1,
        ));
        let offset = (source.find("%app.").unwrap() + "%app.".len()) as u32;
        let node = document.root().unwrap().node_at(TextSize::from(offset));
        let request = request_for(&document, node, offset);
        let items = completions(&provider, &request);
        assert_eq!(items.len(), 1, "{:?}", labels(&items));
        assert_eq!(items[0].label, "%app.parameter%");
        assert_eq!(edit(&items[0]).new_text, "%app.parameter%");
    }

    for (source, label, prohibited) in [
        (
            "<?php return ['services' => [\n    'app.consumer' => ['parent' => ''],\n]];",
            "app.target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => ['arguments' => ['@']],\n]];",
            "app.target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => ['tags' => ['']],\n]];",
            "app.tag",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => ['class' => ''],\n]];",
            r"App\Target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.alias' => '@',\n]];",
            "app.target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => ['factory' => ['@', 'create']],\n]];",
            "app.target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => ['arguments' => [service('')]],\n]];",
            "app.target",
            "",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => [\n        'class' => 'App\\\\Target',\n        'calls' => [['', []]],\n    ],\n]];",
            "configure",
            "create",
        ),
        (
            "<?php return ['services' => [\n    'app.consumer' => [\n        'factory' => ['@app.target', ''],\n    ],\n]];",
            "create",
            "",
        ),
    ] {
        let labels = labels_at(
            &provider,
            "file:///project/config/services.php",
            source,
            |document| {
                let root = document.root().unwrap();
                let mut target = None;
                for literal in phpquery::nodes(&root, &[PHP_STRING]) {
                    if swlsp_symfony::php_array_service_reference_at(&literal)
                        != PhpConfigReferenceKind::None
                        || swlsp_symfony::php_config_reference_at(&literal)
                            != PhpConfigReferenceKind::None
                    {
                        target = Some(literal.clone());
                    }
                    if (label == "configure" || label == "create")
                        && swlsp_symfony::php_array_service_method_at(&root, &literal).is_some()
                    {
                        target = Some(literal);
                    }
                }
                target.expect("target literal")
            },
        );
        assert!(labels.contains(&label.to_string()), "{source}: {labels:?}");
        if !prohibited.is_empty() {
            assert!(!labels.contains(&prohibited.to_string()), "{source}");
        }
    }
}
