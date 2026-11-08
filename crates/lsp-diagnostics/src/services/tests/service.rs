use std::collections::HashMap;

use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};

use super::*;
use crate::services::ServiceAnalyzer;

const MISSING_SERVICE: &str = "symfony.service.missing";
const MISSING_PARAMETER: &str = "symfony.parameter.missing";
const MISSING_CLASS: &str = "symfony.class.missing";
const DEPRECATED_SERVICE: &str = "symfony.service.deprecated";
const DEPRECATED_CLASS: &str = "symfony.class.deprecated";

struct Fixture {
    _dir: tempfile::TempDir,
    analyzer: ServiceAnalyzer,
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/Existing.php",
            r"<?php
namespace App;
class Existing {}
interface ExistingInterface {}
/** @deprecated Use Modern instead. */
class Legacy {}

/** @param string $service #Service */
function resolve_service(string $service): void {}
/** @param string $parameter #Parameter */
function resolve_parameter(string $parameter): void {}
/** @param string $class #Class */
function resolve_class(string $class): void {}
/** @param string $interface #Interface */
function resolve_interface(string $interface): void {}
/** @param string $type #ClassInterface */
function resolve_class_or_interface(string $type): void {}
",
        )],
    );
    let services = service_index(
        &dir.path().join("services"),
        Some(&php),
        &[(
            "/project/indexed_services.yaml",
            r"parameters:
  app.existing: value
  app.parameter: value
services:
  app.existing:
    class: App\Existing
  app.service:
    class: App\Existing
  app.deprecated:
    class: App\Legacy
    deprecated: 'The %service_id% service is deprecated; use app.existing.'
",
        )],
    );
    Fixture {
        _dir: dir,
        analyzer: ServiceAnalyzer::new(Some(services), Some(php)),
    }
}

#[test]
fn service_diagnostics_for_yaml_and_xml() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/services.yaml",
            r"parameters:
  local.parameter: value
services:
  App\Local:
    class: App\Existing
    arguments:
      - '@app.existing'
      - '%app.existing%'
      - '@app.missing'
      - '%app.missing%'
      - '@?app.optional'
  app.class_check:
    class: App\Missing
",
        ),
        (
            "file:///project/services.xml",
            r#"<container>
<parameters><parameter key="local.parameter">value</parameter></parameters>
<services>
  <service id="local.service" class="App\Existing">
    <argument type="service" id="app.existing"/>
    <argument>%app.existing%</argument>
    <argument type="service" id="app.missing"/>
    <argument>%app.missing%</argument>
    <argument type="service" id="app.optional" on-invalid="ignore"/>
  </service>
  <alias id="local.alias" service="local.service"/>
  <service id="class.check" class="App\Missing"/>
</services>
</container>"#,
        ),
    ];
    for (uri, source) in tests {
        let result = analyze(&fixture.analyzer, &document(uri, source));
        assert_codes(
            &result,
            &[MISSING_SERVICE, MISSING_PARAMETER, MISSING_CLASS],
        );
    }
}

