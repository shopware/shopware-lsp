use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;
use crate::RouteIndexer;

struct Fixture {
    name: &'static str,
    path: &'static str,
    source: &'static str,
    marker: &'static str,
    expected: Option<(&'static str, &'static str, &'static str, bool)>,
}

#[test]
fn route_resource_reference_at_cases() {
    let fixtures = [
        Fixture {
            name: "scalar YAML attribute resource",
            path: "/project/config/routes.yaml",
            source: "controllers:\n  resource: ../src/Controller/\n  type: attribute\n",
            marker: "../src",
            expected: Some(("../src/Controller/", "attribute", "", false)),
        },
        Fixture {
            name: "nested YAML resource",
            path: "/project/config/routes.yaml",
            source: "controllers:\n  resource:\n    path: ../src/Controller/\n    namespace: App\\Controller\n  type: attribute\n",
            marker: "../src",
            expected: Some(("../src/Controller/", "attribute", r"App\Controller", true)),
        },
        Fixture {
            name: "YAML route file import",
            path: "/project/config/routes.yaml",
            source: "catalog:\n  resource: routes/catalog.yaml\n",
            marker: "catalog.yaml",
            expected: Some(("routes/catalog.yaml", "", "", false)),
        },
        Fixture {
            name: "YAML service prototype",
            path: "/project/config/services.yaml",
            source: "services:\n  App\\:\n    resource: ../src/\n",
            marker: "../src",
            expected: None,
        },
        Fixture {
            name: "XML attribute resource",
            path: "/project/config/routes.xml",
            source: r#"<routes><import resource="../src/Controller/" type="attribute"/></routes>"#,
            marker: "../src",
            expected: Some(("../src/Controller/", "attribute", "", false)),
        },
        Fixture {
            name: "PHP configurator resource",
            path: "/project/config/routes.php",
            source: r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('../src/Controller/', 'attribute');
};",
            marker: "../src",
            expected: Some(("../src/Controller/", "attribute", "", false)),
        },
        Fixture {
            name: "unrelated PHP import method",
            path: "/project/config/routes.php",
            source: r"<?php
return static function (object $loader): void {
    $loader->import('../src/Controller/', 'attribute');
};",
            marker: "../src",
            expected: None,
        },
    ];
    for fixture in fixtures {
        let file = ParsedFile::new(fixture.path, fixture.source.as_bytes().to_vec());
        let offset = fixture.source.find(fixture.marker).unwrap() + 2;
        let node = file
            .syntax_tree()
            .unwrap()
            .root()
            .node_at(TextSize::from(offset as u32))
            .unwrap();
        let reference = route_resource_reference_at(&node);
        let Some((path, loader, namespace, nested)) = fixture.expected else {
            assert!(reference.is_none(), "{}", fixture.name);
            continue;
        };
        let reference = reference.unwrap_or_else(|| panic!("{}", fixture.name));
        assert_eq!(reference.path, path, "{}", fixture.name);
        assert_eq!(reference.loader, loader, "{}", fixture.name);
        assert_eq!(reference.namespace, namespace, "{}", fixture.name);
        assert_eq!(reference.nested, nested, "{}", fixture.name);
        let range = reference.range;
        assert_eq!(
            &fixture.source[usize::from(range.start())..usize::from(range.end())],
            path,
            "{}",
            fixture.name
        );
    }
}

fn reference(path: &str) -> RouteResourceReference {
    RouteResourceReference {
        path: path.into(),
        loader: "attribute".into(),
        ..RouteResourceReference::default()
    }
}

#[test]
fn route_resource_files_resolve_directories_and_recursive_globs() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().to_str().unwrap();
    let config_path = format!("{root}/config/routes.yaml");
    let controller_dir = format!("{root}/src");
    for (path, content) in [
        ("Controller/CatalogController.php", "<?php"),
        ("Api/AdminController.php", "<?php"),
        ("Controller/Helper.php", "<?php"),
        ("Controller/notes.txt", "text"),
    ] {
        let path = format!("{controller_dir}/{path}");
        std::fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
        std::fs::write(&path, content).unwrap();
    }
    let directory_files = route_resource_files(&config_path, &reference("../src/"));
    assert_eq!(directory_files.len(), 3);
    assert_eq!(
        directory_files[0],
        format!("{controller_dir}/Api/AdminController.php")
    );
    let glob_files = route_resource_files(
        &config_path,
        &reference("../src/{Controller,Api}/**/*Controller.php"),
    );
    assert_eq!(
        glob_files,
        vec![
            format!("{controller_dir}/Api/AdminController.php"),
            format!("{controller_dir}/Controller/CatalogController.php"),
        ]
    );
}

#[test]
fn route_indexer_persists_and_replaces_resource_imports() {
    let cache = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let config_path = format!("{}/routes.yaml", config.path().to_str().unwrap());
    let source = "controllers:\n  resource: ../src/Controller/\n  type: attribute\n";
    let index = RouteIndexer::new(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            config_path.clone(),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    let imports = index.get_route_resource_imports().unwrap();
    assert_eq!(imports.len(), 1);
    assert_eq!(imports[0].path, "../src/Controller/");
    assert_eq!(imports[0].file_path, config_path);
    index.close().unwrap();

    let restored = RouteIndexer::new(cache.path(), None).unwrap();
    let imports = restored.get_route_resource_imports().unwrap();
    assert_eq!(imports.len(), 1);
    assert_eq!(imports[0].loader, "attribute");
    restored
        .index(&ParsedFile::new(
            config_path,
            b"catalog:\n  path: /catalog\n".to_vec(),
        ))
        .unwrap();
    assert!(restored.get_route_resource_imports().unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn route_resource_matches_without_directory_enumeration() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().to_str().unwrap();
    let config_path = format!("{root}/config/routes.yaml");
    let controller = format!("{root}/src/Api/CatalogController.php");
    std::fs::create_dir_all(Path::new(&controller).parent().unwrap()).unwrap();
    std::fs::write(&controller, "<?php").unwrap();
    assert!(route_resource_matches(
        &config_path,
        &controller,
        &reference("../src/")
    ));
    assert!(route_resource_matches(
        &config_path,
        &controller,
        &reference("../src/**/Catalog*.php")
    ));
    assert!(!route_resource_matches(
        &config_path,
        &format!("{root}/other/CatalogController.php"),
        &reference("../src/"),
    ));
}
