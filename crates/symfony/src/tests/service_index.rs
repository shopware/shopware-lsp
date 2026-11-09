use std::sync::Arc;

use swlsp_indexer::{CancelToken, FileScanner, Indexer, ParsedFile, Store};

use crate::ServiceIndex;

#[test]
fn scanner_prepared_indexing_matches_direct_indexing() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = root.path().to_str().unwrap();
    let files = [
        (
            "config/services.yaml",
            "parameters:\n  app.locale: en\nservices:\n  _defaults:\n    autowire: true\n  App\\:\n    resource: '../src/'\n  app.mailer:\n    class: App\\Mailer\n    tags: [app.transport]\n",
        ),
        (
            "config/services.xml",
            "<container>\n  <parameters>\n    <parameter key=\"app.timeout\">30</parameter>\n  </parameters>\n  <services>\n    <service id=\"app.logger\" class=\"App\\Logger\"/>\n  </services>\n</container>\n",
        ),
        (
            "config/services.php",
            "<?php\nuse Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ContainerConfigurator;\nreturn static function (ContainerConfigurator $container): void {\n    $container->parameters()->set('app.retries', 3);\n    $services = $container->services();\n    $services->set('app.cache', App\\Cache::class);\n};\n",
        ),
    ];
    let mut paths = Vec::new();
    for (relative, source) in files {
        let path = root.path().join(relative);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        paths.push((path.to_str().unwrap().to_owned(), source));
    }

    let store = Store::open(cache.path().join("indexes.db")).unwrap();
    let scanned =
        Arc::new(ServiceIndex::new(root_path, cache.path().join("scanned"), Some(&store)).unwrap());
    let mut scanner = FileScanner::new(
        root_path,
        cache.path().join("scanner.db"),
        Some(store.clone()),
    )
    .unwrap();
    scanner.add_indexer(scanned.clone());
    scanner.index_all(&CancelToken::new()).unwrap();
    scanner.close().unwrap();

    let direct = ServiceIndex::new(root_path, cache.path().join("direct"), None).unwrap();
    for (path, source) in &paths {
        direct
            .index(&ParsedFile::new(path.as_str(), source.as_bytes().to_vec()))
            .unwrap();
    }

    let services = |index: &ServiceIndex| {
        let mut services = index.get_all_service_definitions().unwrap();
        services.sort_by(|left, right| (&left.id, &left.path).cmp(&(&right.id, &right.path)));
        services
    };
    let scanned_services = services(&scanned);
    for id in ["app.mailer", "app.logger", "app.cache"] {
        assert!(
            scanned_services.iter().any(|service| service.id == id),
            "service {id:?} not found in {scanned_services:#?}"
        );
    }
    assert_eq!(scanned_services, services(&direct));
    assert_eq!(
        scanned.get_all_parameters().unwrap(),
        direct.get_all_parameters().unwrap()
    );
    assert_eq!(scanned.get_all_parameters().unwrap().len(), 3);
    let mut scanned_prototypes = scanned.prototypes.get_all_values().unwrap();
    let mut direct_prototypes = direct.prototypes.get_all_values().unwrap();
    scanned_prototypes.sort_by(|left, right| left.namespace.cmp(&right.namespace));
    direct_prototypes.sort_by(|left, right| left.namespace.cmp(&right.namespace));
    assert_eq!(scanned_prototypes.len(), 1);
    assert_eq!(scanned_prototypes, direct_prototypes);

    direct.close().unwrap();
    scanned.close().unwrap();
    store.close().unwrap();
}
