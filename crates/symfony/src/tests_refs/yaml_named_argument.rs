use swlsp_indexer::{Indexer, ParsedFile};

use super::*;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn yaml_service_named_arguments_are_collected() {
    let source = r"services:
  app.consumer:
    class: App\Consumer
    arguments:
      '$logger': '@logger'
      $name: product
  app.factory:
    class: App\FactoryProduct
    factory: ['@factory', create]
    arguments:
      $value: product
  App\ImplicitConsumer:
    arguments:
      $enabled: true
  global.consumer:
    class: GlobalConsumer
    arguments:
      $value: true
";
    let parse = swlsp_parser_yaml::parse(source);
    let arguments = yaml_service_named_arguments(&parse.root());
    assert_eq!(arguments.len(), 5);
    assert_eq!(arguments[0].name, "$logger");
    assert_eq!(arguments[0].service_id, "app.consumer");
    assert_eq!(arguments[0].class_name, r"App\Consumer");
    assert_eq!(arguments[0].existing, vec!["$logger", "$name"]);
    assert_eq!(slice(source, arguments[0].range), "$logger");
    assert!(arguments[0].complete);
    assert!(arguments[2].has_factory);
    assert_eq!(arguments[2].class_name, r"App\FactoryProduct");
    assert_eq!(arguments[3].class_name, r"App\ImplicitConsumer");
    assert_eq!(arguments[4].class_name, "GlobalConsumer");
}

#[test]
fn yaml_service_named_argument_at_supports_incomplete_key() {
    let source = "services:\n  app.consumer:\n    class: App\\Consumer\n    arguments:\n      $log";
    let parse = swlsp_parser_yaml::parse(source);
    let offset = (source.find("$log").unwrap() + 4) as u32;
    let context = yaml_service_named_argument_at(&parse.root(), offset).unwrap();
    assert_eq!(context.name, "$log");
    assert!(!context.complete);
    assert_eq!(context.class_name, r"App\Consumer");
    assert_eq!(slice(source, context.range), "$log");
}

#[test]
fn yaml_service_named_argument_ignores_defaults_and_non_argument_keys() {
    let source = "services:\n  _defaults:\n    bind:\n      $logger: '@logger'\n  app.consumer:\n    class: App\\Consumer\n    calls:\n      - [setLogger, {$logger: '@logger'}]\n";
    let parse = swlsp_parser_yaml::parse(source);
    assert!(yaml_service_named_arguments(&parse.root()).is_empty());
}

#[test]
fn yaml_service_default_binding_at_supports_named_and_typed_keys() {
    let source = r"services:
  _defaults:
    bind:
      $proxyUrl: '%env(string:PROXY_URL)%'
      'string $defaultUri': '%env(string:DEFAULT_URI)%'
      Psr\Log\LoggerInterface $logger: '@logger'
";
    let parse = swlsp_parser_yaml::parse(source);
    for (needle, name, type_name) in [
        ("$proxyUrl", "$proxyUrl", ""),
        ("$defaultUri", "$defaultUri", "string"),
        ("$logger", "$logger", r"Psr\Log\LoggerInterface"),
    ] {
        let offset = (source.find(needle).unwrap() + 2) as u32;
        let binding = yaml_service_default_binding_at(&parse.root(), offset).unwrap();
        assert_eq!(binding.name, name);
        assert_eq!(binding.type_name, type_name);
        assert!(slice(source, binding.range).contains(needle));
    }
}

#[test]
fn yaml_service_default_binding_at_ignores_other_bind_mappings() {
    let source = "framework:\n  bind:\n    string $value: example\nservices:\n  app.consumer:\n    arguments:\n      $value: example\n";
    let parse = swlsp_parser_yaml::parse(source);
    for (index, _) in source.match_indices("$value") {
        assert!(yaml_service_default_binding_at(&parse.root(), (index + 2) as u32).is_none());
    }
}

#[test]
fn resolve_yaml_service_named_argument_class_follows_alias() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = ServiceIndex::new(root.path().to_str().unwrap(), cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/services.yaml",
            b"services:\n  app.consumer:\n    alias: app.concrete\n  app.concrete:\n    class: App\\Consumer\n".to_vec(),
        ))
        .unwrap();
    let argument = YamlServiceNamedArgument {
        service_id: "app.consumer".to_owned(),
        ..YamlServiceNamedArgument::default()
    };
    assert_eq!(
        resolve_yaml_service_named_argument_class(Some(&index), &argument)
            .unwrap()
            .as_deref(),
        Some(r"App\Consumer")
    );
    index.close().unwrap();
}
