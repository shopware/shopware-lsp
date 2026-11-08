use swlsp_indexer::{Indexer, ParsedFile};

use super::*;

fn find<'a>(services: &'a [Service], id: &str) -> &'a Service {
    services
        .iter()
        .find(|service| service.id == id)
        .unwrap_or_else(|| panic!("service {id}"))
}

#[test]
fn service_configuration_in_document_covers_php_xml_and_yaml_relationships() {
    let mut cases = vec![
        (
            "/project/config/services.xml",
            r#"<container>
  <services>
    <service id="base" class="App\Base"/>
    <service id="abstract" class="App\AbstractService"/>
    <service id="decorator" class="App\Decorator" decorates="base" parent="abstract"/>
    <prototype namespace="App\" resource="../src" exclude="../src/Excluded"/>
  </services>
</container>"#,
        ),
        (
            "/project/config/services.yaml",
            r"services:
  base:
    class: App\Base
  abstract:
    class: App\AbstractService
  decorator:
    class: App\Decorator
    decorates: base
    parent: abstract
  'App\':
    resource: '../src'
    exclude: '../src/Excluded'
",
        ),
    ];
    cases.push((
        "/project/config/services.php",
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('base', App\Base::class);
    $services->set('abstract', App\AbstractService::class);
    $services->set('decorator', App\Decorator::class)
    ->decorate('base')
    ->parent('abstract');
    $services->load('App\\', '../src')->exclude('../src/Excluded');
};
",
    ));
    for (path, source) in cases {
        let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
        let config =
            service_configuration_in_document(path, parsed.syntax_tree(), parsed.line_index());
        assert_eq!(config.services.len(), 3, "{path}");
        let decorator = find(&config.services, "decorator");
        assert_eq!(decorator.decorates, "base", "{path}");
        assert_eq!(decorator.parent, "abstract", "{path}");
        assert!(!decorator.id_range.is_empty(), "{path}");
        assert!(!decorator.decorates_range.is_empty(), "{path}");
        assert!(!decorator.parent_range.is_empty(), "{path}");
        assert_eq!(config.prototypes.len(), 1, "{path}");
        let prototype = &config.prototypes[0];
        assert_eq!(prototype.namespace, "App\\", "{path}");
        assert_eq!(prototype.resource, "/project/src", "{path}");
        assert_eq!(prototype.excludes, vec!["/project/src/Excluded"], "{path}");
        assert!(!prototype.namespace_range.is_empty(), "{path}");
        assert!(!prototype.resource_range.is_empty(), "{path}");
    }
}

#[test]
fn service_index_persists_relationships_and_expands_yaml_prototype() {
    let root = tempfile::tempdir().unwrap();
    let root_path = root.path().to_str().unwrap();
    let cache = root.path().join(".cache");
    std::fs::create_dir_all(&cache).unwrap();
    let php = swlsp_php::PhpIndex::open(&cache, None).unwrap();
    php.index(&ParsedFile::new(
        format!("{root_path}/src/Included.php"),
        b"<?php\nnamespace App;\nclass Included {}\n".to_vec(),
    ))
    .unwrap();
    let index = ServiceIndex::new(root_path, &cache, None).unwrap();
    index.set_php_index(Some(php.clone()));
    let config_path = format!("{root_path}/config/services.yaml");
    index
        .index(&ParsedFile::new(
            config_path.clone(),
            b"services:\n  base:\n    class: App\\Base\n  decorator:\n    class: App\\Decorator\n    decorates: base\n  'App\\':\n    resource: '../src'\n".to_vec(),
        ))
        .unwrap();
    let definitions = index.service_definitions().unwrap();
    assert_eq!(find(&definitions, "decorator").decorates, "base");
    let declarations = index.service_declarations("App\\Included").unwrap();
    assert_eq!(declarations.len(), 1);
    assert_eq!(declarations[0].path, config_path);
    assert_eq!(declarations[0].line, 7);
    index.close().unwrap();
    php.close().unwrap();
}
