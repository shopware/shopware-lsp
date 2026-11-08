use std::collections::HashMap;

use crate::{Parameter, Service, parse_yaml_services};

fn by_id(services: &[Service]) -> HashMap<&str, &Service> {
    services
        .iter()
        .map(|service| (service.id.as_str(), service))
        .collect()
}

fn by_name(parameters: &[Parameter]) -> HashMap<&str, &Parameter> {
    parameters
        .iter()
        .map(|parameter| (parameter.name.as_str(), parameter))
        .collect()
}

#[test]
fn parse_yaml_services_definitions() {
    let (services, parameters) = parse_yaml_services(
        "test.yaml",
        r#"
services:
  _defaults:
    autowire: true
    autoconfigure: true

  App\Service\ExampleService:
    arguments:
      $parameter: 'value'
    tags:
      - name: kernel.event_subscriber

  app.another_service:
    class: App\Service\AnotherService
    tags:
      - { name: doctrine.event_listener, event: postPersist }

  app.aliased: '@app.another_service'

  app.alias_service:
    alias: app.another_service

parameters:
  app.parameter.string: 'parameter value'
  app.parameter.integer: 42
  app.parameter.service: '@app.another_service'
"#,
    );
    assert_eq!(services.len(), 4);
    let services = by_id(&services);
    let example = services["App\\Service\\ExampleService"];
    assert_eq!(example.class, "App\\Service\\ExampleService");
    assert!(example.tags.contains_key("kernel.event_subscriber"));
    let another = services["app.another_service"];
    assert_eq!(another.class, "App\\Service\\AnotherService");
    assert!(another.tags.contains_key("doctrine.event_listener"));
    assert_eq!(services["app.aliased"].alias_target, "app.another_service");
    assert_eq!(
        services["app.alias_service"].alias_target,
        "app.another_service"
    );

    assert_eq!(parameters.len(), 3);
    let parameters = by_name(&parameters);
    assert_eq!(parameters["app.parameter.string"].value, "parameter value");
    assert_eq!(parameters["app.parameter.integer"].value, "42");
    assert_eq!(
        parameters["app.parameter.service"].value,
        "@app.another_service"
    );
}

#[test]
fn parse_yaml_deprecated_service_metadata() {
    let (services, _) = parse_yaml_services(
        "services.yaml",
        r#"services:
  legacy:
    class: App\Legacy
    deprecated: 'Use App\Modern instead'
  flagged:
    class: App\Flagged
    deprecated: true
  active:
    class: App\Active
    deprecated: false
  legacy_null_marker:
    class: App\LegacyNullMarker
    deprecated: ~
"#,
    );
    assert_eq!(services.len(), 4);
    assert!(services[0].deprecated);
    assert_eq!(services[0].deprecation, "Use App\\Modern instead");
    assert!(!services[0].deprecated_range.is_empty());
    assert!(services[1].deprecated);
    assert!(services[1].deprecation.is_empty());
    assert!(!services[2].deprecated);
    assert!(services[3].deprecated);
    assert!(services[3].deprecation.is_empty());
}

#[test]
fn parse_yaml_services_from_file() {
    let (services, parameters) = parse_yaml_services(
        "test.yaml",
        r#"
services:
  App\Service\ExampleService:
    tags:
      - { name: tag1 }
      - { name: tag2 }

  with_alias:
    alias: original_service

parameters:
  param1: 'value1'
  param2: 'value2'
"#,
    );
    assert_eq!(services.len(), 2);
    assert_eq!(parameters.len(), 2);
    let services = by_id(&services);
    let example = services["App\\Service\\ExampleService"];
    assert_eq!(example.class, "App\\Service\\ExampleService");
    assert!(example.tags.contains_key("tag1"));
    assert!(example.tags.contains_key("tag2"));
    assert_eq!(services["with_alias"].alias_target, "original_service");
    let parameters = by_name(&parameters);
    assert_eq!(parameters["param1"].value, "value1");
    assert_eq!(parameters["param2"].value, "value2");
}

#[test]
fn parse_yaml_services_handles_null_definitions() {
    let (services, parameters) = parse_yaml_services(
        "test.yaml",
        "\nservices:\n  App\\Service\\DefaultService: ~\nparameters:\n  optional: null\n",
    );
    assert_eq!(services.len(), 1);
    assert_eq!(services[0].class, "App\\Service\\DefaultService");
    assert_eq!(parameters.len(), 1);
    assert!(parameters[0].value.is_empty());
}
