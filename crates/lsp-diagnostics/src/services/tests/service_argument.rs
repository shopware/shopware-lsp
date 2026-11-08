use serde_json::Value;

use super::*;
use crate::services::ServiceArgumentAnalyzer;

const MISSING_ARGUMENTS: &str = "symfony.service.arguments.missing";
const UNKNOWN_NAMED_ARGUMENT: &str = "symfony.service.named_argument.unknown";
const MISSING_METHOD: &str = "symfony.service.method.missing";
const INVALID_TYPE: &str = "symfony.service.argument.type";

struct Fixture {
    _dir: tempfile::TempDir,
    analyzer: ServiceArgumentAnalyzer,
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/Services.php",
            r"<?php
namespace App;
interface LoggerInterface {}
interface OtherInterface {}
class Logger implements LoggerInterface {}
class WrongLogger {}
class NeedsArguments {
    public function __construct(
        LoggerInterface $logger,
        string $name,
        ?int $optional = null,
    ) {}
    public function setLogger(LoggerInterface $logger): void {}
}
class Factory {
    public function create(): WrongLogger { return new WrongLogger(); }
}
class ChildArguments extends NeedsArguments {}
class UnionArguments {
    public function __construct(
        LoggerInterface|OtherInterface $dependency,
    ) {}
}",
        )],
    );
    let services = service_index(
        &dir.path().join("services"),
        Some(&php),
        &[(
            "/project/indexed.yaml",
            r"services:
  app.logger:
    class: App\Logger
  app.wrong:
    class: App\WrongLogger
",
        )],
    );
    let typed = services
        .get_services_by_type("App\\LoggerInterface")
        .unwrap();
    assert_eq!(typed.len(), 1, "{typed:?}");
    assert_eq!(typed[0].id, "app.logger");
    Fixture {
        _dir: dir,
        analyzer: ServiceArgumentAnalyzer::new(Some(services), Some(php)),
    }
}

fn contains(value: Option<&Value>, expected: &str) -> bool {
    strings(value).iter().any(|value| value == expected)
}

#[test]
fn xml_service_argument_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.xml",
        r#"<container><services>
  <defaults autowire="false"/>
  <service id="app.complete" class="App\NeedsArguments">
    <argument type="service" id="app.logger"/>
    <argument key="$name">name</argument>
  </service>
  <service id="app.missing" class="App\NeedsArguments">
    <argument type="service" id="app.logger"/>
  </service>
  <service id="app.autowired" class="App\NeedsArguments" autowire="true"/>
  <service id="app.factory" class="App\NeedsArguments"><factory service="factory"/></service>
</services></container>"#,
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), MISSING_ARGUMENTS);
    assert!(result[0].message.contains("$name"));
    let data = payload(&result[0]);
    assert_eq!(data["format"], "xml");
    let arguments = data["missingArguments"].as_array().unwrap();
    assert_eq!(arguments.len(), 1);
    assert_eq!(arguments[0]["name"], "$name");
    assert_eq!(arguments[0]["suggestedService"], "?");
}

#[test]
fn yaml_service_argument_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  _defaults:
    autowire: true

  app.autowired:
    class: App\NeedsArguments

  app.complete:
    class: App\NeedsArguments
    autowire: false
    arguments: ['@app.logger', 'name']

  app.missing:
    class: App\NeedsArguments
    autowire: false
    arguments:
      $logger: '@app.logger'

  app.unsupported:
    class: App\NeedsArguments
    autowire: false
    factory: ['@factory', create]
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), MISSING_ARGUMENTS);
    assert!(result[0].message.contains("$name"));
    assert!(!result[0].message.contains("$logger"));
}

#[test]
fn service_argument_diagnostics_suggest_unique_typed_service() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    arguments: []
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    let arguments = payload(&result[0])["missingArguments"]
        .as_array()
        .unwrap()
        .clone();
    assert_eq!(arguments.len(), 2);
    assert_eq!(arguments[0]["name"], "$logger");
    assert_eq!(arguments[0]["suggestedService"], "app.logger");
    assert_eq!(arguments[1]["name"], "$name");
    assert_eq!(arguments[1]["suggestedService"], "?");
    // Go encodes a nil candidate list as null.
    assert_eq!(arguments[1]["candidates"], Value::Null);
}

#[test]
fn yaml_service_named_argument_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.logger'
      $name: product
      $nam: typo
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), UNKNOWN_NAMED_ARGUMENT);
    assert_eq!(result[0].message, "Symfony: named argument does not exists");
    assert_eq!(range_text(&document, result[0].range), "$nam");
    assert!(contains(payload(&result[0]).get("suggestions"), "$name"));
}

