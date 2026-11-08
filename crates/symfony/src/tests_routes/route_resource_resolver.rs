use std::path::Path;

use swlsp_indexer::{CancelToken, FileScanner, Indexer, ParsedFile};

use super::*;

fn index_bundle_fixture(php: &PhpIndex, root: &str, namespace: &str, name: &str) {
    let interface_path = format!("{}/BundleInterface.php", dir_path(root));
    let interface_source =
        "<?php\nnamespace Symfony\\Component\\HttpKernel\\Bundle;\ninterface BundleInterface {}\n";
    std::fs::create_dir_all(dir_path(&interface_path)).unwrap();
    std::fs::write(&interface_path, interface_source).unwrap();
    php.index(&ParsedFile::new(
        interface_path,
        interface_source.as_bytes().to_vec(),
    ))
    .unwrap();
    let bundle_path = format!("{root}/{name}.php");
    let bundle_source = format!(
        "<?php\nnamespace {namespace};\nfinal class {name} implements \\Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface {{}}\n"
    );
    std::fs::create_dir_all(root).unwrap();
    std::fs::write(&bundle_path, &bundle_source).unwrap();
    php.index(&ParsedFile::new(bundle_path, bundle_source.into_bytes()))
        .unwrap();
}

fn write(path: &str, source: &str) {
    std::fs::create_dir_all(Path::new(path).parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

fn reference(path: &str, loader: &str) -> RouteResourceReference {
    RouteResourceReference {
        path: path.into(),
        loader: loader.into(),
        ..RouteResourceReference::default()
    }
}

#[test]
fn resolves_legacy_bundle_resources() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().to_str().unwrap();
    let php = PhpIndex::open(format!("{root}/.cache"), None).unwrap();
    let bundle_root = format!("{root}/vendor/acme/foo-bundle");
    index_bundle_fixture(&php, &bundle_root, r"Acme\Foo", "FooBundle");
    let route_path = format!("{bundle_root}/Resources/config/routes.xml");
    let controller_path = format!("{bundle_root}/Controller/CatalogController.php");
    write(&route_path, "<routes/>");
    write(&controller_path, "<?php final class CatalogController {}");

    let scanner_dir = tempfile::tempdir().unwrap();
    let scanner =
        Arc::new(FileScanner::new(root, scanner_dir.path().join("scanner.db"), None).unwrap());
    scanner.index_all(&CancelToken::new()).unwrap();
    let resolver = RouteResourceResolver::new(Some(php.clone()), Some(scanner.clone()));
    assert_eq!(
        resolver.files(
            "/project/config/routes.xml",
            &reference(r"@FooBundle\\Resources//config/routes.xml", "")
        ),
        vec![route_path.clone()]
    );
    assert_eq!(
        resolver.files(
            "/project/config/routes.yaml",
            &reference("@FooBundle/Controller/", "attribute")
        ),
        vec![controller_path.clone()]
    );
    assert!(resolver.matches(
        "/project/config/routes.yaml",
        &controller_path,
        &reference("@FooBundle/Controller/**/*.php", "attribute"),
    ));
    let candidates = resolver.bundle_resource_candidates(&CancelToken::new());
    assert!(candidates.contains(&BundleResourceCandidate {
        value: "@FooBundle/Resources/config/routes.xml".into(),
        path: route_path,
    }));
    assert!(candidates.contains(&BundleResourceCandidate {
        value: "@FooBundle/Controller/CatalogController.php".into(),
        path: controller_path,
    }));
    scanner.close().unwrap();
    php.close().unwrap();
}

#[test]
fn refreshes_bundle_catalog_by_php_revision() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().to_str().unwrap();
    let php = PhpIndex::open(format!("{root}/.cache"), None).unwrap();
    let first_root = format!("{root}/first");
    index_bundle_fixture(&php, &first_root, "First", "SharedBundle");
    let first_route = format!("{first_root}/routes.xml");
    write(&first_route, "<routes/>");
    let resolver = RouteResourceResolver::new(Some(php.clone()), None);
    let shared = reference("@SharedBundle/routes.xml", "");
    assert_eq!(
        resolver.files("/project/config/routes.xml", &shared),
        vec![first_route.clone()]
    );

    let second_root = format!("{root}/second");
    index_bundle_fixture(&php, &second_root, "Second", "SharedBundle");
    let second_route = format!("{second_root}/routes.xml");
    write(&second_route, "<routes/>");
    assert_eq!(
        resolver.files("/project/config/routes.xml", &shared),
        vec![first_route, second_route]
    );
    php.close().unwrap();
}

#[test]
fn route_bundle_resource_parts_rejects_incomplete_names() {
    for resource in ["", "FooBundle/routes.xml", "@FooBundle", "@/routes.xml"] {
        assert!(
            route_bundle_resource_parts(resource).is_none(),
            "{resource}"
        );
    }
    assert_eq!(
        route_bundle_resource_parts(r"@FooBundle///Resources\config/routes.xml"),
        Some((
            "FooBundle".to_owned(),
            "Resources/config/routes.xml".to_owned()
        ))
    );
}
