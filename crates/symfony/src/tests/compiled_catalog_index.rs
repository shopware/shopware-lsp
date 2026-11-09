use std::sync::Arc;

use swlsp_indexer::{CancelToken, FileScanner, Indexer, Store, SupplementalPathIndexer};

use crate::{CompiledCatalogIndex, ServiceIndex};

#[test]
fn compiled_container_scanner_persistence_and_removal() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path().to_str().unwrap().to_owned();
    let cache = cache_dir.path().to_path_buf();
    let path = format!("{root}/var/cache/dev_test/Shopware_Core_KernelDevDebugContainer.xml");
    std::fs::create_dir_all(format!("{root}/var/cache/dev_test")).unwrap();
    std::fs::write(
        &path,
        r#"<container><services><service id="compiled.only" class="App\First"/></services></container>"#,
    )
    .unwrap();
    let open = || {
        let store = Store::open(cache.join("indexes.db")).unwrap();
        let services = Arc::new(ServiceIndex::new(&root, &cache, Some(&store)).unwrap());
        let generated = Arc::new(
            services
                .compiled_container_index(&cache, Some(&store))
                .unwrap(),
        );
        let mut scanner =
            FileScanner::new(&root, cache.join("scanner.db"), Some(store.clone())).unwrap();
        scanner.add_indexer(generated.clone());
        (store, services, generated, scanner)
    };
    let close = |store: Store,
                 services: Arc<ServiceIndex>,
                 generated: Arc<CompiledCatalogIndex>,
                 scanner: FileScanner| {
        scanner.close().unwrap();
        generated.close().unwrap();
        services.close().unwrap();
        store.close().unwrap();
    };
    let cancel = CancelToken::new();

    let (store, services, generated, scanner) = open();
    assert!(
        !services.container_catalog().container_exists(),
        "construction does not read generated files"
    );
    assert!(generated.should_enter_directory(&format!("{root}/var/cache/dev_test")));
    assert!(!generated.should_enter_directory(&format!("{root}/var/cache/dev_test/pools")));
    scanner.index_all(&cancel).unwrap();
    let service = services
        .get_service_by_id("compiled.only")
        .unwrap()
        .unwrap();
    assert_eq!(service.class, r"App\First");
    close(store, services, generated, scanner);

    let (store, services, generated, scanner) = open();
    let service = services
        .get_service_by_id("compiled.only")
        .unwrap()
        .unwrap();
    assert_eq!(service.class, r"App\First");
    std::fs::write(
        &path,
        r#"<container><services><service id="compiled.changed" class="App\Second"/></services></container>"#,
    )
    .unwrap();
    scanner.index_files(&cancel, &[path.as_str()]).unwrap();
    assert!(
        services
            .get_service_by_id("compiled.only")
            .unwrap()
            .is_none()
    );
    assert!(
        services
            .get_service_by_id("compiled.changed")
            .unwrap()
            .is_some()
    );
    std::fs::remove_file(&path).unwrap();
    scanner.index_all(&cancel).unwrap();
    assert!(
        services
            .get_service_by_id("compiled.changed")
            .unwrap()
            .is_none()
    );
    assert!(!services.container_catalog().container_exists());
    std::fs::write(
        &path,
        r#"<container><services><service id="compiled.recreated"/></services></container>"#,
    )
    .unwrap();
    scanner.index_files(&cancel, &[path.as_str()]).unwrap();
    assert!(
        services
            .get_service_by_id("compiled.recreated")
            .unwrap()
            .is_some()
    );
    close(store, services, generated, scanner);
}

#[test]
fn service_index_includes_compiled_container_parameters() {
    let root_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path().to_str().unwrap();
    let cache = format!("{root}/var/cache/dev-test");
    std::fs::create_dir_all(&cache).unwrap();
    let container_path = format!("{cache}/Shopware_Core_KernelDevDebugContainer.xml");
    std::fs::write(
        &container_path,
        r#"<container>
<parameters>
  <parameter key="kernel.project_dir">/project</parameter>
  <parameter key="App.MixedCase">value</parameter>
</parameters>
<services>
  <service id="twig">
    <call method="addGlobal">
      <argument>app</argument>
      <argument type="service" id="App\Twig\AppVariable"/>
    </call>
  </service>
</services>
</container>"#,
    )
    .unwrap();
    let config = tempfile::tempdir().unwrap();
    let index = ServiceIndex::new(root, config.path(), None).unwrap();
    index.reload_compiled_container().unwrap();
    let names: Vec<String> = index
        .get_all_parameters()
        .unwrap()
        .into_iter()
        .map(|parameter| parameter.name)
        .collect();
    assert!(names.contains(&"kernel.project_dir".to_owned()));
    assert!(names.contains(&"App.MixedCase".to_owned()));
    let parameter = index
        .get_parameter_by_name("App.MixedCase")
        .unwrap()
        .unwrap();
    assert_eq!(parameter.value, "value");
    let globals = index.get_twig_globals();
    assert_eq!(globals.len(), 1);
    assert_eq!(globals[0].name, "app");
    assert_eq!(globals[0].service_id, r"App\Twig\AppVariable");
    assert_eq!(globals[0].path, container_path);
    index.close().unwrap();
}

#[test]
fn compiled_container_path_selection() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path().to_str().unwrap().to_owned();
    let store = Store::open(cache_dir.path().join("indexes.db")).unwrap();
    let services = ServiceIndex::new(&root, cache_dir.path(), Some(&store)).unwrap();
    let generated = services
        .compiled_container_index(cache_dir.path(), Some(&store))
        .unwrap();
    let container = "Shopware_Core_KernelDevDebugContainer.xml";
    for (path, expected) in [
        (format!("{root}/var/cache/dev/{container}"), true),
        (format!("{root}/app/cache/prod/{container}"), true),
        (format!("{root}//var/cache/./dev/{container}"), true),
        (format!("{root}/var/cache/{container}"), false),
        (format!("{root}/var/cache/dev/pools/{container}"), false),
        (format!("{root}/var/cache/dev/Other.xml"), false),
        (format!("{root}/other/cache/dev/{container}"), false),
    ] {
        assert_eq!(generated.should_index_path(&path), expected, "{path}");
    }
    for (path, expected) in [
        (format!("{root}/var"), true),
        (format!("{root}/app/cache"), true),
        (format!("{root}/var/cache/dev/"), true),
        (format!("{root}/var/log"), false),
    ] {
        assert_eq!(generated.should_enter_directory(&path), expected, "{path}");
    }
    generated.close().unwrap();
    services.close().unwrap();
    store.close().unwrap();
}