#[test]
fn yaml_service_named_argument_diagnostics_support_inherited_constructor() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\ChildArguments
    arguments:
      $logger: '@app.logger'
      $name: product
",
    );
    assert!(analyze(&fixture.analyzer, &document).is_empty());
}

#[test]
fn yaml_service_named_argument_diagnostics_skip_factory_and_unknown_class() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.factory:
    class: App\NeedsArguments
    factory: ['@factory', create]
    arguments:
      $missing: value
  app.unknown:
    class: App\Missing
    arguments:
      $missing: value
",
    );
    assert!(analyze(&fixture.analyzer, &document).is_empty());
}

#[test]
fn yaml_service_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.wrong'
      $name: product
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert_eq!(
        result[0].message,
        "Expect instance of: App\\LoggerInterface"
    );
    assert_eq!(range_text(&document, result[0].range), "@app.wrong");
    assert!(contains(
        payload(&result[0]).get("suggestions"),
        "@app.logger"
    ));
}

#[test]
fn xml_service_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.xml",
        r#"<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <argument type="service" id="app.wrong"/>
    <argument>product</argument>
  </service>
</services></container>"#,
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert_eq!(range_text(&document, result[0].range), "app.wrong");
    assert!(contains(
        payload(&result[0]).get("suggestions"),
        "app.logger"
    ));
}

#[test]
fn service_argument_type_diagnostics_accept_subtype_and_union() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.logger'
      $name: product
  app.union:
    class: App\UnionArguments
    arguments:
      $dependency: '@app.logger'
",
    );
    assert!(analyze(&fixture.analyzer, &document).is_empty());
}

#[test]
fn service_argument_type_diagnostics_use_unsaved_local_service_class() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.local_wrong:
    class: App\WrongLogger
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.local_wrong'
      $name: product
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
}

#[test]
fn yaml_service_call_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    factory: ['@factory', create]
    calls:
      - [setLogger, ['@app.wrong']]
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert!(result[0].message.contains("LoggerInterface"));
}

#[test]
fn xml_service_call_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.xml",
        r#"<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <factory service="factory"/>
    <call method="setLogger">
      <argument type="service" id="app.wrong"/>
    </call>
  </service>
</services></container>"#,
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert!(result[0].message.contains("LoggerInterface"));
}

#[test]
fn yaml_configured_service_method_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.consumer:
    class: App\NeedsArguments
    factory: ['@factory', create]
    calls:
      - [setLogger, ['@app.logger']]
      - setLoggr: []
",
    );
    let result = analyze(&fixture.analyzer, &document);
    let missing = with_code(&result, MISSING_METHOD);
    assert_eq!(missing.len(), 1);
    assert_eq!(missing[0].message, "Missing Method");
    assert_eq!(range_text(&document, missing[0].range), "setLoggr");
    assert!(contains(
        payload(missing[0]).get("suggestions"),
        "setLogger"
    ));
}

#[test]
fn xml_configured_service_method_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.xml",
        r#"<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <factory service="factory"/>
    <call method="setLogger"/>
    <call method="setLoggr"/>
  </service>
</services></container>"#,
    );
    let result = analyze(&fixture.analyzer, &document);
    let missing = with_code(&result, MISSING_METHOD);
    assert_eq!(missing.len(), 1);
    assert_eq!(missing[0].message, "Missing Method");
    assert_eq!(range_text(&document, missing[0].range), "setLoggr");
}

#[test]
fn php_configured_service_method_diagnostics() {
    let fixture = fixture();
    let fixtures = [
        r"<?php
use App\NeedsArguments;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('app.consumer', NeedsArguments::class)
        ->call('setLogger', [])
        ->call('setLoggr', []);
};
",
        r"<?php
use App\NeedsArguments;
use Symfony\Config\ServicesConfig;

return ServicesConfig::config([
    'services' => [
        NeedsArguments::class => [
            'calls' => [
                'setLogger' => [],
                'setLoggr' => [],
            ],
        ],
    ],
]);
",
        r"<?php
return [
    'services' => [
        'App\\NeedsArguments' => [
            'calls' => [
                ['setLogger', []],
                ['setLoggr', [], true],
            ],
        ],
    ],
];
",
    ];
    for source in fixtures {
        let document = document("file:///project/services.php", source);
        let result = analyze(&fixture.analyzer, &document);
        let missing = with_code(&result, MISSING_METHOD);
        assert_eq!(missing.len(), 1, "{source}");
        assert_eq!(range_text(&document, missing[0].range), "setLoggr");
        assert!(contains(
            payload(missing[0]).get("suggestions"),
            "setLogger"
        ));
    }
}

