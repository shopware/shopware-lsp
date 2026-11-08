use std::collections::HashMap;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::LineIndex;

use crate::{Location, Service, ServiceIndex, ServicePrototype};

fn parse_services(name: &str, path: &str, source: &str) -> Vec<Service> {
    match name {
        "yaml" => crate::parse_yaml_services(path, source).0,
        "xml" => crate::parse_xml_services(path, source).0,
        _ => {
            let parse = swlsp_parser_php::parse(source);
            crate::php_service_config(path, &parse.root(), &LineIndex::new(source)).services
        }
    }
}

#[test]
fn service_parsers_preserve_effective_autowire_configuration() {
    let mut cases = vec![
        (
            "yaml",
            "services:\n  _defaults:\n    autowire: true\n  App\\DefaultService: ~\n  App\\DisabledService:\n    autowire: false\n",
        ),
        (
            "xml",
            r#"<container>
  <services>
    <defaults autowire="true"/>
    <service id="App\DefaultService"/>
    <service id="App\DisabledService" autowire="false"/>
  </services>
</container>"#,
        ),
    ];
    cases.push((
        "php fluent",
        r#"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->defaults()->autowire();
    $services->set(App\DefaultService::class);
    $services->set(App\DisabledService::class)->autowire(false);
};"#,
    ));
    cases.push((
        "php array",
        r#"<?php
return [
    'services' => [
    '_defaults' => ['autowire' => true],
    App\DefaultService::class => [],
    App\DisabledService::class => ['autowire' => false],
    ],
];"#,
    ));
    for (name, source) in cases {
        let services = parse_services(name, &format!("/project/config/services.{name}"), source);
        let by_id: HashMap<&str, &Service> = services
            .iter()
            .map(|service| (service.id.as_str(), service))
            .collect();
        let enabled = by_id[r"App\DefaultService"];
        assert!(enabled.autowire_set, "{name}");
        assert!(enabled.autowire, "{name}");
        let disabled = by_id[r"App\DisabledService"];
        assert!(disabled.autowire_set, "{name}");
        assert!(!disabled.autowire, "{name}");
    }
}

#[test]
fn service_parsers_apply_autowire_defaults_to_prototypes() {
    let path = "/project/config/services";
    let mut cases: Vec<(&str, Vec<ServicePrototype>)> = Vec::new();
    let yaml = "services:\n  _defaults:\n    autowire: true\n  App\\:\n    resource: ../src/\n";
    let parse = swlsp_parser_yaml::parse(yaml);
    cases.push((
        "yaml",
        crate::yaml_services::parse_yaml_service_config_tree(
            &format!("{path}.yaml"),
            Some(&parse.tree),
            &LineIndex::new(yaml),
        )
        .2,
    ));
    let xml = r#"<container><services>
  <defaults autowire="true"/>
  <prototype namespace="App\" resource="../src/"/>
</services></container>"#;
    let parse = swlsp_parser_xml::parse(xml);
    cases.push((
        "xml",
        crate::xml_services::parse_xml_service_config_tree(
            &format!("{path}.xml"),
            Some(&parse.tree),
            &LineIndex::new(xml),
        )
        .2,
    ));
    for (name, source) in [
        (
            "php fluent",
            r#"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->defaults()->autowire();
    $services->load('App\\', '../src/');
};"#,
        ),
        (
            "php array",
            r#"<?php
return ['services' => [
    '_defaults' => ['autowire' => true],
    'App\\' => ['resource' => '../src/'],
]];"#,
        ),
    ] {
        let parse = swlsp_parser_php::parse(source);
        cases.push((
            name,
            crate::php_service_config(
                &format!("{path}.php"),
                &parse.root(),
                &LineIndex::new(source),
            )
            .prototypes,
        ));
    }
    for (name, prototypes) in cases {
        assert_eq!(prototypes.len(), 1, "{name}");
        assert!(prototypes[0].autowire_set, "{name}");
        assert!(prototypes[0].autowire, "{name}");
    }
}

#[test]
fn autowired_service_usage_resolves_parents_and_explicit_overrides() {
    let root = tempfile::tempdir().unwrap();
    let root_path = root.path().to_str().unwrap();
    let index = ServiceIndex::new(root_path, root.path().join(".cache"), None).unwrap();
    let path = format!("{root_path}/config/services.yaml");
    let source = r"services:
  app.parent:
    class: App\ParentService
    autowire: true
  app.child:
    class: App\ChildService
    parent: app.parent
  app.disabled:
    class: App\DisabledService
    parent: app.parent
    autowire: false
";
    std::fs::create_dir_all(root.path().join(".cache")).unwrap();
    index
        .index(&ParsedFile::new(path.clone(), source.as_bytes().to_vec()))
        .unwrap();
    assert_eq!(
        index
            .get_autowired_services_usage_by_class_name(r"App\ChildService")
            .unwrap(),
        vec![Location {
            path: path.clone(),
            line: 5
        }]
    );
    assert!(
        index
            .get_autowired_services_usage_by_class_name(r"App\DisabledService")
            .unwrap()
            .is_empty()
    );
    index.close().unwrap();
}

#[test]
fn explicit_service_autowire_overrides_generated_prototype() {
    let root = tempfile::tempdir().unwrap();
    let root_path = root.path().to_str().unwrap();
    let cache = root.path().join(".cache");
    std::fs::create_dir_all(&cache).unwrap();
    let php = swlsp_php::PhpIndex::open(&cache, None).unwrap();
    let index = ServiceIndex::new(root_path, &cache, None).unwrap();
    index.set_php_index(Some(php.clone()));
    let php_path = format!("{root_path}/src/Services.php");
    php.index(&ParsedFile::new(
        php_path,
        b"<?php\nnamespace App;\nclass WiredService {}\nclass ManualService {}\n".to_vec(),
    ))
    .unwrap();
    let service_path = format!("{root_path}/config/services.yaml");
    index
        .index(&ParsedFile::new(
            service_path.clone(),
            b"services:\n  _defaults:\n    autowire: true\n  App\\:\n    resource: ../src/\n  App\\ManualService:\n    autowire: false\n"
                .to_vec(),
        ))
        .unwrap();
    assert_eq!(
        index
            .get_autowired_services_usage_by_class_name(r"App\WiredService")
            .unwrap(),
        vec![Location {
            path: service_path,
            line: 4
        }]
    );
    assert!(
        index
            .get_autowired_services_usage_by_class_name(r"App\ManualService")
            .unwrap()
            .is_empty()
    );
    index.close().unwrap();
    php.close().unwrap();
}
