use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_symfony::RouteIndexer;

use crate::services::RouteRedefinedAnalysis;
use crate::test_support::check_problems;
use crate::{Project, ProjectAnalysis};

const CONTROLLER: &str = r"<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
class ProductController {
    #[Route('/products', name: 'product.list')]
    public function list(): void {}
}
";

fn routes(dir: &tempfile::TempDir) -> Arc<RouteIndexer> {
    let routes = Arc::new(RouteIndexer::new(dir.path().join("routes"), None).unwrap());
    for (path, source) in [
        ("/project/src/Controller/ProductController.php", CONTROLLER),
        (
            "/project/config/routes.yaml",
            "product.list:\n  path: /all-products\n  controller: App\\Other::list\nproduct.show:\n  path: /products/{id}\n  controller: App\\Other::show\n",
        ),
        (
            "/project/vendor/acme/bundle/config/routes.yaml",
            "product.show:\n  path: /show/{id}\n  controller: Acme\\Show\n",
        ),
        (
            "/project/tests/Fixtures/routes.yaml",
            "product.show:\n  path: /fixture/{id}\n  controller: Tests\\Show\n",
        ),
    ] {
        routes
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    routes
}

#[test]
fn reports_route_names_defined_in_other_project_files() {
    let dir = tempfile::tempdir().unwrap();
    let analyzer = Project::new(RouteRedefinedAnalysis::new(Some(routes(&dir))));
    let (_, problems) = check_problems(
        &analyzer,
        "file:///project/config/routes.yaml",
        "{|symfony.route.redefined:product.list|}:\n  path: /all-products\n  controller: App\\Other::list\nproduct.show:\n  path: /products/{id}\n  controller: App\\Other::show\n",
    );
    assert_eq!(
        problems[0].message,
        "Route name 'product.list' is also defined in ProductController.php:5; the definition loaded last replaces the other"
    );
    let related = &problems[0].related_information[0];
    assert_eq!(
        related.location.uri,
        "file:///project/src/Controller/ProductController.php"
    );
    assert_eq!(related.location.range.start.line, 4);

    check_problems(
        &analyzer,
        "file:///project/src/Controller/ProductController.php",
        &CONTROLLER.replace(
            "'product.list'",
            "'{|symfony.route.redefined:product.list|}'",
        ),
    );
}

#[test]
fn vendor_and_test_definitions_neither_count_nor_get_reported() {
    let dir = tempfile::tempdir().unwrap();
    let analyzer = Project::new(RouteRedefinedAnalysis::new(Some(routes(&dir))));
    for uri in [
        "file:///project/vendor/acme/bundle/config/routes.yaml",
        "file:///project/tests/Fixtures/routes.yaml",
    ] {
        check_problems(
            &analyzer,
            uri,
            "product.show:\n  path: /show/{id}\n  controller: Acme\\Show\n",
        );
    }
}

#[test]
fn unsaved_definitions_of_the_document_itself_are_checked() {
    let dir = tempfile::tempdir().unwrap();
    let analyzer = Project::new(RouteRedefinedAnalysis::new(Some(routes(&dir))));
    // The open file renamed its route; the index still has the old name.
    check_problems(
        &analyzer,
        "file:///project/src/Controller/ProductController.php",
        &CONTROLLER.replace("'product.list'", "'product.index'"),
    );
}

#[test]
fn summary_follows_route_changes() {
    let dir = tempfile::tempdir().unwrap();
    let routes = routes(&dir);
    let analyzer = Project::new(RouteRedefinedAnalysis::new(Some(routes.clone())));
    let before = analyzer.analysis().revision();
    routes
        .index(&ParsedFile::new(
            "/project/config/routes.yaml",
            b"product.index:\n  path: /all-products\n  controller: App\\Other::list\n".to_vec(),
        ))
        .unwrap();
    assert_ne!(analyzer.analysis().revision(), before);
    check_problems(
        &analyzer,
        "file:///project/src/Controller/ProductController.php",
        CONTROLLER,
    );
}