#[test]
fn service_diagnostics_for_php_attributes_and_typed_container() {
    let fixture = fixture();
    let document = document(
        "file:///project/Example.php",
        r"<?php
namespace App;
use Psr\Container\ContainerInterface;

class Other { public function get(string $id): mixed {} }

class Example {
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(service: 'app.missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(param: 'app.missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireServiceClosure('app.closure_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireMethodOf(service: 'app.method_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireCallable(service: 'app.callable_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireLocator(
        services: ['app.locator_missing', 'app.existing'],
        exclude: 'app.exclude_missing',
    )]
    public function run(ContainerInterface $container, Other $other): void {
        $container->get('app.missing');
        $container->get('app.existing');
        $other->get('ignored.missing');
    }
}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_codes(
        &result,
        &[
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_SERVICE,
            MISSING_PARAMETER,
        ],
    );
    for problem in &result {
        assert!(!problem.message.contains("ignored.missing"));
    }
}

#[test]
fn service_diagnostics_distinguish_parameter_bags_from_service_containers() {
    let fixture = fixture();
    let document = document(
        "file:///project/Example.php",
        r"<?php /* ContainerConfigurator */
use Psr\Container\ContainerInterface;
use Symfony\Component\DependencyInjection\ContainerInterface as DIContainer;
use Symfony\Component\DependencyInjection\ParameterBag\ContainerBagInterface;
use Symfony\Component\DependencyInjection\ParameterBag\ParameterBagInterface;

function resolve(
    ParameterBagInterface $parameters,
    ContainerBagInterface $containerBag,
    ContainerInterface $services,
    DIContainer $diContainer,
): void {
    $parameters->get('app.service');
    $containerBag->has('app.service');
    $services->get('app.parameter');
    $diContainer->getParameter('app.service');
    $diContainer->hasParameter('app.parameter');
}",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_codes(
        &result,
        &[
            MISSING_PARAMETER,
            MISSING_PARAMETER,
            MISSING_SERVICE,
            MISSING_PARAMETER,
        ],
    );
}

#[test]
fn service_diagnostics_for_php_array_references() {
    let fixture = fixture();
    let document = document(
        "file:///project/config/services.php",
        r"<?php
return [
    'services' => [
        'app.consumer' => [
            'class' => 'App\\MissingArrayClass',
            'parent' => 'app.missing_parent',
            'arguments' => [
                '%app.missing_parameter%',
                '@app.missing_service',
            ],
            'calls' => [['configure', ['%app.existing%']]],
        ],
        'app.alias' => '@app.missing_alias',
    ],
];
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_codes(
        &result,
        &[
            MISSING_SERVICE,
            MISSING_PARAMETER,
            MISSING_SERVICE,
            MISSING_CLASS,
            MISSING_SERVICE,
        ],
    );
}

#[test]
fn service_diagnostics_include_typo_suggestions() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    arguments:
      - '@app.existng'
      - '%app.existng%'
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert!(suggestions(problem).contains(&"app.existing".to_owned()));
        assert_eq!(range_text(&document, problem.range), "app.existng");
    }
}

#[test]
fn service_diagnostics_validate_phpdoc_assistant_references() {
    let fixture = fixture();
    let document = document(
        "file:///project/AssistantUsage.php",
        r"<?php
namespace App;
resolve_service('app.existng');
resolve_parameter('app.parametr');
resolve_class('App\Exsting');
resolve_interface('App\Existing');
resolve_class('App\Existing');
resolve_interface('App\ExistingInterface');
resolve_class_or_interface('App\ExistingInterface');
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 4);
    let by_text: HashMap<String, &Problem> = result
        .iter()
        .map(|problem| (range_text(&document, problem.range), problem))
        .collect();
    assert_eq!(by_text["app.existng"].id.as_str(), MISSING_SERVICE);
    assert!(suggestions(by_text["app.existng"]).contains(&"app.existing".to_owned()));
    assert_eq!(by_text["app.parametr"].id.as_str(), MISSING_PARAMETER);
    assert!(suggestions(by_text["app.parametr"]).contains(&"app.parameter".to_owned()));
    assert_eq!(by_text["App\\Exsting"].id.as_str(), MISSING_CLASS);
    assert!(suggestions(by_text["App\\Exsting"]).contains(&"App\\Existing".to_owned()));
    assert_eq!(by_text["App\\Existing"].id.as_str(), MISSING_CLASS);
    assert!(by_text["App\\Existing"].message.contains("Interface"));
    assert!(suggestions(by_text["App\\Existing"]).contains(&"App\\ExistingInterface".to_owned()));
}

#[test]
fn service_diagnostics_mark_deprecated_services_and_classes() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/services.yaml",
            r"services:
  app.consumer:
    class: App\Legacy
    arguments:
      - '@app.deprecated'
      - '@?app.deprecated'
      - '@?app.runtime_only'
",
            5,
        ),
        (
            "file:///project/services.xml",
            r#"<container><services>
<service id="app.consumer" class="App\Legacy">
  <argument type="service" id="app.deprecated"/>
  <argument type="service" id="app.deprecated" on-invalid="ignore"/>
  <argument type="service" id="app.runtime_only" on-invalid="ignore"/>
</service>
</services></container>"#,
            5,
        ),
        (
            "file:///project/Consumer.php",
            r"<?php
use Psr\Container\ContainerInterface;
use Symfony\Component\DependencyInjection\Attribute\Autowire;
class Consumer {
    #[Autowire(service: 'app.deprecated')]
    public function __construct(private ContainerInterface $container) {}
    public function resolve(): mixed {
        return $this->container->get('app.deprecated');
    }
}",
            4,
        ),
    ];
    for (uri, source, count) in tests {
        let result = analyze(&fixture.analyzer, &document(uri, source));
        assert_eq!(result.len(), count, "{uri}");
        for problem in &result {
            assert!([DEPRECATED_SERVICE, DEPRECATED_CLASS].contains(&problem.id.as_str()));
            assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
            assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
            assert!(!problem.message.contains("%service_id%"));
        }
    }
}