#[test]
fn configured_service_method_diagnostics_use_inherited_methods() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.php",
        r"<?php
return [
    'services' => [
        'App\\ChildArguments' => [
            'calls' => ['setLogger' => []],
        ],
    ],
];
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert!(with_code(&result, MISSING_METHOD).is_empty());
}

#[test]
fn configured_service_method_diagnostics_skip_unknown_classes() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.yaml",
        r"services:
  app.unknown:
    class: App\Unknown
    calls:
      - missing: []
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert!(with_code(&result, MISSING_METHOD).is_empty());
}

#[test]
fn configured_factory_method_diagnostics_use_factory_receiver() {
    let fixture = fixture();
    let fixtures = [
        (
            "file:///project/services.yaml",
            r"services:
  app.factory:
    class: App\Factory
  app.product:
    class: App\WrongLogger
    factory: ['@app.factory', creat]
",
        ),
        (
            "file:///project/services.xml",
            r#"<container><services>
  <service id="app.factory" class="App\Factory"/>
  <service id="app.product" class="App\WrongLogger">
    <factory service="app.factory" method="creat"/>
  </service>
</services></container>"#,
        ),
    ];
    for (uri, source) in fixtures {
        let document = document(uri, source);
        let result = analyze(&fixture.analyzer, &document);
        let missing = with_code(&result, MISSING_METHOD);
        assert_eq!(missing.len(), 1, "{uri}");
        assert_eq!(range_text(&document, missing[0].range), "creat");
        let data = payload(missing[0]);
        assert_eq!(data["className"], "App\\Factory");
        assert!(contains(data.get("suggestions"), "create"));
    }
}

#[test]
fn configured_service_method_diagnostic_includes_create_target() {
    let root = tempfile::tempdir().unwrap();
    let class_path = root.path().join("Called.php");
    let class_source = r"<?php
namespace App;
class Called
{
    public function existing(): void {}
}
";
    std::fs::write(&class_path, class_source).unwrap();
    let php = php_index(&root.path().join("php"), &[(&p(&class_path), class_source)]);
    let document = document(
        &format!("file://{}", p(&root.path().join("services.yaml"))),
        r"services:
  app.called:
    class: App\Called
    calls:
      - [initialize, []]
",
    );
    let result = analyze(&ServiceArgumentAnalyzer::new(None, Some(php)), &document);
    let missing = with_code(&result, MISSING_METHOD);
    assert_eq!(missing.len(), 1);
    let data = payload(missing[0]);
    assert_eq!(data["methodName"], "initialize");
    assert!(!data.contains_key("classURI"));
    assert!(!data.contains_key("insertLine"));
    assert!(!data.contains_key("insertCharacter"));
}

#[test]
fn php_service_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.php",
        r"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\NeedsArguments::class)
    ->args([service('app.wrong')]);
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert_eq!(
        result[0].message,
        "Expect instance of: App\\LoggerInterface"
    );
    assert_eq!(range_text(&document, result[0].range), "app.wrong");
    assert!(contains(
        payload(&result[0]).get("suggestions"),
        "app.logger"
    ));
}

#[test]
fn php_legacy_array_service_argument_type_diagnostics() {
    let fixture = fixture();
    let document = document(
        "file:///project/legacy_services.php",
        r"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

return [
    'services' => [
        'App\NeedsArguments' => [
            'arguments' => [
                '$logger' => '@app.wrong',
            ],
        ],
    ],
];
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.as_str(), INVALID_TYPE);
    assert!(contains(
        payload(&result[0]).get("suggestions"),
        "@app.logger"
    ));
}

#[test]
fn php_service_argument_type_diagnostics_formats_expression_suggestion() {
    let fixture = fixture();
    let document = document(
        "file:///project/services.php",
        r"<?php
use App\NeedsArguments;
use App\WrongLogger;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(WrongLogger::class);
    $services->set(NeedsArguments::class)->arg(0, WrongLogger::class);
};
",
    );
    let result = analyze(&fixture.analyzer, &document);
    assert_eq!(result.len(), 1);
    assert!(contains(
        payload(&result[0]).get("suggestions"),
        "service('app.logger')"
    ));
}
