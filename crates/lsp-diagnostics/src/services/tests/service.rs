use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};

use super::*;
use crate::services::ServiceAnalyzer;
use crate::test_support::check_problems;

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
      - '@{|symfony.service.missing:app.missing|}'
      - '%{|symfony.parameter.missing:app.missing|}%'
      - '@?app.optional'
  app.class_check:
    class: {|symfony.class.missing:App\Missing|}
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
    <argument type="service" id="{|symfony.service.missing:app.missing|}"/>
    <argument>%{|symfony.parameter.missing:app.missing|}%</argument>
    <argument type="service" id="app.optional" on-invalid="ignore"/>
  </service>
  <alias id="local.alias" service="local.service"/>
  <service id="class.check" class="{|symfony.class.missing:App\Missing|}"/>
</services>
</container>"#,
        ),
    ];
    for (uri, source) in tests {
        check_problems(&fixture.analyzer, uri, source);
    }
}

#[test]
fn service_diagnostics_for_php_attributes_and_typed_container() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/Example.php",
        r"<?php
namespace App;
use Psr\Container\ContainerInterface;

class Other { public function get(string $id): mixed {} }

class Example {
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(service: '{|symfony.service.missing:app.missing|}')]
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(param: '{|symfony.parameter.missing:app.missing|}')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireServiceClosure('{|symfony.service.missing:app.closure_missing|}')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireMethodOf(service: '{|symfony.service.missing:app.method_missing|}')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireCallable(service: '{|symfony.service.missing:app.callable_missing|}')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireLocator(
        services: ['{|symfony.service.missing:app.locator_missing|}', 'app.existing'],
        exclude: '{|symfony.service.missing:app.exclude_missing|}',
    )]
    public function run(ContainerInterface $container, Other $other): void {
        $container->get('{|symfony.service.missing:app.missing|}');
        $container->get('app.existing');
        $other->get('ignored.missing');
    }
}",
    );
    for problem in &problems {
        assert!(!problem.message.contains("ignored.missing"));
    }
}

#[test]
fn service_diagnostics_distinguish_parameter_bags_from_service_containers() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
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
    $parameters->get('{|symfony.parameter.missing:app.service|}');
    $containerBag->has('{|symfony.parameter.missing:app.service|}');
    $services->get('{|symfony.service.missing:app.parameter|}');
    $diContainer->getParameter('{|symfony.parameter.missing:app.service|}');
    $diContainer->hasParameter('app.parameter');
}",
    );
}

#[test]
fn service_diagnostics_for_php_array_references() {
    let fixture = fixture();
    check_problems(
        &fixture.analyzer,
        "file:///project/config/services.php",
        r"<?php
return [
    'services' => [
        'app.consumer' => [
            'class' => {|symfony.class.missing:'App\\MissingArrayClass'|},
            'parent' => '{|symfony.service.missing:app.missing_parent|}',
            'arguments' => [
                '%{|symfony.parameter.missing:app.missing_parameter|}%',
                '@{|symfony.service.missing:app.missing_service|}',
            ],
            'calls' => [['configure', ['%app.existing%']]],
        ],
        'app.alias' => '@{|symfony.service.missing:app.missing_alias|}',
    ],
];
",
    );
}

#[test]
fn service_diagnostics_include_typo_suggestions() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    arguments:
      - '@{|symfony.service.missing:app.existng|}'
      - '%{|symfony.parameter.missing:app.existng|}%'
",
    );
    for problem in &problems {
        assert!(suggestions(problem).contains(&"app.existing".to_owned()));
    }
}

#[test]
fn service_diagnostics_validate_phpdoc_assistant_references() {
    let fixture = fixture();
    let (_, problems) = check_problems(
        &fixture.analyzer,
        "file:///project/AssistantUsage.php",
        r"<?php
namespace App;
resolve_service('{|symfony.service.missing:app.existng|}');
resolve_parameter('{|symfony.parameter.missing:app.parametr|}');
resolve_class('{|symfony.class.missing:App\Exsting|}');
resolve_interface('{|symfony.class.missing:App\Existing|}');
resolve_class('App\Existing');
resolve_interface('App\ExistingInterface');
resolve_class_or_interface('App\ExistingInterface');
",
    );
    assert!(suggestions(&problems[0]).contains(&"app.existing".to_owned()));
    assert!(suggestions(&problems[1]).contains(&"app.parameter".to_owned()));
    assert!(suggestions(&problems[2]).contains(&"App\\Existing".to_owned()));
    assert!(problems[3].message.contains("Interface"));
    assert!(suggestions(&problems[3]).contains(&"App\\ExistingInterface".to_owned()));
}

#[test]
fn service_diagnostics_mark_deprecated_services_and_classes() {
    let fixture = fixture();
    let tests = [
        (
            "file:///project/services.yaml",
            r"services:
  app.consumer:
    class: {|symfony.class.deprecated:App\Legacy|}
    arguments:
      - '@{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}'
      - '@?{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}'
      - '@?app.runtime_only'
",
        ),
        (
            "file:///project/services.xml",
            r#"<container><services>
<service id="app.consumer" class="{|symfony.class.deprecated:App\Legacy|}">
  <argument type="service" id="{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}"/>
  <argument type="service" id="{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}" on-invalid="ignore"/>
  <argument type="service" id="app.runtime_only" on-invalid="ignore"/>
</service>
</services></container>"#,
        ),
        (
            "file:///project/Consumer.php",
            r"<?php
use Psr\Container\ContainerInterface;
use Symfony\Component\DependencyInjection\Attribute\Autowire;
class Consumer {
    #[Autowire(service: '{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}')]
    public function __construct(private ContainerInterface $container) {}
    public function resolve(): mixed {
        return $this->container->get('{|symfony.class.deprecated:{|symfony.service.deprecated:app.deprecated|}|}');
    }
}",
        ),
    ];
    for (uri, source) in tests {
        let (_, problems) = check_problems(&fixture.analyzer, uri, source);
        for problem in &problems {
            assert_eq!(problem.severity, Some(DiagnosticSeverity::HINT));
            assert_eq!(problem.tags, vec![DiagnosticTag::DEPRECATED]);
            assert!(!problem.message.contains("%service_id%"));
        }
    }
}
