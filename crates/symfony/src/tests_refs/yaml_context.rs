use std::collections::HashSet;

use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;

use super::*;

#[test]
fn yaml_service_contexts() {
    let parse = swlsp_parser_yaml::parse(
        "services:\n  App\\Service\\Example:\n    class: App\\Service\\Concrete\n    arguments:\n      $logger: '@logger'\n",
    );
    let root = yamlquery::root_value(&parse.root()).unwrap();
    let services = yamlquery::property(&root, "services").unwrap();
    let service_pair = yamlquery::property_pair(&services, r"App\Service\Example").unwrap();
    let service = yamlquery::pair_value(&service_pair).unwrap();
    let class = yamlquery::property(&service, "class").unwrap();
    let arguments = yamlquery::property(&service, "arguments").unwrap();
    let logger = yamlquery::property(&arguments, "$logger").unwrap();
    assert!(is_yaml_service_id(&service_pair));
    assert_eq!(yaml_context_value(&service_pair), r"App\Service\Example");
    assert!(is_yaml_class_property_in_service(&class));
    assert_eq!(yaml_context_value(&class), r"App\Service\Concrete");
    assert!(is_yaml_argument_service_id(&logger));
    assert_eq!(yaml_context_value(&logger), "@logger");
}

#[test]
fn yaml_service_reference_variants() {
    let parse = swlsp_parser_yaml::parse(
        r"services:
  App\Service\Example:
    class: App\Service\Concrete
    decorates: app.target
    parent: '@app.parent'
    arguments:
      - '@?app.optional'
  app.alias:
    alias: app.target
",
    );
    let mut values = HashSet::new();
    let mut classes = Vec::new();
    for scalar in yamlquery::nodes(&parse.root(), &[yamlsyntax::YAML_SCALAR]) {
        if let Some(name) = yaml_service_reference_name(&scalar) {
            values.insert(name);
        }
        if let Some(name) = yaml_class_reference_name(&scalar) {
            classes.push(name);
        }
    }
    assert!(values.contains("app.target"));
    assert!(values.contains("app.parent"));
    assert!(values.contains("app.optional"));
    assert!(!classes.contains(&r"App\Service\Example".to_owned()));
    assert!(classes.contains(&r"App\Service\Concrete".to_owned()));
}
